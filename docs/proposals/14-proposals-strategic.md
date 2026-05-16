# Strategic Proposals

Proposals for the commercial/strategic questions that no technical doc owns. The doc-level open questions are answered in `14-proposals-foundation.md`, `14-proposals-content.md`, `14-proposals-platform.md`, `14-proposals-ops-sec.md`.

Every proposal here is **opinionated**. Pick differently if you want, but don't ship without picking.

---

## S1: The wedge — what makes this different?

**Proposal**: **"WordPress, but you can trust the plugins."** Security and sandbox isolation are the primary product positioning. Performance is a strong second.

**Reasoning**: The competitive landscape's gap is clear once you map it:
- Strapi / Payload / Sanity / Directus — TypeScript-first, dev-loved, **no real plugin ecosystem**.
- Ghost — beautiful, narrow (blogging).
- WordPress — has the ecosystem, **plugin security is the most-cited reason people leave**.

The defensible technical moat in our design is **the WASM-sandboxed plugin runtime + signed marketplace** (doc 02, doc 13). That's the lever. Marketing copy writes itself: "your CMS, your plugins, your peace of mind." Performance (doc 07) is table stakes once the lever lands.

**Confidence**: high. **Reversibility**: expensive (positioning is a 12-month commitment).

---

## S2: License

**Proposal**: **Apache 2.0** for core. **Same license for first-party plugins/themes.** Encourage community plugins to choose their own license (MIT, Apache, GPL, commercial — all fine).

**Reasoning**: WordPress's GPL is a major reason its commercial plugin ecosystem is fragmented and legally murky. We need premium plugin authors to feel safe building businesses on top. Apache 2.0 also has the patent grant the modern enterprise market expects. The only real argument for GPL is "ecosystem alignment with WordPress" — but our wedge (S1) is explicitly *not* WordPress-compatible at the plugin level, so the argument doesn't apply.

Reject: GPL (fragments commercial ecosystem), MPL (file-level copyleft is too subtle for a CMS), BSL/SSPL (kills enterprise adoption).

**Confidence**: high. **Reversibility**: expensive (relicensing requires CLAs from contributors).

---

## S3: First customer — who do we sell to first?

**Proposal**: **Independent technical bloggers and small SaaS marketing sites.** Specifically: people who write about software, run sites with under 10k monthly visitors, and have been burned by a WordPress plugin breach or slow load times.

**Reasoning**: They're vocal (blog posts drive adoption), tolerate v0 rough edges, value the wedge (security + performance), and don't yet depend on ACF Pro / WooCommerce / page builders (which we cannot migrate cleanly). They're also the audience most likely to *self-host*, which is our v1 distribution model (S5).

Reject: agencies (too plugin-dependent on day 1), enterprise (we have no SOC 2), e-commerce stores (no Woo replacement at launch).

**Confidence**: medium. **Reversibility**: cheap (positioning can shift after launch).

---

## S4: Repository structure

**Proposal**: **Single monorepo, Go workspaces + pnpm workspaces.**

```
gonext/
  apps/
    api/          # Go server (HTTP + WASM host)
    worker/       # Go background worker (Asynq consumer)
    web/          # Next.js public site (SSR/ISR)
    admin/        # Next.js admin dashboard
  packages/
    go/           # shared Go packages (auth, policy, plugin host, etc.)
    ts/           # shared TS packages (block SDK, theme SDK, plugin SDK client)
  plugins/
    gn-seo/
    gn-forms/
    gn-shop/
  themes/
    gn-hello/    # block theme
    gn-pro/      # classic theme
  cli/
    gonext/      # Go CLI binary
  tools/          # codegen, build scripts, lint config
  docs/           # public docs site source
  .github/
```

**Reasoning**: Monorepo eliminates the inter-package version drift that would otherwise be brutal (Go API ↔ block schema ↔ admin client ↔ plugin SDK). Go workspaces + pnpm workspaces both handle this cleanly. CI runs change-aware: a plugin-only edit doesn't rebuild the world.

Reject: polyrepo (version coordination nightmare), Nx (overkill, opinionated), Bazel (overkill, learning curve).

**Confidence**: high. **Reversibility**: moderate.

---

## S5: Distribution model

**Proposal**: **Self-host first, SaaS later (post-1.0).** v1 ships as a Docker image and a Helm chart. SaaS waits until self-host validates the wedge.

**Reasoning**: SaaS is a separate product (billing, multi-tenancy, support, compliance). Each is a 6-month project on top of the CMS. v1 already costs 24 months — adding SaaS doubles it. Self-host is also more credible for the wedge (S1): "trust" is easier to deliver when the user controls the bytes.

**Confidence**: high. **Reversibility**: cheap (SaaS can be added on top later).

---

## S6: Pricing (when SaaS happens)

**Proposal** (for post-1.0 SaaS):
- **Free**: 1 site, 1GB storage, community support, watermarked admin (subtle). Forever.
- **Pro**: $20/site/mo. Unlimited storage. Priority support. White-label admin.
- **Team**: $200/mo. 10 sites, multi-user admin, audit log export, SAML SSO.
- **Enterprise**: custom. Compliance addendums, dedicated infra option.

**NOT per-seat**. Per-site is the WordPress mental model.

**Reasoning**: Free tier is non-negotiable for adoption (WP's price point is $0). Pro pricing matches Ghost ($25), undercuts WordPress.com VIP. Team tier captures agencies. Enterprise is custom and infrequent — don't over-engineer.

**Confidence**: medium. **Reversibility**: cheap until 1000s of customers exist.

---

## S7: Marketplace economics

**Proposal**:
- **80/20** revenue share to authors (vs Apple's 70/30, WP's de facto 100% since WP has no marketplace, Shopify's 80/20). Authors keep 80%.
- **Free plugins and themes are listed alongside paid ones** in the same marketplace.
- **All marketplace plugins must be signed** (doc 02 §10). Unsigned plugins installable only via admin override.
- **License keys** for paid plugins/themes: simple ed25519-signed tokens validated against marketplace API. Plugin checks license on activation and periodically; graceful degrade (read-only mode) on lapse, not hard-kill.
- **Marketplace is itself a gonext site** — dogfooding from day 1.

**Reasoning**: 80/20 attracts good authors (Apple's 70/30 is widely criticized; we're trying to win developers). License key model needs to be lenient — angry users from billing disputes will trash the brand. Marketplace-as-our-own-site is both validation and a forcing function for the eCommerce reference plugin.

**Confidence**: medium. **Reversibility**: moderate.

---

## S8: First-party plugins and themes for launch

**Proposal — 3 plugins, 2 themes, all reference-quality:**

**Plugins** (each shipped as a polished, opinionated product that 80% of users won't need to replace):
1. **gn-seo** — sitemap.xml generation, meta tags, OG/Twitter cards, schema.org JSON-LD, redirect manager, robots.txt, internal-link analysis. Replaces Yoast/Rank Math for the common case.
2. **gn-forms** — drag-drop form builder, submissions store, email notifications, spam guard (honeypot + optional Turnstile), GDPR-friendly. Replaces CF7/WPForms for the common case.
3. **gn-shop** — products, cart, Stripe Checkout, basic inventory, basic tax. *Not* a WooCommerce replacement. A lightweight "sell 5 things from your blog" plugin. Anything more goes to a future v2 product.

**Themes**:
1. **gn-hello** — block theme. Minimal, fast, FSE-driven. Demonstrates full-site editing. Default new-site theme.
2. **gn-pro** — classic theme (code-defined templates). For developers who want code-first control. Demonstrates the theme SDK.

**Reasoning**: 3+2 covers the wedge demo without overcommitting. SEO, forms, basic commerce + two well-built themes = a credible "complete site" out of the box. Anything else lives in the community marketplace.

**Confidence**: high. **Reversibility**: cheap (plugin/theme set can be tuned during beta).

---

## S9: Phase 0 — what gets built first

**Proposal — a 4-week vertical slice before anything else:**

| Week | Deliverable |
|---|---|
| 1 | Go binary with HTTP server, Postgres connection, basic migrations, `/healthz`. One endpoint: `GET /api/v1/posts` returning seeded data. |
| 2 | Next.js public app rendering one post via the API. Slug-based routing. ISR enabled. |
| 3 | Block JSON tree storage in `posts.content_blocks`. Render one block (`core/paragraph`). |
| 4 | Auth: sign up, log in (no 2FA yet). Admin shell with one page: post list. |

**Outcome**: end-to-end stack works on real DB + real Next + real auth + real block render. If anything is wrong (Postgres driver, Next.js + Go API contract, block render shape), you find out in 4 weeks not 12 months.

**Reasoning**: The biggest risk to the whole design is "we built piece A and piece B in isolation and they don't compose." Phase 0 forces composition before scale.

**Confidence**: high. **Reversibility**: cheap.

---

## S10: Governance model

**Proposal**: **Benevolent dictator** through v1 (you, with named maintainers for each subsystem). **Foundation model** (Rust-style) post-1.0 if ecosystem grows. **No corporate sponsor logo on the front page** until post-1.0.

**Reasoning**: Pre-1.0 needs fast decisions, not consensus. Foundation overhead is justified only when the ecosystem can support it. Avoiding corporate-logo association during the trust-positioning phase (S1) is important.

**Confidence**: high. **Reversibility**: moderate.

---

## S11: Brand and naming

**Proposal**: **Defer naming until S1 (wedge) is committed.** Working title is `gonext`. The name should encode the trust positioning, be short (≤8 chars), and have a clean .com or .dev TLD.

**Reasoning**: Naming before positioning produces brand that's misaligned with the product. Name picks have permanence costs (domain, GitHub org, npm packages, trademark). Delay 8 weeks. Spend $100 on a designer when ready.

**Confidence**: high. **Reversibility**: expensive once committed.

---

## S12: Browser support matrix

**Proposal**:
- **Public site**: last 2 versions of Chrome, Firefox, Safari, Edge. IE11 dropped (WP dropped it 2022). Mobile Safari 14+. Chrome Android current.
- **Admin & block editor**: last 1 version of Chrome, Firefox, Safari, Edge. Modern only — admins update their browser.
- **Theme authors free to target their own audience.**

**Reasoning**: A modern CMS shouldn't be paying tax for IE11. Admin is a power-user tool; users have agency over browser choice. Public site needs broader compat because visitors don't.

**Confidence**: high. **Reversibility**: cheap.

---

## S13: Support model

**Proposal**:
- **Community-first**: Discord + GitHub Discussions. No paid tier in v0.x.
- Post-1.0 paid support tier: $500/mo for response-time SLA on self-hosters' issues. Bundle with SaaS plans.
- Premium plugin/theme authors are responsible for their own support; no central escalation.

**Reasoning**: A pre-1.0 project can't sustain a paid support business; trying to do it dilutes engineering attention. Discord is fast, public, and lets early users help each other.

**Confidence**: medium. **Reversibility**: cheap.

---

## S14: Documentation strategy

**Proposal**: **Docs are a separate workstream owned by a docs-dedicated engineer, starting at Phase P2.** Not a side project. Not generated from code.

Structure:
- **User docs** ("Site owner") — install, configure, theme switch, plugin install, settings.
- **Plugin author docs** — full SDK, signing, marketplace publishing, examples, capability reference.
- **Theme author docs** — template hierarchy, theme.json, block patterns, SDK reference.
- **Admin API reference** — OpenAPI-generated, hand-curated examples.
- **Architecture decision records** — public ADRs for major decisions (we can publish docs 00–13 as ADR-style).

Hosted on docs.{brand}.dev, source in monorepo `/docs`.

**Reasoning**: Stripe and Vercel won developer adoption on docs quality. WordPress's docs are a mess and that's a known pain point we can win on. Investing a full FTE on docs is unusual but correct for a CMS.

**Confidence**: high. **Reversibility**: cheap.

---

## S15: Migration completeness target

**Proposal**: **For our launch v1, target 95% lossless content migration from WordPress sites that meet these constraints:**
- Don't use ACF Pro Flexible Content or Repeater nesting >2 levels deep
- Don't use WooCommerce
- Don't use a page builder (Elementor, Divi, Beaver Builder)
- Have <100k posts

For sites outside those constraints: import partial + structured warning report. Explicit "you will lose X" disclosure during import.

**Reasoning**: Promising 100% lossless across all WP sites is a lie that destroys trust. Promising 95% in a defined cohort is honest, achievable, and our marketing copy.

**Confidence**: medium (needs the 10-site corpus from doc 08 §16 to validate). **Reversibility**: cheap.

---

## S16: Performance target vs WordPress

**Proposal — published SLO targets v1**:

| Metric | WordPress (typical) | gonext target | Defense |
|---|---|---|---|
| Cold-cache page TTFB (single post, no plugins) | 200-800ms | <100ms | Go + Postgres directly |
| Warm-cache page TTFB | 50-200ms | <30ms | ISR + tag cache |
| Admin post list (100 posts) | 1-3s | <300ms | UUID v7 indexed list, no plugin overhead |
| Block editor save (10 blocks) | 500-1500ms | <200ms | JSON tree direct write |
| Plugin hook dispatch overhead (10 plugins, 1 hook) | n/a (PHP) | <2ms | WASM cold + cache |

Published on the marketing site. Real numbers reported from our own benchmarks; users can `gonext bench` to verify.

**Reasoning**: Specific numbers with verifiable benchmark beats marketing claims. Doc 07 §20 already specs the `gonext bench` CLI; this gives it a purpose.

**Confidence**: medium (achievable based on stack characteristics, but needs verification). **Reversibility**: cheap.

---

## S17: When is v1 actually launched?

**Proposal**: **v1 launches when:**
1. Phase P0–P6 from doc 00 §6 complete.
2. The 10-site migration corpus (doc 08 §16) passes the verification gate (S15).
3. gn-seo, gn-forms, gn-shop, gn-hello, gn-pro all shipped (S8).
4. Marketplace exists with ≥10 community plugins or themes (any quality bar).
5. Docs (S14) cover all four audiences.
6. We've personally run our own marketing site on it for ≥3 months.

**No date commitment.** Date-driven launches with ambitious scope produce broken launches.

**Reasoning**: Each criterion is a forcing function. #6 (dogfooding) is the most important — if you can't run your own marketing site on it, nobody else can either.

**Confidence**: high. **Reversibility**: cheap.

---

## Summary table

| # | Question | Proposal | Confidence | Reversibility |
|---|---|---|---|---|
| S1 | Wedge | "Trust" — sandboxed plugins | high | expensive |
| S2 | License | Apache 2.0 | high | expensive |
| S3 | First customer | Indie tech bloggers / small SaaS sites | medium | cheap |
| S4 | Repo structure | Monorepo, Go + pnpm workspaces | high | moderate |
| S5 | Distribution | Self-host first, SaaS post-1.0 | high | cheap |
| S6 | Pricing (when SaaS) | $0 / $20/site / $200/team | medium | cheap |
| S7 | Marketplace | 80/20 split, signed plugins, ed25519 license keys | medium | moderate |
| S8 | First-party | 3 plugins + 2 themes | high | cheap |
| S9 | Phase 0 | 4-week vertical slice | high | cheap |
| S10 | Governance | BDFL pre-1.0, foundation post | high | moderate |
| S11 | Brand | Defer 8 weeks after S1 | high | expensive |
| S12 | Browsers | Last 2 mainstream, no IE | high | cheap |
| S13 | Support | Discord-first, paid post-1.0 | medium | cheap |
| S14 | Docs | Dedicated FTE from P2 | high | cheap |
| S15 | Migration completeness | 95% in defined cohort | medium | cheap |
| S16 | Performance target | Published SLOs vs WP | medium | cheap |
| S17 | v1 launch criteria | 6 conditions, no date | high | cheap |

---

## What's still unanswered

These can't be answered without market signal:
- Will Apache vs GPL choice actually move the needle on plugin author adoption? (Need ≥6 months of community signal.)
- Is "trust" or "performance" the stronger lever? (Need beta-user feedback.)
- Does the migration tooling actually convert 95%? (Need the 10-site corpus.)
- Will plugins ship in WASM-from-non-Go languages or will the ecosystem default to Go? (Need 6 months of marketplace data.)

These are unknowable from the desk. Phase P0–P3 should produce enough signal to answer them by month 12.
