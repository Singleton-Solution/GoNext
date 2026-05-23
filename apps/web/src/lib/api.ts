/**
 * @gonext/web — typed API client.
 *
 * Server-side fetch helpers for the GoNext REST surface. Three reasons
 * to ship a dedicated client here rather than reusing apps/admin's:
 *
 *  1. The public renderer never carries a session cookie (logged-out
 *     visitors are the vast majority of traffic) — `credentials:
 *     'include'` is the wrong default. We forward an explicit
 *     `Cookie` header only when the caller has one in hand (e.g. the
 *     catch-all route detected a logged-in visitor and is bypassing
 *     the cache).
 *  2. The cache shape is different. Admin pages always go `cache:
 *     'no-store'`; the public site goes `next: { revalidate: ... }`
 *     so the renderer can lean on Next's ISR layer.
 *  3. The endpoints we hit are public-facing (posts/by-slug, themes/
 *     active, etc.) and we want typed return shapes at the call site.
 *
 * Endpoints used here:
 *  - `GET /api/v1/posts/by-slug/{slug}`             — fetch by slug
 *  - `GET /api/v1/posts?status=published&limit=...` — archive feed
 *  - `GET /api/v1/themes/active`                    — active theme
 *  - `GET /api/v1/themes/active/template?...`       — chosen template
 *
 * The endpoints may not all exist on `main` when this lands — every
 * call site treats network/404 failures as "no data" and renders the
 * 404 path. Once the Go side ships these endpoints the renderer
 * starts returning real data with no code change.
 */

import type { Block } from '@gonext/blocks-sdk';

/** Default API base — overridable via `NEXT_PUBLIC_API_URL`. */
const DEFAULT_BASE_URL = 'http://localhost:8080';

export const apiBaseUrl: string =
  (typeof process !== 'undefined' && process.env.NEXT_PUBLIC_API_URL) ||
  DEFAULT_BASE_URL;

/**
 * The minimal post shape the renderer needs. The API may return more
 * fields; this is the contract the renderer relies on. Defensive
 * parsing in `fetchPostBySlug` strips anything else.
 */
export interface Post {
  /** Stable identifier — used for revalidation tags. */
  id: string;
  /** URL slug, unique within post type. */
  slug: string;
  /** Human-readable title, rendered into the post-title slot. */
  title: string;
  /** Optional excerpt — used by archive listings and og:description. */
  excerpt?: string;
  /** Post type, e.g. "post", "page". Drives template precedence. */
  postType: string;
  /** Author display name (used by post-meta). */
  authorName?: string;
  /** ISO-8601 publish timestamp. */
  publishedAt?: string;
  /** The canonical block tree; the walker turns this into HTML. */
  blocks: Block[];
}

/**
 * Minimal author shape used by the author archive route. We only
 * surface the public-safe fields here — the wp-json/users projection
 * already drops email / roles / capabilities for unauthenticated
 * callers, and we further trim to what the renderer paints.
 */
export interface Author {
  /** Stable identifier — used for revalidation tags + author-{id} template. */
  id: string;
  /** URL slug, unique within the user table. */
  slug: string;
  /** Display name (used as the archive heading). */
  name: string;
  /** Optional bio / description shown above the post list. */
  description?: string;
}

/**
 * Minimal term shape used by category / tag archive routes. The Go
 * side stores the taxonomy on the term itself; we forward that so the
 * route can decide which template hierarchy to walk.
 */
export interface Term {
  /** Stable identifier — used for revalidation tags. */
  id: string;
  /** URL slug, unique within the taxonomy. */
  slug: string;
  /** Human-readable display name. */
  name: string;
  /** Taxonomy slug — e.g. "category", "post_tag". */
  taxonomy: string;
  /** Optional description shown above the post list. */
  description?: string;
}

/**
 * Active-theme summary. We deliberately keep this narrow: the Go side
 * already resolved which theme is active and emitted the CSS custom
 * properties; we only need the bits the renderer mixes into the HTML.
 */
export interface ActiveTheme {
  /** Theme slug, e.g. "gn-hello". */
  slug: string;
  /** Display name (used in default og:site_name). */
  title: string;
  /** Pre-emitted `:root { ... }` block, ready to drop into a `<style>`. */
  cssCustomProperties: string;
  /** Header part HTML — already block-walked on the Go side. */
  headerHtml: string;
  /** Footer part HTML — same. */
  footerHtml: string;
}

/**
 * Resolver result: the basename of the template chosen for this
 * request. The renderer doesn't load the file directly — the Go side
 * already block-walked it and returns the HTML in `mainHtml`. We keep
 * the basename so debug headers / e2e snapshots can assert it.
 */
export interface ResolvedTemplate {
  /** Basename, e.g. "single.html", "archive-book.tsx". */
  basename: string;
  /** Server-rendered template body — the bit between header + footer. */
  mainHtml: string;
}

/** Typed error mirroring apps/admin/api-client. */
export class ApiError extends Error {
  public readonly status: number;
  public readonly payload: unknown;
  constructor(status: number, payload: unknown, message?: string) {
    super(message ?? `API error ${status}`);
    this.name = 'ApiError';
    this.status = status;
    this.payload = payload;
  }
}

function joinUrl(base: string, path: string): string {
  const left = base.endsWith('/') ? base.slice(0, -1) : base;
  const right = path.startsWith('/') ? path : `/${path}`;
  return `${left}${right}`;
}

/**
 * Internal fetch wrapper. Handles JSON decoding, treats 404 as null
 * (not an error — common path for the catch-all route), and surfaces
 * other non-2xx as ApiError.
 */
async function getJson<T>(
  path: string,
  init: { cookie?: string; revalidate?: number } = {},
): Promise<T | null> {
  const headers: Record<string, string> = { Accept: 'application/json' };
  if (init.cookie) {
    headers.Cookie = init.cookie;
  }

  let res: Response;
  try {
    res = await fetch(joinUrl(apiBaseUrl, path), {
      method: 'GET',
      headers,
      // Server-to-server; the browser-only credentials mode is moot.
      // When the caller passes a revalidate window we hand it to
      // Next's data cache; otherwise fall back to no-store so the
      // catch-all can still service authenticated visitors safely.
      ...(init.revalidate !== undefined
        ? { next: { revalidate: init.revalidate } }
        : { cache: 'no-store' as RequestCache }),
    });
  } catch (err) {
    // Network failure — treat as "endpoint unavailable". The renderer
    // shows the 404 path so the visitor still gets a structured page.
    throw new ApiError(0, undefined, err instanceof Error ? err.message : 'network error');
  }

  if (res.status === 404) {
    return null;
  }
  if (!res.ok) {
    let payload: unknown = undefined;
    try {
      payload = await res.json();
    } catch {
      payload = await res.text().catch(() => undefined);
    }
    throw new ApiError(res.status, payload);
  }
  // 204 etc. — treat as null to keep call sites uniform.
  if (res.status === 204) {
    return null;
  }
  return (await res.json()) as T;
}

/**
 * Defensive parse. The Go API contract is still evolving; we only
 * trust the fields the renderer reads and drop the rest. Returning
 * `null` triggers the 404 path upstream.
 */
function asPost(raw: unknown): Post | null {
  if (!raw || typeof raw !== 'object') return null;
  const r = raw as Record<string, unknown>;
  const id = typeof r.id === 'string' ? r.id : null;
  const slug = typeof r.slug === 'string' ? r.slug : null;
  const title = typeof r.title === 'string' ? r.title : null;
  const postType = typeof r.postType === 'string' ? r.postType : 'post';
  if (!id || !slug || title === null) return null;
  const blocks = Array.isArray(r.blocks) ? (r.blocks as Block[]) : [];
  return {
    id,
    slug,
    title,
    excerpt: typeof r.excerpt === 'string' ? r.excerpt : undefined,
    postType,
    authorName: typeof r.authorName === 'string' ? r.authorName : undefined,
    publishedAt: typeof r.publishedAt === 'string' ? r.publishedAt : undefined,
    blocks,
  };
}

function asAuthor(raw: unknown): Author | null {
  if (!raw || typeof raw !== 'object') return null;
  const r = raw as Record<string, unknown>;
  // The wp-json/users surface emits numeric ids; accept either a
  // number or a string so this also works against the (in progress)
  // typed /api/v1/users endpoint that returns string ids.
  let id: string | null = null;
  if (typeof r.id === 'string' && r.id !== '') id = r.id;
  else if (typeof r.id === 'number' && Number.isFinite(r.id)) id = String(r.id);
  const slug = typeof r.slug === 'string' ? r.slug : null;
  const name = typeof r.name === 'string' ? r.name : slug ?? '';
  if (!id || !slug) return null;
  return {
    id,
    slug,
    name,
    description: typeof r.description === 'string' ? r.description : undefined,
  };
}

function asTerm(raw: unknown): Term | null {
  if (!raw || typeof raw !== 'object') return null;
  const r = raw as Record<string, unknown>;
  let id: string | null = null;
  if (typeof r.id === 'string' && r.id !== '') id = r.id;
  else if (typeof r.id === 'number' && Number.isFinite(r.id)) id = String(r.id);
  const slug = typeof r.slug === 'string' ? r.slug : null;
  const name = typeof r.name === 'string' ? r.name : slug ?? '';
  // Both `taxonomy` (typed v1 endpoint) and `taxonomySlug` (wp-json
  // alias) are accepted so callers don't have to know which surface
  // answered.
  const taxonomy =
    (typeof r.taxonomy === 'string' && r.taxonomy) ||
    (typeof r.taxonomySlug === 'string' && r.taxonomySlug) ||
    '';
  if (!id || !slug || !taxonomy) return null;
  return {
    id,
    slug,
    name,
    taxonomy,
    description: typeof r.description === 'string' ? r.description : undefined,
  };
}

function asActiveTheme(raw: unknown): ActiveTheme | null {
  if (!raw || typeof raw !== 'object') return null;
  const r = raw as Record<string, unknown>;
  const slug = typeof r.slug === 'string' ? r.slug : null;
  const title = typeof r.title === 'string' ? r.title : slug ?? '';
  if (!slug) return null;
  return {
    slug,
    title,
    cssCustomProperties:
      typeof r.cssCustomProperties === 'string' ? r.cssCustomProperties : '',
    headerHtml: typeof r.headerHtml === 'string' ? r.headerHtml : '',
    footerHtml: typeof r.footerHtml === 'string' ? r.footerHtml : '',
  };
}

function asResolvedTemplate(raw: unknown): ResolvedTemplate | null {
  if (!raw || typeof raw !== 'object') return null;
  const r = raw as Record<string, unknown>;
  const basename = typeof r.basename === 'string' ? r.basename : null;
  const mainHtml = typeof r.mainHtml === 'string' ? r.mainHtml : '';
  if (!basename) return null;
  return { basename, mainHtml };
}

/**
 * Fetch a post by URL slug. Resolves with `null` for 404 — the
 * caller renders the 404 template path.
 *
 * TODO(#post-by-slug): the `/api/v1/posts/by-slug/{slug}` endpoint
 * is referenced in the BACKLOG but may not be wired on every
 * environment yet. The caller treats network errors as "post not
 * found" so the renderer degrades gracefully.
 */
export async function fetchPostBySlug(
  slug: string,
  options: { cookie?: string; revalidate?: number } = {},
): Promise<Post | null> {
  if (!slug) return null;
  try {
    const raw = await getJson<unknown>(
      `/api/v1/posts/by-slug/${encodeURIComponent(slug)}`,
      options,
    );
    return asPost(raw);
  } catch (err) {
    if (err instanceof ApiError && err.status === 0) {
      // Network down — keep the page renderable by returning the 404
      // path rather than crashing the route handler.
      return null;
    }
    throw err;
  }
}

/**
 * Fetch the active theme summary. Resolves with `null` when the
 * endpoint is unreachable — the caller renders an unstyled fallback
 * so the site still serves text content.
 *
 * TODO(#themes-active): wire the `/api/v1/themes/active` endpoint on
 * the Go side. Until then we ship a documented stub theme via
 * `defaultActiveTheme()` so the renderer is testable end-to-end.
 */
export async function fetchActiveTheme(
  options: { revalidate?: number } = {},
): Promise<ActiveTheme | null> {
  try {
    const raw = await getJson<unknown>('/api/v1/themes/active', options);
    return asActiveTheme(raw);
  } catch (err) {
    if (err instanceof ApiError && err.status === 0) return null;
    throw err;
  }
}

/**
 * Ask the Go-side resolver which template basename applies to this
 * request, and get back the already-walked main HTML for it.
 *
 * The query string mirrors the `templates.Request` fields the Go
 * resolver consumes: `type`, `postType`, `postSlug`, etc.
 *
 * TODO(#themes-active-template): the
 * `/api/v1/themes/active/template` endpoint is documented in the
 * theme system design but not yet exposed on every environment.
 * Treat unreachable as "fall back to default render path".
 */
export async function fetchResolvedTemplate(
  query: {
    type: string;
    postType?: string;
    postSlug?: string;
    postId?: string;
    taxonomySlug?: string;
    termSlug?: string;
    termId?: string;
    authorId?: string;
  },
  options: { revalidate?: number } = {},
): Promise<ResolvedTemplate | null> {
  const params = new URLSearchParams();
  params.set('type', query.type);
  for (const [k, v] of Object.entries(query)) {
    if (k === 'type') continue;
    if (typeof v === 'string' && v !== '') params.set(k, v);
  }
  try {
    const raw = await getJson<unknown>(
      `/api/v1/themes/active/template?${params.toString()}`,
      options,
    );
    return asResolvedTemplate(raw);
  } catch (err) {
    if (err instanceof ApiError && err.status === 0) return null;
    throw err;
  }
}

/**
 * Filters accepted by `fetchArchive`. The home / generic archive page
 * passes the empty shape; the author / category / tag / date routes
 * each add one or two filters so the same endpoint can power all four
 * archive types.
 * Public comments shape returned by the API. Mirrors
 * `apps/api/internal/rest/comments`. Defensive parsing strips
 * anything we don't expect so a future field addition doesn't
 * silently break the renderer.
 */
export interface PublicComment {
  id: string;
  post_id: string;
  parent_id?: string;
  path: string;
  depth: number;
  author_display_name: string;
  content: string;
  created_at: string;
}

function asPublicComment(raw: unknown): PublicComment | null {
  if (!raw || typeof raw !== 'object') return null;
  const r = raw as Record<string, unknown>;
  const id = typeof r.id === 'string' ? r.id : null;
  const postId = typeof r.post_id === 'string' ? r.post_id : null;
  const path = typeof r.path === 'string' ? r.path : null;
  if (!id || !postId || !path) return null;
  return {
    id,
    post_id: postId,
    parent_id: typeof r.parent_id === 'string' ? r.parent_id : undefined,
    path,
    depth: typeof r.depth === 'number' ? r.depth : path.split('.').length,
    author_display_name:
      typeof r.author_display_name === 'string' ? r.author_display_name : 'Anonymous',
    content: typeof r.content === 'string' ? r.content : '',
    created_at: typeof r.created_at === 'string' ? r.created_at : '',
  };
}

/**
 * Fetch the approved comments for a post.
 *
 * Returns an empty list on failure (network down, endpoint
 * unavailable). The catch-all route renders the rest of the page
 * regardless — "comments unavailable" is a softer failure than
 * "post page errored".
 */
export async function fetchPostComments(
  postId: string,
  options: { revalidate?: number; cookie?: string } = {},
): Promise<PublicComment[]> {
  if (!postId) return [];
  try {
    const raw = await getJson<unknown>(
      `/api/v1/posts/${encodeURIComponent(postId)}/comments`,
      options,
    );
    if (!raw || typeof raw !== 'object') return [];
    const data = (raw as { data?: unknown[] }).data;
    if (!Array.isArray(data)) return [];
    return data
      .map((row) => asPublicComment(row))
      .filter((c): c is PublicComment => c !== null);
  } catch (err) {
    if (err instanceof ApiError && err.status === 0) return [];
    return [];
  }
}

/**
 * Fetch the archive feed for the home/archive page. The renderer
 * walks blocks for each entry; the API returns the same `Post`
 * shape as `fetchPostBySlug`, minus heavy fields where applicable.
 */
export interface ArchiveQuery {
  /** Restrict to a single post type — e.g. "post", "page", "book". */
  postType?: string;
  /** Page size. Defaults to 10 server-side when omitted. */
  limit?: number;
  /** 1-based page number for pagination links. */
  page?: number;
  /** Restrict to posts authored by this user slug or numeric id. */
  authorSlug?: string;
  authorId?: string;
  /** Restrict to posts in this term (paired with taxonomy below). */
  termSlug?: string;
  taxonomy?: string;
  /**
   * Backwards-compat shortcut for category-feed callers (#416 feed
   * route). When set, forwarded verbatim as `category=<slug>` on the
   * archive URL. New code should prefer `taxonomy: 'category'` +
   * `termSlug: <slug>`; this shortcut keeps the feed/[category]
   * route working without churn.
   */
  category?: string;
  /** Date archive filters — 4-digit year, 1-12 month, 1-31 day. */
  year?: number;
  month?: number;
  day?: number;
}

/**
 * Total-count metadata for archive feeds. The renderer uses this to
 * build "older / newer" pagination links and to short-circuit empty
 * pages into 404s.
 */
export interface ArchivePage {
  /** Posts on the requested page. May be shorter than `limit`. */
  posts: Post[];
  /** Total posts matching the filter, across all pages. */
  total: number;
  /** Page size that was honoured (mirrors `limit` when supplied). */
  perPage: number;
  /** 1-based page number echoed from the request. */
  page: number;
}

/**
 * Build the query string fragments archive endpoints share. Extracted
 * so the route-specific fetchers can reuse the same parameter names
 * without each having to know the canonical spelling.
 */
function buildArchiveParams(query: ArchiveQuery): URLSearchParams {
  const params = new URLSearchParams();
  params.set('status', 'published');
  if (query.postType) params.set('postType', query.postType);
  if (query.limit) params.set('limit', String(query.limit));
  if (query.page) params.set('page', String(query.page));
  if (query.authorSlug) params.set('authorSlug', query.authorSlug);
  if (query.authorId) params.set('authorId', query.authorId);
  if (query.termSlug) params.set('termSlug', query.termSlug);
  if (query.taxonomy) params.set('taxonomy', query.taxonomy);
  if (query.category) params.set('category', query.category);
  if (query.year !== undefined) params.set('year', String(query.year));
  if (query.month !== undefined) params.set('month', String(query.month));
  if (query.day !== undefined) params.set('day', String(query.day));
  return params;
}

/**
 * Defensive parse of an `{ posts, total, perPage, page }` envelope.
 * Falls back to the bare `{ posts }` shape for backwards compatibility
 * with older API stubs. Always returns a valid `ArchivePage`.
 */
function asArchivePage(raw: unknown, fallbackPerPage: number, fallbackPage: number): ArchivePage {
  const empty: ArchivePage = {
    posts: [],
    total: 0,
    perPage: fallbackPerPage,
    page: fallbackPage,
  };
  if (!raw || typeof raw !== 'object') return empty;
  const r = raw as Record<string, unknown>;
  const rawPosts = Array.isArray(r.posts) ? (r.posts as unknown[]) : [];
  const posts = rawPosts.map((p) => asPost(p)).filter((p): p is Post => p !== null);
  const total =
    typeof r.total === 'number' && Number.isFinite(r.total)
      ? r.total
      : posts.length;
  const perPage =
    typeof r.perPage === 'number' && Number.isFinite(r.perPage) && r.perPage > 0
      ? r.perPage
      : fallbackPerPage;
  const page =
    typeof r.page === 'number' && Number.isFinite(r.page) && r.page > 0
      ? r.page
      : fallbackPage;
  return { posts, total, perPage, page };
}

/**
 * Fetch the archive feed for the home/archive page. The renderer
 * walks blocks for each entry; the API returns the same `Post`
 * shape as `fetchPostBySlug`, minus heavy fields where applicable.
 *
 * Kept as the thin "just give me posts" surface for legacy callers
 * (the homepage and the [...slug] catch-all). New callers should
 * prefer `fetchArchivePage` which surfaces total + page metadata.
 */
export async function fetchArchive(
  query: ArchiveQuery = {},
  options: { revalidate?: number; cookie?: string } = {},
): Promise<Post[]> {
  const page = await fetchArchivePage(query, options);
  return page.posts;
}

/**
 * Fetch a paginated slice of an archive feed. Returns the parsed
 * `{ posts, total, perPage, page }` envelope so the renderer can
 * decide whether to paint a "next page" link.
 *
 * Treats network failure and 404 as "empty page" rather than a hard
 * error — the archive route still paints the surrounding theme parts
 * and shows the empty state. A 5xx still throws ApiError so the
 * caller can surface it.
 */
export async function fetchArchivePage(
  query: ArchiveQuery = {},
  options: { revalidate?: number; cookie?: string } = {},
): Promise<ArchivePage> {
  const params = buildArchiveParams(query);
  const fallbackPerPage = query.limit ?? 10;
  const fallbackPage = query.page ?? 1;
  try {
    const raw = await getJson<unknown>(
      `/api/v1/posts?${params.toString()}`,
      options,
    );
    return asArchivePage(raw, fallbackPerPage, fallbackPage);
  } catch (err) {
    if (err instanceof ApiError && err.status === 0) {
      return {
        posts: [],
        total: 0,
        perPage: fallbackPerPage,
        page: fallbackPage,
      };
    }
    throw err;
  }
}

/**
 * Fetch a user / author by slug. Returns `null` for 404 — the author
 * archive route renders the 404 template path in that case.
 *
 * TODO(#author-by-slug): the `/api/v1/users/by-slug/{slug}` endpoint
 * is referenced in the BACKLOG and matches the convention of the
 * existing `/api/v1/posts/by-slug/{slug}` route. Treat unreachable
 * as "author not found" so the renderer degrades gracefully.
 */
export async function fetchAuthorBySlug(
  slug: string,
  options: { cookie?: string; revalidate?: number } = {},
): Promise<Author | null> {
  if (!slug) return null;
  try {
    const raw = await getJson<unknown>(
      `/api/v1/users/by-slug/${encodeURIComponent(slug)}`,
      options,
    );
    return asAuthor(raw);
  } catch (err) {
    if (err instanceof ApiError && err.status === 0) return null;
    throw err;
  }
}

/**
 * Fetch a taxonomy term by slug + taxonomy. Returns `null` for 404 —
 * the category / tag route renders the 404 template path in that case.
 *
 * The lookup is scoped by taxonomy because two taxonomies may ship a
 * term with the same slug (e.g. a "news" category and a "news" tag);
 * the route always knows which taxonomy it's serving.
 *
 * TODO(#term-by-slug): the `/api/v1/terms/by-slug/{taxonomy}/{slug}`
 * endpoint is documented in the theme system design but not yet wired
 * on every environment. Treat unreachable as "term not found".
 */
export async function fetchTermBySlug(
  taxonomy: string,
  slug: string,
  options: { cookie?: string; revalidate?: number } = {},
): Promise<Term | null> {
  if (!taxonomy || !slug) return null;
  try {
    const raw = await getJson<unknown>(
      `/api/v1/terms/by-slug/${encodeURIComponent(taxonomy)}/${encodeURIComponent(slug)}`,
      options,
    );
    return asTerm(raw);
  } catch (err) {
    if (err instanceof ApiError && err.status === 0) return null;
    throw err;
  }
}

// ── Public site config (from PR #416 sitemap/feeds) ──

/**
 * Fetch the public-site config the renderer needs for discoverability
 * surfaces. Falls back to a safe (no-base-url, no-index) shape if the
 * endpoint is unreachable — the route handlers degrade rather than
 * 500.
 *
 * TODO(#public-site-config): wire `/api/v1/public-site/config` on the
 * Go side once the renderer ships these endpoints. Until then the
 * fallback object mirrors `Config.PublicSite` defaults: empty BaseURL,
 * AllowIndex=false (the staging/dev convention).
 */
export async function fetchPublicSiteConfig(
  options: { revalidate?: number } = {},
): Promise<PublicSiteConfig> {
  const fallback: PublicSiteConfig = { baseUrl: '', allowIndex: false };
  try {
    const raw = await getJson<unknown>('/api/v1/public-site/config', options);
    if (!raw || typeof raw !== 'object') return fallback;
    const r = raw as Record<string, unknown>;
    const baseUrl = typeof r.baseUrl === 'string' ? r.baseUrl : '';
    const allowIndex = typeof r.allowIndex === 'boolean' ? r.allowIndex : false;
    // Defensive: strip a trailing slash if the API forgot to.
    return {
      baseUrl: baseUrl.endsWith('/') ? baseUrl.slice(0, -1) : baseUrl,
      allowIndex,
    };
  } catch (err) {
    if (err instanceof ApiError && err.status === 0) return fallback;
    throw err;
  }
}

/**
 * Public-site configuration as seen by the renderer.
 *
 * `baseUrl` is the absolute origin used to compose canonical URLs +
 * sitemap entries. `allowIndex` defaults to `false` so a renderer
 * that can't reach the API serves a Disallow-everything robots.txt
 * — the safe failure mode for an unconfigured deployment is "stay
 * out of search results".
 */
export interface PublicSiteConfig {
  /** Canonical origin (e.g. `https://example.com`). No trailing slash. */
  baseUrl: string;
  /** Whether crawlers may index this deployment. */
  allowIndex: boolean;
}
