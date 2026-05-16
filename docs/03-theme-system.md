# Theme System

> Subsystem doc #03. Read [`00-architecture-overview.md`](00-architecture-overview.md) first. Reader: senior frontend engineer who knows Next.js App Router and WordPress theming.

This document specifies how themes are authored, packaged, resolved, rendered, customized, and extended. The TL;DR: **themes are React component packages** rendered by a Next.js App Router app, with a template hierarchy borrowed from WordPress, optional database-backed templates for full-site editing (FSE), and a `theme.json` design-token manifest. Plugins (doc #02) inject blocks and extension points that themes consume but do not own.

---

## 1. Goals & Non-Goals

### Goals
- **WordPress-familiar mental model.** Authors who know WP theming should recognize the template hierarchy, `theme.json`, parent/child themes, template parts, customizer.
- **Two authoring modes.** *Classic themes* (code-first, files on disk) and *block themes* (data-first, templates editable in the admin Site Editor).
- **First-class TypeScript.** Theme authors get typed props, typed `theme.json`, and a typed block schema.
- **Server-first rendering.** Templates are React Server Components by default; interactive bits are explicit client islands.
- **Hot-swappable.** Switching themes does not require a server restart. Themes are isolated enough that a broken theme falls back gracefully.
- **Child themes & forks.** Override any template, any part, any token, without forking the whole tree.

### Non-Goals (v1)
- **No PHP-style runtime template engine** (Twig, Liquid, etc.). React is the template language.
- **No live in-browser code execution for theme files** (Stackblitz-style sandboxes). Themes are installed packages.
- **No multi-theme-per-site routing** (different themes for different paths) in v1. One active theme + one preview theme.
- **No theme-defined PHP-like REST endpoints.** That's plugins' job.

---

## 2. Theme Package Format

A theme is a directory (and, when distributed, a tarball / npm package / zip). The on-disk layout:

```
my-theme/
├── theme.json                # design tokens, supports, layout — see §3
├── package.json              # npm metadata; "gonext" key identifies it as a theme
├── README.md
├── LICENSE
├── screenshot.png            # 1200×900 thumbnail shown in admin theme switcher
├── translations/
│   ├── en.json
│   ├── fr.json
│   └── ja.json
├── assets/
│   ├── fonts/
│   │   └── Inter-Variable.woff2
│   ├── images/
│   │   └── logo.svg
│   └── styles/
│       └── extra.css         # optional global CSS (rarely needed; prefer theme.json)
├── templates/                # top-level templates resolved by the hierarchy
│   ├── index.tsx             # mandatory: ultimate fallback
│   ├── front-page.tsx
│   ├── home.tsx
│   ├── single.tsx
│   ├── single-product.tsx
│   ├── page.tsx
│   ├── page-about.tsx
│   ├── archive.tsx
│   ├── category.tsx
│   ├── category-news.tsx
│   ├── tag.tsx
│   ├── taxonomy-color.tsx
│   ├── author.tsx
│   ├── date.tsx
│   ├── search.tsx
│   └── 404.tsx
├── parts/                    # reusable composition units (header, footer, sidebar, …)
│   ├── header.tsx
│   ├── footer.tsx
│   ├── sidebar.tsx
│   └── post-meta.tsx
├── patterns/                 # block patterns the theme contributes to the editor
│   ├── hero-cta.json
│   └── three-column-features.json
├── styles/                   # alternate style variations (block theme feature)
│   ├── default.json
│   ├── dark.json
│   └── high-contrast.json
├── block-style-variations/   # extra visual styles for core/plugin blocks
│   └── button-pill.json
└── functions.ts              # optional: hook subscriptions, custom registrations
```

Notes:

- **Every theme MUST ship `theme.json` and `templates/index.tsx`.** Everything else is optional.
- **File names are the contract.** The router scans `templates/` and resolves by name, identical to WordPress's PHP template hierarchy.
- **`functions.ts`** is the rough equivalent of WP's `functions.php`. It runs at theme-load time on the Next.js server and may register block patterns, subscribe to theme hooks (e.g., to filter rendered output), and declare supported features. It does **not** run per-request; per-request logic belongs in template components.
- **`package.json`** identifies the package as a theme with a `gonext` key:

```jsonc
{
  "name": "@acme/hello-gonext",
  "version": "1.0.0",
  "type": "module",
  "main": "./functions.ts",
  "gonext": {
    "kind": "theme",
    "engineVersion": ">=1.0.0 <2.0.0",
    "type": "block",            // "block" | "classic"
    "parent": null,             // for child themes: "@acme/parent-theme"
    "textDomain": "hello-gonext"
  },
  "peerDependencies": {
    "@gonext/theme-sdk": "^1.0.0",
    "react": "^19",
    "next": "^15"
  }
}
```

- The `engineVersion` is a semver range that gates installation against the host's CMS version.
- `peerDependencies` are satisfied by the host. Themes do not bundle React/Next.

---

## 3. `theme.json` Specification

`theme.json` is the **design-token manifest**. It defines the palette, scale, layout constraints, and which block-level features the theme opts into. The block editor reads it to populate UI; the renderer reads it to emit CSS custom properties.

### 3.1 Full example

```jsonc
{
  "$schema": "https://gonext.dev/schemas/theme.json/v1",
  "version": 1,
  "title": "Hello GoNext",
  "settings": {
    "appearanceTools": true,
    "color": {
      "palette": [
        { "slug": "ink",       "name": "Ink",       "color": "#0f172a" },
        { "slug": "paper",     "name": "Paper",     "color": "#ffffff" },
        { "slug": "muted",     "name": "Muted",     "color": "#64748b" },
        { "slug": "accent",    "name": "Accent",    "color": "#2563eb" },
        { "slug": "accent-fg", "name": "On Accent", "color": "#ffffff" }
      ],
      "gradients": [
        { "slug": "sunset",
          "name": "Sunset",
          "gradient": "linear-gradient(135deg, #f59e0b, #ef4444)" }
      ],
      "custom": true,
      "customGradient": true,
      "duotone": []
    },
    "typography": {
      "fontFamilies": [
        { "slug": "sans",
          "name": "Sans",
          "fontFamily": "Inter, ui-sans-serif, system-ui",
          "fontFace": [
            { "src": "/assets/fonts/Inter-Variable.woff2",
              "fontWeight": "100 900",
              "fontStyle": "normal",
              "fontDisplay": "swap" }
          ]},
        { "slug": "serif",
          "name": "Serif",
          "fontFamily": "Iowan Old Style, Apple Garamond, Baskerville, serif" }
      ],
      "fontSizes": [
        { "slug": "sm",  "name": "Small",  "size": "0.875rem" },
        { "slug": "md",  "name": "Medium", "size": "1rem"     },
        { "slug": "lg",  "name": "Large",  "size": "1.25rem"  },
        { "slug": "xl",  "name": "X-Large","size": "1.75rem"  },
        { "slug": "2xl", "name": "Display","size": "2.5rem", "fluid": { "min": "2rem", "max": "3.5rem" } }
      ],
      "lineHeight": true,
      "letterSpacing": true,
      "textDecoration": true
    },
    "spacing": {
      "units": ["px", "rem", "em", "%", "vw"],
      "spacingScale": {
        "operator": "*",
        "increment": 1.5,
        "steps": 7,
        "mediumStep": 1.5,
        "unit": "rem"
      },
      "padding": true,
      "margin": true,
      "blockGap": true
    },
    "layout": {
      "contentSize": "720px",
      "wideSize":    "1180px"
    },
    "border": {
      "color": true,
      "radius": true,
      "style": true,
      "width": true
    },
    "shadow": {
      "presets": [
        { "slug": "soft",  "name": "Soft",  "shadow": "0 1px 2px rgba(0,0,0,.05)" },
        { "slug": "lifted","name": "Lifted","shadow": "0 8px 24px rgba(0,0,0,.12)" }
      ]
    },
    "blocks": {
      "core/button": {
        "border": { "radius": true },
        "color":  { "background": true, "text": true }
      }
    }
  },
  "styles": {
    "color": { "background": "var(--gn-color-paper)", "text": "var(--gn-color-ink)" },
    "typography": {
      "fontFamily": "var(--gn-font-sans)",
      "fontSize":   "var(--gn-font-md)",
      "lineHeight": "1.6"
    },
    "elements": {
      "h1": { "typography": { "fontSize": "var(--gn-font-2xl)", "lineHeight": "1.1" } },
      "h2": { "typography": { "fontSize": "var(--gn-font-xl)" } },
      "link": { "color": { "text": "var(--gn-color-accent)" } }
    },
    "blocks": {
      "core/button": {
        "color": { "background": "var(--gn-color-accent)", "text": "var(--gn-color-accent-fg)" },
        "border": { "radius": "0.5rem" }
      }
    }
  },
  "supports": {
    "blockTemplates": true,
    "siteEditor":     true,
    "darkModeAuto":   true,
    "customizer":     true,
    "menus":          ["primary", "footer"],
    "widgetAreas":    ["sidebar-main", "footer-1", "footer-2"]
  },
  "patterns": ["hero-cta", "three-column-features"],
  "customTemplates": [
    { "name": "page-landing", "title": "Landing Page", "postTypes": ["page"] }
  ],
  "templateParts": [
    { "name": "header", "title": "Header", "area": "header" },
    { "name": "footer", "title": "Footer", "area": "footer" }
  ]
}
```

### 3.2 What the renderer does with this

At theme-load time, the host computes a derived **token sheet** — a `<style>` block of CSS custom properties — and injects it into every rendered page in the document head:

```css
:root {
  --gn-color-ink:  #0f172a;
  --gn-color-paper:#ffffff;
  --gn-color-muted:#64748b;
  --gn-color-accent:#2563eb;
  --gn-color-accent-fg:#ffffff;

  --gn-font-sans:  Inter, ui-sans-serif, system-ui;
  --gn-font-serif: Iowan Old Style, Apple Garamond, Baskerville, serif;

  --gn-font-sm:  0.875rem;
  --gn-font-md:  1rem;
  --gn-font-lg:  1.25rem;
  --gn-font-xl:  1.75rem;
  --gn-font-2xl: clamp(2rem, 1rem + 2vw, 3.5rem);

  --gn-space-1: 0.5rem;  /* derived from spacingScale */
  /* … steps 2..7 … */

  --gn-layout-content: 720px;
  --gn-layout-wide:    1180px;
}
```

The same tokens drive editor previews (the admin loads a stripped version of the theme's stylesheet) and are exposed via a `useTheme()` hook in the SDK for theme authors that want token values at runtime.

### 3.3 Differences from WordPress's `theme.json`

We borrow most of WP's structure but:

- **Drop `version: 2` versioning legacy.** Start at `version: 1`.
- **No `templates` / `templateParts` *content* in `theme.json`.** WP allows you to ship inline templates here; we keep templates in the `templates/` and `parts/` directories or in DB rows. `theme.json` only *declares* which template parts and custom templates exist.
- **Typed via JSON Schema** (`$schema`), validated at install. Authoring tools get autocomplete.
- **`supports` is collapsed**: where WP has features sprinkled across `add_theme_support()` calls and `theme.json`, we keep all of it in `supports`.
- **`spacing.spacingScale` is the only blessed way** to define spacing. WP's "presets vs scale" duality is gone.

---

## 4. Template Hierarchy

The hierarchy is the heart of the theme system. Given a request, the router walks an ordered list of template names and picks the first match present in `templates/` (classic) or in the DB (block theme), falling back to parent theme then core defaults.

### 4.1 Naming convention

Template names are kebab-case, lowercase, with the post type / taxonomy / slug embedded:

```
single-{postType}-{slug}.tsx        most specific
single-{postType}.tsx
single.tsx
index.tsx                            ultimate fallback (mandatory)
```

### 4.2 Full resolution order per request type

#### Singular (single post / page / CPT)
```
1.  single-{postType}-{slug}.tsx        e.g. single-post-hello-world.tsx
2.  single-{postType}-{id}.tsx          e.g. single-post-42.tsx
3.  single-{postType}.tsx               e.g. single-post.tsx
4.  singular.tsx                        any single item
5.  single.tsx                          legacy alias for singular
6.  index.tsx
```

For pages specifically, an additional branch:
```
1.  page-{slug}.tsx                     e.g. page-about.tsx
2.  page-{id}.tsx
3.  page-{template}.tsx                 if the post has a customTemplate assignment (e.g. page-landing.tsx)
4.  page.tsx
5.  singular.tsx
6.  index.tsx
```

#### Archives (post-type archive)
```
1.  archive-{postType}.tsx              e.g. archive-product.tsx
2.  archive.tsx
3.  index.tsx
```

#### Taxonomy archives (categories, tags, custom taxonomies)
```
1.  taxonomy-{tax}-{term-slug}.tsx      e.g. taxonomy-color-red.tsx
2.  taxonomy-{tax}.tsx                  e.g. taxonomy-color.tsx
3.  taxonomy.tsx
4.  archive.tsx
5.  index.tsx
```

For the built-in `category` and `post_tag` taxonomies, friendlier names exist as aliases:
```
category-{slug}.tsx / category-{id}.tsx / category.tsx
tag-{slug}.tsx / tag-{id}.tsx / tag.tsx
```

#### Author archive
```
1.  author-{username}.tsx
2.  author-{id}.tsx
3.  author.tsx
4.  archive.tsx
5.  index.tsx
```

#### Date archive
```
1.  date-{year}-{month}-{day}.tsx       (rarely used)
2.  date-{year}-{month}.tsx
3.  date-{year}.tsx
4.  date.tsx
5.  archive.tsx
6.  index.tsx
```

#### Search results
```
1.  search-{postType}.tsx               when the search is scoped
2.  search.tsx
3.  index.tsx
```

#### Front page & blog home
```
Front page (site root):
  1. front-page.tsx
  2. home.tsx        (if "latest posts" mode)
  3. page-{slug}.tsx (if "static page" mode → slug of the chosen page)
  4. page.tsx
  5. index.tsx

Blog home (posts page, when a static front page is set):
  1. home.tsx
  2. index.tsx
```

#### 404
```
1.  404.tsx
2.  index.tsx
```

### 4.3 Resolution flow (ASCII)

```
            ┌─────────────────────────┐
            │ HTTP request → URL      │
            └────────────┬────────────┘
                         ▼
            ┌─────────────────────────┐
            │ Router resolves URL to: │
            │  - queryType            │   singular | archive | tax |
            │  - postType / taxonomy  │   author | date | search |
            │  - slug / id / term     │   404 | front-page | home
            └────────────┬────────────┘
                         ▼
            ┌─────────────────────────────┐
            │ Build ordered candidate list│
            │ per §4.2 for queryType      │
            └────────────┬────────────────┘
                         ▼
            ┌─────────────────────────┐
            │ For each candidate name:│
            │                         │
            │   Block theme?          │
            │   ├─ yes → DB lookup    │
            │   │        for active   │
            │   │        theme + name │
            │   └─ no  → file lookup  │
            │            in theme dir │
            │                         │
            │   Hit? → render         │
            │   Miss? → next candidate│
            └────────────┬────────────┘
                         ▼
            ┌─────────────────────────┐
            │ All candidates exhausted│
            │ in child theme? walk up │
            │ to parent theme.        │
            │ Still nothing?  fall to │
            │ core/default templates. │
            └────────────┬────────────┘
                         ▼
            ┌─────────────────────────┐
            │ Component imported &    │
            │ rendered with data from │
            │ the Go API.             │
            └─────────────────────────┘
```

### 4.4 The resolver in code

```ts
// @gonext/theme-runtime/src/resolver.ts

export interface ResolvedQuery {
  type: 'singular'|'archive'|'taxonomy'|'author'|'date'|'search'|'404'|'home'|'frontPage';
  postType?: string;
  slug?: string;
  id?: string | number;
  taxonomy?: string;
  term?: string;
  author?: string;
  date?: { year: number; month?: number; day?: number };
  searchScope?: string;
  customTemplate?: string;
}

export function buildCandidates(q: ResolvedQuery): string[] {
  switch (q.type) {
    case 'singular': {
      if (q.postType === 'page') {
        return compact([
          q.slug && `page-${q.slug}`,
          q.id && `page-${q.id}`,
          q.customTemplate && `page-${q.customTemplate}`,
          `page`,
          `singular`,
          `index`,
        ]);
      }
      return compact([
        q.slug && `single-${q.postType}-${q.slug}`,
        q.id && `single-${q.postType}-${q.id}`,
        `single-${q.postType}`,
        `singular`,
        `single`,
        `index`,
      ]);
    }
    case 'archive':
      return compact([`archive-${q.postType}`, `archive`, `index`]);
    case 'taxonomy':
      if (q.taxonomy === 'category')
        return compact([q.term && `category-${q.term}`, `category`, `archive`, `index`]);
      if (q.taxonomy === 'post_tag')
        return compact([q.term && `tag-${q.term}`, `tag`, `archive`, `index`]);
      return compact([
        q.term && `taxonomy-${q.taxonomy}-${q.term}`,
        `taxonomy-${q.taxonomy}`,
        `taxonomy`,
        `archive`,
        `index`,
      ]);
    case 'author':
      return compact([q.author && `author-${q.author}`, `author`, `archive`, `index`]);
    case 'date': {
      const parts: string[] = [];
      const d = q.date!;
      if (d.day)   parts.push(`date-${d.year}-${d.month}-${d.day}`);
      if (d.month) parts.push(`date-${d.year}-${d.month}`);
      parts.push(`date-${d.year}`, `date`, `archive`, `index`);
      return parts;
    }
    case 'search':
      return compact([q.searchScope && `search-${q.searchScope}`, `search`, `index`]);
    case 'frontPage':
      return ['front-page', 'home', 'page', 'index'];
    case 'home':
      return ['home', 'index'];
    case '404':
      return ['404', 'index'];
  }
}

const compact = <T,>(a: (T | undefined | null | false)[]) =>
  a.filter(Boolean) as T[];
```

A separate `loadTemplate(themeId, name)` function tries the active theme, then its parent chain, then core defaults:

```ts
export async function resolveTemplate(theme: ThemeHandle, names: string[]) {
  for (const name of names) {
    let t = theme;
    while (t) {
      const hit = t.kind === 'block'
        ? await db.findTemplate(t.id, name)
        : await fs.findTemplateFile(t.dir, `${name}.tsx`);
      if (hit) return hit;
      t = t.parent;
    }
  }
  return coreDefaults.index;       // last resort: ships with the host
}
```

---

## 5. Template Parts

Template parts are reusable composition units — header, footer, sidebar, post meta block — shared across templates.

### 5.1 In classic themes

Plain React components in `parts/`:

```tsx
// parts/header.tsx
import { SiteTitle, NavMenu } from '@gonext/theme-sdk';

export default function Header() {
  return (
    <header className="site-header">
      <SiteTitle />
      <NavMenu location="primary" />
    </header>
  );
}
```

Templates import them directly:

```tsx
// templates/index.tsx
import Header from '../parts/header';
import Footer from '../parts/footer';

export default function Index({ posts }: ArchiveProps) {
  return (
    <>
      <Header />
      <main>{posts.map(p => <PostCard key={p.id} post={p} />)}</main>
      <Footer />
    </>
  );
}
```

### 5.2 In block themes

Parts are block trees stored in the DB (table `template_parts`, see doc #01 for schema sketch). The file-based parts in the theme package are **seeds** loaded on theme install; once seeded, the admin Site Editor edits the DB copy. Templates reference parts by `area` + `name`:

```jsonc
// templates/index.json — block-theme template seed
{
  "name": "index",
  "blocks": [
    { "type": "core/template-part", "attributes": { "area": "header", "name": "header" } },
    { "type": "core/query",
      "attributes": { "postType": "post", "perPage": 10 },
      "innerBlocks": [
        { "type": "core/post-template",
          "innerBlocks": [
            { "type": "core/post-title", "attributes": { "isLink": true } },
            { "type": "core/post-excerpt" }
          ]
        }
      ]
    },
    { "type": "core/template-part", "attributes": { "area": "footer", "name": "footer" } }
  ]
}
```

The renderer (doc #04) walks this tree. When it encounters `core/template-part`, it loads the named part — again from DB first, then theme file, then parent theme.

### 5.3 Areas

Parts declare an `area` (`header`, `footer`, `sidebar`, or `uncategorized`). The Site Editor uses areas to surface logical regions; classic themes ignore areas entirely.

---

## 6. Block Themes (Full-Site Editing)

A block theme delegates **everything** — including headers, footers, and the index template — to the block editor. Templates and parts live in the DB as block trees.

### 6.1 Storage model

Two tables (sketched here, owned by doc #01):

```sql
CREATE TABLE template (
  id           bigserial PRIMARY KEY,
  theme_id     text NOT NULL,            -- which theme this belongs to
  name         text NOT NULL,            -- e.g. 'single-post'
  blocks       jsonb NOT NULL,           -- block tree
  source       text NOT NULL CHECK (source IN ('seed','user')),
  origin_hash  text,                     -- hash of the seed file at install time
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),
  UNIQUE (theme_id, name, source)
);

CREATE TABLE template_part (
  id           bigserial PRIMARY KEY,
  theme_id     text NOT NULL,
  name         text NOT NULL,
  area         text NOT NULL,
  blocks       jsonb NOT NULL,
  source       text NOT NULL CHECK (source IN ('seed','user')),
  origin_hash  text,
  UNIQUE (theme_id, name, source)
);
```

### 6.2 Seeding on install

When a block theme is activated:

1. Host scans `templates/*.json` and `parts/*.json` in the theme package.
2. For each, computes a content hash, inserts a row with `source='seed'`.
3. The admin Site Editor only ever **reads** `source='seed'` rows and **writes** `source='user'` rows. The seed copy is the immutable origin.

### 6.3 Resolution order with overrides

For a given (theme, name), the resolver picks the first hit in this order:

```
1. template     where theme_id=active     AND name=N AND source='user'    ← user-edited override
2. template     where theme_id=active     AND name=N AND source='seed'    ← file-shipped default
3. template     where theme_id=parent     AND name=N AND source='user'    ← parent theme user-edits (rare)
4. template     where theme_id=parent     AND name=N AND source='seed'    ← parent theme defaults
5. core default for name                                                  ← host built-in
```

This is identical to the classic-theme chain, just with two layers per theme (user-edits then seeds).

### 6.4 Re-seeding on theme update

When a theme is upgraded:

- For each shipped template/part, recompute `origin_hash`.
- If the new hash differs from the stored seed's hash **and** no user override exists for that name → replace the seed.
- If a user override exists → keep the override, but flag the seed as "updated, [Reset to default] available."
- Never silently overwrite a user edit.

### 6.5 Reverting

The Site Editor exposes "Reset to theme default" per template/part, which deletes the `source='user'` row.

---

## 7. Classic Themes

Classic themes ship code-first: every template is a `.tsx` file, nothing is editable in the admin. They exist because:

- Some devs want full programmatic control (custom data fetching, complex layouts).
- They're trivially version-controllable and reviewable.
- They sidestep the entire DB-seeding dance for sites that don't need FSE.

A classic theme declares `"type": "classic"` in `package.json`. The host:

- Does **not** seed any templates/parts into the DB.
- Does **not** show the Site Editor in the admin sidebar (only the Customizer, see §8).
- Resolves templates via filesystem only.

A classic theme can still consume blocks in content (posts/pages are still block trees), but the *templates* are React components written by hand.

---

## 8. Customizer

The Customizer is a live-preview panel for non-code customization. It's the surface that survives from "classic" WordPress and the only theme-config UI for classic themes; block themes get both the Customizer (for global settings) and the Site Editor (for templates).

### 8.1 What it controls

| Section | Applies to | Notes |
|---|---|---|
| Site identity | both | Title, tagline, logo, favicon. Stored in `site_options`, not theme-specific. |
| Colors | both | Overrides to `theme.json` palette. Stored per theme. |
| Typography | both | Font family / size overrides. |
| Header | classic | Layout variant, sticky on/off, transparent on/off (theme-declared options). |
| Footer | classic | Same. |
| Menus | both | Assign menus to declared locations (`primary`, `footer`, …). |
| Widgets / sidebar areas | classic | Drag blocks into declared sidebar areas. (See §10.) |
| Homepage | both | Static page vs latest posts; which page. |
| Additional CSS | both | Free-form CSS injected last. |
| Custom theme options | both | Anything the theme registers via `defineCustomizerSection()`. |

### 8.2 How preview works

The Customizer is **the same Next.js render** as the live site, with two changes:

1. The page is loaded in an `<iframe>` from `/_preview?nonce=…&revision=…`.
2. A `<ThemeOverridesProvider>` injects pending settings from the Customizer panel into a React context. The theme runtime reads from this context first, then falls back to persisted values.

```tsx
// @gonext/theme-runtime/src/preview.tsx
export function ThemeOverridesProvider({ overrides, children }) {
  return (
    <ThemeContext.Provider value={mergeWithBase(overrides)}>
      {children}
    </ThemeContext.Provider>
  );
}

// In the theme:
import { useThemeToken } from '@gonext/theme-sdk';
function Hero() {
  const accent = useThemeToken('color', 'accent');
  return <div style={{ background: accent }}>…</div>;
}
```

When the user drags a color picker, the parent window posts `{type:'override', path:'color.palette.accent', value:'#0ea5e9'}` to the iframe over `postMessage`. The iframe updates the context and React re-renders. **No reload.**

When the user clicks "Save," the overrides POST to the Go backend, which:

- Validates the patch against `theme.json` schema.
- Persists to `site_options.theme_customizations[themeId]`.
- Invalidates ISR cache for affected paths (typically `/*` because tokens are global).
- Returns the new revision; the iframe drops overrides and re-fetches.

### 8.3 Authoring custom Customizer sections

```ts
// my-theme/functions.ts
import { defineCustomizerSection } from '@gonext/theme-sdk';

export const customizer = [
  defineCustomizerSection({
    id: 'hero',
    title: 'Hero',
    controls: [
      { id: 'hero.style',   type: 'select',
        label: 'Hero style', choices: ['simple','split','full-bleed'], default: 'simple' },
      { id: 'hero.image',   type: 'media',  label: 'Background image' },
      { id: 'hero.overlay', type: 'color',  label: 'Overlay color', default: '#000000' },
    ],
  }),
];
```

These values are typed and exposed via `useCustomizerValue('hero.style')`. The admin reads the schema to render controls; no theme-specific admin code required.

---

## 9. Menus & Navigation

Menus are first-class entities, decoupled from posts/pages.

### 9.1 Data model (sketch — owned by doc #01)

```sql
CREATE TABLE menu (
  id          bigserial PRIMARY KEY,
  name        text NOT NULL,
  slug        text NOT NULL UNIQUE,
  created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE menu_item (
  id          bigserial PRIMARY KEY,
  menu_id     bigint NOT NULL REFERENCES menu(id) ON DELETE CASCADE,
  parent_id   bigint REFERENCES menu_item(id) ON DELETE CASCADE,
  position    int NOT NULL,
  label       text NOT NULL,
  target      jsonb NOT NULL,         -- {kind:'post', id:42} | {kind:'term', taxonomy:'category', id:7} | {kind:'url', href:'…'} | {kind:'archive', postType:'product'}
  rel         text,                   -- nofollow, etc.
  classes     text[]
);

CREATE TABLE menu_location_assignment (
  theme_id    text NOT NULL,
  location    text NOT NULL,          -- 'primary' | 'footer' | …
  menu_id     bigint NOT NULL,
  PRIMARY KEY (theme_id, location)
);
```

### 9.2 Theme integration

A theme declares supported menu locations in `theme.json` → `supports.menus`. The admin shows a "Manage menus" UI that lets the user create menus and assign one menu to each declared location.

In templates:

```tsx
import { NavMenu } from '@gonext/theme-sdk';
<NavMenu location="primary" className="site-nav" />
```

`<NavMenu>` is an RSC. It calls the Go API for the menu assigned to `location`, walks the tree, resolves each `target` to a URL, and renders an accessible nav. Theme authors can override rendering by passing a `render` prop:

```tsx
<NavMenu location="primary" render={({ items }) => (
  <ul className="custom-nav">
    {items.map(i => <li key={i.id}><a href={i.url}>{i.label}</a></li>)}
  </ul>
)} />
```

In block themes, `core/navigation` is the editor-facing block that wraps the same primitives.

### 9.3 URL resolution

When a menu item targets `{kind:'post', id:42}`, the Go side resolves it to the post's permalink at read time and caches the result. If the slug changes, the cache invalidates and the link updates — no broken URLs.

---

## 10. Widgets & Sidebar Areas

WordPress's widgets are dynamic blocks living in sidebar areas. They predate the block editor and largely duplicate it.

**Recommendation: deprecate widgets in v1, ship a compatibility wrapper.**

- v1 themes can declare `supports.widgetAreas` for backward conceptual compatibility, but a "widget area" is *just a slot that holds a block tree*. The user edits it in the Customizer (classic themes) or the Site Editor (block themes).
- The host ships a `core/sidebar-area` block whose attribute is the area's slug; it renders the configured block tree at that slot.
- No separate "widget" registration API. Plugins that want to ship sidebar content register blocks (doc #04).
- The WP importer (doc #08) maps legacy widgets to block equivalents (Text widget → Paragraph block, etc.) and dumps any unknown widgets into a `core/html` block as a fallback.

This collapses two concepts into one and removes ~30% of WP's theming surface.

---

## 11. Child Themes

Child themes override a parent theme **file-by-file**.

### 11.1 Manifest

```jsonc
{
  "name": "@acme/hello-child",
  "gonext": {
    "kind": "theme",
    "type": "block",
    "parent": "@acme/hello-gonext",
    "engineVersion": ">=1.0.0"
  }
}
```

### 11.2 Override rules

| Concern | Behavior |
|---|---|
| Template file (classic) | If `templates/single.tsx` exists in child, use it. Else fall back to parent's. Resolution is per-name, not per-tree. |
| Template / part (block theme) | DB resolver chain (§6.3) walks child → parent at each (`user`, `seed`) layer. |
| `theme.json` | **Deep-merged** with parent. Child can override individual tokens, add new ones, or null out parent ones (`"color": { "palette": null }` removes the parent palette). |
| `functions.ts` | Both run. Parent first, then child. Child can subscribe to the same hooks and override. |
| `assets/` | Child assets take precedence at same path. No automatic enqueueing — themes import what they need. |
| `patterns/` | Union: parent patterns + child patterns. Same slug → child wins. |

### 11.3 Resolver pseudocode

```ts
function resolveFile(theme: Theme, relPath: string): string | null {
  let t: Theme | null = theme;
  while (t) {
    const p = path.join(t.dir, relPath);
    if (fs.existsSync(p)) return p;
    t = t.parent ? loadTheme(t.parent) : null;
  }
  return null;
}
```

Theme.json merging is a deep recursive merge with the child as the override layer, special-cased for arrays (e.g., palettes are merged by `slug`, not by index).

---

## 12. Theme Installer & Switcher

### 12.1 Installation paths

1. **Zip upload (admin UI).** Upload `theme.zip` → host extracts to `themes/{theme-id}/` under the install dir → validates → registers.
2. **npm-style install from registry.** `gonext theme install @acme/hello-gonext` resolves a tarball from the official registry (https://registry.gonext.dev, mirrored on npm) and unpacks it the same way.
3. **Local path (dev mode).** A `GONEXT_THEMES_DIR` env var; the host watches the directory and hot-reloads on file changes.

All three end up in the same place: `themes/{id}/` on disk + a `theme` row in Postgres.

### 12.2 Validation

On install, the host:

1. Verifies `package.json` has `gonext.kind === "theme"`.
2. Validates `theme.json` against the v1 JSON Schema.
3. Compiles every `.tsx` template through a sandboxed build step (esbuild) to surface syntax errors before activation.
4. Greps templates for forbidden imports (`node:fs`, `node:child_process`, `next/headers` cookies write APIs, etc.). Themes get a curated subset of Next.js APIs; the linter rejects the rest at install.
5. Checks `engineVersion` against the host's version.
6. Computes a SHA-256 of the package contents for tamper detection.

### 12.3 Security considerations

Themes execute server-side code in the same Next.js process. This is a smaller attack surface than plugins (which are explicitly third-party WASM), but still not trivial:

- The forbidden-import lint is **not a security boundary**, it's a guardrail. A determined author can bypass it.
- Therefore: **only admins can install themes**, and theme installation is logged.
- The official registry signs published themes. Self-hosted installs warn ("unsigned theme") and require an extra confirmation.
- Long-term, we may move theme code into a sub-process per render, but that's expensive and not v1.

### 12.4 Switcher UI

The admin "Appearance → Themes" page lists installed themes (one card each, with `screenshot.png`). Switching is a single API call:

```http
POST /api/v1/admin/themes/active
{ "themeId": "@acme/hello-gonext" }
```

The host:

1. Writes to `site_options.active_theme`.
2. If the new theme is a block theme and has never been seeded → seed templates/parts.
3. Triggers a full ISR purge (every cached page).
4. Returns 200; the admin refreshes.

There's no restart. The Next.js theme registry (next item) re-keys and serves the new templates on the next request.

---

## 13. Server-Side Rendering

### 13.1 Where the public site lives

A single dynamic catch-all route:

```
apps/public-site/
└── src/app/
    ├── layout.tsx                # Document shell, injects theme.json tokens + base CSS
    ├── _preview/page.tsx         # Customizer iframe target
    ├── api/                      # internal endpoints (revalidate, healthz)
    └── [[...slug]]/page.tsx      # everything else
```

### 13.2 The catch-all

```tsx
// app/[[...slug]]/page.tsx
import { resolveQuery, resolveTemplate, fetchData } from '@gonext/theme-runtime';

export default async function Page({ params }: { params: { slug?: string[] } }) {
  const url = '/' + (params.slug?.join('/') ?? '');
  const q   = await resolveQuery(url);                  // → ResolvedQuery
  const T   = await resolveTemplate(q);                 // → React component
  const d   = await fetchData(q);                       // → typed props
  return <T {...d} />;
}

export async function generateMetadata({ params }) {
  const q = await resolveQuery('/' + (params.slug ?? []).join('/'));
  return buildMetadata(q);                              // SEO, OG, canonical
}

export const revalidate = 60;                           // ISR baseline
```

### 13.3 Data fetching

Two modes, picked per call site:

**(a) RSC fetches the Go API over HTTP.** Default. Simple, cacheable, debuggable. Next.js's `fetch` cache deduplicates within a render and persists across requests until invalidated.

```tsx
async function fetchData(q: ResolvedQuery): Promise<TemplateProps> {
  const res = await fetch(`${INTERNAL_API}/v1/render?${qs(q)}`, {
    next: { tags: cacheTags(q) },
  });
  return res.json();
}
```

**(b) Direct in-process call when colocated.** If the Next.js public site and Go API are deployed as a single binary (long-term option via a Go HTTP handler that proxies to Next.js's standalone server), RSCs can hit an internal endpoint over `http://127.0.0.1`. Same shape, no network hop.

We do **not** ship a Go-from-Node FFI bridge. Keeps the boundary clean.

### 13.4 ISR & on-demand revalidation

- **Time-based ISR** is the baseline: `revalidate = 60`. Even un-mutated pages re-render at least once a minute.
- **On-demand revalidation** is wired from the Go backend. Whenever a post/page/term/menu/template is mutated:
  1. Go publishes an event on the internal hooks bus.
  2. A small core "next-revalidate" plugin (in-process, not WASM) listens and POSTs to the Next.js `/api/revalidate` endpoint with affected tags.
  3. Next.js `revalidateTag()` clears them.

Cache tags follow a hierarchy:

```
post:42                       individual post
posttype:post                 entire post-type archive
term:category:42              term archive
menu:primary                  navigation
theme:active                  global tokens
site:*                        nuclear (used on theme switch)
```

### 13.5 Edge runtime feasibility

The catch-all template is dynamic React + a JSON fetch. It *could* run on the edge. But:

- The Go API must be reachable from the edge with low latency, or the win evaporates.
- Theme code is untyped at the edge level — we'd need a strict subset (no Node.js APIs in themes, ever).
- Block render functions (doc #04) can be arbitrarily complex; some plugins will register blocks with Node-only deps.

**Verdict:** the route is Node runtime by default. Themes can opt into `export const runtime = 'edge'` on a per-template basis if they pass an edge-compatibility lint. Don't promise edge in v1 marketing.

### 13.6 Streaming

Templates that fetch multiple independent resources should compose them with `<Suspense>`. The SDK ships `<PostStream>`, `<ArchiveStream>` helpers that wrap a Suspense boundary + skeleton + error boundary:

```tsx
<PostStream id={postId}>
  {(post) => <Article post={post} />}
</PostStream>
```

---

## 14. Styling

Theme CSS is a long-tail problem. We support multiple styles of styling so authors don't have to abandon their habits.

### 14.1 The default: tokens-from-`theme.json` + per-template CSS modules

- The host emits CSS custom properties from `theme.json` into `<head>`. Every theme gets `--gn-color-*`, `--gn-font-*`, `--gn-space-*` for free.
- Theme `.tsx` files can colocate `.module.css` files (Next.js handles them natively).
- Block render functions emit class names from a shared `@gonext/block-styles` package.

This is the recommended path for the reference theme.

### 14.2 Tailwind

Themes that prefer Tailwind ship their own `tailwind.config.ts` referencing the same tokens:

```ts
// my-theme/tailwind.config.ts
import { tokensToTailwind } from '@gonext/theme-sdk/tailwind';
import themeJson from './theme.json';

export default {
  content: ['./templates/**/*.tsx', './parts/**/*.tsx'],
  theme: { extend: tokensToTailwind(themeJson) },
  plugins: [],
};
```

`tokensToTailwind` maps `color.palette[].slug` → `colors.{slug}` (referencing the CSS var, not the hex), etc. The Customizer keeps working because Tailwind classes resolve to `var(--wpc-…)` which the iframe override hot-swaps.

The host does **not** bundle Tailwind. The theme builds its CSS at install time (a `prepare` step in `package.json`), the output is served as a static asset.

### 14.3 Vanilla CSS

`assets/styles/extra.css` is unconditionally enqueued if present. Use it for resets or one-off rules. Discouraged for design tokens — `theme.json` is canonical.

### 14.4 Inline styles from tokens (RSC pattern)

Sometimes a component needs a token at render time. `useThemeToken()` returns the resolved value (post-Customizer-override) without a hooks/client boundary:

```tsx
const accent = useThemeToken('color', 'accent');
return <hr style={{ borderColor: accent }} />;
```

### 14.5 Recommendation

**Default: CSS modules + `theme.json` tokens.** Tailwind opt-in. CSS-in-JS libraries (styled-components, Emotion) are discouraged because they hydrate every page; we don't ban them but the docs steer authors away.

---

## 15. TypeScript Types for Theme Authors

The `@gonext/theme-sdk` package ships:

### 15.1 Template prop types

```ts
// @gonext/theme-sdk/src/templates.ts
export interface PostProps<TFields = Record<string, unknown>> {
  post: {
    id: number;
    type: string;
    slug: string;
    title: string;
    excerpt: string;
    content: BlockTree;
    author: AuthorRef;
    publishedAt: string;
    updatedAt: string;
    terms: Record<string, TermRef[]>;     // by taxonomy
    meta: TFields;
    permalink: string;
  };
  siblings: { prev?: PostRef; next?: PostRef };
  comments?: CommentTree;
}

export interface ArchiveProps {
  query: ResolvedQuery;
  posts: PostRef[];
  pagination: { page: number; perPage: number; total: number };
  archive?: TermRef | AuthorRef | { year: number; month?: number };
}

export interface SearchProps extends ArchiveProps {
  searchTerm: string;
}

export interface NotFoundProps { url: string; }
```

Themes declare their template signatures:

```tsx
import type { PostProps } from '@gonext/theme-sdk';
type ProductMeta = { price: number; sku: string; inStock: boolean };
export default function SingleProduct({ post }: PostProps<ProductMeta>) {
  // post.meta is typed
  return <article>${post.meta.price.toFixed(2)} — {post.meta.sku}</article>;
}
```

### 15.2 Typed `theme.json`

```ts
import { defineTheme } from '@gonext/theme-sdk';
export default defineTheme({
  version: 1,
  settings: { /* … */ },
  styles:   { /* … */ },
});
```

`defineTheme` is an identity function with a generic `ThemeJson` type. Authoring `theme.json` as a `.ts` file (it's allowed) gets full autocomplete; the build emits the JSON.

### 15.3 Block tree renderer

```tsx
import { Blocks } from '@gonext/theme-sdk';
<Blocks tree={post.content} />
```

The component dispatches to registered block renderers (core + plugin) and falls back to an HTML pass-through for unknown types.

### 15.4 Hook subscriptions

Themes can subscribe to a subset of plugin-system hooks (filters that affect rendered output, mostly):

```ts
// functions.ts
import { addFilter } from '@gonext/theme-sdk';

addFilter('render.post.title', (title, { post }) => {
  return post.meta.draft ? `[Draft] ${title}` : title;
}, { priority: 10 });
```

These run server-side at render time, in the same process.

---

## 16. Reference Theme: "Hello GoNext"

The reference theme demonstrates every feature the system expects authors to use.

### 16.1 Files

```
hello-gonext/
├── theme.json
├── package.json
├── README.md
├── screenshot.png
├── functions.ts
├── translations/
│   ├── en.json
│   └── fr.json
├── assets/
│   ├── fonts/Inter-Variable.woff2
│   └── images/placeholder.svg
├── templates/
│   ├── index.tsx              # archive of latest posts
│   ├── front-page.tsx         # marketing-style hero + featured posts
│   ├── single.tsx             # single post
│   ├── page.tsx               # default page
│   ├── page-landing.tsx       # custom-template landing page (registered in theme.json)
│   ├── archive.tsx            # generic archive
│   ├── category.tsx           # category archive
│   ├── search.tsx             # search results
│   └── 404.tsx
├── parts/
│   ├── header.tsx
│   ├── footer.tsx
│   ├── post-meta.tsx
│   └── sidebar.tsx
└── patterns/
    ├── hero-cta.json
    ├── three-column-features.json
    └── newsletter-signup.json
```

### 16.2 Look

- Single accent color (default `#2563eb`), generous whitespace, type-driven.
- Single typeface (Inter variable), 5-step type scale.
- Max content width 720px (prose), wide width 1180px (media).
- Soft drop-shadow on cards, 0.5rem radius.
- Dark-mode swap controlled by `supports.darkModeAuto` + a token override file `styles/dark.json`.

### 16.3 What it proves

| Feature | Demonstrated by |
|---|---|
| Template hierarchy | `single`, `page`, `page-landing`, `archive`, `category`, `search`, `404` |
| Template parts | `header`, `footer`, `post-meta`, `sidebar` |
| Custom page template | `page-landing.tsx` + declaration in `theme.json.customTemplates` |
| `theme.json` tokens | full palette/typography/spacing/layout, all referenced in styles |
| Block patterns | three patterns the editor surfaces |
| Style variations | `styles/default.json` + `styles/dark.json` |
| Block style variations | a `button-pill` variation for `core/button` |
| Customizer custom section | a "Hero" section with style/image/overlay controls |
| Menu locations | `primary`, `footer` |
| Typed meta | `single-product.tsx` example commented out, showing typed `PostProps<Meta>` |
| Hook subscription | `addFilter('render.post.title', …)` in `functions.ts` adding `[Draft]` prefix |

Two variants ship: a **block theme** version (templates are `.json` block trees) and a **classic** version (same UX, hand-written `.tsx`). Together they're the canonical reference for theme authors.

---

## 17. Trade-offs & Rejected Alternatives

### Rejected: pure file-based templates (no FSE)
Simpler implementation, but a huge product regression vs WordPress. Non-developers cannot tweak headers and footers without editing code. The Site Editor is one of WP's strongest recent wins; abandoning it would push us into "developer-only CMS" territory and make the WP migration story weaker.

### Rejected: a runtime template engine (Twig, Liquid, custom DSL)
Sandboxing is trivial, hot-reload is trivial, non-devs can read templates. But: every dynamic block becomes a custom directive, we'd reimplement React's composition model badly, and theme authors can't leverage the React ecosystem (Suspense, RSCs, hooks). React-as-templates is the strategic choice tied to the rest of the stack.

### Rejected: pure GraphQL headless with custom frontend per site
This is what Faust/Frontity and similar try. It moves all theming into a separate codebase, which scales for agencies but devastates the no-code/low-code market. WordPress's reach is largely **because** users don't need to think about a frontend. We keep the bundled renderer and let GraphQL be available for those who want headless.

### Rejected: theme runtime as a separate Node process per theme
Would give us hard isolation. But: huge memory overhead, complex IPC, and themes aren't really untrusted in the same way plugins are — they're admin-installed and code-reviewable. We accept the in-process risk and lint aggressively at install.

### Rejected: a CSS-in-JS-only styling story
Best DX in isolation, worst runtime cost. We'd lock out non-React-native styling preferences (Tailwind, CSS modules, plain CSS) and slow down hydration. The token-based approach gives 80% of the DX benefit (typed, scoped) without the runtime tax.

### Rejected: storing templates as MDX
Tempting — MDX would make templates feel content-like. But: MDX semantics around block rendering are awkward, and we already have a block model in the editor. Two block-tree formats (MDX + our blocks) would fragment tooling. JSON block trees are the source of truth.

### Rejected: theme-defined backend routes
A theme that ships its own REST endpoints would blur into plugin territory. We refuse this: themes are presentation, plugins are behavior. Authors who need both ship a paired plugin.

---

## 18. Open Questions

1. **Theme registry**: do we host one (registry.gonext.dev) or piggyback on npm with a `gonext-theme` tag convention? Hosting our own gives us signing, moderation, screenshots-in-search. Piggybacking is free.
2. **Edge runtime opt-in**: is the per-template `runtime = 'edge'` opt-in actually safe? We'd need a static analyzer to verify no Node-only API is reachable from a given template + its imports. Build it now or defer?
3. **Theme.json in code (`.ts`) vs JSON file**: dual support is easy but doubles documentation surface. Pick one and lint the other away?
4. **Plugin-block style overrides**: when a theme wants to restyle a block from a plugin it doesn't know about, the only path today is global CSS or a child-theme override via `theme.json.styles.blocks[plugin/block]`. Is that enough? Should plugins declare "themable surfaces"?
5. **Customizer for block themes**: do we keep both Customizer and Site Editor, or fold global settings (colors, typography) into the Site Editor and retire the Customizer for block themes? WP is trending toward "Site Editor for everything," but the Customizer has a much smaller learning curve.
6. **Server actions for theme settings**: should theme authors be able to handle form submissions via Next.js Server Actions, or must they go through the Go API? Server Actions break the "themes are presentation" rule but are extremely ergonomic.
7. **Hot reload for theme authors**: how good can we make the dev loop? File watcher in `GONEXT_THEMES_DIR` + Next.js fast refresh works, but DB-stored templates (block themes) don't have files to watch. We'd need a "watch DB and trigger HMR" bridge.
8. **Multi-language theme content**: translations are file-based per locale (`translations/{lang}.json`). Should the Customizer expose translated strings the theme author hard-coded, or only translated content (posts/pages handled by core i18n)?
9. **Bundle splitting**: should each template route ship its own JS bundle, or do we accept one shared bundle? Per-template splitting helps for marketing pages with rare templates; shared simplifies caching.
10. **Theme test harness**: do we ship a `@gonext/theme-test` package that mounts a theme against synthetic fixtures so authors can run `vitest` against their templates? Probably yes, but the scope is non-trivial.
