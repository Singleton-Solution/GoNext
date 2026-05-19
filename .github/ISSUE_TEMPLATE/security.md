---
name: Security vulnerability (STOP — read this first)
about: This template exists to redirect you. Do NOT file public issues for vulnerabilities.
title: '[SECURITY-REDIRECT] please ignore'
labels: ['type:security-redirect']
assignees: []
---

<!--
  GitHub does not yet let issue templates be "read-only redirects". The closest we
  can do is put the redirect text first, label it, and ask you to close the issue.
-->

# Stop — do not file this issue.

Public GitHub issues are visible to everyone, including the people who would weaponize
the bug before we can ship a fix. That is bad for our users, bad for the report's
authors, and bad for the project. **Please close this issue** and use one of the
private paths below instead.

## Private disclosure paths

1. **Preferred — GitHub Security Advisory** (private, end-to-end with maintainers):
   https://github.com/Singleton-Solution/GoNext/security/advisories/new

2. **Email**: <security@gonext.io>
   Subject line: `[SECURITY] GoNext - <short description>`
   PGP fingerprint (placeholder, replaced before v1.0):
   `0000 1111 2222 3333 4444  5555 6666 7777 8888 9999`

## What you get

- Acknowledgement within 24 hours.
- Initial severity assessment within 7 days.
- Coordinated disclosure default of 90 days.
- Bug-bounty payout per the tier table in
  [`docs/16-bug-bounty.md`](../docs/16-bug-bounty.md) once v1.0 ships.
- Credit in the advisory and the project Hall of Fame (or anonymity, if you prefer).
- Safe-harbor protections per [`/SECURITY.md`](../SECURITY.md).

## If you have already published details

If the bug is already public somewhere (a tweet, a blog post, a CTF write-up), that is
**still** a reason to email us privately — we can coordinate the release of a fix
and reduce the window during which users are exposed. Please do not paste those
details into this issue.

## What goes in a public issue

If the underlying bug has been disclosed publicly **and** has been patched **and**
is being tracked for follow-up hardening, you may file a public issue tagged
`security:follow-up`. The original advisory ID must be linked.

For anything else — please close this issue and use the private path. Thank you.

---

**Action requested**: please close this issue and resubmit via the
[private security advisory form](https://github.com/Singleton-Solution/GoNext/security/advisories/new).
