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
 *  - Each tile is a `<button>` carrying the block icon (rendered with
 *    `dangerouslySetInnerHTML` when the icon is an inline SVG string) plus
 *    the title. Clicking calls `onInsert` with a fresh block shape:
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
 */
'use client';

import type {
  Block,
  BlockCategory,
  BlockRegistry,
  BlockTree,
  BlockTypeDefinition,
} from '@gonext/blocks-sdk';
import { useMemo, useState } from 'react';
import type { Pattern, PatternRegistry } from './pattern-types.ts';
import { clonePatternBlocks } from './pattern-clone.ts';

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
    <div className="gonext-block-inserter" data-testid="block-inserter">
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
      />

      <div
        role="tablist"
        aria-label="Block categories"
        className="gonext-block-inserter__tabs"
      >
        {INSERTER_CATEGORIES.map((cat) => {
          const isActive = cat === category;
          const count = countsByCategory.get(cat) ?? 0;
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
        >
          {visible.length === 0 ? (
            <p
              className="gonext-block-inserter__empty"
              data-testid="block-inserter-empty"
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
      >
        <p
          className="gonext-block-inserter__empty"
          data-testid="block-inserter-empty-patterns"
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
    >
      {[...groups.entries()].map(([cat, list]) => (
        <section
          key={cat}
          className="gonext-block-inserter__pattern-group"
          data-testid={`block-inserter-pattern-group-${cat}`}
          aria-label={`${cat} patterns`}
        >
          <h3 className="gonext-block-inserter__pattern-group-title">{cat}</h3>
          <div className="gonext-block-inserter__pattern-grid">
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
    >
      {pattern.preview !== undefined ? (
        <img
          src={pattern.preview}
          alt=""
          aria-hidden="true"
          className="gonext-block-inserter__pattern-preview"
          data-testid={`block-inserter-pattern-preview-${pattern.id}`}
        />
      ) : (
        <span
          aria-hidden="true"
          className="gonext-block-inserter__pattern-preview gonext-block-inserter__pattern-preview--empty"
          data-testid={`block-inserter-pattern-preview-empty-${pattern.id}`}
        />
      )}
      <span className="gonext-block-inserter__pattern-title">
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
 * Single tile. Renders the icon (inline SVG via `dangerouslySetInnerHTML`
 * when the registered value looks like markup; otherwise rendered as text
 * — enough for `lucide:dollar-sign`-style registry ids to remain visible
 * while the editor sorts out icon resolution).
 */
function BlockTile({ def, onSelect }: BlockTileProps) {
  const iconHtml = isInlineSvg(def.icon) ? def.icon : undefined;
  const iconText = !isInlineSvg(def.icon) ? def.icon : undefined;

  return (
    <button
      type="button"
      onClick={onSelect}
      className="gonext-block-inserter__tile"
      data-testid={`block-inserter-tile-${def.name}`}
      title={def.description ?? def.title}
    >
      <span
        aria-hidden="true"
        className="gonext-block-inserter__tile-icon"
        data-testid={`block-inserter-tile-icon-${def.name}`}
        {...(iconHtml !== undefined
          ? { dangerouslySetInnerHTML: { __html: iconHtml } }
          : { children: iconText ?? '' })}
      />
      <span className="gonext-block-inserter__tile-title">{def.title}</span>
    </button>
  );
}

function isInlineSvg(icon: string | undefined): icon is string {
  if (icon === undefined) return false;
  const trimmed = icon.trim();
  return trimmed.startsWith('<svg') || trimmed.startsWith('<?xml');
}
