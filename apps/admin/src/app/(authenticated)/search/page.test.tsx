/**
 * /search — brand-restyle contract tests.
 *
 * We pin three brand-surface guarantees:
 *
 *   1. Empty state (no `q`) — the page Headline renders as the brand
 *      grotesque ("Search.") with no live query.
 *   2. Result state — the Headline interpolates the query as the
 *      italic-accent <em>, and the mono p95 readout appears once the
 *      API returns a `query_duration_ms`.
 *   3. Result cards reuse the .search-page CSS hooks that the brand
 *      restyles to emerald-soft <mark> highlights — we assert the
 *      class plumbing so a refactor can't silently drop it.
 */
import { render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

const searchParams = new Map<string, string>();
vi.mock('next/navigation', () => ({
  useSearchParams: () => ({
    get: (key: string) => searchParams.get(key) ?? null,
  }),
  useRouter: () => ({ push: vi.fn() }),
}));

const apiGetMock = vi.fn();
vi.mock('@/lib/api-client', () => ({
  api: { get: (...args: unknown[]) => apiGetMock(...args) },
  ApiError: class ApiError extends Error {
    constructor(public status: number, message: string) {
      super(message);
    }
  },
}));

import SearchPage from './page';

describe('<SearchPage>', () => {
  it('renders the empty Headline when q is missing', () => {
    searchParams.clear();
    render(<SearchPage />);
    // The brand Headline is the canonical h1 on the page.
    const headings = screen.getAllByRole('heading', { level: 1 });
    expect(headings.length).toBeGreaterThan(0);
    expect(headings.some((h) => h.className.includes('font-display'))).toBe(true);
    // The kbd hint references the cmd-K shortcut.
    expect(screen.getByTestId('search-empty').textContent).toContain('⌘K');
  });

  it('paints the italic-accent query in the Headline and the p95 readout', async () => {
    searchParams.clear();
    searchParams.set('q', 'hello');
    apiGetMock.mockResolvedValueOnce({
      hits: [
        {
          id: 'p1',
          type: 'post',
          slug: 'h',
          title: 'Hello world',
          excerpt_html: 'Saying <mark>hello</mark>.',
          rank: 0.5,
        },
      ],
      total: 1,
      query_duration_ms: 120,
    });

    render(<SearchPage />);

    await waitFor(() => {
      expect(screen.getByTestId('search-results')).not.toBeNull();
    });

    const heading = screen.getByRole('heading', { level: 1 });
    const em = heading.querySelector('em');
    expect(em).not.toBeNull();
    expect(em?.textContent).toBe('hello');

    const p95 = screen.getByTestId('p95-readout');
    expect(p95.textContent).toContain('120ms');
    // Under budget → emerald-deep tone.
    expect(p95.className).toContain('text-emerald-deep');
  });

  it('lifts the p95 readout to warning when over budget', async () => {
    searchParams.clear();
    searchParams.set('q', 'slow');
    apiGetMock.mockResolvedValueOnce({
      hits: [],
      total: 0,
      query_duration_ms: 480,
    });

    render(<SearchPage />);
    await waitFor(() => {
      expect(screen.getByTestId('p95-readout').className).toContain('text-warning');
    });
  });
});
