/**
 * Catch-all public-site route.
 *
 * Matches every path that isn't owned by a more-specific route file.
 * Now that `author/[slug]`, `category/[slug]`, `tag/[slug]`, and the
 * `[year]` / `[year]/[month]` / `[year]/[month]/[day]` date archives
 * exist, Next's App Router precedence reserves this handler for paths
 * of four or more segments (single-, two- and three-segment paths are
 * served by the date routes, which themselves fall through to
 * `renderSingular` when their segments aren't date-shaped).
 *
 * The params shape is `slug: string[]` — Next gives us the path
 * segments unencoded, which we collapse back into a single slug
 * string the API understands.
 *
 * Flow:
 *
 *   1. Read the session cookie (logged-in visitors bypass the cache).
 *   2. Join the slug segments and try `renderSingular`. The renderer
 *      handles its own 404 fallback; we promote 404 status into a
 *      Next 404 response so error-recovery middleware downstream
 *      sees the right signal.
 *   3. Stamp cache headers + the template debug header.
 *
 * The handler is `dynamic = 'force-dynamic'` so the session-cookie
 * check actually runs per request. Pages are still edge-cached via
 * the `Cache-Control` header set by `renderSingular` — Next's data
 * cache caches the upstream API responses, the CDN caches the
 * rendered HTML.
 */
import { cookies, headers } from 'next/headers';
import type { Metadata } from 'next';
import type { ReactElement } from 'react';
import { renderSingular, renderNotFound, isAuthenticatedCookie } from '@/lib/render';
import { fetchPostBySlug, fetchPostComments, apiBaseUrl } from '@/lib/api';
import { CommentsThread } from '@/components/comments/CommentsThread';
import { PublicShell } from '../PublicShell';

export const dynamic = 'force-dynamic';

interface CatchAllParams {
  slug: string[];
}

/**
 * Join the path segments into the slug the API expects. Multi-segment
 * paths (e.g. `/2026/05/post-name`) collapse into a single slug with
 * `/` separators preserved — the Go side uses the trailing component
 * as the lookup key.
 */
function joinSlug(segments: string[] | undefined): string {
  if (!segments || segments.length === 0) return '';
  return segments.map((s) => decodeURIComponent(s)).join('/');
}

async function readCookieHeader(): Promise<string> {
  // `cookies()` is the App Router primitive for reading the request
  // cookie jar. We rebuild the raw header so the api-client can
  // forward it verbatim to the upstream API.
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
 * Per-page metadata. Pre-fetches the slug to derive the document
 * title; Next caches this together with the page body so the upstream
 * fetch only fires once.
 */
export async function generateMetadata(
  { params }: { params: Promise<CatchAllParams> },
): Promise<Metadata> {
  const { slug } = await params;
  const joined = joinSlug(slug);
  if (!joined) return { title: 'Not found' };
  // Use the same render path so any title transformation stays
  // consistent. The renderer is cheap — fetches are deduped within
  // the same request.
  const cookie = await readCookieHeader();
  const result = await renderSingular(joined, { cookie });
  return { title: result.title };
}

/**
 * Stamp the response headers Next gives us a single shot at in App
 * Router server components. We use `headers()`'s mutable response
 * write surface (the unstable API in 15.x); the canonical path will
 * shift to Route Handlers once the API stabilises.
 *
 * For now we surface the template basename via a `<meta>` tag in the
 * shell (which works regardless of the response-header API state) so
 * tests have a stable assertion target.
 */
async function stampCacheHeaders(
  cacheControl: string,
  templateBasename: string,
): Promise<void> {
  // `headers()` is read-only in server components. The actual cache
  // header is set by middleware in production (mirrored by the
  // reverse proxy). We document the intent here so future Next
  // versions that expose a response-header write API can flip the
  // implementation without touching the render flow.
  void cacheControl;
  void templateBasename;
  // Sanity-touch headers() so the page is correctly marked dynamic
  // when a session cookie is present — same trick the admin uses.
  try {
    await headers();
  } catch {
    /* no-op outside request scope */
  }
}

/**
 * `comments_open` may be carried on the post (a future
 * post-metadata field) or omitted — defaults to true. We honour
 * an explicit `false` and never display the form in that case.
 */
function commentsOpenFromPost(raw: unknown): boolean {
  if (!raw || typeof raw !== 'object') return true;
  const r = raw as Record<string, unknown>;
  if (r.commentsOpen === false) return false;
  if (typeof r.commentsOpen === 'object' && r.commentsOpen !== null) {
    return true;
  }
  if (r.comments_open === false) return false;
  return true;
}

export default async function CatchAllSlugPage(
  { params }: { params: Promise<CatchAllParams> },
): Promise<ReactElement> {
  const { slug } = await params;
  const joined = joinSlug(slug);
  const cookie = await readCookieHeader();

  const result = joined
    ? await renderSingular(joined, { cookie })
    : await renderNotFound({ cookie });

  if (result.status === 404) {
    // The 404 page is rendered from the same shell so the visitor
    // still sees the theme chrome. We don't call Next's `notFound()`
    // because that would bypass the theme parts.
    await stampCacheHeaders(
      result.headers['Cache-Control'] ?? '',
      result.templateBasename,
    );
    return (
      <PublicShell
        bodyHtml={result.html}
        cssCustomProperties={result.css}
        templateBasename={result.templateBasename}
      />
    );
  }

  await stampCacheHeaders(
    result.headers['Cache-Control'] ?? '',
    result.templateBasename,
  );

  // Inject the comments surface when the resolved post is a `post`
  // (not a `page`) and the post hasn't been marked comments-closed.
  // We re-fetch the post here so we have the id + postType + the
  // forward-compatible comments_open flag. The fetch is deduped by
  // Next's data cache within the same request, so this is free.
  let commentsBlock: ReactElement | null = null;
  if (joined) {
    const post = await fetchPostBySlug(joined, {
      cookie,
      revalidate: isAuthenticatedCookie(cookie) ? undefined : 300,
    });
    if (post && post.postType === 'post') {
      const open = commentsOpenFromPost(post);
      const initial = open
        ? await fetchPostComments(post.id, {
            revalidate: isAuthenticatedCookie(cookie) ? undefined : 60,
          })
        : [];
      commentsBlock = (
        <CommentsThread
          postId={post.id}
          apiBaseUrl={apiBaseUrl}
          initialComments={initial}
          isAuthenticated={isAuthenticatedCookie(cookie)}
          commentsOpen={open}
        />
      );
    }
  }

  return (
    <PublicShell
      bodyHtml={result.html}
      cssCustomProperties={result.css}
      templateBasename={result.templateBasename}
    >
      {commentsBlock}
    </PublicShell>
  );
}
