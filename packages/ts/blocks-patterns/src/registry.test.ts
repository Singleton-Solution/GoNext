/**
 * Tests for `PatternRegistry`.
 *
 * Contract under test:
 *   1. `register()` adds a pattern and `has()`/`get()` reflect it.
 *   2. Duplicate registration throws `DuplicatePatternError`.
 *   3. `{ replace: true }` overrides an existing entry without throwing.
 *   4. `list()` returns entries in registration order.
 *   5. `byCategory()` filters to a single category and preserves order.
 *   6. `categories()` returns the set of categories with at least one entry.
 *   7. `unregister()` removes a pattern and surfaces a boolean.
 *   8. `clear()` empties the registry.
 */
import { describe, expect, it } from 'vitest';
import type { Pattern } from './types.ts';
import { DuplicatePatternError, PatternRegistry } from './registry.ts';

function makePattern(overrides: Partial<Pattern> = {}): Pattern {
  return {
    id: 'test/sample',
    name: 'Sample',
    category: 'hero',
    blocks: [
      { type: 'core/paragraph', attributes: { content: 'Hello' } },
    ],
    ...overrides,
  };
}

describe('PatternRegistry', () => {
  it('registers and retrieves a pattern', () => {
    const r = new PatternRegistry();
    const p = makePattern();
    r.register(p);
    expect(r.has('test/sample')).toBe(true);
    expect(r.get('test/sample')).toBe(p);
  });

  it('returns undefined for an unknown id', () => {
    const r = new PatternRegistry();
    expect(r.get('nope/nada')).toBeUndefined();
    expect(r.has('nope/nada')).toBe(false);
  });

  it('throws DuplicatePatternError on a collision', () => {
    const r = new PatternRegistry();
    r.register(makePattern());
    expect(() => r.register(makePattern())).toThrow(DuplicatePatternError);
    // The error carries the offending id for downstream surfacing.
    try {
      r.register(makePattern());
    } catch (err) {
      expect((err as DuplicatePatternError).patternId).toBe('test/sample');
    }
  });

  it('replaces an existing entry under { replace: true }', () => {
    const r = new PatternRegistry();
    r.register(makePattern({ name: 'Original' }));
    r.register(makePattern({ name: 'Updated' }), { replace: true });
    expect(r.get('test/sample')?.name).toBe('Updated');
  });

  it('list() returns entries in registration order', () => {
    const r = new PatternRegistry();
    r.register(makePattern({ id: 'a/one' }));
    r.register(makePattern({ id: 'a/two' }));
    r.register(makePattern({ id: 'a/three' }));
    expect(r.list().map((p) => p.id)).toEqual([
      'a/one',
      'a/two',
      'a/three',
    ]);
  });

  it('byCategory() filters to one category preserving order', () => {
    const r = new PatternRegistry();
    r.register(makePattern({ id: 'a/h1', category: 'hero' }));
    r.register(makePattern({ id: 'a/f1', category: 'features' }));
    r.register(makePattern({ id: 'a/h2', category: 'hero' }));
    expect(r.byCategory('hero').map((p) => p.id)).toEqual(['a/h1', 'a/h2']);
    expect(r.byCategory('features').map((p) => p.id)).toEqual(['a/f1']);
    expect(r.byCategory('pricing')).toEqual([]);
  });

  it('categories() surfaces categories with at least one entry', () => {
    const r = new PatternRegistry();
    expect(r.categories()).toEqual([]);
    r.register(makePattern({ id: 'a/h1', category: 'hero' }));
    r.register(makePattern({ id: 'a/h2', category: 'hero' }));
    r.register(makePattern({ id: 'a/c1', category: 'cta' }));
    const cats = r.categories();
    expect(cats).toContain('hero');
    expect(cats).toContain('cta');
    expect(cats).toHaveLength(2);
  });

  it('unregister() removes an entry and reports whether it existed', () => {
    const r = new PatternRegistry();
    r.register(makePattern());
    expect(r.unregister('test/sample')).toBe(true);
    expect(r.has('test/sample')).toBe(false);
    expect(r.unregister('test/sample')).toBe(false);
  });

  it('clear() drops every registration', () => {
    const r = new PatternRegistry();
    r.register(makePattern({ id: 'a/one' }));
    r.register(makePattern({ id: 'a/two' }));
    r.clear();
    expect(r.list()).toEqual([]);
  });
});
