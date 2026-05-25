/**
 * Browser-side host calls.
 *
 * The `host` object is the SDK's main namespace for plugin code that
 * needs to talk back to the GoNext server. Each sub-namespace
 * (`host.posts`, `host.users`, `host.media`, `host.cache`) is a thin
 * shim over the existing REST surfaces the admin already exposes —
 * the WP-compat read/write shim under `/wp-json/wp/v2/...` for
 * posts, users, and media (see apps/api/internal/wprest/shim.go),
 * and the per-plugin sub-tree under `/api/plugins/{slug}/...` for
 * plugin-scoped endpoints like cache invalidation.
 *
 * Design rules:
 *
 *   1. Every call is `same-origin` + `credentials: 'include'`. The
 *      admin authenticates the plugin's browser bundle the same way
 *      it authenticates its own UI: session cookie + nonce. Plugin
 *      code MUST NOT need to forge an Authorization header.
 *
 *   2. Errors throw `HostFetchError` (typed) — never silently
 *      resolve to `undefined`. Plugin authors get a clear stack and
 *      can `instanceof` to branch on network vs. server-side
 *      failures.
 *
 *   3. No client-side state. Each method is a pure function over the
 *      arguments; caching / invalidation is the host's job (and
 *      `host.cache.invalidate` exists precisely so plugins can
 *      participate in it).
 *
 *   4. The slug is read lazily through `requireSlug()` on the FIRST
 *      call, not at module import. Plugins that wrap the SDK in a
 *      lazy-initialized singleton therefore don't need to also wrap
 *      the slug-detection bootstrap.
 *
 * The shims are intentionally narrow. Anything the plugin's own
 * routes (#136) handle is out of scope here — this object exposes
 * the COMMON host surface that nearly every plugin needs.
 */

import { requireSlug } from './slug';

/**
 * Base path for the WP-compat shim. Mirrors `wprest.BasePath` on the
 * Go side. We avoid importing a JSON config so this single file is
 * the wire-level contract — if the host ever moves the prefix, this
 * is the one place to update.
 */
const WP_BASE = '/wp-json/wp/v2';

/**
 * Base path for plugin-scoped sub-routes. The host mounts each
 * plugin's manifest-declared HTTP routes under
 * `/api/plugins/{slug}/...`, and the SDK uses the same prefix for
 * SDK-defined plugin operations (cache invalidation, …) so a future
 * extension can land without a new prefix.
 */
const PLUGIN_API_BASE = '/api/plugins';

/**
 * One post in the WP REST v2 shape. Only the fields the SDK
 * exposes are typed; the underlying payload may contain more
 * fields, which the helpers preserve so consumers can cast.
 */
export interface Post {
  id: number;
  date: string;
  date_gmt?: string;
  modified?: string;
  slug: string;
  status: string;
  type?: string;
  title: { rendered: string };
  content?: { rendered: string };
  excerpt?: { rendered: string };
  author?: number;
  featured_media?: number;
  [extra: string]: unknown;
}

/**
 * Subset of the user shape relevant to client code. The host's
 * `/wp-json/wp/v2/users` returns more fields, which pass through
 * via the index signature.
 */
export interface User {
  id: number;
  name: string;
  slug?: string;
  description?: string;
  avatar_urls?: Record<string, string>;
  [extra: string]: unknown;
}

/**
 * One media item. The shim returns the WP REST v2 attachment
 * shape — same as live WP — so plugin authors with prior WP
 * experience can use the existing field names directly.
 */
export interface Media {
  id: number;
  date?: string;
  slug?: string;
  type?: string;
  title?: { rendered: string };
  source_url?: string;
  mime_type?: string;
  [extra: string]: unknown;
}

/**
 * Common collection-listing arguments. `perPage` maps to WP's
 * `per_page`; we expose the camelCase form for ergonomic plugin
 * code and translate at the wire.
 *
 * Any extra fields are passed through to the query string so
 * future server-side filters work without an SDK rev.
 */
export interface ListOptions {
  page?: number;
  perPage?: number;
  search?: string;
  /** Free-form pass-through. Stringified into the query string. */
  [extra: string]: unknown;
}

/**
 * Strong-typed error thrown by every host call. `status` is `0`
 * when the failure is a transport-level error (CORS, offline,
 * fetch abort); otherwise it carries the HTTP status code.
 *
 * Plugin code branches on:
 *
 *   if (err instanceof HostFetchError && err.status === 0) {…}
 *
 * to distinguish "no network" from "server said no". The
 * `responseBody` field is the parsed JSON body when one exists,
 * else `null` — same shape the WP REST error envelope uses.
 */
export class HostFetchError extends Error {
  override readonly name = 'HostFetchError';
  readonly status: number;
  readonly url: string;
  readonly responseBody: unknown;
  constructor(message: string, status: number, url: string, responseBody: unknown) {
    super(message);
    this.status = status;
    this.url = url;
    this.responseBody = responseBody;
  }
}

/**
 * Options accepted by every host-side call. Exposed so a plugin can
 * forward an `AbortSignal` from its own component lifecycle and
 * cancel in-flight requests on unmount.
 */
export interface HostCallOptions {
  signal?: AbortSignal;
}

/**
 * Issues a host fetch. Owns the canonical request shape — same-
 * origin + cookies, JSON Accept, JSON Content-Type when a body is
 * supplied, an AbortSignal forwarded from the caller.
 *
 * The body parsing is best-effort: a 204 / empty response resolves
 * to `null`; a non-JSON content-type passes through the raw text
 * (cast); a JSON body parses to whatever shape the caller's
 * generic claims. Errors throw `HostFetchError` with the parsed
 * body (if any) attached so callers can render server-side validation
 * messages without re-parsing.
 */
async function hostFetch<T>(
  url: string,
  init: RequestInit,
  callOpts: HostCallOptions,
): Promise<T> {
  const headers = new Headers(init.headers ?? undefined);
  if (!headers.has('Accept')) {
    headers.set('Accept', 'application/json');
  }
  if (init.body !== undefined && init.body !== null && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json');
  }
  let response: Response;
  try {
    response = await fetch(url, {
      credentials: 'include',
      ...init,
      headers,
      signal: callOpts.signal,
    });
  } catch (err) {
    // Network-level failure: AbortError, TypeError ("Failed to
    // fetch"), CORS, offline. Re-raise as HostFetchError so consumers
    // can `instanceof`-check uniformly.
    const message = err instanceof Error ? err.message : String(err);
    throw new HostFetchError(`network error: ${message}`, 0, url, null);
  }
  const parsed = await safeParseBody(response);
  if (!response.ok) {
    throw new HostFetchError(
      `host call failed: ${response.status} ${response.statusText}`,
      response.status,
      url,
      parsed,
    );
  }
  return parsed as T;
}

/**
 * Reads the response body without throwing on an empty / non-JSON
 * payload. Returns `null` when there is no body.
 */
async function safeParseBody(response: Response): Promise<unknown> {
  if (response.status === 204) {
    return null;
  }
  const contentType = response.headers.get('content-type') ?? '';
  // Read text first; many error envelopes from middleware (rate
  // limit, CSP) come back as text/plain.
  const text = await response.text();
  if (text === '') {
    return null;
  }
  if (contentType.includes('application/json')) {
    try {
      return JSON.parse(text) as unknown;
    } catch {
      return text;
    }
  }
  return text;
}

/**
 * Builds the query string from a ListOptions-like object. Returns
 * the prefix '?' included when there is at least one parameter,
 * otherwise an empty string. Keys map camelCase → snake_case for
 * WP-compat; unknown keys pass through as-is.
 *
 * Boolean values serialize as 'true'/'false' (WP convention).
 * Array values join with commas (WP convention).
 * Nullable values are skipped, so callers can build an options
 * object with conditional fields without manual cleanup.
 */
function buildQuery(opts: ListOptions | undefined): string {
  if (opts === undefined) {
    return '';
  }
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(opts)) {
    if (value === undefined || value === null) continue;
    const wire = camelToSnake(key);
    if (Array.isArray(value)) {
      params.set(wire, value.map(String).join(','));
      continue;
    }
    params.set(wire, String(value));
  }
  const s = params.toString();
  return s === '' ? '' : `?${s}`;
}

/**
 * Quick camelCase → snake_case translation. Used for query keys
 * (`perPage` → `per_page`). Naïve on purpose; the surface is small.
 */
function camelToSnake(s: string): string {
  return s.replace(/[A-Z]/g, (m) => `_${m.toLowerCase()}`);
}

/**
 * `host.posts` — read access to published posts.
 *
 * Writes (create / update / delete) deliberately are NOT exposed
 * here. Plugin browser code that needs to author content does so
 * through the host's own admin UI components (`<PostEditor>`),
 * which run with the operator's session cookie + nonce. Letting a
 * plugin's browser bundle mint arbitrary posts would bypass the
 * admin's capability checks.
 */
const posts = {
  /**
   * Lists posts. Pages and per-page caps mirror the WP REST shim.
   * Returns the raw array — the host emits the totals via the
   * `X-WP-Total` header which plugin code can read via the
   * `listResponse` helper if needed.
   */
  list(options?: ListOptions, callOpts: HostCallOptions = {}): Promise<Post[]> {
    const url = `${WP_BASE}/posts${buildQuery(options)}`;
    return hostFetch<Post[]>(url, { method: 'GET' }, callOpts);
  },

  /**
   * Reads a single post by id. The id is encoded into the path so
   * a plugin that builds the id from user input doesn't need its
   * own validation — the host route's `{id}` matcher rejects
   * malformed values.
   */
  get(id: number, callOpts: HostCallOptions = {}): Promise<Post> {
    const url = `${WP_BASE}/posts/${encodeURIComponent(String(id))}`;
    return hostFetch<Post>(url, { method: 'GET' }, callOpts);
  },
} as const;

/**
 * `host.users` — read access to the user directory plus a special
 * `me()` helper that resolves the currently-authenticated user.
 *
 * `me()` calls `/wp-json/wp/v2/users/me`, which is the same
 * endpoint the live WP REST surface uses. It returns the calling
 * user's profile (and only that user's; the host enforces it
 * server-side).
 */
const users = {
  list(options?: ListOptions, callOpts: HostCallOptions = {}): Promise<User[]> {
    const url = `${WP_BASE}/users${buildQuery(options)}`;
    return hostFetch<User[]>(url, { method: 'GET' }, callOpts);
  },
  get(id: number, callOpts: HostCallOptions = {}): Promise<User> {
    const url = `${WP_BASE}/users/${encodeURIComponent(String(id))}`;
    return hostFetch<User>(url, { method: 'GET' }, callOpts);
  },
  me(callOpts: HostCallOptions = {}): Promise<User> {
    return hostFetch<User>(`${WP_BASE}/users/me`, { method: 'GET' }, callOpts);
  },
} as const;

/**
 * `host.media` — read access to the media library.
 *
 * Upload is intentionally not exposed: the WP REST media endpoint
 * accepts multipart bodies and requires a nonce + capability check
 * the admin UI handles via its own `<MediaUploader>`. Plugin code
 * that wants to attach uploads should drive the admin's media
 * picker via the (forthcoming) `host.ui` namespace.
 */
const media = {
  list(options?: ListOptions, callOpts: HostCallOptions = {}): Promise<Media[]> {
    const url = `${WP_BASE}/media${buildQuery(options)}`;
    return hostFetch<Media[]>(url, { method: 'GET' }, callOpts);
  },
  get(id: number, callOpts: HostCallOptions = {}): Promise<Media> {
    const url = `${WP_BASE}/media/${encodeURIComponent(String(id))}`;
    return hostFetch<Media>(url, { method: 'GET' }, callOpts);
  },
} as const;

/**
 * `host.cache` — plugin-scoped cache invalidation.
 *
 * Posts under `/api/plugins/{slug}/cache/invalidate` with the
 * supplied tag list. The host's invalidator (see
 * packages/go/cache/invalidator) then propagates the invalidation
 * across the cluster via the shared Redis channel.
 *
 * The capability gate is the same as the WASM-side
 * `gn_cache_invalidate`: the plugin must hold the
 * `cache.invalidate` grant. Plugins without the grant get a 403
 * back, which surfaces as a `HostFetchError` with status 403.
 *
 * Tag validation is server-side; the client only checks that each
 * tag is a string so a typo throws TYPE-side rather than HTTP-side.
 */
const cache = {
  /**
   * Invalidates one or more cache tags. The host accepts either a
   * single string or an array; the SDK normalizes to an array on
   * the wire so the server-side schema is unambiguous.
   *
   * Returns the (server-supplied) count of invalidated keys, or 0
   * if the server didn't echo one back.
   */
  async invalidate(
    tags: string | ReadonlyArray<string>,
    callOpts: HostCallOptions = {},
  ): Promise<number> {
    const slug = requireSlug();
    const tagList = Array.isArray(tags) ? Array.from(tags) : [tags as string];
    for (const t of tagList) {
      if (typeof t !== 'string' || t === '') {
        throw new TypeError(
          `[@gonext/sdk] host.cache.invalidate: tag must be a non-empty string (got ${JSON.stringify(t)})`,
        );
      }
    }
    const url = `${PLUGIN_API_BASE}/${encodeURIComponent(slug)}/cache/invalidate`;
    const body = JSON.stringify({ tags: tagList });
    const result = await hostFetch<{ invalidated?: number } | null>(
      url,
      { method: 'POST', body },
      callOpts,
    );
    if (result === null || typeof result !== 'object') {
      return 0;
    }
    return typeof result.invalidated === 'number' ? result.invalidated : 0;
  },
} as const;

/**
 * The `host` namespace. Exported as a single frozen object so
 * plugin code that destructures (`const { posts } = host`) gets a
 * stable reference and can't accidentally monkey-patch a method on
 * another plugin's namespace.
 */
export const host = Object.freeze({
  posts,
  users,
  media,
  cache,
}) as {
  readonly posts: typeof posts;
  readonly users: typeof users;
  readonly media: typeof media;
  readonly cache: typeof cache;
};

/**
 * Internal hook for tests. Not exported from the public barrel.
 * Exposes the low-level fetch helper so tests can pin transport
 * behaviour (header set, abort plumbing, error shape) without
 * needing to hit one of the namespace shims.
 *
 * @internal
 */
export const __test_hostFetch = hostFetch;
/** @internal */
export const __test_buildQuery = buildQuery;
