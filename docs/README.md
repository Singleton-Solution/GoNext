# WordPress Clone — Design Documents

Fourteen documents totalling ~18,000 lines describing how to build a modern WordPress alternative on Go + Next.js + Postgres. Status: **design phase only — no code has been written.**

Docs 00–08 were the first pass. A cross-doc review found 24 contradictions and 44 gaps. Docs 09–13 plus surgical edits to 01–08 closed all 9 build-blockers and all 16 P0 gaps. The original review files are kept as `_review-*-v1.md` for the audit trail.

## Read in this order

| # | Doc | Lines | What it covers |
|---|---|---:|---|
| 00 | [Architecture Overview](00-architecture-overview.md) | 160 | Shared foundation. Stack, three hard problems, topology, phasing. **Read first.** |
| 01 | [Core CMS & Data Model](01-core-cms.md) | 1,294 | Posts, taxonomies, JSONB metadata, revisions, states, comments, permalinks, FTS, custom fields, full Postgres DDL with **UUID v7 PKs**. |
| 02 | [Plugin System](02-plugin-system.md) | 2,144 | Hardest subsystem. WASM via wazero, hook bus, capability-scoped ABI (incl. `cache.invalidate`, `audit.emit`), `manifest.json` format, SDK in Go/Rust/TS, signing. |
| 03 | [Theme System](03-theme-system.md) | 1,297 | Template hierarchy, theme.json, classic vs block themes (FSE), customizer, child themes, SSR + ISR. |
| 04 | [Block Editor](04-block-editor.md) | 1,429 | JSON block tree, Lexical, dynamic blocks via plugin WASM, custom fields panel, paste pipeline. Revisions delegated to doc 01. |
| 05 | [Admin & API](05-admin-api.md) | 1,653 | Admin IA, REST + GraphQL, WP REST shim at `/wp-json/wp/v2/...` with four auth mechanisms, `gonext` CLI, ISR revalidate contract. |
| 06 | [Auth & Permissions](06-auth-permissions.md) | 1,237 | argon2id, Redis sessions, passkeys/OIDC/2FA, roles+caps (`admin` slug), Go policy package, audit log, GDPR. |
| 07 | [Media & Performance](07-media-performance.md) | 1,280 | Direct-to-S3 upload, libvips image proxy with single-flight, HLS video, 5-layer cache + tag invalidation, plugin invalidators, in-house RUM. |
| 08 | [Migration & WP Compat](08-migration-compat.md) | 1,185 | dbdirect/WXR/REST importers, HTML→block conversion, phpass→argon2id, unified `redirects` table, ACF, WP REST shim. |
| 09 | [Deployment & Ops](09-deployment-ops.md) | 1,471 | Process topology, container strategy, K8s + Compose + bare-metal, boot/shutdown, multi-region, blue/green, cron leader election. |
| 10 | [Observability](10-observability.md) | 1,130 | OTel-unified logs/metrics/traces/events/errors/RUM, full metric catalog, cardinality budget, plugin observability ABI. |
| 11 | [Testing & CI](11-testing-ci.md) | 941 | Pyramid, theme + plugin contract test CLIs, WASM host tests, OpenAPI/GraphQL diff, migration corpus, Playwright, load gates. |
| 12 | [Jobs & Cron](12-jobs-cron.md) | 1,148 | Asynq ownership: queue topology, task catalog, retry/DLQ, idempotency, plugin scoping, cron leader election (Redis lease), webhook delivery. |
| 13 | [Security Baseline](13-security-baseline.md) | 1,343 | Threat model, HTTP headers, CSP (public/admin/plugin), XSS pipeline, secret tiers, supply chain (Sigstore + SBOM), SSRF guard, GraphQL cost. |

### Proposals — opinionated answers for all open questions
- [14-proposals.md](14-proposals.md) — index of all 159 proposals (17 strategic + 142 doc-level)
- [14-proposals-strategic.md](14-proposals-strategic.md) — wedge, license, customer, repo, marketplace, launch criteria (S1–S17)
- [14-proposals-foundation.md](14-proposals-foundation.md) — answers for docs 00–03 (37)
- [14-proposals-content.md](14-proposals-content.md) — answers for docs 04–07 (42)
- [14-proposals-platform.md](14-proposals-platform.md) — answers for docs 08–10 (30)
- [14-proposals-ops-sec.md](14-proposals-ops-sec.md) — answers for docs 11–13 (33)

### Reviews (audit trail, superseded)
- [_review-contradictions-v1.md](_review-contradictions-v1.md) — 24 contradictions found in the first pass; all blockers fixed.
- [_review-gaps-v1.md](_review-gaps-v1.md) — 44 gaps found; P0s closed via docs 09–13.

## Cross-cutting decisions (every doc honors these)

**Storage**
- Postgres 15+. **UUID v7 PKs everywhere**, `gen_uuid_v7()` is the canonical generator. No BIGSERIAL.
- JSONB for plugin-extensible metadata + GIN indexes on JSON paths.
- Content stored as JSON block tree in `posts.content_blocks`; pre-rendered HTML cached in `posts.content_rendered`.
- One `post_revisions` table (delta-aware, owned by doc 01); autosaves are `kind='autosave'`.
- One `redirects` table (owned by doc 08); doc 01's `permalinks` is the forward lookup.

**Plugins**
- WebAssembly via `wazero`. `manifest.json` is the canonical format.
- Two manifest slots: `capabilities` (WASM sandbox permissions) and `grants_capabilities` (user-facing caps the plugin registers).
- Plugins invalidate cache via `host.cache.invalidate(tags)` under the `cache.invalidate` capability.
- Plugins emit audit events via `host.audit.emit(...)` under the `audit.emit` capability.
- Plugin DB isolation is app-level (per-plugin Postgres role + scoped views), not RLS.
- Plugin admin pages declared in manifest `admin_pages`; SDK `AdminMenu.register()` is build-time sugar.
- Plugin-registered REST routes mount at `/api/plugins/{slug}/...`; auth inherits middleware; per-route capability required from manifest.

**Frontend**
- Next.js App Router (RSC) for public, separate Next.js app for admin.
- shadcn/ui + Tailwind + Radix for admin.
- ISR via Next.js; tag-based invalidation. `POST /internal/revalidate` is the cross-process contract (HMAC auth).

**Auth**
- Opaque Redis-backed cookie sessions for browsers, JWT for API tokens.
- Roles: `super_admin`, `admin`, `editor`, `author`, `contributor`, `subscriber`.
- Policy decisions via a Go policy package; two-layer enforcement (middleware + service).
- Plugins never inherit user capabilities; manifest-declared sandbox perms are the only authority.

**Background**
- Asynq, Redis-backed. 7 queues. Cron via Redis lease (15s TTL). Plugin jobs in `plugins` queue, namespaced + rate-limited.

**Observability**
- OpenTelemetry. `slog` JSON logging. Prometheus `/metrics`. RUM via `/_/rum/beacon`. ClickHouse for RUM (Postgres fallback).

**Security**
- Sigstore signing on core, plugins, and themes. SBOMs (CycloneDX). govulncheck + osv-scanner in CI.
- Three-tier secret manager (system / per-plugin / per-user).
- SSRF guard: DNS-pinned outbound HTTP client used by plugins, webhooks, image proxy, OAuth callbacks.
- CSP per route class with nonces. Trusted Types in admin.

## Still genuinely deferred (not yet documented)

- **Disaster recovery / backups** (P0; mentioned in 07 §26 and 09; needs a dedicated doc).
- **eCommerce** (acknowledged WooCommerce-shaped hole, defer to v2+; reserve namespaces in v1).
- **Forms, SEO surface** (P1; specced as reference plugins, not yet detailed).
- **Email/transactional notifications** (P1; capability declared but adapter/templates/deliverability deferred).
- **Content i18n** as a Polylang/WPML replacement (P1; admin UI strings handled in 05 only).
- **Accessibility as project-wide policy** (P1; touched in 04/05/11).
- **Billing / SaaS multi-tenant** (P2; reserve schema only).

## Headline numbers

- **Total design**: ~18,000 lines across 14 docs, with audit trail.
- **Effort estimate (2 engineers)**: ~24 months to credible v1, realistic 36+.
- **Hardest subsystem**: plugins (doc 02). Win condition and biggest engineering risk.
- **Highest adoption blocker**: migration (doc 08). No migration story = no users.
- **Biggest competitive lever vs WP**: performance (doc 07) + clean plugin sandbox (doc 02) + a real security baseline (doc 13).

## Honest take

The design is now buildable. The 9 build-blocker contradictions are reconciled. The 16 P0 gaps are closed. The remaining deferred topics are honest deferrals, not unknown unknowns.

The unresolved strategic question is still the same: **what does this CMS do that Strapi, Payload, Ghost, Directus, and Sanity don't?** No technical doc answers that. Decide it before writing a line of code.
