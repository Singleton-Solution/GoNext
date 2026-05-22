/**
 * Month archive route tests. Verifies validation, fall-through to
 * singular for non-date-shaped inputs, and pagination URL preservation.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { render } from '@testing-library/react';
import MonthArchivePage from './page';

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

describe('MonthArchivePage', () => {
  it('renders the date archive for a valid year + month', async () => {
    installRouter((url) => {
      if (url.includes('/api/v1/posts?')) {
        return jsonResponse(200, {
          posts: [
            { id: '1', slug: 'a', title: 'May post', postType: 'post', blocks: [] },
          ],
          total: 1,
          perPage: 10,
          page: 1,
        });
      }
      return undefined;
    });
    const element = await MonthArchivePage({
      params: Promise.resolve({ year: '2026', month: '05' }),
      searchParams: Promise.resolve({}),
    });
    const { container } = render(element);
    expect(container.innerHTML).toContain('Archive: May 2026');
    expect(container.innerHTML).toContain('May post');
  });

  it('renders the themed 404 when the month is out of range', async () => {
    installRouter(() => undefined);
    const element = await MonthArchivePage({
      params: Promise.resolve({ year: '2026', month: '13' }),
      searchParams: Promise.resolve({}),
    });
    const { container } = render(element);
    expect(container.innerHTML).toContain('404');
  });

  it('falls through to singular for non-date-shaped two-segment paths', async () => {
    installRouter((url) => {
      if (url.includes('/api/v1/posts/by-slug/')) {
        return jsonResponse(200, {
          id: '1',
          slug: 'news/hello',
          title: 'Two segment slug',
          postType: 'post',
          blocks: [],
        });
      }
      return undefined;
    });
    const element = await MonthArchivePage({
      params: Promise.resolve({ year: 'news', month: 'hello' }),
      searchParams: Promise.resolve({}),
    });
    const { container } = render(element);
    expect(container.innerHTML).toContain('Two segment slug');
  });

  it('does not throw on malformed input', async () => {
    installRouter(() => undefined);
    await expect(
      MonthArchivePage({
        params: Promise.resolve({ year: '99', month: '99' }),
        searchParams: Promise.resolve({}),
      }),
    ).resolves.toBeTruthy();
  });

  it('zero-pads the month in pagination URLs', async () => {
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
    const element = await MonthArchivePage({
      params: Promise.resolve({ year: '2026', month: '5' }),
      searchParams: Promise.resolve({}),
    });
    const { container } = render(element);
    expect(container.innerHTML).toContain('/2026/05?page=2');
  });
});
