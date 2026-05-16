# GoNext Roadmap

This roadmap tracks the path to v1. It is derived from [doc 00 §6](./docs/00-architecture-overview.md) and the strategic proposals in [docs/proposals/14-proposals-strategic.md](./docs/proposals/14-proposals-strategic.md).

Each phase corresponds to a GitHub milestone. Each milestone collects the issues for that phase. Filter by milestone in the [issues view](https://github.com/Singleton-Solution/GoNext/issues).

## Phase 0 — Skeleton (months 0-2)

**Goal**: validate the stack end-to-end. If anything is wrong with our architectural assumptions, find out now, not in month 18.

- [ ] Monorepo bootstrap (Go workspace + pnpm workspace + Makefile + Docker Compose).
- [ ] Go HTTP server with `/healthz`, `/readyz`, Postgres connection, basic migrations.
- [ ] One REST endpoint: `GET /api/v1/posts` returning seeded data.
- [ ] Next.js public app rendering one post via the API.
- [ ] Block JSON tree storage in `posts.content_blocks` (JSONB).
- [ ] Render one block (`core/paragraph`) end-to-end.
- [ ] Basic auth (signup, login, session in Redis).
- [ ] Admin shell with one page (post list).
- [ ] CI pipeline: lint + unit tests on PR.

**Exit criteria**: `docker-compose up` boots a working stack; you can create a user, log in, see one post on the admin list, and render it at a public URL.

## Phase 1 — CMS Core (months 2-5)

**Goal**: the content management feature set people expect.

- [ ] Posts, pages, custom post types per [doc 01](./docs/01-core-cms.md).
- [ ] Taxonomies (categories, tags, hierarchical custom).
- [ ] Comments with moderation.
- [ ] Media library + upload (S3-compatible).
- [ ] Permalinks + redirect history.
- [ ] Users, roles, capabilities per [doc 06](./docs/06-auth-permissions.md).
- [ ] Admin CRUD UI for all of the above.
- [ ] REST API per [doc 05](./docs/05-admin-api.md) §3.1.
- [ ] WP REST shim per [doc 05](./docs/05-admin-api.md) §3.3 (the basics).
- [ ] Search via Postgres FTS per [doc 01](./docs/01-core-cms.md) §8.
- [ ] Settings registry per [doc 05](./docs/05-admin-api.md) §2.6.

**Exit criteria**: usable enough to run a personal blog. Dogfooding starts here.

## Phase 2 — Editor (months 5-9)

**Goal**: a block editor people actually want to use.

- [ ] Block JSON tree data model per [doc 04](./docs/04-block-editor.md) §1.
- [ ] ~20 core blocks (paragraph, heading, image, list, quote, code, table, gallery, embed, columns, group, button, separator, spacer, cover, media-text, video, audio, html, more).
- [ ] Lexical-based rich text per [doc 04](./docs/04-block-editor.md) §4.
- [ ] Block inserter, toolbar, inspector sidebar, list view.
- [ ] Multi-select, drag-drop reorder.
- [ ] Autosave, revisions.
- [ ] Server-side render walker per [doc 04](./docs/04-block-editor.md) §5.
- [ ] Custom fields panel (JSON Schema driven) per [doc 04](./docs/04-block-editor.md) §11.

**Exit criteria**: write a long-form post with mixed content and have it render correctly on the public site.

## Phase 3 — Themes (months 9-12)

**Goal**: developers can build and ship themes.

- [ ] Theme manifest + `theme.json` per [doc 03](./docs/03-theme-system.md) §2-3.
- [ ] Template hierarchy resolver per [doc 03](./docs/03-theme-system.md) §4.
- [ ] Classic themes (file-defined templates).
- [ ] Block themes (DB-stored templates with file fallback) per [doc 03](./docs/03-theme-system.md) §6.
- [ ] Customizer per [doc 03](./docs/03-theme-system.md) §8.
- [ ] Theme installer + switcher.
- [ ] Reference theme: **wpc-hello** (block theme).
- [ ] Reference theme: **wpc-pro** (classic theme).
- [ ] Theme SDK package per [doc 03](./docs/03-theme-system.md) §15.

**Exit criteria**: a third-party developer can build a theme by reading docs and the SDK alone.

## Phase 4 — Plugins (months 12-18)

**Goal**: the ecosystem-enabling layer. This is the riskiest phase.

- [ ] WASM runtime via wazero per [doc 02](./docs/02-plugin-system.md) §4.
- [ ] Hook bus (actions + filters) per [doc 02](./docs/02-plugin-system.md) §5.
- [ ] Host ABI: db, http.fetch, http.serve, kv, queue, cache.invalidate, email, audit.emit, secrets, cron per [doc 02](./docs/02-plugin-system.md) §6.
- [ ] Frontend extension points (admin pages, custom blocks, dynamic blocks) per [doc 02](./docs/02-plugin-system.md) §7.
- [ ] Plugin SDKs: Go (TinyGo), Rust, TypeScript (Javy).
- [ ] Plugin signing + manifest validation per [doc 02](./docs/02-plugin-system.md) §10.
- [ ] Plugin marketplace MVP.
- [ ] Reference plugin: **wpc-seo** (sitemap, meta, schema.org).
- [ ] Reference plugin: **wpc-forms** (form builder + submissions).
- [ ] Reference plugin: **wpc-shop** (lightweight ecommerce + Stripe).

**Exit criteria**: a third-party developer can build and publish a signed plugin that adds a new feature without server access.

## Phase 5 — Migration (months 18-21)

**Goal**: WordPress users can switch.

- [ ] WP importer per [doc 08](./docs/08-migration-compat.md) — dbdirect, WXR, REST modes.
- [ ] HTML→block tree converter.
- [ ] Permalink redirects preserved.
- [ ] WP REST compat shim full coverage per [doc 08](./docs/08-migration-compat.md) §11.
- [ ] Plugin replacement guide generator.
- [ ] phpass→argon2id password rehash on login.
- [ ] ACF field migration (best-effort).
- [ ] 10-site migration corpus + CI verification gate.

**Exit criteria**: take 10 typical WP sites, run them through the importer, and reach 95% content fidelity in the defined cohort (see [proposal S15](./docs/proposals/14-proposals-strategic.md)).

## Phase 6 — Polish & Launch (months 21-24)

**Goal**: v1 release.

- [ ] Performance: meet the published SLOs in [proposal S16](./docs/proposals/14-proposals-strategic.md).
- [ ] Docs site at docs.gonext.dev (or eventual brand domain) with 4 audiences: site owner, plugin author, theme author, API reference.
- [ ] Security audit by external firm.
- [ ] First-party marketing site running on GoNext itself.
- [ ] Marketplace launch with ≥10 community plugins/themes.
- [ ] Hosted SaaS beta (post-1.0; not required for v1 self-host release).

**Exit criteria**: see [proposal S17](./docs/proposals/14-proposals-strategic.md). No date commitment.

## Post-v1

Not on the v1 roadmap:
- Multisite / multi-tenancy
- Real-time collaborative editing (Yjs CRDT)
- Multi-region active-active
- Hosted SaaS (separate workstream after v1 self-host ships)
- WooCommerce-class ecommerce
- WPML-class multilingual

See [doc 06 §17](./docs/06-auth-permissions.md), [doc 04 §10](./docs/04-block-editor.md) for what's already designed in stubs.

## How to influence the roadmap

- Open a `design-discussion` issue.
- Comment on phase milestones.
- Propose ADRs in [`/adr`](./adr).

Major reprioritization requires maintainer approval and a public ADR.
