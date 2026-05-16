# Deployment & Operations

> Doc 09 in the design series. Fills the P0 "Deployment & DevOps" gap identified by `09-review-gaps.md` §A1.
> Reader: senior platform engineer who has run a CMS-shaped workload before. We will skip introductory material.
>
> This document is **opinionated**. We commit to specific tools (Caddy, golang-migrate, Asynq leader-election sidecar, Cloudflare). Where the bigger architecture has not picked a tool, we pick one here and flag it as the deployment default. Subsystem docs may override; this is the ops baseline.

---

## 0. Scope, non-scope, and forward references

### In scope

- Process topology and how those processes communicate.
- Container image strategy (multi-stage Dockerfiles, tagging).
- Reference deployments for Kubernetes, Docker Compose, and bare-metal/systemd.
- Environment variables and config surface.
- Secrets injection (operational shape; cryptographic shape is doc 15).
- Migrations on boot, boot sequence, graceful shutdown.
- Health/readiness, zero-downtime deploys, blue/green, canary.
- Multi-region recommendation.
- Cron leader election (resolves doc 02 §15 open question).
- Resource sizing baselines.
- CDN integration (Cloudflare).
- Multi-tenancy operational notes (defers shape to doc 06 §17.6).
- Backup operational hooks (policy is forward-referenced — see §22).

### Forward-referenced docs (don't yet exist)

- **doc 10 — Observability**: logs, metrics, traces, dashboards. This doc names the surfaces that need to be wired (health probes, queue depth metric) but doesn't specify them.
- **doc 11 — Testing & CI**: the CI pipeline that produces the artifacts this doc deploys.
- **doc 12 — Jobs & Cron**: queue topology, retry policy, DLQ shape. This doc resolves *cron leader election* and *graceful shutdown of in-flight jobs* but defers per-queue policy.
- **doc 13 — Email**: SMTP/SES adapter; this doc lists the env vars only.
- **doc 15 — Security baseline & secrets**: secret store crypto, header set. This doc lists which secrets exist and where they're read; doc 15 owns rotation and at-rest encryption.
- **doc 16 — DR/backup**: cadence, retention, RPO/RTO. This doc covers backup *integration hooks* (where backups attach to the boot sequence and the outbox).

### Glossary for this doc

- **"core"** = the Go binary (HTTP API + WASM plugin host + worker pool).
- **"public-web"** = the Next.js app that renders the public site.
- **"admin-web"** = the Next.js admin / wp-admin equivalent.
- **"worker"** = a `core` invocation in worker mode (Asynq consumer).
- **"cron"** = a `core` invocation in cron mode (Asynq Scheduler / leader).
- **Tenant** = a logical site. v1 is single-tenant per deployment.

---

## 1. Process topology

### 1.1 Recommended process inventory

We split the runtime into **five process classes**, each independently scalable. Every class is the *same binary* (`gonext` for Go; one Next.js standalone build for the two web tiers — see §3) invoked with different flags.

| Class | Binary | Role | Replicas (typical) | Stateful? |
|---|---|---|---|---|
| `core-api` | `gonext` (mode=api) | HTTP API, GraphQL, WASM plugin host for request-path hooks | 2..N (HPA) | No |
| `core-worker` | `gonext` (mode=worker) | Asynq consumer pool; WASM host for job-path hooks | 2..N (HPA on queue depth) | No |
| `core-cron` | `gonext` (mode=cron) | Asynq Scheduler (cron jobs), leader-elected | 1 active + N standby | No (leader lock in Redis) |
| `public-web` | `node server.js` (Next.js standalone) | SSR/SSG/ISR public renderer | 2..N (HPA) | No (ISR cache is in S3 or local volume) |
| `admin-web` | `node server.js` (Next.js standalone) | Admin SPA shell (SSR for shell, CSR for app) | 2..N (HPA, much lower fan-out than public) | No |

All five classes are stateless from the operator's perspective: state lives in Postgres, Redis, S3.

### 1.2 Why split core-api / core-worker / core-cron

They are *the same code*, but the operational characteristics diverge enough that lumping them produces bad incidents:

- **`core-api`** is latency-sensitive (P95 SLO ~250ms). It should be aggressively pre-warmed and aggressively drained on shutdown.
- **`core-worker`** is throughput-sensitive. A worker pod processing a video-thumbnail job is happy at 100% CPU for 90 seconds; a `core-api` pod at 100% CPU for 90 seconds is broken.
- **`core-cron`** must be a single active replica (Asynq scheduling can only elect one writer to the scheduled-set; doc 02 §15 left this open — see §16).

Co-tenanting them inside one process means a bad job starves request handlers. We've seen this fail in production CMSes (looking at you, Drupal Cron) enough times to design it out.

### 1.3 Why split public-web from admin-web (and not just route groups)

We considered three options:

| Option | Pros | Cons |
|---|---|---|
| A. One Next.js app, route groups (`/(public)/...`, `/admin/...`) | Single build, single deploy, shared auth context | Admin chunks ship to public visitors unless aggressive code-splitting; SSR fan-out conflated; ISR config differs |
| B. Two separate Next.js apps in one repo | Separate build outputs, separate Dockerfiles, separate HPA | Two services to operate |
| C. Admin as Vite SPA, public as Next.js | Smallest admin bundle, fastest dev loop | No SSR for admin → harder SEO (not needed for admin anyway), but auth/session bootstrap awkward |

**Recommendation: B (two separate Next.js apps).** Reasons:

1. Public-web autoscaling is page-traffic-driven (1k–100k RPS). Admin-web is editor-driven (<10 RPS in real sites). Conflating them confuses HPA.
2. The public bundle should never carry the editor (TipTap/Lexical/our Gutenberg-equivalent is ~1MB gzipped). Code-splitting in a unified app is leaky.
3. Cache headers diverge: public is heavily cached at the CDN; admin should never be cached.
4. CSP can be tightened on public (no `unsafe-eval`) while admin needs `wasm-unsafe-eval` for in-browser block previews. Separate origins make this clean.
5. ISR is irrelevant to admin; this avoids subtle config drift.

We **reject** option A because the upside (slightly less plumbing) is small compared to the operational divergence. We reject C because admin needs server-side auth bootstrap and we'd just be reinventing Next.js for the admin half.

### 1.4 Communication paths

```
                  CDN (Cloudflare)
                        │ HTTPS
                        ▼
                  Ingress / L7 LB
                        │
              ┌─────────┴─────────┐
        host=example.com     host=admin.example.com
              ▼                   ▼
         public-web           admin-web
              │  HTTP/2 (in-cluster)│
              └─────────┬───────────┘
                        ▼
                     core-api  (/api/v1, /graphql, /healthz, /readyz, /metrics)
                        │
   ┌───────┬────────────┼──────────┬───────────┐
   ▼       ▼            ▼          ▼           ▼
 Postgres Redis (Asynq broker)    S3        SMTP/SES   CDN purge
                │
                ▼
            core-worker (N replicas, WASM host for job hooks)
                ▲
                │ schedules
            core-cron (1 active leader-elected via Redis lock, N standby)
```

Notes:

- public-web and admin-web call core-api over **plain HTTP/2 in-cluster**; mTLS is doc 15.
- Workers do not call core-api; they share libraries and hit Postgres/Redis/S3 directly.
- ISR revalidate: core-api → public-web via signed webhook (doc 07 §15.2). Core needs the public-web's in-cluster URL.
- CDN purge: core-api / worker calls Cloudflare purge-by-tag while draining the `cache_invalidations` outbox (§18).

### 1.5 Single-region happy path (v1 default)

```
one VPC / one region
ingress (1 LB)
 ├─→ public-web (3 pods, HPA 3..30)
 ├─→ admin-web  (2 pods, fixed)
 └─→ core-api   (3 pods, HPA 3..20)
            ├─→ Postgres primary (+ read replica)
            ├─→ Redis primary (+ replicas)
            ├─→ S3 (managed)
            ├─→ core-worker (3 pods, HPA 3..15)
            └─→ core-cron (1 active + 1 standby)
```

---

## 2. Container strategy

### 2.1 Image-per-process vs all-in-one — the decision

**Decision: ONE image per *codebase*, not per process. Two images total.**

1. **`gonext-core`** — the Go binary plus runtime assets. Invoked as `gonext serve [api|worker|cron|migrate]`.
2. **`gonext-web`** — both Next.js apps as a single multi-target image, selected at runtime by an env var or by the entrypoint command (`yarn workspace public-web start` vs `yarn workspace admin-web start`).

Why not one image per process (4–5 images)?

- The Go binary is identical across api/worker/cron. The flag is the only difference. Building three images is wasted CI time and three SBOMs to track.
- The two Next.js apps share 60% of `node_modules`. Building one combined image with two start commands lets us share the layer cache.

Why not one image total (single binary embedding everything)?

- See §3 — embedding Node.js in the Go binary is a non-starter for v1. The two runtimes are managed independently.

Why not the maximalist split (api, worker, cron, public, admin = 5 images)?

- The marginal pull time saved at scale is real but small (~30MB per Go image variant). It's not worth the CI matrix complexity.

### 2.2 `gonext-core` Dockerfile (canonical)

```dockerfile
# syntax=docker/dockerfile:1.7

# ---------- Stage 1: build ----------
FROM golang:1.23-alpine AS build
WORKDIR /src
RUN apk add --no-cache git make ca-certificates tzdata

# Cache deps first
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

# Source
COPY . .

# Embed: migrations + the canonical block schemas + the default theme bundle
#   Migrations live in /src/internal/migrations; the binary embeds them via embed.FS
#   (see §9). We do NOT embed Next.js artifacts here — see §3.

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
ENV CGO_ENABLED=0 GOOS=linux

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build \
      -trimpath \
      -tags "netgo,osusergo" \
      -ldflags "-s -w \
        -X 'gonext/internal/build.Version=${VERSION}' \
        -X 'gonext/internal/build.Commit=${COMMIT}' \
        -X 'gonext/internal/build.Date=${BUILD_DATE}'" \
      -o /out/gonext ./cmd/gonext

# ---------- Stage 2: runtime ----------
FROM gcr.io/distroless/static-debian12:nonroot AS runtime
COPY --from=build /out/gonext /gonext
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
USER nonroot:nonroot

# api: 8080, metrics: 9090, internal RPC (future): 9100
EXPOSE 8080 9090

ENTRYPOINT ["/gonext"]
CMD ["serve","api"]
```

Layer story: Go dependency layer, source layer, build cache mount, distroless `nonroot` user. The image is ~25–30 MB.

### 2.3 `gonext-web` Dockerfile (multi-target Next.js)

```dockerfile
# syntax=docker/dockerfile:1.7

# ---------- Stage 1: deps ----------
FROM node:20-alpine AS deps
WORKDIR /repo
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY apps/public/package.json apps/public/
COPY apps/admin/package.json  apps/admin/
COPY packages/*/package.json  packages/*/   # if any shared packages
RUN corepack enable && pnpm install --frozen-lockfile

# ---------- Stage 2: build ----------
FROM deps AS build
COPY . .
ARG NEXT_PUBLIC_API_URL
ARG NEXT_PUBLIC_VERSION=dev
ENV NEXT_TELEMETRY_DISABLED=1
RUN pnpm --filter public-web build && pnpm --filter admin-web build

# ---------- Stage 3: runtime ----------
FROM node:20-alpine AS runtime
WORKDIR /app
RUN addgroup --system --gid 1001 nodejs && adduser --system --uid 1001 nextjs
ENV NODE_ENV=production
ENV NEXT_TELEMETRY_DISABLED=1

# Use Next.js standalone output (output: 'standalone' in next.config.js for BOTH apps).
COPY --from=build /repo/apps/public/.next/standalone /app/public/
COPY --from=build /repo/apps/public/.next/static     /app/public/apps/public/.next/static
COPY --from=build /repo/apps/public/public           /app/public/apps/public/public

COPY --from=build /repo/apps/admin/.next/standalone  /app/admin/
COPY --from=build /repo/apps/admin/.next/static      /app/admin/apps/admin/.next/static
COPY --from=build /repo/apps/admin/public            /app/admin/apps/admin/public

USER nextjs
EXPOSE 3000

# Entrypoint selects which app via $DONEXT_WEB_APP
COPY docker/web-entrypoint.sh /entrypoint.sh
ENTRYPOINT ["/entrypoint.sh"]
```

`web-entrypoint.sh`:

```sh
#!/bin/sh
set -e
case "$DONEXT_WEB_APP" in
  public) exec node /app/public/apps/public/server.js ;;
  admin)  exec node /app/admin/apps/admin/server.js ;;
  *) echo "DONEXT_WEB_APP must be 'public' or 'admin'"; exit 1 ;;
esac
```

### 2.4 Tag and version strategy

| Tag form | Meaning | Mutability | Use |
|---|---|---|---|
| `gonext-core:1.4.7` | SemVer release | immutable | production pins |
| `gonext-core:1.4.7-amd64`, `...-arm64` | platform-specific manifest entry | immutable | not normally referenced directly |
| `gonext-core:1.4` | latest patch of 1.4 | mutable | dev/staging convenience |
| `gonext-core:1` | latest minor of 1 | mutable | not for prod |
| `gonext-core:main-<git-sha-short>` | every main commit | immutable | preview environments |
| `gonext-core:pr-1234` | per-PR build | mutable | ephemeral PR envs (deleted on merge) |
| `gonext-core:nightly-2026-05-13` | scheduled nightly | mutable for that day, then frozen | canary |
| `gonext-core:latest` | **not published** | — | we deliberately don't publish this |

Same scheme for `gonext-web`.

A core release and a web release **must share the same SemVer**. `gonext-core:1.4.7` and `gonext-web:1.4.7` are tested together; mixing minor versions is allowed within a deploy *only* during a controlled rollout (§13.3) and is otherwise unsupported. Internal API compatibility surface is sliced by major version.

### 2.5 SBOM and signing

- Every published image carries an SPDX SBOM attached via `cosign attach sbom`.
- Every image is signed (keyless via Sigstore for OSS; HSM-key for SaaS).
- Admission controller in K8s verifies signatures on `prod-*` namespaces.
- This forward-references doc 15 §X (supply chain) for the trust policy.

---

## 3. The "single binary self-host" claim — reconciling with Next.js

The arch overview (doc 00 §2) claims "single binary deploy" for Go. The README promises "single-binary self-host." Next.js is a Node process; these claims are in mild tension.

Options considered:

- **A1 — Pure-Go React renderer.** Rejected. No production-grade pure-Go React SSR exists. Writing one (RSC, streaming, suspense, hydration mismatches) is years of work.
- **A2 — Bundle Node into the Go binary** (`pkg`/`nexe`-style). Rejected. Two processes hidden inside one wrapper, two GCs, dishonest packaging.
- **B — Be honest.** Go is one binary; Next.js apps are separate processes. Self-host needs Node.js or the `gonext-web` container.
- **C — Pre-rendered HTML embedded via `embed.FS`.** Works for static marketing sites; breaks ISR, preview mode, and personalized RSC. Rejected as default; viable as an opt-in mode.
- **D — Headless-by-default.** The Go binary IS the CMS, exposing a JSON API. Renderer is the user's problem. We ship Next.js as the default reference renderer, but it's not "the product."

### 3.1 Our position

**B + D, with C as an opt-in mode.** The doc 00 headless-first non-goal already implies decoupling. The README/overview should be revised (out of scope here) to say: *"The Go core is a single binary. The default reference renderer is a separate Next.js process. For static-only sites, the Go binary can serve a pre-built static export."*

Self-hosters get one-command setup via Docker Compose (§5). Operators who want the literal single binary use static-export (§3.2) and accept the trade-offs. We refuse to ship a fake single binary that's two binaries in a trench coat.

### 3.2 Static-export mode (the C compromise, opt-in)

When the operator builds the site with `gonext static-export`:

1. The CLI calls the public-web's `next export` (with `output: 'export'` configured).
2. The resulting `out/` directory is gzipped and embedded into a freshly compiled `gonext` binary via `go:embed`.
3. The resulting binary, when run with `serve api --static-mode`, serves the JSON API as normal AND serves the static HTML at routes that resolve in `out/`.

Limitations explicitly documented to the operator:

- No ISR; revalidation is a re-export + re-build cycle.
- No SSR personalization (logged-in nav bar visible to all visitors).
- No preview mode for unpublished drafts.
- The admin still requires Node for `pnpm dev`. (Or ship the admin as a static-export too, since admin doesn't need SSR — see §3.7.)

This mode is fine for blogs, docs sites, marketing pages. It is **not** fine for membership sites, eCommerce, or anything with per-user UI on the public site.

### 3.3 Admin-as-SPA option

The admin is largely a SPA after first paint. We could ship admin as a static export and have a single binary serve admin + API + (static-exported) public site:

- Pros: one binary, one process, easiest possible self-host.
- Cons: admin SSR for auth bootstrap is awkward (cookie + redirect logic moves to the API), we lose some App-Router conveniences.
- Stance: **acceptable for v1.1 as an opt-in mode.** Default ships Node-based admin-web.

---

## 4. Kubernetes reference deployment

Manifests below are illustrative. We've omitted the `metadata.namespace`, labels, and selectors for brevity (they're the obvious thing). The full manifests live in `deploy/k8s/` (forward reference to repo layout).

### 4.1 Postgres — managed-first, StatefulSet as fallback

**Use managed Postgres** (Cloud SQL, RDS, Aiven, Neon) in production. Day-2 (HA, backups, PITR, version upgrades) is rarely worth running yourself without a DBA.

For in-cluster, **CloudNativePG (CNPG)** with a `Cluster` CR: 3 instances, 100 Gi `fast-ssd`, `unsupervised` primary update strategy. Key `postgresql.parameters`: `max_connections=200`, `shared_buffers=1GB`, `effective_cache_size=3GB`, `work_mem=16MB`, `wal_level=logical` (required for logical replication / future CDC), `max_wal_size=4GB`. `barmanObjectStore` backs up to S3 with `gzip` WAL compression and 30-day retention. `monitoring.enablePodMonitor: true`.

### 4.2 Redis — managed first, StatefulSet fallback

Prefer managed Redis (ElastiCache, Memorystore, Upstash). For in-cluster, a 3-replica `StatefulSet` (Bitnami chart with Sentinel) with `--appendonly yes`, `--maxmemory 2gb`, `--maxmemory-policy allkeys-lru`, and a 20 Gi PVC per replica on a fast-SSD storage class. Manifest omitted; standard shape.

Sizing rules of thumb (see §17): 200 MB / 100k sessions, 500 MB Asynq overhead per 1M queued jobs, plugin-KV per plugin × installed count, +30% headroom.

### 4.3 core-api Deployment + HPA

```yaml
apiVersion: apps/v1
kind: Deployment
metadata: { name: core-api }
spec:
  replicas: 3
  strategy:
    type: RollingUpdate
    rollingUpdate: { maxUnavailable: 0, maxSurge: 1 }
  template:
    spec:
      terminationGracePeriodSeconds: 60
      serviceAccountName: gonext-core
      containers:
      - name: core
        image: ghcr.io/gonext/gonext-core:1.4.7
        args: ["serve","api"]
        ports:
        - { name: http,    containerPort: 8080 }
        - { name: metrics, containerPort: 9090 }
        envFrom:
        - configMapRef: { name: gonext-config }
        - secretRef:    { name: gonext-secrets }
        resources:
          requests: { cpu: "500m", memory: "512Mi" }
          limits:   { cpu: "2",    memory: "1Gi" }
        livenessProbe:  { httpGet: { path: /healthz, port: http }, initialDelaySeconds: 10, periodSeconds: 10, failureThreshold: 3 }
        readinessProbe: { httpGet: { path: /readyz,  port: http }, initialDelaySeconds: 5,  periodSeconds: 5,  failureThreshold: 2 }
        lifecycle:
          preStop:
            exec: { command: ["/gonext","drain","--timeout=45s"] }
        securityContext:
          allowPrivilegeEscalation: false
          readOnlyRootFilesystem: true
          capabilities: { drop: [ALL] }
          runAsNonRoot: true
---
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata: { name: core-api }
spec:
  scaleTargetRef: { apiVersion: apps/v1, kind: Deployment, name: core-api }
  minReplicas: 3
  maxReplicas: 20
  metrics:
  - type: Resource
    resource: { name: cpu,    target: { type: Utilization, averageUtilization: 70 } }
  - type: Resource
    resource: { name: memory, target: { type: Utilization, averageUtilization: 80 } }
  behavior:
    scaleDown:
      stabilizationWindowSeconds: 300
      policies: [{ type: Percent, value: 25,  periodSeconds: 60 }]
    scaleUp:
      stabilizationWindowSeconds: 0
      policies:
      - { type: Pods,    value: 4,   periodSeconds: 30 }
      - { type: Percent, value: 100, periodSeconds: 30 }
      selectPolicy: Max
```

### 4.4 public-web and admin-web Deployments

Identical shape to core-api with the following differences:

```yaml
# public-web: replicas 3, HPA 3..30 on CPU 60%
spec:
  template:
    spec:
      terminationGracePeriodSeconds: 30
      containers:
      - name: web
        image: ghcr.io/gonext/gonext-web:1.4.7
        env:
        - { name: DONEXT_WEB_APP, value: "public" }      # "admin" for admin-web
        - { name: NEXT_PUBLIC_API_URL, value: "http://core-api.gonext.svc.cluster.local:8080" }
        ports: [{ containerPort: 3000 }]
        resources:
          requests: { cpu: "300m", memory: "512Mi" }
          limits:   { cpu: "1",    memory: "1Gi" }
        livenessProbe:  { httpGet: { path: /api/health, port: 3000 } }
        readinessProbe: { httpGet: { path: /api/ready,  port: 3000 } }
        lifecycle:
          preStop:
            exec: { command: ["sh","-c","sleep 5 && kill -SIGTERM 1"] }
```

admin-web: `replicas: 2`, no HPA (low traffic), `cpu: 200m / 1 core`, `memory: 384Mi / 768Mi`. Bump replicas manually for large editorial teams.

### 4.6 core-worker Deployment + HPA on queue depth

Identical shape to core-api, except:

- `args: ["serve","worker"]`, `DONEXT_WORKER_QUEUES=default=6,media=4,invalidation=2,migrate=2,webhooks=2`.
- `terminationGracePeriodSeconds: 300` (long jobs need room).
- `preStop --timeout=240s`. No `readinessProbe` — workers aren't behind a Service.
- HPA on **external metrics** `asynq_queue_depth{queue="default"}` (target 50/pod) and `{queue="media"}` (target 20/pod). The exporter is doc 10's responsibility; queue list is doc 12's.

### 4.7 core-cron Deployment with leader election

Two replicas, `strategy.type: Recreate` (we want the lease to flip cleanly, not roll). Identical container shape with `args: ["serve","cron"]`, `DONEXT_CRON_LEASE_KEY=gonext:cron:leader`, `DONEXT_CRON_LEASE_TTL=15s`. Small resources (`100m/128Mi` request, `500m/256Mi` limit). Liveness only on `/healthz`. See §16 for the leader rationale.

### 4.8 Ingress

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: gonext
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt-prod
    nginx.ingress.kubernetes.io/proxy-body-size: "100m"          # match media upload limit
    nginx.ingress.kubernetes.io/proxy-read-timeout: "60"
    nginx.ingress.kubernetes.io/proxy-send-timeout: "60"
spec:
  ingressClassName: nginx
  tls:
  - hosts: [example.com, admin.example.com]
    secretName: example-com-tls
  rules:
  - host: example.com
    http:
      paths:
      - { path: /,        pathType: Prefix, backend: { service: { name: public-web, port: { number: 3000 } } } }
      - { path: /api,     pathType: Prefix, backend: { service: { name: core-api,   port: { number: 8080 } } } }
      - { path: /graphql, pathType: Prefix, backend: { service: { name: core-api,   port: { number: 8080 } } } }
  - host: admin.example.com
    http:
      paths:
      - { path: /,    pathType: Prefix, backend: { service: { name: admin-web, port: { number: 3000 } } } }
      - { path: /api, pathType: Prefix, backend: { service: { name: core-api,  port: { number: 8080 } } } }
```

Note: `/api` is mounted on both hostnames. Same backend, two front doors. CORS and CSRF are configured per hostname (admin enforces strict same-site; public allows configured CORS origins for headless clients).

### 4.9 NetworkPolicies

Default-deny within the namespace, then allow:

- `web → core-api:8080` from `public-web`, `admin-web`, `core-worker` (ISR webhook back-edge).
- `{core-api, core-worker, core-cron} → gonext-pg:5432`
- `{core-api, core-worker, core-cron} → gonext-redis:6379`
- DNS egress to `kube-system:53/UDP`.
- `core-api → public-web:3000` for ISR revalidate webhook.

S3 / SMTP / CDN egress is external; manage at the cluster egress gateway, not via NetworkPolicy.

### 4.10 PodDisruptionBudget

```yaml
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata: { name: core-api }
spec:
  minAvailable: 2
  selector: { matchLabels: { app: core-api } }
---
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata: { name: public-web }
spec:
  minAvailable: 2
  selector: { matchLabels: { app: public-web } }
```

We do **not** PDB workers — voluntary drains are fine; jobs requeue.

---

## 5. Docker Compose for dev / small self-host

The single recipe below brings up the entire stack on a laptop or a 4-vCPU VPS:

```yaml
# docker-compose.yml — abbreviated; the full file ships in the repo.
x-core-env: &core-env
  DATABASE_URL: postgres://gonext:gonext@postgres:5432/gonext?sslmode=disable
  REDIS_URL:    redis://redis:6379/0
  S3_ENDPOINT:  http://minio:9000
  S3_BUCKET:    gonext-media
  S3_ACCESS_KEY: minioadmin
  S3_SECRET_KEY: minioadmin
  DONEXT_SECRET_KEY: ${DONEXT_SECRET_KEY:?must be set}
  DONEXT_PEPPER:     ${DONEXT_PEPPER:?must be set}

services:
  postgres:
    image: postgres:15-alpine
    environment: { POSTGRES_USER: gonext, POSTGRES_PASSWORD: gonext, POSTGRES_DB: gonext }
    volumes: [pg-data:/var/lib/postgresql/data]
    healthcheck: { test: ["CMD-SHELL","pg_isready -U gonext"], interval: 5s, retries: 10 }

  redis:
    image: redis:7.2-alpine
    command: ["redis-server","--appendonly","yes","--maxmemory","1gb","--maxmemory-policy","allkeys-lru"]
    volumes: [redis-data:/data]
    healthcheck: { test: ["CMD","redis-cli","ping"], interval: 5s, retries: 10 }

  minio:    # dev S3; replace with real S3 in self-host prod
    image: minio/minio:latest
    command: server /data --console-address ":9001"
    environment: { MINIO_ROOT_USER: minioadmin, MINIO_ROOT_PASSWORD: minioadmin }
    volumes: [minio-data:/data]
    ports: ["9000:9000","9001:9001"]

  core-api:
    image: ghcr.io/gonext/gonext-core:1.4.7
    command: ["serve","api"]
    environment: { <<: *core-env, DONEXT_MODE: api }
    depends_on:
      postgres: { condition: service_healthy }
      redis:    { condition: service_healthy }
    ports: ["8080:8080"]

  core-worker:
    image: ghcr.io/gonext/gonext-core:1.4.7
    command: ["serve","worker"]
    environment: { <<: *core-env, DONEXT_MODE: worker }
    depends_on: [core-api]

  core-cron:
    image: ghcr.io/gonext/gonext-core:1.4.7
    command: ["serve","cron"]
    environment: { <<: *core-env, DONEXT_MODE: cron }
    depends_on: [redis]

  public-web:
    image: ghcr.io/gonext/gonext-web:1.4.7
    environment: { DONEXT_WEB_APP: public, NEXT_PUBLIC_API_URL: http://core-api:8080 }
    ports: ["3000:3000"]

  admin-web:
    image: ghcr.io/gonext/gonext-web:1.4.7
    environment: { DONEXT_WEB_APP: admin, NEXT_PUBLIC_API_URL: http://core-api:8080 }
    ports: ["3001:3000"]

volumes: { pg-data: , redis-data: , minio-data: }
```

A `.env.example` ships with safe defaults and the two REQUIRED variables (`DONEXT_SECRET_KEY`, `DONEXT_PEPPER`). The first-run UX is `cp .env.example .env && gonext gen-secrets >> .env && docker compose up -d`.

---

## 6. Bare-metal self-host (single VPS, no Docker)

Goal: a self-hoster moving from cPanel / Plesk / WP-on-LAMP should be able to install on a 2-vCPU / 4-GB VPS in under 15 minutes.

### 6.1 Layout

```
/opt/gonext/
  bin/gonext                  # the Go binary
  bin/node-v20.x.x-linux-x64/ # bundled Node runtime
  apps/public/                # Next.js standalone output
  apps/admin/                 # Next.js standalone output
  config/gonext.env           # config (mode=0600, owned by gonext user)
  config/Caddyfile            # reverse proxy
  data/                       # local FS media (until S3 configured)
  log/                        # local log files
```

### 6.2 Installer

A single shell script that:

1. Creates the `gonext` system user.
2. Downloads the platform tarball.
3. Generates secrets (`DONEXT_SECRET_KEY`, `DONEXT_PEPPER`) into `/opt/gonext/config/gonext.env`.
4. Installs Postgres 15 and Redis 7 via the OS package manager.
5. Creates the DB, runs `gonext migrate`.
6. Installs the systemd units (below).
7. Installs the Caddyfile and reloads Caddy.
8. Opens 80 and 443 in `ufw` (if present).
9. Triggers a Let's Encrypt cert (Caddy auto-TLS).

### 6.3 systemd units

`/etc/systemd/system/gonext-core-api.service`:

```ini
[Unit]
Description=GoNext core API
After=network-online.target postgresql.service redis-server.service

[Service]
Type=notify
User=gonext
Group=gonext
EnvironmentFile=/opt/gonext/config/gonext.env
Environment=DONEXT_MODE=api
ExecStart=/opt/gonext/bin/gonext serve api
Restart=always
RestartSec=5
KillSignal=SIGTERM
TimeoutStopSec=60
LimitNOFILE=65535

# Hardening
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ProtectHome=yes
ReadWritePaths=/opt/gonext/data /opt/gonext/log
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
LockPersonality=yes

[Install]
WantedBy=multi-user.target
```

`gonext-core-worker.service` and `gonext-core-cron.service` are identical with `DONEXT_MODE` changed.

The web units (`gonext-public-web.service`, `gonext-admin-web.service`) use `Type=simple`, `ExecStart=/opt/gonext/bin/node-v20/bin/node server.js`, `Environment=DONEXT_WEB_APP=public|admin NODE_ENV=production PORT=3000|3001`, `TimeoutStopSec=30`, and the same hardening directives.

### 6.4 Caddyfile

```
example.com {
    encode zstd gzip
    log {
        output file /opt/gonext/log/caddy-access.log
        format console
    }

    @api path /api/* /graphql
    reverse_proxy @api 127.0.0.1:8080 {
        flush_interval -1                 # streaming responses
        transport http { read_timeout 60s write_timeout 60s }
    }

    reverse_proxy 127.0.0.1:3000 {
        flush_interval -1
    }

    header {
        Strict-Transport-Security "max-age=31536000; includeSubDomains"
        X-Content-Type-Options "nosniff"
        Referrer-Policy "strict-origin-when-cross-origin"
        Permissions-Policy "camera=(), microphone=(), geolocation=()"
        # Full security headers in doc 15.
    }
}

admin.example.com {
    @api path /api/*
    reverse_proxy @api 127.0.0.1:8080

    reverse_proxy 127.0.0.1:3001

    header {
        Strict-Transport-Security "max-age=31536000; includeSubDomains; preload"
        X-Frame-Options "DENY"
        Content-Security-Policy "default-src 'self'; script-src 'self' 'wasm-unsafe-eval'; style-src 'self' 'unsafe-inline'"
    }
}
```

Caddy is preferred over Nginx for self-host because of auto-TLS. We document an Nginx alternative in the installer for users who already run Nginx; the reverse-proxy semantics are identical.

---

## 7. Environment variables and config

We use **flat env vars** (12-factor) as the source of truth. A `gonext.env.example` ships with every release.

### 7.1 The full surface (core / Go binary)

Required (binary refuses to start with exit 78 if missing):

| Variable | Notes |
|---|---|
| `DONEXT_MODE` | `api` \| `worker` \| `cron` \| `migrate` \| `static-export` |
| `DATABASE_URL` | `postgres://user:pass@host:5432/db?sslmode=require` |
| `REDIS_URL` | `redis://[:pass@]host:6379/0` |
| `S3_REGION`, `S3_BUCKET` | Plus `S3_ACCESS_KEY` / `S3_SECRET_KEY` unless IAM role / IRSA |
| `DONEXT_SECRET_KEY` | 32-byte base64; signs session cookies, CSRF tokens, ISR webhook, encrypts plugin secrets (doc 15) |
| `DONEXT_PEPPER` | 32-byte base64; password-hash pepper (doc 06 §3.3) |
| `DONEXT_PUBLIC_BASE_URL`, `DONEXT_ADMIN_BASE_URL` | Canonical URLs |
| `DONEXT_ISR_WEBHOOK_SECRET` | If ISR enabled; HMAC shared with public-web |

Optional (defaults shown):

| Variable | Default | Notes |
|---|---|---|
| `DONEXT_HTTP_ADDR` / `DONEXT_METRICS_ADDR` | `:8080` / `:9090` | |
| `DONEXT_LOG_LEVEL` / `DONEXT_LOG_FORMAT` | `info` / `json` | |
| `DONEXT_ENV` | `production` | `production`/`staging`/`development` |
| `DATABASE_MAX_CONNS` / `DATABASE_MIN_CONNS` | `25` / `5` | pgxpool sizing |
| `DATABASE_RO_URL` | falls back to `DATABASE_URL` | Read replica DSN |
| `S3_ENDPOINT`, `S3_FORCE_PATH_STYLE`, `S3_PUBLIC_BASE_URL` | — | Endpoint for MinIO/R2; path-style for MinIO; public CDN base |
| `DONEXT_PLUGIN_DIR` | `/var/lib/gonext/plugins` | `.gnplugin` location |
| `DONEXT_PLUGIN_FUEL_BUDGET` / `DONEXT_PLUGIN_TIMEOUT_MS` | `1000000` / `5000` | wazero limits per hook |
| `DONEXT_WORKER_QUEUES` | `default=10` | `q1=N,q2=M` |
| `DONEXT_CRON_LEASE_KEY` / `DONEXT_CRON_LEASE_TTL` | `gonext:cron:leader` / `15s` | (§16) |
| `DONEXT_ISR_WEBHOOK_URL` | `http://public-web:3000` | |
| `SMTP_URL`, `SMTP_FROM` | — | Doc 13 |
| `CDN_PROVIDER`, `CDN_API_TOKEN`, `CDN_ZONE_ID` | `none` | Cloudflare/Fastly/none |
| `DONEXT_FEATURES` | empty | CSV flag set |
| `DONEXT_TRUSTED_PROXIES` | empty | CIDRs allowed to set XFF |
| `DONEXT_STORAGE_LOCAL_PATH` | `/var/lib/gonext/media` | Used only when `S3_*` unset |

Each variable's read site is documented inline in `internal/config/config.go`. `gonext config dump --redact` prints the resolved set with secrets masked.

### 7.2 Web-tier vars (Next.js)

`DONEXT_WEB_APP` (`public`|`admin`), `NEXT_PUBLIC_API_URL`, `NEXT_PUBLIC_BASE_URL`, `DONEXT_ISR_WEBHOOK_SECRET` (public-web only, must match core-api), `NODE_OPTIONS` (`--max-old-space-size=512` per pod typical).

### 7.3 Config precedence

`defaults < /etc/gonext/config.yaml (optional) < env vars < command-line flags`. YAML is self-host convenience; K8s deployments use env vars only.

---

## 8. Secrets injection

### 8.1 Categories of secret

1. **Infrastructure secrets**: `DATABASE_URL` (contains password), `REDIS_URL` (may contain password), `S3_ACCESS_KEY`, `S3_SECRET_KEY`. Set at deploy time.
2. **Cryptographic secrets**: `DONEXT_SECRET_KEY`, `DONEXT_PEPPER`. Generated once per environment, rotated by a procedure in doc 15.
3. **Outbound API secrets**: `CDN_API_TOKEN`, `SMTP_URL` (contains password). Per-environment.
4. **Plugin secrets**: declared by a plugin's manifest (`secrets.keys`, doc 02 §2.2), stored in the host's `plugin_secrets` table, served to the plugin WASM via the `secret_get(name)` host call.

Categories 1–3 are *operator* secrets, injected at process boot. Category 4 is *content-data* — they're stored in the application database (encrypted at rest via the cryptographic secret in category 2). The plugin manifest declares **which** secrets the plugin needs; the operator sets the **values** via the admin UI or `gonext secret set <plugin> <key> <value>`.

This split matters because:

- Operator secrets are part of the *deployment*; their lifecycle is deploy-time, owned by infra.
- Plugin secrets are part of the *content*; their lifecycle is admin-time, owned by site editors.

If we lumped them, every plugin install would require a redeploy.

### 8.2 Injection mechanisms by environment

| Environment | Mechanism |
|---|---|
| K8s | `Secret` resources mounted as env vars (via `envFrom: secretRef`). One secret per logical grouping (`gonext-pg`, `gonext-redis`, `gonext-app-keys`). Sealed Secrets, External Secrets, or SOPS for git-ops. |
| Docker Compose | `.env` file with `mode=0600`; `secrets:` section for higher-security production-Compose deployments. |
| Bare-metal | `/opt/gonext/config/gonext.env` (mode=0600, owned by `gonext:gonext`). |
| Dev | `.env.local` (gitignored). |

We do **not** read secrets from the database during boot — bootstrapping that requires the secret key, which we'd be reading from the database. The cryptographic secret must live outside the DB.

### 8.3 Rotation (operational shape; doc 15 owns the procedure)

- `DONEXT_SECRET_KEY` rotation: support **two simultaneous keys** (`DONEXT_SECRET_KEY` + `DONEXT_SECRET_KEY_PREV`). Verify against both; sign with the current. After all sessions/tokens cycle (typically 30 days), drop `_PREV`. This is doc 15's spec; the operator-facing knob is just two env vars.
- `DONEXT_PEPPER` rotation: cannot be rotated without re-hashing every password on next login (doc 06 §3.3 handles via a `pepper_version` column).
- Plugin secrets: stored encrypted with `DONEXT_SECRET_KEY`. Rotating the secret key requires re-encryption (a migration in the rotation procedure).

### 8.4 Operator vs Plugin secret coexistence

Plugin manifests declare keys (`secrets.keys = ["google_indexing_api_token"]`). Operators set values via admin UI; core-api encrypts with `DONEXT_SECRET_KEY` and writes to `plugin_secrets(plugin_slug, key, value_ciphertext, value_iv, last_read_at)`. When the plugin calls `secret_get(name)`, the host verifies the manifest, decrypts, returns the plaintext into WASM memory, and stamps `last_read_at`. Plaintext never appears in logs (doc 10 redacts `*_token|*_key|*_secret`), in DB rows, or in backups. A restored backup brought up under a different `DONEXT_SECRET_KEY` cannot decrypt plugin secrets — doc 16 owns the restore-with-keys procedure.

---

## 9. Database migrations

### 9.1 Tool choice

**Decision: `golang-migrate/migrate` for core migrations.**

We evaluated:

- `golang-migrate`: standard, idempotent, embeddable, version-table-based, supports up/down. Migrations are plain SQL files. Battle-tested.
- `pressly/goose`: similar, with Go-function migrations. Slightly less popular; the Go-function feature is rarely worth the complexity.
- `Atlas`: declarative schema management. Excellent, but the declarative model is a big bet, and it complicates plugin migrations (plugins ship imperative SQL).
- `sqlc`: not a migration tool — generates Go from SQL. Used as the **query** generator (doc 01 §15.1.5 hints at a repository layer; the choice between `pgx` raw and `sqlc` is doc 01's call, not ours).

`golang-migrate` wins because:

- It supports filesystem and embedded migration sources — we embed them with `embed.FS`.
- The same engine runs core *and* plugin migrations (doc 02 §3.3 requires this; see §9.4 below).
- It's the most-Googled tool, which matters for self-hosters.

### 9.2 Layout

```
internal/migrations/
  core/
    000001_init_users.up.sql
    000001_init_users.down.sql
    000002_init_posts.up.sql
    000002_init_posts.down.sql
    ...
    000043_add_pepper_version.up.sql
    000043_add_pepper_version.down.sql
```

A `go:embed` directive in `internal/migrations/embed.go`:

```go
package migrations

import "embed"

//go:embed core/*.sql
var Core embed.FS
```

### 9.3 Boot-time behavior

```
process start
  └─→ config.Load()
  └─→ db.OpenPool(DATABASE_URL)
  └─→ migrations.Run(ctx, db, migrations.Core, "schema_migrations_core")
        ├─ acquires an advisory lock (pg_try_advisory_lock(0xD0NE_C0DE_CADE))
        ├─ reads current version from schema_migrations_core
        ├─ applies pending up-migrations in order
        ├─ writes new version
        └─ releases lock
```

The advisory lock means: 10 pods can race the migration on boot; one wins, others block until the migration completes, then proceed.

Process behavior:

- `gonext serve api` runs migrations on boot **by default**.
- `--no-auto-migrate` disables auto-migration. In that mode, the binary refuses to start if migrations are pending (compares `schema_version_required` from the build to the DB's recorded version).
- `gonext migrate up` runs migrations and exits.
- `gonext migrate down N` runs N down-migrations and exits.
- `gonext migrate version` prints the current version.

For K8s, the recommended pattern is an **initContainer** running `gonext migrate up`. This makes the migration explicit in pod logs and avoids the "ten pods racing the lock" pattern, which is fine but slow.

### 9.4 Plugin migrations integrate via a separate engine instance

Plugin migrations live in the plugin's `.gnplugin` archive at `migrations/*.sql` (doc 02 §3.3). They are namespaced (`plg_{slug}_*`) by the static SQL linter described in 02 §3.3.

We run plugin migrations with the **same** `golang-migrate` engine, but with:

- A per-plugin migration source (the unpacked plugin directory).
- A per-plugin schema-migrations table: `schema_migrations_plg_{slug}`.
- A per-plugin advisory lock: `hash("plg-"+slug)`.

This means **plugin migrations are not serialized** with core migrations. Pros: a slow plugin migration doesn't block a different plugin's migration. Cons: a plugin migration might run against a core schema that's still mid-migration. We resolve this with the rule: **core migrations always complete first**; plugin activation is gated on `schema_migrations_core` matching the binary's expected version. (Doc 02 §3.3 leaves this open — we resolve it here.)

### 9.5 Rollback story

`golang-migrate` supports down-migrations, but **rollback is not the same as zero-downtime**. Real production rollbacks use the expand/contract pattern (§13.2). Down-migrations are for:

- Dev environments.
- Aborted releases that haven't yet served real traffic.

Once data has flowed under a migration, **don't down-migrate; deploy a corrective forward migration.** The binary's `migrate down` is gated behind `--allow-data-loss` in production mode to prevent accidents.

### 9.6 Migration ordering across core + plugin

```
boot start
  │
  ▼
[1] core migrations run (advisory lock, blocking)
  │
  ▼
[2] plugin discovery: scan DONEXT_PLUGIN_DIR for .gnplugin
  │
  ▼
[3] for each active plugin (in install order):
      ├─ acquire plugin advisory lock
      ├─ run plugin migrations
      └─ release lock
  │
  ▼
[4] hook bus warm-up: register all plugin hook handlers
  │
  ▼
[5] readyz returns 200
```

A plugin whose migration fails is marked `state=error` in `plugins.plugins` (doc 02 §3.4) and not loaded. The error is surfaced in the admin UI; other plugins continue to load.

---

## 10. Boot sequence

```
process start
  │
  ▼  [1] config.Load(env, flags, optional YAML).
  ▼      Missing required vars → exit 78.
  ▼  [2] logger.Init() + metrics.Init(); bind :9090.
  ▼      /healthz → 200 as soon as the process is live.
  ▼      /readyz → 503 until [9].
  ▼  [3] db.OpenPool(); ping with exp-backoff retry (max 60s).
  ▼  [4] migrations.Run() (core, advisory-lock-protected; skipped if --no-auto-migrate).
  ▼  [5] redis.Connect(); verify PING.
  ▼  [6] s3.Connect(); HEAD bucket; verify list+head perms.
  ▼  [7] plugins.Discover(): for each active plugin → verify
  ▼      signature (doc 15) → unpack → run plugin migrations →
  ▼      load WASM module → register hooks. Failed plugins go to
  ▼      state=error and are not loaded; other plugins continue.
  ▼  [8] mode-specific init:
  ▼        api    — hook bus warm; HTTP listener prepared.
  ▼        worker — Asynq client open; consumer pool started.
  ▼        cron   — leader-election loop begun (§16); scheduler
  ▼                 idle until lease held.
  ▼  [9] /readyz → 200.
  ▼        api:    bind :8080, accept.
  ▼        worker: start consuming.
  ▼        cron:   poll for leadership.
```

Boot-time targets: **<5s** warm (no pending migrations), **<30s** with full migration run on fresh DB.

Any permanent failure in [3]–[6] exits non-zero. We do not "degrade gracefully" past a missing dependency — a CMS without Postgres has no business serving traffic.

---

## 11. Graceful shutdown

A clean shutdown is the single most important reliability property of a Go service. We have explicit policy.

### 11.1 Signal handling

```go
ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
defer cancel()
```

`SIGTERM` triggers graceful shutdown. `SIGKILL` (after K8s `terminationGracePeriodSeconds`) is uncontrollable; we make sure not to need it.

### 11.2 The drain sequence

```
SIGTERM
  │
  ▼  [A] /readyz → 503; wait 1s so ingress drops this pod from endpoints.
  ▼  [B] HTTP listener stops accept(). In-flight requests continue.
  ▼  [C] api mode: wait for in-flight handlers; hard deadline 30s.
  ▼      Pod gracePeriod 60s; preStop --timeout=45s; 15s buffer.
  ▼  [D] worker mode: stop pulling jobs. Let in-flight jobs finish (≤4min);
  ▼      cancel via context past that → Asynq requeues per doc 12.
  ▼  [E] cron mode: DEL Redis lease key; standby takes over in ≤ TTL.
  ▼  [F] Drain in-flight WASM hooks. Existing per-hook timeout (doc 02
  ▼      §3.4: 5s default) is NOT extended; wazero CallContext is
  ▼      cancelled on timeout, closing the module instance.
  ▼  [G] Close pgx pool (cancels outstanding queries).
  ▼  [H] Close Redis client.
  ▼  [I] Flush logs + metrics (metrics server closes last so post-shutdown
  ▼      errors are still observable).
  ▼
exit 0
```

### 11.3 Hook drain policy (resolves doc 02 §3.4 ambiguity)

Doc 02 §3.4 mentions "5s drain" but doesn't fully specify the policy. Our position:

- **In-flight hook invocations are NOT extended during shutdown.** A hook that times out at 5s under normal load also times out at 5s during shutdown.
- **In-flight hook invocations are NOT requeued.** Hooks are part of an HTTP request or job. If the request/job is requeued (worker mode), the hook will re-run. If the request is dropped (api mode after timeout), the action-hook side effect is lost. This is the same semantic as any abandoned HTTP request — we do not invent transactional hooks.
- **The shutdown deadline applies to the *outer* operation, not the hook.** If a request has 30s to finish and the hook has 5s, the hook bounds itself; the request gets the remainder.
- **Filter hooks always run to completion** (they're synchronous-in-pipeline; the data they transform is awaited). Action hooks running in goroutines via `actor.Dispatch(...)` (doc 02 §3.4 worker pool) are cancelled with their context — fire-and-forget actions that don't complete are logged but not retried in api mode.

### 11.4 Drain command

Operators can pre-drain a pod with `gonext drain --timeout=45s`. This:

- Hits the local pod's `/internal/drain` endpoint (loopback-only).
- Sets `readyz` to 503.
- Blocks until in-flight count is 0 or timeout.
- Returns exit 0 on clean drain, exit 1 on timeout.

This is what the `preStop` hook invokes (see §4.3).

---

## 12. Health and readiness endpoints

| Endpoint | Bound on | What it checks | Returns |
|---|---|---|---|
| `/healthz` | `:9090` (and `:8080` for api) | Process is alive. No external deps. | 200 always, body `{"status":"ok","mode":"api","version":"1.4.7"}` |
| `/readyz` | same | All deps reachable, migrations applied to expected version, plugins loaded (or marked-failed-but-known), hook bus warm. | 200 ready; 503 not ready with JSON body listing failed checks |
| `/metrics` | `:9090` | Prometheus metrics scrape. | text/plain |
| `/api/health` | public-web / admin-web `:3000` | Next.js process up. | 200 |
| `/api/ready` | public-web / admin-web `:3000` | Next.js can reach core-api (cached 5s). | 200 or 503 |

`/healthz` returning anything other than 200 means the process is broken; restart.

`/readyz` JSON shape:

```json
{
  "status": "not_ready",
  "mode": "api",
  "version": "1.4.7",
  "checks": {
    "db":              { "status": "ok",   "latency_ms": 2 },
    "redis":           { "status": "ok",   "latency_ms": 1 },
    "s3":              { "status": "ok",   "latency_ms": 12 },
    "migrations":      { "status": "ok",   "version": 43 },
    "plugins":         { "status": "ok",   "loaded": 7, "failed": 0 },
    "hook_bus":        { "status": "warm" },
    "isr_webhook":     { "status": "skip", "reason": "no DONEXT_ISR_WEBHOOK_URL configured" }
  }
}
```

Probe config rationale:

- `livenessProbe` hits `/healthz` (cheap; process-up only). Restart on failure.
- `readinessProbe` hits `/readyz`. Remove from endpoints on failure (no restart).
- `initialDelaySeconds`: liveness 10s (more than worst-case boot), readiness 5s.
- `periodSeconds`: liveness 10s (rare restarts), readiness 5s (fast route-out).
- `failureThreshold`: liveness 3 (don't restart for a transient hiccup), readiness 2.

For workers (no Service), only `livenessProbe` is set. The worker controller (Asynq client) knows whether it's connected; if Redis goes away, the liveness check can still pass while the worker idles. That's intentional — we don't want Redis outage to restart workers.

---

## 13. Zero-downtime deploys

### 13.1 Rolling update behavior

K8s `RollingUpdate` with `maxUnavailable: 0, maxSurge: 1`:

1. K8s creates a new pod with the new image.
2. New pod runs boot sequence; `/readyz` is 503 until ready.
3. K8s adds new pod to endpoints when `readinessProbe` passes.
4. K8s sends `SIGTERM` to one old pod.
5. Old pod's `preStop` runs `gonext drain`; new connections route to new pods.
6. Repeat until all old pods are replaced.

Total deploy time for 3 → 3 replicas: ~30–60s.

### 13.2 Schema migrations and the expand/contract pattern

**Rule**: every migration must be safe to run while *both* the old and new binary versions are serving traffic.

This means **no backwards-incompatible DDL in a single release**. To rename `users.name` to `users.full_name`:

| Step | Release | Migration | Behavior |
|---|---|---|---|
| 1 | v1 → v1.1 | Add column `full_name`; trigger to mirror `name` → `full_name` on write | Both old and new code see consistent data; old reads `name`, new writes both. |
| 2 | v1.1 → v1.2 | Backfill `full_name` from `name` | Backfill job; can run in background. |
| 3 | v1.2 → v1.3 | New code reads `full_name` (writes both still) | Cutover. |
| 4 | v1.3 → v1.4 | Drop the trigger; old code is gone | Now safe to read/write `full_name` exclusively. |
| 5 | v1.4 → v1.5 | Drop column `name` | Contract. |

In practice we collapse 1+2 and 3+4+5 into two releases. The reviewer in CI flags any migration that does anything destructive on a column that the previous release was reading.

This is a doc 11 (testing & CI) responsibility to enforce; we name it here as a deploy-time invariant.

### 13.3 Coordinating Next.js and Go versions

A common failure mode: the API ships a new endpoint shape, the web tier still serves the old one (or vice versa) for a few seconds during rollout.

Our position:

- **API is backwards compatible within a major version**. The web tier built against API v1 must work against any v1.x server.
- During rollout, mixed versions are tolerated: a v1.4.7 web pod can hit a v1.4.6 API pod. This is what makes rolling updates work.
- **Major bumps require a coordinated cutover**: two parallel deployments, traffic switched at the ingress (see §15).

### 13.4 Feature flags

We support flags via `DONEXT_FEATURES` env var. Flags are:

- Per-environment (you can run `block_v2` in staging only).
- Read at boot and on `SIGHUP` (config reload without restart, where supported).
- Available to plugins via the `flags.is_enabled(name)` host call (doc 02 ABI; this doc just names the surface).

We did consider runtime-database-backed flags (LaunchDarkly-style). Rejected for v1: too much infra; env-var flags are sufficient for the rollout patterns we care about.

---

## 14. Multi-region

### 14.1 v1 stance: single region

**Recommendation: ship v1 as single-region only.** Reasons:

- Multi-region Postgres is hard. Doing it correctly requires either a managed multi-region offering (Aurora Global, Spanner) or operator expertise in logical replication with conflict resolution.
- ISR cache invalidation across regions adds complexity.
- The cohort of v1 users who need multi-region is small.

For v1, recommend running the cluster in the region closest to the editorial team; the CDN (§18) handles read latency for global visitors.

### 14.2 v2 stance: read-replica regions

```
Cloudflare (global)
   ├─ us-east traffic ──→ us-east region  (write)
   │                       ingress + public-web(N) + admin-web(2) + core-api(N)
   │                       core-worker(N) + core-cron(1)
   │                       Postgres primary, Redis primary
   │                                  ▲
   │                                  │ logical replication
   │                                  ▼
   └─ eu-west traffic ──→ eu-west region  (read-only)
                           ingress + public-web(N) + core-api(N, read pool)
                           Postgres replica, Redis local (regional)
                           (no admin, no workers, no cron)
       S3: cross-region replication, CDN-fronted.
```

Recommendations:

- **Postgres**: managed multi-region (Aurora Global, AlloyDB, or Spanner) or hand-rolled logical replication for the read region(s). Read region is read-only; writes are forwarded to primary via the API tier.
- **Sessions in Redis**: each region has its own Redis. Sessions are sticky to the originating region (cookie carries region hint, ingress routes by hint). This is **not** strictly required (sessions could be replicated), but the simpler answer wins.
- **ISR cache**: each region has its own Next.js ISR cache (in S3 or local volume). ISR revalidate webhook fans out — the core-api in the primary region notifies every public-web service in every region. The list of regions is config (`DONEXT_ISR_WEBHOOK_URLS=https://us-east/...,https://eu-west/...`).
- **Cache-tag invalidations**: same fan-out. The `cache_invalidations` outbox is consumed in the primary region; the consumer emits N HTTP webhooks (one per region).
- **Media**: S3 cross-region replication. `S3_PUBLIC_BASE_URL` points at the CDN; the CDN pulls from the regional bucket.
- **Cron**: single global cron leader in the write region. Jobs are enqueued to the primary Redis; workers in the primary region consume them. (We do **not** run workers in read regions.)
- **Write latency**: editors in the read region see write latency proportional to the cross-region RTT (typically 50–150 ms for North America ↔ Europe). Acceptable.

### 14.3 What we are not building in v2

- Multi-master Postgres. The "two regions both accept writes" pattern requires conflict resolution that we are not going to design for a CMS.
- Sticky-tenant per region (for SaaS). Defer to a future doc.

---

## 15. Blue/green and canary

### 15.1 Blue/green

```
ingress (cookie / header routing)
  ├─ X-Donext-Slice: blue  ─→  blue stack  v1.4.7 (current)
  └─ X-Donext-Slice: green ─→  green stack v2.0.0 (new)
                                    │
                            shared DB + Redis + S3
```

Both stacks share the database and Redis — we cannot fork the data. This works **only** when the schema is compatible with both versions (§13.2 expand/contract). An ingress cookie selects the stack; internal staff get green first via a header. We don't use blue/green for routine releases; rolling updates suffice. Blue/green is for major-version cutovers.

### 15.2 Canary

For a routine release with elevated risk:

- Deploy v1.4.8 as a separate Deployment with `replicas: 1` and label `slice: canary`.
- Ingress routes 5% of traffic to the canary (random or session-stable).
- Operator watches dashboards for 30 minutes.
- If green: scale the canary Deployment up, scale the stable Deployment down, rename.
- If red: scale canary to 0 (no rollback of schema, see §13.2).

We use ingress-level weighted routing (Nginx supports this via `canary` annotations; service meshes like Istio do it cleanly).

### 15.3 Per-tenant canary (SaaS)

For SaaS deployments: tenant ID → slice mapping. A small allowlist of tenants opts into canary. This is a SaaS-layer concern; in v1 self-host, we don't ship it.

---

## 16. Cron leader election

**Resolves doc 02 §15 open question.**

Asynq's `Scheduler` (the cron component) cannot run in two places at once — it would emit duplicate jobs to the queue. We need exactly-one-leader semantics across N `core-cron` pods.

### 16.1 Options considered

| Option | Verdict |
|---|---|
| Pin `replicas: 1` for `core-cron` Deployment | **Rejected**. Single point of failure: during the new pod's boot, no scheduling happens. For a CMS this is mostly fine (cron jobs are not millisecond-critical) but produces missed cron windows on every deploy. |
| K8s `Lease` API for leader election | **Considered**. Works, but requires RBAC for the SA, and only works on K8s — not Compose/bare-metal. |
| Redis SET NX EX with renewal (Redlock-style for a single Redis) | **Chosen.** Works everywhere we run (K8s, Compose, bare-metal). |
| etcd | **Rejected**. We don't otherwise depend on etcd. |
| Postgres advisory lock | Considered. Works, but ties scheduling availability to DB availability. Redis is the right substrate because Asynq itself uses Redis. |

### 16.2 The lease protocol

Two replicas of `core-cron`:

```
each replica loop:
  1. SET gonext:cron:leader <pod-id> NX EX 15
     - if OK: I am leader. Start the Asynq Scheduler.
     - else:  I am follower. Don't start Scheduler.
  2. every 5s while leader:
       EVAL "if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('EXPIRE', KEYS[1], 15) else return 0 end" 1 gonext:cron:leader <pod-id>
       - if EXPIRE returns 0: I lost the lease. Stop Scheduler.
  3. on SIGTERM: DEL gonext:cron:leader (only if I'm the holder).
```

Worst-case missed-cron-window after leader failure: `DONEXT_CRON_LEASE_TTL` = 15s. For a cron job that runs every minute, this means up to one missed iteration. Acceptable.

A safety: when the new leader starts the Scheduler, Asynq's `Scheduler.Start()` re-reads the cron registrations from its in-memory state. The registrations are baked into the binary, so they're identical across pods. There's no "leader catches up on missed schedules" — missed runs stay missed. (Future: a `missed_runs` log surfaced in admin.)

### 16.3 Compose / bare-metal

Single instance of `core-cron`; Redis lease is unnecessary but the same code runs (it just always wins the lease). On restart, missed-window behavior is the same.

---

## 17. Resource sizing baseline

**Target shape: 10,000-page site, 100 RPS sustained, 99th-percentile page latency under 500ms.**

| Component | Min | Headroom | Notes |
|---|---|---|---|
| `core-api` | 3 pods × (500m CPU, 512Mi RAM) | scale to 8 pods at 100 RPS sustained | ~12 RPS per pod; CPU-bound on serialization, not DB. |
| `core-worker` | 3 pods × (500m CPU, 768Mi RAM) | scale to 8 pods | Depends on media throughput; thumbnail jobs dominate. |
| `core-cron` | 1 active + 1 standby × (100m CPU, 128Mi RAM) | constant | tiny |
| `public-web` | 3 pods × (300m CPU, 512Mi RAM) | scale to 12 pods | Next.js SSR; ISR cache absorbs most pages. |
| `admin-web` | 2 pods × (200m CPU, 384Mi RAM) | constant | low traffic |
| Postgres | db.m5.large or equivalent (2 vCPU, 8 GB RAM, 100 GB SSD) | bump to db.m5.xlarge at 1M posts | Read replica adds capacity for theme queries. |
| Redis | cache.m5.large (2 vCPU, 6 GB RAM) | bump if plugin KV grows | 1 GB working set typical. |
| S3 | unlimited; bucket per env | n/a | Cost: ~$5/100GB/month + egress. |
| CDN | Cloudflare free tier viable; Pro for WAF | upgrade per traffic | See §18. |

For a 1M-page site at 1k RPS, multiply: Postgres becomes db.m5.2xlarge with a 200 GB SSD, public-web fleet is 30 pods, core-api is 15 pods. Past that, look at caching and read-replica routing more carefully.

**These are starting points, not guarantees.** Real sizing depends on plugin set (a heavy SEO plugin can double API CPU), theme complexity (a theme that runs the full block tree on every render is 5x a static export), and media usage.

---

## 18. CDN integration

### 18.1 Default: Cloudflare

Reasons: largest free tier, sane defaults, cache-tag support on Enterprise, decent WAF.

### 18.2 Origin headers

Public-web sets cache headers:

- **Static pages** (ISR'd): `Cache-Control: public, s-maxage=60, stale-while-revalidate=86400`. CDN treats as cacheable for 60s; revalidates in background up to 24h.
- **Cache-tag header** (Cloudflare Enterprise): `Cache-Tag: post:42,term:7,user:1`. Tags come from the rendered page's dependencies (computed by the renderer, exposed via a `withTags()` helper).
- **Dynamic pages** (logged-in views, preview mode): `Cache-Control: private, no-store`.
- **Assets** (`_next/static/*`): `Cache-Control: public, max-age=31536000, immutable`. Hashed filenames make this safe.

### 18.3 Purge integration

The `cache_invalidations` outbox in Postgres (doc 07 §16.2) is consumed by `core-worker`. Each row has tags (e.g., `post:42`).

```
worker reads row:
  ├─ POST https://api.cloudflare.com/client/v4/zones/{zone}/purge_cache
  │     body: {"tags": ["post:42"]}
  ├─ check 200; on failure, retry per doc-12 policy
  ├─ on success: mark row processed_at
  └─ also POST to public-web /api/revalidate-tag (Next.js cache tag)
```

For Cloudflare free/Pro (no cache-tag), we fall back to purging by URL. The outbox row carries the affected paths (computed by core when the mutation happens — the same paths sent to the ISR webhook).

### 18.4 WAF rules

Recommended (Cloudflare WAF / managed rulesets):

- OWASP Core Rule Set (paranoia level 2 default; tunable).
- Rate limit on `/admin/login` (5 req/min/IP). Mirrors core's own login rate limit (doc 06).
- Geofence rule template for self-host (off by default; opt-in for admin region).
- Bot management on `/api/v1/comments/*` (per-plugin endpoints declare their own protection level — doc 02 §6.6).

### 18.5 Cache-bust audit

We emit a metric every time a cache tag is purged (`cdn_purges_total{tag_class=...}`). Reviewing this catches plugins that over-invalidate (e.g., "all my pages got purged because a single setting changed").

---

## 19. SaaS / multi-tenancy operational notes

Doc 06 §17.6 has the open question on tenancy shape (per-cluster, per-DB, per-schema). This doc takes no position on that — but does describe the operational consequences of each:

| Model | Ops shape |
|---|---|
| **per-cluster** (one cluster per tenant) | Each tenant gets its own K8s namespace or its own cluster. Strongest isolation. Highest cost per tenant. No shared infrastructure. |
| **per-DB** (one Postgres DB per tenant, shared compute) | Routes per hostname → DB connection. Schema/migrations run per DB. Connection pooling complex. Moderate isolation. |
| **per-schema** (one schema per tenant in shared DB) | `SET search_path` per request. Cheapest. Weakest isolation. Connection pooling friendly. Backup complexity per-tenant. |
| **per-row** (`tenant_id` column on every row) | Rejected per doc 06 §17.6 — but if revisited, the cheapest, with the strongest blast radius on a bug. |

Whichever model is picked, the deploy artifacts in this doc accommodate it:

- `DATABASE_URL` for per-cluster.
- A `DATABASE_TENANT_RESOLVER` flag to use a per-DB resolver (looks up DSN per hostname) for per-DB.
- A search_path middleware for per-schema.

We **do not** ship multi-tenancy in v1. Operators who need it run per-cluster (one deployment per customer) until the v2 SaaS layer (doc TBD) lands.

---

## 20. Backup integration

**Forward reference**: doc 16 (DR/backup) owns policy. This section documents the operational hooks that exist in the deployment.

### 20.1 What gets backed up

| Asset | Mechanism | Frequency | Retention |
|---|---|---|---|
| Postgres | PITR via managed (Aurora/RDS) or `pgBackRest`/CNPG to S3 | continuous WAL + daily base | 30 days |
| S3 media | bucket versioning + lifecycle rule + cross-region replication | continuous | versioning keeps all; lifecycle prunes after 90 days |
| Redis | RDB snapshots to S3 daily (low priority — Redis is ephemeral state in our design) | daily | 7 days |
| Plugin bundles (.gnplugin) | versioned in S3 plugin-store bucket | per upload | indefinite |
| Cryptographic keys (`DONEXT_SECRET_KEY`, `DONEXT_PEPPER`) | stored in a secrets manager (Vault/AWS SM/Sealed Secrets), **NOT in DB backups** | manually rotated | indefinite |

### 20.2 Backup is not in the boot path

Backups run from a separate Deployment (`gonext-backup`) using a CronJob with the relevant tools — we don't bake backup into the binary. The binary exposes `gonext export --to=s3://...` for ad-hoc exports.

### 20.3 The cache-invalidations problem on restore

A restored Postgres has stale ISR caches downstream. The recovery procedure (doc 16) requires:

1. Restore Postgres to the desired PITR point.
2. Run `gonext post-restore` which:
   - Walks the `cache_invalidations` outbox unprocessed rows and replays them.
   - **Emits a global cache flush to the CDN** (purge everything for the zone).
   - **Forces full ISR regeneration** by invalidating Next.js cache tags wholesale.
3. Bring traffic back.

This is destructive to the CDN cache, which is intentional — after a restore, every cached page is suspect.

---

## 21. Trade-offs and rejected alternatives

- **Multi-container vs all-in-one image.** Chose all-in-one per codebase: faster CI, single SBOM per artifact, easier debugging. Cost is slightly larger pulls; acceptable. Detail in §2.1.
- **K8s vs Nomad vs ECS.** Documented K8s and Compose only. K8s is the platform default; Compose covers the small-self-host floor. Nomad/ECS translations are straightforward but unsupported.
- **Embedded Node vs separate Next.js.** Separate Next.js (§3). Embedded Node is single-binary theater. Pure-Go React renderers are scope explosion. Static-export (§3.2) is the opt-in compromise.
- **Asynq vs alternatives.** Asynq is locked in by doc 00. River (Postgres-backed) and Temporal (workflow engine) are v2 candidates. Cron leader election (§16) is the only Asynq gap we close.
- **Per-row vs per-DB tenancy.** Per-DB wins on isolation and operational tooling (backups, accounting, restore). Per-row forces filter discipline; a missing predicate is a data leak. Doc 06 §17.6 already prefers per-DB.
- **Logical replication vs multi-master.** Multi-region uses logical replication / read-replica regions (§14.2). Multi-master is operationally worse than the read latency it would save.
- **Caddy vs Nginx in bare-metal.** Caddy defaults for auto-TLS. Nginx works identically for operators who already run it.
- **Static-export mode vs default Next.js.** Opt-in (§3.2) for marketing sites. Default is Node-backed Next.js because most users want ISR or auth.
- **K8s Lease vs Redis lease for cron.** Redis lease wins (§16.1) — works on K8s, Compose, and bare-metal alike.
- **CDN: Cloudflare vs Fastly vs in-house.** Cloudflare is documented default; Fastly is supported through the same `CDN_PROVIDER` interface with provider-specific purge semantics. No-CDN is supported but discouraged.

---

## 22. Open questions

The following are unresolved and either need a decision before v1 ships or have been deferred to specific later docs:

### 22.1 Resolved here (recap)

- **Cron leader election** → Redis lease (§16). Resolves doc 02 §15.
- **Hook drain on shutdown** → no extension, no requeue, bounded by hook timeout (§11.3). Resolves doc 02 §3.4 ambiguity.
- **Single-binary self-host** → admit it's not literal; offer static-export as an opt-in (§3). Reconciles README claim.
- **Image strategy** → one image per codebase, two total (§2.1).
- **Admin in same Next.js app or separate** → separate (§1.3). Resolves doc 00 Open Question #1 (operationally; doc 05 may revisit from the UI side).
- **Plugin secrets vs operator secrets** → operator at boot, plugin in DB encrypted with operator key (§8). Resolves doc 02 §16.5 (or wherever it lives) operational side.

### 22.2 Deferred to other docs

- **Detailed retry/DLQ per queue** → doc 12 (jobs).
- **Email adapter** → doc 13 (email). This doc just lists env vars.
- **CSP / header set / pepper rotation** → doc 15 (security baseline).
- **Backup retention/RPO/RTO** → doc 16 (DR/backup).
- **Multi-tenancy model** → doc 06 §17.6 (auth doc), with operational footprint sketched in §19.

### 22.3 Genuinely open

- **Service mesh** (Istio/Linkerd) as a multi-region default — probably not v1; revisit at SaaS time.
- **Per-plugin pod isolation**: a dedicated `core-worker` pool per plugin would shrink blast radius at the cost of one process boundary per plugin. Open.
- **mTLS between cluster pods**: off by default; rotation surface unspecified — defer to doc 15.
- **Serverless cold-start**: 5s boot is fine for Cloud Run, but plugin discovery (§10 step [7]) breaks that for plugin-heavy installs. A "lazy-load plugin" path is open.
- **Partial-outage readiness**: `/readyz` returns 503 if Redis is down, even though `/healthz` and `/metrics` don't need Redis. Per-route readiness is open.
- **Plugin migration ordering**: "install order" works if B is installed after A. A `requires: [...]` field with topological sort is the obvious fix; open.
- **Image base for web**: `node:20-alpine` for debuggability vs distroless-node for size. Open.
- **Worker auto-tuning**: HPA on `asynq_queue_depth` lags 10x bursts by ~1 min; KEDA-on-Redis would be faster. Open.

---

## Appendix A — Quick reference

### A.1 Environments at a glance

| Env | Cluster | DB | Redis | Image tag | Backup | CDN |
|---|---|---|---|---|---|---|
| dev | local Compose | postgres (compose) | redis (compose) | `main-<sha>` | none | none |
| staging | k8s staging cluster | managed PG (small) | managed Redis (small) | `staging-<date>` | daily | Cloudflare (preview zone) |
| prod | k8s prod cluster | managed PG (HA) | managed Redis (HA) | `1.4.7` | continuous + daily | Cloudflare |

### A.2 Boot/shutdown timing budgets

| Phase | Budget |
|---|---|
| Cold boot (api, no pending migrations) | 5s |
| Cold boot (api, full migration run) | 30s |
| Readyz to 200 after pod start (typical) | 8s |
| SIGTERM to clean exit (api) | 45s |
| SIGTERM to clean exit (worker) | up to 240s (long jobs) |
| Cron lease takeover after leader loss | up to 15s |

### A.3 The five commands an operator runs

```
gonext serve api          # the API + WASM host
gonext serve worker       # Asynq consumer
gonext serve cron         # cron scheduler (leader elected)
gonext migrate up         # apply core migrations
gonext drain --timeout=Ns # graceful drain (preStop hook)
```

Plus:

```
gonext gen-secrets        # generate DONEXT_SECRET_KEY and DONEXT_PEPPER
gonext static-export      # build the static-export bundle (§3.2)
gonext config dump --redact   # print resolved config with secrets masked
gonext secret set <plugin> <key> <value>   # set a plugin secret
```

That's the surface.

---

*End of doc 09. The deploy story is now concrete enough to ship. Open questions §22.3 should be resolved before v1; deferred items are owned by the named follow-up docs.*
