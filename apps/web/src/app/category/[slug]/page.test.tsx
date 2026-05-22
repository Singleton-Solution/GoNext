/**
 * Category archive route tests. Mirrors the author route tests with
 * the taxonomy bound to `category`.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { render } from '@testing-library/react';
import CategoryArchivePage, { generateMetadata } from './page';

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

describe('CategoryArchivePage', () => {
  it('renders the category archive when the term resolves', async () => {
    installRouter((url) => {
      if (url.includes('/api/v1/terms/by-slug/category/news')) {
        return jsonResponse(200, {
          id: '11',
          slug: 'news',
          name: 'News',
          taxonomy: 'category',
        });
      }
      if (url.includes('/api/v1/posts?')) {
        return jsonResponse(200, {
          posts: [
            { id: '1', slug: 'a', title: 'Breaking', postType: 'post', blocks: [] },
          ],
          total: 1,
          perPage: 10,
          page: 1,
        });
      }
      return undefined;
    });
    const element = await CategoryArchivePage({
      params: Promise.resolve({ slug: 'news' }),
      searchParams: Promise.resolve({}),
    });
    const { container } = render(element);
    expect(container.innerHTML).toContain('Category: News');
    expect(container.innerHTML).toContain('Breaking');
  });

  it('paints the themed 404 when the term does not resolve', async () => {
    installRouter((url) => {
      if (url.includes('/api/v1/terms/by-slug/category/missing')) {
        return jsonResponse(404, null);
      }
      return undefined;
    });
    const element = await CategoryArchivePage({
      params: Promise.resolve({ slug: 'missing' }),
      searchParams: Promise.resolve({}),
    });
    const { container } = render(element);
    expect(container.innerHTML).toContain('404');
  });

  it('forwards the taxonomy filter as a query param', async () => {
    const spy = vi.spyOn(globalThis, 'fetch').mockImplementation(
      async (input: RequestInfo | URL) => {
        const url = typeof input === 'string' ? input : input.toString();
        if (url.includes('/api/v1/terms/by-slug/category/news')) {
          return jsonResponse(200, {
            id: '11',
            slug: 'news',
            name: 'News',
            taxonomy: 'category',
          });
        }
        return jsonResponse(200, { posts: [] });
      },
    );
    await CategoryArchivePage({
      params: Promise.resolve({ slug: 'news' }),
      searchParams: Promise.resolve({}),
    });
    const archiveCall = spy.mock.calls
      .map((c) => String(c[0]))
      .find((u) => u.includes('/api/v1/posts?'));
    expect(archiveCall).toBeDefined();
    expect(archiveCall).toContain('taxonomy=category');
    expect(archiveCall).toContain('termSlug=news');
  });

  it('generates a category-prefixed document title', async () => {
    installRouter((url) => {
      if (url.includes('/api/v1/terms/by-slug/category/news')) {
        return jsonResponse(200, {
          id: '11',
          slug: 'news',
          name: 'News',
          taxonomy: 'category',
        });
      }
      return undefined;
    });
    const metadata = await generateMetadata({
      params: Promise.resolve({ slug: 'news' }),
    });
    expect(metadata.title).toBe('Category: News');
  });
});
