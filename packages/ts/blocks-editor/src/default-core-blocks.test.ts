/**
 * Tests for `defaultCoreBlocks`.
 *
 * Contract: registers `core/paragraph` and `core/heading` into the given
 * registry with real JSON Schema attribute documents. The registry's
 * validator should accept well-formed instances and reject malformed ones.
 */
import { describe, expect, it } from 'vitest';
import { BlockRegistry } from '@gonext/blocks-sdk';
import {
  defaultCoreBlocks,
  headingBlock,
  paragraphBlock,
} from './default-core-blocks.ts';

describe('defaultCoreBlocks', () => {
  it('registers paragraph and heading on the given registry', () => {
    const r = new BlockRegistry();
    defaultCoreBlocks(r);

    expect(r.has('core/paragraph')).toBe(true);
    expect(r.has('core/heading')).toBe(true);

    expect(r.get('core/paragraph')).toBe(paragraphBlock);
    expect(r.get('core/heading')).toBe(headingBlock);
  });

  it('the registered paragraph definition exposes a usable attribute schema', () => {
    const r = new BlockRegistry();
    defaultCoreBlocks(r);

    const ok = r.validate([
      { type: 'core/paragraph', attributes: { text: 'hello' } },
    ]);
    expect(ok.valid).toBe(true);

    const missing = r.validate([
      { type: 'core/paragraph', attributes: {} },
    ]);
    expect(missing.valid).toBe(false);
    expect(missing.errors[0]?.code).toBe('attributes');
  });

  it('the registered heading definition constrains level to 1..6', () => {
    const r = new BlockRegistry();
    defaultCoreBlocks(r);

    expect(
      r.validate([
        { type: 'core/heading', attributes: { level: 2, text: 'hi' } },
      ]).valid,
    ).toBe(true);

    const tooBig = r.validate([
      { type: 'core/heading', attributes: { level: 9, text: 'hi' } },
    ]);
    expect(tooBig.valid).toBe(false);
    expect(tooBig.errors[0]?.code).toBe('attributes');
  });

  it('paragraph and heading belong to the "text" category', () => {
    // The inserter buckets tiles by category — pinning this prevents an
    // accidental category rename from silently making the default
    // inserter empty in tests.
    expect(paragraphBlock.category).toBe('text');
    expect(headingBlock.category).toBe('text');
  });

  it('throws on a second non-replace registration (default contract)', () => {
    const r = new BlockRegistry();
    defaultCoreBlocks(r);
    expect(() => defaultCoreBlocks(r)).toThrow();
  });

  it('supports replace=true for HMR-style re-registration', () => {
    const r = new BlockRegistry();
    defaultCoreBlocks(r);
    // Second pass must not throw when replace is on.
    expect(() => defaultCoreBlocks(r, { replace: true })).not.toThrow();
  });
});
