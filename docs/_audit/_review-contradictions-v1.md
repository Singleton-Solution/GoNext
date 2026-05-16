# Contradictions Review

## Summary
- 24 total contradictions found.
- 9 are blockers (would prevent build or cause runtime divergence); the remaining 15 are major or minor inconsistencies that can be reconciled with editorial changes.

## Contradictions

### C1: `users.id` primary key type — UUID vs BIGSERIAL
- **Severity**: blocker
- **Docs involved**: 01 §10.3, 06 §2.2, 07 §4.1, 04 §9.4
- **Doc 01 says** (`01-core-cms.md` §10.3): `CREATE TABLE users ( id UUID PRIMARY KEY DEFAULT gen_random_uuid(), ... )`. Every FK from posts/comments/etc. is `UUID REFERENCES users(id)`.
- **Doc 06 says** (`06-auth-permissions.md` §2.2): `CREATE TABLE users ( id BIGSERIAL PRIMARY KEY, uuid UUID NOT NULL DEFAULT gen_random_uuid() UNIQUE, ... )`. Every FK from `user_passwords`, `oauth_grants`, `personal_access_tokens`, etc. is `BIGINT REFERENCES users(id)`.
- **Doc 07 says** (`07-media-performance.md` §4.1): `owner_id BIGINT NOT NULL REFERENCES users(id)`.
- **Doc 04 says** (`04-block-editor.md` §9.4): `author_id BIGINT REFERENCES users(id)`.
- **Why they conflict**: The whole subsystem stack disagrees on whether `users.id` is `UUID` or `BIGSERIAL`. FK columns from doc 01 (`posts.author_id UUID`) cannot reference `users.id BIGINT` from doc 06. This is a database-build blocker.
- **Recommended resolution**: Pick UUID (doc 01) as canonical — it matches the rest of doc 01's tables (`posts`, `terms`, etc., all UUID). Rewrite doc 06's schema to use `id UUID PRIMARY KEY DEFAULT gen_random_uuid()` and convert all `BIGINT REFERENCES users(id)` columns in 04/06/07 accordingly. Drop the separate `uuid` column in 06.

### C2: Primary key type for most other tables — UUID (01/02/06) vs BIGSERIAL (04/07/08)
- **Severity**: blocker
- **Docs involved**: 01 §10, 04 §6/§7/§9.4, 07 §4, 08 §8.1
- **Doc 01 says**: All tables use `UUID PRIMARY KEY DEFAULT gen_random_uuid()` (posts, terms, comments, post_revisions, etc.).
- **Doc 04 says** (`04-block-editor.md` §6/§7/§9.4): `block_patterns`, `reusable_blocks`, `post_autosaves`, `post_revisions` all `BIGSERIAL PRIMARY KEY` with `BIGINT REFERENCES posts(id)`.
- **Doc 07 says**: `media`, `variants`, `collections` use `BIGSERIAL PRIMARY KEY`.
- **Doc 08 says**: `redirects` uses `id uuid PRIMARY KEY`.
- **Why they conflict**: Same blocker as C1 but pervasive — FK from `posts(id) UUID` to a `BIGINT REFERENCES posts(id)` column cannot compile.
- **Recommended resolution**: Standardize on UUID v7 as per 01 §10. Mass-rewrite 04 and 07 schemas. (08's `redirects` already uses UUID.)

### C3: `post_revisions` schema — declared twice with incompatible shapes
- **Severity**: blocker
- **Docs involved**: 01 §4.1/§10.6, 04 §9.4
- **Doc 01 says**: `post_revisions(id UUID, post_id UUID, author_id UUID, kind revision_kind, snapshot JSONB, delta_from UUID, delta JSONB, title TEXT, comment TEXT)` with CHECK `(snapshot IS NOT NULL) <> (delta IS NOT NULL)`. Three revision kinds: `autosave|manual|publish`.
- **Doc 04 says**: `post_revisions(id BIGSERIAL, post_id BIGINT, blocks JSONB, blocks_hash BYTEA, author_id BIGINT, reason TEXT)`. No `kind`, no delta. Doc 04 also defines a separate `post_autosaves` table (with PK `(post_id, user_id)`) implying autosaves are NOT stored in `post_revisions` — contradicting 01.
- **Why they conflict**: Two tables with the same name, different columns, different storage strategies (delta vs full snapshot), different handling of autosaves.
- **Recommended resolution**: Pick doc 01's design (delta-aware, three kinds in one table) — it's more carefully reasoned. Remove `post_autosaves` from doc 04 and instead use `post_revisions WHERE kind='autosave'` as 01.§4.2 specifies.

### C4: Permalinks/redirects tables — single design (01) vs separate WP migration design (08)
- **Severity**: major
- **Docs involved**: 01 §7.3-§7.4, 08 §8.1
- **Doc 01 says**: Two tables — `permalinks(path TEXT PK, post_id UUID, is_current BOOLEAN, created_at)` and `permalink_redirects(from_path TEXT PK, to_post_id UUID, code SMALLINT, created_at)`. Manual redirects also live in `permalink_redirects`.
- **Doc 08 says**: One table `redirects(id uuid PK, from_path text, to_path text, status smallint, source text, source_run uuid, hits bigint, last_hit_at, created_at)` for migration redirects. Target is `to_path` (text), not `to_post_id` (uuid FK).
- **Why they conflict**: Two separate tables that both store redirects, with different column shapes and different target semantics (FK to a post vs. textual path). Operationally there is one "redirect lookup" but two storage models.
- **Recommended resolution**: Unify into a single `redirects` table. Use 08's shape (it has `hits`, `source`, `source_run`, supports manual + migration + htaccess sources, and allows redirecting to external URLs). 01's `permalinks` table (current path → post_id) stays separate as the live forward-lookup table.

### C5: Reusable-block / `core/block` referenced post-ID type
- **Severity**: minor (consequence of C2)
- **Docs involved**: 04 §7
- **Doc 04 says**: A `core/block` ref attribute carries `"ref": 17` (an integer); the `reusable_blocks` table uses `BIGSERIAL PRIMARY KEY`.
- **Doc 01 says**: All core entities use UUID PKs.
- **Why they conflict**: Block-tree references to reusable blocks are integers, but every other ID in the system is a UUID. Cross-cutting code can't assume one or the other.
- **Recommended resolution**: Reusable blocks become UUID-keyed; `ref` is a UUID string.

### C6: Plugin user-capability registration syntax — manifest format differs
- **Severity**: major
- **Docs involved**: 02 §2.2, 06 §6.2
- **Doc 02 says**: Manifest is `manifest.json` JSON. Plugin capability declaration appears under top-level `capabilities` block (with WASM/host-side scopes like `db.read`, `http.fetch`, etc.).
- **Doc 06 says**: Plugin manifest is `plugin.toml` with `[capabilities]` (user-facing caps for humans like `manage_forms`) and `[permissions]` (plugin sandbox caps like `db = ["read:forms"]`).
- **Why they conflict**: Two different manifest formats (JSON vs TOML) and two different vocabularies for "capability". Doc 02's `manifest.json.capabilities` is what doc 06 calls `[permissions]`; doc 06's `[capabilities]` (user-facing) has no slot in doc 02's manifest at all.
- **Recommended resolution**: Standardize on doc 02's `manifest.json`. Add a top-level `user_capabilities` (or `grants_capabilities`) field to the manifest schema for user-facing cap registration. Rewrite doc 06 §6.2 to refer to this field instead of inventing TOML.

### C7: Plugin DB capability vocabulary — `db.read:scope` vs `db:read:posts`
- **Severity**: minor
- **Docs involved**: 02 §6, 06 §14.4
- **Doc 02 says**: Capability names use dots and colons: `db.read` with scopes like `core.posts:read`, `core.terms:read`, `plugin.tables:*`.
- **Doc 06 says**: `db = ["read:forms", "write:forms", "read:posts:public"]` and references `db:read:posts:title`, `db:write:users`, etc.
- **Why they conflict**: Token grammar disagrees. Doc 02 uses `db.read` (action namespace) with separate scope strings; doc 06 colon-encodes the whole thing as `db:read:posts`.
- **Recommended resolution**: Adopt doc 02's grammar (it's the authoritative ABI spec). Rewrite doc 06 examples.

### C8: Block registration field — `render` shape differs
- **Severity**: minor
- **Docs involved**: 02 §7.4, 04 §2.1
- **Doc 02 says**: `registerBlock({ ... serverRender: true })` (a boolean flag on the block definition).
- **Doc 04 says**: `render?: { handler: string /* "core/query.render" or plugin-namespaced */ }` — an object naming the handler.
- **Why they conflict**: The shape of "this is a server-rendered block" differs. Doc 02's example is a simplified sketch that wouldn't let the plugin's WASM render function be discovered.
- **Recommended resolution**: Use doc 04's shape (richer; matches the plugin->host bridge in 04 §5.3). Update doc 02 §7.4 example.

### C9: WP REST shim path — `/api/wp-json/wp/v2/...` (05) vs `/wp-json/wp/v2/...` (08)
- **Severity**: minor
- **Docs involved**: 05 §3.3, 08 §11.1
- **Doc 05 says**: "mounted at `/api/wp-json/wp/v2/...` (and aliased at root `/wp-json/...` for migration ease)".
- **Doc 08 says**: Endpoints under `/wp-json/wp/v2/...` (root-mounted, no `/api/` prefix).
- **Why they conflict**: Slightly inconsistent — 05 says "primary path is `/api/wp-json/...` with root alias"; 08 treats `/wp-json/...` as canonical. WP clients hard-code `/wp-json/...`, so 08's choice is the only one that actually works for migrating clients.
- **Recommended resolution**: Use `/wp-json/wp/v2/...` (no `/api` prefix) as canonical. Drop 05's `/api/wp-json/...` form.

### C10: WP REST shim auth — Basic vs cookie nonce vs JWT, "X-WP-Nonce" disagreement
- **Severity**: major
- **Docs involved**: 05 §3.3, 08 §11.4
- **Doc 05 says**: "Cookie nonces are *not* supported; the shim accepts application passwords (legacy WP) translated into our API-key store, plus Basic auth over TLS."
- **Doc 08 says**: "Cookie + nonce (logged-in admin requests) → Our session cookie. The `X-WP-Nonce` header is accepted and validated as a CSRF token against our session." Plus JWT, plus app passwords mapped to API tokens.
- **Why they conflict**: Direct contradiction on whether the shim accepts `X-WP-Nonce` cookie-nonce auth.
- **Recommended resolution**: Accept doc 08's version (more permissive, more compatible with real WP clients). Update doc 05.

### C11: WP REST shim — plugin REST namespaces (out vs stub)
- **Severity**: minor
- **Docs involved**: 05 §3.3 ("Not supported"), 08 §11.1, 08 §19.2 (open question)
- **Doc 05 says**: "Not supported: ... Anything namespaced under non-`wp/v2`".
- **Doc 08 says**: `/wp-json/<namespace>/<route> (plugin-registered)` is **Not implemented** — explicitly 404. But 08's open question #2 contemplates a stub-server option.
- **Why they conflict**: Mostly aligned (both say not implemented). Minor: 08 leaves the door open to stubs.
- **Recommended resolution**: Both agree on "404 in v1." Reconcile by removing 08's wavering or by both docs adopting the same stub-mode language. Low priority.

### C12: User roles list — doc 06 has 6 roles; doc 05 lists 5; doc 08 maps 6
- **Severity**: minor
- **Docs involved**: 05 §2.8, 06 §6.1, 08 §7.3
- **Doc 06 says**: `subscriber, contributor, author, editor, administrator, super_admin`.
- **Doc 05 says**: "Roles: Administrator · Editor · Author · Contributor · Subs" (only 5 — omits `super_admin`).
- **Doc 08 says**: WP roles map to "admin, editor, author, contributor, subscriber" (uses `admin` not `administrator`).
- **Why they conflict**: Doc 08 uses the slug `admin` where doc 06 uses `administrator`; doc 05's matrix omits `super_admin`. A migration that writes `role='admin'` would not match doc 06's seeded role row.
- **Recommended resolution**: Standardize on doc 06's slugs (`administrator`, including `super_admin`). Update 05's matrix to include `super_admin` (or note its absence is intentional for v1); rewrite 08 §7.3 to use `administrator`.

### C13: Custom-role storage table name
- **Severity**: minor
- **Docs involved**: 05 §2.8, 06 §6
- **Doc 05 says**: "Custom roles are stored in `roles` table (slug, name, capabilities JSONB)."
- **Doc 06 says**: Roles live in `roles(id, slug, name, is_builtin, description)` with capabilities normalized into `role_capabilities(role_id, capability_id)` — not a JSONB column.
- **Why they conflict**: 05 implies a denormalized `capabilities JSONB` column on `roles`; 06 specifies the canonical normalized join table.
- **Recommended resolution**: Doc 06's design wins (normalized join). Fix 05's one-line description.

### C14: Plugin REST routes — `/api/plugins/{slug}/...` consistent? — yes, mostly
- **Severity**: minor (no real conflict but worth checking)
- **Docs involved**: 02 §6.4, 05 §3.1
- **Both agree**: `/api/plugins/{slug}/...` is the mount point for plugin-registered routes. Doc 05 lists it under URL conventions and doc 02 specifies the synthetic hook `http.serve.{slug}` dispatch. Consistent.
- **Note**: This is a CLEAN AREA; listed here only because it was on the checklist.

### C15: Block tree column name and version field — column matches; version missing in 01
- **Severity**: minor
- **Docs involved**: 01 §10.5, 04 §1.4
- **Doc 01 says**: `content_blocks JSONB` on `posts`. Also adds `content_text TEXT` and `content_html TEXT`.
- **Doc 04 says**: `content_blocks JSONB`, plus `content_rendered TEXT`, `content_rendered_at TIMESTAMPTZ`, `content_blocks_hash BYTEA`.
- **Why they conflict**: Doc 01 calls the rendered cache `content_html`; doc 04 calls it `content_rendered` and adds `_at` + `_hash` columns. Doc 04's block JSON format does NOT include a top-level document version field (it's per-block `version: number`), but doc 01 §10.5 stores no document version at all — consistent. The naming mismatch (`content_html` vs `content_rendered`) is the real issue.
- **Recommended resolution**: Pick `content_rendered` (doc 04 — it's more accurate; HTML is just one possible render). Add `content_rendered_at` and `content_blocks_hash` to doc 01's DDL.

### C16: Block-tree root structure — array vs object — consistent
- **Severity**: clean
- **Docs involved**: 04 §1.1
- **Doc 04 says**: `BlockDocument = Block[]`. No wrapping envelope. Doc 01 just says JSONB without specifying shape; doc 04's specification stands. **No contradiction.**

### C17: Custom fields storage destination — `posts.meta` vs sidecar
- **Severity**: minor (consistent rules; minor wording drift)
- **Docs involved**: 01 §9.4, 04 §11, 08 §9
- **Doc 01 says**: `storage: { kind: "meta", path } | { kind: "sidecar", column } | { kind: "column" }`.
- **Doc 04 says**: "Custom fields are stored in `posts.meta` (JSONB) and validated against the content type's field schema on save."
- **Doc 08 says**: ACF fields imported into "our custom field definitions" with field-type mappings. Doesn't repeat storage rules.
- **Why they conflict**: Doc 04's sentence omits the sidecar option (it says only `posts.meta`). Likely a simplification rather than a contradiction; but readers of 04 alone will assume meta JSONB is the only target.
- **Recommended resolution**: Add a one-line note in 04 §11 referring to 01 §9.4's `storage.kind` enum.

### C18: Field type registry / JSON Schema dialect
- **Severity**: minor
- **Docs involved**: 01 §9, 04 §11, 05 §2.6, 08 §9.2
- All four use "JSON Schema" without specifying a draft (2020-12, 2019-09, draft-07). Doc 04 imports `JSONSchema7` from `json-schema`, implying **draft-07**. Doc 05 (`SchemaForm`), doc 01 (field group schemas), and doc 08 (ACF translation) make no claim. Doc 06 §6.1 references "JSON Schema + UI hints" — no draft.
- **Why they conflict**: Doc 04 nails it to draft-07 by Typescript import; the others leave it open. If the field group editor in doc 01 emits draft-2020-12 and doc 04's `JSONSchema7` consumer rejects it, you have a build error.
- **Recommended resolution**: Pick one. Recommend **draft-07** (matches doc 04's existing imports and is the practical interop choice for the openapi-typescript / ajv tooling). Document it in 01 §9.2.

### C19: Settings registry — single registry vs theme/plugin parallel registries
- **Severity**: minor
- **Docs involved**: 02 (plugin settings), 03 §8.3, 05 §2.6
- **Doc 05 says**: One JSON-Schema-driven `Settings.register(...)` registry. Plugins/themes extend.
- **Doc 03 says**: Themes use `defineCustomizerSection({...})` with `controls` of `{id, type: 'select'|'media'|'color', label, choices, default}` — NOT JSON Schema; a parallel format.
- **Doc 02 says**: Plugins ship "settings via the SDK" but Appendix C has `settings: { get<T>(key): T | undefined; set<T>(key, v): void }` — no schema mentioned.
- **Why they conflict**: Theme Customizer "controls" are a different shape from doc 05's Settings registry schemas. Plugin settings have a third (non-)story.
- **Recommended resolution**: Either unify on doc 05's `Settings.register({page, section, key, schema, ui})` (preferred), or explicitly call out that the Customizer is a separate UI/storage and document why it diverges. Currently the reader can't tell which one is authoritative for "site-level theme settings".

### C20: ISR/revalidation trigger mechanism — outbox vs hook listener vs direct call
- **Severity**: minor
- **Docs involved**: 03 §13.4, 04 §5.5, 05 §3.1 (events), 07 §15.2
- **Doc 03 says**: "A small core 'next-revalidate' plugin (in-process, not WASM) listens [on the internal hooks bus] and POSTs to the Next.js `/api/revalidate` endpoint with affected tags."
- **Doc 07 says**: "Go enqueues an Asynq job: 'revalidate' → Job POSTs Next.js webhook" — i.e., it's done via the **transactional outbox** (`cache_invalidations` table → invalidation-worker) not via a hook listener.
- **Doc 04 says**: "ISR: pages are revalidated by tag (`post:42`, `query:posts:type=event`). Dynamic blocks declare the tags they consume; the renderer wires them into Next.js `revalidateTag`."
- **Why they conflict**: Two different mechanisms (hook-bus listener vs Asynq outbox worker). They could coexist but the design should pick one path of record. Doc 07 is the more rigorous/transactional design.
- **Recommended resolution**: Adopt doc 07's transactional-outbox + invalidation-worker. Doc 03 should reference that worker rather than describe a parallel "next-revalidate" plugin.

### C21: Cache tag naming — drift across docs
- **Severity**: minor
- **Docs involved**: 03 §13.4, 04 §5.5, 07 §16.1
- **Doc 03 lists tags**: `post:42`, `posttype:post`, `term:category:42`, `menu:primary`, `theme:active`, `site:*`.
- **Doc 07 lists tags**: `post:{id}`, `post-list:{slug}`, `term:{id}`, `term-tree:{taxonomy}`, `user:{id}`, `media:{id}`, `theme`, `nav:{menu-id}`, `global`.
- **Doc 04 mentions**: `post:42`, `query:posts:type=event`.
- **Why they conflict**: Multiple naming conventions for the same concept:
  - "all-of-a-term-archive": 03 uses `term:category:42` (taxonomy embedded in tag); 07 uses `term:{id}` + `term-tree:{taxonomy}` (split).
  - "navigation": 03 uses `menu:primary` (by location); 07 uses `nav:{menu-id}` (by FK id).
  - "nuke everything": 03 uses `site:*`; 07 uses `global`.
  - "theme": 03 uses `theme:active`; 07 uses `theme`.
  - 04 introduces `query:posts:type=event` not listed in either.
- **Recommended resolution**: Adopt doc 07's vocabulary as canonical (it's the most explicit). Update 03 and 04 to reference 07's tags.

### C22: Background-job tool name in WP REST shim row — "GET /wp-json/wp/v2/menus" and other shim claims
- **Severity**: minor
- **Docs involved**: 05 §3.3, 08 §11.1
- **Doc 05's WP-shim table**: only lists `posts, pages, media, categories, tags, users, comments, settings, types, taxonomies`. Explicitly says "Not supported: ... `/wp-json/wp/v2/block-renderer`, Multisite, anything namespaced under non-`wp/v2`".
- **Doc 08's WP-shim table**: lists those PLUS `menus`, `menu-items`, `blocks` (reusable), `themes`, `plugins`, `statuses`, `search`.
- **Why they conflict**: Different inventory of what the shim covers. Doc 08 is much broader.
- **Recommended resolution**: Doc 08 owns the deep dive (per doc 05's own note), so its inventory wins. Update doc 05's summary table to either match 08 or just refer to 08.

### C23: Media URL format — `/img/{id}/{spec}` vs `/media/{id}` — block image refs
- **Severity**: minor
- **Docs involved**: 04 §5.5 / §14.5, 07 §5.1, 08 §6.2
- **Doc 07 says**: Canonical image URL is `/img/{public_id}/{spec}.{ext}`. Documents/video served directly (not through `/img/`).
- **Doc 08 says**: URL rewriting in importer rewrites old `/wp-content/uploads/...` paths to "new media URLs (via migration_map for attachments)". §14 in doc 07 confirms old URLs get 301'd to `/img/.../w_300,h_200,fit_cover.jpg`.
- **Doc 04 says**: `core/image` block carries `id (resolved), url (rewritten), alt, caption, sizeSlug, href`. Does NOT specify what shape `url` takes.
- **Doc 01 §1.4 says**: `attachment` permalink is `/wp-uploads/{year}/{month}/{filename}` (a third URL shape!)
- **Why they conflict**: Doc 01 §7.2 says attachment permalinks are `/wp-uploads/{year}/{month}/{filename}` — i.e., the public attachment URL is NOT `/img/{id}/...`. Doc 07 says all image rendering goes through `/img/{id}/...`. The two shapes disagree on what a "media URL" is. Block image refs (doc 04) need clarification on which form to embed.
- **Recommended resolution**: Use doc 07's `/img/{public_id}/{spec}` exclusively for image rendering (image blocks, srcsets). The `/wp-uploads/...` URLs are 301 redirects only (per doc 07 §14). Remove the `wp-uploads` permalink entry from doc 01 §7.2, or mark it as "legacy redirect only".

### C24: Plugin DB scoping & RLS — app-level (06) vs SQL roles + RLS (02)
- **Severity**: major
- **Docs involved**: 02 §6.2, 06 §8
- **Doc 02 says**: "Plugins get a Postgres connection running as one of two roles: `gn_plugin_ro_{slug}` ... With row-level security: e.g., a plugin granted `core.posts:read` only sees posts with `status='published'`..." — i.e., **Postgres RLS** is the enforcement boundary.
- **Doc 06 says** (§8): "Decision: app-level for v1. Revisit RLS for multi-tenant in v2." — explicitly rejects RLS.
- **Why they conflict**: Doc 02 declares RLS is the v1 mechanism for plugin DB scoping; doc 06 says RLS is rejected for v1. Direct contradiction.
- **Recommended resolution**: Reconcile by clarifying that doc 06's rejection refers to **user-facing CMS authorization** (the policy engine for posts/users), while doc 02's RLS refers narrowly to **plugin DB role isolation**. They're different layers. Both docs should say this explicitly; right now the words look incompatible.

### C25: Block server-render signature — single export vs `register_hook`-dispatched
- **Severity**: minor
- **Docs involved**: 02 §4.4, 04 §5.3
- **Doc 02 says**: Plugins export exactly one entry point: `hook_handler(ptr, len) -> i64`. All hooks dispatch through this.
- **Doc 04 says**: "In WASM, plugin side, Go/Rust/TS: `export function render_block(jsonBlock: string, jsonContext: string): string`."
- **Why they conflict**: Doc 04 implies a separate WASM export `render_block`; doc 02 says there is exactly one export `hook_handler`. Either doc 04 means "the SDK provides this surface but it dispatches through the single hook_handler" — in which case the doc should say so — or there is a real ABI conflict.
- **Recommended resolution**: Clarify in doc 04 §5.3 that `render_block` is the SDK-level developer surface, and the host dispatch is the `core.filter.block.render.{name}` hook via the single `hook_handler` (matching doc 02 §7.4's earlier `serverRender` description).

## Clean areas

- **Checklist #6 (cache invalidation tags)**: addressed in C21; not "clean" but the conflict is documented.
- **Checklist #10 (Asynq queue names / job handler interface)**: no contradiction found — all docs reference Asynq for background jobs without conflicting names or interfaces; specific queue names aren't pinned anywhere.
- **Checklist #12 (Plugin-registered REST routes path)**: consistent across docs 02 and 05 — both use `/api/plugins/{slug}/...`.
- **Checklist #13 (session storage)**: doc 06 specifies opaque, Redis-backed, SHA-256-hashed key, 256-bit token, `__Host-` cookie. Doc 05 §3.4 ("Cookie session (`__Host-gn_session`, HTTPOnly, SameSite=Lax) Redis-backed") matches.
- **Checklist #14 (user → plugin capability boundary)**: doc 06 §14 spells out "plugin never inherits user caps". Doc 02 §5/§6 honors this (capability tokens are per-plugin, not per-user). Doc 05's REST handlers don't contradict — they describe the user-cap check before hook dispatch, which 06 §14.2 confirms.
- **Checklist #16 (options table)**: doc 01 defines `options(key, value, autoload, namespace)`. Docs 05 and 06 both refer to "settings" (the JSON-Schema-driven registry) which sits ON TOP of the options table. They don't contradict but they don't explicitly say so either; the options table is the underlying KV store for settings registry values. Not a contradiction.
- **Checklist #18 (user roles by name)**: see C12; mostly clean once `admin` vs `administrator` slug is unified.
- **Checklist #20 (GraphQL vs REST boundary)**: doc 05 §3.2 declares the rule (admin = mostly REST + GraphQL for editor/dashboards; public = GraphQL-first; 3rd-party = REST + WP shim). Other docs don't show API examples that violate this rule.
