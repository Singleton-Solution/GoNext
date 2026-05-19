/**
 * GET /sitemap.xml
 *
 * Serves the sitemap for the public-facing site. The handler does
 * three things:
 *
 *  1. Pull the canonical base URL from the API's public-site config.
 *     Without a base URL we have nothing to put in `<loc>` elements
 *     and we'd produce a malformed sitemap; we return an empty (but
 *     still well-formed) urlset in that case so crawlers don't see a
 *     500.
 *  2. Fetch the full published-post + page set. The Go side caps the
 *     response at a high limit; for sites that actually approach the
 *     sitemaps.org 50,000-URL per-file cap, the response is split into
 *     numbered child sitemaps + an index file at this URL.
 *  3. Stamp the response with `Cache-Control: public, s-maxage=3600`
 *     so a busy site doesn't hit the API once per crawler poll.
 *
 * The route is `dynamic = 'force-dynamic'` because Next would
 * otherwise statically prerender it at build time, which defeats the
 * point — sitemaps reflect "what's published right now."
 */
import {
  buildSitemap,
  buildSitemapIndex,
  paginateSitemap,
  SITEMAP_MAX_URLS_PER_FILE,
  type SitemapEntry,
} from '@/lib/feeds';
import {
  fetchArchive,
  fetchPublicSiteConfig,
  type Post,
  type PublicSiteConfig,
} from '@/lib/api';

export const dynamic = 'force-dynamic';

/** Single source of truth for the response Content-Type. */
const XML_CONTENT_TYPE = 'application/xml; charset=utf-8';
/** Cache for one hour at the CDN; the API itself revalidates internally. */
const CACHE_CONTROL = 'public, s-maxage=3600, stale-while-revalidate=86400';

/**
 * Build the canonical URL for a post or page. The Go side is the
 * source of truth for slug structure; we mirror its convention:
 *
 *  - `postType === "page"` -> `${base}/${slug}` (no prefix)
 *  - everything else -> `${base}/${slug}`
 *
 * Future post-type-specific prefixes (e.g. `/blog/`) belong here so
 * the sitemap and the catch-all route stay in lockstep.
 */
function postUrl(baseUrl: string, post: Post): string {
  // The slug from the API never starts with "/" by contract; we add
  // the separator here.
  return `${baseUrl}/${post.slug}`;
}

/** Map an archive Post into the SitemapEntry shape. */
function postToEntry(baseUrl: string, post: Post): SitemapEntry {
  const entry: SitemapEntry = { loc: postUrl(baseUrl, post) };
  if (post.publishedAt) {
    entry.lastmod = post.publishedAt;
  }
  return entry;
}

/**
 * Decide whether to serve the index or a single sitemap. Exported as
 * a pure function so tests can hold it to a contract without
 * round-tripping through Response objects.
 */
export function renderSitemapXml(
  cfg: PublicSiteConfig,
  posts: Post[],
): string {
  // Empty post set => still emit a well-formed urlset. Crawlers
  // tolerate this and we avoid sending HTTP errors on a fresh site.
  if (!cfg.baseUrl) {
    return buildSitemap([]);
  }
  const entries = posts.map((p) => postToEntry(cfg.baseUrl, p));
  const pages = paginateSitemap(entries);
  if (pages.length <= 1) {
    return buildSitemap(pages[0] ?? []);
  }
  // Paginated: emit an index. Each page is served at
  // `/sitemap-{n}.xml` (1-indexed) by the child route file. We don't
  // bother re-implementing the child route here — every search engine
  // we care about issues a GET per index entry, so emitting `loc`
  // values the renderer can answer is sufficient.
  return buildSitemapIndex(
    pages.map((_, i) => ({
      loc: `${cfg.baseUrl}/sitemap-${i + 1}.xml`,
    })),
  );
}

export async function GET(): Promise<Response> {
  const cfg = await fetchPublicSiteConfig({ revalidate: 3600 });
  // Limit cap: we ask the API for the absolute cap so the renderer
  // can paginate downstream. The Go side may itself cap lower; the
  // renderer reflects what it gets without crashing.
  const posts = await fetchArchive(
    { limit: SITEMAP_MAX_URLS_PER_FILE * 10 },
    { revalidate: 3600 },
  );
  const body = renderSitemapXml(cfg, posts);
  return new Response(body, {
    status: 200,
    headers: {
      'Content-Type': XML_CONTENT_TYPE,
      'Cache-Control': CACHE_CONTROL,
    },
  });
}
