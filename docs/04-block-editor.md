# 04 — Block Editor

> Subsystem owner: Block editor (Gutenberg-equivalent, redesigned for sanity).
> Depends on: [00 — Architecture Overview](00-architecture-overview.md), [01 — Core CMS & Data Model](01-core-cms.md), [02 — Plugin System](02-plugin-system.md), [03 — Theme System](03-theme-system.md).
> Reader: senior frontend engineer who has used Gutenberg, Notion, Lexical, ProseMirror.

This document is the design for the editor that authors use to write posts, pages, and (for block themes) the entire site. It is the most-touched surface in the product — the place authors live. Getting it right is the single biggest UX bet in this project.

We are not embedding Gutenberg. We are not copying Gutenberg's serialization. We are taking the **block paradigm** (which is the right paradigm) and rebuilding it on a clean data model with a maintained rich-text engine and a renderer split that actually matches how modern React apps render content.

---

## 0. TL;DR

- **Data model**: JSON block tree stored in `posts.content_blocks` (JSONB). Server materializes `posts.content_rendered` (HTML cache) on save and on dynamic-block invalidation.
- **Block API**: `registerBlockType({ name, title, attributes, edit, save?, render? })` — JSON Schema for attributes, React for `edit`, optional React `save` (static blocks) or server `render` (dynamic blocks).
- **Rich text**: **Lexical** (Meta), recommended over TipTap for tree composability and performance. Inline formats live as marks on text leaves, not as nested blocks.
- **Editor shell**: React app (in the admin Next.js bundle). Three regions: canvas, top toolbar, right inspector. Block inserter via `/` (slash), `+` button, or sidebar.
- **Server render**: Go walks the JSON tree; static blocks render to HTML by template; dynamic blocks call into WASM (plugin) or Go (core) render functions.
- **Plugins**: register blocks via the plugin SDK (frontend ES module for `edit`, optional WASM `render` for server output).
- **Patterns, reusable blocks, locking, templates, block context** — all first-class.
- **Real-time collab**: deferred to v2; data model is CRDT-compatible (block tree maps cleanly onto a Yjs Y.Map of Y.Map nodes).

---

## 1. Block Data Model

### 1.1 The block, in TypeScript

```ts
// shared/types/block.ts

/** A block name is namespaced: "core/paragraph", "wp-seo/breadcrumbs". */
export type BlockName = `${string}/${string}`;

/**
 * The canonical, persisted shape of a block.
 * This is what lands in posts.content_blocks (JSONB).
 */
export interface Block<
  Name extends BlockName = BlockName,
  Attrs extends object = Record<string, unknown>,
> {
  /** Stable identity. Generated client-side as ULID. Survives edits, used as React key,
   *  used as the anchor for revisions and (later) CRDT positions. */
  id: string;

  /** Registry key. The renderer dispatches on this. */
  type: Name;

  /** Validated against the block's JSON Schema. Anything not in the schema is dropped on save. */
  attributes: Attrs;

  /** Child blocks. Empty array if the block doesn't accept children.
   *  Absent (undefined) for leaf-only blocks — explicit is cheaper than implicit. */
  innerBlocks: Block[];

  /** Schema version of this block's attributes. Used by the deprecation/migration pipeline. */
  version: number;

  /** Optional. Present if the block declares a context provider (see §12). */
  context?: Record<string, unknown>;
}

/** The full document is just an array of root blocks. No wrapping envelope. */
export type BlockDocument = Block[];
```

The whole content of a post **is** `Block[]`. No wrapper object, no envelope. The post row holds metadata; the body is the array.

### 1.2 Concrete example

```json
[
  {
    "id": "01JCM3X0K6PQRG5Y7E5DZJ4H1Z",
    "type": "core/heading",
    "version": 1,
    "attributes": { "level": 1, "text": "Hello world" },
    "innerBlocks": []
  },
  {
    "id": "01JCM3X0K6RZD8E2F1P3X9B4QA",
    "type": "core/paragraph",
    "version": 1,
    "attributes": {
      "text": {
        "ops": [
          { "insert": "This is " },
          { "insert": "bold", "marks": ["bold"] },
          { "insert": " and " },
          { "insert": "linked", "marks": [{ "type": "link", "href": "https://x.com" }] },
          { "insert": "." }
        ]
      },
      "align": "left"
    },
    "innerBlocks": []
  },
  {
    "id": "01JCM3X0K6TRJWQ6N8AB5MX2VV",
    "type": "core/columns",
    "version": 1,
    "attributes": { "count": 2, "gap": "md" },
    "innerBlocks": [
      {
        "id": "01JCM3X0K7A1...",
        "type": "core/column",
        "version": 1,
        "attributes": { "width": "50%" },
        "innerBlocks": [ /* nested blocks */ ]
      },
      {
        "id": "01JCM3X0K7A2...",
        "type": "core/column",
        "version": 1,
        "attributes": { "width": "50%" },
        "innerBlocks": []
      }
    ]
  }
]
```

The rich-text representation inside `attributes.text` is the Lexical/Quill-style operations list (see §4). The block layer does not parse HTML — it composes typed primitives.

### 1.3 Why a JSON tree beats WP's HTML-with-comments

WordPress serializes a block document as HTML annotated with comment delimiters:

```html
<!-- wp:paragraph {"align":"left"} -->
<p style="text-align:left">This is <strong>bold</strong>.</p>
<!-- /wp:paragraph -->
```

This is genuinely clever — it degrades gracefully if the editor is unavailable, the body still renders as valid HTML, and old WP themes don't care. But it has five problems that compound:

1. **You have to parse it to do anything programmatic.** Every read (search, transformation, migration, query) starts with `wp_blocks_parse()`. The parser is a regex-and-state-machine pile that has had real bugs.
2. **Storage is denormalized.** Attributes live in JSON-inside-HTML-comments. Postgres can't index `attributes.align`. You can't `SELECT posts WHERE blocks @> '[{"type":"core/gallery"}]'`.
3. **Validation is post-hoc.** Gutenberg validates on edit by re-running `save()` and comparing strings. Mismatch → "this block contains unexpected content" — every WP dev has stared at this.
4. **Plugin edits are unsafe.** Any code that touches the body works on a string. Two plugins editing the same post can each produce valid HTML that breaks the block parser when combined.
5. **The HTML *is* the source of truth.** That means a developer who hand-edits HTML in the code view can corrupt the structured form. The editor then either rejects the edit or silently mangles it.

Our model:

| Concern | WP (HTML+comments) | Ours (JSON tree) |
|---|---|---|
| Parse cost on read | Always | Never (already JSON) |
| Postgres indexability | None | JSONB GIN indexes |
| Schema validation | Compare-rendered-HTML | JSON Schema, declarative |
| Plugin-safe edits | String surgery | Tree surgery, IDs as anchors |
| Source of truth | HTML body | The tree |
| Rendered HTML | Live | Cached, materialized |

The trade-off: we lose "open the DB row, see HTML, paste it elsewhere." That's worth losing. The materialized `content_rendered` column gives us back the read-side benefit without making it authoritative.

### 1.4 Storage: `posts.content_blocks` and `posts.content_rendered`

From [01 — Core CMS](01-core-cms.md), the relevant columns:

```sql
ALTER TABLE posts ADD COLUMN content_blocks   JSONB NOT NULL DEFAULT '[]'::JSONB;
ALTER TABLE posts ADD COLUMN content_rendered TEXT  NOT NULL DEFAULT '';
ALTER TABLE posts ADD COLUMN content_rendered_at TIMESTAMPTZ;
ALTER TABLE posts ADD COLUMN content_blocks_hash BYTEA;
-- GIN index for "which posts contain a given block type"
CREATE INDEX posts_blocks_gin ON posts USING GIN (content_blocks jsonb_path_ops);
```

Save flow:

```
client editor                         Go API                          Postgres
─────────────                         ──────                          ────────
PUT /posts/:id  ──blocks JSON──▶
                              │
                              ▼
                     1. Validate vs registry
                        (per-block JSON Schema)
                     2. Migrate any out-of-date
                        block versions (§8)
                     3. Compute hash(blocks)
                     4. Render static blocks ────▶ HTML chunks
                        Render dynamic blocks
                        (Go or WASM) ─────────────▶ HTML chunks
                     5. Concatenate HTML  ─────────▶ content_rendered
                              │
                              ▼
                                                    UPDATE posts SET
                                                      content_blocks = $1,
                                                      content_rendered = $2,
                                                      content_blocks_hash = $3,
                                                      content_rendered_at = now()
```

Dynamic blocks (Query, Latest Posts, Navigation) can't be cached at save time because their output depends on data that changes independently. They render to **placeholder markers** in `content_rendered`:

```html
<div data-dyn-block="core/query" data-dyn-id="01JCM..."></div>
```

The public site renderer (Next.js) treats `content_rendered` as the body but substitutes dynamic markers at render time by calling the Go render endpoint for that block instance. ISR + tag-based invalidation handles freshness.

We accept the storage doubling. Postgres TOAST compression handles it well; for an average post of 5 KB blocks JSON + 8 KB rendered HTML, doubling is fine.

---

## 2. Block Registration

### 2.1 Frontend API (admin / editor side)

```ts
// @platform/blocks/registry.ts

import type { ReactElement } from "react";
import type { JSONSchema7 } from "json-schema";

export interface BlockSupports {
  /** Block can contain other blocks. */
  innerBlocks?: boolean;
  /** If innerBlocks, restrict allowed children (whitelist of block names). */
  allowedChildren?: BlockName[];
  /** Block exposes a colour control in the inspector. */
  color?: { background?: boolean; text?: boolean };
  /** Block participates in spacing controls. */
  spacing?: { margin?: boolean; padding?: boolean };
  /** Block can be aligned (left/center/right/wide/full). */
  align?: ("left" | "center" | "right" | "wide" | "full")[];
  /** Block is HTML-editable (raw HTML escape hatch). Defaults false. */
  html?: boolean;
  /** Block is reusable / can be turned into a synced pattern. */
  reusable?: boolean;
  /** Block can be locked (move/remove disabled). */
  lock?: boolean;
}

export interface BlockDeprecation<OldAttrs, NewAttrs> {
  /** The old attribute schema. */
  attributes: JSONSchema7;
  /** Migrate old attributes into the new shape. */
  migrate: (old: OldAttrs) => NewAttrs;
  /** Optional check: does this old block match? Defaults to "attributes validate against this schema". */
  isEligible?: (attrs: unknown) => boolean;
}

export interface BlockTypeDefinition<Attrs extends object> {
  /** "core/paragraph", "my-plugin/pricing-table". */
  name: BlockName;
  /** Human-readable title shown in the inserter. */
  title: string;
  /** One-line description for the inserter. */
  description?: string;
  /** Lucide icon name or React element. */
  icon: string | ReactElement;
  /** Inserter category. */
  category: "text" | "media" | "design" | "widgets" | "theme" | "embed" | string;
  /** Keywords for inserter search. */
  keywords?: string[];
  /** Schema for attributes. Validated on save. */
  attributes: JSONSchema7;
  /** Current schema version. Increment when shape changes (see §8). */
  version: number;
  /** Capability matrix. */
  supports?: BlockSupports;
  /** Default attribute values for newly inserted instances. */
  defaults?: Partial<Attrs>;
  /** Editable React component. Required. */
  edit: React.ComponentType<BlockEditProps<Attrs>>;
  /** Static block: deterministic React → HTML. Use save() XOR render(). */
  save?: React.ComponentType<BlockSaveProps<Attrs>>;
  /** Dynamic block: server renders this. Provide the server-side handler name; the
   *  frontend uses a placeholder in the editor preview. */
  render?: { handler: string /* "core/query.render" or plugin-namespaced */ };
  /** Schema migrations from prior versions. Ordered newest → oldest. */
  deprecated?: BlockDeprecation<any, Attrs>[];
  /** Style variations registered by themes (see Theme doc). */
  styles?: { name: string; label: string; isDefault?: boolean }[];
  /** Block-level context this block provides to descendants. */
  providesContext?: Record<string, string /* attribute path */>;
  /** Block-level context this block consumes from ancestors. */
  usesContext?: string[];
  /** Optional transforms: paste rules, block-to-block conversions. */
  transforms?: BlockTransforms<Attrs>;
}

export function registerBlockType<Attrs extends object>(
  def: BlockTypeDefinition<Attrs>,
): void;
```

`BlockEditProps` and `BlockSaveProps`:

```ts
export interface BlockEditProps<Attrs> {
  attributes: Attrs;
  setAttributes: (patch: Partial<Attrs>) => void;
  /** True when this block is the active selection. */
  isSelected: boolean;
  /** Stable ID. */
  clientId: string;
  /** Context resolved from ancestors (see §12). */
  context: Record<string, unknown>;
  /** For container blocks: render slot for inner blocks. */
  InnerBlocks: React.ComponentType<InnerBlocksProps>;
}

export interface BlockSaveProps<Attrs> {
  attributes: Attrs;
  /** Same component contract on save side, but no setAttributes / isSelected. */
  InnerBlocks: { Content: React.ComponentType<{}> };
}
```

### 2.2 Core blocks (v1)

| Block | Type | Notes |
|---|---|---|
| Paragraph | static | RichText, align, drop cap, colors |
| Heading | static | level 1–6, anchor id, align |
| List | static | Ordered/unordered, nesting via innerBlocks |
| Quote | static | Inner paragraph(s) + cite |
| Code | static | Language, copy button, no exec |
| Preformatted | static | Plain `<pre>` |
| Table | static | Header row, footer row, fixed/auto layout |
| Image | static | Media library picker, alt, caption, sizes |
| Gallery | static | Inner Image blocks, columns |
| Video | static | URL or upload, poster, captions |
| Audio | static | URL or upload |
| File | static | Download link |
| Cover | static | Bg image/video + inner blocks overlay |
| Media & Text | static | Two-column media+content |
| Embed | dynamic | oEmbed; resolution server-side |
| HTML | static | Raw HTML escape hatch (sanitized) |
| Columns / Column | static | innerBlocks containers |
| Group | static | innerBlocks container w/ layout (flow/flex/grid) |
| Button(s) | static | Single or set; link, style |
| Separator | static | hr |
| Spacer | static | Fixed-height vertical gap |
| More | static | "Read more" cut marker for archives |
| Page Break | static | Paginates the post on render |
| Navigation | dynamic | Pulls a navigation menu from the CMS |
| Post Content | dynamic | FSE: renders the queried post's body |
| Post Title | dynamic | FSE: renders the queried post's title |
| Post Date | dynamic | FSE |
| Post Author | dynamic | FSE |
| Query | dynamic | Loop: query posts, render inner template per result |
| Query Pagination | dynamic | Pagination chrome for nearest Query ancestor |
| Template Part | dynamic | Includes a named template part (header, footer) |

### 2.3 Sample custom block

```ts
// plugins/wp-pricing/src/blocks/pricing-table.tsx
import { registerBlockType } from "@platform/blocks";
import { PricingEdit } from "./pricing-edit";
import { PricingSave } from "./pricing-save";
import { DollarSign } from "lucide-react";

interface Tier {
  name: string;
  price: number;
  features: string[];
  highlighted: boolean;
}

interface PricingAttrs {
  currency: "USD" | "EUR" | "GBP";
  period: "month" | "year";
  tiers: Tier[];
}

registerBlockType<PricingAttrs>({
  name: "wp-pricing/pricing-table",
  title: "Pricing Table",
  description: "A responsive pricing table with up to four tiers.",
  icon: <DollarSign />,
  category: "widgets",
  keywords: ["pricing", "tiers", "subscription"],
  version: 2,
  attributes: {
    type: "object",
    additionalProperties: false,
    required: ["currency", "period", "tiers"],
    properties: {
      currency: { type: "string", enum: ["USD", "EUR", "GBP"] },
      period: { type: "string", enum: ["month", "year"] },
      tiers: {
        type: "array",
        minItems: 1,
        maxItems: 4,
        items: {
          type: "object",
          additionalProperties: false,
          required: ["name", "price", "features", "highlighted"],
          properties: {
            name: { type: "string", maxLength: 64 },
            price: { type: "number", minimum: 0 },
            features: { type: "array", items: { type: "string", maxLength: 256 } },
            highlighted: { type: "boolean" },
          },
        },
      },
    },
  },
  defaults: {
    currency: "USD",
    period: "month",
    tiers: [
      { name: "Starter", price: 9, features: ["1 user"], highlighted: false },
      { name: "Pro",     price: 29, features: ["5 users"], highlighted: true  },
    ],
  },
  supports: { align: ["wide", "full"], reusable: true, lock: true },
  edit: PricingEdit,
  save: PricingSave,
  deprecated: [
    {
      // v1 used a single `cycle` enum instead of `period`.
      attributes: { /* v1 schema */ } as any,
      migrate: (old: any): PricingAttrs => ({
        currency: old.currency ?? "USD",
        period: old.cycle === "yearly" ? "year" : "month",
        tiers: old.tiers ?? [],
      }),
    },
  ],
});
```

The `edit` and `save` components are pure React. They live in the plugin's ES module bundle (loaded into the admin via the import map, see [02 — Plugin System](02-plugin-system.md)). For dynamic rendering, the plugin would also export a WASM `render` function with handler name `wp-pricing/pricing-table.render`.

### 2.4 Available blocks for a given post type (fixed per review — gap B3)

The editor needs to know **which blocks may be inserted into the current post**. The computation is:

```
available_for(post_type) =
    core_blocks
  ∪ plugin_registered_blocks (filtered to active plugins)
  ∩ post_type.supports_blocks            -- from doc 01 §1.3, may be NULL (= no filter)
  − any block whose owning plugin is deactivated
```

That is: union the core registry and all active-plugin block registrations, then intersect with the post type's `supports.blocks` allow-list (the `supports_blocks TEXT[]` column on `post_types`, doc 01 §10.4 — `NULL` means "no per-type restriction"). Glob entries like `core/*` or `my-plugin/*` are expanded against the unioned set.

The admin queries this at editor mount via `GET /api/v1/post-types/{name}/blocks`, which returns the resolved `BlockTypeDefinition[]` (without the React components, which arrive via the import map). The same endpoint is the canonical answer to "what blocks can I insert here?" — it is consumed by the inserter (§3.2) and by the templates engine (§13.2) when validating a CPT's `template: Block[]`. The endpoint is keyed by `(post_type, plugin_set_version)` so it caches cheaply.

---

## 3. Editor UX

### 3.1 Layout

```
┌───────────────────────────────────────────────────────────────────────────────┐
│  Top bar: [≡] [+] [↶] [↷] [Outline] [👁 Preview]    title…    [Save] [Publish]│
├──┬─────────────────────────────────────────────────────────────────┬──────────┤
│  │ ┌─────────────────────────────────────────────────────────────┐ │  Block   │
│≡ │ │ Block toolbar (floats above selected block)                 │ │  / Doc   │
│  │ │  [¶▾] [B I U S] [Link] [Align▾] [⋯ More]                   │ │          │
│  │ └─────────────────────────────────────────────────────────────┘ │ ┌──────┐ │
│  │                                                                 │ │Block │ │
│L │                                                                 │ │ tab  │ │
│i │      ┌────────────────────────────────────────────────────┐    │ ├──────┤ │
│s │      │                                                    │    │ │Style │ │
│t │      │  ▍ The selected block highlights with a left rail  │    │ │ ▢ 1  │ │
│  │      │                                                    │    │ │ ▣ 2  │ │
│V │      └────────────────────────────────────────────────────┘    │ ├──────┤ │
│i │                                                                 │ │Color │ │
│e │                                                                 │ │ Bg ●  │ │
│w │                                                                 │ │ Tx ●  │ │
│  │                                                                 │ ├──────┤ │
│  │                                                                 │ │Layout│ │
│  │                                                                 │ │ ...  │ │
│  │                                                                 │ ├──────┤ │
│  │                                                                 │ │Adv.  │ │
│  │                                                                 │ │ id   │ │
│  │                                                                 │ │ class│ │
│  │                                                                 │ └──────┘ │
└──┴─────────────────────────────────────────────────────────────────┴──────────┘
```

Three regions, all React, all in the admin Next.js bundle:

1. **Canvas** (middle): an iframe whose document is styled by the active theme's editor stylesheet. Why iframe: so theme CSS scoped to the post body actually applies, and so the editor chrome doesn't bleed in. (Gutenberg made this transition. We start from there.)
2. **Top toolbar**: global doc actions, undo/redo, preview, publish.
3. **Right inspector**: tabs `Block` / `Document`. Block tab shows per-block controls (Style variations, Color, Typography, Layout, Advanced). Document tab shows post-level (status, slug, schedule, taxonomies, featured image, custom fields, discussion).

Optional **left sidebar** holds the list view (document outline) and the inserter when pinned.

### 3.2 Inserter

Three entry points, one unified component:

- **`+` button** in the top bar (opens a panel with categories + search + patterns + media library).
- **`+` between blocks** (hover indicator). Inline popover variant.
- **Slash command** (`/`). Typing `/` at the start of an empty paragraph (or anywhere with modifier? we lock it to start-of-paragraph for predictability) replaces it with a slash menu.

```ts
interface InserterItem {
  id: string;            // "block:core/heading" or "pattern:hero-1" or "reusable:42"
  kind: "block" | "pattern" | "reusable" | "media";
  title: string;
  description?: string;
  icon: ReactElement;
  keywords: string[];
  category: string;
  /** Score for ranking, computed from query + recency + frecency. */
  score: number;
}
```

Inserter behavior:

- Search ranks by exact prefix > token match > fuzzy. Ties broken by frecency (most recently used).
- The "Most used" group at top is per-user, persisted via user preferences.
- "Patterns" and "Reusable" tabs alongside "Blocks" and "Media".

### 3.3 Block toolbar

Float-above-selected-block, anchored to the block's bounding box. Contents are composed from:

- **Transform menu** (paragraph → heading, list → quote, etc.) driven by `transforms`.
- **Move up / down** (kept for accessibility — drag isn't enough).
- **Alignment** (if `supports.align`).
- **Inline rich text controls** (Bold, Italic, Strike, Code, Link, Inline image, More).
- **Block-specific buttons** the block injects via a `<BlockToolbar.Slot>` component (e.g. Image block adds "Replace", "Crop").
- **More menu** (Duplicate, Lock, Group, Save as Pattern, Delete).

### 3.4 Document outline / List view

Tree view of the entire document. Each row: indent, block icon, block title (or first text), context actions.

- Click to focus that block.
- Drag-drop to reorder (also serves as primary reorder mechanism for keyboard users — see §15).
- Right-click for the same More menu.

### 3.5 Multi-select

- Shift-click selects a range (block to block at the same depth).
- Cmd/Ctrl-click toggles individual selection.
- A multi-selection's toolbar exposes Group, Delete, Copy, Convert to Reusable.

### 3.6 Drag-drop reordering

`@dnd-kit/core` (not react-dnd — dnd-kit is the modern choice, better keyboard a11y).

- Drag handle appears on hover in the gutter of the block.
- Drop zones: above/below sibling blocks, into container blocks' inner-blocks area.
- During drag, show an insertion marker. The drop commits a single `moveBlock` action to the editor store.

### 3.7 Keyboard shortcuts

| Shortcut | Action |
|---|---|
| `/` | Open slash inserter (at start of empty paragraph) |
| `Cmd/Ctrl+Z`, `Cmd/Ctrl+Shift+Z` | Undo / Redo |
| `Cmd/Ctrl+S` | Save draft (debounced) |
| `Cmd/Ctrl+Enter` | Save & publish |
| `Cmd/Ctrl+B/I/U/K` | Bold / Italic / Underline / Link |
| `Cmd/Ctrl+Shift+D` | Duplicate selected block |
| `Cmd/Ctrl+Shift+Backspace` | Delete selected block |
| `Cmd/Ctrl+Shift+G` | Group selection |
| `Cmd/Ctrl+Shift+U` | Ungroup |
| `Cmd/Ctrl+Alt+T/Y` | Insert block above / below |
| `Cmd/Ctrl+Alt+M` | Toggle list view |
| `Cmd/Ctrl+Alt+H` | Toggle keyboard shortcut cheatsheet |
| `Esc` | Escape to block selection (from inline edit) |
| `Enter` (block selected, not editing) | Enter inline edit mode |
| `↑/↓` (block selected) | Move selection across blocks |
| `Tab` / `Shift+Tab` | Indent / outdent (in list, columns) |

---

## 4. Rich Text Editing

This is where bad choices destroy editors. The block layer above is honest about its job: arrange blocks. The text inside a block needs **inline formatting** (bold, italic, code, link, mention, inline image) with cursor and selection semantics that match a content-editable while being represented as a clean data model.

### 4.1 Lexical vs TipTap (ProseMirror)

Both are maintained, both are production-grade. The decision matters because the rich-text engine is the highest-frequency code in the editor.

| Dimension | Lexical (Meta) | TipTap (ProseMirror) |
|---|---|---|
| Underlying model | Tree of `LexicalNode`s; intentionally light | ProseMirror schema-validated doc; very rigorous |
| API ergonomics for React | Built React-first | React wrapper on a non-React core |
| Bundle size | Smaller core (~22 KB min+gz) | Larger (PM + TipTap, ~70 KB+) |
| Schema rigour | Looser; you enforce via node validation | Strict schema, can reject invalid transitions outright |
| Collaboration | First-class Yjs binding (`@lexical/yjs`) | First-class Yjs binding (mature) |
| Extension model | Composable, plugin nodes | Extensions (battle-tested ecosystem) |
| Performance on large docs | Excellent — designed for FB-scale typing | Excellent — proven for years |
| Learning curve | Lower | Higher |
| Headless / SSR | Headless renderer ships in repo | `prosemirror-view`-free render utilities |
| Lock-in risk | Meta-controlled but Apache-2.0, healthy adoption | Open governance, very mature |

**Recommendation: Lexical.** Reasons specific to our project:

1. **Composes with our block model.** We don't want the rich-text engine to think about block-level structure — the block layer owns that. Lexical's "shallow" attitude (a doc is just text + inline marks + maybe some custom nodes) is the right level. ProseMirror's schema wants to model the whole document, which fights with our outer block tree. We'd end up running ProseMirror per-block (which is fine) but then we're using only 30% of what it offers.
2. **React-first.** The editor is a React app. Lexical's React bindings are not a wrapper, they're the primary API. TipTap's React layer is good but the underlying view is imperative — debugging React + imperative view is the worst combination.
3. **Smaller bundle in the per-block-instance case.** We will instantiate one rich-text editor per text-bearing block. Lexical's smaller footprint matters.
4. **Yjs story is symmetric with TipTap's** — we don't lose anything for v2 collab.

What we give up: TipTap's enormous extension marketplace and ProseMirror's stricter validation. We compensate by validating at the block layer (JSON Schema) and by keeping the inline format set small (see below).

### 4.2 Inline formats vs blocks

Inline formats are **marks on text leaves**, not nested blocks. Specifically:

```ts
type Mark =
  | "bold"
  | "italic"
  | "underline"
  | "strike"
  | "code"
  | { type: "link"; href: string; target?: "_blank" }
  | { type: "color"; value: string }
  | { type: "highlight"; value: string }
  | { type: "kbd" }
  | { type: "sub" }
  | { type: "sup" };

interface InlineRun {
  insert: string;          // text
  marks?: Mark[];
}

interface RichText {
  ops: InlineRun[];        // Quill-Delta-shaped, easy to diff and CRDT-ify later
}
```

`attributes.text` on a paragraph, heading, list-item, table-cell, etc. is `RichText`. The Lexical editor instance for that block reads/writes this shape via a small adapter:

```ts
// Adapter: Lexical EditorState <-> our RichText
function lexicalToRichText(state: LexicalEditorState): RichText;
function richTextToLexical(rt: RichText): LexicalEditorState;
```

The block stores `RichText`, not Lexical's internal state. Lexical state is editor-runtime only.

Things that *seem* inline but are actually blocks (because they break flow or need their own layout):

- Images shown "inline" with text → still an Image block, or a Media+Text block. We do not support arbitrary image-in-paragraph in v1. (Notion gets away with it because everything is a block; in our model an inline image is a paragraph with two text runs and… no, we draw the line at marks.)
- Inline footnotes (v2): a `footnote-ref` mark with a footnote id; the body of footnotes is a sibling Footnotes block.

### 4.3 Slash commands inside rich text

Slash commands are a block-layer feature (insert a new block), not a rich-text feature. When the user types `/` at the start of an empty paragraph, the block layer intercepts and opens the inserter. Inside non-empty text, `/` is just `/`.

---

## 5. Server-Side Rendering of Blocks

### 5.1 Static vs dynamic

- **Static block**: its rendered HTML is a pure function of its attributes + innerBlocks' rendered HTML. The `save` React component is the canonical renderer. The same React tree runs on the editor side (in `edit` mode it's the editable view; in `save` mode it's the deterministic output) and on the server.
- **Dynamic block**: its rendered HTML depends on data the block doesn't carry — current request, query results, time, viewer identity. It has no `save`; it has a `render` server function.

### 5.2 The renderer (Go-side)

```go
// pkg/blocks/renderer.go

type Block struct {
    ID          string                 `json:"id"`
    Type        string                 `json:"type"`
    Version     int                    `json:"version"`
    Attributes  map[string]interface{} `json:"attributes"`
    InnerBlocks []Block                `json:"innerBlocks"`
    Context     map[string]interface{} `json:"context,omitempty"`
}

type RenderContext struct {
    Post        *Post
    Site        *Site
    Theme       *Theme
    Viewer      *User // may be nil for public render
    Request     *http.Request
    // Resolved block context (from ancestors that declare providesContext).
    Inherited   map[string]interface{}
    // For dynamic blocks: a token allowing them to fetch from the API in-process.
    APIBridge   APIBridge
}

type BlockRenderer interface {
    Render(ctx context.Context, b Block, rc *RenderContext) (html.HTML, error)
}

type Registry struct {
    static  map[string]StaticRenderer  // server-side mirror of save() for core blocks
    dynamic map[string]BlockRenderer   // dynamic block renderers
}

func (r *Registry) RenderDocument(ctx context.Context, doc []Block, rc *RenderContext) (html.HTML, error) {
    var b strings.Builder
    for _, blk := range doc {
        out, err := r.renderOne(ctx, blk, rc)
        if err != nil { return "", err }
        b.WriteString(string(out))
    }
    return html.HTML(b.String()), nil
}

func (r *Registry) renderOne(ctx context.Context, blk Block, rc *RenderContext) (html.HTML, error) {
    // Resolve context provided by this block to descendants.
    childRC := rc.withInherited(blk.Context)

    if dyn, ok := r.dynamic[blk.Type]; ok {
        return dyn.Render(ctx, blk, childRC)
    }
    if stat, ok := r.static[blk.Type]; ok {
        innerHTML, err := r.RenderDocument(ctx, blk.InnerBlocks, childRC)
        if err != nil { return "", err }
        return stat.Render(blk, innerHTML, childRC)
    }
    // Unknown block: render a fallback that preserves the JSON so we can round-trip.
    return r.renderUnknown(blk), nil
}
```

### 5.3 Where does the static renderer live?

We need a server-side renderer that matches what the React `save` component produces. Three options were considered:

1. **Run React on the server (Go calls Node or embeds V8).** Highest fidelity but heavy operational cost — every Go pod needs a Node sidecar or a V8 binding. Also adds a hot path through JS that we don't otherwise need.
2. **Force every block author to write a server renderer too** (in Go for core, in WASM for plugins). Cleaner, but doubles the maintenance burden for static blocks.
3. **Codegen Go renderers from React `save`.** Tempting; never works long term.

**Our pick: hybrid.** Core static blocks ship with a hand-written Go renderer alongside the React `save`. The two are kept in lock-step via a shared test suite (a fixture of attribute combinations rendered by both, compared HTML).

Plugins authoring a static block ship a `save` React component **plus** a small WASM render function with a stable signature:

```ts
// In WASM, plugin side, Go/Rust/TS:
export function render_block(jsonBlock: string, jsonContext: string): string;
```

The Go renderer calls the WASM function once per block instance. WASM gets the JSON serialized block and context, returns HTML. This is the same plugin-host bridge described in [02 — Plugin System](02-plugin-system.md).

For dynamic blocks, the WASM `render` function additionally has access to a scoped API via the host (post lookups, taxonomy queries, current user, etc.).

### 5.4 Render flow on save vs read

```
SAVE (admin → API):
  1. Validate.
  2. Migrate.
  3. For each block, render to HTML:
     - static: Go or WASM static renderer → final HTML
     - dynamic: emit placeholder <div data-dyn-block="..." data-dyn-id="..."></div>
  4. Concatenate → content_rendered.
  5. Persist content_blocks + content_rendered.

READ (public site, Next.js):
  1. Fetch post (content_rendered + content_blocks).
  2. If post body contains dynamic markers, fetch resolved HTML for each:
       GET /api/blocks/resolve?post=:id&block=:dynId
       — returns rendered HTML for that block in the current request context.
  3. Concatenate, hydrate any interactive blocks via island bundles.
```

For Next.js, the render step uses React Server Components: a `<RenderedBody postId={x} />` RSC fetches `content_rendered`, parses out dyn markers, and replaces each with `<DynamicBlock id={…} type={…} />` server-components that fetch their HTML in parallel. Interactive blocks (e.g., a tabs block) export a client-bundle island, identified by a `data-island="..."` attribute on their root element; the renderer attaches a small hydration script that lazy-loads the island when it scrolls near the viewport.

### 5.5 Caching strategy

(Fixed per review — tag naming and pre-render cache key conform to doc 07 §15-§16.)

- `content_rendered` is the cheap path. It's a string from Postgres.
- The **pre-render cache key** is `(block_type, attrs_hash, content_version)` per doc 07 §15.5 (canonical hash format). Static blocks omit `content_version` and cache effectively forever; dynamic blocks bump via the Redis counter described in doc 07.
- Dynamic block resolution is cached at the edge with per-block cache keys that include the block's attribute hash plus its dependencies (which posts/taxonomies the block reads). The transactional outbox + invalidation-worker (doc 07 §16.2) is the single mechanism for invalidation; this doc does not introduce a parallel one.
- ISR: pages are revalidated by tag. **Tag naming is owned by doc 07 §16.1** — dotted lowercase: `post:{uuid}`, `term:{uuid}`, `user:{uuid}`, `type:{slug}`, `archive:{type}:{taxonomy}:{term-uuid}`, `site:settings`. Dynamic blocks declare the tags they consume (using doc 07's vocabulary); the renderer wires them into Next.js `revalidateTag`. Any earlier doc 04 examples like `query:posts:type=event` are superseded by doc 07's conventions.

---

## 6. Block Patterns

A pattern is a pre-composed block subtree the user can insert in one click. It is not a runtime construct: once inserted, it's just blocks.

```ts
interface BlockPattern {
  id: string;
  name: string;             // "core/two-column-hero"
  title: string;
  description?: string;
  /** Inserter category. */
  categories: string[];
  /** Free-text keywords for inserter search. */
  keywords?: string[];
  /** Preview image (rendered to thumbnail server-side from the block tree). */
  preview?: string;
  /** The actual block tree, with placeholder text/images. */
  blocks: Block[];
  /** Where this pattern came from, for badging in the inserter. */
  source: "core" | { theme: string } | { plugin: string } | "user";
  /** Visibility (block themes use patterns for templates/parts too). */
  scope?: ("post" | "page" | "template" | "part")[];
  /** Block types this pattern primarily contains, for filtering. */
  blockTypes?: BlockName[];
}
```

Where patterns live:

- **Core**: hard-coded in the platform. Updated with the platform.
- **Themes**: declared in `theme/patterns/*.json` (or `*.tsx` exporting `{ pattern }`). Loaded on theme activation.
- **Plugins**: registered via the plugin SDK during plugin init.
- **User-saved**: created from any selection (Block toolbar More → Create pattern). Stored in `block_patterns` table:

```sql
-- Fixed per review (contract S1): UUID v7 PK, UUID FK to users.
CREATE TABLE block_patterns (
  id          UUID PRIMARY KEY DEFAULT gen_uuid_v7(),
  name        TEXT NOT NULL,
  title       TEXT NOT NULL,
  description TEXT,
  categories  TEXT[] NOT NULL DEFAULT '{}',
  keywords    TEXT[] NOT NULL DEFAULT '{}',
  blocks      JSONB NOT NULL,
  scope       TEXT[] NOT NULL DEFAULT '{post,page}',
  source      TEXT NOT NULL DEFAULT 'user',
  source_ref  TEXT,                                -- theme name / plugin id
  created_by  UUID REFERENCES users(id),
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

A pattern preview is rendered to a static HTML thumbnail (and an SVG silhouette) on save, so the inserter can show previews without instantiating React for each pattern.

---

## 7. Reusable Blocks (Synced Patterns)

A reusable block is a block that points to a shared definition. Editing the reusable block edits all instances on all posts.

Two flavours, matching what Gutenberg landed on:

- **Synced** (true reusable): every insertion references the same source. Edits propagate.
- **Unsynced** (pattern): inserts a copy of the block tree. Edits are local.

The user-saved patterns from §6 are the unsynced flavor. Synced reusable blocks need a real identity:

```sql
-- Fixed per review (contracts S1 + S4): UUID v7 PK, UUID FK to users.
CREATE TABLE reusable_blocks (
  id         UUID PRIMARY KEY DEFAULT gen_uuid_v7(),
  title      TEXT NOT NULL,
  blocks     JSONB NOT NULL,
  status     TEXT NOT NULL DEFAULT 'published',   -- draft / published / trashed
  created_by UUID REFERENCES users(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

In a post's block tree, a reference looks like (fixed per review — `ref` is a **UUID string**, contract S4):

```json
{
  "id": "01HXYZ...",
  "type": "core/block",
  "version": 1,
  "attributes": { "ref": "01HX8K7M5R2N3P4Q5S6T7V8W9Z" },
  "innerBlocks": []
}
```

On render, `core/block` is treated as dynamic: the renderer fetches `reusable_blocks.blocks` for the given `ref` UUID and inline-renders it. In the editor, the block expands to show the live referenced tree (read-only by default, with a "Edit reusable block" entry point that opens the source).

Cache invalidation: when a reusable block is saved, invalidate every post that references it. We maintain a reverse index `reusable_block_uses(reusable_id, post_id)` populated from the GIN index over `content_blocks`.

---

## 8. Block Validation & Migration

### 8.1 Validation

Every block instance is validated on save against the registered block's JSON Schema:

```
On PUT /posts/:id:
  for each block in document (recursively):
    1. Look up registry[block.type]. If missing → mark as "unknown", keep as-is.
    2. If block.version < registry.version → run migration pipeline (§8.2).
    3. Validate block.attributes against registry.attributes (JSON Schema).
    4. Strip any properties not in the schema (additionalProperties: false everywhere).
    5. Defaults applied for missing optional properties.
  If any block fails validation → return 422 with the offending block id and error list.
```

The editor shows validation errors **inline** on the offending block, not in a console. The save button is enabled regardless (we never silently lose work), but with a clear "Fix errors before publishing" indicator.

### 8.2 Deprecation / migration

When a block author needs to change attributes shape, they bump `version` and register a `deprecated` entry:

```ts
registerBlockType<NewAttrs>({
  name: "core/heading",
  version: 3,
  attributes: schemaV3,
  edit: HeadingEdit,
  save: HeadingSave,
  deprecated: [
    {
      // v2 -> v3
      attributes: schemaV2,
      isEligible: a => typeof (a as any).fontSize === "string",
      migrate: v2 => ({
        ...v2,
        typography: { fontSize: parseInt((v2 as any).fontSize, 10) },
      }),
    },
    {
      // v1 -> v2
      attributes: schemaV1,
      migrate: v1 => ({ ...v1, fontSize: (v1 as any).size }),
    },
  ],
});
```

The migration pipeline runs versions in order: a block at version 1 walks v1→v2→v3. We persist the migrated form on the next save (lazy migration, no big-bang reflows). The renderer is tolerant: if an old-version block somehow reaches it, the renderer also runs the pipeline.

This is a strict improvement over Gutenberg's `deprecated` array: Gutenberg matches by trying each old `save` against the persisted HTML, which is fragile and produces "block recovery" prompts to the user. We don't have HTML to compare; we have JSON attributes and explicit schema versions.

---

## 9. Persistence Flow

### 9.1 The state machine

Editor state on the client:

```
┌─────────┐  user types  ┌──────────┐  debounced 2s  ┌──────────┐
│  Clean  │ ───────────▶ │  Dirty   │ ─────────────▶ │ Autosave │
└─────────┘              └──────────┘                │  in-flt  │
     ▲                        │                      └────┬─────┘
     │                        │ user clicks Save         │
     │                        ▼                          │
     │                  ┌──────────┐                     │
     │                  │  Saving  │ ◀───────────────────┘
     │                  └────┬─────┘
     │                       │ ok       │ conflict
     └───────────────────────┘          ▼
                                  ┌────────────┐
                                  │  Conflict  │
                                  └────────────┘
```

### 9.2 Autosave

**Fixed per review (contract S2):** Revision and autosave storage are owned by [`01-core-cms.md`](01-core-cms.md) §4. Autosaves are not a separate table; they are rows in `post_revisions` with `kind = 'autosave'`. The editor autosave behavior described below writes through that schema.

- Trigger: 2 seconds idle after edit, OR 30 seconds since last save, whichever first.
- Endpoint: `POST /posts/:id/autosave` — inserts (or upserts the latest per `(post_id, author_id, kind='autosave')`) a row into `post_revisions` with `kind='autosave'`. Does **not** update the public post (no write to `posts.content_blocks`).
- Restoration: on opening an editor, query the most recent `post_revisions WHERE post_id = $1 AND author_id = $current_user AND kind = 'autosave'` and compare `created_at` to `posts.updated_at`; if the autosave is newer, prompt to restore.
- On manual save or publish, doc 01 §4.2 specifies that the matching autosave row is deleted and a `manual` or `publish` revision is written.

### 9.3 Manual save / publish

- Save draft: writes `content_blocks` and re-renders. Status remains `draft`. A `kind='manual'` row is appended to `post_revisions` (storage shape, retention, and delta-vs-snapshot logic are all owned by doc 01 §4).
- Publish: same, but flips status, writes a `kind='publish'` revision, and triggers post-publish hooks (sitemap, ISR revalidation, webhooks).

### 9.4 Revisions

**Fixed per review (contract S2):** The `post_revisions` table is defined exclusively in [`01-core-cms.md`](01-core-cms.md) §4.1 / §10.6 — a single delta-aware table with three `kind` values (`autosave | manual | publish`). This doc does not redefine the schema; consult doc 01 for columns, indexes, and retention policy.

What this doc owns is the **editor surface**:

- The revisions browser is a tree-diff view (per-block additions/removals/edits) — not a unified HTML diff, since we have structure. Reconstruction of a delta-stored revision walks the chain back to the nearest snapshot (doc 01 §4.1).
- Filtering: the UI defaults to `kind IN ('manual', 'publish')` so the autosave noise is hidden; a toggle reveals autosaves for power users.
- Restore is wired to doc 01 §4.4: a restore creates a **new** `kind='manual'` revision with comment `"Restored from revision X"` rather than rewriting history.

### 9.5 Optimistic UI

- Edits are applied to local state immediately.
- Autosave is fire-and-forget.
- For manual Save / Publish, the toast appears optimistically with an undo affordance; the toast resolves to success/failure once the server replies.

### 9.6 Conflict resolution (v1, no live collab)

If two editors save concurrently:

- Save requests carry `If-Match: <content_blocks_hash>` (the hash the editor loaded from).
- Server compares to current `content_blocks_hash`. If mismatch → 409 Conflict, body includes the current document.
- Editor enters Conflict state, shows a three-way merge UI: base (last common), theirs (current server), mine (my edits).
- The merge is **block-level**: each block id is matched between trees; conflicting blocks are presented with "Keep mine / Keep theirs / Keep both" actions. New blocks from either side merge cleanly when their ids don't collide.

This is dramatically better than what most CMSes do (last-write-wins with a "your changes were overwritten" toast). And it's the conceptual stepping stone to CRDT merge in v2.

---

## 10. Real-Time Collaboration (v2)

Punted to v2, but the data model is designed to accept it.

### 10.1 The plan: Yjs CRDT, per-document

- The block document maps onto a Yjs `Y.Array<Y.Map>` of blocks. Each block's `attributes` is a nested `Y.Map`; rich-text `attributes.text` is a `Y.Text` with our `Mark` set as the inline format API.
- A small Go-side relay (`yjs-server` style) holds rooms keyed by post id, broadcasts updates between connected clients, and periodically snapshots the doc to Postgres.
- Awareness (cursors, selections, names) ride on the same WebSocket as a separate channel.
- Persistence: the canonical form remains `posts.content_blocks` JSON. The Yjs doc state is materialized to JSON on each snapshot. We don't store the binary Y.Doc as the source of truth; we treat it as a transient working copy.

### 10.2 What changes in our model when collab lands

- **IDs are essential.** Already done. Block ids are ULIDs, generated client-side, stable across edits. CRDT operations reference blocks by id.
- **Inline mark semantics.** Our `RichText` `ops` shape is structurally close to Y.Text formatting. The adapter `richTextToY` is mechanical.
- **`isEligible` check on migrations needs to be deterministic.** Already required.
- **Server-side validation on every snapshot, not every keystroke.** The CRDT can produce transient states our schema would reject (e.g., empty required string). Validation happens at snapshot time, with the editor showing block-level error markers in real-time but not blocking typing.
- **Block locking** (§13) becomes "lock acquire/release" semantics propagated through awareness.

What we are deliberately not doing: building our own CRDT. Yjs is the most boring-correct choice; the rich-text bindings for Lexical (`@lexical/yjs`) already exist.

---

## 11. Custom Fields Integration

[01 — Core CMS](01-core-cms.md) describes the custom fields system: arbitrary typed metadata attached to a content type, declared via JSON Schema in the type definition (or via the field-builder UI).

In the editor, custom fields surface as a **Fields panel** in the Document tab of the right inspector:

```
┌──────────────┐
│  Block | Doc │
├──────────────┤
│ Status  ▾    │
│ Slug    ___  │
│ Schedule …   │
├──────────────┤
│  Taxonomies  │
│  □ news      │
│  □ updates   │
├──────────────┤
│   Fields     │  ◀── auto-generated from CPT's JSON Schema
│ Subtitle ___ │
│ Hero img [+] │
│ Author  ▾    │
│ Rating  ★★★☆ │
├──────────────┤
│  SEO         │  ◀── plugin-contributed panel (still a "field" group)
│ Title  ___   │
│ Desc   ___   │
└──────────────┘
```

Driven by a generic JSON-Schema-form component:

```ts
function CustomFieldsPanel({
  schema, value, onChange,
}: {
  schema: JSONSchema7;
  value: Record<string, unknown>;
  onChange: (next: Record<string, unknown>) => void;
}): ReactElement;
```

Plugins can register **field renderers** for specific JSON Schema types (e.g., `format: "media"` → MediaPicker; `enum` → Select; `format: "color"` → ColorPicker). The renderer registry mirrors the block registry pattern.

Custom fields are stored according to the field's declared `storage` (see [01 — Core CMS](01-core-cms.md) §9.4): `meta` (JSONB on `posts.meta`, the common case), `sidecar` (a typed column on a sidecar table), or `column` (a typed column on `posts` itself, reserved for first-party core fields). Whichever target, the value is validated against the content type's field schema on save, the same way block attributes are validated against the block's schema.

---

## 12. Block Context

Some blocks need to know about their ancestors. The Query block is the canonical example: it iterates posts, and inside its inner template a Post Title block needs to know "which post am I rendering for?".

Mechanism:

- A block declares `providesContext: { "postId": "queriedPostId" }` (key = context name, value = path inside the block's attributes/runtime state).
- Descendant blocks declare `usesContext: ["postId"]`.
- The renderer (both editor and server) walks the tree carrying an inherited context map. When entering a provider, it pushes the new values; when leaving, it pops.

Example: Query + Post Title:

```ts
registerBlockType({
  name: "core/query",
  // ...
  providesContext: { postId: "currentPostId", postType: "currentPostType" },
  render: { handler: "core/query.render" },
});

registerBlockType({
  name: "core/post-title",
  // ...
  usesContext: ["postId", "postType"],
  render: { handler: "core/post-title.render" },
});
```

On the server side, the Query block's render function loops over its query results; for each iteration it renders the inner block tree with the current post id pushed into the `Inherited` map. Post Title's render reads `rc.Inherited["postId"]`.

On the editor side, the Query block uses a React Context to project the values; descendants `useBlockContext("postId")`.

We're explicit that context is **declared**, not implicit. A block can't read context it didn't ask for. This keeps the surface contract tight (and is what Gutenberg eventually landed on).

---

## 13. Block Locking & Templates

### 13.1 Locking

Two kinds of locks, both stored as `attributes.lock`:

```ts
interface BlockLock {
  move?: boolean;     // can't be reordered
  remove?: boolean;   // can't be deleted
  edit?: boolean;     // attributes are read-only (still selectable)
  /** Who set this lock — for UI badging. */
  source?: "user" | "template" | "pattern";
}
```

Locking is per-block, not per-block-type. The toolbar's More menu has a Lock entry (when `supports.lock`). Locked blocks render with a small lock badge in their corner.

Roles & capabilities (§ in [06 — Auth & Permissions](06-auth-permissions.md)) gate who can lock/unlock. Editors can unlock; authors can't unlock template-imposed locks.

### 13.2 Templates

A **block template** is a required block tree for a given content type or specific post. Defined in:

- Theme (per template-hierarchy file).
- CPT registration (`template: Block[]`, optional `template_lock: "all" | "insert" | false`).
- Per-post override (admin form).

```ts
interface BlockTemplate {
  /** The required tree. Each block can declare attributes; user fills the placeholders. */
  blocks: Block[];
  /** Default lock applied to every block in the template. */
  lock?: "all"        // can't move, can't insert, can't remove
       | "insert"     // can move/remove but can't insert siblings between
       | false;
}
```

When the post is created from this CPT, the editor pre-populates `content_blocks` with the template. Lock semantics apply on the editor side via the same `attributes.lock` mechanism, with `source: "template"`.

---

## 14. Markdown & Paste Behavior

The clipboard is where most editor frustration comes from. The block model gives us a real chance to be good at it.

### 14.1 The paste pipeline

```
paste event
   │
   ▼
Detect best format (prefer in order):
   1. clipboardData.getData("application/x-platform-blocks")  — our internal format
   2. clipboardData.getData("text/html")
   3. clipboardData.getData("text/plain")
   │
   ▼
Convert to Block[] via the matching converter
   │
   ▼
Filter through registered "paste transforms"
   │
   ▼
Insert at selection
```

### 14.2 Internal format (cross-tab paste)

When copying inside our editor, we write **all three** standard MIME types plus our private `application/x-platform-blocks` which is the JSON block tree. Pasting back parses our private format and rehydrates with fresh ids.

### 14.3 Markdown paste

If the pasted text is detected as Markdown (heuristic: contains `# `, ``` ` ```, `* `, `[…](…)`, etc. in plausible patterns), parse with **remark** (or a smaller markdown parser we control), then map the MDAST to blocks:

| MDAST node | Block |
|---|---|
| `heading` | `core/heading` (level from depth) |
| `paragraph` | `core/paragraph` (children → RichText ops) |
| `list` | `core/list` (ordered from `ordered`) |
| `code` | `core/code` |
| `blockquote` | `core/quote` |
| `thematicBreak` | `core/separator` |
| `image` | `core/image` (URL retained; not re-uploaded) |
| `table` | `core/table` |
| `link` (inline) | RichText link mark |
| `strong`, `emphasis`, `inlineCode`, `delete` | RichText marks |

### 14.4 HTML paste

Source-specific normalization for the worst offenders:

- **Google Docs**: strips `<b style="font-weight:normal">` ("not actually bold"), filters the proprietary classes, recovers headings from `<p>` with role/style, dedupes whitespace.
- **Microsoft Word / Outlook**: strips `mso-*` styles, removes conditional comments, recovers list structure from Word's nested `<p>` lists.
- **Notion**: maps Notion's block classes when present; otherwise falls back to generic HTML.
- **Plain web page**: sanitize with an allowlist, then map (`<h1..h6>` → heading, `<p>` → paragraph, `<ul/ol>` → list, …).

Sanitization uses **DOMPurify** on the client (the editor) and **bluemonday** on the server (for any HTML coming through the API). The two are configured from the same allowlist (we generate both configs from a single TS file).

### 14.5 Drag-drop files

Dragging an image onto the canvas:

- Single image → upload + insert `core/image` block at drop position.
- Multiple images → upload + insert `core/gallery` block.
- Video / audio → corresponding block.
- File → `core/file` block.
- Folder → not supported in v1.

Uploads go through the media service (see [07 — Media & Performance](07-media-performance.md)); the block initially renders an upload progress placeholder.

---

## 15. Accessibility

Editors are notoriously bad for screen-reader and keyboard users. Gutenberg has put real work into this; we steal liberally.

### 15.1 Keyboard story

- Every action accessible via keyboard. Drag-drop has a keyboard-equivalent ("Move up", "Move down", "Move to position…").
- Tab order is predictable: canvas → block toolbar (when a block is selected) → inspector tabs → inspector controls → top bar.
- Focus rings everywhere; the same ring style across editor and theme.

### 15.2 Screen reader

- Selected block is announced ("Heading block, level 1, contains 'Hello'").
- Insertion announces ("Paragraph block inserted after Heading block").
- Block toolbar buttons have descriptive `aria-label`s; toggles announce pressed state.
- Document outline is the screen reader's primary navigation tool; we expose it as a real tree with `role="tree"` and proper aria.

### 15.3 Focus management

- After insertion, focus moves into the new block's text area (if any), or onto the block selection if not.
- After deletion, focus moves to the previous sibling (or parent if first child).
- Modals/popovers trap focus, restore on close.
- The block toolbar is reachable via `Alt+F10` (Office convention) and `Esc` returns focus to the block.

### 15.4 Color & contrast

- All editor chrome WCAG 2.2 AA contrast.
- Theme styles in the canvas: we don't control them, but we run a contrast check on the active foreground/background combos used by core blocks and warn in the inspector if a user-picked color drops below threshold.

---

## 16. Performance

### 16.1 Bundle strategy

- Editor shell (canvas, toolbar, inspector skeleton, registry, Lexical): one bundle, loaded eagerly.
- Each block's `edit` component is in a **lazy chunk** loaded on first use. The registry stub records the import; React Suspense handles the fallback.
- Plugin-registered blocks live in plugin ES modules, also lazy.

This means a fresh document that uses only Paragraph + Heading downloads ~200 KB of editor + 30 KB of those two blocks, not the whole catalog.

### 16.2 Document virtualization

For long documents (1000+ blocks — uncommon but real for sites importing CMS dumps):

- The canvas virtualizes top-level blocks beyond the viewport (and a 2-screen margin) using `@tanstack/react-virtual`.
- Off-screen blocks render a measured placeholder of the correct height.
- Caveat: container blocks (Columns, Group) cannot be virtualized internally without breaking layout — we virtualize only at the root list.

### 16.3 Editor state model

The editor uses a single **immutable** store (Zustand + Immer):

```ts
interface EditorState {
  blocks: BlockDocument;
  selection: { anchor: string | null; focus: string | null };
  active: { clientId: string | null };
  history: { past: BlockDocument[]; future: BlockDocument[] };
  dirty: boolean;
  saving: "idle" | "saving" | "conflict";
  // ...
}
```

- Updates flow through a small set of actions (`insertBlock`, `updateAttributes`, `moveBlock`, `removeBlock`, `setSelection`, …).
- Subscriptions are scoped: a block's `edit` component subscribes only to its own slice keyed by id. Updating one block does not re-render others.
- History snapshots happen on action boundaries with structural sharing (Immer's persistent maps); 100 entries is ~5 MB for a typical document.

### 16.4 Render avoidance

- React keys are `block.id` everywhere — stable through reorders.
- The InnerBlocks component memoizes its children list by id.
- Inspector panels are memoized per block id; switching blocks unmounts the previous inspector.

### 16.5 Server render performance

- Per-post render time target: **p95 < 50 ms** for static-only documents up to 200 blocks.
- Dynamic blocks are resolved in parallel.
- The Go renderer is a single allocator-light pass over the tree; HTML templates are precompiled `text/template` with `html/template`-grade escaping where needed.

---

## 17. Trade-offs & Rejected Alternatives

### 17.1 Why not Notion-style "everything is a block"?

Notion treats every piece of content (including a single line of text) as a block. Pros: a uniform model, free drag-anywhere semantics. Cons:

- A paragraph of text is many small blocks (or one big block with no inline structure), which loses the linguistic unit and makes typing latency higher.
- Inline-image, inline-mention, inline-formula become awkward — they're not blocks, but in a "everything is a block" world they have to be modeled either as inline marks (back to our model) or as a special non-block primitive (the model leaks).
- Server rendering becomes more expensive because every line is a render unit.

Our model is "blocks for structure, marks for inline text formatting." It's the Gutenberg insight, kept.

### 17.2 Why not Slate.js?

Slate is a popular alternative to ProseMirror. We considered it.

- Slate's document model is recursive ("a document is a list of nodes; a node has children which are nodes…") which sounds like a fit for our block tree.
- In practice: Slate has had a turbulent API history (v0.x → v0.50+ → no v1), the maintainers are unpaid (the project has multiple times stalled), and the perf story on large documents is well-documented to need careful work.
- Slate also encourages mixing block-level and inline-level nodes in one model — exactly what we want to keep separate.

Lexical's narrower scope (text + inline formats) fits cleaner into our architecture than Slate's "do everything" model.

### 17.3 Why not just embed Gutenberg?

Gutenberg's React packages (`@wordpress/block-editor`, `@wordpress/blocks`, etc.) are independently published on npm. Tempting.

- They carry deep coupling to WP REST conventions, WP data store patterns (`@wordpress/data` — its own Redux-flavored thing), and WP's "register on a global side-effect" patterns.
- The HTML-with-comments serialization is baked into core utilities. We'd be working around it forever.
- Gutenberg's React is old and slow to modernize; mixing it with a modern React app is friction.
- Bundle size: a minimal Gutenberg setup is 600+ KB before any blocks.
- Plugin compatibility is a benefit only if our plugin ecosystem speaks `@wordpress/*` — it doesn't.

The block paradigm is great. Gutenberg's specific implementation is dragging two decades of constraints. We're freeing ourselves of those.

### 17.4 Why not codegen `save` renderers to Go?

We considered emitting a Go function from each block's `save` React component (AST analysis or a small DSL that compiles to both Go and JSX). Rejected:

- Authors would gain a third place to write things (the codegen DSL).
- The set of React features used by `save` components is small in theory but unbounded in practice; any author who reaches for `useMemo` or a context inside `save` blows up the codegen.
- A maintained handwritten Go renderer for the small set of core blocks is a one-time cost.

For plugin-provided blocks, the WASM render bridge is the answer — authors already have a Go/Rust/TS path for their plugin code; rendering joins that path naturally.

### 17.5 Why not store `content_rendered` as a separate table?

Could split `posts` into `posts` + `post_rendered`. Considered:

- Saves a tiny bit of table-row size in queries that don't need the HTML.
- Costs a join on every render.
- Postgres TOAST already moves the big text out-of-line.

Keep it on the same row.

### 17.6 Why JSONB blocks rather than a fully normalized blocks table?

We could store one row per block in a `post_blocks` table with parent/order columns. Considered:

- Pros: SQL queries against block content, atomic per-block updates, normalized.
- Cons: ten times the row count for the same content; transactional save becomes a big batch insert; ordering pain (gap-filled `position` integers or fractional positions); renders need recursive CTEs.
- The GIN index on JSONB gives us most of the query benefit without the row explosion. Per-block atomic updates aren't needed in v1 (we save whole documents).

If we later add live collab with CRDT, the working representation is in-memory Yjs anyway. JSONB stays.

---

## 18. Open Questions

1. **Iframe vs no-iframe canvas.** Gutenberg moved to iframe to get scoped theme CSS. Confirm: do we want the iframe overhead from day one, or start out-of-iframe and convert when theme styles arrive?
2. **Lexical or TipTap — final call.** Recommendation is Lexical but we should prototype both for one week with the same target block (a Heading) before committing.
3. **Static block server renderer authoring.** For core blocks, do we accept the double-write cost (TS `save` + Go renderer) or codegen Go from a `RenderSpec` JSON the block author writes once?
4. **Per-block edit components**: do plugins ship them as separate ES modules per block (granular lazy load) or one module per plugin (coarser)?
5. **Pattern thumbnails**: render server-side at registration time, or on-demand and cached? On-demand has a cold-start problem.
6. **Reusable blocks visibility**: per-author or site-wide by default? WP went site-wide; some users find this surprising.
7. **Block locking inheritance**: should a locked container's children inherit the lock, or be independently lockable?
8. **Slash command scope**: only at the start of an empty paragraph (predictable), or anywhere (Notion-like)? The latter is harder to do without false positives.
9. **Validation strictness on legacy migrated content.** When migrating WP imports, we'll see HTML-blob content from `core/freeform`. Do we keep that as an unparsed Classic block, or attempt best-effort conversion to native blocks?
10. **Editor in Next.js admin app vs separate Vite SPA.** [00 — Architecture Overview](00-architecture-overview.md) is undecided. The editor is heavy; sharing a Next.js app with the rest of admin might fight us on bundling. Resolve at admin-app design time.
11. **Pasting from competitor block editors.** Should we explicitly support pasting from Gutenberg's clipboard format? It's a meaningful migration on-ramp.
12. **Block ABI versioning across plugins.** When the block registration API changes, how do plugins targeting v1 keep working under v2? Likely a `requires: { editor: ">=2.0" }` field in the plugin manifest plus a shim layer for v1 blocks.

---

## 19. Appendix: ASCII flow — Save → Render

```
                    ┌───────────────────────────┐
                    │ Editor (React, admin app) │
                    └────────────┬──────────────┘
                                 │ PUT /posts/:id
                                 │ body: { blocks: BlockDocument }
                                 │ If-Match: <hash>
                                 ▼
                    ┌───────────────────────────┐
                    │       Go API server       │
                    │                           │
                    │  1. Hash check (conflict?)│
                    │  2. Validate (JSON Schema)│
                    │  3. Migrate (versions)    │
                    │  4. Render pass:          │
                    │     for blk in doc:       │
                    │       if dynamic:         │
                    │         emit marker       │
                    │       else if WASM:       │
                    │         host.call(plugin) │
                    │       else:               │
                    │         go template render│
                    │  5. Persist               │
                    │  6. Fire hooks (publish)  │
                    │  7. Invalidate caches     │
                    └────────────┬──────────────┘
                                 │
                                 │ 200 OK { hash, renderedHash, … }
                                 ▼
                       ┌──────────────────┐
                       │  Public site     │
                       │  Next.js render  │
                       │                  │
                       │  GET /post/:slug │
                       │  → content_rendr │
                       │  → resolve dyn   │
                       │     markers in   │
                       │     parallel     │
                       │  → hydrate isle  │
                       └──────────────────┘
```

End.
