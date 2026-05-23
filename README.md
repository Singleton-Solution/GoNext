# GoNext

A modern, modular content management platform. WordPress's ecosystem promise, built on Go + Next.js + Postgres, with a sandboxed plugin runtime so you can actually trust what you install.

> **Status**: Pre-1.0. ~137 PRs landed; the platform boots, serves a themed page, and now ships a first-run setup wizard. Contributors welcome.

## What this is

- **Backend**: a single Go binary (HTTP server + WebAssembly plugin host + background workers).
- **Frontend**: Next.js for the public site (SSR/SSG/ISR) and a separate Next.js app for the admin.
- **Storage**: PostgreSQL + Redis + S3-compatible object storage.
- **Plugins**: WebAssembly modules with a capability-based ABI. Memory-isolated. Signed.
- **Themes**: React component packages. Both classic (code-defined templates) and block themes (full-site editing).

## What this is NOT

- Not a WordPress fork. Not PHP. Will not run WordPress plugins. **Does** provide tools to import WordPress content.
- Not a headless-only CMS. The admin and editor are first-class.
- Not feature parity with every WordPress feature. We aim at the 95% of common use cases, done well.

## Quickstart

You have two paths to a working install. Pick one.

### Path A — Browser (the WordPress route)

The fastest way to a usable site. Mirrors WordPress's `/wp-admin/install.php`.

```sh
# 1. Bring up Postgres + Redis + MinIO.
make up

# 2. Apply migrations (one shot — idempotent on subsequent runs).
make build-go && ./apps/api/bin/gonext migrate up

# 3. Start the API and admin.
#    In two terminals (or use your preferred process manager):
go run ./apps/api/cmd/server                             # API on :8080
pnpm --filter @gonext/admin dev                          # Admin on :3001
```

Then open **http://localhost:3001/setup** in your browser. The setup wizard walks you through:

1. Welcome + system check
2. Administrator email + password (≥12 characters, enforced server-side)
3. Site name + URL
4. Review + confirm
5. Auto-redirect into the admin dashboard, already logged in

After the wizard succeeds the `/setup` route is permanently locked — every endpoint under `/api/v1/setup/*` returns `423 Locked` and the admin middleware stops redirecting to it. You can re-open the install window only by dropping the `core.site.installation_completed_at` row from the `options` table (psql, out-of-band).

> *Screenshot of the wizard goes here once the design system lands.*

### Path B — CLI (the scripted route)

For deployments where the admin UI isn't reachable from the operator's workstation (CI, Kubernetes init container, air-gapped bootstrap):

```sh
make up
./apps/api/bin/gonext migrate up
./apps/api/bin/gonext init \
    --admin-email=admin@example.com \
    --admin-password='correct-horse-battery-staple' \
    --site-name='Acme CMS' \
    --site-url=https://acme.example.com
```

The CLI hits the same `POST /api/v1/setup/install` endpoint the wizard uses, so the lock behavior is identical — a second invocation returns the same `423 already_installed` code.

> The `gonext init` subcommand is scaffolded but not yet shipped; track its delivery in [issue #TBD]. Until then, use Path A.

## Where to go next

- `docs/00-architecture-overview.md` — the foundation. Read first.
- `docs/06-auth-permissions.md` — argon2id, sessions, roles, the setup wizard's security model.
- `docs/09-deployment-ops.md` — Docker, Kubernetes, env vars, multi-region.
- `docs/13-security-baseline.md` — CSP, secret handling, supply-chain posture.
- `docs/11-testing-ci.md` — running the test pyramid locally.

Proposals for all open questions live in [`/docs/proposals`](./docs/proposals).
Architecture Decision Records in [`/adr`](./adr).

## Quickstart

```sh
# 1. Copy the sample env file and edit the secrets.
cp .env.example .env

# 2. Generate the three required auth secrets.
openssl rand -base64 32   # paste into GONEXT_AUTH_PEPPER
openssl rand -base64 32   # paste into GONEXT_AUTH_SESSION_SECRET
openssl rand -base64 32   # paste into GONEXT_AUTH_CSRF_SECRET

# 3. Bring up Postgres + Redis + MinIO and the API.
docker compose up
```

Every environment variable the API reads is documented in [`.env.example`](.env.example) with default, type, and security notes. For the prose reference (per-section tables, redaction rules, K8s / systemd deployment shapes), see [`docs/17-environment.md`](docs/17-environment.md).

## Design documents

| # | Document | What it covers |
|---|---|---|
| 00 | [Architecture Overview](docs/00-architecture-overview.md) | Foundation. Read first. |
| 01 | [Core CMS & Data Model](docs/01-core-cms.md) | Content types, taxonomies, Postgres schema |
| 02 | [Plugin System](docs/02-plugin-system.md) | WASM runtime, hook bus, capability ABI |
| 03 | [Theme System](docs/03-theme-system.md) | Template hierarchy, theme.json, FSE |
| 04 | [Block Editor](docs/04-block-editor.md) | JSON block tree, editor UX |
| 05 | [Admin & API](docs/05-admin-api.md) | Admin UI, REST + GraphQL |
| 06 | [Auth & Permissions](docs/06-auth-permissions.md) | Argon2id, sessions, roles & capabilities |
| 07 | [Media & Performance](docs/07-media-performance.md) | Upload pipeline, cache layers, ISR |
| 08 | [Migration & WP Compat](docs/08-migration-compat.md) | WordPress importers, REST shim |
| 09 | [Deployment & Ops](docs/09-deployment-ops.md) | Docker, K8s, env, multi-region |
| 10 | [Observability](docs/10-observability.md) | Logs, metrics, traces, RUM |
| 11 | [Testing & CI](docs/11-testing-ci.md) | Test pyramid, contract tests, CI |
| 12 | [Jobs & Cron](docs/12-jobs-cron.md) | Asynq queues, retries, leader election |
| 13 | [Security Baseline](docs/13-security-baseline.md) | Headers, CSP, secrets, supply chain |
| 17 | [Environment & Configuration](docs/17-environment.md) | Every env var the loader reads — type, default, deployment patterns |

Proposals for all open questions live in [`/docs/proposals`](./docs/proposals).
Architecture Decision Records in [`/adr`](./adr).
| 15 | [Security Policy](docs/15-security-policy.md) | Vulnerability disclosure, SLA |
| 16 | [Bug Bounty](docs/16-bug-bounty.md) | Scope, rewards |

## Roadmap

Six phases, ~24 months to v1 with two engineers. See [ROADMAP.md](./ROADMAP.md) for detail.

| Phase | Scope | Milestone |
|---|---|---|
| P0 | Skeleton | Go server, schema, basic auth, one rendered page |
| P1 | CMS Core | Posts/pages/CPTs, taxonomies, media, admin CRUD |
| P2 | Editor | Block editor with ~20 core blocks |
| P3 | Themes | Template hierarchy, customizer, 1-2 reference themes |
| P4 | Plugins | WASM runtime, SDK, 3 reference plugins |
| P5 | Migration | WordPress importer, REST compat |
| P6 | Polish | Performance, docs, launch |

## Contributing

We need help. See [CONTRIBUTING.md](./CONTRIBUTING.md) for how to pick up an issue.

- Browse [open issues](https://github.com/Singleton-Solution/GoNext/issues) filtered by `area:*`, `skill:*`, or `good-first-issue`.
- Read [CODE_OF_CONDUCT.md](./CODE_OF_CONDUCT.md).
- Sign off your commits with `git commit -s` (the [DCO](https://developercertificate.org/) check enforces this on PRs — see [CONTRIBUTING.md](./CONTRIBUTING.md#dco-sign-off)).

## License

License is being finalized — see [`LICENSE`](./LICENSE) and the rationale in [`docs/proposals/14-proposals-strategic.md`](./docs/proposals/14-proposals-strategic.md) §S2.

Current direction: **core under FSL-1.1-Apache-2.0** (source-available, converts to Apache 2.0 after 2 years per file) with the **plugin SDK under Apache 2.0** from day 1. Contributors sign off commits via the [DCO](https://developercertificate.org/) (no CLA). See [`adr/0001-licensing.md`](./adr/0001-licensing.md) and [`adr/0002-dco-requirement.md`](./adr/0002-dco-requirement.md).

## Security

Report vulnerabilities privately per [SECURITY.md](./SECURITY.md). Do not file public issues for security reports.

## Maintainer

Currently maintained by [@tayebmokni](https://github.com/tayebmokni) under [Singleton-Solution](https://github.com/Singleton-Solution). Project governance will transition to a maintainer team as the contributor base grows; see [GOVERNANCE.md](./GOVERNANCE.md) (coming).
