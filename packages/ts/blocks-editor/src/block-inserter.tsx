/**
 * `<BlockInserter>` — the categorized, searchable grid that lets authors
 * pick a registered block to insert.
 *
 * Behavior contract (issue #79 acceptance criteria):
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
 *
 * Why not memoise the filter? The registry is a registration-time thing —
 * registries typically hold tens of entries, not thousands. A linear filter
 * per keystroke is comfortably under any UX-relevant threshold, and skipping
 * `useMemo` keeps the surface easier to reason about.
 */
'use client';

import type {
  Block,
  BlockCategory,
  BlockRegistry,
  BlockTypeDefinition,
} from '@gonext/blocks-sdk';
import { useMemo, useState } from 'react';

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

export interface BlockInserterProps {
  /** Source of truth for what blocks exist. Required. */
  registry: BlockRegistry;
  /**
   * Called when a tile is clicked. Receives a freshly-minted block shape
   * (type + empty attributes + empty innerBlocks).
   */
  onInsert: (block: Block) => void;
  /**
   * Initial active category. Defaults to `'text'`.
   */
  initialCategory?: BlockCategory;
  /**
   * Initial search query. Defaults to `''` (no filter). Mostly useful for
   * tests that want to assert on a pre-filtered state.
   */
  initialQuery?: string;
}

/**
 * The categorized + searchable block picker. Client component.
 */
export function BlockInserter({
  registry,
  onInsert,
  initialCategory = 'text',
  initialQuery = '',
}: BlockInserterProps) {
  const [query, setQuery] = useState<string>(initialQuery);
  const [category, setCategory] = useState<BlockCategory>(initialCategory);

  // Recompute when the registry reference changes. We snapshot here so the
  // filter passes both reference equality (for memos that may depend on us)
  // and works without coupling to any subscription mechanism the SDK
  // doesn't expose.
  const all: BlockTypeDefinition[] = useMemo(
    () => registry.list(),
    [registry],
  );

  const normalizedQuery = query.trim().toLowerCase();
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

  return (
    <div className="gonext-block-inserter" data-testid="block-inserter">
      <input
        type="search"
        role="searchbox"
        aria-label="Search blocks"
        placeholder="Search blocks"
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
      </div>

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
    </div>
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
