/**
 * Structural types the editor uses to interoperate with
 * `@gonext/blocks-patterns` **without** depending on that package.
 *
 * Patterns are pure data fixtures — a Pattern is a small record carrying
 * an id, name, category, optional preview path, optional description /
 * keywords, and a `blocks: BlockTree`. Both the patterns package and the
 * editor's inserter agree on the same shape, but pulling in
 * `@gonext/blocks-patterns` from the editor would create a dependency
 * cycle (the patterns are authored against `@gonext/blocks-core`'s block
 * types, and the editor is the consumer of patterns).
 *
 * The structural types here mirror `@gonext/blocks-patterns`'s exports
 * exactly so plumbing a real PatternRegistry into `<BlockInserter>`
 * Just Works without runtime adapters.
 */
import type { BlockTree } from '@gonext/blocks-sdk';

/**
 * Mirror of `@gonext/blocks-patterns` `Pattern`. See that package for the
 * authoritative field-by-field rationale.
 */
export interface Pattern {
  /** Namespaced slug, unique across the registry. */
  id: string;
  /** Short human label rendered in the inserter tile. */
  name: string;
  /** Category for grouping in the inserter. */
  category: string;
  /** Optional one-line description for tooltips. */
  description?: string;
  /** Optional extra search keywords. */
  keywords?: readonly string[];
  /** Optional preview asset path (resolved by the host bundler). */
  preview?: string;
  /** The BlockTree the pattern inserts. */
  blocks: BlockTree;
}

/**
 * Mirror of `@gonext/blocks-patterns` `PatternRegistry`. We type only
 * the surface the inserter uses (`list()`); other methods may exist on
 * concrete implementations but are not required here.
 */
export interface PatternRegistry {
  list(): Pattern[];
}
