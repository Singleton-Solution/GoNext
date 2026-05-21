# 17 — Environment & Configuration

> Owns: the full env-var surface — every variable the GoNext loader reads, with type, default, production requirements, and security guidance.
> Reader: an operator standing up a new deployment, or a contributor adding a new env-driven knob.
>
> Cross-references:
> - Loader implementation: [`packages/go/config/load.go`](../packages/go/config/load.go).
> - Schema: [`packages/go/config/config.go`](../packages/go/config/config.go).
> - Redaction rules: [`packages/go/config/dump.go`](../packages/go/config/dump.go).
> - Inline-comment quick reference: [`.env.example`](../.env.example).
> - Deployment shapes: [`docs/09-deployment-ops.md`](09-deployment-ops.md).
> - Secret-management policy: [`docs/13-security-baseline.md`](13-security-baseline.md) §5.

---

## 1. Overview

GoNext follows the twelve-factor pattern: **all** runtime configuration comes from the process environment. There are no config files, no flags, no `-config` argument. The loader runs once at process start (`config.Load()`) and the resulting `*config.Config` is passed by pointer to every component that needs it.

### 1.1 Precedence

```
process environment  >  loader-coded defaults
```

There is no intermediate layer. If `DATABASE_URL` is set in the environment, that value wins. If it is unset (or empty — see §1.3), the loader uses the documented default; for required keys, it errors out instead.

### 1.2 Loading from a file

The Go process itself does not read `.env` files. Loading from a file is the responsibility of the supervisor:

- **Local dev** — `cp .env.example .env`, then start via `direnv`, `dotenvx`, or `docker compose --env-file .env up`.
- **systemd** — `EnvironmentFile=/etc/gonext/gonext.env` in the unit file (see §5.3).
- **Kubernetes** — `envFrom: configMapRef` for non-secret keys and `envFrom: secretRef` for the redacted ones (see §5.2).
- **Docker** — `docker run --env-file .env gonext-image` or the `env_file:` key in `compose.yaml`.

### 1.3 Empty-string semantics

Empty-string env values are treated as **unset**. This matters when using `docker-compose` templates with `${FOO:-}` substitution: the substitution produces an empty string, and the loader falls back to the default rather than erroring on a `""` value. This is implemented in `getString` / `getInt` / `getBool` / `getDuration` / `getCSV`.

### 1.4 Value types

| Type | Format | Examples |
|---|---|---|
| string | UTF-8 bytes | `INFO`, `https://example.com` |
| int | base-10 integer | `25`, `1048576` |
| bool | `strconv.ParseBool` set | `true`, `false`, `1`, `0`, `t`, `f` |
| float | base-10 float | `1.0`, `0.25` |
| duration | `time.ParseDuration` | `30s`, `5m`, `1h30m`, `720h` |
| CSV | comma-separated, trimmed | `10.0.0.0/8,192.168.0.0/16` |

A parse error never silently falls back to the default — the loader returns an aggregated error listing every misconfigured key in one batch so an operator does not have to fix the same misconfiguration five times.

---

## 2. Per-section reference

Every variable below is read by `packages/go/config/load.go`. The "Required" column means "production deployment refuses to boot without it". Variables marked **secret** are redacted in `gonext config dump` (see §4).

### 2.1 Environment

| Name | Type | Default | Required | Notes |
|---|---|---|---|---|
| `GONEXT_ENV` | string | `development` | no | One of `development` \| `staging` \| `production` \| `test`. Unknown values fall back to `development`. Drives `PublicSite.AllowIndex` default. |

### 2.2 HTTP server

| Name | Type | Default | Required | Notes |
|---|---|---|---|---|
| `GONEXT_SERVER_ADDR` | string | `":8080"` | no | `host:port` or `:port`. Falls back to `PORT` when unset. |
| `PORT` | int | unset | no | PaaS shorthand (Heroku/Railway/Fly). Used only when `GONEXT_SERVER_ADDR` is empty; expands to `:<PORT>`. |
| `GONEXT_SERVER_READ_HEADER_TIMEOUT` | duration | `5s` | no | |
| `GONEXT_SERVER_READ_TIMEOUT` | duration | `15s` | no | |
| `GONEXT_SERVER_WRITE_TIMEOUT` | duration | `30s` | no | |
| `GONEXT_SERVER_IDLE_TIMEOUT` | duration | `60s` | no | Keep-alive idle window. |
| `GONEXT_SERVER_SHUTDOWN_TIMEOUT` | duration | `30s` | no | SIGTERM drain. |
| `GONEXT_SERVER_MAX_HEADER_BYTES` | int | `1048576` | no | 1 MiB. Mitigates header smuggling / slowloris. |
| `GONEXT_TRUSTED_PROXIES` | CSV | unset | no | CIDRs allowed to set `X-Forwarded-*`. Production: set to your reverse-proxy / CDN edge ranges. |

### 2.3 Logging

| Name | Type | Default | Required | Notes |
|---|---|---|---|---|
| `GONEXT_LOG_LEVEL` | string | `INFO` | no | `DEBUG` \| `INFO` \| `WARN` \| `ERROR`. |
| `GONEXT_LOG_FORMAT` | string | `json` | no | `json` \| `text`. |
| `GONEXT_LOG_ADDSRC` | bool | `false` | no | Emit `file:line` on every record. Small overhead. |

### 2.4 Database (Postgres)

| Name | Type | Default | Required | Notes |
|---|---|---|---|---|
| `DATABASE_URL` | string | — | **yes** | libpq DSN. **Secret** (contains password). Production: `sslmode=require` or `verify-full`. |
| `GONEXT_DB_MAX_OPEN_CONNS` | int | `25` | no | Pool ceiling. |
| `GONEXT_DB_MAX_IDLE_CONNS` | int | `5` | no | Pool floor. |
| `GONEXT_DB_CONN_MAX_LIFETIME` | duration | `30m` | no | Pool reaping. |
| `GONEXT_DB_CONN_MAX_IDLE_TIME` | duration | `5m` | no | Idle reaping. |
| `GONEXT_DB_STATEMENT_TIMEOUT` | duration | `30s` | no | Postgres `statement_timeout`. |
| `GONEXT_MIGRATION_DIR` | string | `./migrations` | no | golang-migrate source path. |

### 2.5 Redis

| Name | Type | Default | Required | Notes |
|---|---|---|---|---|
| `REDIS_URL` | string | `redis://localhost:6379/0` | no | May embed a password. **Secret**. Production: use `rediss://` (TLS). |
| `GONEXT_REDIS_POOL_SIZE` | int | `20` | no | |
| `GONEXT_REDIS_MIN_IDLE_CONNS` | int | `2` | no | |
| `GONEXT_REDIS_DIAL_TIMEOUT` | duration | `5s` | no | |
| `GONEXT_REDIS_READ_TIMEOUT` | duration | `3s` | no | |
| `GONEXT_REDIS_WRITE_TIMEOUT` | duration | `3s` | no | |

### 2.6 Storage (S3-compatible)

| Name | Type | Default | Required | Notes |
|---|---|---|---|---|
| `AWS_ENDPOINT_URL` | string | unset | no | Empty = AWS S3 default endpoint. Set for MinIO/R2/Backblaze. |
| `AWS_REGION` | string | `us-east-1` | no | |
| `GONEXT_S3_BUCKET` | string | `gonext-media` | no | Must exist before first write. |
| `AWS_ACCESS_KEY_ID` | string | unset | no\* | **Secret**. Prefer IAM Role / SSO over static keys when possible. |
| `AWS_SECRET_ACCESS_KEY` | string | unset | no\* | **Secret**. Same. |
| `GONEXT_S3_USE_SSL` | bool | `true` | no | Disable only for local MinIO over plain HTTP. |
| `GONEXT_S3_PATH_STYLE` | bool | `false` | no | Required true for MinIO; false for AWS S3. |

\* Credentials are optional when the process can use ambient AWS identity (Instance Profile, IRSA, AWS SSO). They are required when running on commodity hardware without such ambient identity.

### 2.7 Authentication

| Name | Type | Default | Required | Notes |
|---|---|---|---|---|
| `GONEXT_AUTH_PEPPER` | string | — | **yes** | **Secret**. >=32 bytes (or base64-decodes to >=32). HMAC'd into argon2id input. |
| `GONEXT_AUTH_SESSION_SECRET` | string | — | **yes** | **Secret**. Same length rule. Session cookie signing. |
| `GONEXT_AUTH_CSRF_SECRET` | string | — | **yes** | **Secret**. Same length rule. Public-form CSRF tokens. |
| `GONEXT_AUTH_SESSION_TTL` | duration | `720h` (30d) | no | Cookie absolute lifetime. |
| `GONEXT_AUTH_SESSION_IDLE_TTL` | duration | `168h` (7d) | no | Cookie idle expiration. |

The three required secrets are entropy-checked at boot — a value shorter than the minimum aborts startup with an error that names the key. Rotation, encryption-at-rest, and storage mechanics are in [`docs/13-security-baseline.md`](13-security-baseline.md) §5.

### 2.8 Plugins

| Name | Type | Default | Required | Notes |
|---|---|---|---|---|
| `GONEXT_PLUGINS_DEV_MODE` | bool | `false` | no | Gates registration of `POST /_/plugins/dev/install`. **Production: must remain false.** When false, the route is not registered at all. |
| `GONEXT_PLUGINS_DEV_TOKEN` | string | unset | no | **Secret**. Shared secret for the dev-install handler. Empty + DevMode=true = "reject every request" (deliberate safety floor). |

### 2.9 Performance

| Name | Type | Default | Required | Notes |
|---|---|---|---|---|
| `GONEXT_PERFORMANCE_EARLY_HINTS` | bool | `true` | no | HTTP 103 Early Hints. Disable only if an upstream proxy drops 1xx interim responses. |

### 2.10 RUM (Real User Monitoring)

| Name | Type | Default | Required | Notes |
|---|---|---|---|---|
| `GONEXT_RUM_ENABLED` | bool | `false` | no | Toggles whether the public theme emits the beacon script. The beacon endpoint is always mounted. |
| `GONEXT_RUM_SAMPLE_RATE` | float | `1.0` | no | Per-visitor sample probability in `[0, 1]`. |

### 2.11 Email

`GONEXT_EMAIL_*` keys are canonical; `GONEXT_SMTP_*` keys are fallbacks honored for backwards compatibility with the early-bootstrap env names.

| Name | Fallback | Type | Default | Required | Notes |
|---|---|---|---|---|---|
| `GONEXT_EMAIL_PROVIDER` | — | string | `noop` | no | `smtp` \| `noop` \| `log`. |
| `GONEXT_EMAIL_HOST` | `GONEXT_SMTP_HOST` | string | unset | smtp-only | Required when Provider=smtp. |
| `GONEXT_EMAIL_PORT` | `GONEXT_SMTP_PORT` | int | `587` | no | 587 = STARTTLS submission. 465 = implicit TLS. |
| `GONEXT_EMAIL_USERNAME` | `GONEXT_SMTP_USER` | string | unset | no | Empty disables AUTH. |
| `GONEXT_EMAIL_PASSWORD` | `GONEXT_SMTP_PASSWORD` | string | unset | when USERNAME set | **Secret**. |
| `GONEXT_EMAIL_FROM` | `GONEXT_SMTP_FROM` | string | unset | smtp-only | Envelope sender. |
| `GONEXT_EMAIL_TLS` | — | bool | `false` | no | Implicit TLS on connect (port 465). |
| `GONEXT_EMAIL_AUTH_MECH` | — | string | `plain` | no | `plain` \| `login` \| `crammd5`. |
| `GONEXT_EMAIL_INSECURE_SKIP_VERIFY` | — | bool | `false` | no | **Production: must remain false.** |
| `GONEXT_EMAIL_DIAL_TIMEOUT` | — | duration | `10s` | no | |
| `GONEXT_EMAIL_BRAND_NAME` | — | string | `GoNext` | no | "Welcome to <BrandName>". |
| `GONEXT_EMAIL_BRAND_COLOR` | — | string | `#2563eb` | no | HTML template accent. |
| `GONEXT_EMAIL_SITE_URL` | — | string | unset | no | Footer / welcome sign-in link. |
| `GONEXT_EMAIL_SUPPORT` | — | string | unset | no | Escalation address printed in reset bodies. |

### 2.12 Public site

| Name | Type | Default | Required | Notes |
|---|---|---|---|---|
| `GONEXT_PUBLIC_SITE_BASE_URL` | string | unset | no | Canonical origin for sitemap/feed/robots. No trailing slash; the loader trims one if supplied. Empty disables absolute-URL surfaces. |
| `GONEXT_PUBLIC_SITE_ALLOW_INDEX` | bool | `(Env == production)` | no | When false, robots.txt emits `User-agent: *` / `Disallow: /`. |

---

## 3. Required-in-production cheat sheet

A minimal production env file must define at least these keys:

```sh
GONEXT_ENV=production
DATABASE_URL=postgres://...?sslmode=require
GONEXT_AUTH_PEPPER=<32+ random bytes>
GONEXT_AUTH_SESSION_SECRET=<32+ random bytes>
GONEXT_AUTH_CSRF_SECRET=<32+ random bytes>
```

Strongly recommended on top of the above:

```sh
REDIS_URL=rediss://...                       # TLS to managed Redis
GONEXT_TRUSTED_PROXIES=<CDN/edge CIDRs>
GONEXT_PUBLIC_SITE_BASE_URL=https://example.com
# S3 credentials OR ambient AWS identity (IRSA, Instance Profile)
AWS_ACCESS_KEY_ID=...
AWS_SECRET_ACCESS_KEY=...
GONEXT_S3_BUCKET=<your bucket>
```

Production hardening — these must remain at their safe defaults:

```sh
GONEXT_PLUGINS_DEV_MODE=false
GONEXT_EMAIL_INSECURE_SKIP_VERIFY=false
```

---

## 4. Security & redaction

### 4.1 Redacted fields

The following are masked in every `gonext config dump` output (and any other reflection-based debug surface that consults the same rules):

```
DATABASE_URL              REDIS_URL
AWS_ACCESS_KEY_ID         AWS_SECRET_ACCESS_KEY
GONEXT_AUTH_PEPPER        GONEXT_AUTH_SESSION_SECRET
GONEXT_AUTH_CSRF_SECRET   GONEXT_PLUGINS_DEV_TOKEN
GONEXT_EMAIL_PASSWORD     (and its GONEXT_SMTP_PASSWORD fallback)
```

### 4.2 Redaction mechanics

Implemented in [`packages/go/config/dump.go`](../packages/go/config/dump.go). Two rules combine:

1. **Canonical** — fields tagged `redact:"true"` in [`config.go`](../packages/go/config/config.go) are always masked.
2. **Fallback** — any field whose name matches `(?i)(password|secret|token|key|pepper|dsn)` is masked even without the tag, so a newly added secret field ships with redaction-by-default even when the contributor forgets the struct tag.

The mask format is intentionally informative:

```
GONEXT_AUTH_PEPPER=***REDACTED*** (len=44, sha256[:8]=a3f4c1d2)
```

The length plus the truncated SHA-256 lets an operator verify "I deployed the correct secret" against an expected-hash artifact (stored separately in the deploy pipeline) without ever seeing the plaintext. An empty secret still produces a stable hash (`sha256[:8]=e3b0c442`) so a missing value is visible as "len=0" rather than as a blank line that might be mistaken for fine.

### 4.3 `gonext config dump`

```sh
$ gonext config dump
Auth.CSRFSecret=***REDACTED*** (len=44, sha256[:8]=...)
Auth.Pepper=***REDACTED*** (len=44, sha256[:8]=...)
Auth.SessionIdleTTL=168h0m0s
Auth.SessionSecret=***REDACTED*** (len=44, sha256[:8]=...)
Auth.SessionTTL=720h0m0s
Database.ConnMaxIdleTime=5m0s
Database.ConnMaxLifetime=30m0s
Database.MaxIdleConns=5
Database.MaxOpenConns=25
Database.MigrationDir=./migrations
Database.StatementTimeout=30s
Database.URL=***REDACTED*** (len=84, sha256[:8]=...)
...
```

Output is sorted by key for diff-friendliness. Run with `2>&1 | sort` for the same property over any subset.

### 4.4 Operator hygiene

- Never commit `.env` (it is gitignored).
- Use a secrets manager in production. The `.env.example` defaults are dev-only.
- Rotate the three auth secrets per [`docs/13-security-baseline.md`](13-security-baseline.md) §5.
- After rotating any secret, re-deploy and confirm the new `sha256[:8]` prefix in `config dump` matches the expected-hash in your deploy pipeline.

---

## 5. Deployment patterns

### 5.1 Single-host Docker Compose

`compose.yaml` mounts an `.env` file via `env_file`:

```yaml
services:
  api:
    image: ghcr.io/singleton-solution/gonext:latest
    env_file: .env
    ports: ["8080:8080"]
    depends_on: [postgres, redis]
```

`.env` is a single flat file built from `.env.example`. Suitable for staging or single-tenant production where rotating secrets means restarting the stack.

### 5.2 Kubernetes (ConfigMap + Secret)

Non-secret keys live in a `ConfigMap`; redacted keys live in a `Secret`. Wire both via `envFrom`:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: gonext-config
data:
  GONEXT_ENV: production
  GONEXT_LOG_LEVEL: INFO
  GONEXT_LOG_FORMAT: json
  GONEXT_TRUSTED_PROXIES: "10.0.0.0/8"
  AWS_REGION: us-east-1
  GONEXT_S3_BUCKET: gonext-media-prod
  GONEXT_PUBLIC_SITE_BASE_URL: https://example.com
  GONEXT_PERFORMANCE_EARLY_HINTS: "true"
  # ... etc.
---
apiVersion: v1
kind: Secret
metadata:
  name: gonext-secrets
type: Opaque
stringData:
  DATABASE_URL: postgres://...
  REDIS_URL: rediss://...
  GONEXT_AUTH_PEPPER: <random>
  GONEXT_AUTH_SESSION_SECRET: <random>
  GONEXT_AUTH_CSRF_SECRET: <random>
  AWS_ACCESS_KEY_ID: <if not using IRSA>
  AWS_SECRET_ACCESS_KEY: <if not using IRSA>
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: gonext-api
spec:
  template:
    spec:
      containers:
        - name: api
          image: ghcr.io/singleton-solution/gonext:v1.0.0
          envFrom:
            - configMapRef: { name: gonext-config }
            - secretRef:    { name: gonext-secrets }
```

Prefer **IRSA** (IAM Roles for Service Accounts) on EKS over static AWS credentials — drop `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` from the Secret and attach the role to the Deployment's service account.

### 5.3 systemd

A unit file pointing at an EnvironmentFile keeps the process clean and the secrets root-readable only:

```ini
[Unit]
Description=GoNext API
After=network-online.target postgresql.service redis.service

[Service]
Type=notify
User=gonext
EnvironmentFile=/etc/gonext/gonext.env
ExecStart=/usr/local/bin/gonext serve
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=multi-user.target
```

```sh
$ sudo install -m 600 -o gonext -g gonext .env /etc/gonext/gonext.env
$ sudo systemctl enable --now gonext-api.service
```

---

## 6. Migration guide

This document is the canonical environment surface as of the current release. Per-version diffs land here as they happen.

### Unreleased

- No breaking changes. New env vars are additive.

<!--
TEMPLATE for future entries:

### vX.Y.Z (YYYY-MM-DD)

#### Added
- `GONEXT_FOO_BAR` (bool, default false) — short description; see PR #NNNN.

#### Renamed
- `OLD_NAME` -> `NEW_NAME`. Old key still honored as a fallback through vX.(Y+2).

#### Removed
- `GONEXT_DEPRECATED_KEY` — replaced by `GONEXT_NEW_KEY` in vX.Y-2. Setting the
  old key now logs a warning; the value is ignored.

#### Default change
- `GONEXT_FOO` default flipped false -> true. To restore the prior behavior set
  `GONEXT_FOO=false` explicitly.
-->
