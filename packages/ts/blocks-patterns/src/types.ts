/**
 * Canonical TypeScript types for a block pattern.
 *
 * A **pattern** is a named BlockTree fragment authors can insert as a
 * starter shape. Unlike a registered block type (`BlockTypeDefinition`),
 * a pattern is data: it carries no Edit component, no save serializer,
 * no JSON Schema. The editor inserts a pattern's `blocks` into the
 * current tree the same way it inserts a single fresh block.
 *
 * Patterns are intentionally *flat* records — easy to ship via JSON, easy
 * to seed from a database (the synced-pattern feature in a later issue),
 * and easy to validate via the existing `BlockRegistry.validate()` path
 * since the `blocks` field is exactly a `BlockTree`.
 */
import type { BlockTree } from '@gonext/blocks-sdk';
import type { PatternCategory } from './categories.ts';

/**
 * A single pattern record. The shape mirrors the WordPress
 * `register_block_pattern( $slug, $args )` contract, narrowed and
 * strongly typed so authors can't trip themselves up.
 *
 * Field rationale:
 *
 *  - **`id`** — namespaced slug, e.g. `core/hero-with-cta`. Used as the
 *    registry key and as the React `key` in the inserter grid. Plugins
 *    should use their plugin slug as the namespace (`my-plugin/foo`).
 *  - **`name`** — short human label rendered in the inserter tile.
 *  - **`category`** — one of `BUILT_IN_PATTERN_CATEGORIES` (or any
 *    arbitrary string for plugin-defined groupings).
 *  - **`description`** — one-line tooltip / aria-description.
 *  - **`keywords`** — extra terms the inserter search input matches on
 *    beyond the visible name. e.g. a "Hero with CTA" pattern might list
 *    `['landing', 'banner']`.
 *  - **`preview`** — relative path to a preview image. Optional —
 *    consumers fall back to a generated SVG when omitted.
 *  - **`blocks`** — the BlockTree the pattern inserts. Must round-trip
 *    through `BlockRegistry.validate()` cleanly when every referenced
 *    block type is registered.
 */
export interface Pattern {
  /** Namespaced slug, e.g. `core/hero-with-cta`. Unique across the registry. */
  id: string;
  /** Short human label rendered in the inserter tile. */
  name: string;
  /** Category for grouping in the inserter. See `PatternCategory`. */
  category: PatternCategory;
  /**
   * One-line description for tooltips and accessible labels. Optional
   * for plugin-defined patterns; first-party patterns ship one.
   */
  description?: string;
  /**
   * Extra search keywords the inserter matches on, beyond `name`. The
   * editor lowercases both sides before comparing.
   */
  keywords?: readonly string[];
  /**
   * Preview asset path relative to the package. The inserter resolves
   * this via a bundler import; if absent, a generated SVG placeholder
   * stands in.
   */
  preview?: string;
  /**
   * The BlockTree this pattern inserts. Validated against the editor's
   * `BlockRegistry` before the pattern reaches the inserter UI.
   */
  blocks: BlockTree;
}
