/**
 * Year archive route tests.
 *
 * Covers all four branches:
 *   - Valid year -> date archive renders.
 *   - Year-shaped but out of range (e.g. "0001") -> themed 404.
 *   - Non-year-shaped input ("hello-world") -> singular fall-through.
 *   - Bad input never produces a 500 — every branch returns a valid
 *     ReactElement so the route doesn't throw past Next's boundary.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { render } from '@testing-library/react';
import YearArchivePage, { generateMetadata } from './page';

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

describe('YearArchivePage', () => {
  it('renders the date archive for a valid year', async () => {
    installRouter((url) => {
      if (url.includes('/api/v1/posts?')) {
        return jsonResponse(200, {
          posts: [
            { id: '1', slug: 'a', title: 'A post', postType: 'post', blocks: [] },
          ],
          total: 1,
          perPage: 10,
          page: 1,
        });
      }
      return undefined;
    });
    const element = await YearArchivePage({
      params: Promise.resolve({ year: '2026' }),
      searchParams: Promise.resolve({}),
    });
    const { container } = render(element);
    expect(container.innerHTML).toContain('Archive: 2026');
    expect(container.innerHTML).toContain('A post');
  });

  it('renders the themed 404 for a year-shaped but out-of-range input', async () => {
    installRouter(() => undefined);
    const element = await YearArchivePage({
      params: Promise.resolve({ year: '0001' }),
      searchParams: Promise.resolve({}),
    });
    const { container } = render(element);
    expect(container.innerHTML).toContain('404');
  });

  it('falls through to singular for non-year-shaped input', async () => {
    installRouter((url) => {
      if (url.includes('/api/v1/posts/by-slug/hello-world')) {
        return jsonResponse(200, {
          id: '1',
          slug: 'hello-world',
          title: 'Hello, World',
          postType: 'post',
          blocks: [
            { type: 'core/paragraph', attributes: { content: 'hi' } },
          ],
        });
      }
      return undefined;
    });
    const element = await YearArchivePage({
      params: Promise.resolve({ year: 'hello-world' }),
      searchParams: Promise.resolve({}),
    });
    const { container } = render(element);
    expect(container.innerHTML).toContain('Hello, World');
  });

  it('survives malformed input without throwing', async () => {
    // No router needed — every fetch returns 404 by default. The
    // route must still produce a valid element, not a 500.
    installRouter(() => undefined);
    await expect(
      YearArchivePage({
        params: Promise.resolve({ year: '99' }),
        searchParams: Promise.resolve({}),
      }),
    ).resolves.toBeTruthy();
  });

  it('preserves pagination URLs in the archive view', async () => {
    installRouter((url) => {
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
    const element = await YearArchivePage({
      params: Promise.resolve({ year: '2026' }),
      searchParams: Promise.resolve({ page: '2' }),
    });
    const { container } = render(element);
    expect(container.innerHTML).toContain('/2026?page=3');
    expect(container.innerHTML).toContain('href="/2026"');
  });

  it('generates a date heading as the document title', async () => {
    installRouter(() => undefined);
    const metadata = await generateMetadata({
      params: Promise.resolve({ year: '2026' }),
    });
    expect(metadata.title).toBe('Archive: 2026');
  });
});
