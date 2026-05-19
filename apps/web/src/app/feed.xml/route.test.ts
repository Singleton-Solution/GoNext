/**
 * Tests for /feed.xml — the global Atom feed.
 *
 * We split tests into "pure" (against `renderFeedXml` / sort helper)
 * and "wire" (against the GET handler with mocked fetch). The pure
 * tests pin the deterministic-output contract; the wire tests confirm
 * the route plumbs the API through correctly.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import {
  GET,
  renderFeedXml,
  sortByPublishedDesc,
  postToFeedEntry,
  FEED_ITEM_LIMIT,
} from './route.ts';
import type { Post, PublicSiteConfig } from '@/lib/api.ts';

function jsonResponse(status: number, body: unknown): Response {
  return new Response(body === undefined ? '' : JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

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

function fakePost(slug: string, publishedAt?: string, title?: string): Post {
  return {
    id: slug,
    slug,
    title: title ?? `Post ${slug}`,
    postType: 'post',
    publishedAt,
    blocks: [],
  };
}

describe('sortByPublishedDesc', () => {
  it('puts the newest entry first', () => {
    const posts = [
      fakePost('a', '2026-05-01T00:00:00Z'),
      fakePost('b', '2026-05-19T00:00:00Z'),
      fakePost('c', '2026-05-10T00:00:00Z'),
    ];
    const sorted = sortByPublishedDesc(posts);
    expect(sorted.map((p) => p.slug)).toEqual(['b', 'c', 'a']);
  });

  it('puts unpublished posts last', () => {
    const posts = [
      fakePost('a', '2026-05-19T00:00:00Z'),
      fakePost('b'), // no publishedAt
      fakePost('c', '2026-05-10T00:00:00Z'),
    ];
    const sorted = sortByPublishedDesc(posts);
    expect(sorted[0]?.slug).toBe('a');
    expect(sorted[2]?.slug).toBe('b');
  });

  it('does not mutate the input array', () => {
    const posts = [fakePost('a', '2026-01-01T00:00:00Z')];
    const before = [...posts];
    sortByPublishedDesc(posts);
    expect(posts).toEqual(before);
  });
});

describe('postToFeedEntry', () => {
  it('uses the permalink as the entry id', () => {
    const entry = postToFeedEntry(
      'https://example.com',
      fakePost('hello', '2026-05-19T00:00:00Z'),
    );
    expect(entry.id).toBe('https://example.com/hello');
    expect(entry.link).toBe('https://example.com/hello');
  });

  it('falls back to the unix epoch when published is missing', () => {
    const entry = postToFeedEntry('https://example.com', fakePost('hello'));
    expect(entry.updated).toBe(new Date(0).toISOString());
  });
});

describe('renderFeedXml', () => {
  it('caps the feed at 20 items by default', () => {
    const posts = Array.from({ length: 50 }, (_, i) =>
      fakePost(`p-${i}`, `2026-05-${String((i % 28) + 1).padStart(2, '0')}T00:00:00Z`),
    );
    const xml = renderFeedXml({
      cfg: fakeConfig(),
      posts,
      feedPath: '/feed.xml',
      title: 'Test',
    });
    expect((xml.match(/<entry>/g) ?? []).length).toBe(FEED_ITEM_LIMIT);
  });

  it('emits entries newest-first', () => {
    const posts = [
      fakePost('old', '2026-01-01T00:00:00Z'),
      fakePost('new', '2026-05-19T00:00:00Z'),
      fakePost('mid', '2026-03-01T00:00:00Z'),
    ];
    const xml = renderFeedXml({
      cfg: fakeConfig(),
      posts,
      feedPath: '/feed.xml',
      title: 'Test',
    });
    // The 'new' post should appear before 'mid' and 'old' in document order.
    const idxNew = xml.indexOf('https://example.com/new');
    const idxMid = xml.indexOf('https://example.com/mid');
    const idxOld = xml.indexOf('https://example.com/old');
    expect(idxNew).toBeGreaterThan(-1);
    expect(idxMid).toBeGreaterThan(idxNew);
    expect(idxOld).toBeGreaterThan(idxMid);
  });

  it('escapes special chars in titles', () => {
    const xml = renderFeedXml({
      cfg: fakeConfig(),
      posts: [
        fakePost('x', '2026-05-19T00:00:00Z', 'Tom & Jerry <Adventure>'),
      ],
      feedPath: '/feed.xml',
      title: 'Test',
    });
    expect(xml).toContain('Tom &amp; Jerry &lt;Adventure&gt;');
    expect(xml).not.toContain('Tom & Jerry <Adventure>');
  });

  it('produces deterministic output', () => {
    const posts = [
      fakePost('a', '2026-05-19T00:00:00Z'),
      fakePost('b', '2026-05-18T00:00:00Z'),
    ];
    const a = renderFeedXml({
      cfg: fakeConfig(),
      posts,
      feedPath: '/feed.xml',
      title: 'Test',
    });
    const b = renderFeedXml({
      cfg: fakeConfig(),
      posts,
      feedPath: '/feed.xml',
      title: 'Test',
    });
    expect(a).toBe(b);
  });

  it('falls back to the unix epoch updated for an empty feed', () => {
    const xml = renderFeedXml({
      cfg: fakeConfig(),
      posts: [],
      feedPath: '/feed.xml',
      title: 'Empty',
    });
    expect(xml).toContain('<updated>1970-01-01T00:00:00.000Z</updated>');
  });
});

describe('GET /feed.xml', () => {
  it('serves application/atom+xml with cache headers', async () => {
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
    expect(res.headers.get('Content-Type')).toBe(
      'application/atom+xml; charset=utf-8',
    );
    expect(res.headers.get('Cache-Control')).toContain('s-maxage=3600');
    const body = await res.text();
    expect(body).toContain('<feed xmlns="http://www.w3.org/2005/Atom">');
    expect(body).toContain('<title>Hello</title>');
  });
});
