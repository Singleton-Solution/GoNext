/**
 * REST-shim tests.
 *
 * We mock `globalThis.fetch` with `vi.fn` and pin the request URL,
 * method, headers, and the body shape we send. The response side
 * is constructed via the real `Response` constructor so the SDK's
 * parser exercises the same path it would in the browser.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  HostFetchError,
  host,
  __test_buildQuery,
  __test_hostFetch,
} from './host';
import { __resetSlugCache, setSlug, SlugRequiredError } from './slug';

let fetchMock: ReturnType<typeof vi.fn>;

beforeEach(() => {
  __resetSlugCache();
  setSlug('test-plugin');
  fetchMock = vi.fn();
  globalThis.fetch = fetchMock as unknown as typeof fetch;
});

afterEach(() => {
  __resetSlugCache();
});

/**
 * Helper: builds an okay JSON response.
 */
function jsonResponse(body: unknown, init: ResponseInit = {}): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'content-type': 'application/json' },
    ...init,
  });
}

describe('host.posts', () => {
  it('lists posts via GET /wp-json/wp/v2/posts', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse([{ id: 1 }, { id: 2 }]));
    const result = await host.posts.list();
    expect(result).toHaveLength(2);
    const call = fetchMock.mock.calls[0]!;
    expect(call[0]).toBe('/wp-json/wp/v2/posts');
    expect(call[1].method).toBe('GET');
  });

  it('forwards ListOptions as snake_case query params', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse([]));
    await host.posts.list({ perPage: 5, search: 'hello' });
    const url = fetchMock.mock.calls[0]![0] as string;
    expect(url).toContain('per_page=5');
    expect(url).toContain('search=hello');
  });

  it('reads a single post via GET /wp-json/wp/v2/posts/{id}', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ id: 42, slug: 'p' }));
    const post = await host.posts.get(42);
    expect(post.id).toBe(42);
    expect(fetchMock.mock.calls[0]![0]).toBe('/wp-json/wp/v2/posts/42');
  });

  it('throws HostFetchError on a 5xx', async () => {
    fetchMock.mockResolvedValueOnce(
      new Response(JSON.stringify({ code: 'rest_error' }), {
        status: 500,
        headers: { 'content-type': 'application/json' },
      }),
    );
    await expect(host.posts.get(1)).rejects.toMatchObject({
      name: 'HostFetchError',
      status: 500,
    });
  });

  it('throws HostFetchError with status 0 on transport error', async () => {
    fetchMock.mockRejectedValueOnce(new TypeError('network failed'));
    await expect(host.posts.list()).rejects.toMatchObject({
      name: 'HostFetchError',
      status: 0,
    });
  });

  it('forwards the abort signal', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse([]));
    const ctrl = new AbortController();
    await host.posts.list(undefined, { signal: ctrl.signal });
    const init = fetchMock.mock.calls[0]![1] as RequestInit;
    expect(init.signal).toBe(ctrl.signal);
  });
});

describe('host.users', () => {
  it('resolves the current user via /users/me', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ id: 7, name: 'Ada' }));
    const me = await host.users.me();
    expect(me.name).toBe('Ada');
    expect(fetchMock.mock.calls[0]![0]).toBe('/wp-json/wp/v2/users/me');
  });

  it('lists users', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse([{ id: 1, name: 'A' }]));
    const users = await host.users.list({ perPage: 10 });
    expect(users).toHaveLength(1);
  });

  it('gets a single user by id', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ id: 9, name: 'X' }));
    const u = await host.users.get(9);
    expect(u.id).toBe(9);
    expect(fetchMock.mock.calls[0]![0]).toBe('/wp-json/wp/v2/users/9');
  });
});

describe('host.media', () => {
  it('lists media', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse([{ id: 1, mime_type: 'image/png' }]));
    const media = await host.media.list();
    expect(media[0]!.mime_type).toBe('image/png');
  });

  it('gets a single media item by id', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ id: 5 }));
    const m = await host.media.get(5);
    expect(m.id).toBe(5);
  });
});

describe('host.cache.invalidate', () => {
  it('POSTs to /api/plugins/{slug}/cache/invalidate with the tags', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ invalidated: 3 }));
    const count = await host.cache.invalidate(['posts', 'menus']);
    expect(count).toBe(3);
    const call = fetchMock.mock.calls[0]!;
    expect(call[0]).toBe('/api/plugins/test-plugin/cache/invalidate');
    expect(call[1].method).toBe('POST');
    const body = JSON.parse(call[1].body as string) as { tags: string[] };
    expect(body.tags).toEqual(['posts', 'menus']);
  });

  it('accepts a single string tag', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ invalidated: 1 }));
    await host.cache.invalidate('posts');
    const body = JSON.parse(fetchMock.mock.calls[0]![1].body as string) as {
      tags: string[];
    };
    expect(body.tags).toEqual(['posts']);
  });

  it('defaults to 0 when the server omits a count', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({}));
    const count = await host.cache.invalidate('posts');
    expect(count).toBe(0);
  });

  it('rejects empty / non-string tags before fetch', async () => {
    await expect(host.cache.invalidate('')).rejects.toThrow(TypeError);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('throws SlugRequiredError when slug missing', async () => {
    setSlug(null);
    await expect(host.cache.invalidate('posts')).rejects.toThrow(SlugRequiredError);
  });

  it('throws HostFetchError with the parsed body on 403', async () => {
    fetchMock.mockResolvedValueOnce(
      new Response(JSON.stringify({ code: 'forbidden' }), {
        status: 403,
        headers: { 'content-type': 'application/json' },
      }),
    );
    try {
      await host.cache.invalidate('posts');
      throw new Error('expected throw');
    } catch (err) {
      expect(err).toBeInstanceOf(HostFetchError);
      expect((err as HostFetchError).status).toBe(403);
      expect((err as HostFetchError).responseBody).toEqual({ code: 'forbidden' });
    }
  });
});

describe('hostFetch transport', () => {
  it('sets Accept: application/json by default', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ ok: true }));
    await __test_hostFetch('/foo', { method: 'GET' }, {});
    const init = fetchMock.mock.calls[0]![1] as RequestInit;
    const headers = init.headers as Headers;
    expect(headers.get('Accept')).toBe('application/json');
  });

  it('sets Content-Type: application/json for bodied requests', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ ok: true }));
    await __test_hostFetch('/foo', { method: 'POST', body: '{}' }, {});
    const init = fetchMock.mock.calls[0]![1] as RequestInit;
    const headers = init.headers as Headers;
    expect(headers.get('Content-Type')).toBe('application/json');
  });

  it('sends credentials: include', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ ok: true }));
    await __test_hostFetch('/foo', { method: 'GET' }, {});
    const init = fetchMock.mock.calls[0]![1] as RequestInit;
    expect(init.credentials).toBe('include');
  });

  it('parses a 204 as null', async () => {
    fetchMock.mockResolvedValueOnce(new Response(null, { status: 204 }));
    const out = await __test_hostFetch('/foo', { method: 'DELETE' }, {});
    expect(out).toBeNull();
  });

  it('passes non-JSON content type through as text', async () => {
    fetchMock.mockResolvedValueOnce(
      new Response('hello', {
        status: 200,
        headers: { 'content-type': 'text/plain' },
      }),
    );
    const out = await __test_hostFetch('/foo', { method: 'GET' }, {});
    expect(out).toBe('hello');
  });
});

describe('buildQuery', () => {
  it('returns empty string for undefined', () => {
    expect(__test_buildQuery(undefined)).toBe('');
  });

  it('camelCase keys become snake_case', () => {
    expect(__test_buildQuery({ perPage: 5 })).toBe('?per_page=5');
  });

  it('skips null/undefined values', () => {
    expect(__test_buildQuery({ a: null, b: undefined, c: 1 })).toBe('?c=1');
  });

  it('joins arrays with commas', () => {
    expect(__test_buildQuery({ tags: ['a', 'b'] })).toBe('?tags=a%2Cb');
  });
});
