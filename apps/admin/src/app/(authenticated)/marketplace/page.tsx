/**
 * Marketplace — catalogue browse screen.
 *
 * Server component: fetches the first slice of listings on the server
 * so the page is interactive on the first paint. Filter / search /
 * sort chips on the client island re-fetch as the operator browses.
 *
 * Query string layout:
 *   /marketplace                  — recent listings
 *   /marketplace?category=seo     — category filter
 *   /marketplace?q=spam           — free-text search
 *   /marketplace?sort=stars       — sort key (recent|stars|popular)
 *
 * Auth: the API gates every read endpoint on a logged-in principal.
 * If the fetch fails we surface a friendly inline notice and the
 * empty state so the surrounding admin shell remains navigable.
 */
import type { ReactElement } from 'react';
import Link from 'next/link';
import { listMarketplaceListings } from './actions';
import { MarketplaceClient } from './MarketplaceClient';
import type { SortKey } from './types';

export const dynamic = 'force-dynamic';

type SearchParams = {
  category?: string;
  q?: string;
  sort?: string;
};

function parseSort(raw: string | undefined): SortKey {
  if (raw === 'stars' || raw === 'popular' || raw === 'recent') return raw;
  return 'recent';
}

export default async function MarketplacePage({
  searchParams,
}: {
  searchParams?: Promise<SearchParams>;
}): Promise<ReactElement> {
  const params = (await searchParams) ?? {};
  const sort = parseSort(params.sort);
  const { listings, error } = await listMarketplaceListings({
    category: params.category,
    q: params.q,
    sort,
  });

  return (
    <section>
      <header style={{ marginBottom: 16 }}>
        <h1 style={{ margin: 0, fontSize: 22, fontWeight: 600 }}>
          Marketplace
        </h1>
        <p
          style={{
            margin: '4px 0 0',
            color: 'var(--color-text-muted, #6b7280)',
            fontSize: 14,
            maxWidth: 720,
          }}
        >
          Browse plugins published to your host. Pick a listing to review
          its capability request and install it — the install screen
          mirrors the consent flow you’ll see for{' '}
          <Link href="/plugins/install">manual installs</Link>.
        </p>
      </header>

      {error ? (
        <div
          role="alert"
          style={{
            padding: 12,
            marginBottom: 16,
            background: '#fef9c3',
            color: '#854d0e',
            border: '1px solid #fde68a',
            borderRadius: 6,
            fontSize: 13,
          }}
        >
          Couldn’t load the catalogue ({error}). The marketplace API may
          not be available yet.
        </div>
      ) : null}

      <MarketplaceClient
        initialListings={listings}
        initialQuery={params.q ?? ''}
        initialCategory={params.category ?? ''}
        initialSort={sort}
      />
    </section>
  );
}
