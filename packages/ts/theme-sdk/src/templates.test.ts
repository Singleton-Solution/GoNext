/**
 * Tests for `templatePath()` and `buildCandidates()`.
 *
 * The cases below mirror — line for line, name for name — the Go
 * fixtures in `packages/go/theme/templates/resolver_test.go`. The
 * "drift detector" principle: if a new candidate or precedence rule
 * lands on the Go side, the matching TS case will fail until it's
 * updated, and vice versa. The two test suites pin the same contract
 * from both sides.
 *
 * `templatePath()` here exposes the *full candidate list* rather than
 * resolving against a file set (the Go resolver consumes a `ThemeFiles`
 * abstraction we don't have on the TS side). The TS side's value is
 * the answer to "given this query, what's the precedence order?" —
 * the host walks it at request time, and the editor surfaces it for
 * tooling. Resolution against actual files is the Go's job.
 */

import { describe, expect, it } from 'vitest';
import {
  TEMPLATE_EXTENSIONS,
  buildCandidates,
  templatePath,
  type ContextHints,
  type RequestType,
} from './templates.ts';

describe('TEMPLATE_EXTENSIONS', () => {
  it('matches the Go resolver: .tsx first, .html second', () => {
    expect(TEMPLATE_EXTENSIONS).toEqual(['.tsx', '.html']);
  });
});

describe('buildCandidates() — singular hierarchy', () => {
  const hints: ContextHints = {
    postType: 'book',
    postSlug: 'intro-to-cooking',
  };

  it('builds the full §4.2 chain when every input is set', () => {
    expect(buildCandidates('singular', hints)).toEqual([
      'single-book-intro-to-cooking',
      'single-book',
      'single',
      'singular',
      'index',
    ]);
  });

  it('uses postID when only id is known', () => {
    expect(
      buildCandidates('singular', { postType: 'book', postID: '42' }),
    ).toEqual(['single-book-42', 'single-book', 'single', 'singular', 'index']);
  });

  it('slug wins over id when both are set', () => {
    expect(
      buildCandidates('singular', {
        postType: 'book',
        postSlug: 'intro-to-cooking',
        postID: '42',
      }),
    ).toEqual([
      'single-book-intro-to-cooking',
      'single-book-42',
      'single-book',
      'single',
      'singular',
      'index',
    ]);
  });

  it('drops the postType-specific entries when postType is empty', () => {
    expect(buildCandidates('singular', {})).toEqual([
      'single',
      'singular',
      'index',
    ]);
  });

  it('emits single before singular (matches Go resolver order)', () => {
    const c = buildCandidates('singular', { postType: 'book' });
    const single = c.indexOf('single');
    const singular = c.indexOf('singular');
    expect(single).toBeGreaterThanOrEqual(0);
    expect(singular).toBeGreaterThanOrEqual(0);
    expect(single).toBeLessThan(singular);
  });
});

describe('buildCandidates() — archive hierarchy', () => {
  it('builds archive-{postType} → archive → index', () => {
    expect(buildCandidates('archive', { postType: 'book' })).toEqual([
      'archive-book',
      'archive',
      'index',
    ]);
  });

  it('drops the postType-specific entry when postType is empty', () => {
    expect(buildCandidates('archive', {})).toEqual(['archive', 'index']);
  });
});

describe('buildCandidates() — taxonomy hierarchy', () => {
  const hints: ContextHints = { taxonomySlug: 'genre', termSlug: 'cookbooks' };

  it('builds the full §4.2 chain when tax + term are set', () => {
    expect(buildCandidates('taxonomy', hints)).toEqual([
      'taxonomy-genre-cookbooks',
      'taxonomy-genre',
      'taxonomy',
      'archive',
      'index',
    ]);
  });

  it('drops the term entry when only taxonomy is set', () => {
    expect(buildCandidates('taxonomy', { taxonomySlug: 'genre' })).toEqual([
      'taxonomy-genre',
      'taxonomy',
      'archive',
      'index',
    ]);
  });

  it('drops both tax-specific entries when neither is set', () => {
    expect(buildCandidates('taxonomy', {})).toEqual([
      'taxonomy',
      'archive',
      'index',
    ]);
  });
});

describe('buildCandidates() — author hierarchy', () => {
  it('builds id before handle when both are set (matches Go order)', () => {
    expect(
      buildCandidates('author', { authorID: '42', postSlug: 'alice' }),
    ).toEqual(['author-42', 'author-alice', 'author', 'archive', 'index']);
  });

  it('drops the id entry when authorID is empty', () => {
    expect(buildCandidates('author', { postSlug: 'alice' })).toEqual([
      'author-alice',
      'author',
      'archive',
      'index',
    ]);
  });

  it('falls through to author → archive → index when both are empty', () => {
    expect(buildCandidates('author', {})).toEqual([
      'author',
      'archive',
      'index',
    ]);
  });
});

describe('buildCandidates() — flat hierarchies', () => {
  // The "leaf" request types (no per-postType variants) — checked in
  // one block because the contract per type is "one expected list".
  const cases: Array<{ type: RequestType; want: string[] }> = [
    { type: 'date', want: ['date', 'archive', 'index'] },
    { type: 'search', want: ['search', 'index'] },
    { type: 'home', want: ['home', 'index'] },
    { type: 'front-page', want: ['front-page', 'home', 'index'] },
    { type: '404', want: ['404', 'index'] },
  ];

  for (const c of cases) {
    it(`${c.type}: emits ${c.want.join(' → ')}`, () => {
      expect(buildCandidates(c.type)).toEqual(c.want);
    });
  }
});

describe('buildCandidates() — unknown types', () => {
  it("returns an empty array for type='unknown'", () => {
    expect(buildCandidates('unknown')).toEqual([]);
  });
});

describe('templatePath()', () => {
  it('returns the most-specific candidate as name', () => {
    const r = templatePath('singular', {
      postType: 'book',
      postSlug: 'intro-to-cooking',
    });
    expect(r.error).toBe('');
    expect(r.name).toBe('single-book-intro-to-cooking');
    expect(r.candidates[0]).toBe(r.name);
  });

  it('always terminates at index for every recognised type', () => {
    const types: RequestType[] = [
      'singular',
      'archive',
      'taxonomy',
      'author',
      'date',
      'search',
      'home',
      'front-page',
      '404',
    ];
    for (const t of types) {
      const r = templatePath(t);
      expect(r.candidates[r.candidates.length - 1]).toBe('index');
    }
  });

  it("returns an error for type='unknown'", () => {
    const r = templatePath('unknown');
    expect(r.name).toBe('');
    expect(r.candidates).toEqual([]);
    expect(r.error).toContain('unknown request type');
    expect(r.error).toContain('"unknown"');
  });

  it('precedence-property: removing the head shifts to the next candidate', () => {
    // The Go side's `TestDefaultResolver_PrecedenceProperty` walks
    // down the singular hierarchy by deleting the most-specific file
    // each step. We can't delete TS strings, but we can pin the same
    // invariant by listing the expected step-by-step head names.
    const fullChain = [
      'single-book-intro-to-cooking',
      'single-book',
      'single',
      'singular',
      'index',
    ];
    const all = buildCandidates('singular', {
      postType: 'book',
      postSlug: 'intro-to-cooking',
    });
    expect(all).toEqual(fullChain);
  });
});
