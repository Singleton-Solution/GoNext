/**
 * MarketplaceClient tests.
 *
 * Coverage:
 *   - grid renders one card per listing,
 *   - empty state when the listing slice is empty,
 *   - search input triggers a URL push that the server component
 *     would interpret as a re-fetch,
 *   - filter chips for category + sort drive the URL the same way.
 *
 * `next/navigation` is stubbed: tests inspect the spy to verify the
 * URL that would be pushed without actually navigating.
 */
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';

const pushSpy = vi.fn();
const refreshSpy = vi.fn();

vi.mock('next/navigation', () => ({
  useRouter: () => ({
    push: pushSpy,
    refresh: refreshSpy,
    replace: vi.fn(),
    prefetch: vi.fn(),
  }),
  useSearchParams: () => new URLSearchParams(''),
}));

import { MarketplaceClient } from './MarketplaceClient';
import type { ListingCard } from './types';

const SAMPLE: ListingCard[] = [
  {
    id: 'aaa',
    slug: 'akismet-clone',
    name: 'Akismet Clone',
    summary: 'Comment spam filtering.',
    primary_category: 'antispam',
    stars: 4.5,
    rating_count: 22,
    install_count: 1500,
    created_at: '2026-05-01T00:00:00Z',
  },
  {
    id: 'bbb',
    slug: 'seo-helper',
    name: 'SEO Helper',
    summary: 'On-page SEO recommendations.',
    primary_category: 'seo',
    stars: 4.0,
    rating_count: 8,
    install_count: 320,
    created_at: '2026-05-10T00:00:00Z',
  },
];

describe('MarketplaceClient', () => {
  beforeEach(() => {
    pushSpy.mockReset();
    refreshSpy.mockReset();
  });

  it('renders one card per listing in the grid', () => {
    render(
      <MarketplaceClient
        initialListings={SAMPLE}
        initialQuery=""
        initialCategory=""
        initialSort="recent"
      />,
    );
    const cards = screen.getAllByTestId('marketplace-card');
    expect(cards).toHaveLength(SAMPLE.length);
    expect(screen.getByText('Akismet Clone')).toBeInTheDocument();
    expect(screen.getByText('SEO Helper')).toBeInTheDocument();
  });

  it('shows the empty state when there are no listings', () => {
    render(
      <MarketplaceClient
        initialListings={[]}
        initialQuery=""
        initialCategory=""
        initialSort="recent"
      />,
    );
    expect(
      screen.getByText(/no listings match these filters/i),
    ).toBeInTheDocument();
  });

  it('pushes the search query to the URL on submit', () => {
    render(
      <MarketplaceClient
        initialListings={SAMPLE}
        initialQuery=""
        initialCategory=""
        initialSort="recent"
      />,
    );
    const input = screen.getByLabelText(/search the marketplace/i);
    fireEvent.change(input, { target: { value: 'spam' } });
    const form = input.closest('form');
    expect(form).not.toBeNull();
    fireEvent.submit(form as HTMLFormElement);
    expect(pushSpy).toHaveBeenCalled();
    const target = pushSpy.mock.calls.at(-1)?.[0] as string;
    expect(target).toContain('q=spam');
    expect(target).toContain('sort=recent');
  });

  it('builds chips from the unique categories of the rendered slice', () => {
    render(
      <MarketplaceClient
        initialListings={SAMPLE}
        initialQuery=""
        initialCategory=""
        initialSort="recent"
      />,
    );
    expect(
      screen.getByRole('button', { name: /^antispam$/i }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole('button', { name: /^seo$/i }),
    ).toBeInTheDocument();
  });

  it('switches sort key via the sort chips', () => {
    render(
      <MarketplaceClient
        initialListings={SAMPLE}
        initialQuery=""
        initialCategory=""
        initialSort="recent"
      />,
    );
    const topRatedChip = screen.getByRole('button', { name: /top rated/i });
    fireEvent.click(topRatedChip);
    expect(pushSpy).toHaveBeenCalled();
    const target = pushSpy.mock.calls.at(-1)?.[0] as string;
    expect(target).toContain('sort=stars');
  });
});
