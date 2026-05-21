# 19. End-to-end testing (fresh-install smoke)

The Playwright harness in `tools/e2e/` carries the end-to-end suite. This
document covers the **fresh-install smoke** — the single happy-path
journey that, if broken, means a brand-new GoNext install can't ship a
post. Everything else (a11y gates, richer journeys) is layered on top of
the same harness.

The broader testing strategy lives in `docs/11-testing-ci.md` §11; this
doc zooms into just the smoke.

## 19.1 What the smoke proves

`tools/e2e/tests/install-and-publish.spec.ts` runs one ordered
journey:

1. **Init** — `globalSetup` truncates the e2e database and runs
   `gonext init --non-interactive`, seeding an admin user with a
   known password.
2. **Log in** — drive the browser through `/login`, fill the form,
   submit, assert the dashboard renders.
3. **Author** — visit `/posts/new`, set a title, insert paragraph +
   heading + list blocks.
4. **Publish** — click Publish, wait for the success notification,
   capture the slug from the "View post" link.
5. **Public render** — log out, visit `/`, assert the post appears
   in the list, navigate to the detail, assert the authored content
   is in the DOM.
6. **SEO** — assert the canonical `<link>`, `og:title`, and
   `og:description` are present on the detail page.

If any of those steps fails, a self-hosted user can't get from a
fresh `docker compose up` to a published post. That's the contract.

## 19.2 Running locally

Prerequisites: Node 22+, pnpm 9+, Docker, `psql`.

```bash
# 1. Bring up the stack
make up

# 2. Install e2e deps + browsers (first run only)
cd tools/e2e
pnpm install
pnpm exec playwright install --with-deps chromium

# 3. Run the smoke
cd -
make e2e-smoke
```

`make e2e-smoke` sets `E2E_ALLOW_DESTRUCTIVE=1` for you so
`freshDatabase()` is willing to TRUNCATE the e2e database.

### Configuration

| Variable                   | Default                                          | Purpose                                                |
| -------------------------- | ------------------------------------------------ | ------------------------------------------------------ |
| `E2E_BASE_URL`             | `http://localhost:3000`                          | Public web URL.                                        |
| `E2E_API_BASE_URL`         | `http://localhost:8080`                          | API URL (health check + admin login probe).            |
| `E2E_FRESH_INSTALL`        | unset                                            | When `1`, `globalSetup` resets the DB and runs init.   |
| `E2E_ALLOW_DESTRUCTIVE`    | unset                                            | When `1`, `freshDatabase()` is allowed to TRUNCATE.    |
| `E2E_PG_HOST`/`PORT`/...   | docker-compose defaults                          | Override if you point at a non-local Postgres.         |
| `E2E_ADMIN_PASSWORD_HASH`  | unset                                            | Pre-computed argon2id hash, used as a fallback when the `gonext` CLI is not on PATH. |

## 19.3 Debugging failures

When the spec fails, the Playwright report is the first place to
look. By default we keep traces, screenshots, and video **only on
failure** so artefact size stays manageable.

```bash
# Open the report from a local run
cd tools/e2e && pnpm exec playwright show-report
```

On CI the report and stack logs are uploaded as artefacts; download
them from the workflow run's "Artifacts" panel. The logs cover the
API, admin SPA, and public web processes that the workflow boots
inline.

Common failure modes and where to look:

| Symptom                                          | First place to look                                          |
| ------------------------------------------------ | ------------------------------------------------------------ |
| Spec skipped with "stack not reachable"          | `make up`, then re-run.                                     |
| `freshDatabase()` refuses to run                 | You forgot `E2E_ALLOW_DESTRUCTIVE=1`.                       |
| Login step times out                             | API `auth/login` returned a non-2xx — check `/tmp/api.log`. |
| Editor selectors don't match                     | Admin SPA markup drifted; spec is intentionally lenient.    |
| Public render assertion fails                    | Renderer cache may be stale; check `apps/web` logs.         |

## 19.4 Test contract

The smoke is the **only** spec that:

- Wipes database state via `freshDatabase()`. Other suites assume
  they share state and don't TRUNCATE.
- Calls `gonext init` (or the seeding fallback). Other suites run
  against an already-initialised stack.

That's why the journey is one ordered test (with `test.step()` for
trace clarity) rather than six independent tests: the steps are not
independent — step 5 only exists because step 4 succeeded.

## 19.5 Promotion plan

The CI workflow (`.github/workflows/e2e-smoke.yml`) is **advisory**
on landing — it runs and reports but `continue-on-error: true` so a
red smoke does not block merges. Promotion to a required check
happens once the journey lands three consecutive green PRs without
manual reruns; the gate flip is a one-line workflow edit and a
branch-protection update.
