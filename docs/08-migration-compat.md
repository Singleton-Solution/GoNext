# 08 — Migration & WordPress Compatibility

> Status: design. Owner: migration subsystem.
> Depends on: [`00-architecture-overview.md`](00-architecture-overview.md), [`01-core-cms.md`](01-core-cms.md), [`04-block-editor.md`](04-block-editor.md), [`06-auth-permissions.md`](06-auth-permissions.md), [`07-media-performance.md`](07-media-performance.md).
> Audience: a senior engineer who has done one or more painful WP migrations and is allergic to optimism.

---

## 0. The premise

Migration is **the** adoption blocker. Nobody, no matter how clean our stack is, will leave a 6,000-post WordPress install behind if the move costs them a week of broken permalinks, lost custom fields, and an angry SEO consultant. Everything in this document is shaped by one rule:

> **If we can't get a typical WP site over without a regression, we don't exist.**

We are explicitly *not* running PHP. We are *not* executing WP plugins. We are providing:

1. A best-effort **importer** that pulls content, users, media, custom fields, taxonomies, menus, and permalinks from a live WP install (or an export of one).
2. A **WP REST API compat shim** so existing clients (mobile apps, headless frontends, build pipelines, scripts) keep working with minimal changes.
3. Documentation and tools that turn the inevitable plugin loss into a manageable, scoped problem rather than a panicked surprise.

That's the deal. The rest of this doc is the mechanics.

---

## 1. Scope: what we migrate, what we ignore, what stays manual

Some things move. Some are impossible. Some are *technically* possible but a bad idea (themes). Be explicit up-front so the user doesn't have an unrealistic mental model.

| Asset | Lives in WP at | Action | Why |
|---|---|---|---|
| Posts, pages | `wp_posts` | **Migrate** | Core content. Lossless on body via block conversion (see §5). |
| Custom post types (incl. ACF Pro CPTs) | `wp_posts` (`post_type` column) | **Migrate** | Required: every serious site uses CPTs. Type registration also migrated where derivable. |
| Revisions | `wp_posts` (`post_type=revision`) | **Migrate (optional, opt-in)** | Big and rarely needed. Default off; flag `--with-revisions`. |
| Comments | `wp_comments`, `wp_commentmeta` | **Migrate** | Including threading and approval state. |
| Pingbacks/trackbacks | `wp_comments` (type) | **Migrate as comments** | Tag with `source=trackback`. Most users disable these anyway. |
| Media (attachments) | `wp_posts` (`post_type=attachment`) + `wp_postmeta` + filesystem (`wp-content/uploads`) | **Migrate** | Two modes: copy or lazy proxy. See §6. |
| EXIF, alt text, captions | `wp_postmeta`, `_wp_attachment_metadata` | **Migrate** | Don't break alt text. |
| Users | `wp_users`, `wp_usermeta` | **Migrate (phpass hashes preserved)** | Re-auth on first login; rehash to argon2id. See §7. |
| Roles & capabilities | `wp_options.wp_user_roles` | **Migrate (mapped)** | Built-in roles 1:1. Custom roles best-effort. |
| Taxonomies & terms | `wp_terms`, `wp_term_taxonomy`, `wp_term_relationships` | **Migrate** | Including custom taxonomies. |
| Menus | `wp_terms` (taxonomy=nav_menu) + posts (post_type=nav_menu_item) | **Migrate** | WP's menu model is bizarre; we flatten it on import. |
| Widgets | `wp_options` (theme_mods, sidebars_widgets) | **Migrate (best-effort)** | Map to block widgets where we can; drop with a warning otherwise. |
| Permalinks | derived from `wp_posts.post_name`, `wp_options.permalink_structure` | **MIGRATE → redirects table** | SEO-critical. See §8. |
| Options (settings) | `wp_options` | **Selective migrate** | Site title, tagline, timezone, date format, etc. Allowlist; not a free-for-all. |
| Custom fields (ACF, Meta Box, Pods) | `wp_postmeta` + (for ACF) `wp_posts` `acf-field-group` rows | **Migrate (best-effort)** | Translate field group schemas; copy values. See §9. |
| Themes | filesystem (`wp-content/themes/...`) | **Do not migrate** | Pick a new theme. We will *not* port PHP templates. Document the new-theme process. |
| Plugins | filesystem + DB | **Do not migrate** | Cannot run PHP. We emit a **plugin replacement report**. See §11. |
| Multisite | `wp_blogs`, `wp_site`, per-site tables | **Out of scope v1** | Defer to v2. |
| XML-RPC | n/a | **Drop** | We don't implement it. |
| `.htaccess` rewrites | filesystem | **Best-effort import** | We parse common rules and add to redirects table; flag the rest. |

### What "best-effort" means

In every "best-effort" cell above: we attempt the conversion, we *log every imperfect transformation* with a structured warning, and the verification report (§10) groups warnings by category so the user can triage. We don't silently lose data. We don't pretend we got everything.

---

## 2. Import sources

A user might have any one of: SSH to the WP host with DB credentials, an admin-exported WXR file, a hosting setup where only the REST API is reachable. We support all three.

### 2.1 Sources, ranked

| Source | Coverage | Speed | Risk | When to use |
|---|---|---|---|---|
| **dbdirect** — connect to the WP MySQL/MariaDB directly | ★★★★★ all tables, all postmeta, all ACF | ★★★★ fast | Requires DB credentials; read-only is fine. Network access. | **Default.** Anything we can read from disk we should. |
| **wxr** — parse a WordPress eXtended RSS XML export | ★★★ posts/pages/comments/terms/menus; lossy on postmeta and serialized data | ★★ slow on big files (XML) | Self-contained file. Works offline. | Fallback when DB is unreachable. Smaller sites. |
| **rest** — call the live WP `/wp-json/wp/v2/...` endpoints | ★★ public-readable content only; many fields hidden; rate-limited | ★ slow (one HTTP req per N posts) | No DB credentials needed. | Managed hosts (WP.com Business, WP Engine without DB access). Last resort. |

**Recommendation written into the CLI**: try dbdirect first; offer wxr as portable fallback; only suggest REST when neither is possible.

### 2.2 dbdirect

```
gonext import --from-wpdb \
  --host db.example.com --port 3306 \
  --user wp_reader --password '...' \
  --database wp_prod --prefix wp_ \
  --media-root /mnt/wp-uploads     # optional; for copy mode
```

What we read directly: `wp_posts`, `wp_postmeta`, `wp_users`, `wp_usermeta`, `wp_terms`, `wp_term_taxonomy`, `wp_term_relationships`, `wp_options`, `wp_comments`, `wp_commentmeta`. Custom tables (WooCommerce, BuddyPress) are *not* read by core importer — they need adapters (out of scope v1).

### 2.3 WXR

WordPress's XML export. Notes:
- It contains `<item>` per post with `<content:encoded>` body, taxonomy assignments, and *some* postmeta (the ones authors flagged exportable).
- It does **not** contain user passwords, full ACF field group schemas, or anything stored outside posts/comments/terms.
- Big sites export it in chunks. We accept multiple files and merge.

We use a streaming XML parser (`encoding/xml`'s `Decoder.Token`) — we do not load the whole document.

### 2.4 REST

`/wp-json/wp/v2/...` endpoints, walked with `?per_page=100&page=N`. We follow the pagination headers (`X-WP-TotalPages`). Field coverage varies wildly by plugin — Yoast, ACF To REST API, etc. all add fields. We snapshot whatever's there and warn loudly that this mode is lossy.

---

## 3. Importer architecture

### 3.1 Phases

A migration is not one operation; it's a pipeline. The user can pause between phases, inspect output, and resume.

```
 ┌──────────┐   ┌──────┐   ┌─────────┐   ┌─────────┐   ┌────────┐
 │ discover │ → │ plan │ → │ preview │ → │ execute │ → │ verify │
 └──────────┘   └──────┘   └─────────┘   └─────────┘   └────────┘
       │           │            │             │             │
       ▼           ▼            ▼             ▼             ▼
   counts &    mapping     a sample of    real writes    diff report
   schema      decisions   the output     (idempotent)   + samples
   detection                  rendered
```

| Phase | What it does | Writes? | Reversible? |
|---|---|---|---|
| **discover** | Inspect source: row counts per table, detect plugins, list custom post types, list custom taxonomies, list ACF field groups, locate media root, estimate media size. | No | Trivially. |
| **plan** | Build a written *plan*: "we will import 4,213 posts, 18,402 attachments, 6 CPTs, 14 taxonomies. We will skip plugin-only tables: woocommerce_*, gravityforms_*. We will translate these ACF flexible content fields to repeater blocks. Yoast SEO metadata → gonext-seo plugin fields." Stored as a JSON file (`migration-plan.json`) the user can review and edit. | No (only writes the plan file) | Trivially. |
| **preview** | Run the importer in **dry-run** mode on a small sample (default: 20 posts of each type, 10 users, 5 menus). Render each one and produce a side-by-side preview. | No (writes to a `staging` schema we throw away) | Trivially. |
| **execute** | Run the plan for real, against the production DB. Idempotent — see §3.3. Streamed, resumable. | **Yes** | Snapshot-based rollback (§12). |
| **verify** | Sample N items, fetch from source and target, compare. Produce a structured report. Flag regressions. | No | n/a. |

The wizard UI (admin) and the CLI both walk these phases. CLI subcommands:

```
gonext migrate discover  [--from-wpdb ... | --from-wxr ... | --from-rest ...]
gonext migrate plan      --source <id>
gonext migrate preview   --plan migration-plan.json
gonext migrate execute   --plan migration-plan.json [--resume]
gonext migrate verify    --plan migration-plan.json
gonext migrate rollback  --to-snapshot <snapshot-id>
```

### 3.2 The mapping table

Every WP entity gets a row in `migration_map`:

```sql
CREATE TABLE migration_map (
  source_kind   text   NOT NULL,        -- 'post','user','term','attachment','comment','option'
  source_id     bigint NOT NULL,        -- WP's numeric ID
  target_kind   text   NOT NULL,        -- our domain kind
  target_id     uuid   NOT NULL,        -- our UUID
  source_run   uuid   NOT NULL,         -- which migration run produced this
  status        text   NOT NULL,        -- 'pending','done','failed','skipped'
  warnings      jsonb,                  -- structured warnings: see §3.4
  hash          bytea,                  -- content hash for change detection (incremental sync)
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (source_kind, source_id, source_run)
);
CREATE INDEX migration_map_target ON migration_map (target_kind, target_id);
```

This is the *only* mechanism by which IDs cross the boundary. Anywhere a WP ID appears in a foreign key (post → author, comment → post, term_relationship → post), we resolve it through `migration_map`. If the dependency hasn't been imported yet, we either reorder (preferred) or stub-and-fix-up (when ordering would deadlock — e.g. circular menu items).

### 3.3 Idempotence & resume

The importer is interruptible. SIGINT → flush current batch, write a `checkpoint` row, exit cleanly. `--resume` reads the checkpoint and skips anything already in `migration_map` with `status='done'`. The cost of a partial-then-resumed run is identical to one clean run, plus a couple of "is this already imported?" queries per batch.

We **never** delete and re-create. We **never** dedupe by title or slug — only by `(source_kind, source_id, source_run)`. If a user wants to re-run from scratch, they pick a new `source_run` UUID; the old map rows stay so we can roll back.

### 3.4 Warning model

Every imperfect transformation emits a warning row:

```go
type Warning struct {
    Code     string         // stable enum, e.g. "shortcode_unmapped", "acf_flexible_content_flattened"
    Severity string         // "info" | "warn" | "error"
    Source   SourceRef      // { kind: "post", id: 4221, field: "content" }
    Detail   string         // human-readable
    Context  map[string]any // structured payload
}
```

Warnings are persisted (separate table `migration_warnings`) and surfaced in the verification report grouped by code. The point: a migration run produces a triage list, not a wall of red.

### 3.5 Streaming

The importer must handle sites with millions of rows. Rules:
- Every read uses a cursor or a `LIMIT/OFFSET` (preferred: a keyset cursor on the PK).
- We batch writes (100 rows per `INSERT`).
- We `Stream` HTML conversions through a buffered pipeline, not slice-of-all-posts.
- Per-phase progress is reported via a job queue (Asynq) → the UI subscribes via Server-Sent Events.

### 3.6 Sketch: importer skeleton

Not production code — shape only.

```go
// migrate/runner.go

type Runner struct {
    src     Source        // dbdirect | wxr | rest
    plan    *Plan
    runID   uuid.UUID
    store   *content.Store
    media   *media.Service
    mapper  *Mapper        // wraps migration_map
    warn    *WarnSink
    logger  *slog.Logger
}

func (r *Runner) Execute(ctx context.Context) error {
    // Order matters: terms before posts, users before posts, attachments before posts,
    // posts before comments, menus last.
    phases := []phase{
        {"options",       r.importOptions},
        {"users",         r.importUsers},
        {"taxonomies",    r.importTaxonomies},
        {"terms",         r.importTerms},
        {"attachments",   r.importAttachments},
        {"posts",         r.importPosts},     // includes pages and CPTs
        {"term_rels",     r.importTermRels},
        {"comments",      r.importComments},
        {"menus",         r.importMenus},
        {"redirects",     r.buildRedirects},
        {"acf_schemas",   r.importACFSchemas},
        {"plugin_report", r.emitPluginReport},
    }

    for _, p := range phases {
        if r.plan.Skips[p.name] {
            r.logger.Info("phase skipped", "phase", p.name)
            continue
        }
        cp, _ := r.mapper.LoadCheckpoint(ctx, r.runID, p.name)
        if cp.Done {
            continue
        }
        r.logger.Info("phase start", "phase", p.name, "resume_from", cp.Cursor)
        if err := p.fn(ctx, cp); err != nil {
            return fmt.Errorf("phase %s: %w", p.name, err)
        }
        r.mapper.MarkPhaseDone(ctx, r.runID, p.name)
    }
    return nil
}
```

```go
// migrate/posts.go

func (r *Runner) importPosts(ctx context.Context, cp Checkpoint) error {
    iter := r.src.IteratePosts(ctx, cp.Cursor) // streaming, keyset

    batch := make([]*content.Post, 0, 100)
    for iter.Next() {
        wp := iter.Current()

        if existing, ok := r.mapper.LookupTarget(ctx, "post", wp.ID, r.runID); ok {
            // already imported in this run; skip
            continue
        }

        ourPost, warns, err := r.transformPost(ctx, wp)
        if err != nil {
            r.warn.Emit(Warning{Code: "post_transform_failed", Severity: "error", ...})
            continue
        }
        r.warn.EmitAll(warns)

        batch = append(batch, ourPost)
        if len(batch) == cap(batch) {
            if err := r.flushPosts(ctx, batch); err != nil {
                return err
            }
            batch = batch[:0]
            r.mapper.SaveCheckpoint(ctx, r.runID, "posts", iter.Cursor())
        }
    }
    return r.flushPosts(ctx, batch)
}
```

---

## 4. Sequence diagrams

### 4.1 End-to-end (happy path, dbdirect)

```
User           CLI/Admin            Importer            WP DB           Our DB         Media
 │  configure     │                    │                  │                │              │
 ├───────────────►│                    │                  │                │              │
 │                │  discover          │                  │                │              │
 │                ├───────────────────►│                  │                │              │
 │                │                    │  SELECT counts   │                │              │
 │                │                    ├─────────────────►│                │              │
 │                │                    │◄─────────────────┤                │              │
 │                │◄ summary ──────────┤                  │                │              │
 │                │  show plan         │                  │                │              │
 │  approve plan  │                    │                  │                │              │
 ├───────────────►│  preview           │                  │                │              │
 │                ├───────────────────►│  sample 20 posts │                │              │
 │                │                    ├─────────────────►│                │              │
 │                │                    │  transform       │                │              │
 │                │                    │  render          │                │              │
 │                │◄ side-by-side ─────┤                  │                │              │
 │  approve       │  execute           │                  │                │              │
 ├───────────────►├───────────────────►│  stream users    │                │              │
 │                │                    ├─────────────────►│                │              │
 │                │                    ├──────────── INSERT users ─────────►              │
 │                │                    │  stream terms…   │                │              │
 │                │                    │  stream posts    │                │              │
 │                │                    │  transform body  │                │              │
 │                │                    │  ──────────────── INSERT posts ───►              │
 │                │                    │  stream media (copy or proxy)     │              │
 │                │                    │  ────────────────────────────────────── PUT ───►│
 │                │                    │  rewrite URLs in content          │              │
 │                │                    │  build redirects table            │              │
 │                │                    │  emit plugin report               │              │
 │                │  verify            │                  │                │              │
 │                ├───────────────────►│  sample N        │                │              │
 │                │                    │  fetch from both │                │              │
 │                │                    │  diff            │                │              │
 │                │◄ verification ─────┤                  │                │              │
```

### 4.2 Resumable execute

```
 Execute start
   │
   ▼
 load checkpoint(run_id, phase)
   │
   ├── checkpoint missing ──► start at cursor=0
   │
   └── checkpoint exists ───► start at cursor=<saved>
       │
       ▼
 stream batch
   │
   ▼
 transform & insert (batch of 100)
   │
   ▼
 save checkpoint(run_id, phase, cursor=last_seen_id)
   │
   ▼
 SIGINT? ── yes ──► flush, save checkpoint, exit(0)
   │
   no
   │
   ▼
 more rows? ── yes ──► loop
   │
   no
   │
   ▼
 mark phase done, next phase
```

---

## 5. Content transformations — the HTML problem

This is the hard part. WordPress content lives in `post_content` as a soup of:
- Raw HTML (classic editor era).
- Gutenberg block markers (`<!-- wp:paragraph -->...<!-- /wp:paragraph -->`).
- Shortcodes (`[gallery ids="1,2,3"]`).
- Embed URLs on their own lines (oEmbed magic).
- Inline plugin output (Yoast schema injections in `<head>` not the body, thankfully).

We want a clean JSON block tree (see [`04-block-editor.md`](04-block-editor.md)). The conversion pipeline:

```
                           ┌───────────────────────────────────────┐
                           │             post_content              │
                           └───────────────┬───────────────────────┘
                                           │
                                           ▼
                                ┌──────────────────────┐
                                │  1. Pre-tokenize     │   detect Gutenberg comments, shortcodes,
                                │     scan for markers │   embed lines
                                └──────────┬───────────┘
                                           │
                                           ▼
                                ┌──────────────────────┐
                                │  2. Gutenberg parse  │   for each <!-- wp:type -->...<!-- /wp:type -->
                                │  (if present)        │   → known block? map to our block.
                                │                      │   → unknown? wrap as html block, warn.
                                └──────────┬───────────┘
                                           │
                                           ▼
                                ┌──────────────────────┐
                                │  3. Shortcode pass   │   for each [shortcode attr=...]
                                │                      │   → known? map (gallery, caption, audio, video,
                                │                      │     embed, contact-form-7, wpforms…)
                                │                      │   → unknown? generic shortcode block, warn.
                                └──────────┬───────────┘
                                           │
                                           ▼
                                ┌──────────────────────┐
                                │  4. HTML → blocks    │   parse with golang.org/x/net/html.
                                │  (classic content)   │   walk tree, map known elements to blocks.
                                │                      │   collapse adjacent inline text into one
                                │                      │   paragraph block.
                                └──────────┬───────────┘
                                           │
                                           ▼
                                ┌──────────────────────┐
                                │  5. URL rewrites     │   replace src="https://oldsite.com/wp-content/
                                │                      │   uploads/..." with new media URLs (via
                                │                      │   migration_map for attachments).
                                └──────────┬───────────┘
                                           │
                                           ▼
                                ┌──────────────────────┐
                                │  6. Normalize        │   merge consecutive runs of same block type
                                │                      │   where it doesn't change rendering.
                                └──────────┬───────────┘
                                           │
                                           ▼
                                  output: block tree
```

### 5.1 Mapping table — HTML elements

| Source element / pattern | Target block | Notes |
|---|---|---|
| `<p>` text `</p>` | `core/paragraph` | Inline formatting (`<strong>`, `<em>`, `<a>`, `<code>`) preserved as rich-text attrs. |
| `<h1>` … `<h6>` | `core/heading` (with `level`) | Anchor IDs preserved. |
| `<ul>` / `<ol>` | `core/list` (with `ordered` flag) | Nested lists supported. |
| `<li>` | inner of list | — |
| `<img>` | `core/image` | Pulls `alt`, `title`; resolves `src` via migration_map. |
| `<figure>` with `<img>` + `<figcaption>` | `core/image` (with `caption`) | — |
| `<blockquote>` | `core/quote` | — |
| `<pre><code>` | `core/code` | — |
| `<hr>` | `core/separator` | — |
| `<table>` | `core/table` | Rows mapped. Complex tables (colspan/rowspan) preserved as raw HTML in a `core/table` with a warning. |
| `<iframe src="youtube.com/..."`> | `core/embed` (provider=youtube) | — |
| `<iframe>` other | `core/html` | Warn `iframe_generic`. |
| `<script>` | dropped, warn | Never preserve script tags from migrated content. Security: WP author scripts are *not* trusted. |
| `<style>` (inline) | dropped, warn | Same reason. |
| any unrecognized tag | `core/html` (raw) | Warn `html_unmapped`. |

### 5.2 Mapping table — Gutenberg blocks

| WP block name | Our block | Field translation |
|---|---|---|
| `core/paragraph` | `core/paragraph` | content, dropCap, align |
| `core/heading` | `core/heading` | level, content, anchor |
| `core/list` | `core/list` | ordered, values |
| `core/quote` | `core/quote` | value, citation |
| `core/image` | `core/image` | id (resolved), url (rewritten), alt, caption, sizeSlug, href |
| `core/gallery` | `core/gallery` | images[] (each resolved through migration_map) |
| `core/cover` | `core/cover` | url, overlayColor, dimRatio |
| `core/media-text` | `core/media-text` | — |
| `core/columns` | `core/columns` | recursive into inner blocks |
| `core/group` | `core/group` | — |
| `core/embed` (any provider) | `core/embed` | provider, url, caption |
| `core/code` | `core/code` | — |
| `core/preformatted` | `core/preformatted` | — |
| `core/html` | `core/html` | raw HTML preserved as-is |
| `core/shortcode` | `core/shortcode` | runs through §5.3 |
| `core/buttons` / `core/button` | `core/buttons` / `core/button` | — |
| `core/separator` | `core/separator` | — |
| `core/spacer` | `core/spacer` | height |
| `core/table` | `core/table` | — |
| `core/file` | `core/file` | id, url (resolved) |
| `core/audio` | `core/audio` | id, url |
| `core/video` | `core/video` | id, url |
| `core/post-content`, `core/template-part`, `core/query` (FSE blocks in template parts) | dropped, warn `fse_block_in_content` | These shouldn't appear in `post_content` and usually indicate a template, not content. |
| any `acme/*` (plugin block) | `core/html` (preserving the markup the plugin emitted), warn `plugin_block_unmapped` | Plus an entry in the plugin-replacement report. |

### 5.3 Shortcode handling

Three outcomes per detected shortcode:

| Outcome | When | Behavior |
|---|---|---|
| **Mapped** | Shortcode is on our known list (`gallery`, `caption`, `embed`, `audio`, `video`, `playlist`, `wp_caption`, plus opt-in plugin mappings like `contact-form-7`). | Translated to the appropriate block with the right attrs. |
| **Preserved-as-shortcode** | Shortcode is unknown but the user has installed a gonext plugin that registers a handler for it (e.g. a "shortcode bridge" plugin). | Wrapped in `core/shortcode` block; runtime asks the plugin to render. |
| **Stripped** | Unknown shortcode, no handler. | Replaced with empty string + warning `shortcode_unmapped`. The original text is preserved in the warning's `Context` so the user can decide what to do. |

We never *guess* what a shortcode does. WP plugins overload `[gallery]` and `[contact-form-7]` to mean different things; pattern-matching attributes is a footgun.

### 5.4 Code sketch — block converter

```go
// migrate/html2blocks/convert.go

func ConvertPostContent(ctx context.Context, raw string, ctx ConvertCtx) (blocks.Tree, []Warning) {
    // Stage 1: split by Gutenberg block comments
    chunks := splitGutenbergMarkers(raw)

    var out blocks.Tree
    var warnings []Warning

    for _, c := range chunks {
        switch c.Kind {
        case gutenbergBlock:
            b, w := mapGutenbergBlock(c.Name, c.Attrs, c.InnerHTML, ctx)
            out = append(out, b)
            warnings = append(warnings, w...)
        case classicHTML:
            // Stage 2: pull shortcodes out as placeholders
            withPlaceholders, scs := extractShortcodes(c.Text)
            // Stage 3: parse residual HTML
            parsed := html.Parse(strings.NewReader(withPlaceholders))
            bs, w := walkHTML(parsed, ctx)
            // Stage 4: re-inflate shortcode placeholders → blocks
            bs, w2 := inflateShortcodes(bs, scs, ctx)
            warnings = append(warnings, w...)
            warnings = append(warnings, w2...)
            out = append(out, bs...)
        }
    }

    // Stage 5: URL rewrite pass
    out = rewriteMediaURLs(out, ctx.AttachmentMap)

    return out, warnings
}
```

---

## 6. Media migration

Two modes, picked at plan time.

| Mode | Behavior | Pros | Cons |
|---|---|---|---|
| **copy** | All `wp-content/uploads` files downloaded (via filesystem if `--media-root` given, else via HTTP) and pushed to our S3-compatible bucket. URLs in content rewritten to new bucket. | Self-contained, fast at runtime, no dep on WP. | Slow upfront. Storage cost. |
| **proxy** | URLs in content rewritten to `<our-host>/proxy-media/<hash>`. On first request we lazily fetch from the old origin, store, and serve. | Fast upfront. Spreads cost over time. | Requires old WP host to stay alive during the proxy window. SSL gotchas. |

Default: **copy**. Proxy is for emergencies — "we have to launch tomorrow."

### 6.1 What we preserve per attachment

| Field | Source | Notes |
|---|---|---|
| File bytes | filesystem or HTTP | original size only; we regenerate our own thumbnails via the image pipeline (see [`07-media-performance.md`](07-media-performance.md)). |
| `alt` text | `wp_postmeta._wp_attachment_image_alt` | — |
| Caption | `wp_posts.post_excerpt` for attachment | — |
| Description | `wp_posts.post_content` for attachment | — |
| Title | `wp_posts.post_title` | — |
| EXIF | `wp_postmeta._wp_attachment_metadata` (serialized) | We deserialize PHP's `serialize()` (via a Go phpser library) and re-emit as JSON. |
| MIME type | `wp_posts.post_mime_type` | — |
| Upload date | `wp_posts.post_date` | — |
| Author | `wp_posts.post_author` → user via migration_map | — |
| Featured-image links | `wp_postmeta._thumbnail_id` on posts | Resolved into post's `featured_media_id`. |

### 6.2 URL rewriting

When we write a converted block tree, any `url`/`src` attribute pointing at the old site's `wp-content/uploads/...` is resolved:

1. Strip query string (WP appends `?ver=` and resize params).
2. Look up the path in the attachment-map (keyed by source-path).
3. Replace with the new URL.
4. If no match, warn `media_url_unresolved` and leave the URL alone (it'll 404, but at least we logged it).

We also handle WP's "size suffixes" (`image-1024x768.jpg`) — these are not separate uploads, they're WP's thumbnails. We strip the suffix, resolve the base file, and let our image pipeline regenerate sizes.

---

## 7. Users, passwords, roles

### 7.1 phpass hash preservation

WordPress hashes passwords with phpass — a portable PHP implementation of OpenBSD's Blowfish-based crypt. The hash format is `$P$B...`. We can verify these in Go (the algorithm is fully specified; there are Go ports).

Migration strategy:
1. Copy `user_pass` (the `$P$...` hash) into our `users.password_hash_legacy` column.
2. On first login attempt for a user with a non-null `password_hash_legacy`:
   - Verify the submitted password against the phpass hash.
   - On success: compute argon2id hash, store in `password_hash`, NULL out `password_hash_legacy`.
   - On failure: standard failed-login flow.
3. After 90 days (configurable), force a password reset for any user who hasn't logged in (their legacy hash will be expired).

This is the same approach Discourse, Mastodon, and others use for cross-system migrations. It's well-trodden.

### 7.2 "Your password still works" email

After execute, we offer two email options the user picks per role:
- **Soft** — "We've moved your site to gonext. Your password still works. Log in at <url>." Low friction; users won't notice anything.
- **Forced reset** — "Security update: please reset your password to continue." Higher friction; more secure (assumes the WP DB might have been compromised).

Default: soft for subscribers/contributors, forced reset for administrators and editors. This is a defensible compromise; the user can override per role.

### 7.3 Role mapping

Slugs below are the canonical gonext role slugs from doc 06 §6.1 — `administrator` (not `admin`), `editor`, `author`, `contributor`, `subscriber`, plus `super_admin` for the multisite super-admin (v2 in practice, but the slot is reserved). (Fixed per review C12 — use `administrator` slug and include `super_admin` so a migration write matches the seeded role row.)

| WP role | gonext role slug | Capabilities |
|---|---|---|
| Super Admin (multisite) | `super_admin` | reserved; v1 single-site installs leave this empty. |
| Administrator | `administrator` | full |
| Editor | `editor` | manage content of any type, no plugin/theme/user mgmt |
| Author | `author` | manage own content of type `post` |
| Contributor | `contributor` | draft own posts, cannot publish |
| Subscriber | `subscriber` | read-only |
| Custom role (capabilities-derived) | best-effort match | We diff the cap set against our built-in roles. Closest match → that role + a warning. Exact custom-role replication available v2. |

### 7.4 Caveats

- Two-factor auth state from common WP plugins (Wordfence, Two Factor) is **not** migrated. Users with 2FA enabled get an email explaining they must re-enroll. Bypassing 2FA on migration would be an attack vector.
- Application passwords (WP's named API tokens) are migrated as our API tokens, with a 30-day grace period in which the old token strings work for backward compat.

---

## 8. Permalinks & redirects

The single biggest SEO disaster in a migration is broken URLs. We treat redirects as a first-class object.

### 8.1 What we generate

> **This table is the project-wide redirects store.** It is also referenced from doc 01 §7 for non-migration slug-change history (when a published post's slug changes, core writes a `source='slug_change'` row here so the old permalink continues to 301 to the new one). Plugin-emitted redirects use `source='plugin'`. Doc 01 no longer defines a separate `permalink_redirects` table; this `redirects` table is the single store. (Fixed per review C4 / contract M4 — unify on doc 08's shape; doc 01 forwards here.)

For every imported post/page/attachment/taxonomy term:
1. Compute the **old** URL using WP's `permalink_structure` option (parsed: `%year%/%monthnum%/%postname%/`, etc.) plus the post's `post_date` and `post_name`.
2. Compute the **new** URL using our slug/route rules.
3. If they differ, insert a row in `redirects`.
4. If they don't differ, still insert a row but mark it `identity=true` — this lets us detect URL drift later if we ever change routes.

```sql
CREATE TABLE redirects (
  id          uuid PRIMARY KEY,
  from_path   text NOT NULL,                 -- "/2018/03/14/hello-world/"
  to_path     text NOT NULL,                 -- "/blog/hello-world" (relative) or "https://..." (absolute URL allowed)
  status      smallint NOT NULL DEFAULT 301, -- 301 (permanent) | 302 (temp)
  source      text NOT NULL,                 -- 'slug_change' | 'manual' | 'migration' | 'htaccess' | 'plugin'
  source_run  uuid,                          -- migration run / job that created this row (nullable for slug_change / manual)
  identity    boolean NOT NULL DEFAULT false,-- true when from_path == to_path (drift detection only)
  hits        bigint NOT NULL DEFAULT 0,     -- counted by middleware
  last_hit_at timestamptz,
  created_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (from_path)
);
CREATE INDEX redirects_to ON redirects (to_path);
CREATE INDEX redirects_source ON redirects (source);
```

Supported `source` values:

| Source | Written by | When |
|---|---|---|
| `slug_change` | Core (doc 01 §7) | A post's slug changes after first publish; old permalink redirects to new. |
| `manual` | Admin UI / CLI | Operator-authored redirect (e.g. one-off vanity URL). |
| `migration` | Importer | Section 8.1 — old WP URL → new gonext URL. |
| `htaccess` | Importer | Section 8.3 — parsed `RewriteRule` patterns. |
| `plugin` | Plugin via host call | Plugin-emitted redirects (SEO plugin, link-checker, etc.). |

`to_path` accepts either a relative path (`/blog/hello`) or an absolute URL (`https://archive.example/post`) — the middleware in §8.2 forwards to whichever form is stored.

### 8.2 Serving redirects

A middleware in our Go API (and in the Next.js renderer's middleware layer) checks `from_path` on every 404 before rendering the 404 page. Found → 301 (or whatever status is set). The check is a single Redis lookup (the table is cached in full on startup; mutation invalidates).

### 8.3 `.htaccess` ingestion

If we can read `.htaccess`, we parse common `RewriteRule` patterns into the same `redirects` table. Unrecognized patterns get warnings and the user can hand-edit them post-migration.

### 8.4 Why 301 forever

Search engines collapse 301 chains and update their indexes over months. Removing redirects in <6 months invariably costs traffic. Default is permanent retention; we warn loudly before any UI to delete redirects.

---

## 9. Custom fields (ACF, Meta Box, Pods)

Custom fields are where WP migrations go to die. Every site has them. They power the actual structure of the content. They're stored in `wp_postmeta` as flat key/value rows with serialized-PHP values, and the *schema* (which keys exist on which post types) lives in plugin-specific tables or, for ACF, in `wp_posts` as `post_type=acf-field-group` rows whose `post_content` is JSON.

### 9.1 ACF specifically

ACF stores field groups like this:
- A row in `wp_posts` with `post_type=acf-field-group`, `post_content` = JSON schema of the group.
- For each field in the group, a row in `wp_posts` with `post_type=acf-field`, `post_content` = JSON of the field config.
- Field-value pairs live in `wp_postmeta` with key = the field's name, plus a sibling key prefixed with `_` whose value is the field's internal ID.

We parse all of this:
1. Read all `acf-field-group` rows; parse the JSON.
2. For each group, read its `acf-field` children; parse their JSON.
3. Translate to our **custom field definitions** (see [`01-core-cms.md`](01-core-cms.md)).
4. For each post imported, look up its postmeta and translate the values.

### 9.2 ACF field-type translation

| ACF field type | gonext equivalent | Notes |
|---|---|---|
| `text`, `textarea` | `string`, `text` | direct. |
| `number` | `number` | direct. |
| `email`, `url`, `password` | `string` with validator | direct. |
| `wysiwyg` | `richtext` (HTML → blocks via §5) | converted at import time. |
| `image`, `file` | `media_ref` (UUID FK) | resolved via migration_map. |
| `gallery` | `media_ref[]` | — |
| `select`, `checkbox`, `radio` | `enum` / `enum[]` | choices preserved. |
| `true_false` | `bool` | — |
| `link` | `link` (struct: url, title, target) | — |
| `post_object`, `page_link`, `relationship` | `entity_ref` / `entity_ref[]` | FK to our post UUID via migration_map. Cross-CPT refs resolved after all posts imported. |
| `taxonomy` | `term_ref[]` | — |
| `user` | `user_ref` | — |
| `date_picker`, `date_time_picker`, `time_picker` | `date` / `datetime` / `time` | — |
| `color_picker` | `color` | — |
| `oembed` | `embed_url` | — |
| `google_map` | `geo` (lat/lng/zoom) | — |
| **Repeater** (ACF Pro) | `repeater` (array-of-struct) | Schema and values both translatable. ✅ |
| **Flexible Content** (ACF Pro) | `flexible_content` (tagged union) | Schema translates; values translate; UI in our editor may render differently. ⚠ Warn. |
| **Clone** (ACF Pro) | flatten or reference | Best-effort: if the cloned group is imported separately, we emit a reference. Otherwise inline-flatten and warn. |
| **Group** (ACF Pro) | `group` (struct) | direct. |
| **Block** (ACF Blocks) | mapped to `core/html` for now | The actual block rendering can't be ported (PHP). Warn `acf_block_unrendered` and emit a TODO entry in the plugin report. |

### 9.3 Meta Box & Pods

Same pattern, different storage:
- **Meta Box**: field schemas live in PHP code, not the DB. We can't auto-import them. Workaround: an opt-in CLI flag `--meta-box-config <path/to/exported.json>` accepts an export from the Meta Box "Export" addon. Without it, we warn `meta_box_schema_unknown` and import raw postmeta values into a generic `legacy_meta` JSONB column.
- **Pods**: stores schemas in custom tables (`wp_pods_*`). We read those. Similar translation table; not enumerated here.

### 9.4 The "we don't know what this meta key is for" case

Every postmeta key we encounter that isn't claimed by a known plugin's importer is stored in `legacy_meta` JSONB on the post. Not lost, just unstructured. Users can write a migration plugin later to promote keys into proper fields once they've decided the schema.

---

## 10. Verification

After execute, we run a verification pass. This is the difference between "we ran the importer" and "the migration is done."

### 10.1 Checks

| Check | What it samples | Pass criterion |
|---|---|---|
| **Count parity** | All post types, taxonomies, terms, comments, users, attachments. | Within 1% (allowing for revisions / spam comments dropped). |
| **Rendered HTML similarity** | N random posts per type (default N=50). Fetch the page from old WP and from our staging. Strip volatile elements (timestamps, nonces). Compute structural diff. | ≥ 95% of sampled posts have ≤ 5% character-level diff outside the volatile set. |
| **Media accessibility** | N random attachments. HEAD request to old URL and new URL. | Both 200, content-length matches within 1KB (re-encoding tolerance). |
| **Redirect coverage** | All known old URLs. | 100% have a redirect row. |
| **Search parity** | M canned queries against old WP search and our search. | Top-10 overlap ≥ 70%. (Search isn't expected to be identical — different engines.) |
| **Custom field coverage** | All ACF field groups. | Every group has a translated schema; every field has either a target type or a warning. |
| **Permalink resolvability** | N random old URLs. | All resolve via middleware to a non-404. |

### 10.2 Report format

A single Markdown file plus a JSON sibling for tooling:

```
migration-report.md
├── summary
│   ├── total posts: 4,213 (source) → 4,210 (target, 3 skipped: see warnings)
│   ├── total attachments: 18,402 → 18,402
│   ├── total users: 14 → 14
│   ├── warnings: 287 (info: 220, warn: 65, error: 2)
├── by category
│   ├── shortcode_unmapped: 41
│   ├── plugin_block_unmapped: 12
│   ├── acf_flexible_content_flattened: 8
│   ├── ...
├── samples
│   ├── side-by-side HTML diffs of 10 posts
│   ├── side-by-side media of 5 attachments
├── plugin replacement guide
│   └── (see §11)
└── todo
    └── action items for the user, ordered by impact
```

The admin UI renders this report inline with the option to drill into any category and see the raw rows.

---

## 11. WP REST API compatibility shim

Even after migration, lots of *things* talk to a WP site via `/wp-json/wp/v2/...`: mobile apps, headless frontends (Next/Gatsby/Astro with WP source), build pipelines, analytics tools, third-party integrations. We expose a compatibility shim under the same paths so these clients keep working.

### 11.1 Endpoints

| Endpoint | Methods | Status |
|---|---|---|
| `GET /wp-json/` | GET | Stub: returns a minimal site-info envelope. |
| `GET /wp-json/wp/v2/posts` | GET, POST | Implemented (POST requires our auth, see §11.4). |
| `GET /wp-json/wp/v2/posts/<id>` | GET, POST, PUT, DELETE | Implemented; `<id>` is mapped through `migration_map`. |
| `GET /wp-json/wp/v2/pages` | GET, POST | Implemented. |
| `GET /wp-json/wp/v2/pages/<id>` | GET, POST, PUT, DELETE | Implemented. |
| `GET /wp-json/wp/v2/<custom-type>` | GET, POST, PUT, DELETE | Implemented for any type whose REST `show_in_rest=true` (we mirror that flag). |
| `GET /wp-json/wp/v2/categories` | GET, POST, PUT, DELETE | Implemented. |
| `GET /wp-json/wp/v2/tags` | GET, POST, PUT, DELETE | Implemented. |
| `GET /wp-json/wp/v2/users` | GET, POST, PUT, DELETE | Implemented; sensitive fields gated by capability as in WP. |
| `GET /wp-json/wp/v2/users/me` | GET | Implemented. |
| `GET /wp-json/wp/v2/media` | GET, POST | Implemented. Upload via `multipart/form-data`. |
| `GET /wp-json/wp/v2/comments` | GET, POST, PUT, DELETE | Implemented. |
| `GET /wp-json/wp/v2/types` | GET | Implemented. |
| `GET /wp-json/wp/v2/taxonomies` | GET | Implemented. |
| `GET /wp-json/wp/v2/search` | GET | Implemented; results map to a subset of post fields. |
| `GET /wp-json/wp/v2/menus` (WP 5.9+) | GET | Implemented. |
| `GET /wp-json/wp/v2/menu-items` | GET, POST, PUT, DELETE | Implemented. |
| `GET /wp-json/wp/v2/blocks` (reusable) | GET, POST, PUT, DELETE | Implemented. |
| `GET /wp-json/wp/v2/settings` | GET, POST | Implemented for the option allowlist. |
| `GET /wp-json/wp/v2/themes` | GET | Implemented (read-only; lists our theme as a single entry, mimics WP envelope). |
| `GET /wp-json/wp/v2/plugins` | GET | Implemented (read-only; lists installed gonext plugins). |
| `GET /wp-json/wp/v2/statuses` | GET | Implemented. |
| `GET /wp-json/wp/v2/<namespace>/<route>` (plugin-registered) | varies | **Not implemented.** Plugins ran in PHP. We do not emulate their custom REST routes. |

### 11.2 Field-level emulation

The WP REST API has a particular response envelope. We mirror it.

| WP field | Emulation | Notes |
|---|---|---|
| `id` | integer | We assign and store a stable `legacy_int_id` per post for the shim. New posts created via shim get a fresh integer; for the post's UUID we expose `meta.uuid`. |
| `date`, `date_gmt`, `modified`, `modified_gmt` | direct | — |
| `slug` | direct | — |
| `status` | direct | `publish`/`draft`/`private`/`pending`/`future` mapped. |
| `type` | direct | post type slug. |
| `link` | direct | full permalink. |
| `title` (object: `rendered`, optional `raw`) | direct | title is a string in our model; we synthesize the envelope. |
| `content` (object: `rendered`, `protected`, optional `raw`) | `rendered` = server-rendered HTML of our block tree; `raw` = serialized block markup (Gutenberg-style comments) for round-tripping editors. | — |
| `excerpt` | direct (rendered + raw). | — |
| `author` | integer (legacy_int_id of user) | — |
| `featured_media` | integer | — |
| `comment_status`, `ping_status` | direct | — |
| `sticky` | direct | — |
| `template` | empty string for now | — |
| `format` | direct | — |
| `categories`, `tags` | array of legacy_int_ids | — |
| `meta` | object | We expose registered meta fields (those `register_meta`-ed equivalents) as keys. Unregistered meta omitted by design (matches WP). |
| `_links`, `_embedded` | direct | HAL-style; we generate. |
| `yoast_head`, `yoast_head_json` | **omitted** | WP-specific plugin output. Clients depending on Yoast should swap to our SEO plugin's fields. |
| `acf` | **stubbed** | If the ACF To REST API plugin equivalent is installed in gonext, we emit equivalent shape; otherwise field is absent. |
| `jetpack_*`, custom plugin fields | **omitted** | — |

For each omitted field we provide a documented migration path.

### 11.3 Pagination, ordering, filtering

WP's query params: `page`, `per_page`, `search`, `after`, `before`, `author`, `categories`, `tags`, `slug`, `status`, `orderby`, `order`. All supported with the same semantics.

Response headers `X-WP-Total` and `X-WP-TotalPages` are returned. Clients depend on these.

### 11.4 Authentication

The shim accepts four auth mechanisms, listed canonically (must match doc 05 §3.3 exactly). (Fixed per review C10 / contract M2 — explicit four-mechanism enumeration; same names, same order as doc 05.)

1. **Cookie + `X-WP-Nonce` header** — for browser sessions migrating from WP-style integrations. The nonce is a short-lived token bound to the session; our `/wp-json/wp/v2/...` middleware validates it as a CSRF token against the active session cookie.
2. **Application Passwords** — `Authorization: Basic <user:apppass>` where the password is a generated **application password** (NOT the user's real password). We map Application Passwords onto our **personal-access-token (PAT) system internally**; doc 06 owns the PAT storage schema (see [`06-auth-permissions.md`](06-auth-permissions.md), the `personal_access_tokens` table). Importing existing WP application-password records during migration creates PAT rows with a 30-day grace period in which the old token strings continue to work. (Cross-references gap B10 — Application Password storage is the PAT store, not a separate table.)
3. **Session cookie + CSRF (our native admin flow)** — when called from our own admin UI, the standard `__Host-gn_session` cookie plus `X-CSRF-Token` (double-submit) is accepted; same flow as the rest of the API.
4. **JWT bearer** — `Authorization: Bearer <jwt>` for our own API token system (see doc 06).

OAuth2 application-installed schemes are out of scope for v1 in the shim.

### 11.5 What the shim does NOT do

- It does **not** call any plugin code (we have none in the WP sense).
- It does **not** dispatch WP hooks (`rest_api_init`, etc.). Plugins should hook into *our* hook system; the shim is a thin translation, not an event bridge.
- It does **not** emit `oembed` JSON discovery for arbitrary URLs (a separate oEmbed proxy may come later).
- It does **not** support `XML-RPC` (`/xmlrpc.php`). Always returns 410 Gone.

### 11.6 Why this matters

Without the shim, every mobile app and headless frontend talking to a WP site has to be rewritten before cutover. With the shim, the mobile app keeps shipping; the rewrite can be a separate, scheduled project. That's the difference between "we can switch this weekend" and "we can switch in Q3 next year, maybe."

### 11.7 Sketch

```go
// shim/wp/v2/posts.go

func (h *Handler) GetPosts(w http.ResponseWriter, r *http.Request) {
    q := parseWPQuery(r) // page, per_page, search, status, ...
    page, err := h.svc.ListPosts(r.Context(), q.toListSpec())
    if err != nil {
        wpError(w, err)
        return
    }
    out := make([]wpPostEnvelope, 0, len(page.Items))
    for _, p := range page.Items {
        out = append(out, h.toWPEnvelope(r.Context(), p))
    }
    w.Header().Set("X-WP-Total", strconv.Itoa(page.Total))
    w.Header().Set("X-WP-TotalPages", strconv.Itoa(page.TotalPages))
    writeJSON(w, out)
}

func (h *Handler) toWPEnvelope(ctx context.Context, p *content.Post) wpPostEnvelope {
    return wpPostEnvelope{
        ID:           p.LegacyIntID, // stable, allocated at create time
        Date:         p.PublishedAt.Format("2006-01-02T15:04:05"),
        DateGMT:      p.PublishedAt.UTC().Format("2006-01-02T15:04:05"),
        Slug:         p.Slug,
        Status:       wpStatus(p.Status),
        Type:         p.Type.Slug,
        Link:         h.permalink(p),
        Title:        wpRendered{Rendered: html.EscapeString(p.Title)},
        Content:      wpRendered{Rendered: h.renderBlocks(ctx, p.Blocks)},
        Excerpt:      wpRendered{Rendered: p.Excerpt},
        Author:       userLegacyID(p.AuthorID),
        FeaturedMedia: mediaLegacyID(p.FeaturedMediaID),
        // ...
        Links: h.buildLinks(p),
    }
}
```

---

## 12. Plugin replacement guide (auto-generated)

During discover/plan we read `wp_options.active_plugins` — a serialized PHP array of plugin slugs. For each known slug we have a curated recommendation. The report is emitted as Markdown next to the verification report.

### 12.1 Curated mapping (extract)

| WP plugin | Recommendation |
|---|---|
| `wordpress-seo/wp-seo.php` (Yoast SEO) | gonext-seo plugin. Meta titles/descriptions migrated automatically; sitemap regenerated on import. Yoast schema graph: partial; review post-migration. |
| `seo-by-rank-math/rank-math.php` | gonext-seo plugin. Same migration; check redirects sub-tool migrates into our redirects table. |
| `advanced-custom-fields/acf.php`, `advanced-custom-fields-pro/acf.php` | Built-in custom fields (§9). Field schemas and values imported. |
| `wpforms-lite/wpforms.php` | gonext-forms plugin. Form schemas convertible via a one-shot tool; submissions kept in WP only (we do not migrate form-entry tables v1). |
| `contact-form-7/wp-contact-form-7.php` | gonext-forms plugin. `[contact-form-7]` shortcodes mapped to form blocks. |
| `woocommerce/woocommerce.php` | **No v1 equivalent.** WooCommerce is a CMS-within-the-CMS. Recommend: keep WP for commerce, run gonext for content; or migrate to a dedicated commerce platform (Shopify/Medusa) and we wire it up via our API. Flagged red in the report. |
| `jetpack/jetpack.php` | Multi-feature plugin; we list each Jetpack subfeature in the report with separate recommendations (CDN: use our media pipeline; stats: use our analytics plugin; etc.). |
| `akismet/akismet.php` | gonext-akismet adapter (calls the same Akismet API with the same key) OR built-in spam filter. |
| `wordfence/wordfence.php` | Security: gonext-firewall plugin. 2FA must be re-enrolled (§7.4). |
| `litespeed-cache/litespeed-cache.php`, `w3-total-cache/...`, `wp-rocket/...` | Caching is built in (see [`07-media-performance.md`](07-media-performance.md)). No plugin needed. |
| `redirection/redirection.php` | Redirects imported into our `redirects` table directly. Plugin not needed. |
| `wp-super-cache/wp-cache.php` | Same as above. |
| `mailpoet/mailpoet.php` | **No v1 equivalent.** Keep WP for email lists or migrate to dedicated ESP (Mailchimp, Buttondown). |
| `polylang/polylang.php`, `wpml-multilingual-cms/...` | gonext-i18n plugin (v2). v1: content stays single-language; we tag translations via a custom field for later. |
| `gravityforms/gravityforms.php` | Same as WPForms. |
| `elementor/elementor.php`, `beaver-builder-lite-version/...`, `siteorigin-panels/...` | Page builders. Layout serialized formats unique to each; we attempt to convert top-level structures to columns/group blocks. **Lossy.** Manually review every page-builder page. |

### 12.2 "Unknown plugin" handling

For any active plugin not in our curated list, the report includes an entry: "Unknown plugin: `<slug>`. We don't have a recommendation. Search our plugin marketplace, or build/commission a gonext equivalent." We don't try to auto-translate unknowns; the false-confidence cost is too high.

### 12.3 Output

A Markdown table with three columns: `plugin`, `category` (SEO/forms/cache/security/etc.), `recommendation` (action items). Also a JSON sibling for programmatic consumption.

---

## 13. Rollback

Migrations fail in ways that are only obvious days later: a missed permalink pattern, a custom-field translation that lost data, a verification check that was too lenient. Rollback must be possible.

### 13.1 Snapshot model

Before `execute`, we take a snapshot:
1. **Postgres logical dump** (`pg_dump`) of all our content tables, scoped by `created_at < migration_start_time` — effectively the empty/seed state. Stored as a single tarball.
2. **S3 inventory** of pre-migration media (a manifest of object keys we know about). Pre-existing media is left alone; new uploads from the migration are marked with a `migration_run` tag.
3. A row in `migration_snapshots` describing all of the above.

### 13.2 Rolling back

`gonext migrate rollback --to-snapshot <id>`:
1. Truncate or `DELETE` (depends on scale) all rows in our content tables with `migration_run=<id>` or created during the run window.
2. Delete S3 objects tagged with the run ID.
3. Re-apply any rows that were *modified* by the run (we keep a small WAL of "things touched, not just inserted" — see §13.3).
4. Remove redirects rows with `source_run=<id>`.

### 13.3 What we don't try to roll back

If the migration ran *into* an already-populated gonext instance and modified pre-existing data, we use a per-run WAL of pre-images:

```sql
CREATE TABLE migration_undo_log (
  source_run uuid NOT NULL,
  table_name text NOT NULL,
  pk         jsonb NOT NULL,
  pre_image  jsonb,                       -- null = row didn't exist before
  applied_at timestamptz NOT NULL DEFAULT now()
);
```

On rollback we replay these in reverse order, restoring pre-images. This is bounded by the size of the WAL, which is bounded by the migration's writes. Expensive but correct.

### 13.4 Time window

Snapshots are retained for 30 days by default. After that the user explicitly confirms migration acceptance and snapshots are deleted (they're large). The window is configurable.

---

## 14. Incremental sync (transition mode)

Optional and opt-in. Some users want to run gonext and WP side-by-side for weeks before cutover — they cut over their reading audience first and let editors keep using WP for a while.

### 14.1 Direction

**One-way only**: WP → gonext. Two-way sync would require us to write back into WP, which means implementing WP plugins / DB writes, which we won't.

### 14.2 Mechanism

A nightly Asynq job:
1. Query the WP DB for rows where `post_modified > last_sync_timestamp`.
2. For each modified row, look up the corresponding gonext post via `migration_map`.
3. Re-transform and update; emit warnings.
4. Same for terms, comments, attachments, users.
5. Move forward `last_sync_timestamp` only after a clean pass.

A user editing the same post on the gonext side during the transition window has their edit clobbered — we **conflict-detect** by hashing both sides and refuse to overwrite changes made after a configurable cutoff (default: 1 hour before sync). Conflicts surface as warnings, never silent.

### 14.3 Cutover

When the user is ready: disable the sync job. Optionally lock the WP site to read-only (we provide a documented snippet, but don't run it for the user). Switch DNS.

---

## 15. CLI ↔ UI parity

Every migration step exists in both surfaces. The CLI is required for:
- Sites > 100k posts (UI streams are fine but the user usually wants to script it).
- Headless / CI integrations.
- Resuming a long-running migration from a different machine.

The UI is preferred for:
- Typical 100–10,000-post blogs.
- Users not comfortable with terminals.
- The preview/verify step (visual diffs are much easier to consume in a browser).

Internally they're the same code — the CLI is a thin client over the same Go API the UI uses, talking to the same Asynq job queue.

### 15.1 UI wizard

```
┌─────────────────────────────────────────────────────────────┐
│ Migrate from WordPress                                       │
├─────────────────────────────────────────────────────────────┤
│ Step 1 of 5 — Source                                         │
│                                                              │
│ ○ Connect to WP database (recommended)                       │
│ ○ Upload WXR export                                          │
│ ○ Connect via REST API                                       │
│                                                              │
│ Host:    [_________]                                         │
│ Port:    [_____]                                             │
│ User:    [_________]                                         │
│ Pass:    [_________]                                         │
│ DB:      [_________]                                         │
│ Prefix:  [wp_______]                                         │
│                                                              │
│ [ Test connection ]                                          │
└─────────────────────────────────────────────────────────────┘
```

Subsequent steps: Discover (read-only summary), Plan (editable mapping decisions), Preview (sample posts side-by-side), Execute (live progress), Verify (report).

---

## 16. Compat test corpus & CI

Migration regressions are insidious. We catch them by running a fixed corpus of WP sites through the importer in CI.

### 16.1 Corpus (proposed)

Ten archetypal sites, each as a `mysqldump` + uploads tarball checked in (anonymized where needed):

| # | Profile | Why |
|---|---|---|
| 1 | Tiny personal blog, classic editor, no plugins | Smoke test, fast. |
| 2 | 1k-post news site, classic editor, Yoast + Jetpack | Classic content + common plugins. |
| 3 | 5k-post blog, mixed classic + Gutenberg | Tests the mixed-content path. |
| 4 | 500-post site, Elementor pages | Page-builder lossiness. |
| 5 | 200-post site, ACF Pro heavy (repeaters, flex content) | Custom-fields stress test. |
| 6 | 2k-post site with custom post types and custom taxonomies | CPT/CT coverage. |
| 7 | 1k-comment-heavy site (threaded, with pingbacks) | Comment edge cases. |
| 8 | 50k-image media library | Media pipeline scale. |
| 9 | WooCommerce store (we don't migrate commerce; assert we warn correctly) | Negative-case correctness. |
| 10 | Multi-language site (Polylang) | i18n edge cases (v2 target; for now: assert we warn correctly). |

### 16.2 Assertions

For each corpus site:
- `discover` returns the expected counts (golden file).
- `execute` completes within a documented time budget.
- `verify` passes its checks at the documented thresholds.
- `migration-report.md` matches a golden file modulo timestamps and run IDs.

A new warning code or a changed count → either fix the regression or update the golden file in the PR. Forces explicit ownership of every change to the importer's behavior.

### 16.3 Storage

Corpus files are big (tens of GB). Stored in S3 under `gonext-test-corpus/`, downloaded on demand by CI. Nightly job; not on every PR.

---

## 17. Trade-offs & rejected alternatives

### 17.1 Run a PHP interpreter to support full plugin compat

**Rejected.** This is the most-suggested-by-WP-experts and most-deeply-wrong path. It would mean:
- Shipping a PHP runtime (or worse, a sandboxed subset).
- Implementing WP's hook system in our process so plugins can fire actions/filters.
- Implementing `$wpdb` against our Postgres schema.
- Implementing the WP options table, REST API plumbing, cron, etc.

In practice this is *being WordPress, written in Go*, plus a custom CMS, plus a plugin-translation problem we'd never finish. The whole point of this project is to step off the PHP treadmill. If we did this, we'd ship a slower, less compatible WordPress and gain nothing.

### 17.2 WXR-only importer

**Rejected as the primary mechanism.** WXR is a useful universal fallback, but on a real site it loses:
- Most postmeta (only "exportable" keys included).
- ACF schemas entirely (those are in `acf-field-group` posts that *are* in WXR, but the values are in postmeta that often aren't).
- User password hashes (no way: WXR doesn't include them).
- Custom tables.
- `.htaccess`.

Users would migrate, then discover three weeks later that their entire custom-field structure didn't come over. The dbdirect path costs us more engineering but produces a migration users actually trust.

### 17.3 Live "WP-link" mode as primary (always fetch from WP at runtime)

**Rejected as primary.** Useful as a transition mode (§14) but not as a permanent state. A site that's permanently proxying WP is just slow WordPress with extra moving parts. The proxy path stays as a media-only emergency option (§6).

### 17.4 Convert PHP themes automatically

**Rejected.** PHP-to-React isn't a tractable translation problem for arbitrary code. Themes use `wp_query`, conditional tags, plugin hooks, all of which depend on the surrounding PHP context. Better: pick a theme on our side. The user keeps their old WP site as a visual reference and copies the design intent, not the code.

### 17.5 Best-effort migrate WooCommerce

**Rejected for v1.** WC is its own data model — products, variants, orders, customers, tax, shipping, coupons. Doing it badly is worse than not doing it. Recommendation in the plugin report: keep WP for commerce until we have a proper integration story (v2 at earliest).

### 17.6 Two-way sync (WP ↔ gonext)

**Rejected.** Would require writing into WP, which means running PHP or implementing WP's data invariants in Go. Both are wrong. One-way (WP → gonext) is enough for transition.

### 17.7 Translate page-builder layouts (Elementor, etc.) into native blocks

**Rejected as a hard goal.** Top-level structural conversion (one Elementor section → one `core/group`) is attempted. Beyond that, every page-builder has its own widget ecosystem and visual semantics; perfect conversion would mean reimplementing each. Page-builder users get a documented "you'll need to manually rebuild pages with our editor; the content (text, images) is preserved" path.

### 17.8 Preserve WP integer post IDs as our primary keys

**Rejected.** Two reasons:
1. New posts created via the shim or the editor must work even on a gonext instance that never imported from WP.
2. ID collisions across multiple WP-to-gonext imports are inevitable.

Instead: UUIDs are primary; a `legacy_int_id` column exposes a stable integer-shaped ID per row for the REST shim, allocated from a sequence.

### 17.9 Inline image conversion (re-encode to AVIF/WebP at import)

**Rejected at import time.** Doing it inline blocks the import; doing it per-image makes the migration take days. Instead: import originals, let the standard image pipeline regenerate derivatives lazily as URLs are first requested.

### 17.10 Treat the shim as a long-term API

**Rejected — the shim is a bridge, not a contract.** We don't promise feature parity with future WP REST changes. Our own REST/GraphQL API (see [`05-admin-api.md`](05-admin-api.md)) is the canonical surface. The shim freezes against a specific WP version we test against; we'll bump that version periodically and call out breakage.

---

## 18. Risks

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| Serialized PHP data (postmeta, options) corrupts on parse | high | medium | Use a battle-tested phpser Go library; fuzz-test against real corpus; fall back to raw blob storage on parse failure. |
| Image URL rewrites miss CDNs / odd hostnames | high | high (broken images) | Multi-pattern URL detection; per-site URL-pattern config flag; verification step samples 50 images and HEAD-checks them. |
| Permalink structures we haven't seen | medium | high (SEO) | Parser is data-driven; corpus tests cover common patterns; warn loudly on unknown placeholders. |
| ACF flexible content fields lose ordering | low | medium | Tests with a synthetic field group of 5 layouts; explicit `layout_order` field carried through. |
| Comment threading off-by-one | medium | low | `parent_id` chains rebuilt after all comments imported, in a second pass. |
| 5M-row sites hit memory ceilings | medium | high (migration aborts) | Streaming everywhere; corpus site 8 verifies 50k attachments works on 4GB; periodic profiling. |
| User has 2FA enabled and is locked out | high | high (UX disaster) | Explicit pre-migration email; clear post-migration recovery flow; 30-day grace on application passwords. |
| WP has a quirky table prefix or partial mysql privileges | medium | medium | Prefix configurable; document required GRANTs; discover phase tests reads early and fails clearly. |
| Customer's WP site is on a managed host where DB isn't reachable | high | medium | REST mode + WXR mode as fallbacks; clear UI guidance on choosing. |
| Plugin shortcodes in content with no equivalent | high | low/medium | Stripped + warned; plugin-replacement report flags them; user can install a shortcode-bridge plugin to preserve raw output. |
| WP REST API client expects `yoast_head` (or similar plugin field) | high | low | Document the omission; emit the field as an empty stub when SEO plugin equivalent is installed so it at least parses. |

---

## 19. Open questions

1. **Snapshot storage cost.** A `pg_dump` of a 1M-post site is large. Do we keep snapshots in our DB host's storage, or push to user-supplied S3? Default vs configurable?
2. **WP plugin REST routes (`/wp-json/<namespace>/...`).** Some sites depend heavily on a plugin's custom REST routes for headless. Do we offer a "stub server" mode where these return 501 with a structured error pointing to the replacement plugin, or 404 (current plan)?
3. **Legacy integer IDs across runs.** If a user migrates the same source site twice (e.g. for testing), should `legacy_int_id` be stable? My current bias: yes — keyed by `(source_run, source_id)` but allocated deterministically per source_id so multiple runs collide on the same int. Trade-off: re-runs with different content can confuse the shim.
4. **WXR-by-batch.** WP exports per-author or per-month. Do we accept a directory of WXRs and merge, or require a single file? Leaning: accept a directory.
5. **Live preview during execute.** The wizard currently shows progress but not yet-imported content. Worth a live "as posts come in, render them" view? Cost: a lot of socket plumbing.
6. **Multilingual (Polylang, WPML).** v1 we warn and ignore. Where does this sit in priority — does it block a v1.0 launch for European/multi-lang customers, or is v2 fine? Needs product input.
7. **WooCommerce path.** Strict no-migrate (current plan) vs. a "products only, no orders/customers" subset? Many sites use WC just for a small product catalog. Could be a low-risk add.
8. **2FA migration.** Some users will demand 2FA state be migrated. Doing this safely requires the WP plugin's secret format (Wordfence, Two Factor, etc.); we'd be re-implementing each. Worth the effort or punt?
9. **Plugin replacement guide curation.** Who maintains the curated mapping table (§12.1)? In-tree (code change per plugin) or in a CMS we run? Bias: in-tree for the top 50, CMS-managed for the long tail.
10. **Corpus licensing.** Real-world test sites may have content we can't redistribute. Synthesize realistic but synthetic corpus, or license real sites from friendly users? Probably synthetic.
11. **Re-run guarantees.** If a user re-runs an import after upgrading the source WP, do we expect them to re-run from scratch, or do we offer "diff & apply"? §14 covers nightly sync; one-off diff-apply is a distinct UX. Currently leaning toward "re-run from scratch with a new run_id; rollback the old run if accepted."
12. **Shim versioning.** Pin to WP REST API v5.x semantics? When WP ships a breaking change in v6.x, do we follow or pin? Bias: pin, document, move slowly.

---

## 20. Glossary

- **WXR**: WordPress eXtended RSS — WP's XML export format.
- **phpass**: portable password hashing framework PHP/WP uses (`$P$...` hashes).
- **postmeta**: WP's flat key/value table attached to posts, modeled as EAV.
- **ACF**: Advanced Custom Fields, the dominant WP custom-fields plugin.
- **Shortcode**: `[name attr=...]` markers in WP content that plugins expand server-side.
- **Gutenberg block markers**: HTML comments like `<!-- wp:paragraph -->` that delimit block boundaries inside `post_content`.
- **FSE**: Full Site Editing — WP's block-template architecture (templates/template parts stored as content).
- **migration_map**: our table linking source-side IDs to target-side UUIDs.
- **migration_run**: a UUID that tags every row produced by a single invocation; used for rollback and idempotence.
- **source_run**: alias of migration_run from the perspective of a target row.
- **identity redirect**: a redirect row where `from_path == to_path`; useful as a tombstone if we change URL schemes later.
