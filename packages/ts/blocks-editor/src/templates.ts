/**
 * Post-type templates — issue #210.
 *
 * A *template* declares blocks that MUST be present at given positions
 * inside a post of a given post-type. Templates are configured by an
 * admin per post-type; the editor enforces them on the canvas by:
 *
 *  1. Filling missing slots when a post is opened (`applyTemplate`).
 *     A new "page" post-type with `["core/heading", "core/paragraph"]`
 *     gets those two blocks pre-inserted at positions 0 and 1.
 *  2. Marking template-driven blocks as remove-locked + (optionally)
 *     move-locked. The lock travels on `attributes.lock` so the same
 *     canvas affordances that respect manual locks also respect
 *     template locks. The two are indistinguishable from the canvas
 *     walker's point of view — that's the design.
 *  3. Re-applying the template on save: any template slot that has
 *     been deleted gets reinserted (the post-type configuration is
 *     the source of truth, not the post body).
 *
 * The template itself is a small declarative description, not a
 * block-tree fragment. The editor materialises it into Block nodes
 * via `applyTemplate`. Authors who need richer pre-filled content
 * use the block patterns library (`@gonext/blocks-patterns`) — the
 * template scope is intentionally narrow.
 */

import type { Block, BlockTree } from '@gonext/blocks-sdk';
import { isMoveLocked, isRemoveLocked, withLockState } from './locks.ts';

/**
 * A single template slot. `block` is the type name; `attributes`
 * seeds the block's initial attribute payload.
 *
 * `lock` (default `{ remove: true }`) is the lock applied to the
 * materialised block. The default makes template slots remove-locked
 * but movable — which matches Gutenberg's behaviour and matches the
 * "you can rearrange, but you can't delete" usability story.
 */
export interface TemplateSlot {
  /** Block type name, e.g. "core/heading". */
  block: string;
  /** Initial attribute payload for the block. Defaults to `{}`. */
  attributes?: Record<string, unknown>;
  /**
   * Lock applied to the materialised block. Defaults to remove-only.
   * Pass `{ move: true, remove: true }` for a fully-pinned slot or
   * `{}` for a soft template that just seeds content.
   */
  lock?: { move?: boolean; remove?: boolean };
}

/**
 * A template is an ordered list of slots. Empty array means "no
 * template" — the helper is a no-op for that post-type.
 */
export type Template = TemplateSlot[];

/**
 * Apply a template to a block tree. The tree is mutated only at the
 * positions a slot occupies:
 *
 *  - Position i is empty or holds a block whose type doesn't match
 *    the slot's `block`: insert a fresh slot block at i.
 *  - Position i holds a block whose type matches the slot's `block`:
 *    keep the existing block (we don't overwrite author attributes),
 *    but enforce the slot's lock by merging it onto the block's
 *    `attributes.lock`. The merge is a UNION — a template that says
 *    `lock.remove: true` will not weaken an author's manually-applied
 *    `lock.move: true`.
 *
 * Blocks past the template's length are passed through verbatim.
 * Templates do NOT shrink the tree; an author who deletes a slot's
 * block will see it reappear on the next save (separately handled by
 * `restoreTemplate`).
 */
export function applyTemplate(tree: BlockTree, template: Template): BlockTree {
  if (template.length === 0) return tree;
  const out: Block[] = [];
  for (let i = 0; i < Math.max(tree.length, template.length); i++) {
    const slot = template[i];
    const existing = tree[i];
    if (slot === undefined) {
      if (existing !== undefined) out.push(existing);
      continue;
    }
    if (existing !== undefined && existing.type === slot.block) {
      out.push(mergeSlotLock(existing, slot));
      continue;
    }
    out.push(materialiseSlot(slot));
  }
  return out;
}

/**
 * Re-fill any template slot that's gone missing. Called on save so a
 * user who deleted a template-locked block (via, say, a stale undo
 * path that ignored the lock) still ends up with a post that conforms
 * to the post-type contract.
 *
 * Unlike `applyTemplate`, `restoreTemplate` does NOT merge locks onto
 * existing nodes — its job is purely to reinsert deletions. Callers
 * that want both passes call `applyTemplate(restoreTemplate(tree, t), t)`.
 */
export function restoreTemplate(tree: BlockTree, template: Template): BlockTree {
  if (template.length === 0) return tree;
  // Map of block.type → indexes already used to satisfy a slot. We
  // walk the template in order and consume the first matching block
  // in the tree; any unconsumed slots get a fresh materialisation.
  const claimed = new Set<number>();
  const slotResolutions: Block[] = template.map((slot) => {
    const idx = tree.findIndex(
      (b, i) => !claimed.has(i) && b.type === slot.block,
    );
    if (idx >= 0) {
      claimed.add(idx);
      // tree[idx] is defined here because findIndex returned a
      // valid index; TS doesn't narrow that through the predicate,
      // so an explicit assertion keeps the return type honest.
      const found = tree[idx];
      if (found !== undefined) return found;
    }
    return materialiseSlot(slot);
  });
  // Trailing (non-template) blocks survive in original order.
  const trailing = tree.filter((_, i) => !claimed.has(i));
  return [...slotResolutions, ...trailing];
}

/**
 * Materialise a single template slot into a concrete block node.
 *
 * The default lock is `{ remove: true }` — a movable but
 * undeletable slot. Authors who want pinned slots pass an explicit
 * `lock` on the slot definition.
 */
function materialiseSlot(slot: TemplateSlot): Block {
  const baseAttrs = slot.attributes ? { ...slot.attributes } : {};
  const block: Block = {
    type: slot.block,
    attributes: baseAttrs,
  };
  const slotLock = slot.lock ?? { remove: true };
  return withLockState(block, slotLock);
}

/**
 * Merge a template slot's lock onto an existing block. The merge is
 * a UNION: a slot that says `lock.remove: true` cannot un-lock an
 * author's manual `lock.move: true`. This matches user expectation
 * — the template tightens, never loosens.
 */
function mergeSlotLock(existing: Block, slot: TemplateSlot): Block {
  const slotLock = slot.lock ?? { remove: true };
  const currentMove = isMoveLocked(existing);
  const currentRemove = isRemoveLocked(existing);
  return withLockState(existing, {
    move: currentMove || slotLock.move === true,
    remove: currentRemove || slotLock.remove === true,
  });
}
