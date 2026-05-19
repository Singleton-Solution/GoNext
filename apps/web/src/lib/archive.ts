/**
 * Shared archive renderer for the author / category / tag / date routes.
 *
 * Each of those routes resolves a different filter (author slug, term
 * slug, year/month/day) but otherwise paints the same envelope: a
 * heading, a list of post permalinks, and a "next / previous page"
 * pair of links when paginating. Centralising the flow here keeps the
 * route handlers thin and ensures the cache headers + template
 * resolution stay consistent across all four archive types.
 *
 * Flow per route:
 *
 *   1. The route validates its input (slug present, year is a 4-digit
 *      number, etc.). Bad input means a Next 404 *before* we get here.
 *   2. The route fetches the term / author / date and calls
 *      `renderArchiveBundle` with the resolved metadata + filter.
 *   3. We fetch the paginated post slice, the active theme, and the
 *      chosen template basename in parallel — same parallel-fetch
 *      pattern `renderSingular` uses, so the per-request fanout stays
 *      flat regardless of which archive variant the visitor hit.
 *   4. We compose the body the same way `renderArchive` does, but with
 *      the variant-specific heading + an optional pagination block
 *      that links to the next / previous page.
 *
 * Cache contract mirrors `render.ts`:
 *
 *  - Logged-out visitors get the long edge cache.
 *  - Logged-in visitors (session cookie) get `private, no-store` so
 *    they see drafts / unpublished revisions.
 *  - Resolved-template lookup uses the same revalidate window as the
 *    archive feed so the two layers stay in sync.
 */

import {
  fetchArchivePage,
  fetchResolvedTemplate,
  type Post,
} from './api.ts';
import {
  isAuthenticatedCookie,
  type RenderResult,
  type RequestType,
} from './render.ts';
import { resolveActiveTheme } from './theme.ts';

/**
 * Page size for archive listings. WordPress defaults to ten posts per
 * page; we match that so a freshly-migrated site behaves identically.
 * Routes can override via the call options if they need a different
 * pagination shape (e.g. a year archive that wants more density).
 */
export const DEFAULT_ARCHIVE_PER_PAGE = 10;

/**
 * Request shape for `renderArchiveBundle`. Each variant route fills
 * in the fields it knows about; the others stay undefined and the
 * resolver / fetcher silently skip the corresponding precedence-list
 * candidates and query parameters.
 */
export interface ArchiveBundleRequest {
  /**
   * The RequestType to forward to the template resolver. One of
   * "author" | "taxonomy" | "date" — the renderer doesn't enforce
   * which combinations are valid; the caller knows.
   */
  type: Extract<RequestType, 'author' | 'taxonomy' | 'date' | 'archive'>;

  /** Visible heading at the top of the archive (e.g. "Posts by Jane"). */
  heading: string;

  /** Optional description sourced from the term / author profile. */
  description?: string;

  /** Base path the route lives at (e.g. `/author/jane`). Used to build
   *  the `?page=N` pagination URLs. Must not include a trailing slash. */
  basePath: string;

  /** 1-based page number from the request query string. */
  page: number;

  /** Author archive: the author's URL slug. */
  authorSlug?: string;
  /** Author archive: stringified numeric ID — used by the resolver. */
  authorId?: string;

  /** Taxonomy archive: the taxonomy slug (e.g. "category"). */
  taxonomy?: string;
  /** Taxonomy archive: the term slug (e.g. "news"). */
  termSlug?: string;
  /** Taxonomy archive: stringified term ID — currently unused by the
   *  resolver but forwarded for completeness so an id-suffixed template
   *  (`taxonomy-category-42.tsx`) can be wired without an API change. */
  termId?: string;

  /** Date archive: 4-digit year. */
  year?: number;
  /** Date archive: 1-12 month. */
  month?: number;
  /** Date archive: 1-31 day. */
  day?: number;

  /** Forwarded cookie header for cache-bypass detection. */
  cookie?: string;

  /** Per-page override — defaults to DEFAULT_ARCHIVE_PER_PAGE. */
  perPage?: number;
}

/**
 * HTML-escape — same surface as `render.ts`. Kept inline rather than
 * exported from render.ts to avoid an import cycle.
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
 * Cache header policy. Centralised so all four archive variants stay
 * in sync; mirrors `render.ts::cacheHeaders` for the non-404 paths.
 */
function cacheHeaders(authenticated: boolean): Record<string, string> {
  if (authenticated) {
    return { 'Cache-Control': 'private, no-store' };
  }
  return {
    'Cache-Control': 'public, s-maxage=300, stale-while-revalidate=86400',
  };
}

/**
 * Build the standard archive list — heading, optional description, the
 * `<ul>` of post permalinks, and a pagination block when needed. The
 * theme template can splice this in via `<!--gn:archive-list-->` (same
 * marker as the home/archive route uses).
 */
function buildArchiveMain(
  posts: Post[],
  heading: string,
  description: string | undefined,
  pagination: string,
): string {
  const items = posts
    .map((p) => {
      const link = `/${encodeURIComponent(p.slug)}`;
      const date = p.publishedAt
        ? `<time datetime="${escapeHtml(p.publishedAt)}">${escapeHtml(p.publishedAt)}</time>`
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
  const empty = posts.length === 0 ? '<p class="gn-archive-empty">No posts yet.</p>' : '';
  const desc = description
    ? `<p class="gn-archive-description">${escapeHtml(description)}</p>`
    : '';
  return [
    `<h1 class="gn-archive-heading">${escapeHtml(heading)}</h1>`,
    desc,
    `<ul class="gn-archive-list">${items}</ul>`,
    empty,
    pagination,
  ].join('');
}

/**
 * Build the pagination footer. We emit two anchors with stable class
 * names so themes can style them and e2e tests can assert on the
 * href values. The previous link only renders for page > 1; the next
 * link only renders when there's another page of results.
 *
 * URLs preserve the route's basePath so deep-links to e.g.
 * `/author/jane?page=2` round-trip cleanly. We intentionally use the
 * query-string form (rather than `/page/2`) so the same handler can
 * service all pages without needing extra route segments — the
 * `[year]/page/N` ladder is a future enhancement.
 */
function buildPagination(
  basePath: string,
  page: number,
  perPage: number,
  total: number,
): string {
  const totalPages = perPage > 0 ? Math.ceil(total / perPage) : 1;
  if (totalPages <= 1) return '';

  const prevHref =
    page > 1
      ? `${basePath}${page - 1 === 1 ? '' : `?page=${page - 1}`}`
      : null;
  const nextHref = page < totalPages ? `${basePath}?page=${page + 1}` : null;

  const parts: string[] = [];
  if (prevHref !== null) {
    parts.push(
      `<a class="gn-archive-prev" rel="prev" href="${escapeHtml(prevHref)}">Newer posts</a>`,
    );
  }
  if (nextHref !== null) {
    parts.push(
      `<a class="gn-archive-next" rel="next" href="${escapeHtml(nextHref)}">Older posts</a>`,
    );
  }
  if (parts.length === 0) return '';
  return `<nav class="gn-archive-pagination" aria-label="Archive pagination">${parts.join('')}</nav>`;
}

/**
 * Compose the final HTML body — header, main region, footer. Mirrors
 * the helper in render.ts but kept inline to avoid an import cycle.
 */
function composeBody(headerHtml: string, mainHtml: string, footerHtml: string): string {
  return `${headerHtml}<main class="gn-site-main">${mainHtml}</main>${footerHtml}`;
}

/**
 * Run the parallel fetches an archive page needs and assemble the
 * final render result.
 *
 * Returns a 200 result even when the post list is empty — an empty
 * archive isn't a 404, it just paints the "no posts yet" empty state.
 * Routes that detect a missing author / term / invalid date upstream
 * are expected to call `renderNotFound` themselves before getting
 * here.
 */
export async function renderArchiveBundle(
  req: ArchiveBundleRequest,
): Promise<RenderResult> {
  const authenticated = isAuthenticatedCookie(req.cookie);
  const revalidate = authenticated ? undefined : 300;
  const perPage = req.perPage ?? DEFAULT_ARCHIVE_PER_PAGE;

  const [pageResult, theme, resolved] = await Promise.all([
    fetchArchivePage(
      {
        limit: perPage,
        page: req.page,
        authorSlug: req.authorSlug,
        authorId: req.authorId,
        termSlug: req.termSlug,
        taxonomy: req.taxonomy,
        year: req.year,
        month: req.month,
        day: req.day,
      },
      { revalidate, cookie: req.cookie },
    ),
    resolveActiveTheme({ revalidate }),
    fetchResolvedTemplate(
      {
        type: req.type,
        // Forward the slug-shaped fields the resolver consumes. The
        // resolver itself decides which candidates to build based on
        // `type`; the others are silently ignored.
        postSlug: req.authorSlug ?? '',
        authorId: req.authorId ?? '',
        taxonomySlug: req.taxonomy ?? '',
        termSlug: req.termSlug ?? '',
        termId: req.termId ?? '',
      },
      { revalidate },
    ),
  ]);

  const pagination = buildPagination(
    req.basePath,
    pageResult.page,
    pageResult.perPage,
    pageResult.total,
  );
  const fallbackMain = buildArchiveMain(
    pageResult.posts,
    req.heading,
    req.description,
    pagination,
  );
  const mainHtml = resolved?.mainHtml
    ? resolved.mainHtml.replace('<!--gn:archive-list-->', fallbackMain)
    : fallbackMain;

  return {
    html: composeBody(theme.headerHtml, mainHtml, theme.footerHtml),
    css: theme.cssCustomProperties,
    title: req.heading,
    status: 200,
    headers: cacheHeaders(authenticated),
    templateBasename: resolved?.basename ?? `${req.type}.fallback`,
  };
}

/**
 * Parse a positive-integer query param (e.g. `?page=2`). Returns 1
 * for missing / non-numeric / zero / negative input so the caller can
 * unconditionally use the result as a 1-based page number. We clamp
 * absurdly large values to 10_000 — that's still far past anything a
 * real archive would surface, but it caps the worst-case query plan.
 */
export function parsePageParam(raw: string | string[] | undefined): number {
  const value = Array.isArray(raw) ? raw[0] : raw;
  if (!value) return 1;
  const n = Number.parseInt(value, 10);
  if (!Number.isFinite(n) || n < 1) return 1;
  if (n > 10000) return 10000;
  return n;
}

/**
 * Validate a 4-digit year. Returns the parsed integer or `null` for
 * anything that isn't exactly four digits in the range 1000–9999. We
 * intentionally accept any 4-digit value (not just the current
 * millennium) so historical migrations land cleanly; nonsense values
 * (`"abc"`, `"99"`, `"99999"`) return null so the route handler can
 * promote that to a Next 404 rather than a 500.
 */
export function validateYear(raw: string | undefined): number | null {
  if (!raw || !/^\d{4}$/.test(raw)) return null;
  const n = Number.parseInt(raw, 10);
  if (n < 1000 || n > 9999) return null;
  return n;
}

/**
 * Validate a 1-12 month. Accepts both `"1"` and `"01"` because
 * legitimate WordPress permalinks use the zero-padded form; the
 * leading-zero variant is then normalised to the bare integer so the
 * Go-side query gets a stable shape.
 */
export function validateMonth(raw: string | undefined): number | null {
  if (!raw || !/^\d{1,2}$/.test(raw)) return null;
  const n = Number.parseInt(raw, 10);
  if (n < 1 || n > 12) return null;
  return n;
}

/**
 * Validate a 1-31 day. We do NOT enforce per-month day-count limits
 * here — Feb 30 is rare enough as a typo that round-tripping through
 * the API (which already validates against the real calendar) is the
 * simpler implementation; the API responds with an empty page and the
 * route paints the empty state. Catching nonsense formats (`"abc"`,
 * `"32"`) at the route layer still returns 404 cleanly.
 */
export function validateDay(raw: string | undefined): number | null {
  if (!raw || !/^\d{1,2}$/.test(raw)) return null;
  const n = Number.parseInt(raw, 10);
  if (n < 1 || n > 31) return null;
  return n;
}

/**
 * Format a date archive heading. Examples:
 *
 *   formatDateHeading(2026)              => "Archive: 2026"
 *   formatDateHeading(2026, 5)           => "Archive: May 2026"
 *   formatDateHeading(2026, 5, 19)       => "Archive: May 19, 2026"
 *
 * We use English month names because the renderer doesn't currently
 * resolve a locale; once site settings expose one this becomes
 * `Intl.DateTimeFormat`.
 */
const MONTH_NAMES = [
  'January',
  'February',
  'March',
  'April',
  'May',
  'June',
  'July',
  'August',
  'September',
  'October',
  'November',
  'December',
];

export function formatDateHeading(year: number, month?: number, day?: number): string {
  if (month === undefined) return `Archive: ${year}`;
  const monthName = MONTH_NAMES[month - 1] ?? String(month);
  if (day === undefined) return `Archive: ${monthName} ${year}`;
  return `Archive: ${monthName} ${day}, ${year}`;
}
