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
8. **Sign off your commits** (see [DCO sign-off](#dco-sign-off) below). The CI check will fail otherwise.
9. Address review comments. Squash + rebase if asked. Maintainers will merge.

## DCO sign-off

GoNext uses the **Developer Certificate of Origin** (DCO). Every commit you submit must include a `Signed-off-by:` line in its message, attesting that you have the right to submit the code under the project's license. There is no separate document to sign — the trailer in the commit message is the whole mechanism. See [ADR 0002](./adr/0002-dco-requirement.md) for the rationale.

The easiest way: pass `-s` to `git commit`:

```bash
git commit -s -m "feat(api): add /api/v1/posts endpoint"
```

This appends a line like the following to your commit message:

```
Signed-off-by: Your Real Name <your.email@example.com>
```

Use the **real name** and **email** configured in `git config user.name` and `git config user.email`. Anonymous or pseudonymous commits are not accepted.

If you forget to sign off:

```bash
# Most recent commit only:
git commit --amend --signoff --no-edit

# Last N commits:
git rebase HEAD~N --signoff

# All commits on your branch since main:
git rebase origin/main --signoff

# Then force-push (only your branch, never main):
git push --force-with-lease
```

A required CI check at `.github/workflows/dco.yml` blocks merge until every commit on your PR is signed off.

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

## Policy on AI-generated content

We use AI assistance ourselves and welcome contributions that were drafted with AI help. What we **do not** accept:

- **Unsolicited "AI auto-generated solution" comments dropped on issues.** These will be hidden as off-topic without further notice. We will not adopt code that lives only in issue comments.
- **PRs that are clearly AI dumps with no human review.** Hallmarks: code doesn't match the repo's patterns, tests don't exercise real edge cases, commit messages are templated, the PR author hasn't engaged with the design docs. We will close such PRs and may block the author from the repo.
- **Code that the author has not personally read, understood, and tested.** This is the DCO promise: by signing off you attest the work is yours to submit. Generated code is fine — but you have to own it, test it, and be able to explain it under review.

Good AI use looks like: you read the design doc, drafted with AI help, reviewed the output line by line, ran the tests, added the edge cases the AI missed, and opened a PR that holds together as a coherent change. Bad AI use looks like: drop a comment with code, vanish.

If you're not sure whether your contribution will be received well, open a `design-discussion` issue first and ask.

## Security review checklist

Every PR that touches an authenticated endpoint, an admin surface, a
plugin host boundary, or a SQL query must walk through this checklist
before requesting review. Tick each item in the PR description or
explain why it doesn't apply.

The reviewer is responsible for spot-checking the answers — items
left blank or hand-waved ("N/A" with no reason) block merge.

- [ ] **Input validation.** Every request payload is validated against
  a typed shape (Go struct + `validate` tags / TS Zod) before it
  reaches the business logic. Unexpected fields are rejected by
  `additionalProperties: false` at the schema layer, not silently
  ignored. Required fields are checked explicitly. Length, range,
  and format limits are enforced server-side regardless of any
  client-side validation.
- [ ] **Sanitization.** Output that lands in HTML is escaped (React
  does this by default; raw insertions via `dangerouslySetInnerHTML`
  use `DOMPurify` from `apps/admin/src/components/SafeHTML.tsx`).
  SQL is parametrized — no `fmt.Sprintf` into a query. Shell
  invocations are avoided; when unavoidable, they use `exec.Command`
  with an explicit argv (never `bash -c`).
- [ ] **Auth check.** The endpoint requires authentication (or has a
  documented public-by-design reason). Mounted behind
  `auth.RequireSession` or equivalent. Anonymous endpoints have a
  one-line comment explaining why.
- [ ] **Capability gate.** Beyond authentication, every privileged
  action goes through `policy.Can(...)` with a specific capability
  constant. No role-string comparisons in the handler body. If a
  new capability is introduced, it's added to
  `packages/go/policy/capabilities.go` and to the default role
  mapping in `defaults.go`.
- [ ] **Rate limit.** Login, password reset, email verification,
  and any high-value mutation surface go through the limiter from
  `packages/go/ratelimit`. If a new bucket is added, the default
  policy is documented and conservative (fail-closed when Redis is
  unavailable, except where the doc explicitly approves fail-open).
- [ ] **Audit emit.** Every privileged action emits an
  `audit.Event`. Event type uses the dotted convention
  (`auth.login.success`, `plugin.activated`). Severity matches the
  matrix in `docs/06-auth-permissions.md` §13. Metadata is bounded
  (no caller-controlled blobs).
- [ ] **CSRF.** State-changing endpoints (POST/PUT/PATCH/DELETE) are
  same-origin via session cookies + SameSite=Lax, OR they require
  a CSRF token from `auth/csrf`. The reviewer confirms the
  middleware is in the chain.
- [ ] **Secret handling.** No credentials, tokens, or PII in log
  lines. `slog` attrs use the structured form so the redactor in
  `packages/go/log` can mask sensitive keys. Secrets read from
  config use the masked accessor (`cfg.Auth.Pepper` is masked when
  the config is dumped).
- [ ] **Dependency vulns.** New direct deps are checked against
  `osv-scanner` / `pnpm audit`. Transitive vulns are noted in the
  PR description with a remediation plan (or a documented accept).
  CI runs the scanners on every PR, but humans should still look at
  the manifest diff.
- [ ] **Error message info leak.** Error responses don't echo
  internal paths, SQL fragments, or stack traces. The error code
  is a short slug (`not_found`, `internal_error`); the human
  message is generic. Detailed diagnostics live in the structured
  log, never in the response body.

If your change is a pure documentation update, a comment-only refactor,
or a test-only patch, this section can be skipped — say so in the PR
description.

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
