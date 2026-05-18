# `@gonext/e2e` — Playwright end-to-end harness

End-to-end tests for GoNext. Runs against the local `docker-compose` stack
(or any environment reachable over HTTP). See `docs/11-testing-ci.md` §11
for the broader e2e plan.

This is the **scaffold** introduced by issue #241. Richer journeys
(auth, dashboards, tenant isolation, axe-core a11y) land in follow-up
issues.

## Layout

```
tools/e2e/
├── package.json            # @gonext/e2e
├── playwright.config.ts    # 3 projects (chromium/firefox/webkit), CI-aware retries, traces on failure
├── tsconfig.json
├── fixtures/
│   └── server.ts           # Skips tests with a clear message if the stack is not up
├── tests/
│   ├── smoke.spec.ts       # Home page returns 200
│   └── a11y/               # WCAG 2.1 AA scans (issue #250)
│       ├── helpers/axe.ts          # AxeBuilder wrapper, standard ruleset, carve-outs
│       ├── fixtures/markup.ts      # Static HTML for offline scans (mirrors live surfaces)
│       ├── homepage.spec.ts
│       ├── admin-login.spec.ts
│       ├── admin-post-list.spec.ts
│       └── admin-block-editor.spec.ts
└── README.md               # this file
```

## Running locally

Prerequisites: Node 20+, `pnpm`, Docker, the GoNext repo.

```bash
# 1. Bring up the stack from the repo root
make up

# 2. From this directory, install deps + browsers (first run only)
cd tools/e2e
pnpm install
pnpm exec playwright install --with-deps

# 3. Run the suite
pnpm test

# Variants
pnpm test:a11y          # run only the axe-core a11y subset (issue #250)
pnpm test:headed        # watch the browser
pnpm test:ui            # Playwright UI mode
pnpm test:list          # list tests without running
pnpm typecheck          # tsc --noEmit

# From the repo root
make a11y               # runs the a11y subset only
```

### A11y suite

Issue #250 wires `@axe-core/playwright` into every interactive surface.
Specs live under `tests/a11y/` and follow these rules:

- **Standard ruleset**: WCAG 2.1 AA (`wcag2a`, `wcag2aa`, `wcag21a`,
  `wcag21aa`). See `tests/a11y/helpers/axe.ts`.
- **Documented carve-out**: `color-contrast` is disabled inside
  `.gonext-block-edit-canvas` because theme tokens (issues #354 / #358)
  are resolved against the live theme bundle the e2e harness can't load
  deterministically. Every other surface still gets full contrast
  scanning, and every other axe rule remains active on the canvas.
- **Deterministic by default**: each spec renders the static markup in
  `tests/a11y/fixtures/markup.ts` via `page.setContent`. The fixtures
  mirror the real surfaces byte-for-byte so the gate gives a consistent
  verdict every CI run — there's no "skip because the stack is down"
  mode for an a11y gate. Set `E2E_A11Y_USE_LIVE=1` to scan the live URL
  instead (useful once a real deploy is wired up).

### Configuration

The base URL is environment-driven:

| Variable        | Default                  | Description                                   |
| --------------- | ------------------------ | --------------------------------------------- |
| `E2E_BASE_URL`  | `http://localhost:3000`  | Public-web URL the tests hit.                 |
| `CI`            | unset                    | When set, retries are enabled and reporters switch to `github` + `html`. |

Example: point at a deployed preview:

```bash
E2E_BASE_URL=https://preview.example.com pnpm test
```

### Stack not running?

The `serverRequest` fixture probes the base URL before each test and
**skips** with an actionable message if it cannot connect — you should
not see opaque `ECONNREFUSED` failures. Bring the stack up with `make up`
and re-run.

## CI usage notes

On CI:

- Retries are set to 2 (vs. 0 locally) to absorb transient flakes.
- Reporters: `github` annotations + `html` artifact + `list` (stdout).
- Workers cap at 2 to keep machine pressure predictable; raise once
  per-worker tenant isolation lands (issue #241 acceptance criteria,
  tracked as a follow-up).
- Traces, screenshots, and videos are retained **only on failure** to
  keep artifact size sane.

Suggested workflow steps (illustrative — actual GitHub Actions workflow
lives outside this directory and is added in a follow-up):

```yaml
- run: pnpm install --frozen-lockfile
- run: pnpm --filter @gonext/e2e exec playwright install --with-deps
- run: docker compose up -d --wait
- run: pnpm --filter @gonext/e2e test
- uses: actions/upload-artifact@v4
  if: failure()
  with:
    name: playwright-report
    path: tools/e2e/playwright-report
    retention-days: 14
```

## Follow-ups

This scaffold deliberately does not yet cover every issue-#241
acceptance-criteria item. Tracked as separate work:

- `docker-compose.test.yml` (test-specific stack overlay)
- `support/seed`, `support/signin`, `?fakeTime=` helpers
- Per-worker `X-Tenant` header isolation
- `make test-e2e` / `make test-e2e-headed` targets
- Adding `tools/e2e` to `pnpm-workspace.yaml` (see PR body for why this
  was kept out of the file-ownership scope)

Resolved by later issues:

- `@axe-core/playwright` injection on every spec — done in #250.
