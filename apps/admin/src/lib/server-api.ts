/**
 * @gonext/admin — server-side API helpers.
 *
 * Every Next.js Server Component that talks to the GoNext API needs to
 * forward the operator's `gonext_session` cookie so the API auth
 * middleware sees the session. Without that header the fetch runs
 * anonymously and every admin list / detail page renders the
 * "Couldn't load X (HTTP 401)" empty state — even when the operator is
 * signed in.
 *
 * `credentials: 'include'` (the browser-side api-client.ts default) is
 * a no-op on the Next.js server runtime: there is no document.cookie
 * jar to attach. We instead pull the inbound request's cookies via
 * `next/headers` and stamp them onto the outbound request explicitly.
 *
 * Two surfaces:
 *
 *  - `serverApiGet<T>(path)` — JSON GET. Throws when status is outside
 *    2xx so the caller can render an error state with the HTTP code.
 *  - `serverApiFetch(path, init?)` — escape hatch. Returns the raw
 *    `Response`; the caller decides whether non-2xx is fatal. Used by
 *    callers that already have bespoke error / fallback shapes
 *    (graceful empty states, 404 → notFound(), etc.).
 *
 * Both helpers set `cache: 'no-store'` because every screen they back
 * is operator-facing dynamic data — there is no static surface that
 * benefits from Next's fetch cache, and a cached page would leak one
 * operator's view to the next.
 */
import { cookies } from 'next/headers';
import { apiBaseUrl } from './api-client';

/**
 * Build the request headers for a server-side API call.
 *
 * The cookie header is forwarded from `next/headers` so the inbound
 * session travels with the outbound fetch. `cookies()` can throw
 * during certain build paths (e.g. static prerender of a page that
 * later flips to `force-dynamic`), in which case we drop the cookie
 * header and let the API return whatever it would for an anonymous
 * request — the caller renders its empty/error state from there.
 */
async function buildHeaders(
  extra?: Record<string, string>,
): Promise<HeadersInit> {
  let cookie = '';
  try {
    const cookieStore = await cookies();
    cookie = cookieStore.toString();
  } catch {
    cookie = '';
  }
  return {
    Accept: 'application/json',
    ...(cookie ? { cookie } : {}),
    ...(extra ?? {}),
  };
}

function joinUrl(path: string): string {
  return `${apiBaseUrl()}${path.startsWith('/') ? path : `/${path}`}`;
}

/**
 * JSON GET against the GoNext API. Throws on non-2xx so the caller's
 * `try { ... } catch (err) { ... }` produces the same shape it would
 * have when the previous hand-rolled `fetch` returned `!res.ok`.
 */
export async function serverApiGet<T>(path: string): Promise<T> {
  const res = await fetch(joinUrl(path), {
    headers: await buildHeaders(),
    cache: 'no-store',
  });
  if (!res.ok) {
    throw new Error(`API ${res.status}: ${res.statusText}`);
  }
  return res.json() as Promise<T>;
}

/**
 * Lower-level server-side fetch. Returns the raw `Response` so the
 * caller can decide how to handle non-2xx (a graceful empty state, a
 * 404 → `notFound()`, a permission-denied notice, etc.). The cookie
 * header is forwarded the same way as `serverApiGet`.
 *
 * `body`, when provided, is JSON-encoded and a `Content-Type:
 * application/json` header is added. Callers that need multipart
 * uploads should keep using `fetch` directly — those flows run from
 * client components anyway.
 */
export async function serverApiFetch(
  path: string,
  init?: {
    method?: string;
    body?: unknown;
    headers?: Record<string, string>;
  },
): Promise<Response> {
  return fetch(joinUrl(path), {
    method: init?.method ?? 'GET',
    headers: await buildHeaders({
      ...(init?.body !== undefined ? { 'Content-Type': 'application/json' } : {}),
      ...init?.headers,
    }),
    body: init?.body !== undefined ? JSON.stringify(init.body) : undefined,
    cache: 'no-store',
  });
}
