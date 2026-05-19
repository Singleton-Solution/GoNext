/**
 * End-to-end-ish tests for the page renderer. fetch is mocked per
 * URL so we get fixture-driven assertions on the full pipeline:
 * slug → post → theme → template resolution → block walk → wrap.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import {
  renderSingular,
  renderArchive,
  renderNotFound,
  isAuthenticatedCookie,
} from './render.ts';

function jsonResponse(status: number, body: unknown): Response {
  return new Response(body === undefined ? '' : JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

/**
 * Route fetch calls to the right fixture based on URL contents. This
 * is the closest analogue we have to MSW without adding a runtime
 * dep — the same idea, just inlined.
 */
function installRouter(
  router: (url: string) => Response | undefined,
): void {
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

describe('isAuthenticatedCookie', () => {
  it('returns false for an empty / missing cookie', () => {
    expect(isAuthenticatedCookie(undefined)).toBe(false);
    expect(isAuthenticatedCookie('')).toBe(false);
  });

  it('detects the gn_session cookie', () => {
    expect(isAuthenticatedCookie('gn_session=abc123')).toBe(true);
  });

  it('detects the dev gonext_session cookie', () => {
    expect(isAuthenticatedCookie('foo=1; gonext_session=xyz')).toBe(true);
  });

  it('ignores unrelated cookies', () => {
    expect(isAuthenticatedCookie('foo=1; bar=2')).toBe(false);
  });
});

describe('renderSingular', () => {
  it('renders a post slug → block walk → theme parts', async () => {
    installRouter((url) => {
      if (url.includes('/api/v1/posts/by-slug/hello')) {
        return jsonResponse(200, {
          id: '1',
          slug: 'hello',
          title: 'Hello, world',
          postType: 'post',
          publishedAt: '2026-05-19T00:00:00Z',
          blocks: [
            { type: 'core/paragraph', attributes: { content: 'hi there' } },
          ],
        });
      }
      if (url.includes('/api/v1/themes/active/template')) {
        return jsonResponse(200, {
          basename: 'single.html',
          mainHtml: '<article><h1><!--gn:post-title--></h1><!--gn:post-content--></article>',
        });
      }
      if (url.includes('/api/v1/themes/active')) {
        return jsonResponse(200, {
          slug: 'gn-hello',
          title: 'gn-hello',
          cssCustomProperties: ':root{--c:1}',
          headerHtml: '<header class="t">H</header>',
          footerHtml: '<footer class="t">F</footer>',
        });
      }
      return undefined;
    });

    const result = await renderSingular('hello');
    expect(result.status).toBe(200);
    expect(result.title).toBe('Hello, world');
    expect(result.templateBasename).toBe('single.html');
    // Theme wraps the main region.
    expect(result.html.indexOf('<header class="t">H</header>')).toBe(0);
    expect(result.html.endsWith('<footer class="t">F</footer>')).toBe(true);
    // Block walker output is spliced in.
    expect(result.html).toContain('hi there');
    expect(result.html).toContain('gn-block-paragraph');
    // Template title slot received the escaped title.
    expect(result.html).toContain('Hello, world');
    // CSS forwarded from the theme.
    expect(result.css).toBe(':root{--c:1}');
  });

  it('returns 404 with the correct cache header when the slug is missing', async () => {
    installRouter((url) => {
      if (url.includes('/api/v1/posts/by-slug/missing')) {
        return jsonResponse(404, null);
      }
      if (url.includes('/api/v1/themes/active/template')) {
        return jsonResponse(200, {
          basename: '404.html',
          mainHtml: '<main class="gn-404"><h1>404 — page not found</h1></main>',
        });
      }
      if (url.includes('/api/v1/themes/active')) {
        return jsonResponse(200, {
          slug: 'gn-hello',
          title: 'gn-hello',
          cssCustomProperties: '',
          headerHtml: '<header>H</header>',
          footerHtml: '<footer>F</footer>',
        });
      }
      return undefined;
    });

    const result = await renderSingular('missing');
    expect(result.status).toBe(404);
    expect(result.templateBasename).toBe('404.html');
    expect(result.html).toContain('404');
    // 404 path uses the short edge cache.
    expect(result.headers['Cache-Control']).toBe(
      'public, s-maxage=60, stale-while-revalidate=300',
    );
  });

  it('sets the no-store cache header for authenticated visitors', async () => {
    installRouter((url) => {
      if (url.includes('/api/v1/posts/by-slug/hello')) {
        return jsonResponse(200, {
          id: '1',
          slug: 'hello',
          title: 'Hello',
          postType: 'post',
          blocks: [],
        });
      }
      if (url.includes('/api/v1/themes/active/template')) {
        return jsonResponse(200, {
          basename: 'single.html',
          mainHtml: '<main><!--gn:post-content--></main>',
        });
      }
      if (url.includes('/api/v1/themes/active')) {
        return jsonResponse(200, {
          slug: 'gn-hello',
          title: 'gn-hello',
          cssCustomProperties: '',
          headerHtml: '',
          footerHtml: '',
        });
      }
      return undefined;
    });

    const result = await renderSingular('hello', { cookie: 'gn_session=abc' });
    expect(result.headers['Cache-Control']).toBe('private, no-store');
  });

  it('falls back to inline main HTML when the template endpoint is offline', async () => {
    installRouter((url) => {
      if (url.includes('/api/v1/posts/by-slug/hello')) {
        return jsonResponse(200, {
          id: '1',
          slug: 'hello',
          title: 'Hello',
          postType: 'post',
          blocks: [
            { type: 'core/paragraph', attributes: { content: 'body' } },
          ],
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

    const result = await renderSingular('hello');
    expect(result.status).toBe(200);
    expect(result.templateBasename).toBe('single.fallback');
    expect(result.html).toContain('Hello');
    expect(result.html).toContain('body');
  });
});

describe('renderArchive', () => {
  it('renders the archive feed with post links', async () => {
    installRouter((url) => {
      if (url.includes('/api/v1/posts?')) {
        return jsonResponse(200, {
          posts: [
            { id: '1', slug: 'first', title: 'First', postType: 'post', blocks: [] },
            { id: '2', slug: 'second', title: 'Second', postType: 'post', blocks: [] },
          ],
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

    const result = await renderArchive({ type: 'home' });
    expect(result.status).toBe(200);
    expect(result.html).toContain('First');
    expect(result.html).toContain('Second');
    expect(result.html).toContain('/first');
    expect(result.html).toContain('/second');
  });

  it('emits the long edge cache header for logged-out home views', async () => {
    installRouter(() => undefined);
    const result = await renderArchive({ type: 'home' });
    expect(result.headers['Cache-Control']).toBe(
      'public, s-maxage=300, stale-while-revalidate=86400',
    );
  });

  it('shows a friendly empty state when the feed comes back empty', async () => {
    installRouter((url) => {
      if (url.includes('/api/v1/posts?')) {
        return jsonResponse(200, { posts: [] });
      }
      return undefined;
    });
    const result = await renderArchive({ type: 'home' });
    expect(result.html).toContain('No posts yet.');
  });
});

describe('renderNotFound', () => {
  it('returns the 404 status and template basename', async () => {
    installRouter(() => undefined);
    const result = await renderNotFound();
    expect(result.status).toBe(404);
    expect(result.templateBasename).toBe('404.fallback');
    expect(result.html).toContain('404');
    expect(result.html).toContain('Return home');
  });
});
