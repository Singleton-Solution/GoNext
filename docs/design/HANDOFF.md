# Handoff — GoNext Design System

Everything you need to implement the **GoNext** brand and UI in a real codebase.

## What this is

GoNext is a **WordPress alternative** — an all-in-one platform for content, hosting, and commerce, built on a **Go backend + Next.js frontend**. The product has six surfaces and they're all in this package.

## About the design files

The HTML files in this bundle are **design references**, not production code to copy directly. They are hi-fi prototypes showing intended look, behavior, and content. Your task is to **recreate these designs in the target codebase's environment** (the GoNext repo, presumably Next.js + Go) using its established conventions, component patterns, and libraries — or, if you're starting from scratch, pick the most appropriate framework for the project and implement there.

The single file you should treat as canonical is **`colors_and_type.css`** — the design tokens are the law. Pixel measurements can flex; the tokens, type system, and visual rules should be honored.

## Fidelity

**Hi-fi.** Every screen has final colors, type sizes, spacing, content, and interaction states. The developer should match these pixel-for-pixel using the codebase's existing primitives.

---

## Visual identity in one paragraph

**"Living systems."** Cream paper (`#F5F2EA`) with a deep-forest counter-surface (`#0E1A14`), an emerald primary accent (`#10B981`) and a lavender secondary (`#A78BFA`). Type is **Archivo Black 800** for display headlines paired with **Instrument Serif italic** for emphasized inline words — *the brand's signature move*. Body type is Geist, mono is Geist Mono. Shadows are soft and organic (never hard offsets). Dark surfaces carry organic radial-gradient glows in emerald and lavender to suggest biological intelligence.

### The italic accent rule

This is the move that ties everything together. Inside any heavy Archivo headline, one or two emphasized words swap to Instrument Serif italic:

```html
<h1>Sites that <em>live</em> and grow.</h1>
<h2>One product for everything you used <em>five</em> for.</h2>
<span class="val">38<em>ms</em></span>
```

The `em` rule swaps `font-family` to Instrument Serif, `font-style: italic`, `font-weight: 400`, and uses `--emerald-deep` color on cream, `--emerald-bright` on forest. Use it sparingly — one italic word per headline, max two. It's emphasis, not decoration.

---

## Design tokens (excerpt)

Full set lives in `assets/colors_and_type.css`. The essentials:

### Color
```css
--paper:          #F5F2EA;   /* page background */
--paper-2:        #EFEBE0;   /* cards, panels */
--paper-3:        #E6E1D2;   /* sunken, hover */
--forest:         #0E1A14;   /* dark surface, sidebar */
--forest-2:       #18261E;
--forest-3:       #22322A;
--ink:            #0E1A14;   /* primary text */
--ink-soft:       #1F2D26;
--fg-muted:       #4A5C52;   /* secondary text */
--fg-subtle:      #6B7B72;   /* tertiary */
--border:         #D9D2C0;
--emerald:        #10B981;   /* primary accent */
--emerald-bright: #34D399;   /* emerald on dark */
--emerald-deep:   #047857;   /* emerald text on cream */
--emerald-soft:   #D1FAE5;   /* tinted surface */
--lavender:       #A78BFA;   /* secondary accent */
--lavender-deep:  #7C3AED;
--lavender-soft:  #EDE9FE;
--danger:         #DC2626;
```

### Typography
```css
--font-display: 'Archivo';            /* 800/900 — headlines */
--font-sans:    'Geist';              /* 400/500/600 — UI, body */
--font-serif:   'Instrument Serif';   /* italic — accents */
--font-mono:    'Geist Mono';         /* code, IDs */
```
All four families are free on Google Fonts. The import URL is at the top of `colors_and_type.css`.

### Spacing scale (4-base)
4, 8, 12, 16, 20, 24, 32, 48, 64, 96 — tokens `--s-1` … `--s-10`.

### Radii
`--r-xs:4` `--r-sm:6` `--r-md:8` (default) `--r-lg:12` (cards) `--r-xl:16` (modals/hero) `--r-pill:999`.

### Shadows
Soft, blurred, low-elevation — never hard offsets. Four levels: `--sh-xs` (resting card), `--sh-sm` (subtle), `--sh-md` (hover), `--sh-lg` (modal). Focus ring: `--sh-focus` is an emerald-tinted halo.

### Motion
Ease `cubic-bezier(0.2, 0.7, 0.2, 1)`. Duration `100ms` for instant feedback, `160ms` default, `260ms` for layout shifts. Buttons don't translate on hover — they shift fill color. Quiet, not bouncy.

### Organic gradients (only on dark surfaces)
A signature: 600–800px radial gradients of `rgba(16, 185, 129, 0.10–0.20)` and `rgba(167, 139, 250, 0.08–0.15)`, placed off-canvas on dark forest cards. Pattern in marketing hero and analytics screens. Never on cream surfaces, never as flat decoration.

---

## Screens / Views

All HTML files live in `ui_kits/` and reference `assets/colors_and_type.css`. Open any of them in a browser to see the intended design.

### 1. Marketing site — `ui_kits/marketing/index.html`
**Purpose**: Top-of-funnel homepage covering hero, social proof, features, comparison vs WordPress, pricing, CTA, footer.
**Layout**: Single scroll. Sticky pill-shaped dark nav at the top center. Hero is a 1.3fr/1fr grid (headline left + dark "Pulse" visual card right). Features in a 3-column grid. Comparison table with a tinted GoNext column. Pricing as 3 cards (middle one is forest dark, featured).
**Key components**: Sticky pill nav, dark hero visual with live pulse chart, feature cards with emerald + lavender icon backgrounds, alive-band (full forest section with organic gradient glows), compare table, pricing cards.

### 2. Admin dashboard — `ui_kits/admin/index.html`
**Purpose**: The CMS — listing posts, drafts, scheduled, with status filters and bulk actions.
**Layout**: Three-zone — 248px forest sidebar (logo + org switch + nav + upgrade card + user footer), main content with topbar (52px) and content area. Page head is a full-width display headline with sub + actions. Stats are a 4-column grid. Below that a 3-column "pulse" forest card. Below that the posts table inside a panel.
**Notable**: The page head uses `<h1>Posts, <em>living</em>.</h1>` — italic serif accent in the section title. The pulse card on top of the content area shows the brand's animation/aliveness pattern.

### 3. Pulse / Analytics — `ui_kits/admin/pulse.html`
**Purpose**: Real-time analytics — the brand's most expressive screen.
**Layout**: Full-screen dark forest. Sidebar identical to admin but on dark background. Main is a content stack: page head + live indicator, 4-tile hero stats, a big 24h line chart (emerald views + lavender conversions), a row with a **purple histogram** (sessions × time) and edge-region geo distribution, then bottom row with top posts and a live event feed.
**Critical visual**: The histogram uses lavender bars with the peak bars in emerald-bright. Annotation dashed lines call out median + long-tail readers. This is the moodboard's signature data viz.

### 4. Block editor — `ui_kits/editor/index.html`
**Purpose**: Writing and page-building UI.
**Layout**: Forest topbar (54px, with crumb + view switcher + save state + publish button), then a 52px / 1fr / 320px three-column body. Left rail has block-action icons. Center is a 720px doc on cream paper with editable blocks (title, deck, paragraphs, h2, image block with organic gradient, product card, slash menu). Right inspector has tabs (Block/Document/SEO) and contextual fields.
**Notable**: Block hover shows tools in left margin. Selected block has emerald left-border. Slash menu is shown inline as one of the blocks.

### 5. Marketplace — `ui_kits/marketplace/index.html`
**Purpose**: Theme & extension browsing.
**Layout**: Top nav + hero (headline + big search + note) + filter pill row + 220px sidebar facets + 3-column extension grid.
**Cards**: Each card has a 16:9 thumb (in one of: forest-with-glow, paper-with-rotated-card, emerald glyph, lavender glyph, mono code preview), an icon + title + author, description, and a foot row with price + star rating.

### 6. Templates gallery — `ui_kits/templates/index.html`
**Purpose**: Site template browser — first stop after onboarding.
**Layout**: Top nav + 2-column hero (display headline + meta-list table) + filter pills + featured row (1 large forest card + 2 small) + 3-column gallery grid.
**Each gallery card**: Aspect 4:3 preview rendered as a tiny in-browser scene of the template (editorial, shop, studio dark, docs, portfolio, restaurant menu, landing, zine, studio2). Meta below with title, category, desc, tags, and a "Use →" CTA.

### 7. Docs site — `ui_kits/docs/index.html`
**Purpose**: Documentation reader.
**Layout**: Sticky topbar with brand + Docs label + nav links + search + version pill + GitHub button. Three-column body: 240px left sidenav, 760px content max-width, 220px right TOC. Content is a typical install guide with H1, lead, H2s (auto-numbered with italic serif "01."), code blocks on forest dark, callouts (success + warning), prev/next pager.

### 8. Onboarding — `ui_kits/onboarding/index.html`
**Purpose**: First-90-seconds setup flow, after account creation.
**Layout**: Top strip with 4-step indicator (account ✓ → setup [active] → customize → publish). Center-column hero with welcome headline. Six site-type picker cards (blog, shop [selected], portfolio, landing, docs, local business). Each card has a real mini-preview, icon + title with italic emphasis, description, tags, count. Below: a 2-column "name your site" detail card. Sticky bottom action bar with progress note and Back/Continue.

### 9. Theme Studio — `ui_kits/studio/index.html`
**Purpose**: Live theme customizer — palette, fonts, scale, effects with real-time preview.
**Layout**: Forest topbar (with viewport switcher + save state + publish). Three-column body: 64px left rail with mode tabs (Brand, Theme, Type, Layout, Parts, Pages, Nav, Code), 340px inspector (palette picker, accent swatches, font picker, sliders, toggles), and a preview pane (forest bg, with a paper-card rendering of the customer's actual site).

### 10. UI states — `ui_kits/states/index.html`
**Purpose**: All the in-between states a real product needs.
**Includes**:
- 3 empty states (first run, no products, no search results)
- 2 loading states (skeleton, animated emerald pulse)
- Error 503 + destructive modal
- Toast stack (success/info/danger)
- Dropdown menu
- ⌘K command palette

---

## Component patterns to honor

### Buttons
- `.btn` default — cream `--paper-2` background, ink border, soft shadow
- `.btn.btn--primary` — solid ink background, paper text (the "main" CTA)
- `.btn.btn--emerald` — solid emerald background, dark text (the "active/positive" CTA — start trial, publish)
- `.btn.btn--ghost` — transparent, hover fills with `--paper-3`
- Sizes: `--sm` (5/10 padding) and `--lg` (12/20 padding)

### Tags
- `.tag` default — cream-3 bg, muted text, border
- `.tag--emerald` — emerald-soft bg, emerald-deep text
- `.tag--lavender` — lavender-soft bg, lavender-deep text
- `.tag--success` `--warning` `--danger` — semantic variants
- `.tag--ink` — inverse (dark on light context)
- `.tag--dot` — adds a leading pulse dot

### Inputs
Cream-paper background, hover lifts border, focus shows emerald border + emerald-tinted focus halo (`--sh-focus`).

### Cards
Default is `--paper-2` with `--border`, `--r-lg`, `--sh-xs`. Hover variant lifts to `--sh-md` with `transform: translateY(-2px)`. Featured cards swap to forest background with organic-glow `::before`/`::after` radial gradients.

### Wordmark
```html
<span class="wm-go">Go</span><span class="wm-next">Next</span>
```
`Go` in Archivo 800, `Next` in Instrument Serif italic — same baseline, no space between.

### Logo mark
A `#0E1A14` rounded square (radius 14) holding an organic emerald leaf-arrow glyph (path `M 20 38 Q 28 18, 44 22 Q 36 32, 22 42 Z` filled `#10B981`).

---

## Content & voice

**Confident, quiet, alive.** We're a platform that's done iterating in private — we know what works, we're not arguing.

On-brand:
- "Sites that *live* and grow."
- "One product for everything you used *five* for."
- "Built on Go and Next.js. Both are good ideas."

Off-brand:
- "Unlock seamless content experiences." (fluff)
- "🚀 Supercharge your workflow!" (rocket + verb)
- "Hey friend!" (saccharine)

Always sentence case for headings. No exclamations on headlines. No emoji in product UI.

---

## Stack & dependencies

The HTML mocks pull from CDN — replicate as installed deps in your codebase:

- **Fonts** — `Archivo`, `Geist`, `Instrument Serif`, `Geist Mono` from Google Fonts. Self-host or use `next/font`.
- **Icons** — `lucide` (https://lucide.dev). In React: `pnpm add lucide-react`.
- **CSS approach** — These mocks use plain CSS with custom properties. For React/Next, the recommended port is **CSS variables + Tailwind v4 (which natively reads `@theme`) OR vanilla-extract OR PandaCSS**. The token names should not change — they're the contract.

---

## Suggested folder structure in your codebase

```
src/
  styles/
    tokens.css           ← rename of colors_and_type.css
    globals.css
  components/
    Button.tsx           ← .btn / .btn--primary / .btn--emerald / .btn--ghost
    Tag.tsx              ← .tag + variants
    Input.tsx
    Card.tsx
    Wordmark.tsx         ← the "Go" + italic "Next" composite
    LogoMark.tsx
  features/
    marketing/Home.tsx          ← from ui_kits/marketing
    admin/Posts.tsx             ← from ui_kits/admin
    admin/Pulse.tsx             ← from ui_kits/admin/pulse
    editor/Editor.tsx           ← from ui_kits/editor
    marketplace/Browse.tsx
    templates/Gallery.tsx
    onboarding/Setup.tsx
    studio/Customizer.tsx
    docs/Layout.tsx
  primitives/
    EmptyState.tsx
    LoadingPulse.tsx
    Toast.tsx
    Dropdown.tsx
    CommandPalette.tsx
```

---

## Files in this bundle

```
HANDOFF.md                       ← you are here
README.md                        ← the broader project README
SKILL.md                         ← Claude-skill manifest (optional reference)
assets/
  colors_and_type.css            ← THE design tokens — canonical
  logo-mark.svg
  logo-wordmark.svg
  favicon.svg
ui_kits/
  marketing/index.html
  admin/index.html
  admin/pulse.html
  editor/index.html
  marketplace/index.html
  templates/index.html
  docs/index.html
  onboarding/index.html
  studio/index.html
  states/index.html
preview/                         ← single-component reference cards
  type-*.html
  colors-*.html
  spacing-*.html
  comp-*.html
  brand-*.html
```

When in doubt, open the HTML files in a browser and inspect the styles. Every value is in `colors_and_type.css` as a token — never hardcoded.
