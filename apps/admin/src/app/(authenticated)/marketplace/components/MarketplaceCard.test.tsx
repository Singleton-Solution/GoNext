/**
 * MarketplaceCard tests.
 *
 * Pins the brand-styled card structure: thumb glyph well, icon +
 * display title + monospace slug, summary, foot with emerald stars
 * and a lavender-soft capability chip. Includes a snapshot of the
 * full rendered tree so the brand contract is locked in — a future
 * style drift will fail the snapshot and demand a deliberate update.
 */
import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MarketplaceCard } from './MarketplaceCard';
import type { ListingCard } from '../types';

const SAMPLE: ListingCard = {
  id: 'ccc',
  slug: 'akismet-clone',
  name: 'Akismet Clone',
  summary: 'Comment spam filtering for sites that hum.',
  primary_category: 'antispam',
  stars: 4.5,
  rating_count: 22,
  install_count: 1500,
  created_at: '2026-05-01T00:00:00Z',
};

describe('MarketplaceCard', () => {
  it('links to the listing detail page and exposes the listing slug', () => {
    render(<MarketplaceCard listing={SAMPLE} />);
    const link = screen.getByTestId('marketplace-card');
    expect(link).toHaveAttribute('href', '/marketplace/akismet-clone');
    expect(link).toHaveAttribute('data-listing-slug', 'akismet-clone');
  });

  it('renders the display title, monospace slug, summary, and meta row', () => {
    render(<MarketplaceCard listing={SAMPLE} />);
    expect(screen.getByText('Akismet Clone')).toBeInTheDocument();
    expect(screen.getByText('akismet-clone')).toBeInTheDocument();
    expect(
      screen.getByText(/comment spam filtering for sites that hum/i),
    ).toBeInTheDocument();
    expect(screen.getByText('antispam')).toBeInTheDocument();
    expect(screen.getByText('1,500 installs')).toBeInTheDocument();
    // Rating stars: read-only display widget with the right aria-label.
    expect(screen.getByTestId('rating-stars-display')).toHaveAttribute(
      'aria-label',
      '4.5 out of 5 stars',
    );
  });

  it('falls back to a soft empty-summary line when no summary is provided', () => {
    render(
      <MarketplaceCard listing={{ ...SAMPLE, summary: '' }} />,
    );
    expect(screen.getByText(/no tagline yet/i)).toBeInTheDocument();
  });

  it('uses the brand tokens for the card surface (snapshot)', () => {
    const { asFragment } = render(<MarketplaceCard listing={SAMPLE} />);
    expect(asFragment()).toMatchSnapshot();
  });
});
