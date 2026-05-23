/**
 * Tests for the top-level @gonext/blocks-patterns surface.
 *
 * Contract under test:
 *   1. `CORE_PATTERNS` contains exactly the twenty first-party patterns,
 *      in the documented insertion order. The original ten retain their
 *      positions; the ten additions follow.
 *   2. Each pattern carries the required fields with non-empty values.
 *   3. Each pattern's `blocks` field round-trips through the blocks-sdk
 *      `BlockRegistry.validate()` cleanly once `@gonext/blocks-core`'s
 *      types are registered.
 *   4. `registerCorePatterns(registry)` registers every CORE_PATTERN.
 *   5. Pattern ids and category strings match the documented set.
 */
import { describe, expect, it } from 'vitest';
import { BlockRegistry } from '@gonext/blocks-sdk';
import { registerCoreBlocks } from '@gonext/blocks-core';
import {
  CORE_PATTERNS,
  PatternRegistry,
  registerCorePatterns,
} from './index.ts';
import { BUILT_IN_PATTERN_CATEGORIES } from './categories.ts';

const EXPECTED_IDS = [
  // Original ten — order preserved from #401.
  'core/hero-with-cta',
  'core/three-column-features',
  'core/pricing-three-tier',
  'core/testimonial-grid',
  'core/cta-banner',
  'core/gallery-masonry',
  'core/contact-form',
  'core/header-logo-nav',
  'core/footer-multi-column',
  'core/post-grid',
  // Ten additions.
  'core/faq-accordion',
  'core/team-grid',
  'core/stat-counter-row',
  'core/newsletter-signup',
  'core/comparison-table',
  'core/bullet-list-with-icons',
  'core/timeline-vertical',
  'core/quote-with-portrait',
  'core/image-text-split',
  'core/image-text-split-reversed',
];

describe('@gonext/blocks-patterns surface', () => {
  it('exports exactly the twenty first-party patterns, in order', () => {
    expect(CORE_PATTERNS.map((p) => p.id)).toEqual(EXPECTED_IDS);
  });

  it('every pattern carries the required identifying fields', () => {
    for (const p of CORE_PATTERNS) {
      expect(p.id).toMatch(/^core\//);
      expect(p.name.length).toBeGreaterThan(0);
      expect(p.category.length).toBeGreaterThan(0);
      expect(Array.isArray(p.blocks)).toBe(true);
      expect(p.blocks.length).toBeGreaterThan(0);
    }
  });

  it('every pattern targets one of the built-in categories', () => {
    const allowed = new Set<string>(BUILT_IN_PATTERN_CATEGORIES);
    for (const p of CORE_PATTERNS) {
      expect(allowed.has(p.category)).toBe(true);
    }
  });

  it("every pattern's BlockTree validates against the core block registry", () => {
    const blocks = new BlockRegistry();
    registerCoreBlocks(blocks);

    for (const p of CORE_PATTERNS) {
      const result = blocks.validate(p.blocks);
      // Surface a useful diff when something regresses — Vitest renders
      // the entire object on failure so the first bad path is obvious.
      expect({
        id: p.id,
        valid: result.valid,
        errors: result.errors,
      }).toEqual({ id: p.id, valid: true, errors: [] });
    }
  });

  it('registerCorePatterns() populates a fresh registry', () => {
    const registry = new PatternRegistry();
    registerCorePatterns(registry);
    expect(registry.list()).toHaveLength(CORE_PATTERNS.length);
    for (const id of EXPECTED_IDS) {
      expect(registry.has(id)).toBe(true);
    }
  });

  it('registerCorePatterns() honors { replace: true } across calls', () => {
    const registry = new PatternRegistry();
    registerCorePatterns(registry);
    // Second call without `replace` would throw — assert the option works.
    expect(() => registerCorePatterns(registry)).toThrow();
    expect(() =>
      registerCorePatterns(registry, { replace: true }),
    ).not.toThrow();
    expect(registry.list()).toHaveLength(CORE_PATTERNS.length);
  });

  it('every pattern exposes a preview path', () => {
    for (const p of CORE_PATTERNS) {
      expect(typeof p.preview).toBe('string');
      expect((p.preview ?? '').length).toBeGreaterThan(0);
    }
  });

  it('keywords (when present) are non-empty strings', () => {
    for (const p of CORE_PATTERNS) {
      if (p.keywords === undefined) continue;
      for (const k of p.keywords) {
        expect(typeof k).toBe('string');
        expect(k.length).toBeGreaterThan(0);
      }
    }
  });

  it('every pattern id is unique across the catalog', () => {
    const seen = new Set<string>();
    for (const p of CORE_PATTERNS) {
      expect(seen.has(p.id)).toBe(false);
      seen.add(p.id);
    }
  });
});
