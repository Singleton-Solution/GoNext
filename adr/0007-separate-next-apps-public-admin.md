# ADR 0007: Public site and admin dashboard are separate Next.js apps

- **Status**: proposed
- **Date**: 2026-05-17
- **Deciders**: @tayebmokni
- **Consulted**: doc 00 §2 (stack table), doc 09 (deployment ops), proposal Q00-1
- **Informed**: frontend authors, deploy ops

## Context

GoNext exposes two distinct frontends to humans: the **public site** (where readers consume content — SSR/SSG/ISR-shaped, aggressively cached, theme-rendered) and the **admin dashboard** (where editors and admins manage content — SPA-shaped, interactive, auth-gated, never cached). Doc 00 §2 acknowledges the open question of whether these are one Next.js app or two; proposal Q00-1 answers it.

The two workloads pull in opposite directions on almost every axis:

| Concern | Public site | Admin |
|---|---|---|
| Rendering | SSR + ISR with RSC | Mostly client-side, after a thin SSR shell |
| Caching | Aggressive (CDN, Next ISR, fragment cache) | None or per-user only |
| Auth | Mostly anonymous; logged-in is a minority path | Always authenticated |
| Bundle weight | Tightly budgeted (LCP matters) | Heavy — the block editor alone is large |
| Routing | Theme-driven, dynamic paths | Fixed admin routes |
| Update cadence | Slow (themes change rarely) | Fast (admin features iterate) |
| Deploy artifact | One per theme version | One per admin release |

A single Next.js app forces awkward compromises. Route-group gymnastics keep the trees separate but the bundle splitting is never as clean as separate apps — code-splitting in Next.js has gotten dramatically better with App Router, but the admin's heavy dependencies (Lexical, the block editor scaffolding, the admin component library) still leak into the public chunk graph in practice unless they are gated behind dynamic imports the linker can fully prove out.

The auth and caching shapes are also fundamentally different. Public site responses are cacheable; admin responses must never be cached. Mixing them in one app forces the cache layer (CDN, Next ISR, fragment cache from doc 07 §15) to handle both shapes, which is a known source of "I'm seeing somebody else's draft" bugs.

Same-origin serving keeps the auth story simple. The Go API server reverse-proxies `/wp-admin` (or whatever path we choose — proposal Q00-1 suggests `/wp-admin` for WP-muscle-memory) to the admin Next.js app, and everything else to the public Next.js app. Cookies are scoped to the parent origin, both apps see the same session, and a logged-in editor can flip between editing and previewing without a re-auth.

## Decision

The public site and the admin dashboard are **two separate Next.js apps** in the same monorepo (ADR 0006), under `apps/web` and `apps/admin` respectively. They share UI primitives, the API client, and auth helpers via workspace packages (`packages/ts/*`), but ship distinct bundles, distinct route trees, distinct CI build artifacts, and distinct deploy lifecycles. Both are served same-origin via the Go server's reverse proxy: `apps/admin` at `/wp-admin/*`, `apps/web` at everything else.

## Consequences

### Positive

- Bundle weight isolation. The public site's LCP-critical pages are never accidentally bloated by an admin-only dependency. The admin's heavy editor bundle (Lexical, ADR 0009) never ships to a reader.
- Cache shape isolation. Public site can run RSC + ISR + fragment cache without the admin's "no-cache, no-store" headers leaking in. Admin responses cannot accidentally cache user data.
- Release cadence isolation. We can ship the admin three times in a week without redeploying the public site, and vice versa.
- Deploy topology flexibility. The public site can run on the edge (Vercel, Cloudflare); the admin can run anywhere with persistent connections (WebSocket later, per Q05-3). Putting them in one app would constrain both to the lowest-common-denominator runtime.
- Code review focus. A PR to the admin is reviewed by admin reviewers; a PR to the theme renderer is reviewed by theme reviewers.

### Negative

- Duplicated tooling configuration: two `next.config.js`, two `tsconfig.json` (sharing a base), two ESLint setups, two CI workflows. Mitigated by shared base configs in `packages/ts/configs`.
- Shared components must live in a workspace package, not be inline-imported across app boundaries. This is the right pattern but adds a tiny indirection cost.
- Two SSR runtimes to operate, monitor, and scale. The Go server's reverse proxy adds one hop. Both costs are small in practice.
- Two bundles to type-check on a refactor that crosses the boundary (e.g., a change to the shared API client). Mitigated by workspace-aware tsc and CI fans-out.

### Neutral / accepted tradeoffs

- Both apps are Next.js. We are not picking Vite for the admin (doc 00 §2 lists it as a fallback option). Reasons: shared workspace tooling, shared React version (proposal Q02-4), shared SSR shell story for the admin's initial paint. The block editor itself is an SPA-shaped workload, but it runs inside Next.js fine.
- We pick same-origin routing over a separate subdomain. Subdomain (`admin.example.com`) would require cross-origin cookie work, CORS for the API, and a separate TLS cert. Same-origin with path-based routing avoids all of that.
- The admin app is the only one that consumes the admin REST + GraphQL surface (doc 05). The public site consumes a narrower read-mostly REST API plus the public GraphQL.

## Alternatives considered

### Option A: One unified Next.js app with route groups
- Rejected. The bundle isolation is never as clean as separate apps in practice. The auth/cache tradeoffs differ enough between public and admin that "shared by default, divergent by config" is the wrong default. Doc 00 §7 lists this as an open question; proposal Q00-1 closes it in favor of separate apps with "high confidence."

### Option B: Vite SPA for admin, Next.js for public
- Rejected. We lose the shared workspace tooling and shared React version story. We gain a slightly smaller admin bundle and slightly faster dev server. The trade is not worth the operational and learning cost of running two frontend toolchains.

### Option C: Separate codebases (two repositories)
- Rejected. Duplicated tooling, fragmented commits, version-coordination cost on the shared API client. ADR 0006 commits to a monorepo for exactly the reasons that apply here.

### Option D: Admin embedded in the Go server (Go templates or HTMX)
- Rejected. The block editor (doc 04) is a heavy React SPA; rewriting it in HTMX is not just impractical, it is a different product. The admin's interaction model demands a real client-side framework.

### Option E: Subdomain split (`admin.gonext.example.com`)
- Rejected. Same-origin path-based routing avoids the cross-origin cookie story and the second TLS cert. We can revisit if a future deploy shape requires geographic split.

## References

- Design doc: `docs/00-architecture-overview.md` §2 (stack table), §7 (open questions)
- Design doc: `docs/09-deployment-ops.md` (deployment topology)
- Proposal: `docs/proposals/14-proposals-foundation.md` Q00-1 (admin in same app or separate)
- Related ADRs: ADR 0006 (monorepo layout)
