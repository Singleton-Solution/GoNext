/**
 * MarketplaceCard — single tile in the catalogue grid.
 *
 * Renders the listing's icon-shaped initial, name, tagline, star
 * aggregate, and category chip. The whole card is a link to the
 * detail page (`/marketplace/{slug}`); the install affordance lives
 * on the detail page rather than the card to keep the consent flow
 * out of the browsing surface.
 *
 * Server-component compatible: no hooks, no state, no event handlers.
 * The host can render hundreds of cards in one server response without
 * paying for client-side hydration per tile.
 */

import Link from 'next/link';
import type { CSSProperties, ReactElement } from 'react';
import { RatingStars } from './RatingStars';
import type { ListingCard } from '../types';

const styles: Record<string, CSSProperties> = {
  card: {
    display: 'flex',
    flexDirection: 'column',
    gap: 8,
    padding: 16,
    border: '1px solid var(--color-border, #e4e6ea)',
    borderRadius: 8,
    background: 'var(--color-surface, #ffffff)',
    textDecoration: 'none',
    color: 'inherit',
    transition: 'box-shadow 120ms ease',
    minHeight: 140,
  },
  header: {
    display: 'flex',
    alignItems: 'center',
    gap: 10,
  },
  icon: {
    width: 36,
    height: 36,
    borderRadius: 8,
    background: 'linear-gradient(135deg, #f0f4ff, #e0e7ff)',
    display: 'inline-flex',
    alignItems: 'center',
    justifyContent: 'center',
    fontSize: 16,
    fontWeight: 600,
    color: '#3730a3',
    flex: '0 0 auto',
  },
  name: {
    margin: 0,
    fontSize: 15,
    fontWeight: 600,
    color: 'var(--color-text, #1c2024)',
    overflow: 'hidden',
    textOverflow: 'ellipsis',
    whiteSpace: 'nowrap',
  },
  slug: {
    margin: 0,
    fontSize: 12,
    color: 'var(--color-text-muted, #6b7280)',
    fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
  },
  summary: {
    margin: 0,
    fontSize: 13,
    lineHeight: 1.45,
    color: 'var(--color-text, #1c2024)',
    display: '-webkit-box',
    WebkitLineClamp: 2,
    WebkitBoxOrient: 'vertical',
    overflow: 'hidden',
  },
  footer: {
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'space-between',
    marginTop: 'auto',
    paddingTop: 8,
    gap: 8,
  },
  chip: {
    display: 'inline-block',
    padding: '1px 8px',
    borderRadius: 999,
    background: '#eef2ff',
    color: '#3730a3',
    fontSize: 11,
    fontWeight: 500,
    textTransform: 'lowercase',
  },
  installs: {
    fontSize: 12,
    color: 'var(--color-text-muted, #6b7280)',
  },
};

export interface MarketplaceCardProps {
  listing: ListingCard;
}

export function MarketplaceCard({
  listing,
}: MarketplaceCardProps): ReactElement {
  const initial = (listing.name?.[0] ?? listing.slug[0] ?? '?').toUpperCase();
  return (
    <Link
      href={`/marketplace/${encodeURIComponent(listing.slug)}`}
      style={styles.card}
      data-testid="marketplace-card"
      data-listing-slug={listing.slug}
    >
      <div style={styles.header}>
        <span style={styles.icon} aria-hidden="true">
          {initial}
        </span>
        <div style={{ minWidth: 0, flex: 1 }}>
          <h3 style={styles.name}>{listing.name}</h3>
          <p style={styles.slug}>{listing.slug}</p>
        </div>
      </div>
      {listing.summary ? (
        <p style={styles.summary}>{listing.summary}</p>
      ) : (
        <p style={{ ...styles.summary, fontStyle: 'italic' }}>
          No summary yet.
        </p>
      )}
      <div style={styles.footer}>
        <RatingStars value={listing.stars} count={listing.rating_count} />
        {listing.primary_category ? (
          <span style={styles.chip}>{listing.primary_category}</span>
        ) : null}
        <span style={styles.installs}>
          {listing.install_count.toLocaleString()} installs
        </span>
      </div>
    </Link>
  );
}
