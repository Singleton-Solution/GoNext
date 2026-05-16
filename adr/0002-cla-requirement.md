# ADR 0002: All contributors sign a Contributor License Agreement

- **Status**: proposed
- **Date**: 2026-05-17
- **Deciders**: @tayebmokni
- **Consulted**: ADR 0001 (licensing)
- **Informed**: future contributors, plugin authors

## Context

ADR 0001 picks a three-tier license stack with FSL-1.1-Apache-2.0 at the core. That license choice is only viable if Singleton-Solution can actually exercise the rights it implies: dual-licensing to enterprise customers, granting commercial-use exceptions, relicensing files to Apache 2.0 on each file's conversion date, and (if circumstances change) shifting the whole stack to a different license in the future. Each of those acts requires that the maintainer hold (or have been assigned) the copyright in every contribution.

Without a contributor agreement, every PR that lands becomes a separate copyright held by its author, under the inbound license of the repo (FSL 1.1). Relicensing then requires tracking down every contributor and getting individual permission — a process that has paralyzed real projects (Linux, MariaDB) when license questions resurface years later.

Two practical paths exist: a **Contributor License Agreement (CLA)**, where contributors assign or grant broad rights to the maintainer; or a **Developer Certificate of Origin (DCO)**, where contributors only certify that they have the right to submit the work under the project's license. Linux uses DCO. Apache, Google, Meta, and most commercial-backed projects use CLAs.

A CLA adds first-PR friction. We mitigate that with automation: a CLA-bot (e.g., cla-assistant.io, EasyCLA) detects the signature on a PR and either passes the check or comments with a one-click sign URL. The signature is per-individual (and per-corporation, for employer-owned work) and applies to all future contributions.

## Decision

All contributors must sign a Contributor License Agreement before their first PR can be merged. The CLA assigns copyright in the contribution to **Singleton-Solution** and grants Singleton-Solution the right to sub-license under any terms. The agreement is enforced by an automated CLA-bot integration (cla-assistant.io or equivalent) on every PR. Corporate contributors sign a separate Corporate CLA covering all employee contributions.

## Consequences

### Positive

- Singleton-Solution can exercise the licensing flexibility ADR 0001 depends on: dual-license to enterprise, grant commercial exceptions, perform the rolling FSL → Apache 2.0 conversion, and shift terms with notice if needed.
- A single legal entity holds the copyright, which means a single legal entity can enforce against license violations. Without that, enforcement is fragmented and weak.
- Corporate contributors get a clean legal answer to "can our employees contribute?" — sign the Corporate CLA once.
- The bot makes signing a 30-second click. Friction is real but small.

### Negative

- First-time contributor friction. Some contributors — especially those allergic to CLAs on principle — will not sign and will not contribute. This is a real, observed effect (the SQLite project's stance against CLAs is one reason it has the contributor base it does).
- Maintenance burden: signed CLAs must be retained, the bot must stay configured, corporate signatories' email domains must be kept up to date.
- Optics: some communities view CLAs as a power asymmetry between maintainer and contributor. ADR 0001's commercial sustainability framing makes that asymmetry honest, but it is still real.

### Neutral / accepted tradeoffs

- We will not accept anonymous contributions. Every commit has a signed-off identity. This is a security baseline, not just a CLA requirement.
- The CLA does not give Singleton-Solution the right to use a contributor's name or trademarks for promotion — only the copyright in the contribution itself.

## Alternatives considered

### Option A: Developer Certificate of Origin (DCO)
- Rejected. DCO does not transfer copyright; it only certifies the right to submit. Each contributor remains the copyright holder of their patch. This blocks the relicensing flexibility ADR 0001 requires, and makes dual-licensing to enterprise customers either impossible or contingent on every contributor's separate permission.

### Option B: No CLA at all (inbound = outbound under FSL)
- Rejected. Contributions would land under FSL 1.1 with copyright retained by each author. Any future license change — including the planned per-file conversion to Apache 2.0 — would require tracking down every author. This kills the rolling-conversion model in practice.

### Option C: Permissive copyright grant without assignment (Apache-style ICLA)
- Considered. Apache's ICLA grants rights without transferring copyright. We pick assignment instead because (a) it gives the cleanest enforcement story (single plaintiff) and (b) the practical effect for the contributor is the same — they retain their own copy of the code and can use it however they like; they just give Singleton-Solution the right to do the same plus relicense. Worth revisiting if community signal is strongly against assignment.

### Option D: Hybrid (DCO for small contributions, CLA for large)
- Rejected. Two regimes, two enforcement paths, ambiguity at the threshold. Not worth the complexity.

## References

- Prior ADR: ADR 0001 (licensing)
- Design doc: `docs/proposals/14-proposals-strategic.md` §S2 (licensing context)
- cla-assistant.io: https://cla-assistant.io/
- Apache ICLA: https://www.apache.org/licenses/contributor-agreements.html
- Linux DCO rationale: https://developercertificate.org/
