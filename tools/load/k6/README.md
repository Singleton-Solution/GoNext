# k6 load tests

Scripted load-testing surface for the GoNext hot paths. Run before
every release to verify the SLOs agreed in
[issue #248](https://github.com/Singleton-Solution/GoNext/issues/248).

## SLOs

| Surface                         | p95     | p99     | Bucket        |
| ------------------------------- | ------- | ------- | ------------- |
| Cached public homepage          | < 250ms | < 500ms | `cachedAnon`  |
| Anonymous REST read (WP shim)   | < 400ms | < 800ms | `anonRead`    |
| Logged-in admin list           | < 800ms | < 1500ms| `authedAdmin` |
| Sustained throughput (per run)  | 200 RPS on a 2-vCPU runner            |

Thresholds live in [`lib/baseline.js`](./lib/baseline.js). Each
scenario imports the bucket that matches its endpoint class so the
numbers are defined once.

## Layout

```
tools/load/k6/
  README.md          this file
  Makefile           one-line wrappers around k6
  lib/baseline.js    shared SLO thresholds + ramp profile
  scenarios/
    homepage.js      Next.js public homepage (cached, anon)
    posts-list.js    GET /wp-json/wp/v2/posts (anon)
    login.js         POST /api/v1/auth/login (valid + invalid)
    rest-shim.js     mix of WP-compat REST shim queries
```

## Prerequisites

- k6 v0.50+ (<https://k6.io/docs/get-started/installation/>)
- A running GoNext stack — Compose locally, or a staging URL.

## Running locally

Bring up the data services and api/web from the repo root, then point
k6 at them:

```bash
make up               # from repo root — Postgres, Redis, MinIO
# (separately start apps/api and apps/web for now — see their READMEs)

cd tools/load/k6
make homepage         # ~3 min run
make all              # every scenario, sequentially
```

Override origins via env vars:

```bash
K6_BASE_URL=https://api.staging.example.com \
K6_WEB_BASE_URL=https://staging.example.com \
make all
```

For the login scenario, point at a valid dev account:

```bash
K6_VALID_EMAIL=admin@example.com \
K6_VALID_PASSWORD='your-dev-password' \
make login
```

## CI smoke

`make ci-smoke` runs a tiny version of the homepage scenario (5 VUs,
30s) suitable for a per-PR signal. CI integration is intentionally
deferred — see the TODO in `.github/workflows/load-smoke.yml` — because
it requires a running stack on the runner.

## Reading the output

k6 prints a per-iteration summary plus the final threshold check. The
run exits non-zero if any threshold is breached, so `make all` is
safe to wire into a release gate. Per-endpoint timings show up under
the `endpoint` tag in the summary and in any output sink (JSON, Prom,
Datadog) you point at via `--out`.

## Expected baselines (single 2-vCPU runner, warm cache)

These are the targets, not guarantees:

- `homepage`: p95 ~120ms, throughput ~250 RPS at 50 VUs
- `posts-list`: p95 ~180ms, throughput ~200 RPS at 50 VUs
- `login`: p95 ~600ms (dominated by Argon2id), throughput ~80 RPS
- `rest-shim` (mix): p95 ~250ms, throughput ~200 RPS

If a run is off by more than 25% on p95, treat it as a regression and
investigate before tagging.
