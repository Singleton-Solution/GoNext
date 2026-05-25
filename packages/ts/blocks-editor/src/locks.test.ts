/**
 * Tests for the per-block lock helpers (issue #210).
 *
 * The locks live on `block.attributes.lock` so they round-trip with
 * the persisted tree. These tests cover the public API surface:
 * `readLockState`, `withLockState`, and the `isLocked`/`isMoveLocked`/
 * `isRemoveLocked` predicates.
 */
import { describe, expect, it } from 'vitest';
import type { Block } from '@gonext/blocks-sdk';
import {
  isLocked,
  isMoveLocked,
  isRemoveLocked,
  LOCK_ATTRIBUTE_KEY,
  readLockState,
  withLockState,
} from './locks.ts';

const paragraph = (attrs: Record<string, unknown> = {}): Block => ({
  type: 'core/paragraph',
  attributes: { text: 'hi', ...attrs },
});

describe('readLockState', () => {
  it('returns no lock for a block without the attribute', () => {
    expect(readLockState(paragraph())).toEqual({ move: false, remove: false });
  });

  it('parses both flags', () => {
    const block = paragraph({ lock: { move: true, remove: true } });
    expect(readLockState(block)).toEqual({ move: true, remove: true });
  });

  it('treats missing flags as false', () => {
    expect(readLockState(paragraph({ lock: { move: true } }))).toEqual({
      move: true,
      remove: false,
    });
    expect(readLockState(paragraph({ lock: { remove: true } }))).toEqual({
      move: false,
      remove: true,
    });
  });

  it('rejects non-object lock attributes', () => {
    // String / number / array / null all coerce to "no lock".
    expect(readLockState(paragraph({ lock: 'true' }))).toEqual({
      move: false,
      remove: false,
    });
    expect(readLockState(paragraph({ lock: 1 }))).toEqual({
      move: false,
      remove: false,
    });
    expect(readLockState(paragraph({ lock: null }))).toEqual({
      move: false,
      remove: false,
    });
  });

  it('rejects non-boolean flag values', () => {
    // A loaded tree that wrote `{move: 1}` should be treated as
    // having no lock — we don't truthy-coerce.
    expect(
      readLockState(paragraph({ lock: { move: 1, remove: 'yes' } })),
    ).toEqual({ move: false, remove: false });
  });
});

describe('withLockState', () => {
  it('writes both flags', () => {
    const block = paragraph();
    const locked = withLockState(block, { move: true, remove: true });
    expect(readLockState(locked)).toEqual({ move: true, remove: true });
  });

  it('removes the lock attribute when both flags are false', () => {
    const block = paragraph({ lock: { move: true, remove: true } });
    const unlocked = withLockState(block, {});
    expect(LOCK_ATTRIBUTE_KEY in unlocked.attributes).toBe(false);
  });

  it('preserves unrelated attributes', () => {
    const block = paragraph({ text: 'kept', extra: 99 });
    const locked = withLockState(block, { move: true });
    expect(locked.attributes.text).toBe('kept');
    expect(locked.attributes.extra).toBe(99);
  });

  it('preserves innerBlocks and clientId', () => {
    const block: Block = {
      type: 'core/columns',
      attributes: {},
      innerBlocks: [paragraph()],
      clientId: 'abc',
    };
    const locked = withLockState(block, { remove: true });
    expect(locked.innerBlocks).toEqual(block.innerBlocks);
    expect(locked.clientId).toBe('abc');
  });

  it('is immutable — does not mutate the input', () => {
    const block = paragraph();
    const original = JSON.parse(JSON.stringify(block));
    withLockState(block, { move: true });
    expect(block).toEqual(original);
  });
});

describe('lock predicates', () => {
  it('isMoveLocked', () => {
    expect(isMoveLocked(paragraph())).toBe(false);
    expect(isMoveLocked(paragraph({ lock: { move: true } }))).toBe(true);
    expect(isMoveLocked(paragraph({ lock: { remove: true } }))).toBe(false);
  });

  it('isRemoveLocked', () => {
    expect(isRemoveLocked(paragraph())).toBe(false);
    expect(isRemoveLocked(paragraph({ lock: { remove: true } }))).toBe(true);
    expect(isRemoveLocked(paragraph({ lock: { move: true } }))).toBe(false);
  });

  it('isLocked is true when EITHER flag is set', () => {
    expect(isLocked(paragraph())).toBe(false);
    expect(isLocked(paragraph({ lock: { move: true } }))).toBe(true);
    expect(isLocked(paragraph({ lock: { remove: true } }))).toBe(true);
    expect(
      isLocked(paragraph({ lock: { move: true, remove: true } })),
    ).toBe(true);
  });
});
