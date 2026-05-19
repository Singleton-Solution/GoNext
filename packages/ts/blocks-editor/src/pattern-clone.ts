/**
 * `clonePatternBlocks` — deep-copy a pattern's BlockTree before handing
 * it to the editor's insertion callback.
 *
 * Patterns are registered as immutable fixtures, but the editor mutates
 * its tree state in-place (attribute edits, child inserts, etc.). Without
 * a clone step the pattern fixture would be shared by reference across
 * every insertion and the second author's "Edit hero copy" would mutate
 * the source. We clone defensively here so the inserter's contract stays
 * obvious — "pattern in, fresh tree out, no aliasing".
 *
 * Why not `structuredClone`? It works in modern browsers and Node 18+,
 * but throws on objects containing functions. Patterns are JSON-shaped so
 * `structuredClone` would in fact work today; we still hand-roll the
 * recursion because (a) the recursion is trivial, (b) we know exactly
 * which fields are safe to copy, and (c) we can preserve narrow TS types
 * without an `as unknown as` round trip.
 */
import type { Block, BlockTree } from '@gonext/blocks-sdk';

/**
 * Return a deep copy of `tree`. Attributes are JSON-cloned via
 * `JSON.parse(JSON.stringify(...))`, which is the right primitive because
 * pattern attribute values must be JSON-serialisable to round-trip
 * through `posts.content_blocks`.
 */
export function clonePatternBlocks(tree: BlockTree): BlockTree {
  return tree.map(cloneBlock);
}

function cloneBlock(block: Block): Block {
  const cloned: Block = {
    type: block.type,
    attributes: cloneAttributes(block.attributes),
  };
  if (block.innerBlocks !== undefined && block.innerBlocks.length > 0) {
    cloned.innerBlocks = block.innerBlocks.map(cloneBlock);
  }
  // `clientId` is editor-only and assigned by the editor on insertion;
  // we intentionally do NOT carry one over from the source fixture.
  return cloned;
}

function cloneAttributes(attrs: Record<string, unknown>): Record<string, unknown> {
  // Attribute payloads are guaranteed JSON-shaped (the registry validator
  // rejects anything else), so a JSON round-trip is the simplest correct
  // primitive. Empty attributes round-trip to `{}` cleanly.
  return JSON.parse(JSON.stringify(attrs)) as Record<string, unknown>;
}
