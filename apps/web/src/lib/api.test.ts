/**
 * Tests for the typed API client.
 *
 * We mock `globalThis.fetch` per test (the shared setup already
 * installs a loud stub for any un-mocked call). The mocks return
 * minimal JSON shapes that mirror what the Go API contract documents.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import {
  fetchPostBySlug,
  fetchActiveTheme,
  fetchResolvedTemplate,
  fetchArchive,
  fetchArchivePage,
  fetchAuthorBySlug,
  fetchTermBySlug,
  ApiError,
  fetchPublicSiteConfig,
} from './api.ts';

function jsonResponse(status: number, body: unknown): Response {
  return new Response(body === undefined ? '' : JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe('fetchPostBySlug', () => {
  it('returns the parsed post for a 2xx response', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      jsonResponse(200, {
        id: '42',
        slug: 'hello-world',
        title: 'Hello, World',
        postType: 'post',
        blocks: [
          { type: 'core/paragraph', attributes: { content: 'hi' } },
        ],
      }),
    );
    const post = await fetchPostBySlug('hello-world');
    expect(post).not.toBeNull();
    expect(post?.title).toBe('Hello, World');
    expect(post?.blocks).toHaveLength(1);
  });

  it('returns null on 404', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse(404, null));
    const post = await fetchPostBySlug('missing');
    expect(post).toBeNull();
  });

  it('returns null on network failure (renderer falls through to 404)', async () => {
    vi.spyOn(globalThis, 'fetch').mockRejectedValue(new Error('econnrefused'));
    const post = await fetchPostBySlug('any');
    expect(post).toBeNull();
  });

  it('throws ApiError on a 5xx', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse(500, { message: 'boom' }));
    await expect(fetchPostBySlug('x')).rejects.toBeInstanceOf(ApiError);
  });

  it('rejects empty slug without making a request', async () => {
    const spy = vi.spyOn(globalThis, 'fetch');
    expect(await fetchPostBySlug('')).toBeNull();
    expect(spy).not.toHaveBeenCalled();
  });

  it('drops unrecognised fields and survives a missing block array', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      jsonResponse(200, {
        id: '7',
        slug: 's',
        title: 't',
        // No blocks field, plus a junk field that should be ignored.
        junk: { whatever: true },
      }),
    );
    const post = await fetchPostBySlug('s');
    expect(post).not.toBeNull();
    expect(post?.blocks).toEqual([]);
    // Default postType when omitted.
    expect(post?.postType).toBe('post');
  });
});

describe('fetchActiveTheme', () => {
  it('returns the parsed theme summary', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      jsonResponse(200, {
        slug: 'gn-hello',
        title: 'gn-hello',
        cssCustomProperties: ':root{--x:1}',
        headerHtml: '<header>x</header>',
        footerHtml: '<footer>y</footer>',
      }),
    );
    const theme = await fetchActiveTheme();
    expect(theme?.slug).toBe('gn-hello');
    expect(theme?.headerHtml).toContain('<header>');
  });

  it('returns null on network failure (caller falls back to default)', async () => {
    vi.spyOn(globalThis, 'fetch').mockRejectedValue(new Error('down'));
    expect(await fetchActiveTheme()).toBeNull();
  });
});

describe('fetchResolvedTemplate', () => {
  it('sends the type + post fields as query params', async () => {
    const spy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      jsonResponse(200, { basename: 'single.html', mainHtml: '<main></main>' }),
    );
    const result = await fetchResolvedTemplate({
      type: 'singular',
      postType: 'post',
      postSlug: 'hello',
    });
    expect(result?.basename).toBe('single.html');
    const url = String(spy.mock.calls[0]?.[0]);
    expect(url).toContain('type=singular');
    expect(url).toContain('postType=post');
    expect(url).toContain('postSlug=hello');
  });

  it('returns null when the endpoint 404s', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse(404, null));
    const result = await fetchResolvedTemplate({ type: 'singular' });
    expect(result).toBeNull();
  });
});

describe('fetchArchive', () => {
  it('returns the post list', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      jsonResponse(200, {
        posts: [
          { id: '1', slug: 'a', title: 'A', postType: 'post', blocks: [] },
          { id: '2', slug: 'b', title: 'B', postType: 'post', blocks: [] },
        ],
      }),
    );
    const posts = await fetchArchive({ limit: 10 });
    expect(posts).toHaveLength(2);
    expect(posts[0]?.title).toBe('A');
  });

  it('returns an empty list when the endpoint is unreachable', async () => {
    vi.spyOn(globalThis, 'fetch').mockRejectedValue(new Error('down'));
    expect(await fetchArchive()).toEqual([]);
  });

  it('drops malformed entries without throwing', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      jsonResponse(200, {
        posts: [
          { id: '1', slug: 'a', title: 'A', postType: 'post', blocks: [] },
          { not: 'a-post' },
          null,
        ],
      }),
    );
    const posts = await fetchArchive();
    expect(posts).toHaveLength(1);
  });
});

describe('fetchArchivePage', () => {
  it('returns the parsed envelope with total + page metadata', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      jsonResponse(200, {
        posts: [
          { id: '1', slug: 'a', title: 'A', postType: 'post', blocks: [] },
        ],
        total: 42,
        perPage: 5,
        page: 3,
      }),
    );
    const page = await fetchArchivePage({ limit: 5, page: 3 });
    expect(page.posts).toHaveLength(1);
    expect(page.total).toBe(42);
    expect(page.perPage).toBe(5);
    expect(page.page).toBe(3);
  });

  it('forwards the author / taxonomy / date filters as query params', async () => {
    const spy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      jsonResponse(200, { posts: [] }),
    );
    await fetchArchivePage({
      authorSlug: 'jane',
      taxonomy: 'category',
      termSlug: 'news',
      year: 2026,
      month: 5,
      day: 19,
    });
    const url = String(spy.mock.calls[0]?.[0]);
    expect(url).toContain('authorSlug=jane');
    expect(url).toContain('taxonomy=category');
    expect(url).toContain('termSlug=news');
    expect(url).toContain('year=2026');
    expect(url).toContain('month=5');
    expect(url).toContain('day=19');
  });

  it('returns an empty page on network failure', async () => {
    vi.spyOn(globalThis, 'fetch').mockRejectedValue(new Error('down'));
    const page = await fetchArchivePage({ limit: 10, page: 1 });
    expect(page.posts).toEqual([]);
    expect(page.total).toBe(0);
    expect(page.perPage).toBe(10);
    expect(page.page).toBe(1);
  });

  it('falls back to the bare {posts} shape when totals are missing', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      jsonResponse(200, {
        posts: [
          { id: '1', slug: 'a', title: 'A', postType: 'post', blocks: [] },
          { id: '2', slug: 'b', title: 'B', postType: 'post', blocks: [] },
        ],
      }),
    );
    const page = await fetchArchivePage({ limit: 10, page: 1 });
    expect(page.posts).toHaveLength(2);
    // Total defaults to the post count when the envelope omits it.
    expect(page.total).toBe(2);
  });
});

describe('fetchAuthorBySlug', () => {
  it('returns the parsed author', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      jsonResponse(200, {
        id: 7,
        slug: 'jane',
        name: 'Jane Doe',
        description: 'Writes about things',
      }),
    );
    const author = await fetchAuthorBySlug('jane');
    expect(author).not.toBeNull();
    expect(author?.id).toBe('7');
    expect(author?.slug).toBe('jane');
    expect(author?.name).toBe('Jane Doe');
    expect(author?.description).toBe('Writes about things');
  });

  it('returns null for a 404', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse(404, null));
    expect(await fetchAuthorBySlug('missing')).toBeNull();
  });

  it('returns null on empty slug without hitting the network', async () => {
    const spy = vi.spyOn(globalThis, 'fetch');
    expect(await fetchAuthorBySlug('')).toBeNull();
    expect(spy).not.toHaveBeenCalled();
  });
});

describe('fetchTermBySlug', () => {
  it('returns the parsed term', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      jsonResponse(200, {
        id: '11',
        slug: 'news',
        name: 'News',
        taxonomy: 'category',
      }),
    );
    const term = await fetchTermBySlug('category', 'news');
    expect(term?.taxonomy).toBe('category');
    expect(term?.name).toBe('News');
  });

  it('accepts taxonomySlug as the alias for taxonomy', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      jsonResponse(200, {
        id: '12',
        slug: 'breaking',
        name: 'Breaking',
        taxonomySlug: 'post_tag',
      }),
    );
    const term = await fetchTermBySlug('post_tag', 'breaking');
    expect(term?.taxonomy).toBe('post_tag');
  });

  it('returns null for a 404', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse(404, null));
    expect(await fetchTermBySlug('category', 'missing')).toBeNull();
  });

  it('returns null without a network call when arguments are empty', async () => {
    const spy = vi.spyOn(globalThis, 'fetch');
    expect(await fetchTermBySlug('', 'news')).toBeNull();
    expect(await fetchTermBySlug('category', '')).toBeNull();
    expect(spy).not.toHaveBeenCalled();
  });
});


describe('fetchPublicSiteConfig', () => {
  it('returns the parsed config on 2xx', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      jsonResponse(200, {
        baseUrl: 'https://example.com',
        allowIndex: true,
      }),
    );
    const cfg = await fetchPublicSiteConfig();
    expect(cfg.baseUrl).toBe('https://example.com');
    expect(cfg.allowIndex).toBe(true);
  });

  it('strips a stray trailing slash from baseUrl', async () => {
    // Defense-in-depth: the Go side strips this on load, but if a
    // future provenance change skips that step the renderer still
    // produces well-formed URLs.
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      jsonResponse(200, {
        baseUrl: 'https://example.com/',
        allowIndex: true,
      }),
    );
    const cfg = await fetchPublicSiteConfig();
    expect(cfg.baseUrl).toBe('https://example.com');
  });

  it('returns the safe fallback when the API is unreachable', async () => {
    vi.spyOn(globalThis, 'fetch').mockRejectedValue(new Error('down'));
    const cfg = await fetchPublicSiteConfig();
    expect(cfg.baseUrl).toBe('');
    // CRITICAL: the failure mode must default to "don't index" so
    // an offline API doesn't promote a staging deployment to
    // search-engine eligibility.
    expect(cfg.allowIndex).toBe(false);
  });

  it('returns the safe fallback on 404', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      jsonResponse(404, null),
    );
    const cfg = await fetchPublicSiteConfig();
    expect(cfg).toEqual({ baseUrl: '', allowIndex: false });
  });

  it('coerces invalid response shapes to the safe fallback', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      jsonResponse(200, { baseUrl: 42, allowIndex: 'yes' }),
    );
    const cfg = await fetchPublicSiteConfig();
    expect(cfg).toEqual({ baseUrl: '', allowIndex: false });
  });
});
