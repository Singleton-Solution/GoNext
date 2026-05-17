# GoNext Helm chart

Kubernetes deployment for the full GoNext stack: `core-api`, `core-worker`,
`core-cron`, `public-web`, `admin-web`. Implements the reference deployment
in [`docs/09-deployment-ops.md`](../../../docs/09-deployment-ops.md) §4.

Status: scaffold — production-ready surface, safe defaults, but the
container images referenced (`ghcr.io/gonext/gonext-core`,
`ghcr.io/gonext/gonext-web`) are forward references to images that ship
in later issues.

## TL;DR

```sh
# Lint and render
helm lint deploy/helm/gonext
helm template gonext deploy/helm/gonext

# Install (production: bring your own Postgres/Redis/S3)
helm install gonext deploy/helm/gonext \
  --namespace gonext --create-namespace \
  --set-string secrets.data.GONEXT_DATABASE_URL="postgres://..." \
  --set-string secrets.data.GONEXT_REDIS_URL="redis://..." \
  --set-string secrets.data.GONEXT_AUTH_PEPPER="$(openssl rand -base64 48)" \
  --set-string secrets.data.GONEXT_AUTH_SESSION_SECRET="$(openssl rand -base64 48)" \
  --set-string secrets.data.GONEXT_AUTH_CSRF_SECRET="$(openssl rand -base64 48)" \
  --set ingress.enabled=true \
  --set ingress.publicHost=example.com \
  --set ingress.adminHost=admin.example.com
```

For a kind/minikube smoke test, enable the bundled subcharts:

```sh
helm dependency update deploy/helm/gonext
helm install gonext deploy/helm/gonext \
  --namespace gonext --create-namespace \
  --set postgresql.enabled=true \
  --set redis.enabled=true \
  --set minio.enabled=true \
  --set-string postgresql.auth.password=dev_only \
  --set-string minio.auth.rootPassword=dev_only \
  --set-string secrets.data.GONEXT_DATABASE_URL="postgres://gonext:dev_only@gonext-postgresql:5432/gonext" \
  --set-string secrets.data.GONEXT_REDIS_URL="redis://gonext-redis-master:6379/0" \
  --set-string secrets.data.GONEXT_AUTH_PEPPER="$(openssl rand -base64 48)" \
  --set-string secrets.data.GONEXT_AUTH_SESSION_SECRET="$(openssl rand -base64 48)" \
  --set-string secrets.data.GONEXT_AUTH_CSRF_SECRET="$(openssl rand -base64 48)"
```

## What this chart deploys

| Component  | Kind                  | Replicas (default) | HPA              | PDB |
| ---------- | --------------------- | ------------------ | ---------------- | --- |
| core-api   | Deployment + Service  | 3                  | CPU 70% / Mem 80% | minAvailable=2 |
| core-worker| Deployment            | 2                  | KEDA queue-depth (off by default) | — |
| core-cron  | Deployment (Recreate) | 2 (Redis lease)    | —                | — |
| public-web | Deployment + Service  | 3                  | CPU 60%          | minAvailable=2 |
| admin-web  | Deployment + Service  | 2                  | —                | — |

Plus: Ingress (optional, off), NetworkPolicy (optional, off, default-deny
baseline + selective allows), ServiceAccount, ConfigMap, Secret.

## Required values

The chart will refuse to boot the apps unless every auth secret is set —
either supplied via `secrets.data.*` or projected from an existing Secret
referenced by `secrets.existingSecret`. The required keys are:

- `GONEXT_DATABASE_URL` — postgres DSN
- `GONEXT_REDIS_URL` — redis URL
- `GONEXT_AUTH_PEPPER`
- `GONEXT_AUTH_SESSION_SECRET`
- `GONEXT_AUTH_CSRF_SECRET`

Each auth secret must be ≥ 32 bytes after base64-decoding. The Go binary
validates entropy at boot per `docs/13-security-baseline.md` §5.

## Values reference

The full surface lives in [`values.yaml`](./values.yaml). Top-level
groups:

- `global` — registry prefix + pull secrets shared across all components
- `serviceAccount` — RBAC identity for all pods
- `config` — non-secret env, projected into ConfigMap
- `extraConfig` — extra keys layered on `config`
- `secrets` — secret material (or `existingSecret` pointer)
- `api` / `worker` / `cron` / `web` / `admin` — per-component knobs:
  replicas, image, resources, probes, securityContext, preStop, HPA, PDB
- `ingress` — toggleable single Ingress fronting web/admin/api
- `networkPolicy` — default-deny baseline + selective allows
- `postgresql` / `redis` / `minio` — OPTIONAL Bitnami subcharts (off)

Each key is annotated in `values.yaml` itself; this README intentionally
does not duplicate that detail.

## KEDA-based worker autoscaling

`worker.hpa.enabled=true` renders a `keda.sh/v1alpha1` ScaledObject
that scales the worker on Asynq queue depth (Prometheus trigger).

Prereqs: KEDA installed in the cluster, an Asynq exporter publishing
`asynq_queue_depth{queue="..."}`. Without these, leave it off and use
the static `worker.replicaCount`.

## Subchart dependencies

The chart declares optional Bitnami subcharts for `postgresql`, `redis`,
and `minio`. They are disabled by default. Production should use managed
services and external object storage. To use them locally:

```sh
helm dependency update deploy/helm/gonext
```

## Layout

```
deploy/helm/gonext/
  Chart.yaml
  values.yaml
  values.schema.json
  README.md
  templates/
    _helpers.tpl
    configmap.yaml
    secret.yaml
    serviceaccount.yaml
    api-deployment.yaml
    api-service.yaml
    api-hpa.yaml
    worker-deployment.yaml
    worker-hpa.yaml           # KEDA ScaledObject (off by default)
    cron-deployment.yaml
    web-deployment.yaml       # Deployment + Service + HPA
    admin-deployment.yaml     # Deployment + Service
    ingress.yaml
    networkpolicy.yaml
    pdb.yaml                  # api + web
```

## Smoke test

```sh
helm lint deploy/helm/gonext
helm template gonext deploy/helm/gonext \
  --set-string secrets.data.GONEXT_AUTH_PEPPER=x \
  --set-string secrets.data.GONEXT_AUTH_SESSION_SECRET=x \
  --set-string secrets.data.GONEXT_AUTH_CSRF_SECRET=x \
  | kubectl apply --dry-run=client -f -
```

## Issue and design references

- Issue: [#66 — Add Kubernetes Helm chart for full deployment](https://github.com/Singleton-Solution/GoNext/issues/66)
- Design: `docs/09-deployment-ops.md` §4 (Kubernetes reference deployment)
- Env surface: `packages/go/config/`
