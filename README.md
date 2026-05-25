# GoNext

> A self-hosted WordPress alternative built on Go + Next.js.

GoNext gives you WordPress's "own your content, install plugins, switch themes" model on a modern stack — a single Go binary serves the API and a WebAssembly plugin runtime; two Next.js apps render the public site and the admin dashboard; Postgres + Redis + S3-compatible storage sit underneath. The plugin sandbox is capability-scoped and memory-isolated, so you can install something from the marketplace without praying it doesn't read `/etc/passwd`.

> **Status**: pre-1.0. The stack boots end-to-end, the first-run install wizard works, posts render through the themed public site, and the marketplace + setup flows are wired. We tag releases when things land; pin to a tag if you're shipping.

![Sign-in screen of the GoNext admin dashboard. Cream paper background with soft off-canvas emerald and lavender radial glows. A centered card holds the `GoNext` wordmark (see apps/admin/public/logo-wordmark.svg), an Archivo display headline reading "Sign in" with an italic accent on "in", a Geist body-copy subtitle, and email + password inputs over a primary "Sign in" button.](docs/design/screenshots/login.png)

*Screenshot pending — the brand foundation landed in #432 and a follow-up will capture the asset. Until then, the wordmark lives at `apps/admin/public/logo-wordmark.svg`.*

---

## The first 10 minutes

You'll need Docker Desktop ≥ 24 (with Compose v2 — `docker compose`, not `docker-compose`) and GNU Make. Nothing else; no Go or Node on your host.

```bash
# 1. Clone.
git clone https://github.com/Singleton-Solution/GoNext.git
cd GoNext

# 2. Copy the env template. Edit secrets before you ship to anything
#    that isn't your laptop — the file ships with dev-only defaults
#    flagged "replace-in-prod".
cp .env.example .env

# 3. Bring up the full stack: Postgres, Redis, MinIO, migrate (one-shot),
#    api, worker, admin, web. `make up` composes the base
#    docker-compose.yml with docker-compose.dev.yml — see the local-dev
#    doc for the full long-form command.
make up

# 4. Bootstrap the first admin user. Two options — pick one.

#    Option A (CLI): scripted, friendly to CI and air-gapped boxes.
docker compose run --rm migrate \
  gonext init \
    --admin-email you@example.com \
    --admin-password 'replace-with-a-real-≥12-char-password' \
    --site-name 'My Site' \
    --site-url http://localhost:3000

#    Option B (browser): the WordPress-style wizard. Open
#    http://localhost:3001/setup and walk through welcome → admin
#    credentials → site name + URL → confirm. The wizard locks itself
#    on success — every /api/v1/setup/* endpoint returns 423 Locked
#    afterwards, and the admin middleware stops redirecting to /setup.

# 5. Sign in.
open http://localhost:3001/login
```

That's it. The public site is at `http://localhost:3000`, the API at `http://localhost:8080`, the MinIO console at `http://localhost:9001` (creds: `gonext` / `gonext_dev_only_change_me`).

To stop everything (volumes preserved): `make down`. To wipe state and start over: `docker compose -f docker-compose.yml -f docker-compose.dev.yml down -v && make up`.

---

## Local development tips

A few things that bit us when we ran this for the first time — fix them up front and the stack just works.

### Port conflicts: the `docker-compose.override.yml` pattern

The dev stack publishes Postgres on `5432`, Redis on `6379`, the API on `8080`, the admin on `3001`, the public site on `3000`, MinIO on `9000`/`9001`. If you already run a native Postgres for another project, `make up` fails with `Bind for 0.0.0.0:5432 failed: port is already allocated`.

Fix it once, in a personal override file that Compose merges automatically and that git already ignores:

```yaml
# docker-compose.override.yml   (gitignored)
services:
  postgres:
    ports:
      - "5433:5432"   # talk to the dev DB on 5433 from the host
```

Then connect your IDE / `psql` / `make psql` to `localhost:5433` instead of `5432`. The same trick works for any other published port. Compose reads `docker-compose.override.yml` without you having to name it on the command line, which is exactly what you want — your override stays personal and local.

### The admin proxies `/api/*` through Next.js to avoid CORS

The admin (`:3001`) and the API (`:8080`) are different origins during dev. Rather than ship a CORS allowlist that has to be re-derived for every deploy shape, the admin's `next.config.ts` rewrites `/api/:path*` to the API service. Two consequences:

- **`NEXT_PUBLIC_API_URL=""` is the signal**, not a missing value. The api-client treats an empty string as "use same-origin paths" and lets the rewrite do the work. Setting it to `http://localhost:8080` in dev means the browser hits the API directly, CORS fires, and you spend a confusing afternoon staring at preflight failures. Leave it empty unless you're deliberately testing the cross-origin path.
- **The rewrite destination is baked at build time.** Next.js evaluates `rewrites()` during `next build`, so the container image has a fixed `http://api:8080` (the Compose service name) compiled into the bundle. Pass the destination as the `GONEXT_API_URL` Docker build-arg if you're building a custom image; the default Dockerfile reads it via `NEXT_PUBLIC_API_URL`.

```dockerfile
# Admin image build (from docker-compose.dev.yml)
args:
  NEXT_PUBLIC_API_URL: http://api:8080   # destination for the rewrite
```

### Secret alignment

`GONEXT_AUTH_PEPPER` is HMAC'd into every password hash on user creation. If you re-bootstrap with a different pepper and the same database, every existing user's password becomes uncrackable — `init` will succeed, but you can't log back in as the previous admin. Either set the pepper once and keep it stable, or wipe the DB volume (`down -v`) when you rotate it.

### Other gotchas

For a fuller list — Next.js prerender failures on `useSearchParams` missing `Suspense`, the migrate one-shot exiting `dirty` after a half-applied schema, MinIO bucket creation timing — see [`docs/20-troubleshooting.md`](docs/20-troubleshooting.md).

---

## What's where

| Path | What lives there |
| --- | --- |
| `apps/api` | Go HTTP server. `/healthz`, `/readyz`, `/openapi.json`, `/docs/`, every `/api/v1/*` route. |
| `apps/worker` | Asynq background-job consumer. Image processing, webhooks, cron leaders. |
| `apps/admin` | Next.js admin dashboard. Login, setup wizard, posts/pages CRUD, marketplace, customizer. |
| `apps/web` | Next.js public site. SSR/SSG/ISR, themes, sitemap, feeds. |
| `apps/docs` | Static documentation site (deploys separately from the app). |
| `cli/gonext` | The `gonext` administrative CLI. `init`, `migrate`, `theme`, `plugin`, `bench`, `config`. |
| `packages/go` | Shared Go packages — auth, config, log, db, cache, hooks, middleware, testutil, etc. |
| `packages/ts` | Shared TypeScript packages — UI primitives, block schemas, the plugin/theme SDKs. |
| `migrations` | `golang-migrate` SQL files. Applied by `gonext migrate up` or the Compose `migrate` one-shot. |
| `themes` | First-party theme bundles (the default theme is seeded by `migrate`). |
| `plugins` | First-party reference plugins (WASM bundles + manifests). |
| `docs` | The whole design corpus — architecture docs 00–19, ADRs, proposals, troubleshooting. |
| `tools` | One-off operator tooling: the compose smoke harness, the e2e Playwright suite. |

---

## Documentation map

If you want the next layer down, read these in order.

| Document | What it covers |
| --- | --- |
| [`docs/00-architecture-overview.md`](docs/00-architecture-overview.md) | The shared foundation. Stack, the three hard problems, the topology, the phasing plan. **Read first.** |
| [`docs/17-environment.md`](docs/17-environment.md) | Every env var the loader reads — type, default, redaction rules, K8s + systemd shapes. |
| [`docs/18-local-development.md`](docs/18-local-development.md) | The dev-stack reference: Make targets, the override pattern, the smoke harness, troubleshooting. |
| [`docs/20-troubleshooting.md`](docs/20-troubleshooting.md) | Symptom → cause → fix for every gotcha we hit running the stack for the first time. |

The full catalogue (auth, plugin system, theme system, block editor, observability, jobs, security baseline, etc.) lives in [`docs/README.md`](docs/README.md). Architecture Decision Records are under [`adr/`](adr/). Open design proposals are under [`docs/proposals/`](docs/proposals/).

---

## License

GoNext core is licensed under **FSL-1.1-Apache-2.0** — the Functional Source License 1.1 with automatic conversion to Apache License 2.0 two years after each file's release. Source-available today, fully open-source on a two-year delay. Read it: [`LICENSE`](LICENSE).

The plugin and theme SDKs (everything under `packages/ts/sdk` and `packages/go/sdk`) ship under **Apache-2.0** from day one, so authors building on top of GoNext have a permissive license to work against without any FSL strings attached.

The rationale, including why we picked FSL over BSL or MIT, lives in [`adr/0001-licensing.md`](adr/0001-licensing.md).

---

## Contributing

We need help. Go developers, React developers, designers, technical writers, security reviewers — there's an issue with your name on it.

1. **Find an issue.** Filter the [issue tracker](https://github.com/Singleton-Solution/GoNext/issues) by `good-first-issue`, `help-wanted`, `area:*` (api, web, admin, plugins, themes, security, docs, …), or `skill:*` (go, react, ts, sql, devops, design, docs). Comment on the issue to claim it.
2. **Branch.** `git checkout -b feat/<short-description>` or `fix/<short-description>` off `main`.
3. **Sign off every commit.** GoNext uses the [Developer Certificate of Origin](https://developercertificate.org/) instead of a CLA. Pass `-s` to `git commit` — the CI check rejects unsigned commits. See [`adr/0002-dco-requirement.md`](adr/0002-dco-requirement.md) for the rationale and [`CONTRIBUTING.md`](CONTRIBUTING.md) for the full workflow.
4. **Open the PR against `main`.** Reference the issue you're closing. Keep PRs small and focused — one logical change per PR.

[`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md) applies to every interaction in the repo.

---

## Security

Report vulnerabilities privately per [`SECURITY.md`](SECURITY.md). Do not file public issues for security reports.

---

## Maintainer

Currently maintained by [@tayebmokni](https://github.com/tayebmokni) under [Singleton-Solution](https://github.com/Singleton-Solution). Governance transitions to a maintainer team as the contributor base grows.
