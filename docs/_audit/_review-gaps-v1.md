# Gaps Review

Cross-document gap analysis of `00-architecture-overview.md` through `08-migration-compat.md` (~11,400 lines).
The README already self-identifies 8 missing topics; this review confirms those, adds more, and additionally
catalogs interface gaps between subsystems and topics that are claimed in a doc but underspecified.

## Summary
- Type A (missing subsystems): 14 items
- Type B (interface gaps): 17 items
- Type C (underspecified): 13 items
- Critical-path gaps (block v1): 16 items (listed at end)

Note on scope: I treated as "covered" anything that has a dedicated section, schema, ABI/contract, or
state diagram. I treated as "underspecified" anything that's mentioned in prose with operational
hand-waving but no contract, schema, or algorithm a builder could implement against.

---

## Type A — Missing Subsystems

### A1: Deployment & DevOps
- **What's missing**: No doc owns deployment. There is no description of: container image layout (Go binary + Next public + Next admin + worker, or one image, or three), required init/migration sequence on boot, recommended K8s topology (StatefulSet for Postgres, Deployments for stateless, HPA), single-binary self-host story (the architecture overview claims "single binary" but Next.js is a separate process — how do they coexist?), multi-region considerations (Postgres replication, sticky sessions, ISR behavior across regions), blue/green or canary deploys, zero-downtime migration handling, env-var/config surface, secrets injection, health/readiness endpoints, graceful shutdown of in-flight WASM invocations beyond the 5s drain mentioned in 02 §3.4.
- **Why it matters**: Self-hosters and SaaS operators are first-day users. Without this, nobody can run it; reference plugin and migration work (docs 02 and 08) implicitly assume a deployment shape that isn't defined.
- **Priority**: P0
- **Suggested doc number**: doc 09 — deployment & ops (or extend 00)

### A2: Observability
- **What's missing**: Structured logging conventions (fields, levels, redaction), metrics surface (Prometheus/OTEL counters/histograms for HTTP, DB, cache, hooks, WASM fuel/timeouts, queue depth, ISR webhook), distributed tracing across Next → Go → WASM → Postgres, error tracking (Sentry-style), dashboards. The phrase "production code grows error wrapping, tracing, metrics" appears in 02 §5.6 as a hand-wave; nothing else owns this.
- **Why it matters**: A plugin sandbox with fuel/timeouts and a tag-invalidation system are unmaintainable without metrics. The cache invalidation worker (07 §16.2) silently retries — without metrics this becomes data-loss-shaped.
- **Priority**: P0
- **Suggested doc number**: doc 10 — observability

### A3: Testing strategy
- **What's missing**: Doc 02 §11.4 sketches a plugin test harness; 08 §16 covers a migration corpus. Nothing covers core: unit (Go packages, React components), integration (HTTP + DB), end-to-end (admin flows, editor flows, public site rendering), block snapshot tests, theme contract tests (does theme X satisfy the template-hierarchy contract?), load tests for hooks/dispatcher/ISR webhook, fuzzing of the importer and block validator, the actual CI pipeline.
- **Why it matters**: A CMS where third parties ship code into your runtime cannot ship without contract tests at every boundary.
- **Priority**: P0
- **Suggested doc number**: doc 11 — testing & CI

### A4: Background jobs (Asynq) — no owning doc
- **What's missing**: Asynq is invoked by 01, 02, 04, 05, 06, 07, 08. No doc owns: queue topology (which queues exist; priorities; default vs media-transcode vs invalidation vs migrate vs webhooks), retry policy per queue (07 mentions "1, 5, 30s, 2m, 10m, 1h, 6h" for webhooks — that's it), dead-letter handling (where DLQ lives, how it's surfaced, how it's replayed), idempotency keys at the job level, cron-leader election (02 §15 calls this out as an open question and never resolves it), job-level rate limits, payload size limits, observability hooks. The README itself flags this.
- **Why it matters**: At least 8 features in the design ride on Asynq. Without a job model, every doc is making conflicting assumptions.
- **Priority**: P0
- **Suggested doc number**: doc 12 — jobs & cron

### A5: Email & transactional notifications
- **What's missing**: Plugins get an `email` capability (02 §6.5) and 06 sends password reset/verification mail (§10–11). No doc specifies: SMTP/SES/Postmark adapter, DKIM/SPF/DMARC operational guidance, template engine + rendering, transactional vs marketing separation, suppression lists, bounce/complaint handling, dev-mode email capture, rate limits, unsubscribe / one-click compliance, deliverability metrics. Plugin email is gated only by "host rate-limit and templates" (02 §6.6) — neither is defined.
- **Why it matters**: Password reset, verification, comment notifications, plugin "your form was submitted" — all rely on this. Failures here look like data loss.
- **Priority**: P1
- **Suggested doc number**: doc 13 — email

### A6: i18n at depth (content translation, RTL, locale fallback)
- **What's missing**: 05 §2.14 covers admin UI strings, RTL via Tailwind, plugin translation merging. Nothing covers: content translation as a Polylang/WPML replacement (01 open question #2 explicitly defers this), URL strategy for translated content (`/fr/...` vs subdomain vs query), per-locale slug uniqueness, translation memory / fallback chains, theme.json locale variants, block-level translatable strings, RTL story in the editor itself (mirror toolbars, BiDi text inside blocks), translator workflow (who can edit which locale?), localized media (different image per locale).
- **Why it matters**: "Site in multiple languages" is one of the top three WP plugin categories.
- **Priority**: P1
- **Suggested doc number**: doc 14 — content i18n

### A7: Security beyond auth (CSP, headers, XSS, secrets, supply chain)
- **What's missing**: A site-wide security baseline. 02 §7.6 describes plugin-frontend CSP narrowly; nothing defines the full set of security headers (HSTS, Permissions-Policy, COOP/COEP/CORP, Referrer-Policy, X-Content-Type-Options, X-Frame-Options/CSP frame-ancestors). XSS sanitization for block attributes, theme-injected HTML, plugin-rendered HTML — not unified. Secret management: 02 §2.2 manifest has a `secrets.keys` field; no doc explains where the host keeps those secrets, how operators configure them, rotation, scoping, encryption at rest. Pepper for password hashing (06 §3.3) hand-waves where it lives. Supply-chain: 02 §10.2 covers plugin signing; nothing covers theme signing, core release signing, dependency pinning.
- **Why it matters**: A WP successor whose security story is "CSP is mentioned" loses every comparison.
- **Priority**: P0
- **Suggested doc number**: doc 15 — security baseline & secrets

### A8: Disaster recovery & backups
- **What's missing**: 07 §26 has a single bullet: "Postgres backed up with PITR; S3 has versioning; `gonext restore` tested quarterly." That's a sentence, not a design. Missing: backup cadence policy, retention, encryption, off-site copy strategy, what's included (Postgres, S3, Redis state, secrets, plugin bundles, customizer overrides, ISR cache?), RPO/RTO targets, restore validation, point-in-time recovery procedure, partial-restore (single post, single tenant), the relationship between Postgres backups and `cache_invalidations` outbox table (a restored DB has stale caches — what's the protocol?).
- **Why it matters**: Without a DR plan there is no realistic production deployment.
- **Priority**: P0
- **Suggested doc number**: doc 16 — DR/backup (or merge into A1 deployment)

### A9: Rate limiting / abuse beyond login
- **What's missing**: 05 §3.5 covers API rate limits well, and 06 covers login rate limits. Gaps: per-plugin egress quotas (02 mentions per-manifest allowlist, no quota numbers), per-plugin REST endpoint quotas (a plugin can serve `/api/plugins/gn-seo/*` — no doc limits how many requests these handle), media upload abuse, comment abuse, search query abuse (FTS can be expensive), GraphQL query cost limiting (05 §3.2 doesn't mention complexity limits at all), webhook signing-secret rotation, anti-CSRF beyond admin (the public site receiving plugin REST POSTs).
- **Why it matters**: A WP clone with weak abuse mitigation gets DoS'd within a week of public launch.
- **Priority**: P1
- **Suggested doc number**: extend 05 §3.5 or new doc

### A10: eCommerce (WooCommerce-equivalent)
- **What's missing**: Acknowledged as out-of-scope in README. There is no design for: product CPT shape, cart/checkout/order/payment integration boundaries, tax/shipping, inventory, refunds. Even at "v2+ punt" granularity, the design should reserve the post-type slugs, the capability slugs (`manage_woocommerce`, `view_orders`), and the meta namespace (`gn-shop.*`) so that v1 doesn't paint v2 into a corner.
- **Why it matters**: WooCommerce represents half of WP's pull. Even a deferred design is needed to avoid breaking-change debt.
- **Priority**: P2 (defer build, but reserve the surface area in v1)
- **Suggested doc number**: doc 17 — eCommerce (stub for now)

### A11: Forms (Contact Form 7 / Gravity Forms replacement)
- **What's missing**: Forms are the second-most-used WP plugin category. 02 names a "gn-forms" reference plugin (§7.5) but no doc owns: a form data model, submissions store, spam (honeypot/captcha integration boundary), file uploads in forms, GDPR for submissions, notification routing, conditional logic, multi-step flows.
- **Why it matters**: Listed in the README as a P4 reference plugin; need a contract for what "first-party forms" means even if implementation is in a plugin.
- **Priority**: P1
- **Suggested doc number**: doc 18 — forms (or treat as reference plugin spec)

### A12: SEO (Yoast / Rank Math replacement story)
- **What's missing**: The phrase "gn-seo" appears 40+ times across 02 as a worked example, but no doc says what the SEO surface is: title/description fields location (the meta JSONB already shows `core.seo.title`, so partially decided in 01 §3.3), sitemap generation (mentioned as plugin route in 02), robots.txt, structured data/schema.org, OpenGraph/Twitter, canonical URLs, redirects (touched in 08 §8 but only for migration), noindex/nofollow surface, breadcrumb data, internal-link analysis. Decide which lives in core vs. a reference plugin.
- **Why it matters**: SEO migration is the #1 reason companies don't switch CMSes. The shape needs to be coherent across core + first-party plugin.
- **Priority**: P1
- **Suggested doc number**: extend 01 or new doc 19

### A13: Accessibility as a project-wide policy
- **What's missing**: 04 §15 covers editor a11y well. 05 §2.11 has a one-paragraph "WCAG 2.2 AA target." Theme doc 03 mentions accessibility in `theme.json` non-goals. No doc owns: the WCAG conformance target as a binding requirement on themes, the accessibility test gate in CI, the a11y contract for plugin-supplied admin pages, the a11y story for the public renderer (heading order across blocks, skip links, focus visibility, prefers-reduced-motion compliance), a11y of media (alt-text policy is in 07 §10, but no enforcement), captcha alternatives, audit cadence.
- **Why it matters**: WordPress is currently the accessibility punchline of the CMS world; a clone that doesn't take this seriously inherits the same reputation.
- **Priority**: P1
- **Suggested doc number**: doc 20 — accessibility policy

### A14: Billing / SaaS layer
- **What's missing**: Acknowledged as out-of-scope in 00 (Open Question #5) and README. If the hosted offering matters, missing: tenant model (06 §17.6 rejects per-row tenant_id for v1), billing integration boundary, plan/quota enforcement (storage, bandwidth, plugin slots, build minutes), per-tenant secrets, multi-tenant data isolation guarantees, white-label theming.
- **Why it matters**: The team needs to decide this before doc 06's auth model is finalized — adding tenancy after the fact is the single most painful retrofit in CMS history.
- **Priority**: P0 (decision), P2 (build) — the decision blocks every other doc's open questions about multi-tenancy

---

## Type B — Interface Gaps

### B1: Plugin block registration ↔ block editor model (WASM/JS bridge underspecified)
- **Doc 02 says** (§7.4): a plugin's `web/` ES module calls `registerBlock(...)`. If `serverRender: true`, the renderer calls `core.filter.block.render.{name}` and the plugin's WASM half handles it.
- **Doc 04 says** (§2.1): `registerBlockType` is the editor-side API with full props (`render: { handler: "..." }` is the server-side handler indicator).
- **What's missing**: The contract that ties these two APIs together. Specifically: is `registerBlock` from 02 the same function as `registerBlockType` from 04, with a thinner surface? How does the editor-side `BlockTypeDefinition.attributes` (JSON Schema) get reconciled with the manifest-declared block schema? How does a plugin's WASM handler receive the block attributes — through which hook payload shape? Is the `serverRender` block hydrated client-side, and if so, who ships the hydration JS? How are "dynamic block" plugin renders cached (07 §15.5 has block render cache keyed by `(block_type, attrs_hash, content_version)` — what's the version-tag declaration mechanism for plugin blocks)?
- **Priority**: P0
- **Recommended owner**: doc 04, with a new "§2.4 Plugin-registered blocks" referencing 02

### B2: Theme ↔ plugin-provided blocks
- **Doc 03 says**: themes render the block tree via `<BlockTreeRenderer>` (§15.3). Theme.json declares allowed blocks.
- **Doc 02 says**: plugins can register blocks (§7.4).
- **Doc 04 says** nothing about plugin-supplied blocks in `theme.json`'s `supports.blocks` list.
- **What's missing**: Can a theme allowlist/blocklist plugin-supplied blocks? If a theme.json says `supports.blocks: ["core/*"]`, are plugin blocks excluded? When a theme is missing a plugin's block (plugin uninstalled mid-life), what does the renderer do — placeholder, error, swap? Style-variation inheritance (`styles?` on a BlockTypeDefinition) — can themes register variations on plugin blocks?
- **Priority**: P1
- **Recommended owner**: doc 03 with cross-link to 02/04

### B3: Block editor ↔ post-type registry — "available blocks for this post type"
- **Doc 01 §1.3** has `post_types` registry with `supports` (title, content, etc.) but no field that constrains blocks per post type.
- **Doc 04 §13.2** has templates with locked blocks for a CPT.
- **Doc 02 §7.4** lets plugins register blocks globally.
- **What's missing**: The endpoint that returns "blocks available for post type X" — is this `GET /api/v1/post-types/{name}/blocks`? How is it computed (theme.json supports + post_type.allowed_blocks + plugin registrations − any deactivated plugins)? Where is the per-post-type allowed-blocks list stored — `post_types.allowed_blocks JSONB` (not in DDL)? Does block category filtering happen here too?
- **Priority**: P0
- **Recommended owner**: doc 01 (registry shape) + doc 04 (consumer API)

### B4: ISR revalidation ↔ plugin mutations
- **Doc 07 §15.2** describes ISR webhook fired by core on post change.
- **Doc 02** lets plugins write to their own tables (`plg_seo_*`) and serve their own routes — these can affect rendered HTML (e.g., SEO meta tags injected via `the_head` filter).
- **What's missing**: How does a plugin signal "this mutation should revalidate paths X, Y, Z and tags A, B"? Is there a host call `cache_invalidate(tags, paths)`? Is it in the ABI (no — 02 Appendix A doesn't list it)? Does the plugin write to the `cache_invalidations` outbox directly (only if it has `db.write` on a core table, which is forbidden)? Without this, plugin admin pages that change site-visible state will produce silent staleness.
- **Priority**: P0
- **Recommended owner**: doc 02 (add a `cache` capability) + doc 07 (specify the contract)

### B5: Search backend ↔ themes/admin/plugin queries
- **Doc 01 §8** specifies Postgres FTS with weighted tsvector.
- **Doc 03** themes can render archive pages.
- **Doc 05** admin has a global search box (`Top bar: search`).
- **What's missing**: Is there a unified `SearchService` API? What's the query DSL — does it accept a string only or filters (status, type, author, term)? Faceted-search support? Does the admin search hit the same FTS index as public search (which would surface drafts to authors)? When 01 §8.4 talks about Meilisearch v2, what abstraction prevents a refactor across all three consumers?
- **Priority**: P1
- **Recommended owner**: doc 01 (publish a search API contract)

### B6: Plugin admin pages ↔ admin shell
- **Doc 02 §7.3** plugins register routes via `registerAdminRoute({ path, component })`.
- **Doc 05 §2.1** plugins inject menu items via `AdminMenu.register({...})`.
- **What's missing**: Are these the same registry? In 02 the menu is declarative in `manifest.json`, in 05 the menu is registered at runtime via `AdminMenu.register` — which wins? How does a plugin admin page get the admin shell's chrome (sidebar, top bar, breadcrumb, theme tokens)? What component slots are exposed (header actions, page actions)? How are plugin admin pages auth-gated — does the admin shell read `capability` from the route declaration and 403 client-side, while the API independently enforces server-side? Is the menu order stable across plugin install/uninstall?
- **Priority**: P0
- **Recommended owner**: doc 05 (define the slot/registry contract; reference 02 for the manifest source)

### B7: Plugin migrations ↔ core migration system
- **Doc 02 §3.3** plugin migrations are SQL files under `migrations/`, run on activate, namespaced `plg_{slug}_*`, validated by a static SQL linter.
- **Doc 01** specifies the core schema; nothing in 01 says how plugin migrations integrate.
- **What's missing**: What runs the migrations — does core use `golang-migrate`/`pressly/goose`/something else (never named)? Is the plugin-migration runner the same engine, or a separate one? How are core-version-pinned migrations resolved (plugin says "needs core 1.x", core upgrades to 2.0 with a breaking schema change — what happens)? Is there a global lock when both core and plugin migrations need to run on boot? Migration rollback (02 §3.4 mentions downgrade): how does this coordinate with a core schema rollback?
- **Priority**: P1
- **Recommended owner**: doc 01 (own the migration engine spec) + doc 02 (reference it)

### B8: Plugin-defined CPTs ↔ capabilities
- **Doc 01 §1.3** plugins register `post_types` with a `Capabilities` map.
- **Doc 06 §6.2** plugins register user-facing capabilities under `[capabilities]` in `plugin.toml`.
- **What's missing**: The bridge. When a plugin registers a `post_type` named `event`, who creates `edit_events`, `publish_events`, `delete_events`, etc.? Are they auto-derived from the post-type slug, or must the plugin manifest list them under `[capabilities]`? Who decides which existing roles inherit them — defaults to `administrator`-only per 06 §6.2, but for a `product` CPT the typical expectation is "Editor can edit products." Is there a `default_role_grants` field in the registration? When the plugin uninstalls, do orphan capabilities cascade (cap rows delete → role_capabilities cascade → users lose them)?
- **Priority**: P0
- **Recommended owner**: doc 06 (specify the auto-generation rule) with cross-link to 01 §1.3 and 02 §3

### B9: Block patterns ↔ theme installation
- **Doc 04 §6** block patterns are stored as `block_pattern` posts (per 01 §1.4 table).
- **Doc 03** themes ship templates, template parts, and (presumably?) patterns.
- **What's missing**: How do theme-shipped patterns get into the DB? On install — seeded like template parts (3 §6.2)? Are they versioned per theme? If a user customizes a theme-shipped pattern, does the theme update wipe it (the user-customization-vs-update story for patterns is the same problem as for templates but isn't addressed)? Can plugins ship patterns? Are patterns localized?
- **Priority**: P1
- **Recommended owner**: doc 03 (own theme-shipped patterns) + doc 04 (own the pattern data model already)

### B10: Plugin REST endpoints ↔ auth middleware
- **Doc 02 §6.4** plugin routes mounted at `/api/plugins/{slug}/...`, dispatched via hook bus.
- **Doc 05 §3.4** all admin/API endpoints carry cookie or JWT or API key auth.
- **Doc 06 §7.4** middleware enforces caps.
- **What's missing**: Does the plugin's route inherit the auth middleware automatically? Plugin manifest declares `http.serve.routes` but doesn't declare which capability the requester needs. Is the default "any authenticated user" or "must hold a plugin-declared capability"? Can a plugin route be opted into `public: true` (e.g., `/sitemap.xml`)? CSRF — plugin routes that are state-changing need the same CSRF check as core. Rate limiting — do plugin routes count against per-plugin quotas (A9) or shared API quotas (05 §3.5)?
- **Priority**: P0
- **Recommended owner**: doc 02 (extend `http.serve` route declaration with auth requirements) and doc 06 (extend middleware)

### B11: WP REST shim ↔ Application Passwords
- **Doc 05 §3.3 / Auth**: "the shim accepts application passwords (legacy WP) translated into our API-key store, plus Basic auth over TLS."
- **Doc 08 §11.4**: covers WP REST auth at a similarly high level.
- **What's missing**: The storage spec for Application Passwords. WP's behavior: each app password is a label + 24-char password attached to a user. Are these mapped 1:1 to entries in 05's `api_keys` table? What scopes do they get — the user's full capability set, or a restricted "Application Password scope"? How are they listed/revoked from `/admin/profile/sessions`? Does the WP mobile app's auth probe (`Test-Auth`) work? The WP authorization-application discovery endpoint?
- **Priority**: P1 (P0 if you care about WP mobile app users migrating)
- **Recommended owner**: doc 08 (own the WP-shim auth section) with cross-link to 06

### B12: Custom-field value ↔ REST and GraphQL exposure
- **Doc 01 §3** custom-field groups stored in `posts.meta` JSONB with namespaces, plus a JSON-Schema-driven field-group registry (§9).
- **Doc 05 §3.1–3.2** REST and GraphQL return post objects.
- **What's missing**: How does a custom field appear in REST responses — is it `meta.{namespace}.{key}` by default? Is there a "register this field as a top-level REST property" mechanism (akin to WP's `register_rest_field`)? In GraphQL, are custom fields strongly typed (the field group's JSON Schema → a generated GraphQL type), or returned as `JSON` scalar? Are fields hidden by default per visibility flag? Per-capability gating on field read/write?
- **Priority**: P1
- **Recommended owner**: doc 01 (specify default REST/GraphQL projection rules) with cross-link to 05

### B13: Plugin secrets ↔ host secret store
- **Doc 02 §2.2** plugin manifest can declare `secrets.keys: ["google_indexing_api_token"]`. The host serves these to the plugin.
- **What's missing**: Where the host stores secrets, who can set them (admin UI? CLI?), encryption at rest, rotation, per-tenant isolation if SaaS, dev-mode mock values, how the secret value is delivered to the WASM module (host call `secret_get(name)`?), what happens on plugin uninstall (purge the secrets), and an audit trail for secret reads. This couples directly to A7 (general secret management).
- **Priority**: P0
- **Recommended owner**: doc 02 (specify the host call) + A7 (the underlying store)

### B14: Audit log ↔ plugin activity
- **Doc 06 §13** audit log table with `actor_kind` supporting `plugin`.
- **Doc 02** plugin SDK doesn't expose an audit-emit API in the ABI (Appendix A).
- **What's missing**: Either (a) auto-emission — every plugin host call that mutates state automatically writes an audit row, and the plugin can't write arbitrary audit entries; or (b) an explicit `audit.write` capability and host call. The design needs to pick one and specify it. Without this, "plugin X did Y" is unattributable.
- **Priority**: P1
- **Recommended owner**: doc 06 (specify how plugin actions appear) + doc 02 (add the host call or auto-emit rule)

### B15: theme.json customizer overrides ↔ core settings
- **Doc 03 §8.2** customizer writes to `site_options.theme_customizations[themeId]`.
- **Doc 05 §2.6** has a settings registry persisted to the `options` table (01 §10.11).
- **What's missing**: Is `site_options` the same table as `options`? Different keys, same table? Why two names? Is the JSON schema for `theme_customizations` registered in the settings registry (so the admin can render generic UI) or only via 03's `defineCustomizerSection`? When two surfaces (Site Editor + Customizer) can mutate the same conceptual setting (e.g., site title), what's the source of truth?
- **Priority**: P1
- **Recommended owner**: doc 03 (be specific about the storage table) + doc 01 (reconcile `options` naming)

### B16: Block-editor autosave ↔ post revisions
- **Doc 04 §9.2** autosaves write to `post_autosaves` (per post, per user, PK is composite, no history).
- **Doc 04 §9.4** revisions written on every manual save.
- **Doc 01 §4** revisions stored as posts with `status='revision'` and `parent_id` (a different design).
- **What's missing**: Two revision storage models contradict each other. 01 §4 says "revisions are posts"; 04 §9.4 says `post_revisions` is a separate table. Are autosaves promoted to revisions on manual save? When the user closes the tab and reopens, the autosave UI prompt logic (04 §9.2: "if post_autosaves is newer than posts.updated_at") doesn't account for the timestamp of the most-recent revision. What if a draft is auto-saved by user A, then manually saved by user B with no merge?
- **Priority**: P1
- **Recommended owner**: doc 01 and doc 04 — pick one storage model; this is a real inconsistency

### B17: Theme blocks/style variations ↔ plugin block style variations
- **Doc 04 §2.1** BlockTypeDefinition has `styles?` for variations registered by themes.
- **Doc 03** theme.json supports style variations.
- **Doc 02** plugins can register blocks.
- **What's missing**: Style variations on plugin blocks — who can register them (theme only via theme.json? plugin author via `registerBlockStyleVariation`?), and how the editor surfaces them. This is small but real DX.
- **Priority**: P2
- **Recommended owner**: doc 04

---

## Type C — Underspecified

### C1: 02 §12 — Plugin marketplace
- **What's hand-waved**: The doc explicitly says "data model only — out of scope for design," which is honest. But the data model itself doesn't cover: payments, payouts to plugin authors, refund policy, the publisher signup/verification flow, takedown / abuse procedure, registry availability SLO.
- **What's needed to build**: For v1 you can defer the whole marketplace, but the registry record (slug ownership, version index) is on the critical path because plugin signing depends on it (02 §10.2). The minimal "registry as read-only mirror of GitHub-hosted manifests" alternative isn't articulated.
- **Priority**: P2 for marketplace, P0 for the signing-registry slice

### C2: 02 §11.2 — Debugging WASM
- **What's hand-waved**: A single paragraph plus tooling claim ("source maps via DWARF for Rust/Go-via-TinyGo"). Real DX details missing: how the developer attaches a debugger to a running plugin invocation in dev mode, breakpoint support across WASM/host boundary, how panics surface in the admin UI for the operator.
- **What's needed to build**: Concrete dev-mode flow (likely `gonext plugin dev` keeps the WASM uncompiled and re-instantiates on each request; spell that out), trap-to-stack-trace reverse mapping, the log/error sink in dev vs prod.
- **Priority**: P1

### C3: 04 §10 — Real-time collaboration v2
- **What's hand-waved**: Correctly deferred to v2 (acceptable). However §10.2 lists "what changes in our model when collab lands" without saying which v1 data-model decisions are forward-compatible.
- **What's needed to build**: Annotate revisions, autosave, and conflict-resolution decisions with "v1 only, replaced by Yjs" vs "stable across v1→v2" — otherwise the v1 build will paint itself into a corner.
- **Priority**: P1

### C4: 03 §13.5 — Edge runtime feasibility
- **What's hand-waved**: "Probably no for v1, possibly yes once we trim the runtime." No concrete blockers listed.
- **What's needed to build**: An honest inventory of which Next.js features the theme runtime uses that aren't edge-compatible (Node APIs, dynamic require, etc.), so theme authors don't write themes assuming edge.
- **Priority**: P2

### C5: 05 §3.2 — GraphQL: dataloader, depth/complexity, persisted queries
- **What's hand-waved**: Schema sample is detailed; runtime concerns are absent. Mentions "we use [gqlgen]" but no query depth limits, no complexity scoring, no persisted query story, no dataloader spec, no subscription model (and 05 §4 rejects WebSockets in favor of SSE — does that mean no GraphQL subscriptions?).
- **What's needed to build**: Default depth limit, complexity formula, persisted-query mechanism for the public site, dataloader patterns for N+1.
- **Priority**: P0 (DoS prevention) for limits; P1 for persisted queries

### C6: 05 §3.6 — Webhooks
- **What's hand-waved**: Event list and retry schedule are concrete. Subscription model is admin-only (no per-plugin webhook subscriptions described). Signing-secret rotation, replay protection, max body size, IP allowlisting — not specified.
- **What's needed to build**: A complete signing spec (HMAC scheme, header names, timestamp tolerance), endpoint health check + auto-disable on consecutive failures, the subscription registration API for plugins.
- **Priority**: P1

### C7: 07 §5.3 — Eager vs lazy image generation
- **What's hand-waved**: "Three sizes eager, rest lazy." Which three? Why those three? How is "lazy" gated against thundering-herd (50 viewers hitting `/img/X/1920x1080.avif` simultaneously on a cold variant)?
- **What's needed to build**: The eager-set definition, request-coalescing for the on-demand generator (singleflight pattern), and a back-pressure story when the variant queue saturates.
- **Priority**: P1

### C8: 07 §19 — Edge rendering
- **What's hand-waved**: Mentions Cloudflare Workers as a future option; doesn't pin which subset of the renderer works at the edge or what fallback exists.
- **What's needed to build**: Same as C4 (inventory of edge-incompatible deps), plus a routing rule (which paths edge-render vs origin-render).
- **Priority**: P2

### C9: 06 §15 — Impersonation
- **What's hand-waved**: Mechanism + audit logging are spec'd; the UI for "exit impersonation" and the way it interacts with concurrent admin tabs is not. Re-auth window is "last 5 minutes" — pulled from where? Stored where?
- **What's needed to build**: Session-cookie shape during impersonation (probably a separate cookie name with both IDs), the "exit" affordance (banner with one-click exit), tab-sync, the recent-auth proof storage (session metadata or a separate `recent_auth_events` table).
- **Priority**: P1

### C10: 06 §12.3 — Anomaly detection
- **What's hand-waved**: "We watch for [list of signals]" — no detector implementation, no signal storage, no threshold tuning, no alert routing.
- **What's needed to build**: Either acknowledge as deferred to "later" (but the doc presents it as if it ships), or specify the events table, the rules engine, and the response (lock the account? require step-up auth? notify the user?).
- **Priority**: P2 — but mark as deferred to avoid the false impression of shipped behavior

### C11: 08 §14 — Incremental sync (transition mode)
- **What's hand-waved**: Mechanism (nightly Asynq diff against WP REST). Conflict handling between source-WP edits and destination edits during the transition window is absent. The cutover step (§14.3) is two paragraphs.
- **What's needed to build**: Write-direction rules during sync (we say "one-way WP→ours during transition"; what enforces that? Is the admin read-only during the window?), conflict-detection on the dest side, cutover checklist with rollback gate.
- **Priority**: P1 — this is an important hosted-migration feature

### C12: 08 §13 — Rollback
- **What's hand-waved**: Postgres snapshot + S3 versioning. The "snapshot" is the same Postgres backup as in A8 — they need to be reconciled. The time window (§13.4: "30 days default") doesn't say where it's configured.
- **What's needed to build**: Surface a `migration_snapshot` lifecycle distinct from `daily_backup`, with the operator UI for "rollback to pre-migration."
- **Priority**: P1

### C13: 03 §12.3 — Theme installer security
- **What's hand-waved**: "Themes ship as npm or zip" — what is the install-time validation surface? The classic-theme path runs arbitrary user-supplied React components on the public Next.js renderer. There is no theme-signing equivalent to plugin signing (02 §10.2). Themes have less attack surface than plugins but not zero (they exfiltrate via SSR fetches, they can XSS the public site).
- **What's needed to build**: Either a theme signing/review story analogous to plugins, or a written justification of why themes are trusted differently. CSP applies to plugins (02 §7.6); does it apply to theme-emitted JS? The bundle is on the same origin.
- **Priority**: P0

---

## Critical-path checklist

Every P0 gap below must be closed before any serious build commits to a particular shape. Items marked
"decision" can be a one-page resolution; items marked "design" need a full subsection.

1. A1 Deployment & DevOps — design.
2. A2 Observability — design.
3. A3 Testing strategy — design.
4. A4 Background jobs (Asynq) — design.
5. A7 Security baseline & secrets — design.
6. A8 Disaster recovery & backups — design.
7. A14 Billing/SaaS layer — decision (do we have tenants in v1? blocks 06 §17.6 finalization).
8. B1 Plugin block registration ↔ block editor bridge — design.
9. B3 "Available blocks for post type" endpoint — design.
10. B4 ISR revalidation from plugin mutations — design (add `cache` capability).
11. B6 Plugin admin pages ↔ admin shell slot contract — design.
12. B8 Plugin-defined CPTs ↔ capabilities auto-generation — design.
13. B10 Plugin REST endpoints ↔ auth middleware inheritance — design (extend `http.serve` route decl).
14. B13 Plugin secrets ↔ host secret store — design.
15. C5 GraphQL depth/complexity limits — design (DoS prevention).
16. C13 Theme installer security — decision + design.

Lower-priority items (P1/P2) are tracked above and are real but not block-the-build. Several of them (A5
email, A6 content i18n, A9 abuse beyond login, A11 forms, A12 SEO, A13 a11y policy, B11 Application
Passwords) are visible-from-day-one features in the WordPress comparison and should be on the roadmap,
even if not on the v1 build wall.
