# GoNext — Design System

> Sites that *live* and grow.

GoNext is an **all-in-one alternative to WordPress** — content, hosting, and commerce in one product. Built on **Go** (backend) and **Next.js** (frontend), with intelligence woven in.

This design system is **"living systems"** — biology meets software. A cream paper canvas with a deep forest counter-surface, emerald primary, lavender secondary, heavy grotesque headlines paired with italic serif accents for the editorial pop.

---

## Index

| File / Folder | Purpose |
|---|---|
| `colors_and_type.css` | All design tokens. Import first. |
| `assets/` | Logo mark, wordmark, favicon. |
| `fonts/` | Font sources (Google Fonts). |
| `preview/` | Reference cards in the Design System tab. |
| `ui_kits/marketing/` | Marketing homepage. |
| `ui_kits/admin/` | Admin dashboard (CMS posts). |
| `ui_kits/editor/` | Block editor (writing surface). |
| `ui_kits/marketplace/` | Theme & extension marketplace. |
| `ui_kits/templates/` | Sample site templates gallery. |
| `ui_kits/docs/` | Documentation site. |
| `SKILL.md` | Skill manifest. |

---

## Brand premise

GoNext is **the platform that responds**. Sites built on it aren't static — they evolve. Posts version. Content surfaces what's working. Commerce learns. Hosting routes through 24 living regions in real time.

The visual language reflects this: cream warmth instead of clinical white, deep forest instead of pure black, organic gradients instead of flat fills, italic serif accents inside heavy grotesque headlines for moments of *life* in otherwise systematic type.

---

## Visual foundations

### Color
| Token | Hex | Role |
|---|---|---|
| `--paper` | `#F5F2EA` | Page background, warm cream |
| `--paper-2` | `#EFEBE0` | Card / panel surface |
| `--paper-3` | `#E6E1D2` | Sunken, hover |
| `--forest` | `#0E1A14` | Dark surface (sidebar, dark hero) |
| `--forest-2` | `#18261E` | Lifted dark |
| `--ink` | `#0E1A14` | Primary text — same as forest, "alive" black |
| `--fg-muted` | `#4A5C52` | Secondary text |
| `--emerald` | `#10B981` | Primary brand accent |
| `--emerald-bright` | `#34D399` | Emerald on dark surfaces |
| `--emerald-deep` | `#047857` | Emerald text on cream |
| `--lavender` | `#A78BFA` | Secondary accent — data viz, tags |
| `--danger` | `#DC2626` | Destructive only |

### Typography
Three families, doing three jobs:

- **Archivo** (800/900) — heavy grotesque, the headline workhorse. Display, H1, H2.
- **Geist** (400/500/600) — modern UI sans. Body, labels, UI text.
- **Instrument Serif** italic — *the accent*. Used inline inside Archivo headlines for emphasized words, and for stats like "38*ms*" or "$*19*". This single italic serif accent is the brand's signature move.
- **Geist Mono** — code, version strings, IDs.

The pattern looks like this:
```html
<h1>Sites that <em>live</em> and grow.</h1>
```
The `<em>` rule swaps font to Instrument Serif, italic, slightly upsized, with the emerald-deep color. Use sparingly — one italic word per headline, max two.

### Spacing
4-base scale: 4, 8, 12, 16, 20, 24, 32, 48, 64, 96. Tokens `--s-1`…`--s-10`.

### Radii
- `--r-xs` 4px — keyboard hints
- `--r-sm` 6px — tags, small buttons
- `--r-md` 8px — buttons, inputs
- `--r-lg` 12px — cards
- `--r-xl` 16px — modals, hero cards
- `--r-pill` 999px — status pills, nav

### Shadows
Soft, organic — never hard offsets. Four levels: `--sh-xs` resting, `--sh-sm` subtle, `--sh-md` hover, `--sh-lg` modal. Focus ring is emerald-tinted: `--sh-focus`.

### Organic gradients
The brand uses **radial gradients on dark forest surfaces** to evoke biological glow — never on cream surfaces, never as flat decoration. Example pattern: a 600×600px radial of `rgba(16, 185, 129, 0.18)` placed off-canvas top-right of a forest hero card. See `ui_kits/marketing/index.html` `.hero-visual::before`.

---

## Content & voice

Confident, quiet, alive. We're a platform that's done iterating in private — we know what works, we're not arguing.

- "Sites that live and grow." — on brand
- "One product for everything you used five for." — on brand
- "Built on Go and Next.js. Both are good ideas." — on brand
- "Unlock seamless content experiences." — off brand
- "🚀 Supercharge your workflow!" — off brand

The italic moment in copy mirrors the italic moment in type — sparing emphasis, never decorative.

---

## How to use this system

1. Import `colors_and_type.css` in your `<head>`.
2. Load Lucide icons from CDN.
3. Use the wordmark pattern: `Go` in Archivo Black, `Next` in Instrument Serif italic.
4. Reference `ui_kits/admin/` and `ui_kits/marketing/` as canonical density and chrome.

---

## Source & substitutions

- **Fonts**: Archivo, Geist, Instrument Serif, Geist Mono — all free via Google Fonts.
- **Icons**: Lucide via CDN.
- **Photos**: not provided — organic radial gradients used as placeholders.
