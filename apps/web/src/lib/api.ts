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
 * Fetch the archive feed for the home/archive page. The renderer
 * walks blocks for each entry; the API returns the same `Post`
 * shape as `fetchPostBySlug`, minus heavy fields where applicable.
 */
export async function fetchArchive(
  query: { postType?: string; limit?: number } = {},
  options: { revalidate?: number } = {},
): Promise<Post[]> {
  const params = new URLSearchParams();
  params.set('status', 'published');
  if (query.postType) params.set('postType', query.postType);
  if (query.limit) params.set('limit', String(query.limit));
  try {
    const raw = await getJson<unknown>(
      `/api/v1/posts?${params.toString()}`,
      options,
    );
    if (!raw || typeof raw !== 'object') return [];
    const posts = (raw as { posts?: unknown[] }).posts;
    if (!Array.isArray(posts)) return [];
    return posts
      .map((p) => asPost(p))
      .filter((p): p is Post => p !== null);
  } catch (err) {
    if (err instanceof ApiError && err.status === 0) return [];
    throw err;
  }
}
