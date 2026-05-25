/**
 * MarketplaceCard — single tile in the catalogue grid.
 *
 * Renders the listing's icon-shaped initial, name, tagline, star
 * aggregate, and category chip. The whole card is a link to the
 * detail page (`/marketplace/{slug}`); the install affordance lives
 * on the detail page rather than the card to keep the consent flow
 * out of the browsing surface.
 *
 * Visual identity ("Living systems")
 * ===================================
 * Cards sit on paper-2 with a paper-3 thumb glyph well, hairline
 * border, and the resting `--sh-xs` shadow. Hovering lifts to `--sh-md`
 * and tugs the card up 2px via `transform` — the same hover gesture
 * the marketplace HTML moodboard uses. Inside the body:
 *   - Title is Archivo 600 (sans display) for a tight, alive look
 *   - Tagline is Geist (sans body) at `--t-sm`
 *   - Stars are emerald — not the legacy amber. They sit alongside
 *     a tiny "(n)" rating count in `--fg-subtle`.
 *   - The capability count chip is `--lavender-soft / --lavender-deep`
 *     — the secondary brand accent, matching the data-viz pairing in
 *     the design tokens.
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
    background: 'var(--paper-2)',
    border: '1px solid var(--border)',
    borderRadius: 'var(--r-lg)',
    textDecoration: 'none',
    color: 'var(--ink)',
    boxShadow: 'var(--sh-xs)',
    transition:
      'transform var(--dur) var(--ease), box-shadow var(--dur) var(--ease), border-color var(--dur) var(--ease)',
    overflow: 'hidden',
    minHeight: 220,
  },
  thumb: {
    aspectRatio: '16 / 9',
    background: 'var(--paper-3)',
    borderBottom: '1px solid var(--border)',
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    position: 'relative',
    overflow: 'hidden',
  },
  thumbGlyph: {
    fontFamily: 'var(--font-display)',
    fontWeight: 800,
    fontSize: 56,
    letterSpacing: '-0.04em',
    lineHeight: 1,
    color: 'var(--emerald-deep)',
  },
  body: {
    padding: '16px 18px 18px',
    display: 'flex',
    flexDirection: 'column',
    gap: 10,
    flex: 1,
  },
  headRow: {
    display: 'flex',
    alignItems: 'flex-start',
    gap: 12,
  },
  icon: {
    width: 36,
    height: 36,
    borderRadius: 'var(--r-sm)',
    border: '1px solid var(--border)',
    background: 'var(--paper)',
    display: 'inline-flex',
    alignItems: 'center',
    justifyContent: 'center',
    fontFamily: 'var(--font-display)',
    fontWeight: 800,
    fontSize: 16,
    color: 'var(--emerald-deep)',
    flex: '0 0 auto',
  },
  titleBlock: { minWidth: 0, flex: 1 },
  name: {
    margin: 0,
    fontFamily: 'var(--font-display)',
    fontWeight: 600,
    fontSize: 'var(--t-lg)',
    letterSpacing: '-0.005em',
    lineHeight: 1.2,
    color: 'var(--ink)',
    overflow: 'hidden',
    textOverflow: 'ellipsis',
    whiteSpace: 'nowrap',
  },
  slug: {
    margin: '2px 0 0',
    fontFamily: 'var(--font-mono)',
    fontSize: 'var(--t-xs)',
    color: 'var(--fg-subtle)',
  },
  summary: {
    margin: 0,
    fontFamily: 'var(--font-sans)',
    fontSize: 'var(--t-sm)',
    lineHeight: 1.5,
    color: 'var(--fg-muted)',
    display: '-webkit-box',
    WebkitLineClamp: 2,
    WebkitBoxOrient: 'vertical',
    overflow: 'hidden',
  },
  summaryEmpty: {
    fontStyle: 'italic',
    color: 'var(--fg-faint)',
  },
  foot: {
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'space-between',
    gap: 8,
    marginTop: 'auto',
    paddingTop: 12,
    borderTop: '1px solid var(--border)',
  },
  chip: {
    display: 'inline-flex',
    alignItems: 'center',
    padding: '2px 8px',
    borderRadius: 'var(--r-sm)',
    background: 'var(--lavender-soft)',
    color: 'var(--lavender-deep)',
    fontFamily: 'var(--font-sans)',
    fontSize: 'var(--t-xs)',
    fontWeight: 500,
  },
  installs: {
    fontFamily: 'var(--font-mono)',
    fontSize: 'var(--t-xs)',
    color: 'var(--fg-subtle)',
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
      className="marketplace-card"
      style={styles.card}
      data-testid="marketplace-card"
      data-listing-slug={listing.slug}
    >
      <div style={styles.thumb} aria-hidden="true">
        <span style={styles.thumbGlyph}>{initial}</span>
      </div>
      <div style={styles.body}>
        <div style={styles.headRow}>
          <span style={styles.icon} aria-hidden="true">
            {initial}
          </span>
          <div style={styles.titleBlock}>
            <h3 style={styles.name}>{listing.name}</h3>
            <p style={styles.slug}>{listing.slug}</p>
          </div>
        </div>
        {listing.summary ? (
          <p style={styles.summary}>{listing.summary}</p>
        ) : (
          <p style={{ ...styles.summary, ...styles.summaryEmpty }}>
            No tagline yet.
          </p>
        )}
        <div style={styles.foot}>
          <RatingStars value={listing.stars} count={listing.rating_count} />
          {listing.primary_category ? (
            <span style={styles.chip}>{listing.primary_category}</span>
          ) : null}
          <span style={styles.installs}>
            {listing.install_count.toLocaleString()} installs
          </span>
        </div>
      </div>
    </Link>
  );
}
