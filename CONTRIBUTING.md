# Contributing to GoNext

Thank you for considering a contribution. GoNext is community-driven and we need help across Go, TypeScript/React, design, docs, devops, and security.

## Before you start

1. **Read the relevant design doc** in [`/docs`](./docs). All architectural decisions are documented there. Implementation should follow the design unless you're proposing to change it (in which case, open a `design-discussion` issue first).
2. **Check open [issues](https://github.com/Singleton-Solution/GoNext/issues)** for what's available. Filter by:
   - `good-first-issue` — small, well-scoped, suited to newcomers
   - `help-wanted` — actively looking for contributors
   - `area:*` — pick by subsystem (api, web, admin, plugin-host, block-editor, theme-system, auth, media, migration, jobs, observability, security, ci, docs)
   - `skill:*` — pick by language/skill (go, react, ts, sql, devops, security, design, docs)
3. **Comment on the issue** to claim it before you start. Avoids duplicate work.

## Workflow

1. Fork the repo.
2. Branch from `main`: `git checkout -b feat/<short-description>` or `fix/<short-description>`.
3. Make your change. Keep PRs small and focused — one logical change per PR.
4. Write or update tests. See [docs/11-testing-ci.md](./docs/11-testing-ci.md) for testing strategy.
5. Update relevant docs/ADRs if you change architecture.
6. Run linters and tests locally (see project README in each subpackage once code exists).
7. Open a PR against `main`. Reference the issue you're closing: `Closes #123`.
8. Sign the CLA when the bot prompts on your first PR. This is required for the project to maintain licensing flexibility (see [`LICENSE`](./LICENSE)).
9. Address review comments. Squash + rebase if asked. Maintainers will merge.

## Commit messages

Conventional Commits style: `type(scope): short description`.

- `feat(plugin-host): add cache.invalidate host ABI`
- `fix(media): handle EXIF orientation correctly`
- `docs(adr): add ADR 0008 for block JSON tree`
- `test(auth): add policy package contract tests`
- `chore(ci): bump go to 1.24`

Types: `feat`, `fix`, `docs`, `style`, `refactor`, `perf`, `test`, `build`, `ci`, `chore`, `revert`.

## What we DO want

- Bug fixes with a reproducer.
- Features that match an existing issue (claim it first).
- Performance improvements with before/after benchmarks.
- Docs improvements (typos, clarity, examples).
- Test coverage for under-tested areas.
- ADRs proposing architectural changes (open as `design-discussion` issue first).

## What we DON'T want (without discussion)

- Drive-by refactors without a related issue.
- Reformatting/style-only PRs (style is enforced by linters).
- New features not on the roadmap. Open an issue first.
- Dependency upgrades without justification.
- "Improve performance" PRs with no measurement.

## Reporting bugs

Open an issue with the `bug` template. Include:
- GoNext version + git SHA.
- OS, Postgres version, Node version, browser (if frontend).
- Minimal reproduction steps.
- Expected vs actual behavior.
- Logs or stack traces.

## Reporting security issues

**Do not open public issues for security vulnerabilities.** See [SECURITY.md](./SECURITY.md) for the private disclosure process.

## Proposing design changes

For changes that touch architecture (new subsystem, change a public API, new dependency, change a core abstraction):

1. Open a `design-discussion` issue describing the problem, proposed solution, and alternatives considered.
2. Maintainers respond within 1 week with feedback or approval.
3. If approved, follow up with an ADR PR (see [`/adr`](./adr) for the format) and then the implementation.

## Style

### Go
- `gofmt`, `go vet`, `golangci-lint` enforce style.
- Prefer standard library first; vet new dependencies.
- Errors wrap with `%w`, not concatenation.
- Logging via `slog`, structured fields, never `fmt.Println` in shipped code.

### TypeScript / React
- `eslint` + `prettier` enforce style.
- Functional components, hooks. No class components.
- Type everything. `any` requires comment justifying.
- Server components default, client components when interactivity needed.

### SQL
- Migrations via `golang-migrate`. Forward-only after merge (rollbacks live in the next migration).
- Always reversible during PR review; once merged, treat as immutable.
- Use UUID v7 PKs, `timestamptz`, JSONB with explicit GIN indexes.

## Local development

(Setup instructions live in subpackage READMEs once they exist. Expect docker-compose for full stack, `make dev` shortcuts, and `pnpm dev` for frontend apps.)

## Getting help

- Open a `question` issue if blocked.
- Discussions: https://github.com/Singleton-Solution/GoNext/discussions
- Discord: (coming, see ROADMAP P0)

## Code of Conduct

Be kind. See [CODE_OF_CONDUCT.md](./CODE_OF_CONDUCT.md). Violations are addressed by maintainers and may result in PRs blocked or contributor banned.
