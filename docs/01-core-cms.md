# Core CMS & Data Model

> Owner: Core CMS subsystem. Depends on [`00-architecture-overview.md`](00-architecture-overview.md). Adjacent docs: [`02-plugin-system.md`](02-plugin-system.md) (plugins are consumers of the extension points defined here), [`04-block-editor.md`](04-block-editor.md) (the block tree lives in `posts.content_blocks` defined below), [`06-auth-permissions.md`](06-auth-permissions.md) (users/roles referenced in FKs).

This document specifies the **content layer**: what a "post" is, how taxonomies work, how custom fields are stored without recreating WordPress's EAV nightmare, how revisions/comments/permalinks behave, and the concrete SQL DDL. It assumes Postgres 15+ and Go 1.22+ on the server side.

The guiding principle: **WordPress's data model is brilliant in intent and miserable in execution.** Almost everything that hurts to maintain in WordPress is downstream of two decisions made in 2003: one giant `wp_posts` table for every content type, and EAV (`wp_postmeta`) for everything that didn't fit. We adopt the conceptual model wholesale (it's the right abstraction for users) and reject the storage shape entirely.

---

## 1. Content Types

### 1.1 The WordPress baseline (and what's wrong with it)

In WordPress, **every piece of content lives in `wp_posts`**: posts, pages, attachments, revisions, nav menu items, Custom Post Types (CPTs), and increasingly bizarre things plugins shove in there (Woo orders, ACF field groups, block patterns). The discriminator is `post_type VARCHAR(20)`. The schema is the union of every column any post type might need: `post_content` (longtext), `post_title`, `post_excerpt`, `post_status`, `post_parent`, `menu_order`, `comment_status`, etc.

What this buys you:
- One query path. Any plugin querying "posts" works for any type.
- Trivial polymorphism: relationships to "content" are just `bigint UNSIGNED` FKs.
- One revision table, one autosave path, one trash flow.

What it costs you:
- The table is gigantic and hot. On a busy site it's the #1 contention point. CPT writes block category-archive reads.
- The columns are a lowest-common-denominator. `post_content` is `longtext` because *some* type needs it, but Woo orders don't, and nav menu items don't, and the wasted I/O is real.
- The schema cannot enforce per-type invariants. There is no way to say "an event must have a `start_date`" in the database. That validation lives in PHP, gets bypassed by direct SQL, and produces orphan rows.
- Indexing is a compromise. Every index on `wp_posts` serves every type; you can't add a partial index for "events by start_date" without it appearing on every other type's plan.
- EAV (`wp_postmeta`) compounds the problem: every type-specific field becomes a row in a join table, indexed by `meta_key` (a string). Querying "events in May with capacity > 100" is a self-join over `wp_postmeta` twice, and the planner hates it.

### 1.2 The decision: single `posts` table with type discriminator, **plus** typed sidecar tables for hot CPTs

We adopt a **hybrid** model:

1. **`posts` is the canonical row** for every content item. It carries the columns 95% of CPTs need: title, slug, content (block JSON), status, author, timestamps, type. This preserves the WP-style query uniformity and the cross-type relationships (a comment's `post_id` is just an FK to `posts`, period).
2. **CPTs may register a typed sidecar table** when they have a fixed, queryable schema. An `events` CPT gets `event_details (post_id PK FK, start_at, end_at, venue_id, capacity, …)`. Querying joins the sidecar. The post still appears in `posts`.
3. **Plugin-defined or schema-flexible fields go into `posts.meta JSONB`** (see §3), not a separate EAV table.

This is the same trick Stripe used migrating off Mongo and that Linear uses for issues + per-team extensions. You keep the polymorphism where it's useful and pay schema rent only where the access pattern justifies it.

#### Why not table-per-type?

We considered it. Rejected because:
- Every cross-type query (recent activity, sitemap, search index, taxonomy term archives) becomes a `UNION ALL` over N tables. N grows with every installed plugin. Plans get ugly.
- Comments, revisions, terms, permalinks, redirects, audit log, media attachments — all of these have polymorphic FKs to content. Making them work across N tables means either a "what type is this id?" lookup table (which is just the `posts` table rebuilt poorly) or `oid + type` composite keys everywhere.
- The migration story from WordPress lands naturally in a single table.

#### Why not pure single-table?

We considered that too (and it's what WP does). Rejected because the EAV escape hatch always grows past where it's useful. Once `wp_postmeta` is the only place a CPT's data lives, you've lost types, indexes, joins, and constraints. Sidecar tables for CPTs that have a real schema (events, products, orders, listings) preserve everything you'd want from a dedicated table without giving up the polymorphism.

#### Decision table

| Concern | Single table (WP) | Table-per-type | **Hybrid (ours)** |
|---|---|---|---|
| Cross-type queries | Trivial | Painful `UNION ALL` | Trivial (use `posts`) |
| Per-type indexes | Bad (every index on hot table) | Good | **Good** (sidecar) |
| Per-type constraints | None | Good | **Good** (sidecar) |
| Polymorphic FKs (comments etc.) | Trivial | Painful | **Trivial** |
| Schema for plugin-defined types | EAV (`postmeta`) | New table per plugin | **JSONB on `posts`, sidecar opt-in** |
| Hot-row contention | High | Low | **Medium** (sidecar absorbs writes) |
| Familiarity for WP devs | High | Low | **High** |

### 1.3 The `post_type` registry

Post types are **declared**, not inferred. A type lives either in core config (`post`, `page`, `attachment`, `nav_menu_item`, `revision`) or in a plugin/theme manifest. Registration creates a row in the `post_types` table (see DDL) describing:

- `name` — slug, machine ID, e.g. `event`.
- `label_singular`, `label_plural`.
- `supports` — bitmask of capabilities: title, content, excerpt, thumbnail, comments, revisions, author, custom_fields, page_attributes.
- `supports.blocks` — optional allow-list of block-type globs (`["core/*", "my-plugin/pricing-table"]`) that the editor will offer for this post type. The block editor (doc 04 §2.4) queries this when computing "available blocks for post type X". Omitted = all registered blocks are available.
- `taxonomies` — which taxonomies attach (categories, tags, custom).
- `hierarchical` — bool, whether `parent_id` is meaningful (pages: yes; posts: no).
- `public` — bool, whether the front-end renders archive/single pages.
- `rewrite` — permalink pattern (see §7).
- `rest_base` — REST endpoint slug.
- `field_schema` — a JSON Schema describing the typed shape of the type-specific data (used to validate `posts.meta` and to render the editor's "custom fields" UI, see §9).
- `sidecar_table` — optional name of a sidecar table.
- `capability_type` — TEXT. Either an existing family slug like `'post'` (the new CPT reuses the post family's caps) or a fresh prefix like `'book'` (a new capability family is minted). Default: `'post'`.
- `capabilities` — JSONB. Map of action → capability slug. Example for a `book` CPT minting its own family:
  ```json
  {
    "edit":          "edit_books",
    "edit_others":   "edit_others_books",
    "publish":       "publish_books",
    "read":          "read_books",
    "delete":        "delete_books",
    "delete_others": "delete_others_books",
    "edit_private":  "edit_private_books",
    "read_private":  "read_private_books"
  }
  ```

**Capability mapping rule (canonical contract S7 — fixed per review):**

- When `capability_type = 'post'` (the default), the CPT **inherits** the existing post family's capabilities (`edit_posts`, `publish_posts`, etc.). The `capabilities` JSONB may be omitted or left empty.
- When `capability_type = '<new-prefix>'` (e.g., `'book'`), capability slugs are **auto-derived** from the prefix (`edit_<prefix>s`, `publish_<prefix>s`, `delete_others_<prefix>s`, …) using the standard pluralization rule. Any entries explicitly listed in the `capabilities` JSONB **override** the auto-derived slug for that action.
- Capability rows are written to the `capabilities` table (owned by doc 06) on CPT registration; on uninstall they cascade-delete and remove from `role_capabilities` (also owned by doc 06).
- The runtime CHECK ("does this user hold `edit_books`?") is performed by the policy engine in doc 06 §7. The ABI surface that plugins use to register CPTs is owned by doc 02 §3. This doc owns only the schema and the mapping rule.

A registration that names a `sidecar_table` is responsible for shipping a migration that creates it; core enforces the FK shape (`post_id UUID PRIMARY KEY REFERENCES posts(id) ON DELETE CASCADE`). The core CRUD code reads/writes the sidecar through a registered Go interface (`SidecarStore`) supplied by the plugin/theme bundle; for WASM plugins, the host bridges this through the plugin ABI (see plugin system doc).

```go
// Go type for the in-memory registry (core, not WASM-side)
type PostType struct {
    Name           string
    LabelSingular  string
    LabelPlural    string
    Supports       SupportFlags // bitfield
    Taxonomies     []string
    Hierarchical   bool
    Public         bool
    Rewrite        RewriteRule
    RESTBase       string
    FieldSchema    json.RawMessage // JSON Schema
    SidecarStore   SidecarStore    // nil if no sidecar
    Capabilities   CapabilityMap   // which roles can edit/publish/delete
}

type SupportFlags uint32
const (
    SupportTitle SupportFlags = 1 << iota
    SupportContent
    SupportExcerpt
    SupportThumbnail
    SupportComments
    SupportRevisions
    SupportAuthor
    SupportCustomFields
    SupportPageAttrs
)

type SidecarStore interface {
    Insert(ctx context.Context, postID uuid.UUID, data json.RawMessage) error
    Update(ctx context.Context, postID uuid.UUID, data json.RawMessage) error
    Fetch(ctx context.Context, postID uuid.UUID) (json.RawMessage, error)
    Delete(ctx context.Context, postID uuid.UUID) error
    // Query is type-specific; sidecar exposes its own repo for typed queries.
}
```

### 1.4 Built-in types

| `name` | Hierarchical | Public | Notes |
|---|---|---|---|
| `post` | no | yes | The default blog post. Has categories + tags. |
| `page` | yes | yes | Hierarchical, no taxonomies by default. |
| `attachment` | no | yes (file URL) | Media; `posts.content_blocks` is empty, metadata in `attachments` sidecar. |
| `revision` | no (parent FK) | no | See §4. Stored as posts with `status = 'revision'` and `parent_id` set. |
| `nav_menu_item` | yes | no | Menu items; sidecar `nav_menu_items` carries URL/target/menu_id. |
| `block_pattern` | no | no | Reusable block compositions, see block editor doc. |
| `template` | no | no | FSE templates (block tree), see theme doc. |
| `template_part` | no | no | FSE template parts. |

---

## 2. Taxonomies

### 2.1 Model

A **taxonomy** is a named set of **terms**, attachable to specific post types. WordPress's term model is correct in shape (`terms`, `term_taxonomy`, `term_relationships`) but overly normalized for the wrong reason — splitting term identity from taxonomy-scoped term info was useful when terms could be shared across taxonomies, but in practice nobody does this and it just doubles the join cost.

We collapse it: **one `terms` table, taxonomy is a column on the term**. Term identity is per-taxonomy.

```
taxonomies (registry, like post_types)
   id, name, label_singular, label_plural, hierarchical, post_types[]
terms
   id, taxonomy, parent_id (nullable, self FK), name, slug, description, count, meta JSONB
term_relationships
   post_id, term_id, sort_order
```

`taxonomies` is a registry table (also lives in code / plugin manifests, mirrored in DB for foreign keys).

### 2.2 Hierarchical vs flat

Taxonomies declare `hierarchical bool`. Categories are hierarchical (`Tech > Programming > Go`), tags are flat. Hierarchy is stored as a self-referential `parent_id` plus a materialized **`path ltree`** column for fast ancestor/descendant queries.

```sql
-- inside terms table
parent_id UUID REFERENCES terms(id) ON DELETE SET NULL,
path      ltree NOT NULL,  -- e.g. 'tech.programming.go'
```

`ltree` makes "all descendants of Tech" a single index lookup (`path <@ 'tech'`) instead of a recursive CTE. We pay a write-time cost (rebuilding `path` on parent move), worth it for the read pattern.

### 2.3 Term-content relationships

`term_relationships(post_id, term_id, sort_order)` is the join table. Both columns are FKs, the PK is `(post_id, term_id)`. `sort_order` exists because WP plugins consistently re-invent ordering and users want it ("primary category", "featured tag" can be `MIN(sort_order)`).

Indexes:
- `(post_id)` — list terms for a post.
- `(term_id, post_id)` — list posts in a term (the archive query). Note column order: term_id first for the term-archive lookup.

The term-archive query (the hottest read in most blogs) becomes:

```sql
SELECT p.*
FROM posts p
JOIN term_relationships tr ON tr.post_id = p.id
WHERE tr.term_id = $1
  AND p.status = 'published'
  AND p.type = 'post'
ORDER BY p.published_at DESC
LIMIT 20 OFFSET $2;
```

With the right indexes (`(term_id, post_id)` on `term_relationships`, and a partial index `posts(published_at DESC) WHERE status='published' AND type='post'`) the planner uses an index nested loop and we're done.

### 2.4 Counts

`terms.count` is denormalized for archive-listing performance. Updated by triggers on `term_relationships` and on `posts.status` change (a draft post does not count against its term). Triggers are written in plain SQL; the cost is paid on write, and write-on-publish is already a heavy operation (cache purge, ping, RSS rebuild).

---

## 3. Metadata: the EAV exorcism

This is the single biggest schema decision in the document. Get it wrong and we ship a WordPress with a new coat of paint.

### 3.1 What WP's `*_meta` tables are

`wp_postmeta(meta_id, post_id, meta_key VARCHAR(255), meta_value LONGTEXT)`. Classic EAV. Every "custom field" — SEO title, ACF field group value, Yoast settings, plugin state — is a row. A page with 60 ACF fields has 60 rows in `wp_postmeta`. Querying "all posts where meta_key='_event_start' AND CAST(meta_value AS DATE) > NOW()" is a full scan over a multi-million-row table, and the planner can't help because `meta_value` is `LONGTEXT`.

Plugins routinely query meta by string key, with LIKE patterns, with type coercion. The result is the #2 hot table after `wp_posts` and a constant source of incident pages on managed WP hosts.

### 3.2 Our model

Three tiers, in order of preference:

| Tier | Storage | When to use |
|---|---|---|
| **1. Typed columns** on `posts` | Columns like `seo_title TEXT`, `featured_image_id UUID` | Core fields known at schema-design time. Indexed normally. |
| **2. Typed columns on a sidecar table** | `event_details.start_at TIMESTAMPTZ` | Per-CPT fields with known schema and real query needs (range scans, joins). |
| **3. `meta JSONB` on `posts`** | `meta @> '{"yoast": {"focus_keyword": "go"}}'` | Plugin/theme-defined fields, schema-flexible data, low-query fields. |

The default for any new field is **tier 3**. A field gets promoted to tier 2 when:
- It needs a range scan or sort (dates, prices).
- It joins to another table.
- It's queried in the hot path more than ~1% of read traffic.

A field gets promoted to tier 1 when it's in the core feature set (built into the editor UI, used by core templates).

### 3.3 The JSONB schema

`posts.meta JSONB NOT NULL DEFAULT '{}'::jsonb`. We use **namespaced top-level keys**:

```json
{
  "core": {
    "seo": { "title": "...", "description": "...", "noindex": false },
    "social": { "og_image_id": "...", "twitter_card": "summary_large_image" }
  },
  "yoast-seo": { "focus_keyword": "go", "readability": 78 },
  "events": { "start_at": "2026-06-12T18:00:00Z", "capacity": 200 }
}
```

Every key at the top level is **owned** by a namespace (a plugin slug, a theme slug, or `core`). The plugin registry enforces this — a plugin declares its `meta_namespace` and can only read/write keys under it (with capability `meta:read:*` or `meta:write:own`; see plugin doc). This prevents the WP problem of two plugins fighting over `_seo_title`.

### 3.4 Indexing JSONB

Naive: `CREATE INDEX ON posts USING gin (meta);` — works for `@>` containment queries on any path, but the index is huge and writes are slow.

Smart: index **specific paths** known to be queried:

```sql
-- For "events starting in the next week"
CREATE INDEX posts_events_start_at_idx
  ON posts ((meta -> 'events' ->> 'start_at'))
  WHERE type = 'event';

-- For free-text search over SEO title (rare; usually you'd promote this to a column)
CREATE INDEX posts_seo_title_trgm
  ON posts USING gin ((meta #>> '{core,seo,title}') gin_trgm_ops);

-- For "any post tagged with this plugin's flag"
CREATE INDEX posts_yoast_focus_kw
  ON posts USING gin ((meta -> 'yoast-seo' -> 'focus_keyword'));
```

The post-type registry's `field_schema` lets the admin **suggest** indexes when a plugin declares a field as `queryable: true`. We do **not** auto-create indexes (sysadmins hate surprises), but we surface them in the admin's "Performance" panel with one-click apply.

### 3.5 The plugin-extensible meta API

Plugins read/write via a typed API rather than raw JSONB paths:

```go
// Host side
type MetaStore interface {
    Get(ctx context.Context, postID uuid.UUID, ns, key string) (json.RawMessage, error)
    Set(ctx context.Context, postID uuid.UUID, ns, key string, val json.RawMessage) error
    Delete(ctx context.Context, postID uuid.UUID, ns, key string) error
    Query(ctx context.Context, ns string, pred MetaPredicate) ([]uuid.UUID, error)
}
```

`Query` accepts a constrained predicate language (eq, gt/lt, exists, in) that compiles to a planner-friendly `WHERE meta -> ns ->> 'k' OP $1` expression. We do not allow arbitrary JSON path expressions from plugins. This is both a safety boundary (plugin can't construct a query plan that scans 10M rows) and a clarity boundary (the queries a plugin can express are auditable).

### 3.6 User meta and term meta

Same pattern:
- `users.meta JSONB` for user-level extension data.
- `terms.meta JSONB` for term-level (term descriptions are a column; meta is for plugin data).

We do **not** create separate `usermeta` and `termmeta` tables. There is no use case where the EAV shape pays for itself.

---

## 4. Revisions & Autosave

### 4.1 Storage shape

WordPress stores revisions as rows in `wp_posts` with `post_type = 'revision'` and `post_parent` pointing at the live post. This is fine in principle (single revisions table, one query path) but the row-bloat is significant: every save makes a full copy of `post_content` even if only the title changed.

We store revisions in a **separate `post_revisions` table** with content stored as a **JSONB delta when small enough, full content otherwise**:

```sql
CREATE TABLE post_revisions (
    id              UUID PRIMARY KEY DEFAULT gen_uuid_v7(),
    post_id         UUID NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    author_id       UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    kind            revision_kind NOT NULL, -- 'autosave' | 'manual' | 'publish'
    -- exactly one of these is non-null
    snapshot        JSONB,  -- full snapshot of editable fields
    delta_from      UUID REFERENCES post_revisions(id),
    delta           JSONB,  -- RFC 6902 JSON Patch from delta_from
    title           TEXT,   -- denormalized for the revisions list UI
    comment         TEXT    -- optional human note ("renamed section")
);

CREATE INDEX ON post_revisions(post_id, created_at DESC);
CREATE INDEX ON post_revisions(post_id, kind, created_at DESC);
```

Reconstructing a revision walks deltas back to the nearest snapshot. We force a full snapshot every 20 revisions or every 24h, whichever comes first, to bound reconstruction cost.

For most editors this is a 5–10× space win over WP's approach. For block-heavy pages where a single edit touches 1 KB of a 200 KB tree, it's 50×.

### 4.2 Autosave

- Autosave fires every 10s (debounced) while the editor is dirty.
- Autosaves write to `post_revisions` with `kind='autosave'`.
- Each user has **one** active autosave per post — UPSERT on `(post_id, author_id, kind='autosave')` semantically (in practice we keep the most recent and discard older with the same key).
- On manual save or publish, a `manual` or `publish` revision is created and the matching autosave is deleted.

### 4.3 Retention

Default retention policy (overridable via site setting):

| `kind` | Keep |
|---|---|
| `autosave` | Latest only per (post, user). |
| `manual` | Last 30 per post. |
| `publish` | Last 10 per post. |
| Any kind | Keep all from the last 7 days. |

A nightly Asynq job prunes. Snapshots that are still referenced by un-pruned deltas are retained even if older — we run an actual reachability sweep, not a naive `DELETE WHERE created_at < ...`.

### 4.4 Restoration

Restoring a revision creates a **new** revision (with `kind='manual'`, comment `"Restored from revision X"`) rather than rewriting history. Audit trail stays intact.

---

## 5. Content States

### 5.1 The states

| State | Visible to public | Listed in admin | Notes |
|---|---|---|---|
| `draft` | No | Yes (Drafts) | Editor working state. |
| `pending` | No | Yes (Pending Review) | Contributor submitted, awaiting editor. |
| `scheduled` | No (until `publish_at`) | Yes (Scheduled) | Will auto-transition to `published` at `publish_at`. |
| `published` | Yes | Yes (All Posts) | Public. |
| `private` | Logged-in with capability | Yes | Visible to authors/editors only. |
| `trash` | No | Yes (Trash) | Soft-deleted. Auto-purged after 30 days. |
| `revision` | n/a | Tab in editor | Handled in `post_revisions`, not in `posts.status` (revisions live in their own table). |
| `auto-draft` | No | No | Editor created an empty draft on "New Post" click; GC'd after 24h if untouched. |

We intentionally **drop** WP's `inherit` status (it existed for attachments, and we model that differently) and `future` (replaced by `scheduled` + `publish_at`).

### 5.2 State machine

```
                  ┌──────────────┐
   user clicks    │              │  abandoned (>24h)
   "New Post" ──▶ │  auto-draft  │────────────────────▶ (deleted)
                  │              │
                  └──────┬───────┘
                         │ user types anything
                         ▼
                  ┌──────────────┐
                  │              │◀──────────────┐
                  │    draft     │               │
                  │              │               │ reject
                  └──┬────────┬──┘               │
       submit review │        │ schedule         │
                     ▼        ▼                  │
              ┌──────────┐  ┌──────────┐         │
              │ pending  │  │scheduled │         │
              └────┬─────┘  └────┬─────┘         │
                   │             │ time elapses  │
              approve            ▼               │
                   │      ┌──────────────┐       │
                   └─────▶│              │       │
                          │  published   │───────┘ (unpublish)
                          │              │
                          └──────┬───────┘
                                 │ make private
                                 ▼
                          ┌──────────────┐
                          │   private    │
                          └──────┬───────┘
                                 │
                          ┌──────▼───────┐         purge (30d)
                          │    trash     │────────────────▶ (deleted)
                          └──────────────┘
                                 ▲
            (any state) ─────────┘ user trashes
```

### 5.3 Implementation

Transitions are validated in Go, not in the DB. The DB enforces only the enum and the FK invariants. We considered a check constraint with the state graph in SQL; rejected because the validation needs context (current user's capability, whether the post has a `publish_at` set, etc.) and that belongs in app code.

Each transition fires a hook (`post.transitioned`, with `from`, `to`, `post_id`) so plugins can hang behavior off it (notification on submit-for-review, cache purge on publish, RSS rebuild on publish).

The scheduler is an Asynq cron job that scans `WHERE status='scheduled' AND publish_at <= now()` every 30s and transitions matches. We don't trust `pg_cron` for application logic.

---

## 6. Comments

### 6.1 Model

```sql
CREATE TABLE comments (
    id              UUID PRIMARY KEY DEFAULT gen_uuid_v7(),
    post_id         UUID NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    parent_id       UUID REFERENCES comments(id) ON DELETE CASCADE,
    path            ltree NOT NULL,  -- thread path, e.g. '01.04.02'
    author_user_id  UUID REFERENCES users(id) ON DELETE SET NULL,
    author_name     TEXT,    -- denormalized; set for anon comments
    author_email    TEXT,    -- never returned via public API
    author_url      TEXT,
    author_ip       INET,    -- for spam scoring; redacted after 90d
    content         TEXT NOT NULL,
    content_html    TEXT,    -- rendered, sanitized
    status          comment_status NOT NULL DEFAULT 'pending',
    -- 'pending' | 'approved' | 'spam' | 'trash'
    karma           SMALLINT NOT NULL DEFAULT 0,
    user_agent      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    meta            JSONB NOT NULL DEFAULT '{}'::jsonb
);

CREATE INDEX comments_post_status_created_idx
  ON comments (post_id, status, created_at DESC);
CREATE INDEX comments_path_idx ON comments USING gist (path);
CREATE INDEX comments_pending_idx
  ON comments (created_at DESC) WHERE status = 'pending';
```

### 6.2 Threading

`parent_id` is the immediate parent. `path` is the materialized thread path (`ltree`), built from the parent's `path` plus a zero-padded sequence number. Inserting a child to a parent of `path='01.04'` looks at the parent's children, finds the next number (`02`), and writes `path='01.04.02'`. Reading a whole thread in order is `WHERE post_id=$1 ORDER BY path`. Subtree queries (collapse this branch) are `WHERE path <@ '01.04'`.

We cap thread depth at 6 levels by default (site setting). Deeper threads collapse visually but remain stored.

### 6.3 Moderation & spam

Two integration points:
- **Spam scoring** (`pre_comment` filter) — hooks return a score 0–100 and an optional reason. If score ≥ threshold, status is set to `spam` on insert. Akismet-style plugins implement this.
- **Moderation queue** (`comment.transitioned` action) — fires when an admin/editor changes status.

Anti-abuse minimums core ships:
- Honeypot field (a CSS-hidden input named `email_confirm` that bots fill).
- Min time-on-page before submit (humans take ≥3s).
- Per-IP rate limit (10 comments / 10 min) via Redis token bucket.
- Hash of `(email, IP, post_id)` checked against recent submissions to dedupe.

`author_ip` is required for spam scoring but redacted to `/24` (IPv4) or `/64` (IPv6) after 90 days; full IP retention is opt-in for GDPR reasons.

### 6.4 What we don't do

- We don't ship trackbacks/pingbacks. They are 99% spam in 2026.
- We don't ship a built-in comment editor. Content is sanitized markdown-ish (a deliberately limited subset; see security doc when it exists). Plugins can extend.

---

## 7. Slugs & Permalinks

### 7.1 Slug rules

- Slugs are unique **per (type, parent_id)** for hierarchical types, and **per type** for flat types. Enforced by partial unique indexes:
  ```sql
  CREATE UNIQUE INDEX posts_slug_flat_uq
    ON posts (type, slug)
    WHERE parent_id IS NULL AND status <> 'trash';

  CREATE UNIQUE INDEX posts_slug_hier_uq
    ON posts (type, parent_id, slug)
    WHERE parent_id IS NOT NULL AND status <> 'trash';
  ```
  Trashed posts don't block slug reuse, but see §7.4 on redirects.
- Slug generation: lower-case, ASCII-fold (unidecode), spaces → hyphens, strip non-alnum-hyphen, collapse hyphens, trim. Max 200 chars.
- On collision, append `-2`, `-3`, etc. (database-driven, not optimistic).

### 7.2 Permalink structure

Permalinks are templated by post type. The default:

| Type | Pattern | Example |
|---|---|---|
| `post` | `/{year}/{month}/{slug}` | `/2026/05/hello-world` |
| `page` | `/{path}` | `/about/team` (uses ancestor slugs joined by `/`) |
| `attachment` | `/wp-uploads/{year}/{month}/{filename}` (S3-fronted) | n/a |
| custom | declared in `post_types.rewrite` | `/events/{year}/{slug}` |

Token vocabulary: `{slug}`, `{year}`, `{month}`, `{day}`, `{author}`, `{category}` (primary), `{id}`, `{path}` (hierarchical only). Custom tokens registerable via the routes plugin hook.

Patterns must be **prefix-disjoint** across active types — `/{slug}` is rejected at install if another type already claims top-level slugs, because routing becomes ambiguous. We resolve at registration time, not at request time.

### 7.3 Resolution

A single forward-lookup table makes routing fast:

```sql
CREATE TABLE permalinks (
    path        TEXT PRIMARY KEY,        -- normalized, leading slash, no trailing
    post_id     UUID NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    is_current  BOOLEAN NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

`permalinks` is recomputed when slug, parent, type, or `published_at` changes. The recompute is one SQL statement per affected post (using the pattern stored on `post_types.rewrite`). Indexed by PK → O(1) routing lookup; we don't parse patterns at request time.

**Historical and manually-created redirects live in the `redirects` table defined in doc 08 §8.1** (canonical contract S3 — fixed per review), which supports textual `from_path`/`to_path`, a `hits` counter, and multiple `source` values (`slug-change`, `manual`, `migration`, `htaccess`). The middleware checks `permalinks` first for live posts, then falls back to `redirects` for 301s. Older designs of this doc had a parallel `permalink_redirects` table; that has been removed in favor of the single `redirects` table owned by doc 08.

### 7.4 Slug-change history (redirects)

When a slug changes on a published post:
1. The old `permalinks` row is removed (or marked `is_current = FALSE`).
2. A row is written to doc 08's `redirects(from_path = old_path, to_path = new_path, status = 301, source = 'slug-change')`.
3. The new path is inserted into `permalinks`.
4. A `permalink.changed` hook fires (SEO plugins listen to push notifications to indexers).

This means a post that has been renamed three times has three rows in `redirects` (one per change) and one current row in `permalinks`. The same `redirects` table also stores manual redirects (`/old-thing` → `/new-thing`) and any htaccess/migration redirects imported from other CMSes — see doc 08 §8.1 for the full DDL and `source` enum.

Bonus: a permalink resolves in **one** query: `SELECT post_id FROM permalinks WHERE path = $1`. If miss, `SELECT to_path, status FROM redirects WHERE from_path = $1`. No pattern matching in the hot path.

### 7.5 Reserved paths

Core reserves `/wp-admin`, `/api`, `/_next`, `/feed`, `/sitemap.xml`, `/robots.txt`, `/.well-known/*`. Slug generation refuses to mint these; permalink registration refuses to claim them.

---

## 8. Search

V1 is **Postgres FTS**. V2 may move to Meilisearch/Typesense for relevance/typo tolerance, but most sites under 100k posts will be fine on Postgres.

### 8.1 tsvector column with weights

We store a precomputed `tsvector` on `posts` populated by a trigger:

```sql
ALTER TABLE posts ADD COLUMN search_doc tsvector;

CREATE FUNCTION posts_search_doc_update() RETURNS trigger AS $$
BEGIN
  NEW.search_doc :=
       setweight(to_tsvector('english', coalesce(NEW.title, '')), 'A')
    || setweight(to_tsvector('english', coalesce(NEW.excerpt, '')), 'B')
    || setweight(to_tsvector('english', coalesce(NEW.content_text, '')), 'C')
    || setweight(to_tsvector('english',
         coalesce(NEW.meta #>> '{core,seo,description}', '')), 'D');
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER posts_search_doc_trg
  BEFORE INSERT OR UPDATE OF title, excerpt, content_text, meta ON posts
  FOR EACH ROW EXECUTE FUNCTION posts_search_doc_update();

CREATE INDEX posts_search_doc_gin ON posts USING gin (search_doc);
```

Weights: title (A) > excerpt (B) > body (C) > meta description (D). The renderer extracts `content_text` from `content_blocks` (the block tree's plain-text projection) during save; we **don't** rebuild it at index time.

### 8.2 Querying

```sql
SELECT id, title, ts_rank_cd(search_doc, q, 32) AS rank
FROM posts, plainto_tsquery('english', $1) q
WHERE search_doc @@ q
  AND status = 'published'
  AND type IN ('post', 'page')
ORDER BY rank DESC, published_at DESC
LIMIT 20;
```

`32` flag normalizes by rank sum of unique words (Postgres FTS doc). `plainto_tsquery` handles user input safely. For phrase / proximity / prefix, we expose `phrase`, `prefix:`, and negation in the search API and translate to `tsquery` ourselves.

### 8.3 Language

Default is `english`. Sites declare a `default_language` in `options`; the dictionary is selected from that. Multi-language sites (per-post language) get a per-row `search_doc` built with the post's declared language — the trigger reads `posts.language` and picks `english`, `simple`, `french`, etc.

### 8.4 Beyond Postgres

When a site outgrows FTS (relevance complaints, typo tolerance, faceted search at scale), Meilisearch/Typesense plugs in via the **search backend interface** in core:

```go
type SearchBackend interface {
    Index(ctx context.Context, p *Post) error
    Delete(ctx context.Context, id uuid.UUID) error
    Search(ctx context.Context, q Query) (*SearchResult, error)
}
```

Default impl is Postgres; alternative impls (registered by plugins) replace it. Migration is a one-shot reindex job; the API surface to consumers doesn't change.

---

## 9. The "Meta Box" / Custom Fields Problem

This is the single most important UX decision in the doc, because **ACF + Meta Box + Pods + Toolset** represent something like 30% of the WordPress plugin economy and are how every serious WP site is actually built. If we ship a CMS where adding a "subtitle" field to the Page type requires editing Go code, we will have failed.

### 9.1 What ACF does, in one paragraph

ACF lets a non-developer say: "On the Event post type, add fields Start Date (date), Capacity (number), Speakers (repeater of: name text, bio textarea, photo image)." The editor UI then renders those fields below the content editor; saving writes to `wp_postmeta` rows; reading is `get_field('start_date')` in the theme. The schema lives in PHP code or in the database (depending on configuration).

### 9.2 Our approach: JSON Schema-driven field groups

Custom fields are declared as a **field group**: a JSON document conforming to a slightly extended JSON Schema, attached to one or more post types (or to a taxonomy, or to a user role). The schema is the single source of truth for:

1. **Storage** — defines where each field lives (`meta.foo.bar` or a sidecar column).
2. **Editor UI** — the editor reads the schema and renders the appropriate inputs.
3. **Validation** — server enforces the schema on save.
4. **API surface** — REST and GraphQL expose the typed shape based on the schema.
5. **Template access** — theme components call `useField('subtitle')` and TypeScript types are generated from the schema.

A minimal example:

```json
{
  "id": "event-details",
  "title": "Event Details",
  "applies_to": { "post_types": ["event"] },
  "namespace": "events",
  "fields": [
    {
      "key": "start_at",
      "label": "Start time",
      "type": "datetime",
      "required": true,
      "storage": { "kind": "sidecar", "column": "start_at" }
    },
    {
      "key": "capacity",
      "label": "Capacity",
      "type": "integer",
      "min": 0,
      "storage": { "kind": "sidecar", "column": "capacity" }
    },
    {
      "key": "speakers",
      "label": "Speakers",
      "type": "repeater",
      "min_items": 0, "max_items": 50,
      "storage": { "kind": "meta", "path": "events.speakers" },
      "fields": [
        { "key": "name", "type": "string", "required": true },
        { "key": "bio",  "type": "richtext" },
        { "key": "photo_id", "type": "attachment", "accept": "image/*" }
      ]
    },
    {
      "key": "venue_id",
      "label": "Venue",
      "type": "reference",
      "ref_type": "venue",
      "storage": { "kind": "sidecar", "column": "venue_id" }
    }
  ]
}
```

### 9.3 Field types

Core ships with: `string`, `text`, `richtext`, `markdown`, `integer`, `number`, `boolean`, `date`, `datetime`, `time`, `select`, `multiselect`, `attachment`, `gallery`, `reference` (FK to another post type), `taxonomy` (multi-select of terms), `repeater`, `group`, `url`, `email`, `color`, `geo`, `json` (escape hatch).

Plugins can register new field types via a **field type plugin** interface: a server-side validator + a client-side React component. The block editor and the admin field editor pick these up dynamically.

### 9.4 Where the data goes (`storage`)

Each field declares `storage`:

- `{ "kind": "meta", "path": "namespace.key" }` — lives in `posts.meta` JSONB. Default.
- `{ "kind": "sidecar", "column": "start_at" }` — lives in the sidecar table for the post type. The sidecar must be declared at type registration; mismatch is a registration error.
- `{ "kind": "column" }` — lives in a built-in `posts` column (only for core fields).

This lets a CPT author **start in JSONB** (fast iteration, no migrations) and **graduate to sidecar columns** when the field becomes hot, without changing the editor UI or the template code that reads it.

### 9.5 The editor render

The block editor shows a "Document settings" sidebar. Each field group attached to the current post type renders as a panel. Each field renders by `type`. Layout, conditional logic (`show_if`), tabs, validation messages — all declared in the schema.

A field group with **no fields** but a custom UI is also supported (`ui: "component:my-plugin/event-map-picker"`) for the cases where the JSON Schema doesn't reach far enough.

### 9.6 Why JSON Schema and not a Go DSL

- Schemas roundtrip through the admin UI (editors build field groups visually, save the JSON).
- Schemas serialize to TypeScript types via a build step — themes get autocomplete on `post.fields.start_at`.
- Schemas are portable: a plugin can ship a schema file, the admin can clone/edit it.
- Schemas validate without re-running Go code — useful for the API surface and for migration tools.

We accept the cost of building the editor-of-editors (the UI that creates field groups). It's a one-time cost and ACF showed it can be done well.

---

## 10. Database Schema (concrete DDL)

Postgres 15+. **All primary keys are UUID v7 (time-sortable)**, generated by a project-supplied SQL function `gen_uuid_v7()` (thin wrapper over the `pg_uuidv7` extension when present, otherwise a Postgres-side implementation we ship in our base migration). Every table in core, the block editor (doc 04), and media (doc 07) uses `id UUID PRIMARY KEY DEFAULT gen_uuid_v7()`. Every FK is `UUID REFERENCES x(id)`. We do **not** use `BIGSERIAL` for any user-visible entity. All timestamps are `timestamptz`. All FKs have explicit `ON DELETE`. Money/decimals (not used here, but for the record) use `numeric`.

> **Canonical contract (S1 — fixed per review):** `gen_uuid_v7()` is the only PK generator across the schema. Any DDL elsewhere in the design (docs 04, 07, etc.) that still shows `BIGSERIAL` should be read as a typo against this contract.

### 10.1 Extensions

```sql
CREATE EXTENSION IF NOT EXISTS pgcrypto;       -- gen_random_uuid (fallback only)
CREATE EXTENSION IF NOT EXISTS pg_uuidv7;      -- gen_uuid_v7 (preferred PK generator)
CREATE EXTENSION IF NOT EXISTS pg_trgm;        -- trigram, fuzzy search
CREATE EXTENSION IF NOT EXISTS ltree;          -- hierarchy paths
CREATE EXTENSION IF NOT EXISTS citext;         -- case-insensitive emails
CREATE EXTENSION IF NOT EXISTS btree_gin;      -- gin on scalar + jsonb composites
```

If the host Postgres lacks `pg_uuidv7`, our base migration installs a plpgsql `gen_uuid_v7()` function with the same semantics (time-sortable v7 UUID). Every PK column in this doc, doc 04, and doc 07 uses `DEFAULT gen_uuid_v7()`.

### 10.2 Enums

```sql
CREATE TYPE post_status AS ENUM (
  'auto-draft', 'draft', 'pending', 'scheduled', 'published', 'private', 'trash'
);
CREATE TYPE comment_status AS ENUM ('pending', 'approved', 'spam', 'trash');
CREATE TYPE revision_kind  AS ENUM ('autosave', 'manual', 'publish');
CREATE TYPE user_status    AS ENUM ('active', 'suspended', 'deleted');
```

### 10.3 `users`

Full design is in [`06-auth-permissions.md`](06-auth-permissions.md). The minimum surface the CMS needs:

```sql
CREATE TABLE users (
    id              UUID PRIMARY KEY DEFAULT gen_uuid_v7(),
    email           CITEXT NOT NULL UNIQUE,
    username        CITEXT NOT NULL UNIQUE,
    display_name    TEXT NOT NULL,
    password_hash   TEXT,                       -- nullable for SSO-only
    status          user_status NOT NULL DEFAULT 'active',
    locale          TEXT NOT NULL DEFAULT 'en-US',
    timezone        TEXT NOT NULL DEFAULT 'UTC',
    avatar_id       UUID,                       -- FK to posts(id) where type='attachment'
    meta            JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at    TIMESTAMPTZ
);
CREATE INDEX users_meta_gin ON users USING gin (meta jsonb_path_ops);
```

Roles/capabilities live in a separate table set; see auth doc.

### 10.4 `post_types` (registry)

```sql
CREATE TABLE post_types (
    name            TEXT PRIMARY KEY,
    label_singular  TEXT NOT NULL,
    label_plural    TEXT NOT NULL,
    supports        INTEGER NOT NULL DEFAULT 0,  -- bitfield
    -- Block allow-list (NULL = all registered blocks). See §1.3 and doc 04 §2.4.
    supports_blocks TEXT[],
    hierarchical    BOOLEAN NOT NULL DEFAULT FALSE,
    public          BOOLEAN NOT NULL DEFAULT TRUE,
    rewrite_pattern TEXT,                         -- e.g. '/{year}/{month}/{slug}'
    rest_base       TEXT,                         -- e.g. 'posts'
    sidecar_table   TEXT,                         -- nullable
    field_schema    JSONB NOT NULL DEFAULT '{}'::jsonb,
    -- Capability binding (see §1.3, contract S7).
    capability_type TEXT NOT NULL DEFAULT 'post', -- 'post' to inherit, or new prefix
    capabilities    JSONB NOT NULL DEFAULT '{}'::jsonb,
                                                  -- action -> cap slug overrides
    origin          TEXT NOT NULL,                -- 'core' | 'plugin:slug' | 'theme:slug'
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

(Fixed per review: `supports_blocks`, `capability_type`, `capabilities` added to close gap B3 and contract S7.)

### 10.5 `posts`

```sql
CREATE TABLE posts (
    id              UUID PRIMARY KEY DEFAULT gen_uuid_v7(),
    type            TEXT NOT NULL REFERENCES post_types(name),
    status          post_status NOT NULL DEFAULT 'draft',
    title           TEXT NOT NULL DEFAULT '',
    slug            TEXT NOT NULL,
    excerpt         TEXT,
    -- Block tree (see block editor doc). NULL for types that don't use blocks.
    content_blocks  JSONB,
    -- Plain-text projection of content_blocks, for FTS. Maintained by app on save.
    content_text    TEXT,
    -- Rendered HTML cache; nullable, regenerated by the renderer.
    -- (Fixed per review C13/C15: column is `content_rendered`, matching doc 04's name.)
    content_rendered     TEXT,
    content_rendered_at  TIMESTAMPTZ,
    -- Hash of the block tree at the time content_rendered was produced. Used
    -- as the cache key for the pre-render cache (see doc 04 §1.4 / §5.5).
    content_blocks_hash  BYTEA,
    -- Author. Nullable to survive user deletion.
    author_id       UUID REFERENCES users(id) ON DELETE SET NULL,
    -- Hierarchical parent (pages, nav menu items).
    parent_id       UUID REFERENCES posts(id) ON DELETE CASCADE,
    -- Featured image (an attachment post).
    featured_image_id UUID REFERENCES posts(id) ON DELETE SET NULL,
    -- Ordering within a parent for hierarchical types.
    menu_order      INTEGER NOT NULL DEFAULT 0,
    -- Comments toggle (per-post override of type default).
    comments_open   BOOLEAN NOT NULL DEFAULT TRUE,
    -- Plugin/theme metadata.
    meta            JSONB NOT NULL DEFAULT '{}'::jsonb,
    -- Language for FTS dictionary selection.
    language        TEXT NOT NULL DEFAULT 'english',
    -- FTS document (see §8).
    search_doc      tsvector,
    -- Lifecycle.
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at    TIMESTAMPTZ,
    publish_at      TIMESTAMPTZ,                  -- scheduled publish time
    -- Soft delete.
    trashed_at      TIMESTAMPTZ,
    -- Optimistic concurrency.
    version         BIGINT NOT NULL DEFAULT 1
);

-- Slug uniqueness (see §7.1).
CREATE UNIQUE INDEX posts_slug_flat_uq
  ON posts (type, slug)
  WHERE parent_id IS NULL AND status <> 'trash';
CREATE UNIQUE INDEX posts_slug_hier_uq
  ON posts (type, parent_id, slug)
  WHERE parent_id IS NOT NULL AND status <> 'trash';

-- Hot read paths.
CREATE INDEX posts_published_idx
  ON posts (type, published_at DESC)
  WHERE status = 'published';

CREATE INDEX posts_author_idx ON posts (author_id, created_at DESC)
  WHERE status <> 'trash';

CREATE INDEX posts_parent_idx ON posts (parent_id, menu_order)
  WHERE parent_id IS NOT NULL;

CREATE INDEX posts_scheduled_idx ON posts (publish_at)
  WHERE status = 'scheduled';

-- Trash GC sweep.
CREATE INDEX posts_trashed_idx ON posts (trashed_at)
  WHERE status = 'trash';

-- FTS.
CREATE INDEX posts_search_doc_gin ON posts USING gin (search_doc);

-- Generic JSONB containment (small; specific path indexes are added per-need).
CREATE INDEX posts_meta_gin ON posts USING gin (meta jsonb_path_ops);
```

`updated_at` is maintained by trigger. `version` is bumped on every UPDATE (also by trigger) and used in optimistic-concurrency UPDATEs from the API (`WHERE id = $1 AND version = $2`).

### 10.6 `post_revisions`

(See §4.1 for the DDL — repeated here for completeness.)

```sql
CREATE TABLE post_revisions (
    id              UUID PRIMARY KEY DEFAULT gen_uuid_v7(),
    post_id         UUID NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    author_id       UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    kind            revision_kind NOT NULL,
    snapshot        JSONB,
    delta_from      UUID REFERENCES post_revisions(id),
    delta           JSONB,
    title           TEXT,
    comment         TEXT,
    CHECK ((snapshot IS NOT NULL) <> (delta IS NOT NULL))
);

CREATE INDEX post_revisions_post_created_idx
  ON post_revisions(post_id, created_at DESC);
CREATE INDEX post_revisions_kind_idx
  ON post_revisions(post_id, kind, created_at DESC);
```

### 10.7 `taxonomies` & `terms`

```sql
CREATE TABLE taxonomies (
    name             TEXT PRIMARY KEY,
    label_singular   TEXT NOT NULL,
    label_plural     TEXT NOT NULL,
    hierarchical     BOOLEAN NOT NULL DEFAULT FALSE,
    -- which post types this taxonomy applies to
    post_types       TEXT[] NOT NULL DEFAULT '{}',
    rewrite_base     TEXT,    -- e.g. 'category' -> /category/{slug}
    origin           TEXT NOT NULL
);

CREATE TABLE terms (
    id              UUID PRIMARY KEY DEFAULT gen_uuid_v7(),
    taxonomy        TEXT NOT NULL REFERENCES taxonomies(name) ON DELETE RESTRICT,
    parent_id       UUID REFERENCES terms(id) ON DELETE SET NULL,
    name            TEXT NOT NULL,
    slug            TEXT NOT NULL,
    description     TEXT,
    path            ltree NOT NULL,
    count           INTEGER NOT NULL DEFAULT 0,
    meta            JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (taxonomy, slug)
);

CREATE INDEX terms_path_idx ON terms USING gist (path);
CREATE INDEX terms_taxonomy_name_idx ON terms (taxonomy, name);
CREATE INDEX terms_parent_idx ON terms (parent_id) WHERE parent_id IS NOT NULL;
```

### 10.8 `term_relationships`

```sql
CREATE TABLE term_relationships (
    post_id     UUID NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    term_id     UUID NOT NULL REFERENCES terms(id) ON DELETE CASCADE,
    sort_order  INTEGER NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (post_id, term_id)
);

-- Term archive query (term_id leading).
CREATE INDEX term_relationships_term_idx
  ON term_relationships (term_id, sort_order, post_id);
```

### 10.9 `comments`

(See §6.1.)

### 10.10 `permalinks` (forward lookup)

(See §7.3 for the DDL. The historical/manual `redirects` table is owned by [`08-migration-compat.md`](08-migration-compat.md) §8.1 — there is no separate `permalink_redirects` table in this design. Fixed per review.)

### 10.11 `options` (key-value site config)

WordPress's `wp_options` is a global key-value store: site URL, theme, active plugins, transients (cache), and an autoload flag that's often the #1 cause of "WordPress is slow on startup" because thousands of rows get autoloaded into memory on every request.

We keep the concept, fix the autoload pathology:

```sql
CREATE TABLE options (
    key             TEXT PRIMARY KEY,
    value           JSONB NOT NULL,
    autoload        BOOLEAN NOT NULL DEFAULT FALSE,
    namespace       TEXT NOT NULL DEFAULT 'core',  -- 'core' | 'plugin:slug' | etc.
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX options_autoload_idx ON options (key) WHERE autoload = TRUE;
CREATE INDEX options_namespace_idx ON options (namespace);
```

Rules:
- A single Redis hash mirrors `WHERE autoload = TRUE`. App boot loads the hash once; updates publish invalidations.
- Plugins **cannot** mark their options as `autoload` without an admin opt-in (UI panel: "this plugin wants to autoload N options"). This prevents the WP plugin-induced cold-start tax.
- Transients (TTL'd caches) do **not** go here. They live in Redis with TTLs. `wp_options` ended up as a cache backend in WP and it was always the wrong choice.

### 10.12 `sessions`

Full design is in [`06-auth-permissions.md`](06-auth-permissions.md). The CMS only needs to know sessions exist for editor lock features (who's editing this post right now). For completeness:

```sql
CREATE TABLE sessions (
    id              BYTEA PRIMARY KEY,           -- hashed session token
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at      TIMESTAMPTZ NOT NULL,
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    ip              INET,
    user_agent      TEXT,
    revoked_at      TIMESTAMPTZ
);
CREATE INDEX sessions_user_idx ON sessions (user_id) WHERE revoked_at IS NULL;
CREATE INDEX sessions_expires_idx ON sessions (expires_at) WHERE revoked_at IS NULL;
```

Hot-path session lookups go through Redis; Postgres is the durable store and the source of truth for "all my sessions" / "log out everywhere".

### 10.13 Editor locks

```sql
CREATE TABLE post_locks (
    post_id     UUID PRIMARY KEY REFERENCES posts(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    acquired_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL
);
CREATE INDEX post_locks_expires_idx ON post_locks (expires_at);
```

Locks expire after 90s of editor inactivity; the editor heartbeats every 30s. Steal-lock requires a capability check.

### 10.14 Triggers

Two we ship by default:

```sql
-- Maintain updated_at on every UPDATE.
CREATE OR REPLACE FUNCTION touch_updated_at() RETURNS trigger AS $$
BEGIN NEW.updated_at = now(); RETURN NEW; END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER posts_touch BEFORE UPDATE ON posts
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();
-- (same on users, terms, options, comments, …)

-- Maintain version on every UPDATE (optimistic concurrency).
CREATE OR REPLACE FUNCTION bump_version() RETURNS trigger AS $$
BEGIN NEW.version = OLD.version + 1; RETURN NEW; END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER posts_version BEFORE UPDATE ON posts
  FOR EACH ROW EXECUTE FUNCTION bump_version();
```

Term-count maintenance:

```sql
CREATE OR REPLACE FUNCTION recount_terms_on_rel_change() RETURNS trigger AS $$
BEGIN
  IF (TG_OP = 'INSERT') THEN
    UPDATE terms SET count = count + 1 WHERE id = NEW.term_id;
  ELSIF (TG_OP = 'DELETE') THEN
    UPDATE terms SET count = count - 1 WHERE id = OLD.term_id;
  END IF;
  RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER term_rel_count AFTER INSERT OR DELETE ON term_relationships
  FOR EACH ROW EXECUTE FUNCTION recount_terms_on_rel_change();
```

(Recount on post status change is handled in app code, not in a trigger, because we need to inspect old + new status.)

---

## 11. Go-side Models

For grounding. These are not the wire types (those live in the REST/GraphQL layer) but the domain types the store layer returns.

```go
package content

import (
    "time"

    "github.com/google/uuid"
)

type Post struct {
    ID               uuid.UUID
    Type             string
    Status           Status
    Title            string
    Slug             string
    Excerpt          string
    ContentBlocks    json.RawMessage   // block tree
    ContentText      string
    ContentHTML      string
    AuthorID         *uuid.UUID
    ParentID         *uuid.UUID
    FeaturedImageID  *uuid.UUID
    MenuOrder        int
    CommentsOpen     bool
    Meta             json.RawMessage   // tier-3 metadata
    Language         string
    CreatedAt        time.Time
    UpdatedAt        time.Time
    PublishedAt      *time.Time
    PublishAt        *time.Time
    TrashedAt        *time.Time
    Version          int64
}

type Status string
const (
    StatusAutoDraft Status = "auto-draft"
    StatusDraft     Status = "draft"
    StatusPending   Status = "pending"
    StatusScheduled Status = "scheduled"
    StatusPublished Status = "published"
    StatusPrivate   Status = "private"
    StatusTrash     Status = "trash"
)

type Term struct {
    ID          uuid.UUID
    Taxonomy    string
    ParentID    *uuid.UUID
    Name        string
    Slug        string
    Description string
    Path        string // ltree
    Count       int
    Meta        json.RawMessage
}

type Comment struct {
    ID            uuid.UUID
    PostID        uuid.UUID
    ParentID      *uuid.UUID
    Path          string
    AuthorUserID  *uuid.UUID
    AuthorName    string
    AuthorEmail   string  // redacted in API responses
    AuthorURL     string
    Content       string
    ContentHTML   string
    Status        CommentStatus
    Karma         int16
    CreatedAt     time.Time
    UpdatedAt     time.Time
    Meta          json.RawMessage
}

type Repository interface {
    // Posts
    GetPost(ctx context.Context, id uuid.UUID) (*Post, error)
    GetPostBySlug(ctx context.Context, typ, slug string, parentID *uuid.UUID) (*Post, error)
    ListPosts(ctx context.Context, q PostQuery) ([]*Post, int, error)
    CreatePost(ctx context.Context, p *Post) error
    UpdatePost(ctx context.Context, p *Post) error // uses optimistic concurrency on Version
    Transition(ctx context.Context, id uuid.UUID, to Status, by uuid.UUID) error

    // Terms
    GetTerm(ctx context.Context, id uuid.UUID) (*Term, error)
    ListTerms(ctx context.Context, q TermQuery) ([]*Term, error)
    AttachTerms(ctx context.Context, postID uuid.UUID, termIDs []uuid.UUID) error

    // Comments
    InsertComment(ctx context.Context, c *Comment) error
    ListComments(ctx context.Context, q CommentQuery) ([]*Comment, error)

    // Revisions
    SaveRevision(ctx context.Context, r *Revision) error
    ListRevisions(ctx context.Context, postID uuid.UUID) ([]*Revision, error)
    Restore(ctx context.Context, revisionID uuid.UUID, by uuid.UUID) error
}
```

`Repository` is the single boundary between the HTTP/GraphQL layer and the database. We don't expose `*sql.DB` further up the stack. Implementations use `pgx` directly; we evaluated `sqlc` (codegen) and `ent` (ORM), and `pgx` won on flexibility for JSONB/ltree/tsvector and on lack of compile-time codegen weight.

---

## 12. Read & Write Paths (representative)

### 12.1 Publishing a post (state transition)

1. Editor calls `PATCH /api/posts/{id} { status: 'published' }`.
2. HTTP handler resolves capability (`publish_post`), loads `Post`, validates transition (`draft → published` is allowed).
3. Transaction:
   - `UPDATE posts SET status='published', published_at = COALESCE(published_at, now()) WHERE id=$1 AND version=$2`.
   - `INSERT INTO post_revisions (kind='publish', snapshot=…)`.
   - Recompute `permalinks` row (and possibly insert into doc 08's `redirects` table with `source='slug-change'` if the slug changed).
   - `UPDATE terms SET count = count + 1 …` for newly-counting terms (was draft, now public).
4. Fire `post.transitioned` hook → plugins (SEO indexer, cache invalidator, RSS rebuilder).
5. Enqueue background jobs: cache purge, sitemap regen, webhook fanout.
6. Return updated post.

### 12.2 Rendering a public URL

1. Next.js calls `GET /api/render?path=/2026/05/hello-world`.
2. Go handler: `SELECT post_id FROM permalinks WHERE path = $1` → one row.
3. `SELECT * FROM posts WHERE id = $1` → load post; if `attachment` or special type, dispatch.
4. Load terms, author, featured image in parallel (3 queries, all keyed by indexes).
5. Read fragment cache for `(post_id, version)` — if hit, return rendered HTML.
6. Otherwise: render block tree → HTML, store fragment cache, return.

We considered putting block-tree rendering in Next.js exclusively; the API returns the JSON tree and the client renders. We chose to also support server render in Go for headless consumers and for cases where the Next.js render is bypassed (AMP, RSS, email). The renderer is duplicated (Go renderer of the block tree + React renderer of the block tree); we eat that cost because it preserves headless flexibility.

### 12.3 Term archive

1. `GET /category/programming?page=2`.
2. `SELECT id FROM terms WHERE taxonomy='category' AND slug='programming'`.
3. Term archive query (see §2.3) → 20 post IDs.
4. Hydrate posts in a single `IN (…)` query.
5. Cache the archive listing keyed by `(term_id, page, post_count_at)` — invalidated when `terms.count` changes.

---

## 13. Trade-offs & Rejected Alternatives

### 13.1 Rejected: pure EAV (`postmeta` table)

What WP does. Rejected for the reasons in §3.1: type-blind, slow joins, no constraints, hot table. Even with a `postmeta(post_id, key, value JSONB)` flavor (where `value` is typed), the cost of the join compared to JSONB containment on the same row is not worth the marginal flexibility.

### 13.2 Rejected: separate table per post type

Discussed in §1.2. Cross-cutting concerns (comments, revisions, terms, permalinks, search, audit) all become polymorphic, and you rebuild a registry of `(type, id)` → row which is just `posts` again.

### 13.3 Rejected: graph database (Dgraph, Neo4j) for content + relationships

Tempting because content has a lot of relationships (term ↔ post, parent ↔ child, related posts). Rejected: operations and ecosystem are far behind Postgres, Postgres covers 99% of our query needs with `ltree` + arrays + JSONB, and we'd lose every plugin author who knows SQL.

### 13.4 Rejected: row-level revisions via temporal tables / pg-tsv / pgaudit

Postgres has `pg_temporal` and there are extensions for system-versioned tables. Considered, rejected: editor revisions are a product feature (named, restorable, with author + comment), not an audit log. They need a different shape. We may still add `pgaudit` for security audit; that's separate from `post_revisions`.

### 13.5 Rejected: store block tree as serialized HTML with comment delimiters (WP-Gutenberg style)

WP stores blocks as HTML with `<!-- wp:block -->` markers. This was a backwards-compat choice they had to make. We aren't bound by it. JSONB is queryable, validatable, and avoids parsing on every render. The downside is HTML-out is no longer the canonical form; we re-render on demand. That's the right tradeoff.

### 13.6 Rejected: dropping comments from core

Considered. Most modern sites use Disqus / Giscus / nothing. We keep core comments because:
- Migration from WP needs them.
- Many users still want first-party comments without a third-party dependency.
- The implementation is small (~one table, one moderation API).

We keep them, but we don't invest heavily.

### 13.7 Rejected: a separate "page_revisions" / "comment_revisions"

Only `posts` get revisions in v1. Comments aren't versioned (track-changes on comments is an anti-feature). Terms aren't versioned (renaming a term is OK to be destructive; we log to audit).

### 13.8 Trade-off accepted: JSONB is queryable but not constrained

We will see plugins shove garbage into `meta`. JSON Schema declared in `post_types.field_schema` is enforced at write time **for fields the schema covers**. Unknown keys are allowed (under the right namespace). We accept the looseness for the plugin-extensibility win. The escape hatch is sidecar tables when a field needs real constraints.

### 13.9 Trade-off accepted: ltree adds write cost on term reparenting

Reparenting a term in a deep hierarchy rewrites `path` on every descendant. We accept this; reparenting is rare, the alternative (recursive CTE on every term archive) is worse.

### 13.10 Trade-off accepted: tsvector storage doubles row size for content-heavy posts

A 50 KB blog post produces a tsvector of ~30 KB. We pay storage and write cost; we get a single-index FTS query. For a v1 CMS this is fine. v2 may move to an external index.

---

## 14. Open Questions

Real ones, not filler.

1. **Soft-delete depth.** Today `posts.status='trash'` is the soft-delete signal. Do we want a separate `deleted_at` column for proper soft-delete on every table (terms, comments, users) with a unified GC? Pro: one purge job, predictable. Con: doubles the `WHERE status <> 'trash'` predicates we already have. Leaning: keep per-table flags, don't unify.

2. **Multi-language posts: one row per language, or one row with translations JSON?** WP uses plugins (WPML, Polylang) and each picks differently. The polyglot WP world is a mess of incompatible schemas. We could ship a `translations` table (`post_id`, `language`, `slug`, `title`, `content_blocks`) v1 and avoid the question. Or we ship nothing and let plugins handle it (then plugins fight). Open.

3. **Block tree size limits.** A page with 500 blocks produces ~500 KB of JSONB. Postgres will store it (TOAST), but rendering and revisions get expensive. Do we cap at 1 MB per post? Do we offload very large content to a separate `post_content` table to keep `posts` skinny? Probably the latter, but we want data before committing.

4. **`post_revisions.delta` format.** RFC 6902 JSON Patch is the obvious choice. But block trees have stable `clientId`s — a diff-by-id (move/insert/delete by clientId) would be smaller and more semantic. Build vs use-off-the-shelf decision. Defer until the block editor doc lands.

5. **Term hierarchy: ltree vs closure table vs nested set.** We chose ltree for hierarchical taxonomies and comment threads. Closure tables are more flexible (forests, multi-parent) but more expensive on writes. Open question whether some taxonomies need multi-parent (a term in multiple categories simultaneously). If yes, ltree is wrong.

6. **Custom field "groups" vs "fields-on-types".** §9 declares field groups as separate documents attached to types. WordPress (ACF) lets you attach a group to "posts in category X, by user role Y, on URL Z". Conditional attachments are powerful and produce horrible bugs. Open: ship attachments as a simple `applies_to: { post_types: […] }` only, defer conditional rules to plugins? Or build conditional rules in core?

7. **Search relevance tuning surface.** Site admins want sliders ("boost titles 2x", "demote old content"). Postgres FTS supports weights at index time, not query time, which limits dynamic tuning. Do we add a small DSL for query-time weight overrides? Or punt to external search?

8. **Per-row authorization at the SQL level.** RLS (row-level security) is tempting — let Postgres enforce "you can only see published posts" — but our auth model needs context (caps, roles, post-type capability overrides) that's awkward in RLS. We currently do auth in Go and trust the API layer. Open: would RLS catch enough mis-implementations to justify the complexity?

9. **`options` autoload sizing.** If autoload + Redis hash gets above ~1 MB, boot cost gets meaningful. Do we shard the autoload hash by namespace? Lazy-load per-plugin?

10. **WP compat shim and the data model.** The migration doc (08) needs to import `wp_postmeta` rows. Where does an unknown `meta_key` go? Into `meta.imported.<key>` namespace? Into a fallback `legacy_meta` table to keep `meta` clean? Decision affects how forgiving the importer can be.

---

*End of Core CMS & Data Model.*
