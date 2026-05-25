/**
 * Tests for the post-type template helpers (issue #210).
 *
 * Templates declare blocks that MUST exist at given positions. The
 * editor applies them at load time and re-applies on save so an
 * author can't delete a slot away.
 */
import { describe, expect, it } from 'vitest';
import { applyTemplate, restoreTemplate, type Template } from './templates.ts';
import { isMoveLocked, isRemoveLocked } from './locks.ts';

const heading = (text = 'Title'): Block => ({
  type: 'core/heading',
  attributes: { text },
});
const paragraph = (text = 'body'): Block => ({
  type: 'core/paragraph',
  attributes: { text },
});

// re-imported here to keep the tests self-contained
import type { Block } from '@gonext/blocks-sdk';

const pageTemplate: Template = [
  { block: 'core/heading', attributes: { text: 'Page Title' } },
  { block: 'core/paragraph' },
];

describe('applyTemplate', () => {
  it('fills missing slots on an empty tree', () => {
    const out = applyTemplate([], pageTemplate);
    expect(out).toHaveLength(2);
    expect(out[0]!.type).toBe('core/heading');
    expect(out[1]!.type).toBe('core/paragraph');
  });

  it('preserves existing blocks at matching positions', () => {
    const existing: Block[] = [heading('Custom'), paragraph('Custom body')];
    const out = applyTemplate(existing, pageTemplate);
    expect(out[0]!.attributes.text).toBe('Custom');
    expect(out[1]!.attributes.text).toBe('Custom body');
  });

  it('replaces blocks at mismatched positions', () => {
    const existing: Block[] = [paragraph('wrong slot')];
    const out = applyTemplate(existing, pageTemplate);
    expect(out[0]!.type).toBe('core/heading');
    expect(out[1]!.type).toBe('core/paragraph');
  });

  it('default slot is remove-locked, movable', () => {
    const out = applyTemplate([], pageTemplate);
    expect(isRemoveLocked(out[0]!)).toBe(true);
    expect(isMoveLocked(out[0]!)).toBe(false);
  });

  it('honours explicit slot lock', () => {
    const t: Template = [
      { block: 'core/heading', lock: { move: true, remove: true } },
    ];
    const out = applyTemplate([], t);
    expect(isMoveLocked(out[0]!)).toBe(true);
    expect(isRemoveLocked(out[0]!)).toBe(true);
  });

  it('honours soft (no-lock) slot', () => {
    const t: Template = [{ block: 'core/heading', lock: {} }];
    const out = applyTemplate([], t);
    expect(isMoveLocked(out[0]!)).toBe(false);
    expect(isRemoveLocked(out[0]!)).toBe(false);
  });

  it('template tightens existing manual lock (never weakens)', () => {
    // Existing block has move-lock; template adds remove-lock.
    const existing: Block[] = [
      { type: 'core/heading', attributes: { text: 't', lock: { move: true } } },
    ];
    const out = applyTemplate(existing, [
      { block: 'core/heading', lock: { remove: true } },
    ]);
    expect(isMoveLocked(out[0]!)).toBe(true);
    expect(isRemoveLocked(out[0]!)).toBe(true);
  });

  it('passes trailing blocks through verbatim', () => {
    const existing: Block[] = [
      heading(),
      paragraph(),
      paragraph('extra'),
      heading('trailing'),
    ];
    const out = applyTemplate(existing, pageTemplate);
    expect(out).toHaveLength(4);
    expect(out[2]!.attributes.text).toBe('extra');
    expect(out[3]!.attributes.text).toBe('trailing');
  });

  it('empty template is a no-op', () => {
    const existing: Block[] = [heading()];
    expect(applyTemplate(existing, [])).toEqual(existing);
  });
});

describe('restoreTemplate', () => {
  it('reinserts deleted slots', () => {
    const existing: Block[] = [paragraph('only paragraph')];
    const out = restoreTemplate(existing, pageTemplate);
    expect(out).toHaveLength(2);
    expect(out[0]!.type).toBe('core/heading');
    expect(out[1]!.type).toBe('core/paragraph');
  });

  it('consumes existing blocks for matching slot types', () => {
    // The paragraph already in the tree satisfies the paragraph slot;
    // a fresh heading materialises for the missing slot.
    const existing: Block[] = [paragraph('kept')];
    const out = restoreTemplate(existing, pageTemplate);
    expect(out[1]!.attributes.text).toBe('kept');
  });

  it('keeps trailing non-template blocks at the end', () => {
    const existing: Block[] = [
      heading(),
      paragraph(),
      { type: 'core/image', attributes: {} },
    ];
    const out = restoreTemplate(existing, pageTemplate);
    expect(out).toHaveLength(3);
    expect(out[2]!.type).toBe('core/image');
  });

  it('empty template is a no-op', () => {
    const existing: Block[] = [paragraph()];
    expect(restoreTemplate(existing, [])).toEqual(existing);
  });
});
