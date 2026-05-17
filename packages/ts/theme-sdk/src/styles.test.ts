/**
 * Tests for `cssVar()` and `CSS_VAR_PREFIX`.
 *
 * The shapes pinned here come straight from `docs/03-theme-system.md`
 * §3.2's canonical list of emitted properties (`--gn-color-ink`,
 * `--gn-font-md`, `--gn-layout-content`, …). If the renderer ever
 * changes the prefix or naming, the docs change first, then this test
 * fails, then the helper updates. That ordering keeps the
 * theme-author-facing surface in lockstep with the runtime output.
 */

import { describe, expect, it } from 'vitest';
import { CSS_VAR_PREFIX, cssVar } from './styles.ts';

describe('CSS_VAR_PREFIX', () => {
  it('is the documented "--gn-" prefix from §3.2', () => {
    expect(CSS_VAR_PREFIX).toBe('--gn-');
  });
});

describe('cssVar() — dot-delimited string form', () => {
  it('color.accent → var(--gn-color-accent)', () => {
    expect(cssVar('color.accent')).toBe('var(--gn-color-accent)');
  });

  it('color.accent-fg keeps the inner hyphen', () => {
    expect(cssVar('color.accent-fg')).toBe('var(--gn-color-accent-fg)');
  });

  it('layout.content → var(--gn-layout-content)', () => {
    expect(cssVar('layout.content')).toBe('var(--gn-layout-content)');
  });

  it('font.md → var(--gn-font-md)', () => {
    expect(cssVar('font.md')).toBe('var(--gn-font-md)');
  });

  it('nested tokens (a.b.c) flatten with hyphens', () => {
    expect(cssVar('shadow.preset.lifted')).toBe('var(--gn-shadow-preset-lifted)');
  });

  it('single-segment tokens still work', () => {
    expect(cssVar('accent')).toBe('var(--gn-accent)');
  });
});

describe('cssVar() — array form', () => {
  it('["color", "accent"] → var(--gn-color-accent)', () => {
    expect(cssVar(['color', 'accent'])).toBe('var(--gn-color-accent)');
  });

  it('array and dot-string forms are interchangeable', () => {
    expect(cssVar(['color', 'accent-fg'])).toBe(cssVar('color.accent-fg'));
    expect(cssVar(['font', 'display'])).toBe(cssVar('font.display'));
  });

  it('skips empty segments rather than producing "--gn--"', () => {
    expect(cssVar(['color', '', 'accent'])).toBe('var(--gn-color-accent)');
  });
});

describe('cssVar() — every §3.2 canonical token round-trips', () => {
  // Pin the shape against every property the §3.2 example emits.
  // Mismatch here means either the docs drifted, the prefix drifted,
  // or the helper drifted — and the test name tells you which token.
  const cases: Array<[string, string]> = [
    ['color.ink', 'var(--gn-color-ink)'],
    ['color.paper', 'var(--gn-color-paper)'],
    ['color.muted', 'var(--gn-color-muted)'],
    ['color.accent', 'var(--gn-color-accent)'],
    ['color.accent-fg', 'var(--gn-color-accent-fg)'],
    ['font.sans', 'var(--gn-font-sans)'],
    ['font.serif', 'var(--gn-font-serif)'],
    ['font.sm', 'var(--gn-font-sm)'],
    ['font.md', 'var(--gn-font-md)'],
    ['font.lg', 'var(--gn-font-lg)'],
    ['font.xl', 'var(--gn-font-xl)'],
    ['font.2xl', 'var(--gn-font-2xl)'],
    ['layout.content', 'var(--gn-layout-content)'],
    ['layout.wide', 'var(--gn-layout-wide)'],
  ];
  for (const [input, want] of cases) {
    it(`${input} → ${want}`, () => {
      expect(cssVar(input)).toBe(want);
    });
  }
});
