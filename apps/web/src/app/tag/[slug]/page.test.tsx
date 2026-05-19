/**
 * Tag archive route tests. Same idiom as the category route tests but
 * pinned to the `post_tag` taxonomy.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { render } from '@testing-library/react';
import TagArchivePage, { generateMetadata } from './page';

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

describe('TagArchivePage', () => {
  it('renders the tag archive when the term resolves', async () => {
    installRouter((url) => {
      if (url.includes('/api/v1/terms/by-slug/post_tag/breaking')) {
        return jsonResponse(200, {
          id: '21',
          slug: 'breaking',
          name: 'Breaking',
          taxonomy: 'post_tag',
        });
      }
      if (url.includes('/api/v1/posts?')) {
        return jsonResponse(200, {
          posts: [
            { id: '1', slug: 'a', title: 'Headline', postType: 'post', blocks: [] },
          ],
          total: 1,
          perPage: 10,
          page: 1,
        });
      }
      return undefined;
    });
    const element = await TagArchivePage({
      params: Promise.resolve({ slug: 'breaking' }),
      searchParams: Promise.resolve({}),
    });
    const { container } = render(element);
    expect(container.innerHTML).toContain('Tag: Breaking');
    expect(container.innerHTML).toContain('Headline');
  });

  it('paints the themed 404 when the term does not resolve', async () => {
    installRouter((url) => {
      if (url.includes('/api/v1/terms/by-slug/post_tag/missing')) {
        return jsonResponse(404, null);
      }
      return undefined;
    });
    const element = await TagArchivePage({
      params: Promise.resolve({ slug: 'missing' }),
      searchParams: Promise.resolve({}),
    });
    const { container } = render(element);
    expect(container.innerHTML).toContain('404');
  });

  it('uses post_tag as the taxonomy filter', async () => {
    const spy = vi.spyOn(globalThis, 'fetch').mockImplementation(
      async (input: RequestInfo | URL) => {
        const url = typeof input === 'string' ? input : input.toString();
        if (url.includes('/api/v1/terms/by-slug/post_tag/breaking')) {
          return jsonResponse(200, {
            id: '21',
            slug: 'breaking',
            name: 'Breaking',
            taxonomy: 'post_tag',
          });
        }
        return jsonResponse(200, { posts: [] });
      },
    );
    await TagArchivePage({
      params: Promise.resolve({ slug: 'breaking' }),
      searchParams: Promise.resolve({}),
    });
    const archiveCall = spy.mock.calls
      .map((c) => String(c[0]))
      .find((u) => u.includes('/api/v1/posts?'));
    expect(archiveCall).toContain('taxonomy=post_tag');
    expect(archiveCall).toContain('termSlug=breaking');
  });

  it('generates a tag-prefixed document title', async () => {
    installRouter((url) => {
      if (url.includes('/api/v1/terms/by-slug/post_tag/breaking')) {
        return jsonResponse(200, {
          id: '21',
          slug: 'breaking',
          name: 'Breaking',
          taxonomy: 'post_tag',
        });
      }
      return undefined;
    });
    const metadata = await generateMetadata({
      params: Promise.resolve({ slug: 'breaking' }),
    });
    expect(metadata.title).toBe('Tag: Breaking');
  });
});
