/**
 * Page renderer for @gonext/web.
 *
 * Orchestrates the end-to-end flow used by both the catch-all slug
 * route and the homepage handler:
 *
 *   1. Determine RequestType from the route (singular | home | …).
 *   2. Fetch the post (or archive feed) the route resolves to.
 *   3. Resolve the active theme + template via the API. Both calls
 *      have inline defaults so the renderer is forgiving while the
 *      Go endpoints are still being wired up.
 *   4. Walk the block tree(s) into HTML.
 *   5. Wrap in the theme's header / footer parts.
 *   6. Return the assembled HTML + cache headers + status code.
 *
 * Cache contract (issue acceptance criterion #5):
 *  - Logged-out visitors get a long edge cache:
 *      `Cache-Control: public, s-maxage=300, stale-while-revalidate=86400`
 *  - Logged-in visitors (session cookie detected) bypass the cache:
 *      `Cache-Control: private, no-store`
 *  - 404 pages get a shorter `s-maxage=60` so a freshly-published
 *    slug appears within a minute without flooding the origin.
 *
 * The renderer never throws on a missing endpoint — it returns the
 * 404 path so the surrounding shell still paints.
 */

import { fetchPostBySlug, fetchArchive, fetchResolvedTemplate, type Post } from './api.ts';
import { renderBlocks } from './blocks.ts';
import { resolveActiveTheme } from './theme.ts';

/**
 * Mirrors `packages/go/theme/templates/types.go::RequestType` — the
 * literal strings match Go's `RequestType.String()` so the same value
 * can be forwarded to the resolver query parameter.
 */
export type RequestType =
  | 'singular'
  | 'archive'
  | 'taxonomy'
  | 'author'
  | 'date'
  | 'search'
  | 'home'
  | 'front-page'
  | '404';

/**
 * The catch-all and homepage routes both call this. The return shape
 * is what Next's Response builders need: a string body, a status
 * code, and a Headers map.
 */
export interface RenderResult {
  /** Assembled HTML — header + main + footer. */
  html: string;
  /** Inline `:root { ... }` CSS block emitted by the active theme. */
  css: string;
  /** Document `<title>` value. */
  title: string;
  /** HTTP status code (200 for found, 404 for not-found, etc.). */
  status: number;
  /** Headers to stamp on the response — currently just cache headers. */
  headers: Record<string, string>;
  /**
   * Debug info — surfaced as `X-GoNext-Template` so e2e tests can
   * assert which template basename was resolved.
   */
  templateBasename: string;
}

/**
 * Detect whether the request carries a session cookie that should
 * force a cache bypass. We accept the canonical GoNext session cookie
 * (`gn_session`) and the dev-mode alternative (`gonext_session`); the
 * presence of any non-empty value is enough — we don't validate the
 * token here, the API will reject an invalid one.
 */
export function isAuthenticatedCookie(cookieHeader: string | undefined): boolean {
  if (!cookieHeader) return false;
  return /(?:^|;\s*)(?:gn_session|gonext_session|gn_admin)=[^;]+/i.test(
    cookieHeader,
  );
}

/**
 * Cache header policy. Centralised so we can audit it in one place
 * and so the 404 / archive variants stay consistent.
 */
function cacheHeaders(
  type: RequestType,
  authenticated: boolean,
): Record<string, string> {
  if (authenticated) {
    return { 'Cache-Control': 'private, no-store' };
  }
  if (type === '404') {
    return {
      'Cache-Control':
        'public, s-maxage=60, stale-while-revalidate=300',
    };
  }
  return {
    'Cache-Control':
      'public, s-maxage=300, stale-while-revalidate=86400',
  };
}

/**
 * Compose the final HTML body. Theme parts are HTML strings produced
 * by the Go-side block walker — trusted by construction. Block tree
 * output came out of our own walker (which uses the core blocks'
 * server-render hints, each of which HTML-escapes user input).
 */
function composeBody(headerHtml: string, mainHtml: string, footerHtml: string): string {
  return `${headerHtml}<main class="gn-site-main">${mainHtml}</main>${footerHtml}`;
}

/**
 * HTML-escape — used for the document title / single-post-title slot
 * where the input is a post field rather than already-rendered HTML.
 */
function escapeHtml(value: string): string {
  return value
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

/**
 * Build the post-page main HTML when the Go-side template endpoint
 * isn't reachable. Renders the single-post layout per the Living-Systems
 * handoff: Archivo display headline, Geist 17px body, generous
 * line-height, an optional italic-accent rule on the headline, and a
 * Geist Mono / fg-subtle meta row above the title.
 *
 * The classnames map back to selectors in `globals.css` so the cascade
 * stays predictable; nothing in this output bakes a colour or font
 * literal — every value flows through brand tokens.
 */
function fallbackSingularMain(post: Post, blocksHtml: string): string {
  const dateMeta = post.publishedAt
    ? `<time class="gn-post-meta__date" datetime="${escapeHtml(
        post.publishedAt,
      )}">${escapeHtml(post.publishedAt)}</time>`
    : '';
  const authorMeta = post.authorName
    ? `<span class="gn-post-meta__author">${escapeHtml(post.authorName)}</span>`
    : '';
  const meta =
    dateMeta || authorMeta
      ? `<div class="gn-post-meta">${dateMeta}${authorMeta}</div>`
      : '';
  return [
    '<article class="gn-post">',
    meta,
    `<h1 class="gn-post-title">${escapeHtml(post.title)}</h1>`,
    `<div class="gn-post-content">${blocksHtml}</div>`,
    '</article>',
  ].join('');
}

function fallbackArchiveMain(posts: Post[], heading: string): string {
  const items = posts
    .map((p) => {
      const link = `/${encodeURIComponent(p.slug)}`;
      const date = p.publishedAt
        ? `<time datetime="${escapeHtml(p.publishedAt)}">${escapeHtml(
            p.publishedAt,
          )}</time>`
        : '';
      const excerpt = p.excerpt
        ? `<p class="gn-archive-excerpt">${escapeHtml(p.excerpt)}</p>`
        : '';
      return [
        '<li class="gn-archive-item">',
        `<h2 class="gn-archive-title"><a href="${link}">${escapeHtml(p.title)}</a></h2>`,
        date,
        excerpt,
        '</li>',
      ].join('');
    })
    .join('');
  const empty = posts.length === 0 ? '<p>No posts yet.</p>' : '';
  return [
    `<h1 class="gn-archive-heading">${escapeHtml(heading)}</h1>`,
    `<ul class="gn-archive-list">${items}</ul>`,
    empty,
  ].join('');
}

function fallback404Main(): string {
  // The italic-accent rule fires on the second word of the headline,
  // matching the brand voice ("confident, quiet, alive"). The status
  // code is surfaced as a small eyebrow above the headline so it's
  // visible without crowding the Archivo display type.
  return [
    '<section class="gn-404">',
    '<div class="gn-404__eyebrow">404</div>',
    '<h1>Page <em>not</em> found.</h1>',
    '<p>The page you were looking for has moved or never existed.</p>',
    '<p><a href="/">Return home →</a></p>',
    '</section>',
  ].join('');
}

/**
 * Render a singular post by slug. Returns a 404 result when the post
 * isn't found.
 */
export async function renderSingular(
  slug: string,
  options: { cookie?: string } = {},
): Promise<RenderResult> {
  const authenticated = isAuthenticatedCookie(options.cookie);
  // Authenticated visitors bypass the data cache too — they may be
  // previewing an unpublished revision.
  const revalidate = authenticated ? undefined : 300;

  const post = await fetchPostBySlug(slug, {
    cookie: options.cookie,
    revalidate,
  });
  if (!post) {
    return renderNotFound({ cookie: options.cookie });
  }

  const theme = await resolveActiveTheme({ revalidate });
  const blocksHtml = renderBlocks(post.blocks);

  const resolved = await fetchResolvedTemplate(
    {
      type: 'singular',
      postType: post.postType,
      postSlug: post.slug,
      postId: post.id,
    },
    { revalidate },
  );

  // Splice the rendered block tree into the template's post-content
  // slot. When the Go endpoint isn't available we fall back to a
  // hand-assembled main region that mirrors gn-hello/single.html.
  const mainHtml = resolved?.mainHtml
    ? resolved.mainHtml
        .replace('<!--gn:post-title-->', escapeHtml(post.title))
        .replace('<!--gn:post-content-->', blocksHtml)
    : fallbackSingularMain(post, blocksHtml);

  return {
    html: composeBody(theme.headerHtml, mainHtml, theme.footerHtml),
    css: theme.cssCustomProperties,
    title: post.title,
    status: 200,
    headers: cacheHeaders('singular', authenticated),
    templateBasename: resolved?.basename ?? 'single.fallback',
  };
}

/**
 * Render an archive / blog-home page.
 */
export async function renderArchive(
  options: {
    cookie?: string;
    postType?: string;
    heading?: string;
    type?: Extract<RequestType, 'home' | 'archive' | 'front-page'>;
  } = {},
): Promise<RenderResult> {
  const authenticated = isAuthenticatedCookie(options.cookie);
  const revalidate = authenticated ? undefined : 300;
  const type = options.type ?? 'home';

  const [posts, theme, resolved] = await Promise.all([
    fetchArchive(
      { postType: options.postType, limit: 10 },
      { revalidate },
    ),
    resolveActiveTheme({ revalidate }),
    fetchResolvedTemplate({ type, postType: options.postType }, { revalidate }),
  ]);

  const heading = options.heading ?? (type === 'home' ? 'Latest posts' : 'Archive');
  const fallbackMain = fallbackArchiveMain(posts, heading);
  const mainHtml = resolved?.mainHtml
    ? resolved.mainHtml.replace('<!--gn:archive-list-->', fallbackMain)
    : fallbackMain;

  return {
    html: composeBody(theme.headerHtml, mainHtml, theme.footerHtml),
    css: theme.cssCustomProperties,
    title: heading,
    status: 200,
    headers: cacheHeaders(type, authenticated),
    templateBasename: resolved?.basename ?? `${type}.fallback`,
  };
}

/**
 * Render the 404 template. Always returns status 404; the catch-all
 * route forwards this when a slug fetch comes back null.
 */
export async function renderNotFound(
  options: { cookie?: string } = {},
): Promise<RenderResult> {
  const authenticated = isAuthenticatedCookie(options.cookie);
  const revalidate = authenticated ? undefined : 60;
  const theme = await resolveActiveTheme({ revalidate });
  const resolved = await fetchResolvedTemplate({ type: '404' }, { revalidate });
  const mainHtml = resolved?.mainHtml ?? fallback404Main();

  return {
    html: composeBody(theme.headerHtml, mainHtml, theme.footerHtml),
    css: theme.cssCustomProperties,
    title: 'Not found',
    status: 404,
    headers: cacheHeaders('404', authenticated),
    templateBasename: resolved?.basename ?? '404.fallback',
  };
}
