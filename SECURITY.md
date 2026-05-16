# Security Policy

## Supported versions

GoNext is pre-1.0. There is no supported version yet. Once v1.0 ships, security fixes will be backported to the current stable release for 12 months.

## Reporting a vulnerability

**Do not file public issues for security vulnerabilities.** Public issues are visible to everyone; we need time to fix before disclosure.

Report privately:

1. **Preferred**: open a private security advisory via GitHub's [Security advisories tab](https://github.com/Singleton-Solution/GoNext/security/advisories/new). This is the fastest path.
2. **Alternative**: email [tayeb.mokni@gmail.com](mailto:tayeb.mokni@gmail.com) with the subject line `[SECURITY] GoNext - <short description>`.

Please include:
- Description of the vulnerability.
- Steps to reproduce.
- Affected versions / git SHAs.
- Impact assessment.
- Suggested fix (if known).

## What happens next

| Time | What we do |
|---|---|
| ≤ 48 hours | Acknowledge receipt. |
| ≤ 7 days | Triage. Confirm severity. Reach back with assessment. |
| ≤ 30 days for critical, ≤ 90 days for high/medium | Fix, test, prepare advisory. |
| Coordinated disclosure | Patch released, advisory published, CVE requested if applicable. |

These SLAs are tracked in [docs/13-security-baseline.md §13](./docs/13-security-baseline.md).

## What we ask of you

- Give us reasonable time to fix before public disclosure (we follow 90-day default per Google Project Zero guidelines).
- Do not exploit the vulnerability beyond what's needed to demonstrate it.
- Do not access data that isn't yours.
- Do not test against production sites that aren't yours.
- Test against a local install or your own staging environment.

## What we offer

- Credit in the security advisory and release notes (or anonymity if you prefer).
- Listing in `SECURITY-HALL-OF-FAME.md` once it exists.
- Cash bounty: not yet. Bug bounty program planned for post-v1.0 per [proposal Q13-1](./docs/proposals/14-proposals-ops-sec.md). Until then, contributions are accepted in good faith.

## Scope

In scope:
- The Go server, admin app, public app, workers, importers.
- The plugin host (WASM sandbox isolation, capability enforcement).
- The first-party plugins under [`plugins/`](./plugins/) once they exist.
- The first-party themes under [`themes/`](./themes/) once they exist.
- Migration importers (parsing, SSRF, injection).

Out of scope:
- Third-party plugins not built by us. Report to the plugin author.
- Third-party themes not built by us. Same.
- Self-hosted installs with non-default configurations (e.g., disabled CSP).
- Pre-v0.1 development branches.

## Hall of fame

(Will be populated as reports come in.)

## Crypto

We use:
- argon2id for password hashing per [doc 06 §3](./docs/06-auth-permissions.md).
- AES-256-GCM for at-rest secret encryption per [doc 13 §5](./docs/13-security-baseline.md).
- ed25519 for plugin signing per [doc 02 §10](./docs/02-plugin-system.md).
- HMAC-SHA256 for webhook signing.

If you find a misconfiguration, weak choice, or implementation bug in any of the above, that's in scope.
