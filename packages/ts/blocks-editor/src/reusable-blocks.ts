/**
 * Reusable / synced blocks — editor-side helpers for issue #193.
 *
 * The block type `core/block` carries a single attribute, `ref`, that
 * resolves to a UUID in the `reusable_blocks` table. The Go-side
 * renderer (`packages/go/blocks/reusable/resolve.go`) inlines the
 * referenced tree on read; this module is the editor's view of the
 * same contract.
 *
 * What lives here:
 *
 *  - `REUSABLE_BLOCK_TYPE` constant — the canonical wire name. Match
 *    the Go constant of the same value.
 *  - `MISSING_BLOCK_TYPE` constant — sentinel for unresolved refs.
 *  - `makeReusableRef(uuid)` — build a fresh `core/block` placeholder
 *    node for the canvas inserter.
 *  - `isReusableRef(block)` — predicate, true when `block.type` is
 *    `core/block`.
 *  - `getReusableRef(block)` — extract the UUID, or undefined when the
 *    attribute is missing/malformed.
 *  - `inlineReusableRefs(tree, lookup)` — client-side resolver that
 *    substitutes refs in a tree. The editor uses this when it loads
 *    a post: the API returns the unresolved tree (refs intact, so
 *    edits can propagate back), but the canvas walker needs an
 *    inlined view to render.
 *
 * The resolver is async because the lookup is async (network fetch
 * against the admin REST surface). A missing ref becomes a
 * `core/missing` sentinel — matches the Go-side behaviour exactly.
 */

import type { Block, BlockTree } from '@gonext/blocks-sdk';

/**
 * Canonical wire name for a reusable-block reference. Matches the
 * `RefBlockType` constant in `packages/go/blocks/reusable/model.go`.
 */
export const REUSABLE_BLOCK_TYPE = 'core/block';

/**
 * Canonical sentinel substituted for an unresolved reference (target
 * deleted, cycle detected, malformed attrs). Matches
 * `MissingBlockType` in the Go package.
 */
export const MISSING_BLOCK_TYPE = 'core/missing';

/**
 * The attribute payload carried by a `core/block` reference. The
 * intersection with `Record<string, unknown>` satisfies the SDK's
 * `BlockAttributes` constraint (which requires an index signature)
 * without giving up the typed `ref` accessor on the consumer side.
 */
export type ReusableRefAttrs = {
  /** UUID of the row in `reusable_blocks`. */
  ref: string;
} & Record<string, unknown>;

/**
 * The decoded shape of a reusable-block entry coming back from the
 * admin REST list/get endpoints. Mirrors `ReusableView` on the
 * server side.
 */
export interface ReusableEntry {
  id: string;
  name: string;
  attrs: Record<string, unknown>;
  content: BlockTree;
  created_at: string;
  updated_at: string;
}

/**
 * Build a fresh placeholder block referencing the given UUID. Used
 * by the inserter when an author picks a reusable block from the
 * library.
 */
export function makeReusableRef(uuid: string): Block<ReusableRefAttrs> {
  return {
    type: REUSABLE_BLOCK_TYPE,
    attributes: { ref: uuid },
  };
}

/** True when the block is a reusable-block reference. */
export function isReusableRef(block: Block): boolean {
  return block.type === REUSABLE_BLOCK_TYPE;
}

/**
 * Extract the UUID from a reusable-block reference, or undefined if
 * the attribute is missing or not a string. The editor's resolver
 * uses this to collect every ref it needs to fetch in a single
 * round-trip.
 */
export function getReusableRef(block: Block): string | undefined {
  const ref = (block.attributes as Record<string, unknown>).ref;
  return typeof ref === 'string' && ref.length > 0 ? ref : undefined;
}

/**
 * Lookup signature for `inlineReusableRefs`. The editor wires this
 * to the admin REST endpoint (or a localStorage cache for tests).
 * A nullish return signals "not found" — the resolver substitutes
 * the missing sentinel.
 */
export type ReusableLookup = (
  ref: string,
) => Promise<ReusableEntry | undefined>;

/**
 * Inline every `core/block` reference in `tree` with the referenced
 * entry's content. Recurses into nested innerBlocks. Cycles are
 * broken at the first repeat; the unresolved second visit becomes a
 * `core/missing` sentinel, matching the Go resolver.
 *
 * The returned tree is fresh — the input is not mutated.
 */
export async function inlineReusableRefs(
  tree: BlockTree,
  lookup: ReusableLookup,
): Promise<BlockTree> {
  return walkInline(tree, lookup, new Set<string>());
}

async function walkInline(
  tree: BlockTree,
  lookup: ReusableLookup,
  visiting: Set<string>,
): Promise<BlockTree> {
  const out: Block[] = [];
  for (const node of tree) {
    if (isReusableRef(node)) {
      const ref = getReusableRef(node);
      if (ref === undefined) {
        out.push(missingSentinel('invalid_ref'));
        continue;
      }
      if (visiting.has(ref)) {
        out.push(missingSentinel('cycle'));
        continue;
      }
      const entry = await lookup(ref);
      if (entry === undefined) {
        out.push(missingSentinel('not_found'));
        continue;
      }
      visiting.add(ref);
      const inlined = await walkInline(entry.content, lookup, visiting);
      visiting.delete(ref);
      out.push(...inlined);
      continue;
    }
    if (node.innerBlocks !== undefined && node.innerBlocks.length > 0) {
      const innerBlocks = await walkInline(node.innerBlocks, lookup, visiting);
      out.push({ ...node, innerBlocks });
    } else {
      out.push(node);
    }
  }
  return out;
}

function missingSentinel(reason: string): Block<{ reason: string }> {
  return {
    type: MISSING_BLOCK_TYPE,
    attributes: { reason },
  };
}
