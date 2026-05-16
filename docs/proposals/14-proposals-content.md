# 14 — Proposals: Content-layer Open Questions (Docs 04–07)

Opinionated answers to the "Open Questions" sections of docs 04 (Block Editor), 05 (Admin API), 06 (Auth & Permissions), and 07 (Media & Performance). Each answer is a recommendation, not a survey. Defers are explicit and have a trigger.

Phases referenced: **P0** Skeleton (0–2mo), **P1** CMS core (2–5mo), **P2** Editor (5–9mo), **P3** Themes (9–12mo), **v1** = P0–P3, **v2** = post-launch.

---

## Summary

| Doc | Questions | High-confidence | Deferred |
|---|---|---|---|
| 04 — Block Editor | 12 | 9 | 3 |
| 05 — Admin API | 10 | 8 | 2 |
| 06 — Auth & Permissions | 10 | 8 | 2 |
| 07 — Media & Performance | 10 | 6 | 4 |
| **Total** | **42** | **31** | **11** |

---

## Doc 04 — Block Editor

### Q04-1: Iframe vs no-iframe canvas
**Source**: doc 04 §18.1 — start in iframe (Gutenberg style) or out-of-iframe and convert later?

**Proposal**: Start **out-of-iframe** in P2. Move to iframe in P3 when `theme.json` and theme stylesheets land and CSS scoping becomes the actual problem the iframe solves.

**Reasoning**: The iframe pays a real cost — slower hot-reload, hostile to React DevTools, harder a11y testing, postMessage plumbing for selection and toolbar — and we don't yet have theme CSS to scope. P3 is when theme styles arrive (per doc 03 / phase plan); converting then is a contained refactor since canvas chrome already isolates from the document tree.

**Confidence**: high
**Reversibility**: moderate

---

### Q04-2: Lexical vs TipTap — final call
**Source**: doc 04 §18.2 — pick the rich-text engine.

**Proposal**: Ship **Lexical**. Skip the one-week dual prototype — the architectural fit is decisive enough that the spike is overhead, not learning.

**Reasoning**: Lexical's node tree maps cleanly to our `BlockDocument` (both are immutable trees with stable IDs and explicit transforms), and Meta's editorial pedigree means realistic perf at long-document sizes. TipTap's ProseMirror schema is more opinionated about inline-vs-block in ways that fight our "blocks are the top-level structural unit, inline formats are a separate concern" rule (§4.2). The prototype week is more useful spent on the block ABI.

**Confidence**: medium
**Reversibility**: expensive

---

### Q04-3: Static block server renderer authoring
**Source**: doc 04 §18.3 — double-write (TS `save` + Go renderer) or codegen Go from a `RenderSpec`?

**Proposal**: **Double-write for core blocks in v1**, single source via `RenderSpec` is v2 R&D. Each core block ships a Go renderer hand-written against a shared snapshot test fixture, with parity tests that diff TS `save` output against Go output on the same `blocks.json`.

**Reasoning**: We have ~20 core blocks (§2.2). 20 hand-written Go functions is a one-time cost measured in days; the codegen path is a custom DSL that has to handle conditionals, attribute coercion, and HTML escaping, and any divergence from React's rendering becomes a debugging nightmare. The parity-test harness catches drift cheaply. Aligns with §17.4 ("Why not codegen `save` renderers").

**Confidence**: high
**Reversibility**: moderate

---

### Q04-4: Per-block edit components — granularity
**Source**: doc 04 §18.4 — one ES module per block, or one module per plugin?

**Proposal**: **One module per block** for plugin-supplied blocks; one bundle for core blocks. The inserter triggers lazy import of a block's edit module only when the block is actually inserted or focused.

**Reasoning**: The whole point of code-splitting is to keep the editor's initial bundle small when a plugin registers 20 blocks but the user only uses 2. Per-plugin coarse bundles regress this; per-block is the natural unit and matches §16.1's bundle strategy. Core is fine as one chunk since you're paying the cost anyway.

**Confidence**: high
**Reversibility**: cheap

---

### Q04-5: Pattern thumbnails — server-side at registration vs on-demand
**Source**: doc 04 §18.5 — when do pattern previews get rendered?

**Proposal**: **On-demand, then cached forever** keyed by `(pattern_id, pattern_version, theme_id)`. First request triggers a headless render via the same Go static renderer + a screenshotter (Chromium in Asynq job); subsequent requests hit the variant cache.

**Reasoning**: Server-side-at-registration sounds clean but breaks the moment a theme switch changes pattern appearance, or a plugin updates a pattern — you'd re-render everything on every plugin update. On-demand has a cold-start problem only on first inserter open; warming via a background job at theme/plugin install solves it cheaply. Aligns with §5.5 caching strategy.

**Confidence**: high
**Reversibility**: cheap

---

### Q04-6: Reusable blocks visibility — per-author or site-wide
**Source**: doc 04 §18.6 — default scope for reusable blocks.

**Proposal**: **Site-wide by default**, with a "Private" toggle that scopes to author. Mirror WP's behavior (familiarity matters for migrators) but make the toggle prominent at save time.

**Reasoning**: The complaint about WP isn't site-wide-by-default, it's that there's no obvious toggle. We fix the UX flaw, keep the migration on-ramp. Author-private as the default surprises team-of-one users who reuse blocks across drafts, which is the majority case.

**Confidence**: high
**Reversibility**: cheap

---

### Q04-7: Block locking inheritance
**Source**: doc 04 §18.7 — do locked containers cascade lock to children?

**Proposal**: **Cascade by default** (`lock.mode = "all"` on a container locks its descendants), with an explicit `lock.cascade = "container-only"` opt-out at the parent level. Children can never override more permissively than their nearest locked ancestor.

**Reasoning**: The mental model "this section is locked" matches expectations far better than "this section is locked but its contents are not." Template designers locking a hero section overwhelmingly mean "users can't edit anything inside." The opt-out covers the rare "frame is locked, slot inside is editable" case without contortion.

**Confidence**: high
**Reversibility**: moderate

---

### Q04-8: Slash command scope
**Source**: doc 04 §18.8 — slash anywhere or only on empty paragraph?

**Proposal**: **Only at the start of an empty paragraph** in v1. Generalize to anywhere-in-text in v2 once we have telemetry on false-positive rates from real usage.

**Reasoning**: Notion-style anywhere-slash is delightful when it works and infuriating when "$50/hr" turns into an autocomplete menu. The cost of being conservative in v1 is users typing `/` and hitting Enter on an empty line — trivial. The cost of being aggressive is editor reliability complaints from day one. Easy to relax later.

**Confidence**: high
**Reversibility**: cheap

---

### Q04-9: Validation strictness on migrated content
**Source**: doc 04 §18.9 — keep WP `core/freeform` as Classic block, or convert?

**Proposal**: **Best-effort conversion at import time, Classic block as fallback**. Run a converter that handles common WP HTML patterns (headings, paragraphs, lists, images, embeds) and only emits a Classic block when the HTML doesn't cleanly decompose. Always log conversion stats per import.

**Reasoning**: Keeping everything as Classic means migrated sites never benefit from native block features — search inside content, block-level styling, block render cache (§5.5). Pure conversion fails on the long tail and silently corrupts edge cases. Tiered approach gives the 80% case native blocks and a safe fallback for the rest. Cross-ref doc 08.

**Confidence**: high
**Reversibility**: cheap

---

### Q04-10: Editor in Next.js admin app vs separate Vite SPA
**Source**: doc 04 §18.10 — where does the editor live?

**Proposal**: **Defer to admin-app design (doc 05 follow-up) in P1**. Resolve trigger: bundle-size budget for `/admin` first-load JS sits at <300KB gzipped without the editor; if the editor's per-route lazy chunk pushes `/admin/posts/:id/edit` first-load past 600KB gzipped, split it into a separate Vite SPA mounted at `/admin/edit`.

**Reasoning**: Genuine fork-in-the-road. A unified Next.js admin gives shared auth, shared layout, shared component library — cheap UX consistency. A separate Vite SPA gives the editor full control over bundling, no Next.js runtime overhead, and shorter cold-load. We don't know the bundle reality until P2 mid-build. Tie the decision to a measurable threshold rather than aesthetics.

**Confidence**: high
**Reversibility**: expensive

---

### Q04-11: Pasting from Gutenberg's clipboard format
**Source**: doc 04 §18.11 — explicit support for Gutenberg clipboard?

**Proposal**: **Yes, v1.** Detect Gutenberg's `<!-- wp:* -->` HTML-comment block markup in pasted HTML and convert via the same WP→DoNext import path as bulk migration.

**Reasoning**: Migration is a force multiplier. A user evaluating us against WP will paste from their existing Gutenberg site as a smoke test; if it works, friction drops to zero. The converter already exists for bulk migration (Q04-9); plumbing it into the paste pipeline (§14.1) is a small wrapper. Marketing value alone justifies it.

**Confidence**: high
**Reversibility**: cheap

---

### Q04-12: Block ABI versioning across plugins
**Source**: doc 04 §18.12 — how do v1-targeted plugins survive a v2 block API?

**Proposal**: Adopt the proposed scheme verbatim: **manifest field `requires.editor: ">=2.0"`** plus a **shim layer that adapts v1 block registrations to v2 at plugin-load time**. Major ABI breaks get a 12-month deprecation window with shim-warning telemetry.

**Reasoning**: This is the standard answer for extension-API evolution and the doc already proposes it. The shim layer is bounded work (one ABI shim per major bump), the manifest requirement gives us a clean rejection path for plugins targeting versions we no longer shim. 12 months matches typical ecosystem norms (Node LTS, Chrome extension manifest v2 sunset).

**Confidence**: high
**Reversibility**: expensive

---

## Doc 05 — Admin API

### Q05-1: OpenAPI auto-generation tool
**Source**: doc 05 §5.1 — swag annotations vs custom struct-tag pass.

**Proposal**: **Custom struct-tag pass in P0.** Drive OpenAPI generation from a single source: the request/response Go structs already have JSON tags, validation tags, and (added) `openapi:"description=...,example=..."` tags. Write a 200-line generator over `go/ast` that emits OpenAPI 3.1.

**Reasoning**: swag's annotation comments are write-only — nobody updates them, they drift, code review can't catch the drift. The struct-tag approach makes the spec a derived artifact of the code that handlers actually use, which means it cannot drift. The custom generator is small because OpenAPI 3.1 schemas align almost 1:1 with Go structs.

**Confidence**: high
**Reversibility**: moderate

---

### Q05-2: Public marketplace API — federated vs centralised
**Source**: doc 05 §5.2 — marketplace topology.

**Proposal**: **Centralised in v1, federation hooks deferred to v2.** Single canonical index at `marketplace.donext.dev`; install URLs hard-code that origin. Plugin manifests carry a `update_url` that *may* point elsewhere, leaving a federation seam without committing to one.

**Reasoning**: Federation's TLS/identity story (plugin signing roots, mirror discovery, trust delegation) is its own multi-quarter design. Premature for v1; we don't have plugin authors yet. The `update_url` seam costs nothing and means a future federated index is a contract change, not a schema migration. Resolve when: third-party self-hosted marketplaces appear in user demand.

**Confidence**: high
**Reversibility**: moderate

---

### Q05-3: Realtime collaboration — WebSocket plumbing in P2 or P3
**Source**: doc 05 §5.3 — when to ship the transport.

**Proposal**: **Defer to phase P3+** (post-v1). Ship neither the WebSocket plumbing nor the CRDT in v1. Defer trigger: resolve when an enterprise customer requests it or when user research shows >20% of editing sessions have concurrent-author conflicts.

**Reasoning**: Doc 04 §10 already commits to Yjs as the eventual CRDT and confirms our `BlockDocument` is CRDT-compatible. The actual blocker for collab is server infrastructure (Yjs provider, presence channel, persistence cadence), not API foreclosure. Building WebSocket plumbing speculatively in P2 risks shipping a transport that's wrong for the real workload. Aligns with doc 04 §10 deferral.

**Confidence**: high
**Reversibility**: cheap

---

### Q05-4: Admin SSR or full SPA after login
**Source**: doc 05 §5.4 — SSR the first list screen?

**Proposal**: **Full SPA after login, no SSR for admin routes.** Skip the P1 SSR spike.

**Reasoning**: Admin TTI is bounded by the editor bundle (Q04-10), not by list-render. SSR-ing one list screen saves ~200ms on the first paint of a route the user visits seconds after the heavy admin shell mounts — savings are noise relative to bundle download. SSR also doubles the cache-invalidation surface (server cache, client cache) for marginal benefit. Public site is a different story and stays SSR.

**Confidence**: high
**Reversibility**: moderate

---

### Q05-5: Capability versioning in JWTs
**Source**: doc 05 §5.5 — bump `caps_v` on role change?

**Proposal**: **Bump `caps_v` per user on any role/capability change; tokens with stale `caps_v` fail the next request and force a refresh.** Refresh tokens carry a freshly-resolved capability snapshot.

**Reasoning**: Accepting a stale window means a demoted admin retains admin rights for up to the access-token TTL — unacceptable for an admin-facing CMS. The cost is one extra Redis lookup per request (already needed for session validation per doc 06 §5.2), so the operational delta is zero. Refresh-token rotation already exists; this just adds a side-effect on role mutation. Confirms doc 06's lean.

**Confidence**: high
**Reversibility**: moderate

---

### Q05-6: CLI bundling
**Source**: doc 05 §5.6 — single binary or separate CLI artifact.

**Proposal**: **Single binary, subcommand-dispatched** (`donext serve`, `donext migrate`, `donext bench`). One release artifact, one go.mod, one Docker image.

**Reasoning**: The "cleaner" separation is purely aesthetic; in practice the CLI and server share 80% of their code (config loading, DB access, model packages). Two binaries means two release pipelines, two SBOMs, two version-skew matrices. Embedded-tools-as-subcommands is the kubectl/git pattern and works fine. Cross-compilation is one `go build` either way.

**Confidence**: high
**Reversibility**: cheap

---

### Q05-7: Persisted GraphQL queries
**Source**: doc 05 §5.7 — codegen-only, or ad-hoc in dev + lock in prod?

**Proposal**: **Persisted-only, codegen-enforced, no escape hatch.** Admin app ships with a generated query manifest (`queries.json`) keyed by hash; the GraphQL endpoint rejects any query whose hash isn't in the manifest. Dev mode auto-extends the manifest from local source.

**Reasoning**: Ad-hoc queries in any environment mean we can't reason about query cost, can't cache reliably, and hand attackers a query-of-death surface. Persisted-only is operationally simpler (whitelist enforcement is trivial, query stats per hash). The "flexibility" argument is for third-party clients — those use REST anyway per the doc.

**Confidence**: high
**Reversibility**: moderate

---

### Q05-8: Webhook signing — HMAC vs Ed25519
**Source**: doc 05 §5.8 — add asymmetric signing?

**Proposal**: **HMAC-SHA256 only in v1. Add Ed25519 in v2.** Ship a `signature_version` header from day one (`v1=hmac-sha256`) so v2 adds a value without breaking v1 verifiers.

**Reasoning**: HMAC covers 99% of webhook receivers — Stripe, GitHub, Slack all standardized on it. Ed25519's value is verification-without-shared-secret, which only matters at scale where a single endpoint receives from many publishers; we don't have that scale in v1. The version header is the cheap forward-compat move that lets us add Ed25519 as a parallel option later without a breaking migration.

**Confidence**: high
**Reversibility**: cheap

---

### Q05-9: Multi-tenant admin
**Source**: doc 05 §5.9 — single-site v1, but don't bake in `tenant_id = 1`.

**Proposal**: **Audit during P1 with a hard rule: every query that touches a tenant-scoped table accepts `tenant_id` as a parameter, even if v1 always passes `1`.** Add a linter that fails CI on hardcoded `tenant_id = 1` outside the bootstrap path.

**Reasoning**: Multitenancy retrofit is the canonical "miserable database migration" — every query, every index, every cache key. Adding the parameter from day one costs nothing; removing it later costs everything. The linter prevents creep. Whether multitenancy ever ships is doc 00's call (§17.2 — undecided); this proposal makes that decision deferrable.

**Confidence**: high
**Reversibility**: expensive

---

### Q05-10: Plugin admin route allocation
**Source**: doc 05 §5.10 — `/admin/{section}/{plugin}/...` vs `/admin/plugins/{plugin}/...`?

**Proposal**: **`/admin/plugins/{plugin}/...` with an allow-list for top-level promotion.** Plugins default to namespaced routes. A site admin can promote specific plugins via a setting (`promoted_plugins`) to appear at `/admin/{section}/...`.

**Reasoning**: The collision risk of section-first is real and costly — two plugins both wanting `/admin/forms` is a routing conflict that bites users at install time, not at design time. Namespacing eliminates the class entirely. The promotion path covers the "this plugin is part of our brand identity" case (e.g., WooCommerce-tier integrations) without granting it to every plugin. Doc 05's lean is correct.

**Confidence**: high
**Reversibility**: cheap

---

## Doc 06 — Auth & Permissions

### Q06-1: Step-up auth as first-class concept
**Source**: doc 06 §18.1 — generalize "recent auth required" into a middleware?

**Proposal**: **Yes — ship `policy.RequireRecentAuth(duration)` in v1.** Standard middleware that checks the session's `last_auth_at`; on miss, the API returns `401` with `WWW-Authenticate: ReAuth realm="step-up"` and the admin app shows a modal re-login (not full-page).

**Reasoning**: We already need step-up for 2FA disable, impersonation, password change, and PAT creation (§5.4, §15) — that's enough callsites to justify the abstraction. The modal UX matches what users expect from Google/GitHub when they revisit a sensitive setting; full-page re-login is for unauthenticated sessions. Same primitive, several caps benefit.

**Confidence**: high
**Reversibility**: cheap

---

### Q06-2: Passkeys required for `super_admin`?
**Source**: doc 06 §18.2 — passkey-or-bust for the most privileged role?

**Proposal**: **Required in hosted SaaS, strongly recommended in self-host (warning banner, never hard-blocked).** Self-host owners can configure `require_passkey_for_super_admin = true` via the env or admin settings; default `false` in self-host, `true` in hosted.

**Reasoning**: Doc 06's tentative answer is correct. Self-host hostility is a real failure mode — the user with a broken passkey on a self-hosted instance has no recovery vendor. Hosted has recovery support. The banner ("Your super_admin account is not protected by a passkey — read more") creates social pressure without a footgun. Operator-controllable flag means security-conscious self-hosters get the same protection.

**Confidence**: high
**Reversibility**: cheap

---

### Q06-3: OIDC for the public site
**Source**: doc 06 §18.3 — Google login for commenters?

**Proposal**: **Yes, v1, with mandatory captcha on first-comment from a fresh OIDC identity** (not on subsequent comments). Treat OIDC sign-in as a low-trust identity until the user has one human-moderated comment.

**Reasoning**: OIDC dramatically reduces friction for genuine readers — the right thing for engagement. The bot-army risk is real but localised to comment spam, which is a content-moderation problem we have to solve anyway. The "first comment captcha" pattern is what Disqus and Hacker News effectively do via shadowban+manual review. Cheap mitigation, big UX win.

**Confidence**: medium
**Reversibility**: cheap

---

### Q06-4: Public user discovery via API
**Source**: doc 06 §18.4 — `GET /api/users` for author pages, what columns?

**Proposal**: **Dedicated `public_profile` view exposes exactly: `handle`, `display_name`, `avatar_url`, `bio` (markdown), `joined_year` (not month/day), `post_count`.** No email, no role, no last_seen, no IP, no real name. Locked down by SQL view + explicit allowlist in the API serializer; security-reviewed in P1.

**Reasoning**: The right answer is "the smallest set that supports an author page." `joined_year` instead of full date avoids profiling. No role exposure prevents enumeration of admins. SQL view + serializer allowlist is defense-in-depth: a future developer can't accidentally leak a column by adding it to the user model. Aligns with doc 06's plan.

**Confidence**: high
**Reversibility**: cheap

---

### Q06-5: PAT scope granularity
**Source**: doc 06 §18.5 — when do scopes need "posts in category X" granularity?

**Proposal**: **Defer to phase v2.** Resolve trigger: when >5% of issued PATs are observed in audit logs being used against data their owner doesn't intend to expose, or when a paying customer asks. Until then, PATs inherit the user's full capabilities, scoped only by coarse permissions (`read`, `write`, `admin`).

**Reasoning**: Object-level PAT scoping is genuinely hard — the scope language has to be expressive enough to match real intent ("only my drafts in category Foo") but constrained enough to evaluate cheaply per request. Premature for v1 because we don't yet have evidence users want it. WP plugins offering this are niche.

**Confidence**: medium
**Reversibility**: cheap

---

### Q06-6: Audit log access — separate compliance role
**Source**: doc 06 §18.6 — gate global audit access behind a distinct role?

**Proposal**: **Yes — add `compliance_officer` role in v1**, separate from `admin`. `admin` sees their own audit entries plus entries about resources they own. `compliance_officer` sees global audit, can't create or modify content. Roles are independent (a user can be both).

**Reasoning**: Conflating "can administer the site" with "can read everyone's audit log" makes compliance review uncomfortable in any org bigger than 5 people — admins audit themselves. Splitting in v1 is cheap (one role definition, one capability rename); splitting in v1.5 means an awkward migration of existing capability assignments. Cross-ref doc 06 §6.1 built-in roles.

**Confidence**: high
**Reversibility**: moderate

---

### Q06-7: Plugin-issued sessions
**Source**: doc 06 §18.7 — can a plugin authenticate users (LDAP plugin etc.)?

**Proposal**: **Defer concrete design to plugin-runtime RFC in P3**, but reserve the seam: ship `auth.providers` hook in the plugin manifest schema from v1, no implementations allowed yet (validation rejects unknown providers). Trigger: resolve when the v1 WASM plugin runtime has been in production for one quarter and we have a real third-party identity request.

**Reasoning**: This is a security-critical extension point — getting it wrong means a plugin can mint sessions for any user. That review deserves its own design doc, with explicit threat modeling and a sandbox for plugin-side credential handling. Reserving the manifest field prevents future plugins from squatting on conflicting names.

**Confidence**: high
**Reversibility**: expensive

---

### Q06-8: WebAuthn account recovery
**Source**: doc 06 §18.8 — recovery when all passkeys + no other factor are lost.

**Proposal**: **Email magic link → limited "recovery session" that can ONLY register a new passkey or set a password, nothing else.** Recovery session expires in 15 minutes, requires re-confirmation by email on each login attempt during the recovery window, and triggers a delayed (24h) notification to all session emails ("recovery was initiated").

**Reasoning**: The 24h notification window is the critical anti-takeover: if an attacker compromises the email account, the legitimate user has 24h to spot the alert before the new passkey can be used to lock them out fully (we keep the old passkeys active for that window). The limited session prevents recovery from being a backdoor for everything else. Matches doc 06's lean with an explicit anti-takeover delay.

**Confidence**: high
**Reversibility**: cheap

---

### Q06-9: Rate limit storage — Redis outage behavior
**Source**: doc 06 §18.9 — fail-open or fail-closed?

**Proposal**: **Fail-closed for admin/auth routes (login, password change, 2FA, PAT issuance). Fail-open with elevated logging + an in-memory token bucket as fallback for public read routes.** Redis outage triggers a paging alert regardless.

**Reasoning**: Doc 06's lean is right; this just adds the in-memory fallback for public reads so a Redis outage doesn't 503 the public site. The in-memory bucket is per-instance (less effective than Redis) but better than no protection. Admin fail-closed accepts brief login-blocking during Redis outages — a 5-minute admin login lockout is preferable to an attacker exploiting the outage window for credential stuffing.

**Confidence**: high
**Reversibility**: cheap

---

### Q06-10: GDPR export format
**Source**: doc 06 §18.10 — JSON-LD vs W3C DataPortability vs ZIP.

**Proposal**: **ZIP of JSON files + media, with top-level `manifest.json`.** File layout: `manifest.json`, `profile.json`, `posts/*.json`, `comments.json`, `media/*` (original files), `audit.json`. Manifest declares schema version + per-file schemas.

**Reasoning**: Doc 06's lean is correct. Standards-compliance isn't the user need — operator-readability is. Anyone receiving a GDPR export is going to want to grep through it or import it elsewhere; JSON-LD's `@context` machinery adds friction for both. The manifest gives us forward-compat without standards lock-in.

**Confidence**: high
**Reversibility**: cheap

---

## Doc 07 — Media & Performance

### Q07-1: AI alt-text vendor lock-in
**Source**: doc 07 §27.1 — built-in default vs bring-your-own-key.

**Proposal**: **Bring-your-own-key only in v1**, with a pluggable `alt_text_provider` interface and a one-click OpenAI provider in the marketplace. No bundled default. Self-hosters configure their own credentials; SaaS offers a managed-billing add-on with a metered OpenAI proxy in v2.

**Reasoning**: Bundling a default makes us an LLM reseller with the cost-control burden — one user uploading 10k photos a day costs us real money, and we don't have the metering infrastructure in v1. BYOK is honest about the cost and lets self-hosters use their preferred vendor (Anthropic, local LLaVA, etc.). Marketplace plugin path means UX feels integrated without us underwriting compute.

**Confidence**: high
**Reversibility**: cheap

---

### Q07-2: Animated AVIF / WebP / APNG
**Source**: doc 07 §27.2 — GIF transcode target.

**Proposal**: **Animated WebP as the default GIF transcode target**, with original GIF preserved as the fallback variant served when `Accept` doesn't include `image/webp` (vanishingly rare in 2026 — desktop+mobile coverage is essentially universal).

**Reasoning**: Animated WebP is the only format with both broad browser support and meaningful compression vs GIF (typically 40–60% smaller). Animated AVIF support still lags in iOS Safari edge cases. APNG is universal but doesn't compress nearly as well. Doc 07's lean is correct.

**Confidence**: high
**Reversibility**: cheap

---

### Q07-3: Edge runtime for image proxy (WASM-vips at edge)
**Source**: doc 07 §27.3 — subset of resize/format at the edge?

**Proposal**: **Defer to v2 explicitly.** Resolve trigger: when image-proxy origin p95 latency exceeds the SLA we publish (likely <150ms warm, <800ms cold), or when a SaaS customer pays for premium image performance.

**Reasoning**: The complexity is real (WASM-vips toolchain, edge cache split between resized and non-resized, fallback to origin when the edge can't handle a format) and the benefit is only meaningful for the cold-variant case, which is rare in a well-warmed system. Origin libvips with single-flight (§5.3a) and CDN caching handles the warm case fine. Park.

**Confidence**: high
**Reversibility**: expensive

---

### Q07-4: Per-tenant CDN config
**Source**: doc 07 §27.4 — vanity domains, custom WAF.

**Proposal**: **Defer to v2 SaaS feature.** Resolve trigger: when SaaS tier pricing requires it as a differentiator (typically Pro+ plans), or when an enterprise customer requests vanity-domain images. Self-host already gets full CDN control by definition.

**Reasoning**: Per-tenant CDN routing is a SaaS-only concern (multi-tenant) and is gated on doc 00's multitenancy decision anyway. Even if multitenancy ships, the customer demand for vanity-domain images is small until you're at >100 paying tenants. Premature.

**Confidence**: medium
**Reversibility**: moderate

---

### Q07-5: DASH support for video
**Source**: doc 07 §27.5 — HLS only or both?

**Proposal**: **HLS only in v1 and v2. Skip DASH unless a paying customer with a specific Android-ecosystem requirement asks.**

**Reasoning**: HLS works on every modern browser via hls.js polyfill on the consumer side, native everywhere on Apple. DASH's advantages (codec flexibility, lower overhead) are theoretical for the CMS use case where most video is short-form H.264. Two manifest formats means doubled testing, doubled storage, doubled CDN cache footprint, and zero user benefit until proven otherwise. Doc 07's lean is correct.

**Confidence**: high
**Reversibility**: cheap

---

### Q07-6: Bandwidth metering / quota
**Source**: doc 07 §27.6 — per-tenant bandwidth caps.

**Proposal**: **Defer to v2 SaaS launch.** Resolve trigger: design and implement before first SaaS dollar of revenue, not before. Reserve the hook now: every image-proxy and asset response goes through a single `accounting.RecordEgress(tenant_id, bytes)` middleware that's a no-op in v1.

**Reasoning**: Bandwidth metering is a billing primitive, not a CMS primitive. Building it before SaaS launch is wasted work — pricing model and metering granularity (per-asset? per-variant? per-bandwidth-tier?) are downstream of pricing decisions that don't exist yet. The no-op middleware now means v2 is a one-file change.

**Confidence**: high
**Reversibility**: moderate

---

### Q07-7: Cache-tag header support across CDNs
**Source**: doc 07 §27.7 — Cloudflare's `Cache-Tag` vs Fastly's `Surrogate-Key` vs Bunny's weaker support.

**Proposal**: **Ship a `CDNDriver` interface in v1**, modelled on the existing storage-driver abstraction. Implementations: `cloudflare`, `fastly`, `bunny`, `noop`. Each driver translates our internal tag-set into the CDN's native header. Default driver: `cloudflare`. Self-hosters configure via env.

**Reasoning**: We already abstract storage drivers; cache invalidation has the same shape (one interface, multiple vendor implementations). Doing this in v1 forces clean separation between "we have invalidation tags" and "how we tell the CDN about them" — much cheaper than retrofitting when Fastly's first SaaS customer arrives. Aligns with §16.1 canonical tag naming.

**Confidence**: high
**Reversibility**: moderate

---

### Q07-8: Should the proxy support remote URLs (`/img/proxy?url=...`)
**Source**: doc 07 §27.8 — convenience vs SSRF.

**Proposal**: **No in v1. v2 as a gated plugin only, never core.** Hard policy: an attacker who controls a content field cannot cause our infrastructure to fetch arbitrary URLs. If a plugin implements this, it must use a vetted egress proxy with an allowlist.

**Reasoning**: SSRF + cache-poisoning is the worst-case combination — one bad URL can poison every CDN POP. Migration use cases (importing a WP site's external image URLs) are better served by a one-shot import job that fetches once, stores, and writes back. Doc 07's lean is correct, just stronger.

**Confidence**: high
**Reversibility**: cheap

---

### Q07-9: Background blur / saliency-based smart-crop
**Source**: doc 07 §27.9 — `fit=cover` smart-crop focus point.

**Proposal**: **Defer to v2.** Resolve trigger: when user-uploaded "wrong crop" complaints are >2% of media-related support tickets, or when a paying customer explicitly asks. v1 ships face-detection-only smart crop (libvips has built-in support, cheap to add).

**Reasoning**: Saliency models are a real ML dependency (model file, GPU or slow CPU inference, model lifecycle management) for a quality-of-life feature. Face detection covers 70% of the "wrong crop" complaints in practice (people in hero shots). Full saliency is gold-plating until we have evidence it matters.

**Confidence**: medium
**Reversibility**: cheap

---

### Q07-10: Per-request CPU accounting for image proxy
**Source**: doc 07 §27.10 — billing per render.

**Proposal**: **Defer to v2 SaaS launch — bundle with Q07-6 (bandwidth metering).** Reserve hook: `accounting.RecordCPU(tenant_id, duration)` wrapping each libvips invocation, no-op in v1.

**Reasoning**: Same logic as bandwidth metering — it's a billing primitive. The hook costs nothing; the implementation is downstream of pricing. The global limiter (existing §5) already prevents a hostile customer from starving everyone else; what's missing is the ability to charge them differentially, which is a SaaS concern.

**Confidence**: high
**Reversibility**: moderate

---

## Cross-doc Notes

- **Phasing alignment**: deferred items cluster around v2 SaaS economics (Q07-4, Q07-6, Q07-10) and post-v1 advanced features (Q05-3 collab, Q06-5 PAT granularity, Q06-7 plugin sessions, Q07-3 edge-WASM). This is the right shape for a v1 — defer revenue-only and complex-extension work, ship the security and correctness baseline.

- **Reusable patterns surfaced**:
  - "Manifest field / hook reserved now, implementation later" appears in Q05-2 (federation), Q05-8 (signing version), Q06-7 (auth providers), Q07-6 (accounting), Q07-10 (CPU accounting). This is a cheap forward-compat pattern; standardize it as a design heuristic.
  - "Driver abstraction now, second driver later" appears in Q07-7 (CDN drivers) and mirrors existing storage drivers. Consider applying to: search backend (Postgres FTS → Meilisearch per doc 00), cache backend (Redis → ?), queue backend (Asynq → ?).
  - "Sensible default + explicit opt-out" appears in Q04-6 (reusable blocks), Q04-7 (lock cascade), Q06-2 (passkey requirement). When in doubt about defaults, mirror this pattern.

- **Open questions explicitly NOT resolved here**: anything contingent on doc 00's multitenancy call (Q05-9 carries the load-bearing hedge); anything contingent on the post-v1 plugin runtime maturity (Q06-7).

- **Decisions worth promoting into the relevant doc body** (not just open-questions answers):
  - Q04-3 parity-test harness for static block renderers — belongs in §8.1 validation.
  - Q05-1 struct-tag-driven OpenAPI generator — belongs in §2 API surface.
  - Q06-1 `RequireRecentAuth` middleware — belongs in §7.4.
  - Q07-7 `CDNDriver` interface — belongs in §16.

End.
