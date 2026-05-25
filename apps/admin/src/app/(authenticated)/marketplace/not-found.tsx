/**
 * Marketplace — 404 surface.
 *
 * Renders when:
 *   • A listing slug doesn't match any catalogue entry.
 *   • The marketplace catalogue API returns `not_found` for a
 *     `/marketplace/[slug]` request and the page calls `notFound()`.
 *
 * Uses the shared brand <NotFoundState>. Per the brand voice, 404 is
 * a successful "no such page" outcome — calm emerald accent, no
 * alarm. The CTA sends the user back to the catalogue, not to the
 * admin home, because that's the surface they were trying to use.
 */
import type { ReactElement } from 'react';
import { NotFoundState } from '@/components/states';

export default function MarketplaceNotFound(): ReactElement {
  return (
    <section style={{ padding: 24 }}>
      <NotFoundState
        eyebrow="404 · extension"
        title={
          <>
            No <em>extension</em> at that slug.
          </>
        }
        body="It may have been unpublished, renamed, or never existed in our catalogue. Browse the marketplace for something nearby."
        href="/marketplace"
        actionLabel="Back to marketplace"
      />
    </section>
  );
}
