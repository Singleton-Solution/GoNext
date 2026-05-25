/**
 * Tests for the reusable-blocks editor helpers (issue #193).
 */
import { describe, expect, it } from 'vitest';
import type { Block } from '@gonext/blocks-sdk';
import {
  getReusableRef,
  inlineReusableRefs,
  isReusableRef,
  makeReusableRef,
  MISSING_BLOCK_TYPE,
  REUSABLE_BLOCK_TYPE,
  type ReusableEntry,
} from './reusable-blocks.ts';

const entry = (
  id: string,
  content: Block[],
  name = 'snippet',
): ReusableEntry => ({
  id,
  name,
  attrs: {},
  content,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
});

const lookupFor = (entries: Record<string, ReusableEntry>) =>
  async (ref: string) =>
    entries[ref];

describe('makeReusableRef / isReusableRef / getReusableRef', () => {
  it('makeReusableRef creates a canonical core/block ref', () => {
    const node = makeReusableRef('abc');
    expect(node.type).toBe(REUSABLE_BLOCK_TYPE);
    expect(node.attributes.ref).toBe('abc');
  });

  it('isReusableRef matches core/block', () => {
    expect(isReusableRef({ type: REUSABLE_BLOCK_TYPE, attributes: {} })).toBe(
      true,
    );
    expect(isReusableRef({ type: 'core/paragraph', attributes: {} })).toBe(
      false,
    );
  });

  it('getReusableRef returns the ref or undefined', () => {
    expect(getReusableRef(makeReusableRef('abc'))).toBe('abc');
    expect(
      getReusableRef({ type: REUSABLE_BLOCK_TYPE, attributes: {} }),
    ).toBeUndefined();
    expect(
      getReusableRef({ type: REUSABLE_BLOCK_TYPE, attributes: { ref: 99 } }),
    ).toBeUndefined();
  });
});

describe('inlineReusableRefs', () => {
  it('substitutes a single ref with its content', async () => {
    const ref = 'abc-123';
    const inner: Block[] = [
      { type: 'core/paragraph', attributes: { text: 'hello' } },
    ];
    const lookup = lookupFor({ [ref]: entry(ref, inner) });
    const tree: Block[] = [
      makeReusableRef(ref),
      { type: 'core/heading', attributes: { text: 'after' } },
    ];
    const out = await inlineReusableRefs(tree, lookup);
    expect(out).toHaveLength(2);
    expect(out[0]!.type).toBe('core/paragraph');
    expect(out[1]!.type).toBe('core/heading');
  });

  it('substitutes a missing entry with the sentinel', async () => {
    const lookup = lookupFor({});
    const out = await inlineReusableRefs([makeReusableRef('missing')], lookup);
    expect(out).toHaveLength(1);
    expect(out[0]!.type).toBe(MISSING_BLOCK_TYPE);
  });

  it('substitutes a malformed ref with the sentinel', async () => {
    const lookup = lookupFor({});
    const out = await inlineReusableRefs(
      [{ type: REUSABLE_BLOCK_TYPE, attributes: {} }],
      lookup,
    );
    expect(out[0]!.type).toBe(MISSING_BLOCK_TYPE);
  });

  it('recurses into innerBlocks', async () => {
    const ref = 'abc';
    const inner: Block[] = [
      { type: 'core/paragraph', attributes: { text: 'deep' } },
    ];
    const lookup = lookupFor({ [ref]: entry(ref, inner) });
    const tree: Block[] = [
      {
        type: 'core/columns',
        attributes: {},
        innerBlocks: [makeReusableRef(ref)],
      },
    ];
    const out = await inlineReusableRefs(tree, lookup);
    expect(out[0]!.innerBlocks?.[0]!.type).toBe('core/paragraph');
  });

  it('breaks cycles to the sentinel', async () => {
    const a = 'a';
    const b = 'b';
    const lookup = lookupFor({
      [a]: entry(a, [makeReusableRef(b)]),
      [b]: entry(b, [makeReusableRef(a)]),
    });
    const out = await inlineReusableRefs([makeReusableRef(a)], lookup);
    // A inlines its ref to B; B inlines its ref to A but A is on
    // the visiting set, so that becomes a missing sentinel.
    expect(out).toHaveLength(1);
    expect(out[0]!.type).toBe(MISSING_BLOCK_TYPE);
  });

  it('returns an empty array for an empty tree', async () => {
    const lookup = lookupFor({});
    expect(await inlineReusableRefs([], lookup)).toEqual([]);
  });
});
