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
  ApiError,
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
