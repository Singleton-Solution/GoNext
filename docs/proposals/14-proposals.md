# Proposals — Answers to All Open Questions

Every open question across docs 00–13 plus the strategic/commercial questions the technical docs don't own. **159 proposals total.**

Each proposal is opinionated. "Do X" not "consider X". Pick differently if you want — but don't ship with the question unresolved.

| File | Scope | Proposals |
|---|---|---:|
| [14-proposals-strategic.md](14-proposals-strategic.md) | Wedge, license, customer, repo, distribution, pricing, marketplace, first-party, phase 0, governance, brand, browsers, support, docs, migration target, perf target, launch criteria | **17** (S1–S17) |
| [14-proposals-foundation.md](14-proposals-foundation.md) | Docs 00 (architecture), 01 (core CMS), 02 (plugin system), 03 (theme system) | **37** |
| [14-proposals-content.md](14-proposals-content.md) | Docs 04 (block editor), 05 (admin & API), 06 (auth & perms), 07 (media & perf) | **42** |
| [14-proposals-platform.md](14-proposals-platform.md) | Docs 08 (migration), 09 (deployment), 10 (observability) | **30** |
| [14-proposals-ops-sec.md](14-proposals-ops-sec.md) | Docs 11 (testing/CI), 12 (jobs/cron), 13 (security baseline) | **33** |

## Format used in each file

```
### Q[doc#]-[n]: short title
Source: doc XX §Y (restatement)
Proposal: opinionated answer
Reasoning: 1-3 sentences
Confidence: high | medium | low
Reversibility: cheap | moderate | expensive
```

The strategic file uses `S1`, `S2`, ... numbering.

## Top-line decisions (the ones that touch everything)

If you only read 17 proposals, read these:

| # | Question | Answer | Why it matters |
|---|---|---|---|
| **S1** | The wedge | "WordPress, but you can trust the plugins" — security/sandbox positioning | Drives marketing, ecosystem strategy, and what features get prioritized. |
| **S2** | License | Apache 2.0 | Determines who can build commercial plugins on top. Reversal cost is enormous. |
| **S4** | Repo structure | Monorepo, Go + pnpm workspaces | Day-1 decision; affects every dev workflow afterward. |
| **S9** | Phase 0 | 4-week end-to-end vertical slice | The single thing to actually build first. |
| **Q00-4** | License (also) | Apache 2.0 (matches S2) | |
| **Q01-3** | Block content storage | Split into separate `post_content` table, 1MB soft cap | Avoids the WP `wp_posts.post_content` size pathology. |
| **Q02-9** | Plugin ABI migration | 18-month deprecation + codemods + manifest `abi` version | Ecosystem trust requires predictable breaking-change policy. |
| **Q03-3** | theme.json schema source | JSON-only + codegen TS helper | Single source of truth across tooling. |
| **Q05-5** | Admin SPA vs SSR | Full SPA, no SSR | Saves ~6 months of work that nobody asks for. |
| **Q06-2** | Passkeys for super_admin | Hosted-required, self-host-recommended | Right security/UX balance. |
| **Q11-5** | Testing — WP migration corpus | Synthetic WP-shape generator, not real anonymized sites | Kills the licensing question, makes CI faster. |
| **Q12-5** | Job consistency | Transactional outbox (DB + Redis) | Closes the silent-data-loss vector around webhooks/email. |
| **Q13-13** | PII in debug logs | Write-time redaction + separate encrypted debug sink | The compliance-vs-debugging tradeoff actually resolved. |

## Decisions explicitly deferred (with named triggers)

These shouldn't be answered now — but they shouldn't be forgotten either. Each has a phase/signal trigger.

| Question | Deferred until | Trigger to revisit |
|---|---|---|
| Q01-4 | P2 | Block schema stabilizes; real revision data volume measurable. |
| Q01-7 | P6 | Real user-search patterns exist to design DSL around. |
| Q02-6 | P6 | Plugin marketplace has enough volume to justify automated scanners. |
| Q03-2 | P6 | Theme edge-runtime demand from real authors. |
| Q08-6 | v1.1 | Once core multilingual story (i18n doc not yet written) is committed. |
| Q09-1 | v2 | When service mesh's value exceeds its operational cost — likely never for v1 shape. |
| Q10-3 | P5 | When tracing volume justifies eBPF's complexity over OTel SDK. |
| Q11-7 | P4 | 2-week spike with named go/no-go criterion. |
| Q12-1 | P5 | Multi-region work actually begins. |
| Q13-13 stakeholder review | Before P4 | Privacy/compliance stakeholder sign-off needed. |
| S11 (brand name) | 8 weeks after S1 commits | Wedge positioning crystallizes. |

## Things consciously rejected

Proposals that explicitly say "**don't build this**":

- **Q12-3** Priority preemption — not worth the complexity. Drain windows are good enough.
- **Q13-1** Pre-launch bug bounty — burn money on noise. Wait for v1.0.
- **Q02-4** Two React copies in the bundle — banned. Re-export from SDK.
- **Q05-9** SSR for admin — banned. Full SPA.
- **Q08-4** WooCommerce full migration — out of scope. Catalog-only opt-in.

## Confidence distribution

| Confidence | Count | What it means |
|---|---|---|
| High | ~95 | Standard practice or clearly dominant choice. Build to this. |
| Medium | ~50 | Defensible but contestable. Revisit if a contributor pushes back hard. |
| Low | ~14 | Pick one because we can't ship without a pick; expect to change it once real usage data exists. |

## Reversibility distribution

| Reversibility | Count | Implication |
|---|---|---|
| Cheap | ~95 | Pick fast. Move on. Re-decide if wrong. |
| Moderate | ~45 | Some refactor cost. Plan ~1-2 weeks if reversed mid-build. |
| Expensive | ~19 | Architectural commitment. License, wedge, repo structure, etc. Re-decide only with cause. |

## How to use this

1. **Read [14-proposals-strategic.md](14-proposals-strategic.md) first.** S1, S2, S4, S5, S9 set the frame for everything else.
2. **Read the doc-level file for the subsystem you're building.** Each proposal answers a question that doc raised.
3. **Disagree as needed.** Each proposal has reasoning. If you disagree, you've identified a decision worth keeping in the ADR log.
4. **Confidence + reversibility tells you the conviction.** Low-confidence + cheap-reversibility = pick fast, change later. High-confidence + expensive-reversibility = commit and don't relitigate.
