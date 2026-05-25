/**
 * `@gonext/blocks-core` aggregate tests.
 *
 * Verifies that:
 *  1. `registerCoreBlocks(registry)` lands every expected name in the registry
 *     exactly once.
 *  2. Each registered block's attribute schema validates a representative
 *     instance through the registry's own `validate()` API.
 *  3. Duplicate registration throws unless `replace: true` is passed.
 *  4. Every core block is namespaced under `core/...`.
 */
import { describe, expect, it } from 'vitest';
import { BlockRegistry } from '@gonext/blocks-sdk';
import {
  CORE_BLOCKS,
  registerCoreBlocks,
} from './index.ts';

const EXPECTED_NAMES = [
  'core/paragraph',
  'core/heading',
  'core/list',
  'core/image',
  'core/quote',
  'core/code',
  'core/separator',
  'core/spacer',
  'core/columns',
  'core/group',
  'core/table',
  'core/gallery',
  'core/video',
  'core/button',
  'core/file',
  'core/embed',
  'core/media-text',
  'core/navigation',
  'core/query',
] as const;

describe('registerCoreBlocks', () => {
  it('lands every expected name in the registry', () => {
    const r = new BlockRegistry();
    registerCoreBlocks(r);
    for (const name of EXPECTED_NAMES) {
      expect(r.has(name)).toBe(true);
    }
    expect(r.list()).toHaveLength(EXPECTED_NAMES.length);
  });

  it('CORE_BLOCKS is exactly the 19 expected entries, in the expected order', () => {
    expect(CORE_BLOCKS.map((b) => b.definition.name)).toStrictEqual(
      EXPECTED_NAMES,
    );
  });

  it('throws on a second non-replace registration', () => {
    const r = new BlockRegistry();
    registerCoreBlocks(r);
    expect(() => registerCoreBlocks(r)).toThrow();
  });

  it('accepts replace=true for HMR-style re-registration', () => {
    const r = new BlockRegistry();
    registerCoreBlocks(r);
    expect(() => registerCoreBlocks(r, { replace: true })).not.toThrow();
  });

  it('every block lives under the `core/` namespace', () => {
    for (const block of CORE_BLOCKS) {
      expect(block.definition.name.startsWith('core/')).toBe(true);
    }
  });

  it('every block exposes lazy edit and save factories', () => {
    for (const block of CORE_BLOCKS) {
      expect(typeof block.definition.edit).toBe('function');
      // Static blocks (every core block today) carry a save factory too.
      expect(typeof block.definition.save).toBe('function');
    }
  });

  it('the registry validates a tree containing every core block', () => {
    const r = new BlockRegistry();
    registerCoreBlocks(r);
    const tree = [
      { type: 'core/paragraph', attributes: { content: 'p' } },
      { type: 'core/heading', attributes: { content: 'h', level: 2 } },
      { type: 'core/list', attributes: { ordered: false, values: ['a'] } },
      {
        type: 'core/image',
        attributes: { url: 'https://x.test/a.png', alt: 'a' },
      },
      { type: 'core/quote', attributes: { value: 'q' } },
      { type: 'core/code', attributes: { content: 'x' } },
      { type: 'core/separator', attributes: {} },
      { type: 'core/spacer', attributes: { height: 8 } },
      {
        type: 'core/columns',
        attributes: { columns: 2 },
        innerBlocks: [
          { type: 'core/paragraph', attributes: { content: 'inner' } },
        ],
      },
      {
        type: 'core/group',
        attributes: {},
        innerBlocks: [
          { type: 'core/paragraph', attributes: { content: 'inside' } },
        ],
      },
      { type: 'core/table', attributes: { body: [['1']] } },
      {
        type: 'core/gallery',
        attributes: {
          images: [{ url: 'https://x/a.png', alt: 'A' }],
        },
      },
      { type: 'core/video', attributes: { src: 'https://x/v.mp4' } },
      { type: 'core/button', attributes: { text: 'Go' } },
      {
        type: 'core/file',
        attributes: { href: 'https://x/a.pdf', fileName: 'a.pdf' },
      },
      { type: 'core/embed', attributes: { url: 'https://youtu.be/abc' } },
      {
        type: 'core/media-text',
        attributes: { mediaUrl: 'https://x/a.png', mediaAlt: 'A' },
        innerBlocks: [
          { type: 'core/paragraph', attributes: { content: 'side' } },
        ],
      },
      {
        type: 'core/navigation',
        attributes: {
          items: [
            { label: 'Home', url: '/' },
            { label: 'Blog', url: '/blog' },
          ],
        },
      },
      {
        type: 'core/query',
        attributes: { limit: 5 },
        innerBlocks: [
          { type: 'core/heading', attributes: { content: 'Post', level: 3 } },
        ],
      },
    ];
    const result = r.validate(tree);
    expect(result.errors).toStrictEqual([]);
    expect(result.valid).toBe(true);
  });
});
