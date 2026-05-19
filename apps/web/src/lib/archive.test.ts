/**
 * Tests for the shared archive helpers — input validation, pagination
 * parsing, heading formatting, and the assembled render bundle.
 *
 * The bundle test uses the same URL-routed fetch stub pattern as
 * render.test.ts so the API-shape boundary stays consistent.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import {
  parsePageParam,
  validateYear,
  validateMonth,
  validateDay,
  formatDateHeading,
  renderArchiveBundle,
  DEFAULT_ARCHIVE_PER_PAGE,
} from './archive.ts';

function jsonResponse(status: number, body: unknown): Response {
  return new Response(body === undefined ? '' : JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function installRouter(router: (url: string) => Response | undefined): void {
  vi.spyOn(globalThis, 'fetch').mockImplementation(
    async (input: RequestInfo | URL) => {
      const url = typeof input === 'string' ? input : input.toString();
      const res = router(url);
      if (!res) {
        return jsonResponse(404, null);
      }
      return res;
    },
  );
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe('parsePageParam', () => {
  it('defaults to 1 for missing input', () => {
    expect(parsePageParam(undefined)).toBe(1);
    expect(parsePageParam('')).toBe(1);
  });

  it('returns the parsed value for valid pages', () => {
    expect(parsePageParam('2')).toBe(2);
    expect(parsePageParam('17')).toBe(17);
  });

  it('takes the first value when the param is an array', () => {
    expect(parsePageParam(['3', '4'])).toBe(3);
  });

  it('clamps zero / negative / non-numeric to 1', () => {
    expect(parsePageParam('0')).toBe(1);
    expect(parsePageParam('-1')).toBe(1);
    expect(parsePageParam('abc')).toBe(1);
  });

  it('caps absurdly large values', () => {
    expect(parsePageParam('99999999')).toBe(10000);
  });
});

describe('validateYear', () => {
  it('returns the parsed year for valid input', () => {
    expect(validateYear('2026')).toBe(2026);
    expect(validateYear('1999')).toBe(1999);
  });

  it('returns null for short / long / non-numeric input', () => {
    expect(validateYear('99')).toBeNull();
    expect(validateYear('20260')).toBeNull();
    expect(validateYear('abcd')).toBeNull();
    expect(validateYear(undefined)).toBeNull();
  });

  it('rejects out-of-range four-digit numbers', () => {
    expect(validateYear('0001')).toBeNull();
    expect(validateYear('0999')).toBeNull();
  });
});

describe('validateMonth', () => {
  it('accepts both padded and unpadded forms', () => {
    expect(validateMonth('1')).toBe(1);
    expect(validateMonth('01')).toBe(1);
    expect(validateMonth('12')).toBe(12);
  });

  it('rejects out-of-range and malformed input', () => {
    expect(validateMonth('0')).toBeNull();
    expect(validateMonth('13')).toBeNull();
    expect(validateMonth('99')).toBeNull();
    expect(validateMonth('abc')).toBeNull();
    expect(validateMonth(undefined)).toBeNull();
  });
});

describe('validateDay', () => {
  it('accepts 1-31 in padded or unpadded form', () => {
    expect(validateDay('1')).toBe(1);
    expect(validateDay('09')).toBe(9);
    expect(validateDay('31')).toBe(31);
  });

  it('rejects 0, 32+, and non-numeric input', () => {
    expect(validateDay('0')).toBeNull();
    expect(validateDay('32')).toBeNull();
    expect(validateDay('abc')).toBeNull();
    expect(validateDay(undefined)).toBeNull();
  });
});

describe('formatDateHeading', () => {
  it('formats year-only', () => {
    expect(formatDateHeading(2026)).toBe('Archive: 2026');
  });

  it('formats year + month with the English month name', () => {
    expect(formatDateHeading(2026, 5)).toBe('Archive: May 2026');
  });

  it('formats year + month + day with the English month name', () => {
    expect(formatDateHeading(2026, 5, 19)).toBe('Archive: May 19, 2026');
  });
});

describe('renderArchiveBundle', () => {
  it('renders the archive list with pagination links when there are more pages', async () => {
    installRouter((url) => {
      if (url.includes('/api/v1/posts?')) {
        return jsonResponse(200, {
          posts: [
            { id: '1', slug: 'first', title: 'First', postType: 'post', blocks: [] },
            { id: '2', slug: 'second', title: 'Second', postType: 'post', blocks: [] },
          ],
          total: 25,
          perPage: 10,
          page: 1,
        });
      }
      if (url.includes('/api/v1/themes/active/template')) {
        return jsonResponse(404, null);
      }
      if (url.includes('/api/v1/themes/active')) {
        return jsonResponse(404, null);
      }
      return undefined;
    });

    const result = await renderArchiveBundle({
      type: 'author',
      heading: 'Posts by Jane',
      basePath: '/author/jane',
      page: 1,
      authorSlug: 'jane',
      authorId: '42',
    });

    expect(result.status).toBe(200);
    expect(result.html).toContain('Posts by Jane');
    expect(result.html).toContain('First');
    expect(result.html).toContain('Second');
    expect(result.html).toContain('/first');
    // Next-page link present, previous link absent on the first page.
    expect(result.html).toContain('/author/jane?page=2');
    expect(result.html).not.toContain('rel="prev"');
    expect(result.html).toContain('rel="next"');
    expect(result.templateBasename).toBe('author.fallback');
    // Cache header matches the long edge cache for logged-out visitors.
    expect(result.headers['Cache-Control']).toBe(
      'public, s-maxage=300, stale-while-revalidate=86400',
    );
  });

  it('paints a friendly empty state for an author with no posts', async () => {
    installRouter((url) => {
      if (url.includes('/api/v1/posts?')) {
        return jsonResponse(200, { posts: [], total: 0, perPage: 10, page: 1 });
      }
      return undefined;
    });
    const result = await renderArchiveBundle({
      type: 'author',
      heading: 'Posts by Empty',
      basePath: '/author/empty',
      page: 1,
      authorSlug: 'empty',
    });
    expect(result.status).toBe(200);
    expect(result.html).toContain('No posts yet.');
  });

  it('preserves the basePath in pagination URLs', async () => {
    installRouter((url) => {
      if (url.includes('/api/v1/posts?')) {
        return jsonResponse(200, {
          posts: [
            { id: '1', slug: 'a', title: 'A', postType: 'post', blocks: [] },
          ],
          total: 30,
          perPage: 10,
          page: 2,
        });
      }
      return undefined;
    });
    const result = await renderArchiveBundle({
      type: 'taxonomy',
      heading: 'Category: News',
      basePath: '/category/news',
      page: 2,
      taxonomy: 'category',
      termSlug: 'news',
    });
    // On page 2 we see both prev and next links, and the prev link
    // drops the ?page=1 query (so /category/news is the canonical URL).
    expect(result.html).toContain('href="/category/news"');
    expect(result.html).toContain('/category/news?page=3');
    expect(result.html).toContain('rel="prev"');
    expect(result.html).toContain('rel="next"');
  });

  it('emits private no-store cache headers for authenticated visitors', async () => {
    installRouter(() => undefined);
    const result = await renderArchiveBundle({
      type: 'date',
      heading: 'Archive: 2026',
      basePath: '/2026',
      page: 1,
      year: 2026,
      cookie: 'gn_session=abc',
    });
    expect(result.headers['Cache-Control']).toBe('private, no-store');
  });

  it('uses DEFAULT_ARCHIVE_PER_PAGE when perPage is not supplied', async () => {
    const spy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      jsonResponse(404, null),
    );
    await renderArchiveBundle({
      type: 'author',
      heading: 'h',
      basePath: '/author/x',
      page: 1,
      authorSlug: 'x',
    });
    const url = String(spy.mock.calls[0]?.[0] ?? '');
    expect(url).toContain(`limit=${DEFAULT_ARCHIVE_PER_PAGE}`);
  });
});
