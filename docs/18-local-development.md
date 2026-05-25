# 18 · Local development

The shortest path from a fresh clone to a running GoNext stack on your
laptop. Three commands, ~3 minutes the first time, ~30 seconds on
re-runs (BuildKit caches).

## Prerequisites

| Tool                | Version  | Why                                          |
|---------------------|----------|----------------------------------------------|
| Docker Desktop      | ≥ 24     | The dev stack runs in Compose.               |
| Docker Compose v2   | bundled  | `docker compose` (the plugin), not `docker-compose`. |
| GNU Make            | any      | Wraps the long `docker compose` invocations. |
| `curl`              | any      | For the smoke script's HTTP probes.          |

That is the entire dependency surface for the **stack**. You only need
Go (1.25+) and Node/pnpm if you plan to run apps **outside** Compose
(`make build`, `make test`).

## Quick start

```bash
git clone https://github.com/Singleton-Solution/GoNext.git
cd GoNext
cp .env.example .env          # optional — Compose ships dev defaults
make up                       # build + start the full stack
make smoke                    # verify every service is healthy
```

`make up` composes `docker-compose.yml` (data services) with
`docker-compose.dev.yml` (application services) and runs them as a
single stack named `gonext-dev`. The first run pulls base images and
builds the five GoNext images:

| Image              | Built from                  | Stage           |
|--------------------|------------------------------|------------------|
| `gonext-api:dev`   | `apps/api/Dockerfile`        | api HTTP server  |
| `gonext-worker:dev`| `apps/worker/Dockerfile`     | Asynq consumer   |
| `gonext-cli:dev`   | `cli/gonext/Dockerfile`      | one-shot migrate + seeder |
| `gonext-admin:dev` | `apps/admin/Dockerfile`      | admin dashboard  |
| `gonext-web:dev`   | `apps/web/Dockerfile`        | public site      |

The stack starts in dependency order: Postgres / Redis / MinIO come up
first; once they pass their healthchecks the `migrate` one-shot runs
(`gonext migrate up`, which applies the schema and seeds the default
theme); only then do api / worker / admin / web boot.

## Access URLs

| Surface           | URL                              | Notes                          |
|-------------------|----------------------------------|--------------------------------|
| API               | http://localhost:8080            | JSON: `name`, `version`, `commit` at `/`.       |
| API liveness      | http://localhost:8080/healthz    | Always 200 once the binary is up.               |
| API readiness     | http://localhost:8080/readyz     | 200 iff DB + Redis are reachable.               |
| API OpenAPI       | http://localhost:8080/openapi.json | The contract every SDK targets.                |
| API Swagger UI    | http://localhost:8080/docs/      | Dev-only; not present in production builds.     |
| Admin dashboard   | http://localhost:3001            | Next.js (placeholder pre-launch).               |
| Public site       | http://localhost:3000            | Next.js (placeholder pre-launch).               |
| MinIO console     | http://localhost:9001            | Dev creds: `gonext` / `gonext_dev_only_change_me`. |
| Postgres          | `localhost:5432`                 | DB `gonext_dev`, user `gonext`, pwd `gonext_dev_only`. |
| Redis             | `localhost:6379`                 | DB 0 by default.                                 |

## Day-to-day

| Command         | What it does                                                        |
|-----------------|---------------------------------------------------------------------|
| `make up`       | Start the full stack (data + apps). Idempotent.                     |
| `make up-data`  | Start only Postgres / Redis / MinIO (run api locally via `go run`). |
| `make ps`       | List container state.                                                |
| `make logs`     | Tail logs from every service.                                        |
| `make smoke`    | Bring up, probe every `/healthz`, tear down.                         |
| `make restart`  | `make down && make up`.                                              |
| `make down`     | Stop the stack (volumes preserved).                                  |
| `make psql`     | Open a psql shell against `gonext_dev`.                              |
| `make redis-cli`| Open a `redis-cli` shell against the dev Redis.                      |

## Configuration

`.env.example` documents every supported environment variable. Compose
sets dev defaults inline for all the secrets the api refuses to boot
without (`GONEXT_AUTH_PEPPER`, `GONEXT_AUTH_SESSION_SECRET`,
`GONEXT_AUTH_CSRF_SECRET`), so a freshly cloned repo runs end-to-end
without any operator setup. Override any of them by editing the
`x-go-env` block at the top of `docker-compose.dev.yml`, or by setting
`environment:` overrides in a personal `docker-compose.override.yml`
(Docker Compose merges that file automatically and never tracks it in
git).

The dev secrets are intentionally low-entropy and labelled
`replace-in-prod` — production deploys MUST source secrets from a
secrets manager. See [13-security-baseline.md §5](./13-security-baseline.md).

## The `docker-compose.override.yml` pattern

Docker Compose has a convention that's perfect for personal local
tweaks: any file named `docker-compose.override.yml` next to the base
compose file is merged in automatically, with no extra `-f` flag, and
the path is already in `.gitignore`. Use it for anything you want to
change that isn't worth a tracked-file commit.

The single most common need is **remapping a published port** when
something on your host is already on the same number. The dev stack
publishes:

| Service  | Container port | Host port   |
|----------|----------------|-------------|
| postgres | 5432           | 5432        |
| redis    | 6379           | 6379        |
| minio    | 9000 / 9001    | 9000 / 9001 |
| api      | 8080           | 8080        |
| admin    | 3000           | 3001        |
| web      | 3000           | 3000        |

If you already run a native Postgres on 5432, drop this into
`docker-compose.override.yml`:

```yaml
services:
  postgres:
    ports:
      - "5433:5432"   # publish on 5433 from the host; container stays on 5432
```

Now `make psql` (which talks via `docker compose exec`) still works
unchanged, but your IDE, `psql`, `pgcli`, and any host-local tooling
should point at `localhost:5433`. The same pattern fixes conflicts on
any other published port — only the `services:` map matters; the rest
of the base compose file is unchanged.

Other things people commonly drop into the override file:

```yaml
services:
  api:
    environment:
      # Crank up the logs while debugging a specific issue.
      GONEXT_LOG_LEVEL: DEBUG
      GONEXT_LOG_ADDSRC: "true"
  admin:
    volumes:
      # Mount your local app source for hot-reload outside Compose.
      - ./apps/admin/src:/app/apps/admin/src
```

## How the admin reaches the API: Next.js rewrites, not CORS

The admin (Next.js, served on `:3001` from the host) and the API
(`:8080`) are different origins during dev. Two ways to bridge that:

1. **CORS**: ship an `Access-Control-Allow-Origin` allowlist on the API
   that matches every shape a deployment might take.
2. **Same-origin proxy**: rewrite `/api/:path*` on the admin server to
   the API service, so the browser only ever talks to `:3001`.

We picked option 2. The admin's `next.config.ts` defines a `rewrites()`
block that forwards `/api/*` to the API. The browser sees one origin;
no preflight; no allowlist to maintain.

Two consequences flow from that choice:

- **`NEXT_PUBLIC_API_URL=""` is the signal**, not a missing value.
  `apps/admin/src/lib/api-client.ts` treats an empty string as "use
  same-origin paths" — `/api/v1/posts` etc. — and lets the rewrite do
  the work. Setting `NEXT_PUBLIC_API_URL=http://localhost:8080` makes
  the browser hit the API directly, which means CORS preflights, which
  means an afternoon staring at network-tab errors. Leave it empty
  unless you're deliberately testing the cross-origin path.

- **The rewrite destination is baked in at build time.** Next.js
  evaluates `rewrites()` during `next build`, so the container image
  has a fixed destination compiled into the bundle. The Dockerfile
  declares `ARG NEXT_PUBLIC_API_URL=""` and the Compose build block
  passes `http://api:8080` — the cluster-internal service name — as
  the build-arg. If you build a custom admin image you need to pass
  the same arg pointed at whatever your API is reachable as inside the
  cluster (e.g. `--build-arg NEXT_PUBLIC_API_URL=http://api:8080` for
  Compose, or your K8s Service DNS name in a cluster build).

  In some deployments we pass this through as `GONEXT_API_URL` so the
  variable name reflects "the GoNext API target", not "the public env
  var Next bakes into the bundle"; the Dockerfile's `ARG` block is the
  single point that ties the two names together.

The rewrite happens at the Next.js server (port `:3000` inside the
admin container, `:3001` from the host) — *not* in the browser. So
the API service only needs to accept connections from the admin
container on the Compose network, not from `localhost:3001`.

## The smoke harness

`make smoke` invokes `tools/compose-smoke/compose-smoke.sh`. The script:

1. Brings the stack up via `docker compose up -d --wait`.
2. Polls every service's readiness signal up to 60s:
   - Postgres / Redis / MinIO — compose-declared healthcheck.
   - api — HTTP 200 on `/healthz` (liveness) and `/readyz` (DB+Redis).
   - worker — container in `running` state (no HTTP listener yet).
   - admin / web — HTTP 200 on `/`.
3. Exercises one real JSON request flow — `GET /openapi.json` returns
   200 + a JSON body whose first byte is `{`.
4. Tears down (`compose down -v --remove-orphans`) on success OR
   failure. On failure, dumps the last 80 lines per service first.

The same script runs in CI via `.github/workflows/compose-smoke.yml`
on any PR that touches the compose surface. The workflow is
**advisory** today (`continue-on-error: true`) — a red light surfaces
the regression without blocking merge while the 137-PR feature backlog
shakes out. It gets promoted to a release gate once the run is green
for 14 consecutive days on main.

### Useful smoke env overrides

```bash
KEEP_UP=1 make smoke            # don't tear down on success (debugging)
API_PORT=18080 make smoke        # probe a remapped host port
HEALTH_TIMEOUT_SECS=120 make smoke  # raise the per-probe budget
```

## Troubleshooting

For a wider symptom → cause → fix catalogue, see
[20-troubleshooting.md](./20-troubleshooting.md). The list below covers
just the issues that surface during day-one local-dev setup.

**"Bind for 0.0.0.0:8080 failed: port is already allocated"** —
something else on your host is already on 8080, 3000, 3001, 5432,
6379, 9000, or 9001. Either stop the conflicting container
(`docker ps`, then `docker stop <name>`) or remap the published port in
a personal `docker-compose.override.yml` (see the section above for
the canonical Postgres-on-5433 example).

**"service \"migrate\" didn't complete successfully: exit 1"** —
look at `make logs` for the `migrate-1` container. The most common
cause is a stale data volume from a previous failed run that left the
schema_migrations table in a `dirty` state. Reset the dev volumes:

```bash
docker compose -f docker-compose.yml -f docker-compose.dev.yml down -v
make up
```

`down -v` blows away the Postgres / Redis / MinIO data volumes — fine
for a dev box but obviously **never** run this against a database you
care about.

**api `/readyz` returns 503** — the api couldn't reach Postgres or
Redis. `make ps` will show whether those containers are healthy;
`make logs` shows the api binary's connection error.

**Admin login: "invalid email or password" with the right
credentials** — almost always a pepper mismatch. `GONEXT_AUTH_PEPPER`
is HMAC'd into every password hash; rotating it without rehashing
existing rows makes every existing password uncrackable. Either keep
the pepper stable across re-bootstraps, or wipe state with
`make down && docker volume rm gonext-dev_postgres-data && make up`
and rerun `gonext init`.

**Admin build fails with `useSearchParams() should be wrapped in a
suspense boundary`** — Next.js 15 requires any client component that
reads `useSearchParams()` from the App Router to sit inside a
`<Suspense>` boundary, because the hook reads dynamic data that
isn't available at prerender time. If you add a new page using the
hook, wrap the consumer in `<Suspense fallback={…}>`. The compile
error points at the file path; the fix is local.

**`make up` succeeds but admin returns 502 / network error** — the
admin built with `NEXT_PUBLIC_API_URL` pointing at something
unreachable. Check the Compose build args (should be
`http://api:8080`) and confirm the api service is actually healthy via
`make ps`. If you've been switching between Compose and bare
`pnpm dev` runs, the cached `.next/` build may have the wrong baked
destination — `rm -rf apps/admin/.next` and rebuild.

**Slow rebuilds** — the multi-stage Dockerfiles use BuildKit cache
mounts for the Go module cache and the pnpm store. Touch a Go file
and rebuild — the second run should be sub-30s. If it isn't, your
Docker BuildKit cache may have been cleared; this is normal after
`docker system prune`.

## Architecture pointers

If you want to dig deeper into how the stack hangs together:

- Lifecycle & shutdown — [09-deployment-ops.md §3](./09-deployment-ops.md)
- Theme seeder — `packages/go/theme/seed/doc.go`
- Asynq queue + worker — [12-jobs-cron.md](./12-jobs-cron.md), `apps/worker/cmd/worker/main.go`
- Healthz design — `apps/api/internal/healthz/doc.go`
- Migration runner — `packages/go/migrate/doc.go`
