/**
 * Day archive route tests. Same idiom as the month tests, plus a
 * specific check that the day segment is validated.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { render } from '@testing-library/react';
import DayArchivePage from './page';

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

describe('DayArchivePage', () => {
  it('renders the date archive for a valid year + month + day', async () => {
    installRouter((url) => {
      if (url.includes('/api/v1/posts?')) {
        return jsonResponse(200, {
          posts: [
            { id: '1', slug: 'a', title: 'May 19 post', postType: 'post', blocks: [] },
          ],
          total: 1,
          perPage: 10,
          page: 1,
        });
      }
      return undefined;
    });
    const element = await DayArchivePage({
      params: Promise.resolve({ year: '2026', month: '05', day: '19' }),
      searchParams: Promise.resolve({}),
    });
    const { container } = render(element);
    expect(container.innerHTML).toContain('Archive: May 19, 2026');
    expect(container.innerHTML).toContain('May 19 post');
  });

  it('renders the themed 404 when the day is out of range', async () => {
    installRouter(() => undefined);
    const element = await DayArchivePage({
      params: Promise.resolve({ year: '2026', month: '05', day: '32' }),
      searchParams: Promise.resolve({}),
    });
    const { container } = render(element);
    expect(container.innerHTML).toContain('404');
  });

  it('renders the themed 404 when the month is out of range', async () => {
    installRouter(() => undefined);
    const element = await DayArchivePage({
      params: Promise.resolve({ year: '2026', month: '13', day: '19' }),
      searchParams: Promise.resolve({}),
    });
    const { container } = render(element);
    expect(container.innerHTML).toContain('404');
  });

  it('falls through to singular for non-date-shaped three-segment paths', async () => {
    installRouter((url) => {
      if (url.includes('/api/v1/posts/by-slug/')) {
        return jsonResponse(200, {
          id: '1',
          slug: 'a/b/c',
          title: 'Three segment slug',
          postType: 'post',
          blocks: [],
        });
      }
      return undefined;
    });
    const element = await DayArchivePage({
      params: Promise.resolve({ year: 'foo', month: 'bar', day: 'baz' }),
      searchParams: Promise.resolve({}),
    });
    const { container } = render(element);
    expect(container.innerHTML).toContain('Three segment slug');
  });

  it('does not throw on bad input', async () => {
    installRouter(() => undefined);
    await expect(
      DayArchivePage({
        params: Promise.resolve({ year: '99', month: '99', day: '99' }),
        searchParams: Promise.resolve({}),
      }),
    ).resolves.toBeTruthy();
  });

  it('zero-pads the month and day in pagination URLs', async () => {
    installRouter((url) => {
      if (url.includes('/api/v1/posts?')) {
        return jsonResponse(200, {
          posts: [
            { id: '1', slug: 'a', title: 'A', postType: 'post', blocks: [] },
          ],
          total: 25,
          perPage: 10,
          page: 1,
        });
      }
      return undefined;
    });
    const element = await DayArchivePage({
      params: Promise.resolve({ year: '2026', month: '5', day: '7' }),
      searchParams: Promise.resolve({}),
    });
    const { container } = render(element);
    expect(container.innerHTML).toContain('/2026/05/07?page=2');
  });
});
