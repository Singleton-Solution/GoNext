/**
 * Tests for the top-level @gonext/blocks-transforms surface.
 *
 * Contract under test:
 *   1. `CORE_TRANSFORMS` contains exactly the documented built-ins.
 *   2. Each transform id is unique.
 *   3. `registerBuiltinTransforms(registry)` populates the registry.
 */
import { describe, expect, it } from 'vitest';
import {
  CORE_TRANSFORMS,
  TransformRegistry,
  registerBuiltinTransforms,
} from './index.ts';

const EXPECTED_IDS = [
  'core/paragraph-to-heading',
  'core/heading-to-paragraph',
  'core/paragraph-to-quote',
  'core/quote-to-paragraph',
  'core/list-to-paragraphs',
  'core/paragraphs-to-list',
  'core/image-to-gallery',
  'core/heading-level-up',
  'core/heading-level-down',
  'core/code-to-paragraph',
  'core/paragraph-to-code',
  'core/columns-to-group',
  'core/group-to-columns',
];

describe('@gonext/blocks-transforms surface', () => {
  it('exports exactly the documented built-ins, in order', () => {
    expect(CORE_TRANSFORMS.map((t) => t.id)).toEqual(EXPECTED_IDS);
  });

  it('every built-in transform id is unique', () => {
    const seen = new Set<string>();
    for (const t of CORE_TRANSFORMS) {
      expect(seen.has(t.id)).toBe(false);
      seen.add(t.id);
    }
  });

  it('registerBuiltinTransforms() populates a fresh registry', () => {
    const r = new TransformRegistry();
    registerBuiltinTransforms(r);
    expect(r.list()).toHaveLength(CORE_TRANSFORMS.length);
    for (const id of EXPECTED_IDS) {
      expect(r.has(id)).toBe(true);
    }
  });
});
