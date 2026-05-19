/**
 * GET /feed/[category]
 *
 * Per-category Atom feed. Same shape as /feed.xml but the upstream
 * archive query is filtered by category slug. The Go side does the
 * filtering — the renderer just forwards the slug.
 *
 * URL convention: `/feed/<slug>` (no `.xml` suffix; this matches
 * WordPress's `/category/<slug>/feed/` shape minus the prefix). The
 * `.xml` variant could be added later as a parallel route if there's
 * demand; for now the Content-Type header is the canonical signal.
 *
 * If the API returns zero posts for the slug we still serve a
 * well-formed empty feed — readers handle that gracefully and it
 * makes "the category just hasn't published anything yet" indistin-
 * guishable from a transient empty result.
 */
import { fetchArchive, fetchPublicSiteConfig } from '@/lib/api';
import { renderFeedXml, FEED_ITEM_LIMIT } from '../../feed.xml/route';

export const dynamic = 'force-dynamic';

const ATOM_CONTENT_TYPE = 'application/atom+xml; charset=utf-8';
const CACHE_CONTROL = 'public, s-maxage=3600, stale-while-revalidate=86400';

/**
 * Params shape for the dynamic route segment. Next 15 makes `params`
 * a promise; the handler awaits it once.
 */
interface RouteContext {
  params: Promise<{ category: string }>;
}

export async function GET(
  _request: Request,
  context: RouteContext,
): Promise<Response> {
  const { category } = await context.params;
  // Defensive: a missing param can't happen via the Next router but
  // a direct call (e.g. from a test) might pass undefined. Treat as
  // 404 so we don't fan out to the unfiltered archive feed.
  if (!category) {
    return new Response('', { status: 404 });
  }
  const cfg = await fetchPublicSiteConfig({ revalidate: 3600 });
  const posts = await fetchArchive(
    { limit: FEED_ITEM_LIMIT * 2, category },
    { revalidate: 3600 },
  );
  const body = renderFeedXml({
    cfg,
    posts,
    feedPath: `/feed/${encodeURIComponent(category)}`,
    title: `Recent posts: ${category}`,
    subtitle: `Posts in category "${category}"`,
  });
  return new Response(body, {
    status: 200,
    headers: {
      'Content-Type': ATOM_CONTENT_TYPE,
      'Cache-Control': CACHE_CONTROL,
    },
  });
}
