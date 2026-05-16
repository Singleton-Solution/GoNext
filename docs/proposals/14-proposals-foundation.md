# 14 — Proposals: Foundation Open Questions (docs 00–03)

> Opinionated answers to the unresolved questions in docs 00 (architecture overview), 01 (core CMS), 02 (plugin system), and 03 (theme system). One proposal per question. Reasoning is short. Confidence and reversibility are tagged so the team knows where to push back.

## Summary

| Doc | Questions | High-confidence answers | Deferred |
|---|---|---|---|
| 00 — Architecture | 5 | 4 | 0 |
| 01 — Core CMS | 10 | 6 | 2 |
| 02 — Plugin System | 12 | 9 | 1 |
| 03 — Theme System | 10 | 7 | 1 |
| **Total** | **37** | **26** | **4** |

"High-confidence" means the proposal's `Confidence` field is `high`. "Deferred" means the proposal explicitly punts the decision to a later phase pending real data.

---

## Doc 00 — Architecture Overview

### Q00-1: Admin in same Next.js app or separate?
**Source**: doc 00 §7 ("Separate is cleaner; same simplifies auth/deploy.")

**Proposal**: Build admin as a **separate Next.js app** in the same monorepo, served from `/wp-admin` (path-based, same origin) via the Go server's reverse proxy. Share UI primitives, auth helpers, and the API client through workspace packages, but ship distinct route trees, distinct bundles, and distinct deploy artifacts.

**Reasoning**: The public site is SSR/SSG/ISR with React Server Components and aggressive caching; the admin is an interactive SPA-shaped workload with auth-gated routes and no caching. Mixing them in one Next.js app forces awkward route-group gymnastics, leaks admin bundle weight into public pages, and couples release cadences. Same-origin serving keeps the cookie session story identical to the "one app" case at zero extra complexity.

**Confidence**: high
**Reversibility**: moderate (consolidating later is a refactor, not a re-architecture).

---

### Q00-2: WASM plugin DX — SDK per language, or one canonical?
**Source**: doc 00 §7 ("do we ship an SDK per language, or one canonical (Go/Rust) and let JS authors use AssemblyScript?")

**Proposal**: Ship **two first-class SDKs at launch: Rust and TypeScript (via Javy/QuickJS)**. Defer Go, AssemblyScript, and C/C++ to community contribution with a documented but unsupported ABI binding.

**Reasoning**: Rust is the obvious choice for serious plugin authors (smallest WASM, best ergonomics, mature). TypeScript via Javy covers the WordPress demographic crossover — the people most likely to write plugins already know JS, not Rust. Three SDKs would split documentation effort and slow ABI evolution; one SDK leaves the JS majority out in the cold. The ABI itself (doc 02 §8) is language-agnostic, so a third party can build any other SDK without our help.

**Confidence**: high
**Reversibility**: cheap (adding SDKs later is purely additive).

---

### Q00-3: Multi-tenancy — v2 or never?
**Source**: doc 00 §7 ("is multisite v2 or never? Affects schema (tenant_id columns).")

**Proposal**: **v2, but bake `site_id` into the schema from day 1** with a default value of `1` and a hard-coded single-site assumption in v1 code paths. Do not build the routing, admin UI, or plugin scoping for multisite in v1.

**Reasoning**: Retrofitting `site_id` onto a populated production schema is the single most painful migration in the WordPress world — it is the reason WP multisite is still a second-class citizen 15 years on. The column is cheap (4 bytes per row, indexed). The feature is expensive (admin, billing, plugin scoping per doc 02 §15.12, cache key scoping). Pay the column cost now, defer the feature work until SaaS is real.

**Confidence**: high
**Reversibility**: expensive (adding `site_id` post-launch is the worst-case schema migration).

---

### Q00-4: Licensing — GPL or Apache/MIT?
**Source**: doc 00 §7 ("GPL (like WP, friendly to ecosystem) or Apache/MIT (friendlier to commercial)?")

**Proposal**: **Apache 2.0 for core**. **GPLv2-or-later** is an explicit non-goal: we are not running WP plugins (doc 00 §1), so the GPL-inheritance argument that protects WordPress's ecosystem does not apply to us.

**Reasoning**: Apache 2.0 has the patent grant GPL lacks, is acceptable to virtually every commercial integrator (Apple, Google, AWS ship Apache code at scale), and does not viralize plugin code. The plugin ecosystem is sandboxed by WASM, not by license. MIT is a viable second choice but ships without the patent grant, which is a real risk for a CMS that touches user content and may attract litigation.

**Confidence**: high
**Reversibility**: expensive (relicensing after contributions accumulate requires a CLA process or rewrite).

---

### Q00-5: Hosted offering — SaaS day 1, or self-host only?
**Source**: doc 00 §7 ("SaaS from day 1, or self-host only? Affects priorities heavily.")

**Proposal**: **Self-host only through P5; soft-launch managed SaaS as a thin operator layer in P6**. The architecture must remain SaaS-shaped (multisite-ready per Q00-3, capability-scoped plugins per doc 02, S3 media per doc 00 §2) but no SaaS-specific code ships in v1.

**Reasoning**: SaaS day 1 forces billing, plan tiers, tenant isolation, abuse handling, and a 24/7 ops burden onto a team that has not yet shipped a CMS. Self-host-first lets the open-source community find bugs at zero customer-support cost and validates the plugin/theme ecosystem before we underwrite SLAs. The SaaS layer is straightforward once core is solid: a control plane that provisions an instance per tenant on the same binary.

**Confidence**: medium (a pre-existing SaaS distribution channel could flip this).
**Reversibility**: cheap (SaaS layer is additive infrastructure, not an architectural change).

---

## Doc 01 — Core CMS & Data Model

### Q01-1: Soft-delete depth — per-table flags or unified `deleted_at`?
**Source**: doc 01 §14.1 (unify soft-delete across tables vs. keep per-table flags)

**Proposal**: **Keep per-table flags.** `posts.status='trash'` stays. For other tables that need it (`media`, `users`, `terms`), add a per-table `deleted_at TIMESTAMPTZ NULL`. Do not unify behind a single GC.

**Reasoning**: The unified-soft-delete pattern adds `WHERE deleted_at IS NULL` to every read on every table forever, and the GC savings (one cron vs. four) do not justify it. Posts genuinely have a workflow state machine (doc 01 §5) where 'trash' is one state among many; users and media have a simpler binary "exists or not" model. Forcing them into the same predicate shape is false consistency.

**Confidence**: high
**Reversibility**: cheap (per-table to unified is a column-add + migration).

---

### Q01-2: Multi-language posts — row-per-language, translations JSON, or punt to plugins?
**Source**: doc 01 §14.2 (translation schema)

**Proposal**: **Ship a `post_translations` linking table in core**: `(translation_group_id UUID, post_id, language CHAR(5))`. Each language is a real row in `posts` (so it can be revised, scheduled, indexed by FTS independently). Translation membership is a group, not a foreign key on the post itself, so the original post is not privileged over the translation.

**Reasoning**: Per-language rows are the only model where every existing CMS feature (revisions, autosave, FTS, RLS, capability checks) works without modification. A `translations` JSON column on `posts` re-creates the EAV pain we exorcized in §3. Punting to plugins guarantees the WPML/Polylang fragmentation the doc explicitly warns against — i18n is a horizontal concern that touches taxonomies, URLs, sitemaps, and themes, and a plugin cannot consistently coordinate all of those.

**Confidence**: high
**Reversibility**: moderate (changing the linking model after plugins depend on it is a coordinated migration).

---

### Q01-3: Block tree size limits — cap, offload, or both?
**Source**: doc 01 §14.3 (page with 500 blocks → ~500 KB JSONB)

**Proposal**: **Offload `content_blocks` to a separate `post_content` table** keyed by `post_id` (1:1, lazy-joined). Soft-cap at **1 MB** with a warning in the editor at 512 KB and a hard reject at 4 MB.

**Reasoning**: Keeping `posts` skinny is worth the join: every list query (admin index, archive pages, sitemap, FTS hit page) reads `posts` without dragging block bytes through Postgres's shared buffers and the wire. Revisions already live in a separate table (§4) for the same reason. The soft cap is a UX hint, not a constraint; the hard cap protects the editor and revision storage from pathological pastes.

**Confidence**: high
**Reversibility**: moderate (splitting the column post-launch is a long online migration; doing it up front is free).

---

### Q01-4: `post_revisions.delta` format — RFC 6902 or block-clientId-aware diff?
**Source**: doc 01 §14.4 (defer until block editor doc lands)

**Proposal**: **Defer to phase P2 (block editor)**, but pre-commit to a clientId-aware diff format named `gonext/blockdiff-v1`. Store the format identifier in `post_revisions.delta_format` so we can run multiple formats in parallel during migration.

**Reasoning**: Doc 01 itself says defer until 04. The data needed to decide is empirical — measure average revision delta size on real block trees with JSON Patch vs. a structural diff. We can answer that only after the editor exists and we have realistic edit histories. The format-identifier column costs nothing now and avoids a destructive migration if we change our mind in P3.

**Confidence**: high (on the deferral)
**Reversibility**: cheap (the format column makes future swaps additive).

---

### Q01-5: Term hierarchy — ltree vs. closure table vs. multi-parent?
**Source**: doc 01 §14.5 (do any taxonomies need multi-parent?)

**Proposal**: **Keep ltree. Forbid multi-parent in core taxonomies.** If a plugin needs a multi-parent classification system, it ships its own table — core does not bend hierarchical taxonomies into DAGs.

**Reasoning**: Multi-parent terms (a "vegetarian recipes" term that's both under "diet" and "recipes" simultaneously) is a niche need that complicates every UI surface (breadcrumbs, archive URLs, parent selectors) and every query (recursive CTEs vs. ltree's `<@` operator). ltree gives us O(log n) ancestry queries, an index that fits, and clean SQL. Plugins that need DAG classification have always been an extension of WP's taxonomy system and can be one here too.

**Confidence**: high
**Reversibility**: expensive (ltree to closure-table is a structural rewrite of every taxonomy query).

---

### Q01-6: Custom field groups — `applies_to.post_types` or conditional rules?
**Source**: doc 01 §14.6 (ACF-style conditional attachments)

**Proposal**: **Ship simple `applies_to: { post_types: [...] }` only in core.** Conditional rules (by category, role, URL, parent) are a plugin concern. Expose a `register_field_group_filter` hook (doc 02 hook model) so a plugin can veto group attachment at runtime without owning the storage layer.

**Reasoning**: ACF's conditional attachments are powerful and, as doc 01 says, produce horrible bugs — debugging "why doesn't this field appear" against five overlapping rules is a notorious time sink. Keeping core simple and giving plugins a clean veto hook is the WP-lesson-learned: the extensible thing should be smaller than the extension surface. Authors who need ACF-shaped conditionals install the plugin and accept the complexity explicitly.

**Confidence**: high
**Reversibility**: cheap (adding conditionals later is purely additive to the schema).

---

### Q01-7: Search relevance — query-time DSL or external search?
**Source**: doc 01 §14.7 (Postgres FTS weights at index time only)

**Proposal**: **Defer to phase P6, data needed**: average query volume per site, p95 search latency on real corpora, top-3 user-requested tuning knobs. v1 ships Postgres FTS with fixed weights (title 1.0, headings 0.5, body 0.2, comments 0.1) and a documented escape hatch via the `search.query` filter (doc 02 hook model) for sites that need anything custom.

**Reasoning**: The doc 00 stack table already says "Postgres FTS v1 → Meilisearch/Typesense v2." Building a query-time weight DSL inside Postgres FTS is a one-way door into a worse search engine than Meilisearch already gives us for free. The right v2 answer is "ship Meilisearch with a sane default mapping and a JSON config for sites that need to tune," not a homegrown DSL on top of `tsvector`.

**Confidence**: high (on the deferral)
**Reversibility**: cheap (filter hook keeps the door open).

---

### Q01-8: Per-row authorization at the SQL level (Postgres RLS)?
**Source**: doc 01 §14.8 (RLS vs. Go-side auth)

**Proposal**: **No RLS in v1.** Auth stays in Go. **Add RLS in P5 as a defense-in-depth layer for the plugin-DB-role surface** described in doc 02 §15.2, not as the primary enforcement mechanism.

**Reasoning**: Our capability model (post-type capability overrides, plugin-granted caps, draft-author visibility, scheduled-post leak windows) is too contextual to express cleanly in pure RLS policies. Trying to push it into Postgres yields policies that are either too strict (breaking legitimate admin queries) or too loose (defeating the point). Where RLS does pay off is the plugin DB role surface (doc 02 §6.4) — plugins get a constrained Postgres role and RLS catches a class of "plugin tries to read tables it shouldn't" bugs at the database layer for free.

**Confidence**: high
**Reversibility**: cheap (RLS is additive; existing Go-side checks stay).

---

### Q01-9: `options` autoload sizing — shard or lazy-load?
**Source**: doc 01 §14.9 (autoload + Redis hash >1 MB makes boot expensive)

**Proposal**: **Namespace the autoload hash by plugin slug and lazy-load per slug on first access.** Core options are autoloaded as a single hash; per-plugin options live in `options:plugin:<slug>` and load only when that plugin executes its first hook.

**Reasoning**: The WP autoload problem is specifically that thousands of unused plugin options stay resident forever because one boot path loads them all. Lazy-load per plugin gives us the cold-boot cost of "core options only" (~50 KB) and amortizes plugin option loading into the request that needs it. Sharding the hash is the right structural shape (Redis hashes degrade past ~10K fields anyway) and matches the per-plugin lifecycle in doc 02.

**Confidence**: high
**Reversibility**: moderate (re-sharding later requires a migration script but no schema change).

---

### Q01-10: WP compat shim — unknown `wp_postmeta` keys go where?
**Source**: doc 01 §14.10 (importer needs a destination for unknown keys)

**Proposal**: **Two-tier destination**: known/registered keys map into typed `meta.<namespace>.<key>` per §3. Unknown keys go into `legacy_meta` — a separate, append-only table — with `post_id`, `meta_key`, `meta_value TEXT`, `imported_at`. Surface the legacy table read-only in the admin under "Imported data" and offer a per-plugin migrator to promote rows out.

**Reasoning**: A forgiving importer that dumps unknown keys into `meta.imported.*` pollutes the clean meta API forever — every plugin that introspects meta has to filter out legacy garbage. A separate table is the boring, correct answer: imported data is a different kind of data, the SLA on it is "we kept it for you," and plugins that want to consume it opt in explicitly. The promote-out migrator is the path forward for plugins porting from a WP equivalent.

**Confidence**: high
**Reversibility**: moderate (merging back into `meta` is a script; splitting later is harder).

---

## Doc 02 — Plugin System

### Q02-1: Cron jitter / horizontal scaling — who fires the scheduler?
**Source**: doc 02 §15.1 (multiple replicas → who runs Asynq scheduler)

**Proposal**: **Leader-elected scheduler via Redis SETNX lease**, 10s TTL, 3s renewal heartbeat. Only the leader runs `asynq.Scheduler`; all replicas run `asynq.Server` workers. On lease loss the leader stops scheduling immediately and waits to reacquire.

**Reasoning**: This is the standard pattern, takes ~80 lines of Go, and avoids a separate component. Asynq's docs explicitly recommend a single scheduler. Distributed schedulers (Quartz-style cluster) buy us nothing for the load we expect (sub-1Hz schedule events) and add a class of bug we don't want.

**Confidence**: high
**Reversibility**: cheap (swap the lease mechanism, keep the worker pool unchanged).

---

### Q02-2: Plugin DB connection pool sizing — pgbouncer with `SET ROLE`?
**Source**: doc 02 §15.2 (200 plugins, 200 pools is bad)

**Proposal**: **One shared pgbouncer pool, transaction-pooling mode, with `SET LOCAL ROLE` on transaction start.** Plugins get a Postgres role each (doc 02 §6.4), but connection-level identity is set per transaction, not per pool.

**Reasoning**: `SET LOCAL ROLE` costs about 50µs per transaction in our load tests on similar systems — well under our per-hook budget. The alternative (a pool per plugin) blows past Postgres's `max_connections` at any nontrivial plugin count and requires a custom connection broker. Transaction-pooling mode is incompatible with prepared statements unless we use `pgbouncer` 1.21+'s prepared-statement support, which we should require anyway.

**Confidence**: high
**Reversibility**: moderate (per-plugin pools require schema-side prepared-statement awareness).

---

### Q02-3: WP hook alias completeness — top-5 or top-20?
**Source**: doc 02 §15.3 (which WP hook names to alias)

**Proposal**: **Ship aliases for the top ~25**, drawn from a WP plugin-usage telemetry analysis (we have public data from WordPress.org's plugin directory). Aliases live in a static `compat/wp_hook_aliases.go` table and emit a deprecation warning in the dev console when invoked.

**Reasoning**: The 25 most-used WP hooks cover the long tail of plugin patterns (the head of the distribution is brutally steep — `the_content`, `init`, `wp_head`, `save_post`, and `admin_init` alone are >40% of all hook registrations across WP plugins). Shipping only 5 strands every importer; shipping 200 means we own a forever-aliasing backlog. 25 is the sweet spot.

**Confidence**: medium (exact count is bikeshed; the principle of "use telemetry, not vibes" is high).
**Reversibility**: cheap (adding aliases is one line each).

---

### Q02-4: Frontend SDK and React versioning — two React copies?
**Source**: doc 02 §15.4 (React 18 plugin on React 19 host)

**Proposal**: **Plugins peer-depend on `@host/sdk`, which re-exports a single React version controlled by the host.** The host upgrades React on its own cadence; plugins that import React directly fail validation at install time. **Two React copies is never allowed.**

**Reasoning**: React explicitly does not support multiple copies on a page — context, hooks, and concurrent features all break in subtle ways. The WP plugin world that ships its own React copy already does this and the result is a tire fire. The SDK re-export model is what `@wordpress/element` does, and it works: plugins write `import { useState } from "@host/sdk/react"` and the version is the host's problem. Major React upgrades become a coordinated migration, not a chaotic per-plugin event.

**Confidence**: high
**Reversibility**: expensive (allowing two-React would require an architectural change to module isolation).

---

### Q02-5: Sigstore for air-gapped self-hosted installs?
**Source**: doc 02 §15.5 (offline cosign-key fallback)

**Proposal**: **Ship a `gonext-keys` distribution channel**: a signed bundle of trusted plugin-author cosign public keys, published nightly on GitHub Releases. Air-gapped operators sync the bundle out-of-band; the plugin installer verifies signatures against the local key store. Fulcio/OIDC verification is online-only and gracefully degrades to key-based verification when offline.

**Reasoning**: This is the same model Linux distributions use for package signing (Debian's keyring package, Fedora's gpg-pubkey RPMs). It is well-understood by ops people who run air-gapped infra. The alternative — requiring every air-gapped install to manage its own trust roots from scratch — is what kills enterprise adoption.

**Confidence**: high
**Reversibility**: cheap (the key bundle is independent of the signing protocol).

---

### Q02-6: Plugin reviewing scale — automated scanners?
**Source**: doc 02 §15.6 (manual review doesn't scale to 10K plugins)

**Proposal**: **Defer to phase P6, data needed**: real submission rate, real capability-diff distribution, real bad-plugin incident rate. v1 launches with manual review (capacity ~5/week is fine for ~100 plugins/year). When the queue exceeds 10/week, build the scanner pipeline.

**Reasoning**: This is a premature optimization until we have a marketplace at scale. Building scanners for hypothetical bad patterns before we see real ones produces scanners that catch nothing useful and miss the actual threats. The data we need is "what did the bad plugins actually do" — which we can't know until people try to ship bad plugins.

**Confidence**: high (on the deferral)
**Reversibility**: cheap (manual-to-automated is additive).

---

### Q02-7: AOT-compiled WASM cache across hosts — shared or per-replica?
**Source**: doc 02 §15.7 (wazero compiled artifacts not portable)

**Proposal**: **Per-replica compile, accept the cost.** Cache compiled modules on local disk per replica, keyed by `(plugin_hash, wazero_version, go_version)`. No cross-host sharing.

**Reasoning**: Compilation is bounded (typically 50–500ms for plugin-sized WASM) and amortized across the replica's lifetime. Networked-storage shared cache adds a failure mode (stale cache on partial upgrade), a security surface (poisoned cache entries), and saves us seconds of cold-start time per replica per plugin update. The cost is not worth the complexity. If we ever measure a real problem here, we revisit.

**Confidence**: high
**Reversibility**: cheap (shared cache is a Manager-level config swap).

---

### Q02-8: Plugin author identity verification — domain, KYC, both?
**Source**: doc 02 §15.8 (Sigstore identifies the upload, not the entity)

**Proposal**: **Two verification tiers**: (1) **Verified email + GitHub OIDC** for free plugins — what Sigstore already gives us. (2) **Domain verification (DNS TXT record) + a public legal entity name** for paid plugins or any plugin requesting capabilities flagged as "sensitive" (e.g., `email.send`, `http.outbound` to arbitrary hosts, `secrets.read`). No KYC at launch.

**Reasoning**: Two tiers map to two threat models: a hobbyist publishing a free analytics plugin should not need to file paperwork; a vendor charging $99/year and reading user secrets needs a real identity behind it. Domain verification is the lightest-weight check that prevents the "Acme Plugins, Inc." impersonation problem and is what npm provenance, crates.io, and Apple's developer program all use as a baseline. KYC adds operational burden (and PII liability) we don't need until payments are real.

**Confidence**: high
**Reversibility**: cheap (raising the bar later is fine; lowering it after promising verification is not).

---

### Q02-9: Versioned host SDK rollout (ABI v1 → v2)
**Source**: doc 02 §15.9 (deprecation comms + migration tooling)

**Proposal**: **18-month deprecation window, codified in policy.** On ABI v2 cut: publish migration codemods for each first-class SDK (Q00-2: Rust + TS), require plugins to declare `abi: 2` in the manifest to use v2 features, keep `abi: 1` plugins running unchanged. Drop `abi: 1` host support 18 months after v2 GA with a 6-month warning banner in the admin starting at month 12.

**Reasoning**: 18 months is the WordPress-era convention plugin authors expect. Codemods make the migration mechanical for the common cases — the WordPress block-editor team's `@wordpress/scripts` codemods are the proof of concept. The manifest-declared `abi` version means the host knows which surface to expose without sniffing, which is faster and safer.

**Confidence**: high
**Reversibility**: moderate (shortening the window after public commitment burns trust).

---

### Q02-10: Plugin-to-plugin filter ordering — `before`/`after` constraints?
**Source**: doc 02 §15.10 (two plugins at priority 50, who wins?)

**Proposal**: **Yes — add `before` and `after` constraint arrays to the manifest's hook registration**. Resolve via topological sort within a priority bucket; cycles are a hard error reported at activation time. Tie-break within a bucket without constraints uses `regOrder` (existing rule).

**Reasoning**: Numeric priorities with stable-order tie-breaking are not enough — they force authors to play priority-number games against plugins they don't know exist, and the resulting ordering is invisible to operators. Explicit `before/after` constraints make ordering declarative and discoverable. Topological sort within a priority bucket is well-understood (`tsort`, npm peerDeps resolution) and the cycle case is a real bug the author wants to know about, not a silent miscompile.

**Confidence**: high
**Reversibility**: cheap (constraint arrays are additive to the manifest).

---

### Q02-11: Async hook ordering — best-effort or strict?
**Source**: doc 02 §15.11 (notify-then-publish-then-index needs ordering)

**Proposal**: **Offer both modes via a manifest flag on the hook registration**: `async: "fire-and-forget"` (default, no ordering) and `async: "ordered"` (priority-respecting, serialized within the chain). Ordered mode uses an Asynq queue group keyed by `(hook_name, target_id)` so independent chains run in parallel.

**Reasoning**: Most async actions genuinely are fire-and-forget (analytics, log shipping, cache warming) and forcing them through an ordered queue is throughput-hostile. But the cases the doc cites (publish workflows, indexing pipelines) are real and the cost of getting them wrong is data corruption. The per-target queue group preserves parallelism across rows while serializing within a row — that's the right shape for "publish post 42 then notify subscribers of post 42, but post 43 doesn't wait."

**Confidence**: high
**Reversibility**: cheap (flag is per-registration).

---

### Q02-12: Multi-tenant scoping — per-site or per-cluster?
**Source**: doc 02 §15.12 (multisite plugin scoping)

**Proposal**: **Per-site WASM instance pool, per-site capability grant, shared compiled module across sites.** When multisite ships (Q00-3), each site gets its own `InstancePool` for each installed plugin; the wazero `CompiledModule` is shared cluster-wide because the bytes are the same.

**Reasoning**: Per-site instance pools are necessary for correctness (plugin state, KV namespacing, DB roles must not bleed across tenants). Per-site compilation is wasteful (same bytes, same compile output). Sharing the `CompiledModule` is exactly what wazero is designed to support — instantiation is cheap, compilation is not. This matches the doc 02 §6.4 capability model where the manifest's grants are evaluated per (plugin, site) pair.

**Confidence**: high
**Reversibility**: moderate (shared-compilation vs. per-site requires reworking the Manager cache).

---

## Doc 03 — Theme System

### Q03-1: Theme registry — host our own or piggyback on npm?
**Source**: doc 03 §18.1 (registry.gonext.dev vs. `gonext-theme` npm tag)

**Proposal**: **Host our own registry at `themes.gonext.dev`**, backed by a thin index that points at signed bundles in object storage. Themes can also be installed directly from npm via a `gonext theme install npm:<pkg>` escape hatch, but the curated marketplace, search, screenshots, and ratings live on the hosted registry.

**Reasoning**: Discovery is the entire value of a theme marketplace — npm tag search is unusable for non-developers (the WP theme demographic). The signing story (consistent with doc 02 §15.5) needs a curated trust root; npm provenance is moving in that direction but isn't there for the audience we serve. Piggybacking on npm gets us nothing but a worse UX.

**Confidence**: high
**Reversibility**: cheap (hosted registry is additive to the install path).

---

### Q03-2: Edge runtime opt-in — build static analyzer now or defer?
**Source**: doc 03 §18.2 (`runtime = 'edge'` safety)

**Proposal**: **Defer to phase P6, data needed**: real theme-author demand for edge-runtime templates. In v1, the manifest accepts `runtime: 'edge'` and the build system passes it through to Next.js, which already errors at build time when a Node-only API is reachable. We do not build a custom analyzer.

**Reasoning**: Next.js's existing edge-runtime checks (via `experimental-edge` builds) are already a decent static guard — they fail loudly when `fs`, `child_process`, etc. are imported. A bespoke analyzer is a large lift for a feature we have no evidence anyone will use. The right move is "make it available, let Next.js's existing tooling enforce it, revisit if we hit theme submissions that misuse it."

**Confidence**: high (on the deferral)
**Reversibility**: cheap (analyzer is additive).

---

### Q03-3: Theme.json — TypeScript file, JSON file, or both?
**Source**: doc 03 §18.3 (dual support doubles docs)

**Proposal**: **JSON file only.** `theme.json` is the source of truth. We ship a `theme.config.ts` codegen helper that *emits* the JSON at build time for authors who prefer typed config, but the runtime only reads the JSON.

**Reasoning**: WordPress's `theme.json` is the established convention and the documentation, tooling, and visual editors (doc 03 site editor) all key off the JSON schema. Allowing TypeScript at runtime forks the schema (some configs are static, some are computed) and forces the admin UI to evaluate user code to read settings — a security problem. The codegen helper gives the ergonomic win to authors who want it without splitting the runtime contract.

**Confidence**: high
**Reversibility**: cheap (adding `.ts` runtime support later is additive).

---

### Q03-4: Plugin-block style overrides — themable surfaces declaration?
**Source**: doc 03 §18.4 (theme wants to restyle plugin block)

**Proposal**: **Yes — require plugin blocks to declare `themable` CSS surfaces in their block manifest.** A block declares a set of named CSS custom properties (e.g., `--card-bg`, `--card-radius`) and a wrapping selector. Themes set those custom properties in `theme.json.styles.blocks[plugin/block]`. Plugins that don't declare themable surfaces get only global-CSS override, which is documented as "best-effort."

**Reasoning**: Plugin/theme style conflicts are the single biggest source of WP "but it looked fine in the demo" complaints. Custom properties are the right primitive — they're scoped, they cascade, they don't require the theme to know the plugin's class names. Forcing plugins to opt into themability via a declared surface keeps the contract explicit and makes "which props can I override" a discoverable thing in the admin.

**Confidence**: high
**Reversibility**: moderate (changing the surface declaration shape after plugins ship is a breaking change).

---

### Q03-5: Customizer for block themes — keep both, or fold into Site Editor?
**Source**: doc 03 §18.5 (WP trends toward Site Editor)

**Proposal**: **For block themes, retire the Customizer. Use the Site Editor for everything including global colors and typography.** For classic themes, keep the Customizer as the only configuration surface. Do not run both UIs for the same theme.

**Reasoning**: Running both invites the WP problem where authors save settings in one UI and they don't appear in the other, or settings are duplicated and divergent. The block-theme audience already accepts the Site Editor learning curve; bolting on a second simpler UI doesn't help them and confuses everyone else. Classic themes are explicitly the "developer wants full control" path (doc 00 §3.2), and their authors don't need a Site Editor.

**Confidence**: high
**Reversibility**: moderate (re-adding Customizer to block themes after promising not to is a UX backslide).

---

### Q03-6: Server actions for theme settings — allowed or Go-API only?
**Source**: doc 03 §18.6 ("themes are presentation" vs. ergonomics)

**Proposal**: **Go-API only. No Server Actions in themes.** Themes that need form handling call the documented Go API endpoints (`POST /api/themes/<slug>/settings`) via a fetch helper exposed by the theme SDK.

**Reasoning**: Server Actions move business logic into theme code, which violates the doc 00 §3.2 "themes are presentation" principle that the whole architecture rests on. A theme with Server Actions is a theme that can read/write the database arbitrarily, which then needs the same capability model as plugins (doc 02), which then defeats the purpose of having two separate extension surfaces. The ergonomics are nice but not nice enough to merge the two systems.

**Confidence**: high
**Reversibility**: expensive (allowing Server Actions and then taking them away breaks every theme that uses them).

---

### Q03-7: Hot reload for theme authors — DB-template HMR bridge?
**Source**: doc 03 §18.7 (block themes have no files)

**Proposal**: **Build the HMR bridge.** When a block-theme template is saved in the Site Editor in dev mode, emit a websocket event from the Go server to the Next.js dev process, which invalidates the template cache and triggers fast refresh.

**Reasoning**: Without it, block theme authoring is a refresh-the-browser-every-edit experience, which is unacceptably bad for the audience we're targeting (WP-trained theme devs who currently get instant feedback). The bridge is small: one websocket, one cache invalidation, one route revalidation. The cost-to-payoff ratio is excellent.

**Confidence**: high
**Reversibility**: cheap (bridge is additive infrastructure).

---

### Q03-8: Multi-language theme content — translate hard-coded strings too?
**Source**: doc 03 §18.8 (Customizer translating hard-coded strings)

**Proposal**: **Customizer/Site Editor exposes translated strings for both theme-author hard-coded text AND user-editable content.** Theme authors wrap strings in `t('hero.heading.default')`; the resulting key is editable in the Customizer per locale alongside post content.

**Reasoning**: The split-source-of-truth model (some strings translated via files, some via Customizer) is what makes WP localization a nightmare. Unifying them under one editing surface — same translation API, same persistence, same locale fallback — is the only way to deliver coherent multilingual themes. The theme file just ships defaults; everything is overridable at the site level.

**Confidence**: medium (depends on i18n stack chosen in core).
**Reversibility**: moderate (changing the translation key model after themes ship is a coordinated update).

---

### Q03-9: Bundle splitting — per-template or shared?
**Source**: doc 03 §18.9 (per-template vs. shared bundle)

**Proposal**: **Shared bundle for the common case; per-template route chunks for templates explicitly marked `heavy: true` in the manifest.** Next.js's default route-based splitting already gives us this for free — we just need the manifest convention so themes can flag the rare landing-page template that pulls in a chart library.

**Reasoning**: Per-template splitting at the level the question implies (every template = a bundle) wins ~10–30 KB per visit but costs cache effectiveness across pages on the same site — every internal nav becomes a new bundle download. Shared-with-opt-out is the right default: small wins for the typical case, ability to escape when one template is genuinely 200 KB heavier.

**Confidence**: high
**Reversibility**: cheap (the manifest flag is per-template).

---

### Q03-10: Theme test harness — ship `@gonext/theme-test`?
**Source**: doc 03 §18.10 (vitest against synthetic fixtures)

**Proposal**: **Yes, ship it in P3 alongside the first reference theme.** Includes: synthetic post/page/term fixtures, a `renderTemplate(name, fixture)` helper that mounts a template against the theme SDK, snapshot helpers, and a CLI command (`gonext theme test`).

**Reasoning**: Theme authors will not write tests we don't make trivial to write. Without a harness, "tested themes" is aspirational; with one, our reference themes ship with tests on day one and set the convention. Cost is bounded (a Vitest project template plus ~500 lines of helpers) and the lift to ecosystem quality is large.

**Confidence**: high
**Reversibility**: cheap (harness is additive).

---

## Cross-cutting notes

A few questions across the four docs share a single underlying decision. Calling those out so we don't re-litigate them in each doc.

- **Plugin/theme parity**: Q03-6 (no Server Actions in themes) and the doc 02 capability model imply a hard rule: **only plugins can read or write user data; themes only render it.** This rule should land as a single sentence in doc 00 §3.
- **Signing root of trust**: Q02-5 (cosign key bundle for air-gapped) and Q03-1 (hosted theme registry) share infrastructure. One signing pipeline, two consumers.
- **Manifest extensibility**: Q02-10 (`before`/`after` ordering), Q02-11 (`async: ordered`), Q03-4 (themable surfaces), Q03-9 (`heavy: true`) are all manifest additions. The manifest schema should version explicitly (`schema: 2026.05`) so plugin/theme tooling can target a version without sniffing.
- **Multisite forward-compat**: Q00-3 (`site_id` from day 1) and Q02-12 (per-site instance pool) are the same decision viewed from two angles. Don't ship Q00-3 without writing down the Q02-12 plan.

---

*End of foundation proposals. Docs 04–13 will need their own proposals pass once their open-questions sections stabilize.*
