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
 * Issue #510 wires the static-front-page dispatcher promised by the
 * comment that used to live here. When an admin sets
 * `core.reading.homepage_type = 'static_page'` and pins a
 * `core.reading.homepage_page_id`, this handler renders that page
 * through `renderSingular` + `PublicShell` — the same path the
 * catch-all slug route uses. All other configurations (the default
 * `'latest_posts'`, or `'static_page'` with an empty id) fall through
 * to the marketing landing so a half-configured admin form never
 * breaks the front door.
 *
 * The catch-all slug route is owned by `[...slug]/page.tsx`; Next
 * routes `/` here because root-level `page.tsx` wins over the
 * catch-all for the empty slug array.
 */
import { cookies } from 'next/headers';
import type { ReactElement } from 'react';
import { fetchArchive, fetchSiteOptions, type Post } from '@/lib/api';
import { isAuthenticatedCookie, renderSingular } from '@/lib/render';
import { MarketingNav } from '@/components/marketing/Nav';
import { MarketingHero } from '@/components/marketing/Hero';
import { MarketingLogos } from '@/components/marketing/LogoMarquee';
import { MarketingFeatures } from '@/components/marketing/Features';
import { MarketingAliveBand } from '@/components/marketing/AliveBand';
import { MarketingRecentStories } from '@/components/marketing/RecentStories';
import { MarketingCtaBlock } from '@/components/marketing/CtaBlock';
import { MarketingFooter } from '@/components/marketing/Footer';
import { PublicShell } from './PublicShell';

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

/**
 * Render the marketing landing — extracted so both the default branch
 * of the dispatcher and the "static page misconfigured / fetch failed"
 * fallback share one implementation. Pre-fetches the recent posts so
 * `<MarketingRecentStories>` paints cards on first paint.
 */
async function renderMarketingLanding(
  cookie: string,
  revalidate: number | undefined,
): Promise<ReactElement> {
  // Fetch the most recent posts so the "Recent stories" section feels
  // real even on a freshly-installed site. We bound to 6 because the
  // section paints a 3-column grid two rows deep.
  let posts: Post[] = [];
  try {
    posts = await fetchArchive({ limit: 6 }, { revalidate, cookie });
  } catch {
    // fetchArchive throws on a 5xx; the marketing landing still paints
    // — Recent Stories just shows the empty state.
    posts = [];
  }

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

export default async function HomePage(): Promise<ReactElement> {
  const cookie = await readCookieHeader();
  const revalidate = isAuthenticatedCookie(cookie) ? undefined : 300;

  // Dispatcher (issue #510). The reading projection lives on the same
  // public-site payload that already feeds the marketing nav, so this
  // is the same fetch that paints the chrome — Next dedupes it.
  const opts = await fetchSiteOptions({ revalidate: 60 });
  if (
    opts.reading.homepageType === 'static_page' &&
    opts.reading.homepagePageId !== ''
  ) {
    try {
      const result = await renderSingular(opts.reading.homepagePageId, { cookie });
      if (result.status === 200) {
        return (
          <PublicShell
            bodyHtml={result.html}
            cssCustomProperties={result.css}
            templateBasename={result.templateBasename}
          />
        );
      }
      // Page fetch came back 404 (or any non-200). Fall through to the
      // marketing landing so an admin who pinned a since-deleted page
      // doesn't blow up the front door.
    } catch {
      // renderSingular shouldn't throw, but if a downstream fetch
      // surfaces an unexpected error we still want the landing.
    }
  }

  return renderMarketingLanding(cookie, revalidate);
}
