# ADR 0006: Single monorepo using Go workspaces and pnpm workspaces

- **Status**: proposed
- **Date**: 2026-05-17
- **Deciders**: @tayebmokni
- **Consulted**: proposal S4 in `docs/proposals/14-proposals-strategic.md`
- **Informed**: contributors, CI authors

## Context

GoNext is a polyglot project: a Go backend (API server, Asynq workers, WASM plugin host, CLI), two Next.js frontends (public site, admin — see ADR 0007), shared TypeScript SDK packages (block SDK, theme SDK, plugin SDK client), shared Go packages (auth, policy, plugin host internals), first-party plugins (in both Go and TypeScript depending on the plugin), themes, documentation source, and code generation tooling.

These artifacts have **tight coupling** that the repository structure has to respect or fight:

- The block schema (TypeScript types in the editor, Go renderer in the server, JSON schema in the database) must stay in sync. A breaking change to the schema lands in three packages atomically.
- The plugin SDK ABI version (doc 02 §4.3) is shared between the host (Go), the plugin client SDKs (Rust, TypeScript), and any first-party plugins. ABI v1 → ABI v2 is one logical change that touches many directories.
- The admin app talks to the Go API via a generated client. Schema changes flow API → generated client → admin code in one PR.

In a polyrepo world, every one of those couplings becomes a coordinated multi-PR dance with version pinning, registry publishes, and brittle "which version of X is compatible with which version of Y" matrices. WordPress's separate-repos-for-everything model is part of why integrating the admin (Calypso), the editor (Gutenberg), and the server is a perennial coordination headache.

In a monorepo, the same change lands atomically. Tooling has caught up: Go 1.18+ workspaces and pnpm workspaces both handle multi-package coordination natively, without the heavy machinery of Nx, Turborepo, or Bazel. Change-aware CI (only rebuild what was touched) is straightforward with the right path filters; nothing forces us to "rebuild the world" on every PR.

The intended layout is (from proposal S4):

```
gonext/
  apps/
    api/          # Go server (HTTP + WASM host)
    worker/       # Go background worker (Asynq consumer)
    web/          # Next.js public site
    admin/        # Next.js admin dashboard
  packages/
    go/           # shared Go packages
    ts/           # shared TS packages (block SDK, theme SDK, plugin SDK client)
  plugins/        # first-party plugins
  themes/         # first-party themes
  cli/            # Go CLI binary
  tools/          # codegen, build scripts, lint config
  docs/           # public docs site source
  adr/            # this directory
```

`go.work` at the root composes the Go modules. `pnpm-workspace.yaml` at the root composes the TypeScript packages. The two coexist cleanly — they manage non-overlapping languages.

## Decision

GoNext lives in a single monorepo. Go modules are composed via `go.work` workspaces. TypeScript packages are composed via pnpm workspaces. CI is change-aware: a PR that only touches `themes/gn-hello` does not rebuild the API. We do not adopt a heavier monorepo tool (Nx, Turborepo, Bazel) unless and until the workspace primitives become insufficient.

## Consequences

### Positive

- Atomic cross-package changes. A block schema update lands in editor, renderer, and database migration in one PR with a single review pass.
- No version drift between internal packages. The TypeScript SDK in `packages/ts/sdk` is always exactly the version the admin app builds against, because they share the workspace.
- One source-of-truth for tooling: one lint config, one Prettier config, one CI workflow that fans out by path filter, one `gofmt`/`golangci-lint` config.
- Onboarding is trivial: `git clone`, `pnpm install`, `go build ./...`, done. No "install these N repos in the right order."
- Refactoring is honest. Renaming a Go interface that's implemented across multiple packages touches all of them in one PR; the diff is the truth.
- Code search across the project is one `grep`.

### Negative

- Repo size grows over time. Git operations slow down on very large repos — we are unlikely to hit that ceiling in the v1 timeframe, but it is real for projects at the scale of, say, Linux or Chromium.
- A weak permissions story: every contributor with push access sees everything. We do not have a use case for repo-level permission partitioning, but if we did, monorepo loses to polyrepo.
- CI must be set up carefully. A naive "run everything on every PR" gets slow fast. We commit to change-aware CI from day one (path filters in GitHub Actions, build caches).
- Tooling that does not understand workspaces (some legacy IDE integrations, some dependency scanners) will need configuration.

### Neutral / accepted tradeoffs

- We are explicitly not adopting a heavier monorepo orchestrator yet. The combination of go.work + pnpm workspaces is enough for the foreseeable future. We will revisit if we hit specific pain (cross-language dependency graphs, distributed build caching at scale).
- First-party plugins and themes live in the same repo. Third-party plugins are in their own repos and consume the SDK as a published package (Apache 2.0 per ADR 0001).

## Alternatives considered

### Option A: Polyrepo (one repo per package)
- Rejected. Version coordination across N repos is the documented WordPress-ecosystem failure mode. Every cross-cutting change becomes a multi-PR dance with version-pinning brittleness. Reviewing API + generated client + admin code requires three open PR tabs across three repos, which kills review quality.

### Option B: Nx
- Rejected. Powerful but opinionated; we would inherit Nx's task graph mental model and its config surface. For a two-language project with two workspace types, native workspaces are simpler and the marginal benefit of Nx's task orchestration is small at our scale.

### Option C: Turborepo
- Rejected for the same reason as Nx — extra layer on top of pnpm workspaces that we do not need yet. Worth revisiting if remote caching becomes a measurable CI bottleneck.

### Option D: Bazel
- Rejected. Excellent at scale but the learning curve is brutal, the BUILD-file boilerplate is heavy, and the polyglot integration story for Go + Next.js is not as clean as the dedicated tooling each language ships. We are not Google.

### Option E: Two repos (one for Go backend, one for TS frontend)
- Considered. The split is intuitive because the languages are different, but the cross-cutting changes (block schema, plugin SDK ABI) cross the boundary often enough that the seam would chafe. Rejected in favor of one repo.

### Option F: Git submodules
- Rejected. Inherits polyrepo's coordination cost plus the ergonomic disaster of submodule UX. Used as a last resort, never by choice.

## References

- Proposal: `docs/proposals/14-proposals-strategic.md` §S4 (repository structure)
- Go workspaces: https://go.dev/doc/tutorial/workspaces
- pnpm workspaces: https://pnpm.io/workspaces
- Related ADRs: ADR 0007 (separate Next.js apps live in `apps/web` and `apps/admin`), ADR 0005 (WASM host lives in `packages/go`)
