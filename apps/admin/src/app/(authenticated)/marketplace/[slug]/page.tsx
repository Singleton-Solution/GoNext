/**
 * Marketplace — listing detail page.
 *
 * Server component that fetches the listing detail, version history,
 * and ratings aggregate in parallel, then hands them to the client
 * island for the install affordance and the rating-submission form.
 *
 * The install button on this page navigates to
 * `/marketplace/{slug}/install` — that route owns the capability
 * review screen + the consent checkbox. We deliberately do not show
 * a "one-click install" affordance: every install path in GoNext
 * must traverse the capability review, no exceptions.
 *
 * Brand
 * =====
 * The "Back" crumb uses the emerald-deep underline pattern that runs
 * across the marketplace; on error we surface a `--warning-soft`
 * notice with the canonical card chrome.
 */
import Link from 'next/link';
import { notFound } from 'next/navigation';
import type { ReactElement } from 'react';
import {
  getMarketplaceListing,
  getMarketplaceRatings,
  getMarketplaceVersions,
} from '../actions';
import { ListingDetailView } from './ListingDetailView';

export const dynamic = 'force-dynamic';

const backLinkStyle = {
  display: 'inline-flex',
  alignItems: 'center',
  gap: 4,
  marginBottom: 18,
  fontFamily: 'var(--font-sans)',
  fontSize: 'var(--t-sm)',
  color: 'var(--emerald-deep)',
  textDecoration: 'underline',
  textDecorationColor: 'var(--emerald-soft)',
  textUnderlineOffset: 3,
} as const;

export default async function MarketplaceListingPage({
  params,
}: {
  params: Promise<{ slug: string }>;
}): Promise<ReactElement> {
  const { slug } = await params;
  const [{ listing, error }, { versions }, { ratings }] = await Promise.all([
    getMarketplaceListing(slug),
    getMarketplaceVersions(slug),
    getMarketplaceRatings(slug),
  ]);
  if (!listing) {
    if (error === 'not_found') notFound();
    // Other errors: render an inline notice rather than 404.
    return (
      <section>
        <Link href="/marketplace" style={backLinkStyle}>
          ← Back to marketplace
        </Link>
        <h1
          className="h1"
          style={{ marginTop: 0, fontSize: 'clamp(36px, 4.5vw, 52px)' }}
        >
          {slug}
        </h1>
        <div
          role="alert"
          style={{
            marginTop: 16,
            padding: '12px 14px',
            background: 'var(--warning-soft)',
            color: 'var(--warning)',
            border: '1px solid var(--warning-soft)',
            borderRadius: 'var(--r-md)',
            fontFamily: 'var(--font-sans)',
            fontSize: 'var(--t-sm)',
          }}
        >
          Couldn&apos;t load this listing ({error ?? 'unknown error'}).
        </div>
      </section>
    );
  }

  return (
    <section>
      <Link href="/marketplace" style={backLinkStyle}>
        ← Back to marketplace
      </Link>
      <ListingDetailView
        listing={listing}
        versions={versions}
        ratings={ratings ?? { aggregate: { average: 0, count: 0 }, ratings: [] }}
      />
    </section>
  );
}
