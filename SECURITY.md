# Security Policy

GoNext takes the security of our software, our users, and the researchers who report issues to us seriously. This document is the canonical entry point for reporting vulnerabilities.

For the full programmatic policy, threat model, and hardening defaults see [`docs/15-security-policy.md`](docs/15-security-policy.md). For payouts and bug-bounty terms see [`docs/16-bug-bounty.md`](docs/16-bug-bounty.md). For the RFC 9116 machine-readable summary see [`/.well-known/security.txt`](.well-known/security.txt).

## Reporting a vulnerability

**Do not file public GitHub issues for security vulnerabilities.** Public issues are visible to everyone and rob us of the time we need to ship a fix before the issue is weaponized.

Report privately via either of:

1. **Preferred — GitHub Security Advisory**: open a [private security advisory](https://github.com/Singleton-Solution/GoNext/security/advisories/new). GitHub keeps the report confidential between you and our maintainers.
2. **Email**: <security@gonext.io>. Encrypt sensitive details with our PGP key (fingerprint placeholder, to be replaced before v1.0): `0000 1111 2222 3333 4444  5555 6666 7777 8888 9999`. The public key block will be published at <https://gonext.io/.well-known/pgp-key.txt> ahead of the v1.0 release.

Subject line for email: `[SECURITY] GoNext - <short description>`.

Please include:

- Description of the vulnerability.
- Steps to reproduce.
- Affected versions / git SHAs / commit ranges.
- Impact assessment (what an attacker can do, with what privileges).
- Suggested fix (if known).
- Whether you wish to be credited, and the handle to use.

## Response SLAs

We commit to the following timelines. They are measured in business hours and start when a maintainer first acknowledges receipt; we triage 24/7 best-effort but the official clock honors timezones.

| Window | What we do |
|---|---|
| Within **24 hours** | Acknowledge receipt and assign a tracking ID. |
| Within **7 days** | Initial severity assessment, scope confirmation, and a triage decision (accepted / needs-info / out-of-scope). |
| **30 days** (Critical), **60 days** (High), **90 days** (Medium / Low) | Fix released or — for genuinely hard issues — a written mitigation plan with a revised target date. |
| Day **90** | Coordinated disclosure by default (Google Project Zero rule). We may extend by mutual agreement; we will not unilaterally extend past 120 days. |

If you do not hear back within 24 hours, please re-send. Mail can be lost. Tag the email `[REMINDER]` and we will move it to the top of the queue.

## Supported versions

GoNext is currently pre-1.0. Once v1.0 ships:

| Version | Status | Security fixes |
|---|---|---|
| Latest minor (e.g. `1.x`) | Active | Yes |
| Previous minor | Maintenance | Critical + High only, for **12 months** after the next minor's release |
| Anything older | Unsupported | No |

Until v1.0, only the tip of `main` is supported. We will not backport fixes to pre-release tags.

## Scope

### In scope

- The Go API server (`apps/api`), public Next.js renderer (`apps/web`), admin app (`apps/admin`), worker (`apps/worker`).
- The plugin host: WASM sandbox isolation, capability ABI, signing pipeline.
- First-party plugins under `plugins/` once they exist.
- First-party themes under `themes/` once they exist.
- Migration importers (`apps/api/internal/importer/...`): parsing, SSRF, injection, deserialization.
- Cryptographic choices: argon2id (passwords), AES-256-GCM (secrets), ed25519 (plugin signing), HMAC-SHA256 (webhooks).
- Container images we publish (`ghcr.io/singleton-solution/gonext-*`).
- The GoNext-controlled marketing site at `gonext.io` and its `.well-known/` path.

### Out of scope

- Third-party plugins or themes not built by us — report to their maintainers.
- Self-hosted installs running with deliberately weakened configuration (e.g. disabled CSP, debug mode, `DEBUG_ALLOW_UNSAFE_INLINE=1`).
- Pre-v0.1 development branches; experimental branches prefixed `wip/` or `spike/`.
- Reports based solely on missing best-practice headers without a concrete attack path.
- Denial-of-service via volumetric traffic. We delegate L3/L4 DoS to the edge provider.
- Social engineering, physical attacks, or attacks requiring physical access to a maintainer's machine.
- Issues in dependencies that have not yet been disclosed upstream. Report those to the upstream first; we will track the advisory.
- Vulnerabilities requiring a compromised admin account, unless they break privilege separation between admins and the OS or break tenant isolation.

## Safe harbor

We will not pursue or support legal action against researchers who:

1. Make a good-faith effort to comply with this policy.
2. Report vulnerabilities promptly.
3. Avoid violating the privacy of others, destroying or modifying data, or degrading service for our users.
4. Do not exploit a vulnerability beyond the minimum necessary to confirm its existence.
5. Do not publicly disclose the issue before we have had a reasonable opportunity to fix it, per the SLAs above.

If your research is consistent with this policy, we consider it **authorized**. We will work with you to understand and resolve the issue quickly, and will not initiate or recommend legal action related to your research.

If at any time you have concerns or are uncertain whether your research is consistent with this policy, please email <security@gonext.io> before going further.

## Recognition

- Credit in the security advisory, CHANGELOG, and release notes (or anonymity, if you prefer).
- Listing in `SECURITY-HALL-OF-FAME.md` (created when the first valid report lands).
- Cash bounty per the tier table in [`docs/16-bug-bounty.md`](docs/16-bug-bounty.md) — Critical $5,000 / High $1,000 / Medium $250 / Low $50.

## Crypto

We use:

- **argon2id** for password hashing per [`docs/06-auth-permissions.md`](docs/06-auth-permissions.md).
- **AES-256-GCM** for at-rest secret encryption per [`docs/13-security-baseline.md`](docs/13-security-baseline.md).
- **ed25519** for plugin signing per [`docs/02-plugin-system.md`](docs/02-plugin-system.md).
- **HMAC-SHA256** for webhook signing.

Misconfigurations, weak choices, or implementation bugs in any of the above are in scope.
