/**
 * Author archive route tests.
 *
 * Async server components are exercised by awaiting the page function
 * directly, then rendering the returned element through RTL. `next/
 * headers` is mocked at the module boundary so we don't need a full
 * Next request context.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { render } from '@testing-library/react';
import AuthorArchivePage, { generateMetadata } from './page';

vi.mock('next/headers', () => ({
  cookies: async () => ({
    getAll: () => [] as Array<{ name: string; value: string }>,
  }),
  headers: async () => new Headers(),
}));

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
      if (!res) return jsonResponse(404, null);
      return res;
    },
  );
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe('AuthorArchivePage', () => {
  it('renders the author archive when the slug resolves', async () => {
    installRouter((url) => {
      if (url.includes('/api/v1/users/by-slug/jane')) {
        return jsonResponse(200, {
          id: '7',
          slug: 'jane',
          name: 'Jane Doe',
          description: 'Tech writer',
        });
      }
      if (url.includes('/api/v1/posts?')) {
        return jsonResponse(200, {
          posts: [
            { id: '1', slug: 'a', title: 'First post', postType: 'post', blocks: [] },
          ],
          total: 1,
          perPage: 10,
          page: 1,
        });
      }
      return undefined;
    });
    const element = await AuthorArchivePage({
      params: Promise.resolve({ slug: 'jane' }),
      searchParams: Promise.resolve({}),
    });
    const { container } = render(element);
    const html = container.innerHTML;
    expect(html).toContain('Posts by Jane Doe');
    expect(html).toContain('First post');
    expect(html).toContain('Tech writer');
  });

  it('paints the themed 404 when the slug does not resolve', async () => {
    installRouter((url) => {
      if (url.includes('/api/v1/users/by-slug/ghost')) {
        return jsonResponse(404, null);
      }
      return undefined;
    });
    const element = await AuthorArchivePage({
      params: Promise.resolve({ slug: 'ghost' }),
      searchParams: Promise.resolve({}),
    });
    const { container } = render(element);
    // The 404 template renders the "page not found" copy from the
    // renderer's fallback.
    expect(container.innerHTML).toContain('404');
  });

  it('preserves the ?page=N query in pagination links', async () => {
    installRouter((url) => {
      if (url.includes('/api/v1/users/by-slug/jane')) {
        return jsonResponse(200, { id: '7', slug: 'jane', name: 'Jane' });
      }
      if (url.includes('/api/v1/posts?')) {
        return jsonResponse(200, {
          posts: [
            { id: '1', slug: 'a', title: 'A', postType: 'post', blocks: [] },
          ],
          total: 25,
          perPage: 10,
          page: 2,
        });
      }
      return undefined;
    });
    const element = await AuthorArchivePage({
      params: Promise.resolve({ slug: 'jane' }),
      searchParams: Promise.resolve({ page: '2' }),
    });
    const { container } = render(element);
    expect(container.innerHTML).toContain('/author/jane?page=3');
    // Previous page goes back to the bare archive URL.
    expect(container.innerHTML).toContain('href="/author/jane"');
  });

  it('generates an author-specific document title', async () => {
    installRouter((url) => {
      if (url.includes('/api/v1/users/by-slug/jane')) {
        return jsonResponse(200, { id: '7', slug: 'jane', name: 'Jane Doe' });
      }
      return undefined;
    });
    const metadata = await generateMetadata({
      params: Promise.resolve({ slug: 'jane' }),
    });
    expect(metadata.title).toBe('Posts by Jane Doe');
  });

  it('returns a Not found title when the author is missing', async () => {
    installRouter(() => jsonResponse(404, null));
    const metadata = await generateMetadata({
      params: Promise.resolve({ slug: 'ghost' }),
    });
    expect(metadata.title).toBe('Not found');
  });
});
