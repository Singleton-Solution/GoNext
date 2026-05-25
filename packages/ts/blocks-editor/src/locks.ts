/**
 * Block-level lock helpers — issue #210.
 *
 * GoNext blocks can opt into a per-instance lock that prevents the
 * canvas from moving or removing them. The lock state lives on
 * `block.attributes.lock` (see `BlockLockState` in @gonext/blocks-sdk)
 * so it round-trips with the persisted block tree.
 *
 * Why on attributes and not on `supports`?
 *
 *  - `supports.lock` is the **capability**: "this block type CAN be
 *    locked at all" (the inspector then shows a lock toggle).
 *  - `attributes.lock` is the **runtime state**: "this instance IS
 *    locked right now".
 *
 * The two flags compose:
 *
 *  - `lock.move:   true` → drag-and-drop disabled; reorder buttons hidden.
 *  - `lock.remove: true` → delete buttons disabled (toolbar + keyboard).
 *
 * This module exposes:
 *
 *  - `readLockState(block)`     — pull the lock out of attributes (with
 *                                 defensive parsing — bad shapes coerce
 *                                 to "no lock").
 *  - `withLockState(block, ...)`— immutably update the lock state on a
 *                                 block (used by the inspector toggle).
 *  - `isMoveLocked(block)`      — quick predicate for the canvas walker.
 *  - `isRemoveLocked(block)`    — same, for delete affordances.
 *  - `isLocked(block)`          — either flag set; used to decide
 *                                 whether `BlockLockIndicator` renders.
 */

import type { Block, BlockLockState } from '@gonext/blocks-sdk';

/**
 * The attribute key locks live under. Exposed so consumers building
 * custom inspectors don't have to magic-string the lookup.
 */
export const LOCK_ATTRIBUTE_KEY = 'lock';

/**
 * Read the lock state off a block. Returns the canonical
 * `{move: boolean, remove: boolean}` shape — missing flags default
 * to `false` so callers can treat the result as fully-populated.
 *
 * Defensive about the input: an attribute value that isn't an object,
 * or whose `move`/`remove` aren't booleans, is treated as "no lock".
 * A block tree loaded from an old persistence layer that wrote weird
 * values won't crash the canvas.
 */
export function readLockState(block: Block): { move: boolean; remove: boolean } {
  const raw = (block.attributes as Record<string, unknown>)[LOCK_ATTRIBUTE_KEY];
  if (raw === null || typeof raw !== 'object') {
    return { move: false, remove: false };
  }
  const lock = raw as Record<string, unknown>;
  return {
    move: lock.move === true,
    remove: lock.remove === true,
  };
}

/**
 * Return a new block with the given lock state merged into its
 * attributes. Immutable — callers MUST replace the original node in
 * their tree (the editor's state store does the splice).
 *
 * Passing `{}` removes both flags, which removes the lock attribute
 * altogether — that keeps the persisted JSON tidy when an author
 * un-locks a block.
 */
export function withLockState<A extends Record<string, unknown>>(
  block: Block<A>,
  next: BlockLockState,
): Block<A> {
  const move = next.move === true;
  const remove = next.remove === true;
  const nextAttrs = { ...block.attributes } as Record<string, unknown>;
  if (!move && !remove) {
    delete nextAttrs[LOCK_ATTRIBUTE_KEY];
  } else {
    const lock: BlockLockState = {};
    if (move) lock.move = true;
    if (remove) lock.remove = true;
    nextAttrs[LOCK_ATTRIBUTE_KEY] = lock;
  }
  // Re-build the block in a way that preserves every other field —
  // `innerBlocks` and `clientId` are passed through verbatim.
  const out: Block<A> = {
    type: block.type,
    attributes: nextAttrs as A,
  };
  if (block.innerBlocks !== undefined) out.innerBlocks = block.innerBlocks;
  if (block.clientId !== undefined) out.clientId = block.clientId;
  return out;
}

/** True when the block's move-lock is on. */
export function isMoveLocked(block: Block): boolean {
  return readLockState(block).move;
}

/** True when the block's remove-lock is on. */
export function isRemoveLocked(block: Block): boolean {
  return readLockState(block).remove;
}

/** True when ANY lock flag is on. Drives the `BlockLockIndicator`. */
export function isLocked(block: Block): boolean {
  const lock = readLockState(block);
  return lock.move || lock.remove;
}
