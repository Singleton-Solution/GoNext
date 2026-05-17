/**
 * Tests for the block re-export surface.
 *
 * `blocks.ts` is a pure re-export, so the tests below are deliberately
 * minimal: they verify that the names theme authors expect to import
 * from `@gonext/theme-sdk` are reachable at the *type* level. If a
 * symbol disappears from `@gonext/blocks-sdk` without a corresponding
 * update here, the file fails to compile — same drift detector as the
 * theme-json types.
 */

import { describe, expect, it } from 'vitest';
import type {
  Block,
  BlockAttributes,
  BlockTree,
  ValidationResult,
} from './blocks.ts';

describe('blocks re-export surface', () => {
  it('Block, BlockTree, BlockAttributes typecheck against literal data', () => {
    const attrs: BlockAttributes = { foo: 'bar', n: 42 };
    const block: Block = { type: 'core/paragraph', attributes: attrs };
    const tree: BlockTree = [
      block,
      { type: 'core/heading', attributes: { level: 2, content: 'Hi' } },
    ];
    expect(tree).toHaveLength(2);
    expect(tree[0]?.type).toBe('core/paragraph');
    expect((tree[0]?.attributes['foo'] as string | undefined)).toBe('bar');
  });

  it('ValidationResult is reachable for theme tooling', () => {
    const ok: ValidationResult = { valid: true, errors: [] };
    expect(ok.valid).toBe(true);
  });
});
