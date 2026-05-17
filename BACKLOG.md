# Backlog

This file is the **committed-to-repo** view of what to work on, ordered by dependency. The **live** view lives at the [GoNext Project](https://github.com/orgs/Singleton-Solution/projects/4) — that's where statuses change as work moves. This file is updated periodically to mirror the project state and add rationale.

If the two disagree, the GitHub Project wins.

## Reading guide

- **Wave** = "you can start this once everything in earlier waves is done." Items in the same wave are independent of each other and can be done in any order or in parallel.
- **Status** values match the GitHub Project's Status field: `Backlog`, `Ready`, `In progress`, `In review`, `Done`.
- **Phase** = milestone in GitHub. P0 = Skeleton, P1 = CMS Core, P2 = Editor, P3 = Themes, P4 = Plugins, P5 = Migration, P6 = Polish.

---

## Phase P0 — Skeleton

Goal: a working chassis. Server boots, talks to DB, runs migrations, reports health.

### Wave 1 — foundation ✅ Done

Already in `main` (via commit `1feecc4` — see [HISTORY.md](./HISTORY.md)).

| # | Title | Status |
|---|---|---|
| [#1](https://github.com/Singleton-Solution/GoNext/issues/1) | Bootstrap monorepo layout | Done |
| [#145](https://github.com/Singleton-Solution/GoNext/issues/145) | slog logger with redaction | Done |
| [#10](https://github.com/Singleton-Solution/GoNext/issues/10) | Typed env config loader | Done |
| [#2](https://github.com/Singleton-Solution/GoNext/issues/2) | HTTP server chassis | Done |

### Wave 2 — DB foundation (do these next, in order)

These unlock every DB-touching feature in P1.

| # | Title | Why now | Depends on |
|---|---|---|---|
| [#3](https://github.com/Singleton-Solution/GoNext/issues/3) | Postgres pgxpool | DB connection foundation | Wave 1 |
| [#96](https://github.com/Singleton-Solution/GoNext/issues/96) | golang-migrate integration | Schema management | #3 |
| [#33](https://github.com/Singleton-Solution/GoNext/issues/33) | First migration (extensions + gen_uuid_v7) | Foundation every other migration depends on | #96 |
| [#8](https://github.com/Singleton-Solution/GoNext/issues/8) | /healthz, /readyz endpoints | Operational readiness | #3 |

Duplicates to consolidate before starting:
- [#4](https://github.com/Singleton-Solution/GoNext/issues/4) overlaps with #96 → close one.
- [#102](https://github.com/Singleton-Solution/GoNext/issues/102) overlaps with #8 → close one.

### Wave 3 — independent packages (parallel-safe)

Each touches a distinct directory; can be done in any order once Wave 2 lands.

| # | Title | Touches |
|---|---|---|
| [#109](https://github.com/Singleton-Solution/GoNext/issues/109) | argon2id password package | `packages/go/auth/password/` |
| [#36](https://github.com/Singleton-Solution/GoNext/issues/36) | HTTP security headers middleware | `packages/go/middleware/security/` |
| [#135](https://github.com/Singleton-Solution/GoNext/issues/135) | CSRF middleware (double-submit cookie) | `packages/go/middleware/csrf/` |
| [#150](https://github.com/Singleton-Solution/GoNext/issues/150) | Prometheus `/metrics` endpoint | `packages/go/metrics/` |
| [#158](https://github.com/Singleton-Solution/GoNext/issues/158) | Core HTTP metrics | `packages/go/middleware/metrics/` |
| [#105](https://github.com/Singleton-Solution/GoNext/issues/105) | Secret store interface + adapters | `packages/go/secrets/` |
| [#121](https://github.com/Singleton-Solution/GoNext/issues/121) | Boot-time required-secrets validation | `apps/api/cmd/server` |
| [#129](https://github.com/Singleton-Solution/GoNext/issues/129) | Env-var redacted dump | `packages/go/config` |
| [#195](https://github.com/Singleton-Solution/GoNext/issues/195) | Brute-force protection primitives | `packages/go/ratelimit/` |
| [#232](https://github.com/Singleton-Solution/GoNext/issues/232) | testcontainers helpers | `packages/go/testutil/containers/` |
| [#238](https://github.com/Singleton-Solution/GoNext/issues/238) | Per-test DB tx-rollback helper | `packages/go/testutil/` |
| [#43](https://github.com/Singleton-Solution/GoNext/issues/43) | `X-Content-Type-Options: nosniff` | `packages/go/middleware/security/` |

### Wave 4 — devops + auth glue (after Wave 3)

| # | Title | Touches |
|---|---|---|
| [#44](https://github.com/Singleton-Solution/GoNext/issues/44) | Multi-stage Dockerfile for core | `apps/api/Dockerfile`, `apps/worker/Dockerfile`, `cli/gonext/Dockerfile` |
| [#49](https://github.com/Singleton-Solution/GoNext/issues/49) | Multi-target Dockerfile for web | `apps/web/Dockerfile`, `apps/admin/Dockerfile` |
| [#57](https://github.com/Singleton-Solution/GoNext/issues/57) | Enhanced Docker Compose stack | `docker-compose.yml`, `docker-compose.dev.yml` |
| [#66](https://github.com/Singleton-Solution/GoNext/issues/66) | K8s Helm chart | `deploy/helm/gonext/` |
| [#72](https://github.com/Singleton-Solution/GoNext/issues/72) | HPA on core-api | `deploy/helm/gonext/templates/` |
| [#81](https://github.com/Singleton-Solution/GoNext/issues/81) | KEDA HPA on worker | `deploy/helm/gonext/templates/` |
| [#120](https://github.com/Singleton-Solution/GoNext/issues/120) | systemd + Caddy bare-metal | `deploy/bare-metal/` |
| [#112](https://github.com/Singleton-Solution/GoNext/issues/112) | Graceful shutdown (extend) | `packages/go/httpx/` |
| [#14](https://github.com/Singleton-Solution/GoNext/issues/14) | Dockerfile + compose (consolidate with 44/49/57) | (close one) |
| [#17](https://github.com/Singleton-Solution/GoNext/issues/17) | CI: lint, build, test, migrate up/down | `.github/workflows/ci.yml` |
| [#254](https://github.com/Singleton-Solution/GoNext/issues/254) | CI refinements (sharding, paths-filter, coverage) | `.github/workflows/ci.yml` |
| [#24](https://github.com/Singleton-Solution/GoNext/issues/24) | Auth middleware skeleton (cookie + JWT recognition) | `packages/go/middleware/auth/` |
| [#131](https://github.com/Singleton-Solution/GoNext/issues/131) | Session store (Redis-backed, opaque cookie) | `packages/go/session/` |
| [#184](https://github.com/Singleton-Solution/GoNext/issues/184) | Roles + capabilities + policy package | `packages/go/policy/` |
| [#188](https://github.com/Singleton-Solution/GoNext/issues/188) | Audit log table + emit | migration + `packages/go/audit/` |
| [#148](https://github.com/Singleton-Solution/GoNext/issues/148) | Email verification flow | depends on #131 |
| [#116](https://github.com/Singleton-Solution/GoNext/issues/116) | User signup flow | depends on #109, #131, users-table-migration |
| [#124](https://github.com/Singleton-Solution/GoNext/issues/124) | Login flow | depends on #109, #131 |
| [#29](https://github.com/Singleton-Solution/GoNext/issues/29) | OpenAPI 3.1 spec scaffold | `apps/api/openapi/` |
| [#12](https://github.com/Singleton-Solution/GoNext/issues/12) | Admin Next.js app scaffold | `apps/admin/` |
| [#240](https://github.com/Singleton-Solution/GoNext/issues/240) | Vitest + RTL setup | `apps/admin/` + `packages/ts/test-config/` |
| [#241](https://github.com/Singleton-Solution/GoNext/issues/241) | Playwright e2e harness | `tools/e2e/` |
| [#243](https://github.com/Singleton-Solution/GoNext/issues/243) | WP REST corpus generator | `tools/migrate-corpus/` |
| [#244](https://github.com/Singleton-Solution/GoNext/issues/244) | `gonext plugin test` runner | `cli/gonext/cmd/plugin/` |
| [#246](https://github.com/Singleton-Solution/GoNext/issues/246) | `gonext theme test` runner | `cli/gonext/cmd/theme/` |
| [#250](https://github.com/Singleton-Solution/GoNext/issues/250) | axe-core a11y tests | `tools/e2e/` |

---

## Phase P1 — CMS Core (92 issues)

After P0 lands. Posts, pages, custom post types, taxonomies, comments, media, users, REST API. Goal: usable enough to run a personal blog.

Detailed wave breakdown for P1 will be added once we're closer to P1 — too speculative right now. For now, filter the [GitHub Project](https://github.com/orgs/Singleton-Solution/projects/4) by Milestone = `P1 — CMS Core` and treat anything without a Dependencies block as Wave 1.

Key issues to be aware of:

- DB schema migrations (each is a separate issue): users (#39), post_types (#48), posts (#55), terms (#62), comments (#70), permalinks (#77), options (#92), sessions (#99), post_locks (#106).
- Search: Postgres FTS setup (#119).
- Revisions: snapshot/delta storage (#128), retention pruner (#169).
- Hook bus kernel (#178) — foundation for plugin system in P4.
- REST API skeleton (#76), GraphQL scaffold (#83).
- Admin shell + CRUD screens (#16, #19, #25, #31, #35, #38, #42, #50, #56, #65).
- WP REST shim basics (#89) — slots in here for P5 migration support.

---

## Phase P2 — Editor (26 issues)

Block JSON tree, Lexical rich text, ~20 core blocks, autosave, revisions, server-side block render.

Critical-path issues: #75 (block tree types), #79 (BlockTypeDefinition), #87 (Lexical integration), #134 (server-side render walker), #141 (core blocks tracker).

---

## Phase P3 — Themes (22 issues)

Template hierarchy, theme.json, classic + block themes, customizer, 2 reference themes (gn-hello, gn-pro).

Critical-path issues: #5 (theme.json schema), #7 (template hierarchy resolver), #11 (block theme seeding), #28 (gn-hello), #32 (gn-pro), #40 (theme SDK).

---

## Phase P4 — Plugins (51 issues)

The make-or-break phase. WASM runtime, hook bus, capability ABI, SDKs, signing, marketplace.

Foundation issues (must land first within P4): #6 (wazero runtime), #9 (instance pool), #15 (resource limits), #34 (manifest validator), #45 (lifecycle), #95 (hook handler entry point), #107 (capability registry), #275 (JSON Schema dialect pinning).

---

## Phase P5 — Migration (21 issues)

WordPress importer (dbdirect/WXR/REST), HTML→block converter, REST shim, 95% migration fidelity in the defined cohort.

Critical-path issues: #144 (importer scaffold), #147 (migration_map), #153 (WXR parser), #170 (HTML→blocks), #218 (verification gate), #219 (10-site corpus), #227 (REST shim).

---

## Phase P6 — Polish (16 issues)

Performance to published SLOs, docs site, security audit, marketing site dogfood, marketplace launch. v1.0 release gate.

Notable: #122 (Early Hints / HTTP 103), #126 (bundle budget enforcement), #132 (in-house RUM), #133 (`gonext bench` CLI), #215 (Grafana dashboards), #221 (System Status admin page), #224 (bug bounty), #229 (external pentest).

---

## How this file stays accurate

- Update **when a wave ships** (move done items from "next wave" to "done" within their phase block).
- Update **when an issue is closed/duplicated/split** (note the consolidation in the affected row).
- Do **not** update for every status change — that's what the GitHub Project view is for.
- Treat this file as **rationale + reading order**, not as a real-time dashboard.
