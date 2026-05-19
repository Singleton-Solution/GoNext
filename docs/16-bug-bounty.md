# 16 — Bug Bounty Program

> Owns: the bounty side of the security disclosure program. Tiers, payouts, the report template, examples of valid and invalid reports. The general disclosure path is owned by [`/SECURITY.md`](../SECURITY.md); the threat model and hardening defaults are owned by [`15-security-policy.md`](15-security-policy.md).

The bounty program is **scaffolding** as of this document. Payouts start the day v1.0 ships. Until then, every report still receives full triage, credit, and Hall-of-Fame listing — we just cannot wire money yet.

---

## 0. At a glance

| Severity | Payout (USD) | Examples |
|---|---:|---|
| **Critical** | **$5,000** | RCE in `apps/api`, sandbox escape from a WASM plugin, full admin auth bypass, persistent SQL injection in core. |
| **High** | **$1,000** | Privilege escalation between roles, server-side SSRF that reaches an internal service, stored XSS in admin with admin context, secret-store ACL bypass. |
| **Medium** | **$250** | Reflected XSS in admin, CSRF on a destructive admin endpoint, SSRF blocked at the firewall, audit-log tampering without detection. |
| **Low** | **$50** | Information disclosure of non-secret data, missing security header with a concrete exploitation chain, rate-limit bypass on a non-destructive endpoint. |

We may pay above the tier when a report includes a working patch, a clean reproduction, and a useful disclosure document — up to **+50%**. We will not pay below the tier; if we accept the report, you get at least the listed amount.

---

## 1. Scope

The bounty scope is identical to the technical scope in [`/SECURITY.md`](../SECURITY.md). The short version:

| In scope | Out of scope |
|---|---|
| `apps/api`, `apps/web`, `apps/admin`, `apps/worker` | Third-party plugins or themes |
| Plugin WASM sandbox + capability ABI | Self-hosted installs with deliberately weakened config |
| First-party plugins under `plugins/` | DoS via volumetric traffic |
| First-party themes under `themes/` | Social engineering, physical attacks |
| Migration importers | Issues already public for >30 days |
| Cryptographic choices and implementations | Best-practice header complaints without an attack path |
| Container images we publish | Reports generated solely by automated scanners |
| `gonext.io` marketing site and its `.well-known/` paths | Spam, missing SPF/DKIM on transactional mail |

If you are unsure whether something is in scope, **ask first**: <security@gonext.io>. We would much rather have a 30-second email exchange than a 30-day argument over a denied report.

---

## 2. Eligibility

You may submit a report and receive a bounty if:

1. You are the **first reporter** of the specific issue (we time-stamp on receipt).
2. You comply with the safe-harbor language in [`/SECURITY.md`](../SECURITY.md).
3. You are **not** a current or former employee of the company maintaining GoNext within the last 12 months.
4. You are **not** in a jurisdiction that prohibits us from sending you money under applicable export control or sanctions rules. We use Stripe Connect or Wise; if neither can pay your country, we will offer the equivalent in store credit or a charitable donation in your name.
5. You are **not** a minor (legal age in your jurisdiction). If you are, a parent or guardian must accept the payout on your behalf.

We do not require you to sign an NDA. We do ask that you delay public disclosure until we have shipped a fix or hit the 90-day default deadline, whichever is sooner.

---

## 3. The report template

Copy this into your private advisory or email. The more of this you complete, the faster we can triage.

```text
Title: <one-line summary>

Severity (your assessment): Critical / High / Medium / Low
Affected component: e.g. apps/api/internal/handler/posts
Affected versions: e.g. v0.4.0..main, commit abc1234

--- Summary ---
<2-3 sentence description of the issue>

--- Reproduction ---
1. <step>
2. <step>
3. <step>

Test environment: e.g. local docker-compose, fresh seed data
Auth context required: none / valid commenter / valid author / valid admin

--- Impact ---
<What can an attacker do? With what privileges? Against which data?>
<Cite specific tables, endpoints, or capabilities where relevant.>

--- Suggested fix ---
<If you have one. Skip if not.>

--- Disclosure preferences ---
Credit handle: <name or anonymous>
Public timing preference: <ASAP after fix / 30 days / 90 days / coordinated>
Payout currency / method: <USD via Stripe / EUR via Wise / store credit / donation>
```

A complete report **roughly doubles** your odds of a +50% modifier.

---

## 4. Severity rubric

We use a CVSS-influenced rubric but reserve the final call for the security lead. The published tier is the **paid** tier; CVSS is a guide, not a contract.

### Critical (CVSS 9.0–10.0 typical)

- Remote code execution on `apps/api`, `apps/web`, `apps/admin`, or `apps/worker` without authentication.
- Full authentication bypass for admin accounts.
- Sandbox escape from a WASM plugin to the host process.
- Mass data exfiltration via a single request.
- Persistent SQL injection with read+write to arbitrary tables.

### High (CVSS 7.0–8.9 typical)

- Authenticated RCE.
- Privilege escalation between defined roles.
- SSRF to an internal service from the public surface.
- Stored XSS in admin executing in admin context.
- Capability ABI bypass that grants a plugin a capability it did not declare.
- Secret-store ACL bypass.

### Medium (CVSS 4.0–6.9 typical)

- Reflected XSS in admin.
- CSRF against a destructive admin endpoint.
- SSRF blocked at the firewall but reachable to non-internal destinations.
- Audit-log tampering without detection.
- IDOR exposing non-secret data (e.g. another user's draft).

### Low (CVSS 0.1–3.9 typical)

- Information disclosure of non-secret data (e.g. version banners on undocumented endpoints with a concrete chain).
- Rate-limit bypass on a non-destructive endpoint.
- Missing security headers tied to a working exploit narrative.
- Self-XSS that requires non-trivial victim interaction.

### Informational (no payout)

- Best-practice violations with no demonstrated exploitation chain.
- Open redirects that do not enable a phishing scenario we have not already documented.
- Reports of secrets in public Docker images that are intentionally public test secrets.
- Output of automated scanners without manual verification.

---

## 5. Payout terms

| Term | Value |
|---|---|
| Currency | USD by default. EUR, GBP, JPY, INR available. |
| Method | Stripe Connect (preferred) or Wise. |
| Timing | Within **30 days** of fix release. |
| Tax | Reporter is responsible for their own tax obligations. We will issue a 1099-NEC for US persons crossing the IRS threshold. |
| Refunds | Bounties are not refundable. If we later determine a report duplicates an earlier one, the second report's bounty stands. |
| Bonus modifiers | +50% for working patches, +25% for clean reproductions, +25% for high-quality disclosure documents. Modifiers stack additively up to +50%. |

We will not pay bounties for:

- Issues we already had on file (we will show you the internal advisory ID).
- Issues reported simultaneously by another researcher when we cannot determine first-mover (we split 50/50).
- Issues you generated by attacking our infrastructure beyond what was necessary to confirm the bug.

---

## 6. Examples — valid reports

### 6.1 Critical — sandbox escape (paid $5,000)

> "Plugin manifest declares only `cache.invalidate`. Plugin uses a malformed UTF-8 boundary in a host-call argument to cause a buffer overrun in the wazero adapter, which lets it write into the host's WASM compilation cache. On next request, the host loads attacker-controlled bytes as a 'native' helper. PoC plugin attached."

Why it's good: declares the capability scope, isolates the abused boundary, provides a reproducible PoC, identifies the host-side weakness.

### 6.2 High — privilege escalation (paid $1,000)

> "Author-role user can issue `POST /api/v1/users/:id/role` with their own ID and body `{"role":"admin"}`. The handler checks the *target* user's permissions, not the actor's. cURL transcript attached. Affected since the role API was introduced in v0.3."

Why it's good: one-sentence root cause, concrete reproduction, version range.

### 6.3 Medium — stored XSS in admin (paid $250)

> "An author can insert a `core/html` block with `<script>` content. The admin preview renders this unsanitized. Public render strips it correctly. Author role is intended to use `unfiltered_html`, but the preview pane should still treat content as untrusted because Author X may view Author Y's draft via the editorial flow."

Why it's good: identifies the subtle distinction between author trust and preview-pane trust.

---

## 7. Examples — invalid reports

### 7.1 Missing CSP on public endpoint (no payout)

> "Your public endpoint at `/feed.xml` lacks a Content-Security-Policy header."

Why it's rejected: RSS feeds don't execute JavaScript; CSP is irrelevant. The header is intentionally omitted to keep the response trim. No exploitation chain.

### 7.2 Self-XSS in URL fragment (no payout)

> "If I paste `<script>alert(1)</script>` into the URL fragment of `/admin/posts/new#…`, it executes."

Why it's rejected: URL fragments aren't transmitted to the server; an attacker can't make the victim's browser do this without an existing XSS or a victim deliberately typing the payload. Self-XSS is not a vulnerability.

### 7.3 Nmap output (no payout)

> "I ran nmap against gonext.io and you have port 443 open."

Why it's rejected: that's the intended configuration of an HTTPS service. Automated-scanner output without manual analysis is not actionable.

### 7.4 Public repository's test secret (no payout)

> "I found the string `test-secret-do-not-use` hardcoded in `docker-compose.test.yml`."

Why it's rejected: that's a deliberately-public test fixture, not a leaked credential. It is replaced at boot in any non-test environment.

---

## 8. Duplicates and overlapping reports

- **First-reporter wins** based on receipt timestamp.
- If two reports arrive within 12 hours, we attempt to determine who reproduced first; if we can't, we **split the bounty** 50/50.
- Two reports describing *different aspects* of the same root cause may both be paid, at the security lead's discretion. We will explain our decision in writing.

---

## 9. Disputes

If you disagree with our triage or severity decision:

1. Reply on the same advisory thread (or email <security@gonext.io>) with the reasoning.
2. We will re-review with a second maintainer who was not on the original triage.
3. The second review is final from our side. You retain all rights to publicly disclose per the safe-harbor terms.

We track every dispute outcome in an internal ledger. The aggregate stats (how many disputes, how many upheld, how many revised) are published in the annual transparency report.

---

## 10. Related documents

- [`/SECURITY.md`](../SECURITY.md) — disclosure entry point.
- [`/docs/15-security-policy.md`](15-security-policy.md) — full policy, threat model, hardening defaults, breach recovery.
- [`/docs/13-security-baseline.md`](13-security-baseline.md) — technical security baseline.
- [`/.well-known/security.txt`](../.well-known/security.txt) — RFC 9116 machine-readable summary.
