# 15 — Security Policy

> Owns: the project's public security policy. Disclosure path, threat model summary, hardening defaults shipped out of the box, audit log requirements, expectations of plugin authors, breach recovery runbook.
>
> The deep technical baseline (headers, CSP, supply chain, secret tiers, SSRF guard) lives in [`13-security-baseline.md`](13-security-baseline.md). This document is the public-facing complement — the version a security researcher, an auditor, or a regulator reads first.

---

## 0. Scope and audience

| Reader | They want |
|---|---|
| Security researcher | How to report, what's in scope, what they'll get paid. |
| Compliance auditor | Threat model, hardening defaults, audit trail commitments. |
| Plugin author | What we expect them to do so a plugin doesn't become the breach vector. |
| Site operator | What to do in the first 24 hours after a confirmed breach. |
| New contributor | Why the codebase is shaped the way it is. |

If you are reporting a vulnerability, jump to [`/SECURITY.md`](../SECURITY.md). If you want a bounty, see [`16-bug-bounty.md`](16-bug-bounty.md).

---

## 1. Reporting path (canonical)

The full disclosure flow is documented in [`/SECURITY.md`](../SECURITY.md). The short version:

1. **Open a [private GitHub Security Advisory](https://github.com/Singleton-Solution/GoNext/security/advisories/new)** — preferred path.
2. Or email <security@gonext.io>, PGP fingerprint `0000 1111 2222 3333 4444  5555 6666 7777 8888 9999` (placeholder, replaced before v1.0).
3. We acknowledge within **24 hours**, give an initial assessment within **7 days**, and target coordinated disclosure at **90 days**.

The RFC 9116 machine-readable summary is served at `https://gonext.io/.well-known/security.txt` and is mirrored in the repository at [`/.well-known/security.txt`](../.well-known/security.txt).

---

## 2. Threat model summary

This is the short version. The canonical model is in [`13-security-baseline.md §1`](13-security-baseline.md). Below is the one-screen version a non-engineer can read.

### 2.1 Who we worry about

| # | Attacker | Position | Headline risk |
|---|---|---|---|
| T1 | Unauthenticated internet | Public HTTP, no creds | Web app vulns, credential stuffing, abusive scraping |
| T2 | Authenticated low-privilege (commenter) | Valid session, low caps | Stored XSS, SSRF via profile fields |
| T3 | Author/editor | Content caps | Stored XSS in posts, `unfiltered_html` abuse |
| T4 | Malicious plugin author | Code installed by admin | Sandbox escape, data exfil via host ABI |
| T5 | Malicious theme author | Runs in the Next.js Node process | Arbitrary JS in render path |
| T6 | Insider with admin access | Legitimate creds | Data exfil, audit erasure |
| T7 | Supply chain | Compromised dependency or build pipeline | Implanted code in core / plugin / theme / dep |
| T8 | Network adversary | On-path or DNS | Session theft, downgrade, rebinding |
| T9 | Compromised browser/extension on admin | Authenticated browser is hostile | Token theft, action-on-behalf |

### 2.2 Out of scope

- DDoS at L3/L4 — delegated to the edge provider.
- Physical access to hosts — operator responsibility.
- Side-channel attacks on the host kernel — not in v1.
- 100% PII-free logs — we redact aggressively but treat logs as a sensitive store.

---

## 3. Hardening defaults the project ships with

GoNext is **secure by default**. Operators have to actively turn things off to make the install unsafe, not turn things on to make it safe.

### 3.1 HTTP

- HSTS `max-age=63072000; includeSubDomains; preload` on every public response.
- TLS 1.3 only on managed deployments; TLS 1.2 with mandated cipher list on self-hosted (`pkg/security/tls`).
- Frame-Options `DENY`, Referrer-Policy `strict-origin-when-cross-origin`, X-Content-Type-Options `nosniff`, Cross-Origin-Opener-Policy `same-origin`.
- A strict default CSP. The admin app's CSP forbids `unsafe-inline` and `unsafe-eval`; the public app's CSP scopes by theme bundle hash.
- Per-route rate limits with a 429 ceiling visible to operators in metrics.

### 3.2 Authentication

- argon2id with project-wide pepper. Parameters reviewed annually against the OWASP cheat sheet.
- Passkeys (WebAuthn) as the recommended default for admin accounts. TOTP is supported but deprecated for new installs.
- Sessions are server-side, opaque, and short-lived. Tokens are never stored in `localStorage`.
- Step-up authentication is required for: plugin installs, theme installs, role changes, secret rotation, audit log export, and account deletion.

### 3.3 Plugin sandbox

- Plugins run inside `wazero`-hosted WebAssembly with a capability-gated host ABI.
- No ambient authority. Every host call (`db.query`, `cache.invalidate`, `http.outbound`, `audit.emit`, `secret.read`) is gated by an explicit capability in the plugin manifest, granted by an admin at install time.
- The plugin signing pipeline rejects unsigned plugins by default. `policy.plugins.allow_unsigned` must be flipped on per-install and is audit-logged.

### 3.4 Theme trust model

- Themes run inside the Next.js Node process. They are *not* sandboxed. This is documented; the trade-off is performance and full server-component access.
- Themes are signed with ed25519 keys. Unsigned themes require explicit admin opt-in at install time, identical to the plugin path.
- An install-time review checklist lives in [`docs/03-theme-system.md`](03-theme-system.md).

### 3.5 Secrets

- Three-tier secret store: process env (boot-only), runtime secrets (KMS-encrypted), per-tenant secrets (KMS + tenant key).
- Secrets are *opaque* to plugins. Plugins ask for capability `secret.read("<name>")` and the host returns a stub that proxies the actual call; the plugin never sees the secret's bytes.
- AES-256-GCM for at-rest secret encryption. AAD includes the tenant ID.

### 3.6 Audit log

- Append-only, tamper-evident via per-row HMAC chain (each row's HMAC includes the previous row's HMAC).
- Mandatory events: any admin action, any secret read, any capability grant, any plugin install / enable / disable / uninstall, any theme install, any user role change.
- Exportable as a signed JSONL bundle. Export action is itself audit-logged.
- Retained for **365 days** by default; configurable up to 7 years.

### 3.7 Supply chain

- SBOM published with every release (CycloneDX format).
- Container images signed with Sigstore (`cosign`); signatures verifiable from the public Rekor log.
- Dependency pins use digest, not floating tag, for everything inside our build images.
- Dependabot + `govulncheck` + `trivy` run on every PR; the dependency-vuln workflow blocks merges on critical / high findings.

---

## 4. Audit log requirements (normative)

These rules apply to anyone shipping code that runs inside the GoNext server, including plugin authors who use the `audit.emit` capability.

1. **An admin action without an audit entry is a bug.** If you build a new admin endpoint, the PR must include the audit emission.
2. The audit log is the **source of truth for what happened**. Do not derive after-the-fact narratives from access logs alone.
3. Plugin authors using `audit.emit`:
   - MUST emit *before* mutating state, not after.
   - MUST include the actor's user ID (the host injects this; do not synthesize).
   - MUST NOT emit fake events under another plugin's namespace; the host stamps the plugin ID.
   - SHOULD emit a corresponding `*.completed` or `*.failed` event after the action resolves.
4. Operators MUST configure off-host log shipping (S3 + KMS, Loki, etc.) before going to production. The local audit table is best-effort durable but is not the system of record.
5. The audit-log retention period MUST be longer than the bug-bounty disclosure window (currently 365 days vs 90 days).

---

## 5. Expectations of plugin authors

If you publish a plugin to the GoNext marketplace (or even sideload one for a single tenant), you accept the following:

### 5.1 What you MUST do

1. **Request only the capabilities you use.** Reviewers reject over-broad manifests. The marketplace surfaces a "capability scope" badge to end users.
2. **Sign your plugin.** Unsigned plugins do not appear in the marketplace and require admin opt-in to sideload.
3. **Validate every input that crosses the host ABI boundary.** Treat host responses as untrusted too — a malicious peer plugin or a compromised host returns data through that boundary.
4. **Emit audit events for any mutation a user would expect to be logged.** See §4.
5. **Disclose vulnerabilities in your own plugin via the path in `/SECURITY.md`.** We will coordinate disclosure with you.

### 5.2 What you MUST NOT do

1. Do not ship a plugin that requests `http.outbound` without declaring the destination domains in the manifest. The host enforces an allow-list.
2. Do not use `eval`-equivalent constructs available in your SDK language (e.g. Rust's `proc_macro` at runtime, Go reflection to call un-exported host functions). The sandbox will trap these but they signal abusive intent.
3. Do not store secrets in plugin-owned tables. Use `secret.read` with a capability.
4. Do not bundle a copy of a host-provided library to bypass the version pin. The host wins.
5. Do not crawl other tenants. Cross-tenant requests are denied by the host; attempting them repeatedly is grounds for delisting.

### 5.3 What you SHOULD do

- Run the `gonext plugin lint` and `gonext plugin contract-test` commands before publishing. They catch ~80% of the issues we'd otherwise have to flag in review.
- Subscribe to the GoNext Security mailing list (publication target: v1.0).
- Provide a `security.txt` of your own in your plugin's repository.

---

## 6. Recovery steps after a confirmed breach

This is the **first-24-hours runbook** for site operators. Doc 09 (Deployment & Ops) owns the full incident-response process; this is the security-flavored slice.

### 6.1 Hour 0 — Contain

1. **Rotate the admin pepper, session signing key, and webhook HMAC key.** All sessions invalidate on next request. Webhook subscribers reject invalid signatures and re-handshake.
2. **Disable all third-party plugins.** `gonext plugin disable --all-third-party --reason="incident-<id>"`. Audit-logged.
3. **Disable third-party themes.** Force-fall-back to the bundled default theme.
4. **Rotate the database password and the S3 / object-storage credentials.** Keep the old credentials valid for a 15-minute overlap; do not break in-flight migrations.
5. **Take a forensic snapshot.** Volume snapshot or `pg_dump` to a write-once bucket *before* doing anything else destructive.

### 6.2 Hour 0–4 — Assess

1. Pull the audit log for the suspected actor / window. Look for: secret reads, plugin installs, role changes, mass content edits, audit-export attempts.
2. Diff `posts.content_blocks` against the most recent revision for changed rows. Block-editor inserts of `core/html` with external script tags are the canonical signal.
3. Run `gonext security scan --since=<incident-start>` — checks plugin install diffs, theme install diffs, capability grants, and admin user changes.
4. Check the `outbound_http_audit` table for unusual destinations. Plugins that suddenly start talking to a new domain are suspect.

### 6.3 Hour 4–24 — Eradicate and notify

1. Restore from the most recent **pre-incident** snapshot, or apply targeted rollbacks if the blast radius is small enough.
2. If user data was exfiltrated:
   - Notify affected users via the contact email on file.
   - File the breach with the relevant data-protection authority within statutory windows (GDPR: 72 hours from awareness).
   - Open a public incident retrospective issue *after* containment and remediation.
3. File a private security advisory if the root cause is a vulnerability in GoNext itself. Coordinated disclosure rules apply.
4. Replace any leaked secret material end-to-end. Treat anything the plugin sandbox could have touched as leaked unless you can prove otherwise from the audit log.

### 6.4 Beyond 24 hours — Learn

1. Publish a blameless retrospective. Template lives at [`docs/_audit/RETRO-TEMPLATE.md`](_audit/RETRO-TEMPLATE.md) (added with this policy).
2. Open issues for every gap identified.
3. If the breach was caused by an over-broad capability grant, update the marketplace review checklist accordingly.
4. Update this document. The threat model is a living artifact.

---

## 7. Policy review cadence

- This document is reviewed **quarterly** by the project's security lead.
- The threat model is reviewed **annually**, and after any incident.
- The bounty tier amounts ([`16-bug-bounty.md`](16-bug-bounty.md)) are reviewed annually and any time the project's funding posture materially changes.

Material policy changes are announced via:

- A pinned issue in the repository.
- A post on the GoNext blog (post-launch).
- A diff in this document, signed off via the standard PR review process.

---

## 8. Related documents

- [`/SECURITY.md`](../SECURITY.md) — disclosure entry point.
- [`/docs/13-security-baseline.md`](13-security-baseline.md) — technical baseline (headers, CSP, secrets, supply chain).
- [`/docs/16-bug-bounty.md`](16-bug-bounty.md) — bounty tiers and payout terms.
- [`/docs/02-plugin-system.md`](02-plugin-system.md) — WASM sandbox, capability ABI, signing.
- [`/docs/06-auth-permissions.md`](06-auth-permissions.md) — passwords, sessions, MFA, RBAC.
- [`/docs/09-deployment-ops.md`](09-deployment-ops.md) — full incident response process.
- [`/.well-known/security.txt`](../.well-known/security.txt) — RFC 9116 summary.
