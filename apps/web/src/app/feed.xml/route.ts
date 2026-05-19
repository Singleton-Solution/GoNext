/**
 * GET /feed.xml
 *
 * Atom 1.0 feed of the 20 most recently published posts. The 20-item
 * cap is the conventional default in the feed-reader ecosystem
 * (WordPress, Jekyll, Hugo all pick numbers in this range) — it
 * balances "enough to populate a freshly-subscribed reader" against
 * "don't ship a megabyte of HTML to every poll request."
 *
 * Cache contract: same one-hour s-maxage as the sitemap. A new post
 * propagates to subscribers within the hour without flooding the
 * origin.
 *
 * The route shares `renderFeedXml` (exported) with the per-category
 * route so the two stay behaviourally identical except for the
 * filter.
 */
import { buildAtomFeed, type FeedEntry } from '@/lib/feeds';
import {
  fetchArchive,
  fetchPublicSiteConfig,
  type Post,
  type PublicSiteConfig,
} from '@/lib/api';
import { renderBlocks } from '@/lib/blocks';

export const dynamic = 'force-dynamic';

const ATOM_CONTENT_TYPE = 'application/atom+xml; charset=utf-8';
const CACHE_CONTROL = 'public, s-maxage=3600, stale-while-revalidate=86400';

/** Conventional feed length. Search the file for "20" to retune. */
export const FEED_ITEM_LIMIT = 20;

/**
 * Build the canonical permalink for a feed entry. Mirrors the
 * sitemap route's `postUrl` — kept independent (rather than
 * imported) so each route's URL convention can diverge if a future
 * post-type-specific prefix lands.
 */
function postUrl(baseUrl: string, post: Post): string {
  return `${baseUrl}/${post.slug}`;
}

/**
 * Project a Post into a FeedEntry. The Atom spec requires `updated`;
 * we use `publishedAt` because the API doesn't yet expose a
 * separate "last edit" timestamp. When that lands we can pass it
 * through directly.
 *
 * `contentHtml` is built by re-running the block walker — a small
 * cost per request and the simplest way to keep the feed in lockstep
 * with the rendered page. The walker is pure, so the cost is bounded
 * by the post's block count.
 */
export function postToFeedEntry(baseUrl: string, post: Post): FeedEntry {
  const link = postUrl(baseUrl, post);
  // `id` must be globally stable. Using the permalink ties feed
  // identity to URL, which is the convention every reader expects.
  // If a slug changes, the entry registers as a new item — which is
  // arguably the right behaviour (a renamed post _is_ a new URL).
  const updated = post.publishedAt ?? new Date(0).toISOString();
  const entry: FeedEntry = {
    id: link,
    title: post.title,
    link,
    updated,
  };
  if (post.publishedAt) entry.published = post.publishedAt;
  if (post.authorName) entry.authorName = post.authorName;
  if (post.excerpt) entry.summary = post.excerpt;
  if (post.blocks && post.blocks.length > 0) {
    entry.contentHtml = renderBlocks(post.blocks);
  }
  return entry;
}

/**
 * Sort posts newest-first by publishedAt. Posts without a publish
 * date sort to the end — they're either drafts that leaked into the
 * archive feed (an API bug we want to surface, not hide) or future-
 * dated entries that the archive shouldn't have returned. Either
 * way, sorting them last keeps the visible feed sensible.
 */
export function sortByPublishedDesc(posts: Post[]): Post[] {
  return [...posts].sort((a, b) => {
    const at = a.publishedAt ?? '';
    const bt = b.publishedAt ?? '';
    if (at === bt) return 0;
    return at < bt ? 1 : -1;
  });
}

/**
 * Pure render function shared by /feed.xml and /feed/[category].
 * Takes the public-site config + already-fetched posts + feed
 * metadata, produces the byte-stable XML body.
 */
export function renderFeedXml(args: {
  cfg: PublicSiteConfig;
  posts: Post[];
  feedPath: string;
  title: string;
  subtitle?: string;
  limit?: number;
}): string {
  const limit = args.limit ?? FEED_ITEM_LIMIT;
  const sorted = sortByPublishedDesc(args.posts).slice(0, limit);
  const baseUrl = args.cfg.baseUrl || '';
  const feedUrl = `${baseUrl}${args.feedPath}`;
  const siteUrl = baseUrl || feedUrl;
  // `updated` reflects the most recent entry. If the feed is empty,
  // fall back to the unix epoch — same convention as `postToFeedEntry`.
  const updated = sorted[0]?.publishedAt ?? new Date(0).toISOString();
  return buildAtomFeed({
    id: feedUrl,
    title: args.title,
    subtitle: args.subtitle,
    siteUrl,
    feedUrl,
    updated,
    entries: sorted.map((p) => postToFeedEntry(baseUrl, p)),
  });
}

export async function GET(): Promise<Response> {
  const cfg = await fetchPublicSiteConfig({ revalidate: 3600 });
  // Over-fetch a little so the sort + slice can pick the genuinely
  // most recent 20 even if the API didn't pre-sort. The limit here
  // is generous; the API caps internally.
  const posts = await fetchArchive(
    { limit: FEED_ITEM_LIMIT * 2 },
    { revalidate: 3600 },
  );
  const body = renderFeedXml({
    cfg,
    posts,
    feedPath: '/feed.xml',
    title: 'Recent posts',
  });
  return new Response(body, {
    status: 200,
    headers: {
      'Content-Type': ATOM_CONTENT_TYPE,
      'Cache-Control': CACHE_CONTROL,
    },
  });
}
