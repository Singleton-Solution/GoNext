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
 *
 * Brand
 * =====
 * The page head uses the "living systems" Headline pattern: a heavy
 * Archivo display word, an italic-serif accent for the emphasized
 * noun, and an emerald eyebrow. The lead paragraph is `--fg-muted`
 * Geist so it sits a half-step behind the headline.
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
      <header style={{ marginBottom: 32 }}>
        <span className="eyebrow">Marketplace — themes &amp; extensions</span>
        <h1
          className="h1"
          style={{
            margin: '8px 0 0',
            fontSize: 'clamp(40px, 5vw, 56px)',
            lineHeight: 0.95,
          }}
        >
          Marketplace <em>catalogue</em>.
        </h1>
        <p
          className="lead"
          style={{
            margin: '12px 0 0',
            maxWidth: 640,
          }}
        >
          Browse plugins published to your host. Pick a listing to review
          its capability request and install it — the install screen
          mirrors the consent flow you&apos;ll see for{' '}
          <Link
            href="/plugins/install"
            style={{
              color: 'var(--emerald-deep)',
              textDecoration: 'underline',
              textDecorationColor: 'var(--emerald-soft)',
              textUnderlineOffset: 3,
            }}
          >
            manual installs
          </Link>
          .
        </p>
      </header>

      {error ? (
        <div
          role="alert"
          style={{
            padding: '12px 14px',
            marginBottom: 16,
            background: 'var(--warning-soft)',
            color: 'var(--warning)',
            border: '1px solid var(--warning-soft)',
            borderRadius: 'var(--r-md)',
            fontFamily: 'var(--font-sans)',
            fontSize: 'var(--t-sm)',
          }}
        >
          Couldn&apos;t load the catalogue ({error}). The marketplace API may
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
