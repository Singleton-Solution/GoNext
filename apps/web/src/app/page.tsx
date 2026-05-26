/**
 * Homepage handler — `/` route.
 *
 * Previously this dispatched to `renderArchive` so the GoNext root
 * route looked like a classic WordPress blog-home (a vertical list of
 * latest posts). The Living-Systems handoff redefines the public
 * landing as the **marketing site**: a cream hero with a massive
 * "Sites that *live* and grow." headline, a feature grid, a forest
 * "alive band" full of stats, and a closing CTA.
 *
 * The original archive behavior is preserved as a *section* on the
 * marketing page — `<MarketingRecentStories>` reads from `fetchArchive`
 * and surfaces the most recent posts as cards. So nothing about the
 * data flow changes; the visual envelope does.
 *
 * Once site settings let an admin pin a static front page, this
 * handler will dispatch between `renderSingular(<frontPageSlug>)` and
 * the marketing page based on `core.reading.show_on_front`. Until that
 * wiring lands the default behaviour is the brand landing.
 *
 * The catch-all slug route is owned by `[...slug]/page.tsx`; Next
 * routes `/` here because root-level `page.tsx` wins over the
 * catch-all for the empty slug array.
 */
import { cookies } from 'next/headers';
import type { ReactElement } from 'react';
import { fetchArchive } from '@/lib/api';
import { isAuthenticatedCookie } from '@/lib/render';
import { MarketingNav } from '@/components/marketing/Nav';
import { MarketingHero } from '@/components/marketing/Hero';
import { MarketingLogos } from '@/components/marketing/LogoMarquee';
import { MarketingFeatures } from '@/components/marketing/Features';
import { MarketingAliveBand } from '@/components/marketing/AliveBand';
import { MarketingRecentStories } from '@/components/marketing/RecentStories';
import { MarketingCtaBlock } from '@/components/marketing/CtaBlock';
import { MarketingFooter } from '@/components/marketing/Footer';

export const dynamic = 'force-dynamic';

async function readCookieHeader(): Promise<string> {
  try {
    const store = await cookies();
    return store
      .getAll()
      .map((c) => `${c.name}=${c.value}`)
      .join('; ');
  } catch {
    return '';
  }
}

export default async function HomePage(): Promise<ReactElement> {
  const cookie = await readCookieHeader();
  const revalidate = isAuthenticatedCookie(cookie) ? undefined : 300;
  // Fetch the most recent posts so the "Recent stories" section feels
  // real even on a freshly-installed site. We bound to 6 because the
  // section paints a 3-column grid two rows deep.
  const posts = await fetchArchive(
    { limit: 6 },
    { revalidate, cookie },
  );

  return (
    <div className="min-h-screen bg-paper text-ink">
      <MarketingNav />
      <main>
        <MarketingHero />
        <MarketingLogos />
        <MarketingFeatures />
        <MarketingAliveBand />
        <MarketingRecentStories posts={posts} />
        <MarketingCtaBlock />
      </main>
      <MarketingFooter />
    </div>
  );
}
