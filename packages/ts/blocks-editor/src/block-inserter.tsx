/**
 * `<BlockInserter>` — the categorized, searchable grid that lets authors
 * pick a registered block to insert.
 *
 * Behavior contract (issue #79 acceptance criteria, plus patterns):
 *
 *  - Reads its catalogue from a `BlockRegistry.list()` snapshot. The list
 *    is recomputed whenever the registry reference changes; HMR-style
 *    re-registration on the same registry instance is *not* observed here
 *    (consumers should pass a fresh registry or call `forceUpdate` from
 *    above — keeping this dumb avoids a subscription mechanism the SDK
 *    doesn't ship).
 *  - Renders a search input that filters by `block.title` (case-insensitive
 *    substring match). Empty query shows everything.
 *  - Renders one tab per built-in `BlockCategory` (`text`, `media`, `design`,
 *    `widgets`, `theme`, `embed`, `custom`). Tabs whose category has zero
 *    matching blocks are still rendered (so the layout stays stable) but
 *    appear disabled.
 *  - Each tile is a `<button>` carrying the block icon (routed through
 *    the `gn-editor` Trusted Types policy via `sanitizeBlockIcon`
 *    when the icon is an inline SVG string) plus the title. Clicking
 *    calls `onInsert` with a fresh block shape:
 *    `{ type, attributes: {}, innerBlocks: [] }`.
 *  - When a `patternRegistry` prop is passed, a **"Patterns"** tab is
 *    appended after the block-category tabs. Activating it swaps the
 *    main panel for a category-grouped list of pattern tiles. Clicking a
 *    pattern tile calls `onInsertPattern(blocks)` with a deep copy of the
 *    pattern's BlockTree so callers can splice it into the editor's tree
 *    state without sharing references with the source fixture.
 *
 * Why not memoise the filter? The registry is a registration-time thing —
 * registries typically hold tens of entries, not thousands. A linear filter
 * per keystroke is comfortably under any UX-relevant threshold, and skipping
 * `useMemo` keeps the surface easier to reason about. Patterns get the same
 * treatment for the same reasons (we ship ~10 first-party + plugin-loaded).
 *
 * Visual styling — "Living systems" brand (docs/design/HANDOFF.md +
 * docs/design/ui_kits/editor/index.html):
 *   - The panel sits on `--paper-2` with a hairline `--border` and the
 *     resting `--sh-xs` shadow, exactly the slash-menu surface from
 *     the editor mock.
 *   - The search input is the brand's `--paper-3` sunken input with a
 *     `--sh-focus` emerald halo on focus.
 *   - Category tabs are Geist 500 labels (case kept lowercase to honour
 *     the BlockCategory string verbatim); the active tab carries the
 *     emerald-soft pill the mock's slash-menu active item uses.
 *   - Each tile carries a Lucide-style icon glyph keyed off the block
 *     name (Type / Heading1 / List / Image / ShoppingBag / Code2 /
 *     Quote / square fallback) — inline SVG so the package doesn't
 *     take a `lucide-react` dep just for the editor surface.
 *   - Patterns panel renders a 3-column grid; tiles use the paper-3
 *     hover that mirrors the slash menu's hover row.
 */
'use client';

import type {
  Block,
  BlockCategory,
  BlockRegistry,
  BlockTree,
  BlockTypeDefinition,
} from '@gonext/blocks-sdk';
import { useMemo, useState, type CSSProperties, type ReactNode } from 'react';
import type { Pattern, PatternRegistry } from './pattern-types.ts';
import { clonePatternBlocks } from './pattern-clone.ts';
import { sanitizeBlockIcon } from './trusted-types.ts';

/**
 * The set of categories the inserter renders tabs for, in display order.
 * `BlockCategory` allows arbitrary strings, but the issue scope freezes the
 * tab list to these seven so the layout is deterministic. Blocks registered
 * under an unknown category will not appear in any tab — consumers needing
 * "Other" support should subclass / fork this component.
 */
export const INSERTER_CATEGORIES: readonly BlockCategory[] = [
  'text',
  'media',
  'design',
  'widgets',
  'theme',
  'embed',
  'custom',
] as const;

/** Sentinel used as the `category` value when the Patterns tab is active. */
const PATTERNS_TAB = '__patterns__' as const;

export interface BlockInserterProps {
  /** Source of truth for what blocks exist. Required. */
  registry: BlockRegistry;
  /**
   * Called when a tile is clicked. Receives a freshly-minted block shape
   * (type + empty attributes + empty innerBlocks).
   */
  onInsert: (block: Block) => void;
  /**
   * Optional source of truth for what patterns exist. When omitted, the
   * Patterns tab is not rendered. When passed, a "Patterns" tab is
   * appended after the block-category tabs and `onInsertPattern` becomes
   * required.
   */
  patternRegistry?: PatternRegistry;
  /**
   * Called when a pattern tile is clicked. Receives a deep copy of the
   * pattern's BlockTree so the caller can splice it into editor state
   * without aliasing the source fixture.
   *
   * Required when `patternRegistry` is set; otherwise unused.
   */
  onInsertPattern?: (blocks: BlockTree, pattern: Pattern) => void;
  /**
   * Initial active category. Defaults to `'text'`. Pass `'__patterns__'`
   * (the value of the `PATTERNS_TAB` sentinel re-exported below) to land
   * directly on the Patterns tab.
   */
  initialCategory?: BlockCategory | typeof PATTERNS_TAB;
  /**
   * Initial search query. Defaults to `''` (no filter). Mostly useful for
   * tests that want to assert on a pre-filtered state.
   */
  initialQuery?: string;
}

/** Re-exported sentinel for callers that want to default to the Patterns tab. */
export const BLOCK_INSERTER_PATTERNS_TAB = PATTERNS_TAB;

/**
 * Style tokens for the inserter chrome. As with the canvas, every value
 * references a CSS custom property declared in
 * `apps/admin/src/styles/tokens.css` with a literal fallback so vitest
 * snapshots render correctly in isolation.
 */
const panelStyle: CSSProperties = {
  background: 'var(--paper-2, #EFEBE0)',
  border: '1px solid var(--border, #D9D2C0)',
  borderRadius: 'var(--r-lg, 12px)',
  padding: 'var(--s-4, 16px)',
  boxShadow: 'var(--sh-xs, 0 1px 2px rgba(14, 26, 20, 0.04))',
  fontFamily:
    "var(--font-sans, 'Geist', -apple-system, system-ui, sans-serif)",
  color: 'var(--ink, #0E1A14)',
  width: '100%',
  maxWidth: 360,
  display: 'flex',
  flexDirection: 'column',
  gap: 'var(--s-3, 12px)',
};

const searchStyle: CSSProperties = {
  width: '100%',
  background: 'var(--paper-3, #E6E1D2)',
  border: '1px solid var(--border, #D9D2C0)',
  borderRadius: 'var(--r-md, 8px)',
  padding: '8px 12px',
  fontFamily: 'inherit',
  fontSize: 'var(--t-sm, 13px)',
  color: 'var(--ink, #0E1A14)',
  outline: 'none',
};

const tabsStyle: CSSProperties = {
  display: 'flex',
  flexWrap: 'wrap',
  gap: 'var(--s-1, 4px)',
  paddingBottom: 'var(--s-2, 8px)',
  borderBottom: '1px solid var(--border-subtle, #E8E2D1)',
};

const tabBase: CSSProperties = {
  padding: '4px 10px',
  background: 'transparent',
  border: '1px solid transparent',
  borderRadius: 'var(--r-pill, 999px)',
  fontFamily: 'inherit',
  fontSize: 'var(--t-xs, 12px)',
  fontWeight: 500,
  color: 'var(--fg-muted, #4A5C52)',
  cursor: 'pointer',
  textTransform: 'lowercase',
};

const tabActive: CSSProperties = {
  ...tabBase,
  background: 'var(--emerald-soft, #D1FAE5)',
  color: 'var(--emerald-deep, #047857)',
};

const tabDisabled: CSSProperties = {
  ...tabBase,
  color: 'var(--fg-faint, #94A199)',
  cursor: 'not-allowed',
  opacity: 0.6,
};

const gridStyle: CSSProperties = {
  display: 'grid',
  gridTemplateColumns: 'repeat(3, minmax(0, 1fr))',
  gap: 'var(--s-2, 8px)',
};

const emptyStyle: CSSProperties = {
  gridColumn: '1 / -1',
  fontFamily:
    "var(--font-mono, 'Geist Mono', ui-monospace, monospace)",
  fontSize: 'var(--t-xs, 12px)',
  color: 'var(--fg-subtle, #6B7B72)',
  textAlign: 'center',
  margin: 0,
  padding: 'var(--s-4, 16px) 0',
};

const tileStyle: CSSProperties = {
  display: 'flex',
  flexDirection: 'column',
  alignItems: 'center',
  justifyContent: 'center',
  gap: 'var(--s-1, 4px)',
  padding: 'var(--s-3, 12px) var(--s-2, 8px)',
  background: 'var(--paper, #F5F2EA)',
  border: '1px solid var(--border, #D9D2C0)',
  borderRadius: 'var(--r-md, 8px)',
  cursor: 'pointer',
  transition:
    'background var(--dur-fast, 100ms) var(--ease, cubic-bezier(0.2, 0.7, 0.2, 1)), border-color var(--dur-fast, 100ms) var(--ease, cubic-bezier(0.2, 0.7, 0.2, 1)), box-shadow var(--dur, 160ms) var(--ease, cubic-bezier(0.2, 0.7, 0.2, 1))',
  fontFamily: 'inherit',
};

const tileIconStyle: CSSProperties = {
  width: 20,
  height: 20,
  display: 'inline-flex',
  alignItems: 'center',
  justifyContent: 'center',
  color: 'var(--fg-muted, #4A5C52)',
};

const tileTitleStyle: CSSProperties = {
  fontSize: 'var(--t-xs, 12px)',
  fontWeight: 500,
  color: 'var(--ink, #0E1A14)',
  textAlign: 'center',
};

const patternsStyle: CSSProperties = {
  display: 'flex',
  flexDirection: 'column',
  gap: 'var(--s-4, 16px)',
};

const patternGroupStyle: CSSProperties = {
  display: 'flex',
  flexDirection: 'column',
  gap: 'var(--s-2, 8px)',
};

const patternGroupTitleStyle: CSSProperties = {
  margin: 0,
  fontFamily: 'inherit',
  fontSize: 'var(--t-2xs, 11px)',
  fontWeight: 600,
  letterSpacing: '0.08em',
  textTransform: 'uppercase',
  color: 'var(--fg-subtle, #6B7B72)',
};

const patternGridStyle: CSSProperties = {
  display: 'grid',
  gridTemplateColumns: 'repeat(2, minmax(0, 1fr))',
  gap: 'var(--s-2, 8px)',
};

const patternTileStyle: CSSProperties = {
  display: 'flex',
  flexDirection: 'column',
  gap: 'var(--s-2, 8px)',
  background: 'var(--paper, #F5F2EA)',
  border: '1px solid var(--border, #D9D2C0)',
  borderRadius: 'var(--r-md, 8px)',
  padding: 'var(--s-2, 8px)',
  cursor: 'pointer',
  fontFamily: 'inherit',
  textAlign: 'left',
};

const patternPreviewStyle: CSSProperties = {
  width: '100%',
  aspectRatio: '16 / 9',
  borderRadius: 'var(--r-sm, 6px)',
  background: 'var(--paper-3, #E6E1D2)',
  objectFit: 'cover',
  display: 'block',
};

const patternTitleStyle: CSSProperties = {
  fontSize: 'var(--t-xs, 12px)',
  fontWeight: 500,
  color: 'var(--ink, #0E1A14)',
};

/**
 * The categorized + searchable block picker. Client component.
 */
export function BlockInserter({
  registry,
  onInsert,
  patternRegistry,
  onInsertPattern,
  initialCategory = 'text',
  initialQuery = '',
}: BlockInserterProps) {
  const [query, setQuery] = useState<string>(initialQuery);
  const [category, setCategory] =
    useState<BlockCategory | typeof PATTERNS_TAB>(initialCategory);

  // Recompute when the registry reference changes. We snapshot here so the
  // filter passes both reference equality (for memos that may depend on us)
  // and works without coupling to any subscription mechanism the SDK
  // doesn't expose.
  const all: BlockTypeDefinition[] = useMemo(
    () => registry.list(),
    [registry],
  );

  // Patterns get the same snapshot-on-reference treatment as blocks. When
  // no pattern registry is provided we substitute an empty array — that
  // keeps the rest of the component branch-free and lets the Patterns tab
  // simply skip rendering.
  const patterns: Pattern[] = useMemo(
    () => (patternRegistry !== undefined ? patternRegistry.list() : []),
    [patternRegistry],
  );

  const normalizedQuery = query.trim().toLowerCase();

  // Patterns search across both `name` and `keywords` — the WordPress
  // precedent. Block search stays name-only to preserve the existing
  // behaviour the inserter's tests rely on.
  const patternMatchesQuery = (p: Pattern): boolean => {
    if (normalizedQuery.length === 0) return true;
    if (p.name.toLowerCase().includes(normalizedQuery)) return true;
    if (p.keywords !== undefined) {
      for (const k of p.keywords) {
        if (k.toLowerCase().includes(normalizedQuery)) return true;
      }
    }
    return false;
  };

  const visible = all.filter((def) => {
    if (def.category !== category) return false;
    if (normalizedQuery.length === 0) return true;
    return def.title.toLowerCase().includes(normalizedQuery);
  });

  // Pre-bucket by category once so the category tabs can be lit up only when
  // they have at least one matching entry — this matches the WordPress
  // inserter's "empty tab" affordance.
  const countsByCategory = new Map<BlockCategory, number>();
  for (const def of all) {
    if (
      normalizedQuery.length > 0 &&
      !def.title.toLowerCase().includes(normalizedQuery)
    ) {
      continue;
    }
    countsByCategory.set(
      def.category,
      (countsByCategory.get(def.category) ?? 0) + 1,
    );
  }

  // Pattern count across the entire registry — surfaced both as the
  // Patterns-tab's disabled state and as part of the test contract.
  const visiblePatterns = patterns.filter(patternMatchesQuery);
  const patternsTabEnabled =
    patternRegistry !== undefined && visiblePatterns.length > 0;

  const isPatternsTabActive = category === PATTERNS_TAB;

  return (
    <div
      className="gonext-block-inserter"
      data-testid="block-inserter"
      style={panelStyle}
    >
      <input
        type="search"
        role="searchbox"
        aria-label={
          isPatternsTabActive ? 'Search patterns' : 'Search blocks'
        }
        placeholder={
          isPatternsTabActive ? 'Search patterns' : 'Search blocks'
        }
        value={query}
        onChange={(event) => setQuery(event.target.value)}
        className="gonext-block-inserter__search"
        data-testid="block-inserter-search"
        style={searchStyle}
      />

      <div
        role="tablist"
        aria-label="Block categories"
        className="gonext-block-inserter__tabs"
        style={tabsStyle}
      >
        {INSERTER_CATEGORIES.map((cat) => {
          const isActive = cat === category;
          const count = countsByCategory.get(cat) ?? 0;
          const tabStyle = isActive
            ? tabActive
            : count === 0
              ? tabDisabled
              : tabBase;
          return (
            <button
              key={cat}
              type="button"
              role="tab"
              aria-selected={isActive}
              aria-controls={`gonext-block-inserter-panel-${cat}`}
              disabled={count === 0}
              onClick={() => setCategory(cat)}
              className="gonext-block-inserter__tab"
              data-testid={`block-inserter-tab-${cat}`}
              data-active={isActive ? 'true' : 'false'}
              style={tabStyle}
            >
              {cat}
            </button>
          );
        })}

        {patternRegistry !== undefined ? (
          <button
            key="patterns"
            type="button"
            role="tab"
            aria-selected={isPatternsTabActive}
            aria-controls="gonext-block-inserter-panel-patterns"
            disabled={!patternsTabEnabled && !isPatternsTabActive}
            onClick={() => setCategory(PATTERNS_TAB)}
            className="gonext-block-inserter__tab gonext-block-inserter__tab--patterns"
            data-testid="block-inserter-tab-patterns"
            data-active={isPatternsTabActive ? 'true' : 'false'}
            style={
              isPatternsTabActive
                ? tabActive
                : !patternsTabEnabled
                  ? tabDisabled
                  : tabBase
            }
          >
            patterns
          </button>
        ) : null}
      </div>

      {isPatternsTabActive ? (
        <PatternsPanel
          patterns={visiblePatterns}
          onSelect={(pattern) => {
            if (onInsertPattern === undefined) return;
            onInsertPattern(clonePatternBlocks(pattern.blocks), pattern);
          }}
        />
      ) : (
        <div
          role="tabpanel"
          id={`gonext-block-inserter-panel-${category}`}
          aria-label={`${category} blocks`}
          className="gonext-block-inserter__grid"
          data-testid={`block-inserter-panel-${category}`}
          style={gridStyle}
        >
          {visible.length === 0 ? (
            <p
              className="gonext-block-inserter__empty"
              data-testid="block-inserter-empty"
              style={emptyStyle}
            >
              No blocks match.
            </p>
          ) : (
            visible.map((def) => (
              <BlockTile
                key={def.name}
                def={def}
                onSelect={() =>
                  onInsert({
                    type: def.name,
                    attributes: {},
                    innerBlocks: [],
                  })
                }
              />
            ))
          )}
        </div>
      )}
    </div>
  );
}

interface PatternsPanelProps {
  patterns: Pattern[];
  onSelect: (pattern: Pattern) => void;
}

/**
 * The Patterns tab panel — patterns grouped by category. Within a
 * category, patterns render in registry order. The panel renders an
 * empty-state when no patterns survive the search filter.
 */
function PatternsPanel({ patterns, onSelect }: PatternsPanelProps) {
  if (patterns.length === 0) {
    return (
      <div
        role="tabpanel"
        id="gonext-block-inserter-panel-patterns"
        aria-label="patterns"
        className="gonext-block-inserter__grid"
        data-testid="block-inserter-panel-patterns"
        style={gridStyle}
      >
        <p
          className="gonext-block-inserter__empty"
          data-testid="block-inserter-empty-patterns"
          style={emptyStyle}
        >
          No patterns match.
        </p>
      </div>
    );
  }

  // Group preserving first-occurrence order so plugin-registered
  // categories appear after the built-in ones in a deterministic way.
  const groups = new Map<string, Pattern[]>();
  for (const p of patterns) {
    const list = groups.get(p.category);
    if (list === undefined) {
      groups.set(p.category, [p]);
    } else {
      list.push(p);
    }
  }

  return (
    <div
      role="tabpanel"
      id="gonext-block-inserter-panel-patterns"
      aria-label="patterns"
      className="gonext-block-inserter__patterns"
      data-testid="block-inserter-panel-patterns"
      style={patternsStyle}
    >
      {[...groups.entries()].map(([cat, list]) => (
        <section
          key={cat}
          className="gonext-block-inserter__pattern-group"
          data-testid={`block-inserter-pattern-group-${cat}`}
          aria-label={`${cat} patterns`}
          style={patternGroupStyle}
        >
          <h3
            className="gonext-block-inserter__pattern-group-title"
            style={patternGroupTitleStyle}
          >
            {cat}
          </h3>
          <div
            className="gonext-block-inserter__pattern-grid"
            style={patternGridStyle}
          >
            {list.map((p) => (
              <PatternTile
                key={p.id}
                pattern={p}
                onSelect={() => onSelect(p)}
              />
            ))}
          </div>
        </section>
      ))}
    </div>
  );
}

interface PatternTileProps {
  pattern: Pattern;
  onSelect: () => void;
}

/**
 * Single pattern tile. Renders the preview image (or the SVG fallback
 * the `.svg` placeholder gives us) and the pattern name. Clicking
 * fires `onSelect` — the parent panel deep-clones the BlockTree before
 * calling out to `onInsertPattern`.
 */
function PatternTile({ pattern, onSelect }: PatternTileProps) {
  return (
    <button
      type="button"
      onClick={onSelect}
      className="gonext-block-inserter__pattern-tile"
      data-testid={`block-inserter-pattern-tile-${pattern.id}`}
      title={pattern.description ?? pattern.name}
      style={patternTileStyle}
    >
      {pattern.preview !== undefined ? (
        <img
          src={pattern.preview}
          alt=""
          aria-hidden="true"
          className="gonext-block-inserter__pattern-preview"
          data-testid={`block-inserter-pattern-preview-${pattern.id}`}
          style={patternPreviewStyle}
        />
      ) : (
        <span
          aria-hidden="true"
          className="gonext-block-inserter__pattern-preview gonext-block-inserter__pattern-preview--empty"
          data-testid={`block-inserter-pattern-preview-empty-${pattern.id}`}
          style={patternPreviewStyle}
        />
      )}
      <span
        className="gonext-block-inserter__pattern-title"
        style={patternTitleStyle}
      >
        {pattern.name}
      </span>
    </button>
  );
}

interface BlockTileProps {
  def: BlockTypeDefinition;
  onSelect: () => void;
}

/**
 * Single tile. Icon resolution priority (preserved from before this
 * restyle, plus the new brand-glyph fallback):
 *
 *   1. The registered `icon` is inline SVG → sanitize through the
 *      gn-editor Trusted Types policy (DOMPurify svg profile) and
 *      render via `dangerouslySetInnerHTML`. The policy strips
 *      `<script>` / `<foreignObject>` payloads from plugin-supplied
 *      SVG.
 *   2. The registered `icon` is a non-SVG string (e.g.
 *      `'lucide:dollar-sign'`) → render the string as text. Keeps the
 *      legacy "id stays visible until icon resolution lands" contract
 *      and the test in `block-inserter.test.tsx > renders non-SVG icon
 *      strings as plain text` happy.
 *   3. No `icon` at all → render an inline Lucide-style glyph keyed
 *      off the block name first, then category, with a square
 *      fallback. Lifts the "tiles have an icon" mock requirement
 *      without burdening plugin authors.
 */
function BlockTile({ def, onSelect }: BlockTileProps) {
  const iconHtml = isInlineSvg(def.icon) ? def.icon : undefined;
  const iconText =
    !isInlineSvg(def.icon) && def.icon !== undefined ? def.icon : undefined;
  // The Lucide fallback only fires for blocks with no `icon` at all —
  // never overrides a registered string, which would change the public
  // surface for plugin authors.
  const lucideGlyph =
    iconHtml === undefined && iconText === undefined
      ? pickLucideGlyph(def)
      : undefined;

  // Inline SVG icons route through the gn-editor Trusted Types policy
  // (DOMPurify + SVG profile) so the admin's strict CSP doesn't reject
  // the innerHTML assignment. The wrapper around dangerouslySetInnerHTML
  // is the only sanctioned use of that prop in the editor — see
  // .eslintrc.json for the rule that excludes sanitizeBlockIcon calls.
  const iconProps: React.HTMLAttributes<HTMLSpanElement> =
    iconHtml !== undefined
      ? { dangerouslySetInnerHTML: sanitizeBlockIcon(iconHtml) }
      : iconText !== undefined
        ? { children: iconText }
        : { children: lucideGlyph };

  return (
    <button
      type="button"
      onClick={onSelect}
      className="gonext-block-inserter__tile"
      data-testid={`block-inserter-tile-${def.name}`}
      title={def.description ?? def.title}
      style={tileStyle}
    >
      <span
        aria-hidden="true"
        className="gonext-block-inserter__tile-icon"
        data-testid={`block-inserter-tile-icon-${def.name}`}
        style={tileIconStyle}
        {...iconProps}
      />
      <span
        className="gonext-block-inserter__tile-title"
        style={tileTitleStyle}
      >
        {def.title}
      </span>
    </button>
  );
}

function isInlineSvg(icon: string | undefined): icon is string {
  if (icon === undefined) return false;
  const trimmed = icon.trim();
  return trimmed.startsWith('<svg') || trimmed.startsWith('<?xml');
}

/**
 * Lucide-style glyph map keyed off block name first, then category, with
 * a generic square fallback. We don't take a `lucide-react` dep because
 * the editor surface is intentionally framework-agnostic — the admin
 * app pulls in lucide-react separately for its own chrome. Paths are
 * lifted from https://lucide.dev and trimmed to the 24×24 viewBox
 * Lucide ships.
 */
const lucidePaths: Record<string, ReactNode> = {
  // Type — a serif "T", the canonical "Paragraph" glyph in the mock.
  type: <SvgPaths d="M4 7V4h16v3M9 20h6M12 4v16" />,
  // Heading1 — "H" + a small "1" mark.
  'heading-1': (
    <SvgPaths d="M4 12h8M4 18V6M12 18V6M17 12l3-2v8" />
  ),
  list: <SvgPaths d="M8 6h13M8 12h13M8 18h13M3 6h.01M3 12h.01M3 18h.01" />,
  image: (
    <>
      <SvgRect x={3} y={3} width={18} height={18} rx={2} />
      <SvgCircle cx={9} cy={9} r={2} />
      <SvgPaths d="M21 15l-5-5L5 21" />
    </>
  ),
  'shopping-bag': (
    <SvgPaths d="M6 2L3 6v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2V6l-3-4zM3 6h18M16 10a4 4 0 0 1-8 0" />
  ),
  'code-2': <SvgPaths d="M18 16l4-4-4-4M6 8l-4 4 4 4M14.5 4l-5 16" />,
  quote: (
    <SvgPaths d="M3 21c3 0 7-1 7-8V5c0-1.25-.756-2.017-2-2H4c-1.25 0-2 .75-2 1.972V11c0 1.25.75 2 2 2 1 0 1 0 1 1v1c0 1-1 2-2 2s-1 .008-1 1.031V20c0 1 0 1 1 1zM15 21c3 0 7-1 7-8V5c0-1.25-.757-2.017-2-2h-4c-1.25 0-2 .75-2 1.972V11c0 1.25.75 2 2 2h.75c0 2.25.25 4-2.75 4v3c0 1 0 1 1 1z" />
  ),
  square: <SvgRect x={3} y={3} width={18} height={18} rx={2} />,
};

function pickLucideGlyph(def: BlockTypeDefinition): ReactNode {
  // Explicit name → glyph mapping for the blocks the editor mock
  // calls out. Matches the slash-menu items in
  // docs/design/ui_kits/editor/index.html: Heading 2 / Pull quote /
  // Image / Product / Code block.
  if (def.name === 'core/paragraph') return lucidePaths['type'];
  if (def.name === 'core/heading') return lucidePaths['heading-1'];
  if (def.name === 'core/list') return lucidePaths['list'];
  if (def.name === 'core/image') return lucidePaths['image'];
  if (def.name === 'core/quote') return lucidePaths['quote'];
  if (def.name === 'core/code') return lucidePaths['code-2'];
  if (def.name === 'core/product') return lucidePaths['shopping-bag'];

  // Category fallbacks for blocks we don't know by name.
  switch (def.category) {
    case 'text':
      return lucidePaths['type'];
    case 'media':
      return lucidePaths['image'];
    case 'embed':
      return lucidePaths['code-2'];
    case 'widgets':
    case 'design':
    case 'theme':
    case 'custom':
    default:
      return lucidePaths['square'];
  }
}

/** Inline SVG primitives so we don't haul in a JSX-icon dep. */
function SvgPaths({ d }: { d: string }): ReactNode {
  return (
    <svg
      viewBox="0 0 24 24"
      width="20"
      height="20"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.75"
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      <path d={d} />
    </svg>
  );
}

function SvgRect({
  x,
  y,
  width,
  height,
  rx,
}: {
  x: number;
  y: number;
  width: number;
  height: number;
  rx: number;
}): ReactNode {
  return (
    <svg
      viewBox="0 0 24 24"
      width="20"
      height="20"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.75"
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      <rect x={x} y={y} width={width} height={height} rx={rx} />
    </svg>
  );
}

function SvgCircle({
  cx,
  cy,
  r,
}: {
  cx: number;
  cy: number;
  r: number;
}): ReactNode {
  return (
    <svg
      viewBox="0 0 24 24"
      width="20"
      height="20"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.75"
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      <circle cx={cx} cy={cy} r={r} />
    </svg>
  );
}
