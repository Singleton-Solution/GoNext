/**
 * Tests for the per-category Atom feed at /feed/[category].
 *
 * The route is a thin wrapper around the same `renderFeedXml` used
 * by /feed.xml — these tests own the slug-forwarding contract: the
 * handler must pass the category slug through to the upstream
 * archive endpoint, and the resulting feed self-link must point at
 * /feed/<slug>.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { GET } from './route.ts';

function jsonResponse(status: number, body: unknown): Response {
  return new Response(body === undefined ? '' : JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

afterEach(() => {
  vi.restoreAllMocks();
});

/**
 * Capture the URL of every fetch made by the handler so a test can
 * assert the category slug was forwarded. The router also returns a
 * fixture response for the captured request.
 */
function installCapturingRouter(
  router: (url: string) => Response | undefined,
): { urls: string[] } {
  const urls: string[] = [];
  vi.spyOn(globalThis, 'fetch').mockImplementation(
    async (input: RequestInfo | URL) => {
      const url = typeof input === 'string' ? input : input.toString();
      urls.push(url);
      const res = router(url);
      if (!res) return jsonResponse(404, null);
      return res;
    },
  );
  return { urls };
}

describe('GET /feed/[category]', () => {
  it('forwards the category slug to the archive endpoint', async () => {
    const captured = installCapturingRouter((url) => {
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
    const res = await GET(new Request('https://example.com/feed/news'), {
      params: Promise.resolve({ category: 'news' }),
    });
    expect(res.status).toBe(200);
    const archiveUrl = captured.urls.find((u) => u.includes('/api/v1/posts?'));
    expect(archiveUrl).toBeDefined();
    expect(archiveUrl).toContain('category=news');
  });

  it('emits a feed whose self-link points at /feed/<slug>', async () => {
    installCapturingRouter((url) => {
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
    const res = await GET(new Request('https://example.com/feed/news'), {
      params: Promise.resolve({ category: 'news' }),
    });
    const body = await res.text();
    expect(body).toContain(
      'href="https://example.com/feed/news"',
    );
  });

  it('serves application/atom+xml', async () => {
    installCapturingRouter((url) => {
      if (url.includes('/api/v1/public-site/config')) {
        return jsonResponse(200, {
          baseUrl: 'https://example.com',
          allowIndex: true,
        });
      }
      if (url.includes('/api/v1/posts?')) return jsonResponse(200, { posts: [] });
      return undefined;
    });
    const res = await GET(new Request('https://example.com/feed/news'), {
      params: Promise.resolve({ category: 'news' }),
    });
    expect(res.headers.get('Content-Type')).toBe(
      'application/atom+xml; charset=utf-8',
    );
  });

  it('returns 404 for an empty slug', async () => {
    // Defensive — the Next router won't normally send an empty
    // param, but a direct test or future internal call might.
    const res = await GET(new Request('https://example.com/feed/'), {
      params: Promise.resolve({ category: '' }),
    });
    expect(res.status).toBe(404);
  });

  it('filters posts to the requested category via the API', async () => {
    // The renderer does NOT filter posts itself — it relies on the
    // API. This test pins the contract: a category-filtered response
    // is rendered verbatim.
    installCapturingRouter((url) => {
      if (url.includes('/api/v1/public-site/config')) {
        return jsonResponse(200, {
          baseUrl: 'https://example.com',
          allowIndex: true,
        });
      }
      if (url.includes('category=news')) {
        return jsonResponse(200, {
          posts: [
            {
              id: '1',
              slug: 'news-post',
              title: 'News Post',
              postType: 'post',
              publishedAt: '2026-05-19T00:00:00Z',
              blocks: [],
            },
          ],
        });
      }
      // Different category returns different posts — must not leak.
      if (url.includes('category=tech')) {
        return jsonResponse(200, {
          posts: [
            {
              id: '2',
              slug: 'tech-post',
              title: 'Tech Post',
              postType: 'post',
              publishedAt: '2026-05-18T00:00:00Z',
              blocks: [],
            },
          ],
        });
      }
      return undefined;
    });

    const newsRes = await GET(new Request('https://example.com/feed/news'), {
      params: Promise.resolve({ category: 'news' }),
    });
    const newsBody = await newsRes.text();
    expect(newsBody).toContain('News Post');
    expect(newsBody).not.toContain('Tech Post');

    vi.restoreAllMocks();
    installCapturingRouter((url) => {
      if (url.includes('/api/v1/public-site/config')) {
        return jsonResponse(200, {
          baseUrl: 'https://example.com',
          allowIndex: true,
        });
      }
      if (url.includes('category=tech')) {
        return jsonResponse(200, {
          posts: [
            {
              id: '2',
              slug: 'tech-post',
              title: 'Tech Post',
              postType: 'post',
              publishedAt: '2026-05-18T00:00:00Z',
              blocks: [],
            },
          ],
        });
      }
      return undefined;
    });

    const techRes = await GET(new Request('https://example.com/feed/tech'), {
      params: Promise.resolve({ category: 'tech' }),
    });
    const techBody = await techRes.text();
    expect(techBody).toContain('Tech Post');
    expect(techBody).not.toContain('News Post');
  });
});
