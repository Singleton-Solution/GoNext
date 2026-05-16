# ADR 0002: All contributors sign off commits via the Developer Certificate of Origin

- **Status**: proposed
- **Date**: 2026-05-17
- **Deciders**: @tayebmokni
- **Consulted**: ADR 0001 (licensing); supersedes the original draft of this ADR which specified a CLA
- **Informed**: future contributors, plugin authors

## Context

ADR 0001 picks a three-tier license stack with FSL-1.1-Apache-2.0 at the core. That license choice needs a contributor-rights mechanism: without one, every PR's copyright is held individually by its author under the inbound license (FSL 1.1), which is fine for normal use but constrains future flexibility (notably, dual-licensing to enterprise customers without going back to every contributor).

Two practical mechanisms exist:

- **Contributor License Agreement (CLA)** — contributors assign or grant broad rights to the maintainer via an out-of-band signature, usually managed by a bot like cla-assistant.io.
- **Developer Certificate of Origin (DCO)** — contributors add `Signed-off-by: Name <email>` to each commit message. This is a per-commit attestation that the contributor has the right to submit the work under the project's license. It does **not** transfer copyright.

The first draft of this ADR specified a CLA, with `cla-assistant.io` as the planned automation. On security review, the broad OAuth scope that `cla-assistant.io` requests (read access to all org repos, public and private) was deemed unacceptable for the **Singleton-Solution** org, which hosts unrelated private repos containing client work and commercial IP. The OAuth permission model does not allow per-repo restriction; the bot's promise to only read CLA-enabled repos cannot be technically enforced.

Alternative CLA tools were considered:
- **EasyCLA** (Linux Foundation) — proper per-repo scoping but requires LF membership/affiliation, formal setup, and corporate overhead disproportionate to a solo-founder pre-launch project.
- **Dedicated bot account with cla-assistant.io** — viable but adds operational complexity (managing a separate GitHub user, monitoring its activity, rotating credentials).

DCO requires no third-party service, no GitHub App installation, and no broad OAuth grant. The check is a small workflow file in `.github/workflows/dco.yml` that verifies the `Signed-off-by:` trailer is present on every commit in a pull request.

The tradeoff: DCO does not give the maintainer the legal right to relicense contributions commercially without re-asking every contributor. This matters only if Singleton-Solution decides to dual-license GoNext under non-FSL terms (e.g., selling a fully proprietary license to an enterprise customer). For v1, this is not a planned business motion — the FSL → Apache 2.0 conversion is automatic per-file, the SaaS-hosting restriction is the commercial moat, and any enterprise customer that needs different terms can be served through the FSL's existing exceptions process.

If commercial dual-licensing becomes a goal post-v1.0, a CLA can be layered on top of DCO at that point. The reverse path (moving from CLA to DCO) is also doable. The decision is not permanent.

## Decision

All contributors must sign off every commit they submit, using the Developer Certificate of Origin (DCO). Every commit in a pull request must contain a line of the form:

```
Signed-off-by: Real Name <email@example.com>
```

This is added automatically by `git commit -s` (or `--signoff`). A required CI check at `.github/workflows/dco.yml` blocks merge until every commit on the PR carries the trailer.

A formal Contributor License Agreement is **not** required for v1. The decision to add a CLA is deferred to v1.0+, contingent on a clear commercial dual-licensing requirement that the FSL terms cannot satisfy on their own.

## Consequences

### Positive

- **Zero third-party service installs.** No GitHub App, no OAuth grant, no broad-scope token sitting on someone else's servers. The check runs entirely on GitHub Actions inside our repo.
- **No exposure of private repos** in the Singleton-Solution org to any external service.
- **Lower contributor friction** than CLA — no out-of-band signing flow. A single `-s` flag on `git commit`.
- **Industry-accepted at scale.** Linux kernel, Docker, GitLab, Helm, all CNCF projects since 2022 use DCO. It is not seen as a weaker mechanism in modern OSS culture.
- **DCO sign-offs are part of the git history**, which makes the attestation trail durable and auditable forever, without depending on a service's continued operation.

### Negative

- **No copyright assignment.** Singleton-Solution cannot unilaterally relicense contributions to terms outside what the inbound FSL 1.1 license already permits. Specifically, we cannot offer a contributor's code under a non-FSL commercial license without that contributor's separate permission.
- **Enforcement is fragmented** if someone violates the license downstream — Singleton-Solution can enforce on our own original code, but for contributor code, the contributor is the proper plaintiff.
- **A future shift to a CLA requires either** (a) re-getting all contributor permission (hard for any project past 100 contributors), or (b) replacing the contributed code over time. This is a real lock-in cost.

### Neutral / accepted tradeoffs

- Anonymous contributions are still not accepted — `Signed-off-by` requires a real name and email. This is a security baseline distinct from the DCO mechanism.
- The DCO does not give Singleton-Solution the right to use a contributor's name or trademarks for promotion. The CLA wouldn't have either.
- If Singleton-Solution later decides commercial dual-licensing is critical, the path forward is: (a) add a CLA from that date onward, accept that pre-CLA contributors retain their rights, or (b) buy out / re-implement the contested code paths. Both are doable but neither is free.

## Alternatives considered

### Option A: CLA via cla-assistant.io
- Rejected. Requires broad OAuth scope (read access to all org repos). Unacceptable for an org with unrelated private repos. The OAuth permission model has no per-repo restriction.

### Option B: CLA via EasyCLA (Linux Foundation)
- Rejected for v1. Proper per-repo scoping but requires LF affiliation, formal corporate setup, and overhead disproportionate to a solo-founder pre-launch project. Revisit if the project formally joins a foundation.

### Option C: CLA via cla-assistant.io with a dedicated bot account
- Rejected. Cordons the OAuth blast radius to one repo (good), but adds operational complexity: managing a separate GitHub user, monitoring its activity, rotating credentials, and explaining the architecture to contributors. The friction outweighs the benefit when DCO is available.

### Option D: No contributor mechanism at all
- Rejected. Contributions would land with no explicit attestation that the contributor has the right to submit them. This is a real legal risk for the maintainer if a contributor later turns out to have submitted code they did not own.

### Option E: Hybrid (DCO for small contributions, CLA for large)
- Rejected. Two regimes, two enforcement paths, ambiguity at the threshold. Not worth the complexity.

## References

- Prior ADR: ADR 0001 (licensing)
- Design doc: `docs/proposals/14-proposals-strategic.md` §S2 (licensing context)
- DCO text: https://developercertificate.org/
- Linux DCO process: https://www.kernel.org/doc/html/latest/process/submitting-patches.html#sign-your-work-the-developer-s-certificate-of-origin
- GitLab on DCO: https://docs.gitlab.com/ee/legal/developer_certificate_of_origin.html
- CNCF DCO requirement: https://github.com/cncf/foundation/blob/main/charter.md
- This ADR supersedes the original CLA-based draft (was `0002-cla-requirement.md`)
