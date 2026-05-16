# 14 — Proposals: Platform Open Questions (docs 08–10)

> Status: proposal. Owner: platform/architecture.
> Scope: recommended, opinionated answers to the "Open questions" sections of `08-migration-compat.md`, `09-deployment-ops.md`, and `10-observability.md`.
> Reader: an engineer or tech lead who has read the three source docs and wants a concrete default to argue with, not a menu of options.

The point of this doc is to **pick a side** on each open question. Every proposal is reversible at some cost; the goal is to remove the cognitive overhead of "still TBD" so implementation can move. If a question is genuinely premature, the proposal is an explicit defer with a named trigger that should re-open it.

A few guiding biases that show up repeatedly below:

1. **Self-host parity is a hard constraint, not a stretch goal.** Anything we propose has to work on a single VPS without a cloud account. When the SaaS shape and self-host shape conflict, self-host wins for v1 defaults and SaaS gets a premium toggle.
2. **WASM is the isolation boundary.** Doc 02 commits to WASM sandboxing for plugins. Several proposals here lean on that commitment rather than re-litigating it (notably Q09-2).
3. **Observability defaults must not surprise the operator.** A v1 self-hoster who deploys the Helm chart should not discover, in production, that we added an always-on continuous profiler or an eBPF DaemonSet they didn't ask for. New observability surface area is opt-in by default.
4. **Migration is irreversible from the customer's perspective.** Once they cut over from WP to us, the inverse migration tool is "good luck." Anything that risks data loss in §08 is biased toward "warn loudly and defer to v1.1" rather than "ship a half-working version."
5. **Don't pin to other people's roadmaps.** WP REST API, Pyroscope, KEDA, etc. all evolve on their own cadence. Where we depend on them, we pin a version explicitly and document the upgrade trigger.

---

## Summary table

| ID | Question (short) | Proposal (short) | Conf | Revers |
|---|---|---|---|---|
| Q08-1 | Snapshot storage cost | User-supplied S3 by default; managed-DB storage only for SaaS small tier | high | cheap |
| Q08-2 | WP plugin REST routes | 501 + structured replacement pointer (not 404) | high | cheap |
| Q08-3 | Stable `legacy_int_id` across re-imports | Yes, deterministic per `(source_host, source_id)`; allow `--rerun-stable=false` | high | moderate |
| Q08-4 | WXR-by-batch (directory vs single file) | Accept directory; merge with stable ordering | high | cheap |
| Q08-5 | Live preview during execute | Defer to P3; ship "head of run" sampling instead | medium | cheap |
| Q08-6 | Multilingual (Polylang/WPML) | Defer to v1.1; not a v1.0 blocker. Ship a documented two-pass workaround in v1.0 | medium | moderate |
| Q08-7 | WooCommerce path | Products + categories + images only (no orders/customers); separate adapter flag | medium | moderate |
| Q08-8 | 2FA migration | Do not migrate; force re-enroll on first login. Document the rationale | high | cheap |
| Q08-9 | Plugin replacement guide curation | In-tree YAML for top 100; community-PR-driven for the long tail | high | cheap |
| Q08-10 | Corpus licensing | Synthesize; do not license real sites | high | cheap |
| Q08-11 | Re-run guarantees | Re-run from scratch (new `run_id`) is the supported path; sync mode in §14 covers the rest | high | moderate |
| Q08-12 | Shim versioning | Pin to WP REST v2/v5.x semantics at fork; document; move slowly | high | moderate |
| Q09-1 | Service mesh as multi-region default | No mesh in v1; revisit at SaaS GA | high | moderate |
| Q09-2 | Per-plugin pod isolation | No isolated pods; rely on WASM sandbox + per-plugin worker pool tier (premium feature) | medium | moderate |
| Q09-3 | mTLS between cluster pods | Defer to doc 15; ship cert-manager bundle as opt-in | high | moderate |
| Q09-4 | Serverless cold-start with plugins | Two-tier boot: minimal core ready in <2s; lazy plugin discovery as a flag | medium | moderate |
| Q09-5 | Per-route readiness | Yes — `/readyz` differentiates by dependency; per-route weights table | high | cheap |
| Q09-6 | Plugin migration ordering | Add `requires:` field + topological sort; refuse cycles at register time | high | cheap |
| Q09-7 | Image base for `core-web` | `node:20-alpine` for v1; revisit distroless in v1.2 with measured CVE noise | medium | cheap |
| Q09-8 | Worker auto-tuning | KEDA-on-Redis as the documented default; HPA as fallback | high | moderate |
| Q10-1 | Long-term metric store | Mimir for self-host reference stack; Grafana Cloud free tier as the SaaS default | medium | moderate |
| Q10-2 | Continuous profiling default | Off by default in v1; ship Pyroscope wiring and one-flag enable | high | cheap |
| Q10-3 | eBPF host-level visibility | SaaS-only add-on; defer self-host until v2 | high | cheap |
| Q10-4 | OTel collector shape | DaemonSet per node; sidecar only for high-isolation tenants | high | moderate |
| Q10-5 | Audit log signed batches & key mgmt | Defer to doc 06; observability subscribes via read-only signed-batch S3 prefix | high | moderate |
| Q10-6 | RUM device classification | Add `headless` (cheap, useful for bot triage); skip `app-webview` | medium | cheap |
| Q10-7 | Plugin sourcemap upload | Add `sourcemap_url` to plugin manifest in P4; required for prod plugins | high | cheap |
| Q10-8 | Trace propagation through CDN | Mint a fresh root span at the edge; preserve `traceparent` as parent only when CDN config allows | medium | moderate |
| Q10-9 | Cost cap mode | Yes — `GONEXT_OBSERVABILITY_BUDGET=cheap` with documented sampling profile | high | cheap |
| Q10-10 | Per-tenant observability | Defer to v2; ship `tenant_id` label plumbing now, dashboards/budgets later | high | cheap |

---

# Doc 08 — Migration & WordPress Compatibility

### Q08-1: Snapshot storage cost
**Source**: doc 08 §19.1 — pg_dump of a 1M-post site is large; do snapshots live in our DB host storage, or push to user-supplied S3? Default vs configurable?

**Proposal**: User-supplied S3 (or S3-compatible) is the **default and required** in production deploys. The managed-DB local-disk path is only kept for the SaaS small/free tier (capped at 5 GB total, 7-day retention) and for dev/test. Self-hosters get a single env var (`GONEXT_MIGRATION_SNAPSHOT_BUCKET=s3://...`) and we refuse to start a migration if it's unset on production-mode boots.

**Reasoning**: Snapshots are the single largest non-content artifact migrations produce, and they grow with source-site size, not our user count. Coupling them to our DB host's storage volume turns every large migration into an unplanned capacity event for ops. S3 is cheap, durable, lifecycle-policy-able, and trivial to scope with per-tenant IAM. The "configurable" framing in the question is a trap — defaults shape the system, and the safer default is "the customer owns this storage." This also reinforces the doc 13 rollback story (§13 here references snapshot-based rollback): when the snapshot lives in the customer's S3, the rollback path is auditable from their side too.

**Operational notes**:
- Self-host with no S3 access: refuse to start; print a one-liner pointing at the docs.
- SaaS small tier: snapshots in our managed S3, customer-tagged, 7-day retention.
- Snapshot format: `pg_dump --format=custom --compress=9`, plus an inventory JSON describing the run.

**Confidence**: high
**Reversibility**: cheap

---

### Q08-2: WP plugin REST routes
**Source**: doc 08 §19.2 — sites depend on plugin-added `/wp-json/<namespace>/...` routes for headless. Stub server returning 501 with structured pointer, or 404 (current plan)?

**Proposal**: Reverse the current plan: return **HTTP 501 Not Implemented** with a structured JSON body that names (a) the originating plugin we detected during migration, (b) the recommended replacement plugin or core feature, and (c) a stable doc URL. Keep 404 reserved for genuinely unknown routes. Implement this behind a feature flag (`shim.unknown_plugin_routes=stub|404`) so the operator can opt into the stricter 404 mode after they've finished cutover.

**Reasoning**: A 404 silently breaks headless frontends with no signal of *why*; their request handler usually maps 404 to "post not found" and ships broken UI. A 501 with a structured payload is a self-explaining error: the frontend's catch-all error reporter will surface it, the user reads it, they know exactly which plugin to replace. The cost is one switch table mapping known plugin namespaces to replacement metadata, which we already maintain for §12. The downside (false positives for legitimately removed plugins) is bounded by the operator-controlled flag.

**Response shape**:
```json
{
  "code": "wpshim_plugin_route_not_implemented",
  "namespace": "yoast/v1",
  "detected_plugin": "Yoast SEO",
  "replacement": { "plugin": "gonext-seo", "since": "1.0.0", "docs_url": "https://docs.gonext.dev/migrate/yoast" },
  "message": "This route was provided by Yoast SEO in WordPress. Install gonext-seo or remove the calling code."
}
```

**Confidence**: high
**Reversibility**: cheap

---

### Q08-3: Stable `legacy_int_id` across re-imports
**Source**: doc 08 §19.3 — re-importing same source site: should `legacy_int_id` be stable across runs? Bias was yes, keyed by `(source_run, source_id)` but deterministic per `source_id`.

**Proposal**: Yes, stable by default. Allocate `legacy_int_id = stable_hash(source_host, source_table, source_id) mod 2^31` with collision resolution via a small `legacy_id_overflow` table. Add a `--rerun-stable=false` flag for the niche case where the operator wants a fresh mapping (e.g., testing two divergent forks of the same site). Document that "same source_id from a different source_host" gets a different legacy id — the host is part of the key.

**Reasoning**: The whole point of `legacy_int_id` is to let WP-era integrations keep working after re-runs (e.g., a CMS-of-CMSes scraping by id). If a re-import shuffles ids, every external system gets silently broken. Determinism is the correct default. The collision rate at 31 bits with a domain-namespaced hash is negligible at any realistic site size; the overflow table handles the rare collision without breaking the contract. The "different content under the same id" risk the original question raises is real but bounded — the shim already exposes `last_modified` and an `_etag`, so well-behaved clients revalidate.

**Confidence**: high
**Reversibility**: moderate (changing the hash later requires a one-shot remapping migration)

---

### Q08-4: WXR-by-batch
**Source**: doc 08 §19.4 — WP exports per-author or per-month. Accept a directory of WXRs and merge, or require a single file?

**Proposal**: Accept a directory. Merge in lexicographic-by-filename order, with explicit warnings logged when a later file overrides an earlier `<wp:post_id>` (which signals a corrupt export, not a normal one). Also accept a single file (no behavior change). CLI: `gonext import --from-wxr ./exports/` if directory, or `--from-wxr ./export.xml` if file.

**Reasoning**: WordPress's own export UI emits multi-file outputs for any site above ~50 MB. Forcing operators to `cat *.xml > all.xml` is a footgun (the resulting file is not valid XML — concatenating `<rss>...</rss>` documents doesn't merge them). Our streaming parser already iterates `<item>` tokens; doing it across a sorted file list adds maybe 30 lines and removes the most common support-ticket source.

**Confidence**: high
**Reversibility**: cheap

---

### Q08-5: Live preview during execute
**Source**: doc 08 §19.5 — wizard shows progress but not yet-imported content. Worth a "render as posts come in" view?

**Proposal**: Defer the full live-preview to P3. For v1, ship a "head of run" sampling view: every N posts (default N=100), the importer writes a thumbnail/snippet of the latest imported post to `migration_preview_samples`, and the wizard renders a slow-refreshing carousel of "most recent 12 imports." No sockets; no per-post push. The wizard polls `GET /api/v1/migrations/:id/recent-samples` every 2s.

**Reasoning**: The full vision (live socket-pushed render of each post as it lands) is a lot of plumbing for a feature whose actual value is "did my migration choose a sensible block conversion for this representative post." A sampled feed gives 90% of the value at ~5% of the cost. Real-time rendering also conflicts with the importer's batching — we'd be paying socket overhead during a phase that should be optimizing throughput.

**Confidence**: medium
**Reversibility**: cheap

---

### Q08-6: Multilingual (Polylang, WPML)
**Source**: doc 08 §19.6 — v1 warns and ignores. Blocker for v1.0 in EU multi-lang markets, or v2?

**Proposal**: Defer full multilingual to v1.1; do not block v1.0. For v1.0, ship a documented two-pass workaround: (1) run the importer once with `--lang=primary` to migrate the default-language content; (2) run it again per additional language with `--lang=fr --post-translation-link=meta:_polylang_translations`. Posts in subsequent passes are imported as separate posts linked via a `translations` join table. This is ugly but unblocks the migration; first-class multilingual gets a dedicated design doc in P5.

**Reasoning**: WPML and Polylang model multilingual content fundamentally differently, and both differ from anything we'd build natively. Doing this *correctly* requires a content-model decision (one row with translations vs. N linked rows) that touches doc 01. That's a design conversation, not a v1.0 patch. The two-pass workaround is honest about the limitation and gives EU customers a migration path while we design the real thing. The signal to re-open: third multi-lang customer asks in pre-sales, OR a v1.1 customer hits the workaround's edge cases (term translation linkage, menu translation).

**Confidence**: medium
**Reversibility**: moderate (the v1.1 work is a content-model migration on already-imported sites)

---

### Q08-7: WooCommerce path
**Source**: doc 08 §19.7 — strict no-migrate vs. "products only, no orders/customers"?

**Proposal**: Build a **products-only** WooCommerce adapter, off by default, gated behind `--with-woocommerce-catalog`. Migrate: product CPT, product variations, categories, tags, images, prices, SKU, simple inventory levels. Explicitly *do not* migrate: orders, customers, coupons, shipping zones, tax rules, payment gateway config. The CLI prints a loud banner about what's not coming over.

**Reasoning**: The "small product catalog" use case is real — agency sites with 20–200 SKUs where WC is essentially a product directory with a pretty admin. Those migrations get blocked today and the alternatives (CSV export + manual re-entry) are painful. The risk is scope creep into orders/customers/payments, which is genuinely out of scope (regulatory, payment-gateway-specific, no upside for us). A hard line at "catalog only, opt-in, loud about exclusions" captures the easy win without inviting the hard one.

**Confidence**: medium
**Reversibility**: moderate (once shipped, customers will ask for orders; we have to be willing to say no)

---

### Q08-8: 2FA migration
**Source**: doc 08 §19.8 — users will demand 2FA state migration; doing it safely needs per-plugin secret formats. Worth it or punt?

**Proposal**: Do not migrate 2FA state. Force re-enrollment on first login post-migration. Send a templated email at cutover ("your account is preserved; you'll re-enroll 2FA on next sign-in") and document the rationale in the runbook. Reject this question being re-opened until we ship our own first-party 2FA in core (doc 06).

**Reasoning**: TOTP secrets cross a security boundary — copying them from a foreign plugin's storage format means trusting their key-derivation, their encryption-at-rest decisions, and their migration semantics for backup codes. Each WP 2FA plugin (Wordfence, Two Factor, miniOrange, Duo) has a different format; building five adapters multiplies the surface area where we can leak secrets. Re-enrollment is a 30-second user experience that adds zero security debt to us. The bar to revisit is "we've built core 2FA and want to migrate it from a previous GoNext deploy" — not "a WP user complained."

**Confidence**: high
**Reversibility**: cheap

---

### Q08-9: Plugin replacement guide curation
**Source**: doc 08 §19.9 — who maintains the curated table? In-tree or CMS-managed?

**Proposal**: In-tree YAML in `internal/migrations/plugin_map/` for the top 100 plugins by usage (yoast, wpforms, contact-form-7, woocommerce, elementor, etc.). Long tail is community-PR-driven: a `CONTRIBUTING-plugin-map.md` and a CI check that validates schema. The CMS-managed option is rejected — it introduces a runtime dependency on infrastructure we don't otherwise need and creates a deployment-skew problem (a self-hoster's binary might point at a CMS we deprecated).

**Reasoning**: The top 100 plugins cover ~85% of WP installs by traffic. Those entries are stable, change slowly, and are the most valuable to get right — in-tree means they ship versioned with the binary and reviewed in code review. The long tail (10,000+ plugins) is where a CMS would help, but it would also be where most entries are stale, low-quality, or contentious — handling that in a PR queue with a CODEOWNERS for `plugin_map/` is the same governance burden without operating a service.

**Confidence**: high
**Reversibility**: cheap

---

### Q08-10: Corpus licensing
**Source**: doc 08 §19.10 — real test sites have un-redistributable content. Synthesize or license real sites?

**Proposal**: Synthesize. Build a generator (`internal/testdata/wpcorpus/gen.go`) that emits WP-dump shapes with realistic distributions: post-length tails, postmeta cardinality, shortcode density, attachment counts, multilingual sprinkles, plugin-emitted markup variants. Seed corpus with manually-crafted "pathological" cases (broken HTML, mojibake, emoji-in-slugs, 50MB single post). Real-site licensing is rejected on legal/redistribution grounds and because we'd be testing against a frozen historical artifact instead of an adversarial generator.

**Reasoning**: A generator gives reproducible, redistributable, versioned corpora that we can grow as we find new edge cases. Real sites give a snapshot that we can't extend or share. The cost difference is one engineer-month upfront vs. ongoing legal review of every donated site, and the generator is strictly better for fuzzing / regression isolation.

**Confidence**: high
**Reversibility**: cheap

---

### Q08-11: Re-run guarantees
**Source**: doc 08 §19.11 — user upgrades source WP and re-runs. From-scratch with new `run_id` + rollback old, or "diff & apply"?

**Proposal**: Re-run from scratch with a new `run_id` is the supported path. The §14 incremental-sync mode (transition mode) covers the "diff & apply" use case where it's actually safe. There is no separate one-off "diff & apply" mode in v1. If the user wants the result of "diff and apply" without sync mode, the documented path is: take a new run, rollback the old, verify, swap.

**Reasoning**: Diff-and-apply on a CMS migration has terrible edge cases — what's a "change" when block markup got re-serialized? When a slug changed because of a redirect rule? When the user manually edited a post in the new system between runs? Sync mode in §14 avoids those by being explicitly the active-replication path with conflict rules. The one-off diff-apply has all the complexity with none of the structure. Funneling users into "re-run + rollback" keeps the surface area tractable.

**Runbook (documented for support)**:
1. Run new import with `--run-id=auto`.
2. Verify the new run on a staging origin (DNS still points at old origin or old run).
3. If accepted: `gonext migration promote <new_run_id>` — this flips redirects, switches the canonical run pointer, and rolls back the previous run.
4. If rejected: `gonext migration discard <new_run_id>` — drops the new run, leaves the old one canonical.

**Confidence**: high
**Reversibility**: moderate (adding a true diff-apply later means new tooling, not new schema)

---

### Q08-12: Shim versioning
**Source**: doc 08 §19.12 — pin to WP REST v5.x semantics? When WP ships breaking changes in v6.x, follow or pin?

**Proposal**: Pin to the WP REST API contract as of the WP version current at our v1.0 ship date (likely WP 6.7 era — exact version is recorded in `internal/wpshim/SUPPORTED_WP_VERSION`). Follow WP REST changes only when (a) they're additive or (b) >25% of our active customer base reports needing them. Maintain a single "WP shim version" string in the OpenAPI doc; clients that need newer semantics route through real WP via reverse-proxy in transition mode (§14).

**Reasoning**: WordPress REST API is the lingua franca of headless WP frontends; chasing every minor change destabilizes our contract for the 95% of customers whose code doesn't care. Pinning gives us a stable target. The risk — that real WordPress evolves out from under us — is bounded by transition mode, which lets the user keep their WP install hot during cutover and read whatever's new from there. The bar to bump the shim version is explicit and customer-driven, which keeps it from becoming a feature-request black hole.

**Confidence**: high
**Reversibility**: moderate (bumping the pinned version requires a contract review of every shim endpoint)

---

# Doc 09 — Deployment & Operations

### Q09-1: Service mesh (Istio/Linkerd) as multi-region default
**Source**: doc 09 §22.3 — "probably not v1; revisit at SaaS time."

**Proposal**: No service mesh in v1 or v1.x. Revisit at SaaS GA, and only if (a) we have multi-region traffic split or (b) we need per-pod identity for compliance attestation. Document this as the path forward: cert-manager + cluster-local mTLS is the v1.x answer; a mesh is the v2/SaaS answer. The trigger to re-open is "second customer demands FedRAMP/SOC-2 type attestation that requires per-pod mTLS identity."

**Reasoning**: Service meshes have a real operational tax (sidecar memory overhead, control-plane upgrade story, mesh-aware troubleshooting). Self-hosters on a single region get nothing from a mesh except a steeper learning curve. The benefits (mTLS, traffic shaping, observability) are partially available without a mesh — cert-manager handles TLS, in-cluster HPA handles traffic shape, the observability stack in doc 10 doesn't depend on mesh telemetry. Adopting a mesh before we need the multi-region or zero-trust features it actually unlocks is premature complexity.

**Confidence**: high
**Reversibility**: moderate (adopting a mesh later is a multi-week project but doesn't break app code)

---

### Q09-2: Per-plugin pod isolation
**Source**: doc 09 §22.3 — dedicated `core-worker` pool per plugin would shrink blast radius at the cost of one process boundary per plugin.

**Proposal**: No per-plugin pods in v1. Rely on the WASM sandbox (doc 02) as the primary isolation boundary; that's what it's *for*. Offer a **per-tenant worker tier** in SaaS-premium where the customer's plugins run in dedicated worker pods — this is what enterprises actually ask for ("my plugin must not be co-tenanted with another customer's"), and it's strictly better than per-plugin isolation at the same operational cost.

**Reasoning**: Per-plugin pods would multiply our pod count by the average plugin install size (10–30 plugins), which destroys the resource efficiency of WASM (the whole point of WASM-as-isolation is "many tenants, one process"). The blast-radius argument assumes a WASM sandbox escape — if that happens, we have a critical security incident regardless of pod boundaries (one tenant attacking another via shared kernel still works). Per-tenant isolation, by contrast, is a clean product feature with predictable resource math: one pod pool per tenant, sized to their traffic, billed at a per-pool rate.

**Cross-doc check**: doc 02 §plugin-host commits to WASM as the isolation primitive. This proposal does not require revisiting that commitment — it depends on it. If doc 13's threat model later concludes WASM sandbox escapes are likely enough to plan around (which would be a major finding), this proposal needs to re-open alongside doc 02.

**Confidence**: medium
**Reversibility**: moderate (per-tenant worker pools require config + scheduler work)

---

### Q09-3: mTLS between cluster pods
**Source**: doc 09 §22.3 — off by default; rotation surface unspecified; defer to doc 15.

**Proposal**: Defer the specifics to doc 15 as planned, but commit now to the operator-facing contract: ship a **cert-manager Helm sub-chart** as an opt-in (`gonext-mtls`) that issues short-lived (24h) certs to each pod from an in-cluster CA. Off by default. The chart handles rotation; the app reads `MTLS_CERT_PATH` / `MTLS_KEY_PATH` and does HTTPS internally if both are set. No SPIFFE, no SPIRE, no mesh — just cert-manager + an in-app HTTPS toggle.

**Reasoning**: mTLS-on-by-default is a self-host trap (operators don't know how to debug it when it breaks). Cert-manager is the lowest-friction path for the operators who actually want mTLS, and it's standard enough that we don't need to invent rotation tooling. Specifics like cipher allowlist and audit logging belong in doc 15; the deployment-side contract (Helm chart, env vars, toggle) belongs here and should be committed.

**Confidence**: high
**Reversibility**: moderate

---

### Q09-4: Serverless cold-start with plugin-heavy installs
**Source**: doc 09 §22.3 — 5s boot is fine for Cloud Run, but plugin discovery breaks that for plugin-heavy installs.

**Proposal**: Implement a **two-tier boot**: (1) "minimal core" mode ready in <2s, serves requests that don't touch plugin hooks; (2) lazy plugin discovery happens in background, gated by a `plugins_ready` readiness gate. The readiness probe goes 200 OK once core is up; a separate `/readyz?include=plugins` probe gates the plugin-dependent routes. For routes that hit a not-yet-ready plugin, return HTTP 503 with `Retry-After`.

**Reasoning**: Eager plugin discovery on every cold start makes Cloud Run a poor fit for plugin-heavy installs, which is exactly the deployment shape we want to support for the long tail. Lazy discovery + a graded readiness model lets the typical request (a public-web page hit) succeed at second 2 while the editor admin (which actually needs every plugin) waits until second 8. This is how Lambda's "init phase" pattern works for warm-up. The cost is two readiness probes and a `plugin_state=loading` enum we have to thread through the hook dispatch — manageable.

**Confidence**: medium
**Reversibility**: moderate (the readiness model is invasive once shipped)

---

### Q09-5: Partial-outage readiness
**Source**: doc 09 §22.3 — `/readyz` returns 503 if Redis is down, even though `/healthz` and `/metrics` don't need Redis. Per-route readiness is open.

**Proposal**: Yes, ship per-route readiness in v1. Implement `/readyz` to return a structured JSON body listing each dependency (postgres, redis, s3, plugin_host) with status, and accept a `?need=` query param so load balancers can probe per-route requirements (e.g., the public-web LB probes `/readyz?need=postgres`; the worker queue probes `/readyz?need=postgres,redis,s3`). Define a per-route dependency table in `internal/health/route_deps.go` and surface it in the OpenAPI doc.

**Reasoning**: A monolithic 200/503 readiness signal pretends the app is one thing; it's not. With Redis down, `core-api` can still serve cached public pages — turning that off because of an unrelated dependency is a self-inflicted outage. Per-route readiness lets each LB make the right call. The implementation is small (a dependency-set per route, evaluated against a single shared health-cache) and the OpenAPI/docs surface keeps it discoverable.

**Dependency-set table (sample)**:
| Route prefix | Required dependencies |
|---|---|
| `/healthz` | (none) |
| `/metrics` | (none) |
| `/readyz` | (none — meta-endpoint) |
| `GET /api/v1/posts/:slug` | postgres |
| `POST /api/v1/posts` | postgres, redis (for invalidation outbox) |
| `POST /api/v1/media` | postgres, s3, redis |
| `/api/v1/jobs/*` | postgres, redis |
| `/wp-json/*` (shim) | postgres |

**Confidence**: high
**Reversibility**: cheap

---

### Q09-6: Plugin migration ordering
**Source**: doc 09 §22.3 — "install order" works if B is installed after A; a `requires: [...]` field with topological sort is the obvious fix.

**Proposal**: Implement `requires: [pluginA@>=1.2.0, pluginB]` in plugin manifests. Topological sort during the boot phase that runs plugin migrations. Refuse cycles at register-time with a clear error pointing at the cycle. Missing-required-plugin is a fatal boot error. Version constraints use a documented SemVer subset (no caret/tilde gymnastics — `>=`, `<`, `=`, and major-version equality only).

**Reasoning**: "Install order works" is a fragile contract — a CLI re-order, a config-driven install via Helm values, or a bulk re-enable all break it silently. A `requires` field is the standard fix in every package ecosystem; the work is one library call (`topo.Sort`) and one validator. Refusing cycles at register is mandatory because partial-cycle resolution is the worst kind of footgun. A small SemVer subset is what plugin authors actually use and avoids the npm-style range-spec rabbit hole.

**Confidence**: high
**Reversibility**: cheap

---

### Q09-7: Image base for web tier
**Source**: doc 09 §22.3 — `node:20-alpine` for debuggability vs. distroless-node for size.

**Proposal**: `node:20-alpine` for v1. Document size as ~180 MB compressed and CVE-scan results in CI. Revisit distroless in v1.2 with **measured** CVE noise from the v1 release window. The decision criterion to switch: if we're shipping security patches more than monthly purely to chase alpine package CVEs, distroless wins.

**Reasoning**: Distroless wins on size and attack surface; alpine wins on "can I `kubectl exec -it` and run `node` / `curl` / `dig` during a 2am incident." For a v1 product where the operator surface is still evolving, debuggability wins. The deferred-revisit is concrete (a CVE cadence threshold) so this doesn't become an indefinite "we'll get to it."

**Confidence**: medium
**Reversibility**: cheap

---

### Q09-8: Worker auto-tuning
**Source**: doc 09 §22.3 — HPA on `asynq_queue_depth` lags 10x bursts by ~1 min; KEDA-on-Redis would be faster.

**Proposal**: KEDA-on-Redis is the **documented default** for worker autoscaling in our Helm chart. The chart ships a `KEDA ScaledObject` resource pointed at the Asynq Redis broker, polling queue depth every 5s with a scale-out target of "queue depth per pod = 50." HPA-on-`asynq_queue_depth` (exposed as a Prometheus metric and fed via `external.metrics.k8s.io`) is documented as the **fallback** for clusters that can't install KEDA.

**Reasoning**: HPA's metrics pipeline has a 60–90s end-to-end lag (scrape interval + adapter polling + HPA reconciler period). For a CMS where bursts are common (a publish event triggers cache invalidation + thumbnail regen + RSS rebuild), that's a 10-minute backlog of jobs before the new pods land. KEDA polls Redis directly and reacts in ~10s. KEDA is widely adopted, the installation is one Helm chart, and it doesn't conflict with HPA on other dimensions (CPU, memory). The fallback path keeps us self-host-friendly for the K8s-minimalist crowd.

**Helm values surface**:
```yaml
worker:
  autoscaling:
    enabled: true
    mode: keda        # or "hpa"
    targetQueueDepthPerPod: 50
    minReplicas: 2
    maxReplicas: 30
    scaleDownStabilization: 300s   # avoid flapping on bursty workloads
```

**Confidence**: high
**Reversibility**: moderate (the Helm chart is part of the supported deploy surface)

---

# Doc 10 — Observability

### Q10-1: Long-term metric store
**Source**: doc 10 §20.1 — Thanos vs Mimir vs Grafana Cloud vs nothing-for-self-host.

**Proposal**: Two-track answer. (a) **Self-host reference stack**: Grafana Mimir, shipped as an optional Helm sub-chart (`gonext-mimir`). Self-hosters who don't enable it get Prometheus's local TSDB with 15-day retention — documented as "good enough for the first year." (b) **SaaS default**: Grafana Cloud free tier for early customers, with a documented "bring your own backend via OTLP" path. Thanos is rejected — Mimir is its successor and the project has more momentum. "Nothing for self-host" is rejected — site owners need at least a quarter of metric retention for capacity planning.

**Reasoning**: Mimir is the operationally-modern Thanos and is what Grafana Labs themselves run. Coupling our reference deployment to it gives us a stable target for docs, dashboards, and runbooks. The Grafana Cloud SaaS default piggybacks on a free tier that's sufficient for our typical SaaS customer's first year and pushes the bring-your-own decision to the customer when they outgrow it. The "nothing for self-host" non-answer would push operators to invent their own stacks per-deploy, which fragments the dashboards in §14a.

**Retention budget defaults**:
- Self-host without Mimir: 15 days (Prometheus local TSDB).
- Self-host with Mimir: 90 days hot, 1 year cold (Parquet on S3).
- SaaS standard tier: 30 days.
- SaaS enterprise: configurable up to 13 months.

**Confidence**: medium
**Reversibility**: moderate (the Helm chart and the dashboards are coupled)

---

### Q10-2: Continuous profiling enable-by-default in P6
**Source**: doc 10 §20.2 — Pyroscope ~free in ops cost; might just ship enabled.

**Proposal**: Off by default in v1; ship Pyroscope wiring as `gonext-pyroscope` Helm chart and a single env var (`GONEXT_PROFILING_ENDPOINT`) to enable in-process pprof shipping. Re-evaluate enabling-by-default after a P5 perf review *with a real customer workload*. The trigger to flip the default: 3 separate incidents where the root cause would have been faster to diagnose with continuous profiling than with traces+metrics.

**Reasoning**: "Free in ops cost" is the SaaS sales pitch; on self-host, every always-on subsystem is a thing the operator has to understand. Shipping it as an easy-toggle gets us 80% of the value (operators who want it can flip it on) without making a "huh, what's pyroscope?" moment for the median self-hoster. The trigger is concrete and incident-driven, which is the right bar to add an always-on dependency.

**Confidence**: high
**Reversibility**: cheap

---

### Q10-3: eBPF host-level visibility
**Source**: doc 10 §20.3 — useful for SaaS appliance, ops-heavy for self-host. Probably SaaS-only add-on.

**Proposal**: SaaS-only. Defer self-host availability until v2 at the earliest. The SaaS implementation uses Cilium Tetragon or Pixie (vendor TBD at SaaS GA); self-host gets a documented "if you want this, here's how to install it yourself" pointer. Do not ship anything eBPF-shaped in the v1 self-host Helm chart.

**Reasoning**: eBPF requires kernel-version compatibility, privileged daemonsets, and a substantial debugging story when probes fail. Inflicting that on every self-hoster is a non-starter; the value (host-level network/syscall visibility) is real but doesn't pay back at single-VPS scale. SaaS is the right form factor — we control the underlying kernel and ops team — and "v2 at earliest" prevents this from re-opening before we have the operational surface to support it.

**Confidence**: high
**Reversibility**: cheap

---

### Q10-4: OpenTelemetry collector deployment shape
**Source**: doc 10 §20.4 — sidecar per pod vs DaemonSet vs shared cluster. Forward to doc 11.

**Proposal**: **DaemonSet per node** as the default; sidecar-per-pod only for high-isolation tenants (SaaS premium tier with per-tenant collectors). Shared-cluster (single Deployment) is rejected — it's a single point of failure for observability. The default config: one collector pod per node, scrapes localhost stdout for logs, receives OTLP/gRPC from app pods on `localhost:4317`, batches and forwards to the chosen backend.

**Reasoning**: DaemonSet is the standard pattern, well-documented, and gives us one collector to debug per node. Sidecar-per-pod multiplies the collector count by app pod count, which is wasteful for >90% of deployments. A shared single-pod collector is fragile — its restart drops everyone's telemetry. The "per-tenant collector" sidecar mode is a premium feature where regulatory isolation actually matters (PII redaction config per tenant, for example). Doc 11 should ratify this but the default belongs here.

**Confidence**: high
**Reversibility**: moderate (the Helm chart's collector resource is committed)

---

### Q10-5: Audit log storage backend & signed batches
**Source**: doc 10 §20.5 — doc 06 owns this; observability subscribes. If signed batches, what's the key-management story?

**Proposal**: Defer storage backend choice to doc 06 (it's an auth/security concern, not an observability concern). Commit now to the **interface**: audit log batches are written to an S3 prefix (`s3://<bucket>/audit/<date>/<batch>.json.zst.sig`) with a detached signature using KMS-managed keys (AWS KMS / GCP KMS / age for self-host with operator-managed key). Observability subscribes via read-only IAM on that prefix — we do *not* duplicate audit content into the operational log stream. Key rotation is doc 06's problem; the verification flow (re-verify signature on read) lives in our admin UI's audit-viewer.

**Reasoning**: The audit log is a compliance artifact; mixing it with operational logs creates an attack surface where an operator with log-stream access can read audit data. Keeping the separation (separate bucket, separate IAM, separate signing) means observability tooling can read but not modify. Key management is hard but standard (KMS for cloud, age + operator-managed key for self-host); doc 06 is the right home for the rotation policy. This proposal commits the **interface** so doc 10's §6 (events vs audit) is unblocked.

**Confidence**: high
**Reversibility**: moderate

---

### Q10-6: RUM device classification
**Source**: doc 10 §20.6 — currently desktop/tablet/mobile from UA. Add `app-webview` / `headless`?

**Proposal**: Add `headless` (detected by `navigator.webdriver === true` and known UA strings). Skip `app-webview` for v1. Total label cardinality stays low (4 values: desktop/tablet/mobile/headless), which keeps the RUM metrics within the cardinality budget.

**Reasoning**: `headless` has a clear use case — bot/scraper triage in RUM dashboards, where you want to filter them out of P75 LCP measurements. `app-webview` has weaker signal: half of mobile traffic goes through in-app browsers (Instagram, Facebook, Twitter) and the categorization is genuinely useful for marketing teams, but the detection is a UA-sniffing rats nest that breaks on every iOS update. Ship the cheap, useful one; defer the expensive, fragile one until a marketing-side customer explicitly asks.

**Confidence**: medium
**Reversibility**: cheap

---

### Q10-7: Plugin sourcemap upload protocol
**Source**: doc 10 §20.7 — plugin doc 02 §SDK references it; observability depends on it for §8.5. Coordinate so the manifest field exists in P4.

**Proposal**: Add `sourcemap_url: <URL>` to the plugin manifest schema in P4. The URL points to a `.map.json` file uploaded alongside the plugin bundle. Required for plugins distributed via the marketplace; optional for self-host-only plugins (with a CLI warning that frontend errors will be unsymbolicated). The error tracker fetches sourcemaps lazily on first symbolication need, caches in S3 for the lifetime of that plugin version, GC'd when the version is unpublished.

**Reasoning**: Without sourcemap upload, plugin frontend errors land in Sentry/GlitchTip as minified-stack noise that's useless for debugging. The manifest field is small, the upload flow is standard (it's what every JS error tracker has), and making it required-for-marketplace is the lever that ensures coverage. Self-host-only opt-out is honest about the trade-off. The P4 coordination is the actual ask — this proposal commits the schema field now so doc 02 and doc 10 land aligned.

**Confidence**: high
**Reversibility**: cheap

---

### Q10-8: Trace propagation through CDN
**Source**: doc 10 §20.8 — most CDNs strip `traceparent`. Per-CDN config or mint fresh root span at edge?

**Proposal**: Mint a fresh root span at the CDN edge (Cloudflare Worker / Fastly VCL snippet shipped in the docs), and preserve `traceparent` only when it's verifiably from our own origin (signed via a short-lived HMAC over the trace-id + timestamp). The Cloudflare Worker / Fastly VCL is operator-installed from a recipe in `docs/observability/cdn-tracing.md`; we don't auto-install. Akamai and other CDNs get "best-effort, see docs."

**Reasoning**: Trying to preserve `traceparent` across an arbitrary internet-traversal is a security risk (clients can forge trace IDs to pollute the trace store with bogus spans, exhaust storage, or piggyback on internal trace IDs to learn about backend topology). Minting fresh at the edge is the safe-by-default move. The HMAC-signed preservation path lets first-party Next.js/Vercel deployments get end-to-end traces when they want them. The "per-CDN recipe" approach is honest about the fact that we can't auto-configure every CDN, and it puts the operational lift on the customer who wants the feature.

**Edge span shape**: the CDN worker emits a single span per request with attributes `cdn.pop`, `cdn.cache_status`, `cdn.tls_version`, `http.url`, and a `tracecontext.parent_verified=true|false` flag indicating whether an incoming `traceparent` survived HMAC verification.

**Confidence**: medium
**Reversibility**: moderate

---

### Q10-9: Cost cap mode
**Source**: doc 10 §20.9 — `GONEXT_OBSERVABILITY_BUDGET=cheap` to pre-configure sampling/retention?

**Proposal**: Yes, ship it. Define three profiles: `cheap`, `standard` (default), `verbose`. `cheap` sets: trace sampling 1% (vs 10% default), log level WARN (vs INFO), RUM beacon batched at 30s (vs 5s), metric scrape interval 60s (vs 15s). Document the dollar-per-month estimates for each profile against the reference Mimir/Loki/Tempo stack. The env var is read once at boot; changing it requires a restart.

**Reasoning**: Self-hosters on a $5/mo VPS will turn off observability entirely if the default cost is too high, which is worse for everyone than running them on a frugal profile. Pre-baked profiles are the right abstraction — fewer knobs than "set each sampling rate individually," and the names are self-explanatory in support tickets ("what's your budget profile?"). The three-profile choice (not two, not five) covers the meaningful gradient without over-fitting.

**Profile matrix**:
| Setting | `cheap` | `standard` | `verbose` |
|---|---|---|---|
| Trace sampling | 1% | 10% | 100% (with 1h auto-revert) |
| Log level | WARN | INFO | DEBUG |
| RUM beacon interval | 30s | 5s | 5s |
| Metric scrape interval | 60s | 15s | 15s |
| Trace retention (Tempo) | 24h | 7d | 7d |
| Log retention (Loki) | 3d | 30d | 30d |
| Estimated $/mo on Mimir+Loki+Tempo for 50 RPS | ~$15 | ~$80 | ~$200 |

The `verbose` profile auto-reverts after 1 hour to prevent leaving it on by accident — incident debugging is the only time you should be there.

**Confidence**: high
**Reversibility**: cheap

---

### Q10-10: Per-tenant observability for v2 multi-tenant
**Source**: doc 10 §20.10 — `tenant_id` label everywhere; question is per-tenant dashboards and cardinality budgets. Defer to v2.

**Proposal**: Defer dashboards and cardinality budgets to v2 as suggested, but commit now to **shipping the `tenant_id` label plumbing** in v1 (it's already a placeholder in §4). This means: every log, metric, trace, and RUM beacon carries `tenant_id` even in single-tenant v1 (where it's hardcoded `default`). Per-tenant dashboards arrive in v2 alongside the multi-tenancy model in doc 06; per-tenant cardinality budgets come once we have a customer with >1k tenants (the only point at which cardinality becomes a real problem).

**Reasoning**: Retrofitting a label dimension across logs+metrics+traces is painful — adding it now while the only value is `default` is cheap. The dashboard and budget features are correctly deferred because they don't have a meaningful design until the multi-tenancy model is locked. The cardinality concern is real but bounded: 1k tenants × 100 series each = 100k series, which is a large-but-manageable Prometheus instance; below that, it's a non-issue.

**Confidence**: high
**Reversibility**: cheap

---

## Appendix — How to read this doc

- **Confidence high** means "I would defend this in a design review and would be surprised if it gets overturned."
- **Confidence medium** means "this is my pick, but a reasonable engineer with different priors picks the other side."
- **Confidence low** would mean "barely better than a coin flip" — no proposal in this doc is low-confidence; if it were, the proposal would be an explicit defer instead.
- **Reversibility cheap** means a single PR can flip it.
- **Reversibility moderate** means a coordinated change across 2–5 docs/services, possibly a data migration.
- **Reversibility expensive** means it changes a contract customers depend on; would only flip on a major version.

The proposals here are inputs to a decision, not the decision itself. If you disagree with one, open a thread on the corresponding source doc's open-questions section and cite this doc's Q-id.

*End of doc 14.*
