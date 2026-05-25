# 20 · Troubleshooting

A symptom → cause → fix catalogue for the issues that bit us actually
running the stack. Skim the symptoms; jump to the one that matches.
For per-subsystem deep dives (auth, plugins, themes, jobs) go to the
relevant numbered doc — this file is the shallow-but-broad
operator-facing layer.

If your problem isn't here, please open an issue. Real first-run
friction is the kind of bug we most want to learn about.

---

## 1. Stack won't start

### 1.1 `Bind for 0.0.0.0:<port> failed: port is already allocated`

**Symptom.** `make up` exits with a port-conflict error. The port is
one of 5432 (postgres), 6379 (redis), 8080 (api), 3000 (web), 3001
(admin), 9000 / 9001 (MinIO).

**Cause.** Another process on your host (often a native Postgres, a
side-project's Compose stack, or a stray container) already publishes
the same port.

**Fix.** Drop a `docker-compose.override.yml` in the repo root that
remaps the conflicting port:

```yaml
services:
  postgres:
    ports:
      - "5433:5432"
```

Docker Compose merges the override automatically; the file is already
gitignored. Then point host-side tooling at the new port
(`localhost:5433` in this example). See
[18-local-development.md](./18-local-development.md#the-docker-composeoverrideyml-pattern)
for the long-form explanation.

### 1.2 `service "migrate" didn't complete successfully: exit 1`

**Symptom.** The Compose stack starts; the `migrate` one-shot fails
before api / worker boot. `make ps` shows the data services healthy
but the apps stuck in `Created`.

**Cause.** The `schema_migrations` table is in a `dirty` state. Usually
this is the residue of a previous run that was interrupted mid-DDL —
the migration partially applied, the runner crashed, the row was left
flagged dirty.

**Fix.**

```bash
make down
docker compose -f docker-compose.yml -f docker-compose.dev.yml down -v
make up
```

`down -v` wipes the Postgres / Redis / MinIO data volumes. Fine on
your laptop; never on a database that holds anything you care about.

### 1.3 `api` boots but `/readyz` returns 503

**Symptom.** `make ps` shows `api` running; `curl localhost:8080/readyz`
returns 503. `/healthz` returns 200.

**Cause.** The API process started, but its DB or Redis probe is
failing. `/readyz` is the conjunction of "DB reachable" AND "Redis
reachable"; `/healthz` is just "process alive".

**Fix.** Check `make ps` for unhealthy data services. Check `make logs`
for `connect: connection refused` or `dial tcp: lookup postgres on …
no such host` on the API container — both point at network or
healthcheck-ordering issues. Bring the stack down and back up; if it
persists, post the api logs.

---

## 2. First-run bootstrap

### 2.1 `gonext init` succeeds but you can't log in afterwards

**Symptom.** `gonext init --admin-email …` reports success. You open
`/login`, enter the email + password you just set, and get "invalid
email or password".

**Cause.** Pepper mismatch. `GONEXT_AUTH_PEPPER` is HMAC'd into every
password hash. If the pepper used during `init` differs from the
pepper the api server reads at request time, the hash never matches
and login fails. The most common path here is running `init` outside
Compose (which reads a different env) and `make up` inside Compose
(which has dev defaults baked into `docker-compose.dev.yml`).

**Fix.** Make sure both processes read the same pepper. Either:

- Run `init` via Compose so it inherits the same `x-go-env` block:
  `docker compose run --rm migrate gonext init …`.
- Or set `GONEXT_AUTH_PEPPER` in your shell to the value in
  `docker-compose.dev.yml` before running `init` against the dev DB.

If the wrong pepper is already committed to a row, the only recovery
is to wipe and redo: `make down && docker compose -f
docker-compose.yml -f docker-compose.dev.yml down -v && make up &&
docker compose run --rm migrate gonext init …`.

### 2.2 The `/setup` wizard 423s before you've finished

**Symptom.** You open `/setup`, fill in the form, hit submit — and the
api returns `423 already_installed`.

**Cause.** Installation is single-shot. The `core.site.installation_completed_at`
row in `options` records the install timestamp; once present, every
`POST /api/v1/setup/*` returns 423 and the admin middleware stops
redirecting to `/setup`. You're seeing this because a previous run
already installed.

**Fix.**

- *Intentional re-install* (you really want to start over): drop the
  row out of band. From `make psql`:

  ```sql
  DELETE FROM options WHERE name = 'core.site.installation_completed_at';
  ```

  Then reload `/setup`. The wizard will re-open.

- *Just trying to log in*: the install already succeeded — go to
  `/login`. The wizard finished its job.

### 2.3 `gonext init` errors with "admin already exists"

**Symptom.** `init` exits 1 with a message about the admin user
already being present.

**Cause.** `init` is intentionally idempotent on the *schema* layer
(re-running just no-ops the migrations), but refuses to silently
clobber an existing user. If a previous run created the same email
address, it bails out so you don't accidentally overwrite a real
user's credentials.

**Fix.** Either pick a different email (`--admin-email`) or wipe the DB
volume and start fresh.

---

## 3. Admin app

### 3.1 Admin shows "network error" / `502` on every page

**Symptom.** The admin loads, but every panel hits a network error.
Network tab shows requests to `/api/v1/...` failing.

**Cause.** Two flavours:

1. The admin was built with `NEXT_PUBLIC_API_URL` pointing somewhere
   unreachable. The rewrite destination is baked at build time
   (see §3.2), so a wrong build-arg produces a broken image.
2. The api service isn't healthy.

**Fix.** Check `make ps` first — if the api isn't healthy, fix that.
Otherwise rebuild the admin with the right build-arg:

```bash
docker compose -f docker-compose.yml -f docker-compose.dev.yml \
  build --build-arg NEXT_PUBLIC_API_URL=http://api:8080 admin
make up
```

If you switched between Compose and bare `pnpm dev`, blow away the
stale `.next/` cache: `rm -rf apps/admin/.next`.

### 3.2 Why `NEXT_PUBLIC_API_URL=""` is the right value in dev

**Symptom.** You set `NEXT_PUBLIC_API_URL=http://localhost:8080` to
"fix" something, and now every admin request gets a CORS preflight
failure.

**Cause.** The admin's `next.config.ts` declares a `rewrites()` block
that proxies `/api/:path*` to the API service over the Compose
network. The api-client treats an empty `NEXT_PUBLIC_API_URL` as "use
same-origin paths" so the rewrite catches them; an explicit
`http://localhost:8080` makes the browser try to reach the API
directly, which triggers CORS.

**Fix.** Set `NEXT_PUBLIC_API_URL=""` (the default in the Dockerfile
and the Compose env). For custom builds, leave it empty unless you
genuinely want the browser to talk cross-origin to the API.

### 3.3 `useSearchParams() should be wrapped in a suspense boundary`

**Symptom.** `next build` fails with the message above and a path to a
page file under `apps/admin/src/app/...`.

**Cause.** Next.js 15 requires any client component reading
`useSearchParams()` from the App Router to sit inside a `<Suspense>`
boundary, because the hook's value depends on runtime query string
data that isn't available at prerender time. Without the boundary the
prerender step fails.

**Fix.** Wrap the consumer:

```tsx
import { Suspense } from 'react';

export default function Page() {
  return (
    <Suspense fallback={<div>Loading…</div>}>
      <PageContents />
    </Suspense>
  );
}

function PageContents() {
  const params = useSearchParams();
  // …
}
```

Move the `useSearchParams()` call into the inner component. The outer
page becomes a thin wrapper that owns the boundary.

### 3.4 Admin prerender failures on routes that hit the API

**Symptom.** `next build` fails with `fetch failed` errors during the
"Collecting page data" step. The failing routes are ones that call the
API server-side.

**Cause.** Next.js statically generates routes at build time. If a
route's `generateStaticParams` or server-side data fetcher hits the
API, the build step needs the API to be reachable from the *builder*
container — but in a fresh `make up`, the api service may not be
healthy yet when the admin image is being built.

**Fix.** Two paths:

- Mark the route as fully dynamic with `export const dynamic = 'force-dynamic'`
  so it skips prerender entirely. Best for admin pages that always
  need fresh data anyway.
- Or run `make up postgres redis minio api` first, wait for the api
  to be healthy, *then* `make up` (which rebuilds admin against the
  running api).

The admin is auth-gated and short-lived, so most routes are
acceptable as `force-dynamic`. The block editor pages already are.

---

## 4. Background workers and jobs

### 4.1 Worker keeps restarting

**Symptom.** `make ps` shows the `worker` container looping through
`Restarting`. `make logs worker` shows it exiting at boot with a
config-validation error.

**Cause.** The worker shares the same env-loader contract as the api.
If a required secret (`GONEXT_AUTH_PEPPER`, `GONEXT_AUTH_SESSION_SECRET`,
`GONEXT_AUTH_CSRF_SECRET`) is missing or too short, it refuses to
boot.

**Fix.** Check `make logs worker` for the specific missing key.
Compose's `docker-compose.dev.yml` ships dev defaults inline — if
you've overridden the env via `.env` or `docker-compose.override.yml`,
make sure all three secrets are at least 32 bytes.

### 4.2 Image processing jobs sit forever in `pending`

**Symptom.** You upload an image; the upload succeeds; the thumbnail
never appears. Job inspector (admin → Jobs) shows the task `pending`.

**Cause.** The worker isn't running, or it can't reach MinIO. Image
processing pulls the original from S3, runs libvips, and writes back
— if any leg fails, the job retries with backoff.

**Fix.** Check `make ps` for the worker container; check `make logs
worker` for the S3 / libvips error. If MinIO is healthy but the
worker can't reach it, the most likely culprit is an
`AWS_ENDPOINT_URL` mismatch — should be `http://minio:9000` inside
the Compose network, not `http://localhost:9000`.

---

## 5. Resetting to a clean state

When the easiest path is to wipe and start over:

```bash
make down
docker compose -f docker-compose.yml -f docker-compose.dev.yml down -v
docker system prune -f                  # optional; reclaims build cache
make up
docker compose run --rm migrate gonext init \
  --admin-email you@example.com \
  --admin-password 'replace-me' \
  --site-name 'My Site' \
  --site-url http://localhost:3000
```

The `down -v` is the destructive bit — Postgres, Redis, and MinIO
volumes are gone. The build cache survives unless you also prune.

---

## 6. Getting more diagnostics

| Need | How |
| --- | --- |
| All service logs (live) | `make logs` |
| One service's logs | `docker compose -f docker-compose.yml -f docker-compose.dev.yml logs -f api` |
| API config (secrets masked) | `docker compose run --rm migrate gonext config dump` |
| psql shell | `make psql` |
| Redis shell | `make redis-cli` |
| Full smoke probe | `make smoke` (brings up, probes every healthz, tears down) |
| Crank up API logs | `GONEXT_LOG_LEVEL=DEBUG` and `GONEXT_LOG_ADDSRC=true` in `docker-compose.override.yml` |

If you've ruled the symptom out as a known issue and the stack is
still misbehaving, open a GitHub issue with the output of `make ps`,
the failing logs, and the contents of any `docker-compose.override.yml`
you have in the tree.
