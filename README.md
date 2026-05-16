# GoNext

A modern, modular content management platform. WordPress's ecosystem promise, built on Go + Next.js + Postgres, with a sandboxed plugin runtime so you can actually trust what you install.

> **Status**: Pre-development. Design phase complete. ~150 implementation tasks are being filed as GitHub issues. Contributors welcome.

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

## Why it exists

WordPress is the most-used CMS in the world, and also the most-exploited. Plugin security holes are the #1 reason commercial sites get breached. GoNext is built on the bet that a sandboxed plugin runtime + signed marketplace + modern stack solves the trust problem WordPress can't.

## Design documents

All architectural decisions are documented in [`/docs`](./docs):

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

Proposals for all open questions live in [`/docs/proposals`](./docs/proposals).
Architecture Decision Records in [`/adr`](./adr).

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
