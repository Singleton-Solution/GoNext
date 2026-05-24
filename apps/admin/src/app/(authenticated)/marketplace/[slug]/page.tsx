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
        <p style={{ marginBottom: 12 }}>
          <Link href="/marketplace">← Back to marketplace</Link>
        </p>
        <h1 style={{ marginTop: 0, fontSize: 22, fontWeight: 600 }}>
          {slug}
        </h1>
        <div
          role="alert"
          style={{
            padding: 12,
            background: '#fef9c3',
            color: '#854d0e',
            border: '1px solid #fde68a',
            borderRadius: 6,
            fontSize: 13,
          }}
        >
          Couldn’t load this listing ({error ?? 'unknown error'}).
        </div>
      </section>
    );
  }

  return (
    <section>
      <p style={{ marginBottom: 12 }}>
        <Link href="/marketplace">← Back to marketplace</Link>
      </p>
      <ListingDetailView
        listing={listing}
        versions={versions}
        ratings={ratings ?? { aggregate: { average: 0, count: 0 }, ratings: [] }}
      />
    </section>
  );
}
