/**
 * GET /robots.txt
 *
 * Two-state robots.txt driven by the public-site config:
 *
 *  - Production-shaped deployments (`allowIndex=true`) emit the
 *    crawl-everything convention plus a Sitemap line pointing at
 *    `/sitemap.xml`. This is the canonical public-site shape.
 *  - Staging / preview / dev (`allowIndex=false`) emit
 *    `User-agent: * / Disallow: /` and omit the Sitemap line. Crawlers
 *    that honour the directive stay out; crawlers that ignore it
 *    aren't handed a content URL list either.
 *
 * The body is served as `text/plain` per the de-facto convention
 * (the spec itself is silent on Content-Type but search engines
 * uniformly accept text/plain). Same one-hour s-maxage as the other
 * discoverability surfaces.
 */
import { buildRobotsTxt } from '@/lib/feeds';
import { fetchPublicSiteConfig } from '@/lib/api';

export const dynamic = 'force-dynamic';

const TEXT_CONTENT_TYPE = 'text/plain; charset=utf-8';
const CACHE_CONTROL = 'public, s-maxage=3600, stale-while-revalidate=86400';

export async function GET(): Promise<Response> {
  const cfg = await fetchPublicSiteConfig({ revalidate: 3600 });
  const sitemapUrl =
    cfg.baseUrl && cfg.allowIndex ? `${cfg.baseUrl}/sitemap.xml` : undefined;
  const body = buildRobotsTxt({
    allowIndex: cfg.allowIndex,
    sitemapUrl,
  });
  return new Response(body, {
    status: 200,
    headers: {
      'Content-Type': TEXT_CONTENT_TYPE,
      'Cache-Control': CACHE_CONTROL,
    },
  });
}
