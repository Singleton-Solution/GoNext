# ADR 0001: Licensing — source-available core, permissive SDK

- **Status**: proposed
- **Date**: 2026-05-17
- **Deciders**: @tayebmokni
- **Consulted**: design docs, proposal S2 in `docs/proposals/14-proposals-strategic.md`
- **Informed**: future contributors

## Context

GoNext is being built as a long-term commercial project. The maintainer (Singleton-Solution) intends to fund continued development via hosted SaaS, commercial licensing, and premium first-party plugins. The license choice must serve two goals that pull in opposite directions:

1. **Commercial sustainability**: the project should not be cloneable as a managed service by hyperscalers in direct competition with the maintainer. This is the moat.
2. **Community contribution + plugin ecosystem growth**: contributors and plugin authors must feel safe building on the project. License-induced friction kills ecosystems.

Surveying the landscape:
- **GPL / AGPL** (WordPress's choice): allows hyperscaler cloning. WordPress relies on trademark and Automattic's control of WordPress.com as the moat. Strong ecosystem but the moat is brand, not code.
- **Apache 2.0 / MIT**: maximum freedom, but anyone can fork and host commercially. Used by Kubernetes (where Red Hat / CNCF make money on services, not the code).
- **BSL 1.1** (MariaDB / HashiCorp Terraform / CockroachDB / Couchbase): source-available; non-competing use is free; competing managed-service hosting requires a commercial license; auto-converts to a permissive license after a defined period (4 years per file is standard).
- **FSL 1.1** (Sentry, newer projects): similar to BSL but with cleaner terms. Two years to Apache 2.0 OR MIT.
- **ELv2** (Elastic): source-available; no auto-conversion; broader restrictions.
- **SSPL** (MongoDB): controversial; viral copyleft that's hostile to integrators.

The plugin SDK is a separate question. Whatever license we choose for the core, **plugin and theme authors importing the SDK must not be encumbered**. Otherwise the ecosystem won't grow.

## Decision

A three-tier license stack:

1. **Core** (Go API server, admin app, public app, workers, importers, CLI — everything under `/apps`, `/cli`, and `/packages/go/{internal,host,...}` except SDK packages) is licensed under the **Functional Source License 1.1** with conversion to **Apache License 2.0** after **2 years from each file's commit date** (FSL-1.1-Apache-2.0).
2. **SDK packages** (`/packages/ts/sdk`, `/packages/go/sdk`, anything else that plugin or theme authors must import) are licensed under **Apache License 2.0** from day one.
3. **First-party plugins and themes** (`/plugins/*`, `/themes/*` that ship in this repository) are licensed under **FSL-1.1-Apache-2.0** (same as core).
4. **Documentation, design docs, ADRs, proposals** (`/docs`, `/adr`) are licensed under **Creative Commons Attribution 4.0 International (CC-BY-4.0)**.

All contributors must sign a **Contributor License Agreement** assigning copyright in their contributions to Singleton-Solution. See ADR 0002.

## Consequences

### Positive

- Singleton-Solution retains exclusive right to operate GoNext as a managed SaaS (the primary commercial moat) during the FSL period.
- After 2 years, each file becomes Apache 2.0 — the project ages into a fully OSI-approved open-source codebase. The community is not locked out forever.
- Plugin authors face zero license friction when importing the SDK. The ecosystem can grow.
- Singleton-Solution can sell commercial licenses to enterprises that need the code under different terms.
- The CLA gives Singleton-Solution flexibility to dual-license, change license terms with notice, and enforce.

### Negative

- FSL is **not OSI-approved as "open source"**. A subset of the free-software community will reject the project on this basis alone. We accept that loss — that subset is not our target customer.
- Some companies have policies against using non-OSI licenses, even source-available ones. They will not adopt until files start converting to Apache 2.0.
- The CLA adds friction to first-time contributors. Mitigated by automation (CLA-bot signs on PR).
- Singleton-Solution must maintain the rolling conversion (file-by-file) and publish a tool that shows which files are Apache 2.0 vs FSL.

### Neutral / accepted tradeoffs

- We are explicitly NOT compatible with the WordPress GPL ecosystem. Code cannot flow from WP plugins into GoNext core. This was already a non-goal (we migrate content, not code).
- AGPL-style "you must release your modifications" is NOT in scope. Customers can run modified GoNext internally without disclosing changes.

## Alternatives considered

### Option A: Apache 2.0 (originally proposed in S2 v1)
- Rejected. Allows AWS / GCP / Azure to host GoNext as a managed service in direct competition with Singleton-Solution. This defeats the commercial sustainability goal.

### Option B: GPL v3 (the WordPress model)
- Rejected. Moat is trademark only; commercial competitors can still clone-and-host. Also deters commercial plugin authors who don't want to GPL their plugins.

### Option C: AGPL v3
- Rejected. Stronger than GPL for SaaS but viral copyleft on "modifications" creates legal risk for adopters that's hard to reason about. Hostile to integrators.

### Option D: BSL 1.1 (HashiCorp / MariaDB)
- Rejected as primary choice in favor of FSL 1.1, but acceptable fallback. BSL's per-file Change Date adds operational complexity; FSL has cleaner terms.

### Option E: ELv2 (Elastic License v2)
- Rejected. No auto-conversion to permissive — the community never gets full OSS. Aggressive optics.

### Option F: SSPL (MongoDB)
- Rejected. Viral copyleft is hostile, OSI explicitly rejected it, the community optics are bad.

### Option G: Dual license — GPL + Commercial
- Rejected. Requires CLA anyway; FSL gives clearer signaling and avoids "what GPL version" debates.

## References

- Design doc: `docs/proposals/14-proposals-strategic.md` §S2 (revised by this ADR)
- FSL 1.1 text: https://fsl.software/
- BSL 1.1 text: https://mariadb.com/bsl11/
- Sentry on FSL: https://blog.sentry.io/introducing-the-functional-source-license-freedom-without-free-riding/
- HashiCorp on BSL: https://www.hashicorp.com/blog/announcing-hashicorp-adoption-of-business-source-license
- Related ADRs: ADR 0002 (CLA requirement)
