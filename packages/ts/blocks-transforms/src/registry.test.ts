/**
 * Tests for `TransformRegistry`.
 *
 * Contract under test:
 *   1. `register()` adds a transform and `has()`/`get()` reflect it.
 *   2. Duplicate registration throws `DuplicateTransformError`.
 *   3. `{ replace: true }` overrides an existing entry without throwing.
 *   4. `list()` returns entries in registration order.
 *   5. `from(name)` filters to transforms whose source matches.
 *   6. `from(name, block)` consults each transform's `isMatch`.
 *   7. `to(name)` filters to transforms whose destination matches.
 *   8. `apply(id, block)` runs the transform; unknown id throws.
 *   9. `unregister()` / `clear()` behave as documented.
 */
import { describe, expect, it } from 'vitest';
import type { Block } from '@gonext/blocks-sdk';
import {
  DuplicateTransformError,
  TransformRegistry,
} from './registry.ts';
import type { Transform } from './types.ts';

function makeTransform(overrides: Partial<Transform> = {}): Transform {
  return {
    id: 'test/identity',
    from: 'core/paragraph',
    to: 'core/paragraph',
    label: 'Identity',
    convert: (b) => b,
    ...overrides,
  };
}

function paragraph(content = 'hi'): Block {
  return { type: 'core/paragraph', attributes: { content } };
}

describe('TransformRegistry', () => {
  it('registers and retrieves a transform', () => {
    const r = new TransformRegistry();
    const t = makeTransform();
    r.register(t);
    expect(r.has('test/identity')).toBe(true);
    expect(r.get('test/identity')).toBe(t);
  });

  it('returns undefined for an unknown id', () => {
    const r = new TransformRegistry();
    expect(r.get('nope/nada')).toBeUndefined();
    expect(r.has('nope/nada')).toBe(false);
  });

  it('throws DuplicateTransformError on a collision', () => {
    const r = new TransformRegistry();
    r.register(makeTransform());
    expect(() => r.register(makeTransform())).toThrow(
      DuplicateTransformError,
    );
    try {
      r.register(makeTransform());
    } catch (err) {
      expect((err as DuplicateTransformError).transformId).toBe(
        'test/identity',
      );
    }
  });

  it('replaces an existing entry under { replace: true }', () => {
    const r = new TransformRegistry();
    r.register(makeTransform({ label: 'Old' }));
    r.register(makeTransform({ label: 'New' }), { replace: true });
    expect(r.get('test/identity')?.label).toBe('New');
  });

  it('list() returns entries in registration order', () => {
    const r = new TransformRegistry();
    r.register(makeTransform({ id: 'a/one' }));
    r.register(makeTransform({ id: 'a/two' }));
    r.register(makeTransform({ id: 'a/three' }));
    expect(r.list().map((t) => t.id)).toEqual([
      'a/one',
      'a/two',
      'a/three',
    ]);
  });

  it('from(name) filters to transforms whose source matches', () => {
    const r = new TransformRegistry();
    r.register(makeTransform({ id: 'a/one', from: 'core/paragraph' }));
    r.register(makeTransform({ id: 'a/two', from: 'core/heading' }));
    r.register(makeTransform({ id: 'a/three', from: 'core/paragraph' }));
    expect(r.from('core/paragraph').map((t) => t.id)).toEqual([
      'a/one',
      'a/three',
    ]);
    expect(r.from('core/heading').map((t) => t.id)).toEqual(['a/two']);
    expect(r.from('core/code')).toEqual([]);
  });

  it('from(name, block) consults each transform isMatch predicate', () => {
    const r = new TransformRegistry();
    r.register(
      makeTransform({
        id: 'a/always',
        from: 'core/paragraph',
      }),
    );
    r.register(
      makeTransform({
        id: 'a/only-non-empty',
        from: 'core/paragraph',
        isMatch: (b) =>
          typeof b.attributes['content'] === 'string' &&
          (b.attributes['content'] as string).length > 0,
      }),
    );

    const ids = (block: Block) =>
      r.from('core/paragraph', block).map((t) => t.id);

    expect(ids(paragraph('hello'))).toEqual(['a/always', 'a/only-non-empty']);
    expect(ids(paragraph(''))).toEqual(['a/always']);
  });

  it('to(name) filters to transforms whose destination matches', () => {
    const r = new TransformRegistry();
    r.register(makeTransform({ id: 'a/one', to: 'core/paragraph' }));
    r.register(makeTransform({ id: 'a/two', to: 'core/heading' }));
    r.register(makeTransform({ id: 'a/three', to: 'core/heading' }));
    expect(r.to('core/heading').map((t) => t.id)).toEqual([
      'a/two',
      'a/three',
    ]);
  });

  it('apply(id, block) runs the transform; unknown id throws', () => {
    const r = new TransformRegistry();
    r.register(
      makeTransform({
        id: 'a/upper',
        convert: (b) => ({
          ...b,
          attributes: {
            ...b.attributes,
            content: (b.attributes['content'] as string).toUpperCase(),
          },
        }),
      }),
    );

    const out = r.apply('a/upper', paragraph('hi'));
    expect(out).toEqual({
      type: 'core/paragraph',
      attributes: { content: 'HI' },
    });

    expect(() => r.apply('nope/nada', paragraph())).toThrow(
      /unknown transform/,
    );
  });

  it('unregister() removes an entry and reports whether it existed', () => {
    const r = new TransformRegistry();
    r.register(makeTransform());
    expect(r.unregister('test/identity')).toBe(true);
    expect(r.has('test/identity')).toBe(false);
    expect(r.unregister('test/identity')).toBe(false);
  });

  it('clear() drops every registration', () => {
    const r = new TransformRegistry();
    r.register(makeTransform({ id: 'a/one' }));
    r.register(makeTransform({ id: 'a/two' }));
    r.clear();
    expect(r.list()).toEqual([]);
  });
});
