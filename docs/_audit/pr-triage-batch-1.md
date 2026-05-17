# PR Triage — Batch 1 (PRs #284–#303)

20 implementation PRs filed by parallel agents, each reviewed cold by an independent agent (no shared context with the author). This file records the verdicts and the concrete findings.

**Final: 11 MERGE, 9 NEEDS_CHANGES.**

For each NEEDS_CHANGES the reviewer reproduced a real bug or a real spec gap. None are "the diff isn't pretty" complaints. Every finding has a concrete fix.

## ✅ Ready to merge (11)

| PR | Closes | Subsystem | Coverage | Notes from reviewer |
|---|---|---|---:|---|
| **#286** | #150 | Prometheus `/metrics` scaffold | 94.2% | Clean. `MustBoundedLabels` advises (doesn't enforce) — strengthen later. |
| **#287** | #105 | Secret store (Env/File/Noop) | 96.7% | Rigorous redaction with sentinel tests, no caching, path-traversal guarded, stubs return explicit "not implemented." |
| **#293** | #120 | systemd + Caddy bare-metal | — | Production-grade hardening; reviewer noted more strict than checklist asked. |
| **#294** | #44 | Dockerfiles for Go binaries | — | All 3 build cleanly, distroless nonroot, ldflags injection verified. |
| **#295** | #184 | Policy / RBAC | 97.8% | Default-deny correct, strict-equality cap matching, hierarchy structurally guaranteed. |
| **#298** | #240 | Vitest + RTL test infra | — | End-to-end verified with smoke test; jest-dom matchers + jsdom + coverage all work. |
| **#299** | #29 | OpenAPI 3.1 + Swagger UI | — | Redocly lint clean, mount deferral honored, no XSS surface. |
| **#292** | #232 | testcontainers helpers | — | Real readiness waits (HTTP probes), `t.Cleanup` correct, sync.Once for skip-on-no-docker. |
| **#300** | #246 | `gonext theme test` CLI | 87.2% / 82.3% | Clean contract runner, real fixture tests, additive `main.go` delta. |
| **#301** | #243 | WP-shape corpus generator | — | Deterministic (byte-identical `diff -r`), 10 profiles with real variety, stdlib-only. |
| **#302** | #244 | `gonext plugin test` CLI | — | Capability vocab nit (extra `users.write`; missing `i18n`/`clock`); mechanical merge conflict with #300. |

## ⚠️ Needs changes (9)

| PR | Closes | Blocking issue | Severity |
|---|---|---|---|
| **#284** | #36 | Skips 3 explicit AC items: Server-header strip, per-request CSP nonce, RouteClass types | Spec gap |
| **#285** | #109 | `Verify` **panics** on adversarial PHC strings with `t=0` or `p=0` — crafted DB row crashes login | **Crypto/availability** |
| **#288** | #49 | Builder stage broken (missing `node_modules`); `CMD ["start"]` references nonexistent script | **Image doesn't run** |
| **#289** | #241 | TypeScript typecheck fails at `tools/e2e/fixtures/server.ts:52` with 2 `noImplicitAny` errors | Build-broken |
| **#290** | #135 | (1) Path-traversal `SkipPaths` bypass via `..`; (2) negative-TTL becomes `Max-Age=0` and deletes the cookie on issuance | **CSRF bypass** |
| **#291** | #131 | (1) Absolute TTL silently never enforced; (2) `user_sessions:{uid}` set TTL collapses on first Get; (3) session-resurrection TOCTOU race after Delete defeats "easy revoke" | **Session integrity** |
| **#296** | #195 | 3 explicit AC misses: per-email gating only on existing emails (enumeration oracle); durable `FailureStore` (in-memory only — restart wipes lockouts); audit events not emitted | Spec gap |
| **#297** | #188 | (1) X-Forwarded-For honored unconditionally → audit-log IP spoofable; (2) middleware silently drops `Emit` errors → silent audit-trail loss | **Audit integrity** |
| **#303** | #66 | (1) NetworkPolicy enabled = web/admin/worker can't reach api on 8080 (default-deny blocks SSR); (2) HPA-managed Deployments still emit static `replicas:` → every `helm upgrade` resets scaling | **K8s upgrade fights** |

## What stands out

### Three findings could have caused real outages
- **#285 (argon2id panic on `t=0`)** — Any attacker who can write a row into `users.password_hash` (e.g., SQL injection elsewhere, or a malicious admin) crashes login for everyone.
- **#290 (CSRF SkipPaths path-traversal)** — `POST /webhooks/../admin/users` skips CSRF protection while downstream routing canonicalizes to `/admin/users`. The reviewer reproduced this.
- **#291 (session-resurrection TOCTOU)** — Delete-then-Get race resurrects a session blob the `DeleteAllForUser` index thinks is gone. Defeats the entire opaque-cookie design.

### Three findings show the AC-vs-claim gap
- **#284**, **#296**, and (less severely) **#288** all closed their issues while skipping explicit acceptance criteria. The PR bodies were too confident. This is a process lesson: PR template should require "AC checklist with deviations explained" not just "implementation summary."

### The MERGE pile is genuinely clean
- **#293 (systemd)** went further than the checklist (`ProtectControlGroups`, `MemoryDenyWriteExecute`, etc.).
- **#287 (secrets)** added redaction-leak tests using sentinel values to prove the property.
- **#295 (policy)** structurally guarantees role hierarchy via `Union()` chaining, not assertion.

These three are good models for the next batch of PRs.

## Recommended next moves

### Option A — Land the 11 ready ones, fix the 9
1. Merge the 11 MERGE PRs in roughly the listed order (small/devops first, security-sensitive after each one has CI green). Aim for ~5 PRs/day so you have time to actually read them.
2. Hand back the 9 NEEDS_CHANGES PRs to the originating agents (or a fresh fix-it agent) with the reviewer's findings appended to each. Each fix is small; aim for a single round of changes per PR.

### Option B — Bulk fix-up batch
1. Dispatch 9 "fix-it" agents in parallel, one per NEEDS_CHANGES PR. Each agent gets the reviewer comment as input and lands a follow-up commit on the same branch.
2. Re-review the fixed commits.
3. Then merge all 20.

**My recommendation: Option A.** The 11 MERGEs include the foundation pieces (secrets, policy, metrics, test infra) that make the next round of work easier. Landing them unblocks the next implementation batch. Fix the 9 in a tighter feedback loop.

## Maintenance

This file is the audit trail. Don't update it as PRs land — open a new triage doc when you do the next batch.
