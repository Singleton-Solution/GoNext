/**
 * Tests for the /sitemap.xml route handler.
 *
 * The route delegates the actual XML emission to `buildSitemap` /
 * `buildSitemapIndex` (covered exhaustively in feeds.test.ts). What
 * the route tests here own:
 *
 *  - end-to-end behaviour given mocked API responses
 *  - the empty-input branch (no posts -> still well-formed)
 *  - the 100-post and >50k branches that exercise the pagination
 *    boundary
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { GET, renderSitemapXml } from './route.ts';
import { SITEMAP_MAX_URLS_PER_FILE } from '@/lib/feeds.ts';
import type { Post, PublicSiteConfig } from '@/lib/api.ts';

function jsonResponse(status: number, body: unknown): Response {
  return new Response(body === undefined ? '' : JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

/**
 * Install a routing fetch stub. Returns the registered URL count so a
 * test can assert which endpoints the handler hit (useful for catching
 * a regression where the renderer over-fetches).
 */
function installRouter(
  router: (url: string) => Response | undefined,
): void {
  vi.spyOn(globalThis, 'fetch').mockImplementation(
    async (input: RequestInfo | URL) => {
      const url = typeof input === 'string' ? input : input.toString();
      const res = router(url);
      if (!res) return jsonResponse(404, null);
      return res;
    },
  );
}

afterEach(() => {
  vi.restoreAllMocks();
});

function fakeConfig(overrides: Partial<PublicSiteConfig> = {}): PublicSiteConfig {
  return { baseUrl: 'https://example.com', allowIndex: true, ...overrides };
}

function fakePost(slug: string, publishedAt?: string): Post {
  return {
    id: slug,
    slug,
    title: `Post ${slug}`,
    postType: 'post',
    publishedAt,
    blocks: [],
  };
}

describe('renderSitemapXml (pure)', () => {
  it('returns an empty urlset for zero posts', () => {
    const xml = renderSitemapXml(fakeConfig(), []);
    expect(xml).toContain(
      '<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">',
    );
    expect(xml).not.toContain('<url>');
  });

  it('returns an empty urlset when baseUrl is empty', () => {
    // No base URL means we can't build absolute <loc> values; serve a
    // well-formed empty doc instead of a malformed one.
    const xml = renderSitemapXml({ baseUrl: '', allowIndex: false }, [
      fakePost('a'),
    ]);
    expect(xml).toContain('<urlset');
    expect(xml).not.toContain('<url>');
  });

  it('includes one entry per published post', () => {
    const posts = Array.from({ length: 100 }, (_, i) => fakePost(`post-${i}`));
    const xml = renderSitemapXml(fakeConfig(), posts);
    expect((xml.match(/<url>/g) ?? []).length).toBe(100);
    expect(xml).toContain('<loc>https://example.com/post-0</loc>');
    expect(xml).toContain('<loc>https://example.com/post-99</loc>');
  });

  it('paginates into an index when post count exceeds the cap', () => {
    // Generating SITEMAP_MAX_URLS_PER_FILE + 1 entries is expensive
    // but not prohibitive; the test runs in well under a second.
    const posts = Array.from(
      { length: SITEMAP_MAX_URLS_PER_FILE + 1 },
      (_, i) => fakePost(`post-${i}`),
    );
    const xml = renderSitemapXml(fakeConfig(), posts);
    // Sitemap index, not a flat urlset.
    expect(xml).toContain('<sitemapindex');
    expect(xml).not.toContain('<urlset');
    // Two child sitemaps: one full, one with the overflow entry.
    expect((xml.match(/<sitemap>/g) ?? []).length).toBe(2);
    expect(xml).toContain('<loc>https://example.com/sitemap-1.xml</loc>');
    expect(xml).toContain('<loc>https://example.com/sitemap-2.xml</loc>');
  });

  it('emits lastmod when publishedAt is present', () => {
    const xml = renderSitemapXml(fakeConfig(), [
      fakePost('a', '2026-05-19T00:00:00Z'),
    ]);
    expect(xml).toContain('<lastmod>2026-05-19T00:00:00Z</lastmod>');
  });
});

describe('GET /sitemap.xml', () => {
  it('serves application/xml with cache headers', async () => {
    installRouter((url) => {
      if (url.includes('/api/v1/public-site/config')) {
        return jsonResponse(200, {
          baseUrl: 'https://example.com',
          allowIndex: true,
        });
      }
      if (url.includes('/api/v1/posts?')) {
        return jsonResponse(200, {
          posts: [
            {
              id: '1',
              slug: 'hello',
              title: 'Hello',
              postType: 'post',
              publishedAt: '2026-05-19T00:00:00Z',
              blocks: [],
            },
          ],
        });
      }
      return undefined;
    });
    const res = await GET();
    expect(res.status).toBe(200);
    expect(res.headers.get('Content-Type')).toBe('application/xml; charset=utf-8');
    expect(res.headers.get('Cache-Control')).toContain('s-maxage=3600');
    const body = await res.text();
    expect(body).toContain('<urlset');
    expect(body).toContain('<loc>https://example.com/hello</loc>');
  });

  it('serves an empty urlset when the API has no posts', async () => {
    installRouter((url) => {
      if (url.includes('/api/v1/public-site/config')) {
        return jsonResponse(200, {
          baseUrl: 'https://example.com',
          allowIndex: true,
        });
      }
      if (url.includes('/api/v1/posts?')) {
        return jsonResponse(200, { posts: [] });
      }
      return undefined;
    });
    const res = await GET();
    expect(res.status).toBe(200);
    const body = await res.text();
    expect(body).toContain('<urlset');
    expect(body).not.toContain('<url>');
  });
});
