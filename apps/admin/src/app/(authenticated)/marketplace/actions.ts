'use server';

/**
 * Marketplace — server actions and fetchers.
 *
 * Thin wrappers over the API's `/api/v1/admin/marketplace/*` routes
 * (handler in apps/api/internal/admin/marketplace). Lives on the
 * server so the session cookie can be forwarded explicitly without
 * relying on browser-side credentialed fetch (the admin and API live
 * on different origins in dev).
 *
 * Each function returns either the typed payload (read-side) or an
 * ActionResult discriminated union (write-side). The same shape used
 * by the plugins admin's install action — keeps the call sites
 * uniform.
 */
import { cookies } from 'next/headers';
import { apiBaseUrl } from '@/lib/api-client';
import type {
  ActionResult,
  InstallResponse,
  ListingCard,
  ListingDetail,
  RatingsResponse,
  SortKey,
  VersionRow,
} from './types';

async function cookieHeader(): Promise<string> {
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

async function call(path: string, init: RequestInit = {}): Promise<Response> {
  const url = `${apiBaseUrl().replace(/\/$/, '')}${path}`;
  const cookie = await cookieHeader();
  const headers = new Headers(init.headers);
  headers.set('Accept', 'application/json');
  if (cookie && !headers.has('Cookie')) headers.set('Cookie', cookie);
  return fetch(url, { ...init, headers, cache: 'no-store' });
}

/**
 * Fetch the catalogue page. Filter/search/sort params map verbatim to
 * the API query string. Returns an empty array on any error so the
 * page never crashes the admin shell.
 */
export async function listMarketplaceListings(opts: {
  category?: string;
  q?: string;
  sort?: SortKey;
}): Promise<{ listings: ListingCard[]; error?: string }> {
  const params = new URLSearchParams();
  if (opts.category) params.set('category', opts.category);
  if (opts.q) params.set('q', opts.q);
  if (opts.sort) params.set('sort', opts.sort);
  const qs = params.toString();
  const path =
    '/api/v1/admin/marketplace/listings' + (qs ? `?${qs}` : '');
  try {
    const res = await call(path);
    if (!res.ok) {
      return { listings: [], error: `HTTP ${res.status}` };
    }
    const body = (await res.json()) as { data?: ListingCard[] };
    return { listings: body.data ?? [] };
  } catch (err) {
    const reason = err instanceof Error ? err.message : 'network error';
    return { listings: [], error: reason };
  }
}

/** Fetch the detail row for one listing. */
export async function getMarketplaceListing(
  slug: string,
): Promise<{ listing: ListingDetail | null; error?: string }> {
  try {
    const res = await call(
      `/api/v1/admin/marketplace/listings/${encodeURIComponent(slug)}`,
    );
    if (res.status === 404) {
      return { listing: null, error: 'not_found' };
    }
    if (!res.ok) {
      return { listing: null, error: `HTTP ${res.status}` };
    }
    return { listing: (await res.json()) as ListingDetail };
  } catch (err) {
    const reason = err instanceof Error ? err.message : 'network error';
    return { listing: null, error: reason };
  }
}

/** Version history for one listing. */
export async function getMarketplaceVersions(
  slug: string,
): Promise<{ versions: VersionRow[]; error?: string }> {
  try {
    const res = await call(
      `/api/v1/admin/marketplace/listings/${encodeURIComponent(slug)}/versions`,
    );
    if (!res.ok) {
      return { versions: [], error: `HTTP ${res.status}` };
    }
    const body = (await res.json()) as { data?: VersionRow[] };
    return { versions: body.data ?? [] };
  } catch (err) {
    const reason = err instanceof Error ? err.message : 'network error';
    return { versions: [], error: reason };
  }
}

/** Ratings (aggregate + reviews) for one listing. */
export async function getMarketplaceRatings(
  slug: string,
): Promise<{ ratings: RatingsResponse | null; error?: string }> {
  try {
    const res = await call(
      `/api/v1/admin/marketplace/listings/${encodeURIComponent(slug)}/ratings`,
    );
    if (!res.ok) {
      return { ratings: null, error: `HTTP ${res.status}` };
    }
    return { ratings: (await res.json()) as RatingsResponse };
  } catch (err) {
    const reason = err instanceof Error ? err.message : 'network error';
    return { ratings: null, error: reason };
  }
}

/**
 * Install the latest compatible version of a listing. The API resolves
 * the version and dispatches to the host's plugin lifecycle.
 *
 * Returns the install response on success, or a user-facing error
 * string. The caller is responsible for presenting the capability-
 * review screen *before* invoking this — the API trusts the admin
 * UI's consent contract since the server-side check is the lifecycle
 * manifest validation.
 */
export async function installMarketplacePlugin(
  slug: string,
  acknowledged: boolean,
): Promise<ActionResult<InstallResponse>> {
  if (!slug) return { ok: false, error: 'Missing listing slug.' };
  if (!acknowledged) {
    return {
      ok: false,
      error: 'You must acknowledge the capability review before installing.',
    };
  }
  try {
    const res = await call(
      `/api/v1/admin/marketplace/listings/${encodeURIComponent(slug)}/install`,
      { method: 'POST' },
    );
    if (!res.ok) {
      const body = await res.text().catch(() => '');
      return {
        ok: false,
        error: body
          ? `Install failed (HTTP ${res.status}): ${body.slice(0, 200)}`
          : `Install failed (HTTP ${res.status} ${res.statusText})`,
      };
    }
    return { ok: true, data: (await res.json()) as InstallResponse };
  } catch (err) {
    const reason = err instanceof Error ? err.message : 'network error';
    return { ok: false, error: `Couldn't reach the API (${reason}).` };
  }
}

/**
 * Submit (or update) a star rating + review for the latest version of
 * a listing. The version_id is optional — the API defaults to the
 * latest published version when missing.
 */
export async function submitMarketplaceRating(
  slug: string,
  stars: number,
  reviewText?: string,
  versionId?: string,
): Promise<ActionResult> {
  if (!slug) return { ok: false, error: 'Missing listing slug.' };
  if (stars < 1 || stars > 5) {
    return { ok: false, error: 'Stars must be in 1..5.' };
  }
  try {
    const res = await call(
      `/api/v1/admin/marketplace/listings/${encodeURIComponent(slug)}/ratings`,
      {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          stars,
          review_text: reviewText ?? '',
          version_id: versionId ?? undefined,
        }),
      },
    );
    if (!res.ok) {
      const body = await res.text().catch(() => '');
      return {
        ok: false,
        error: body
          ? `Rating failed (HTTP ${res.status}): ${body.slice(0, 200)}`
          : `Rating failed (HTTP ${res.status})`,
      };
    }
    return { ok: true };
  } catch (err) {
    const reason = err instanceof Error ? err.message : 'network error';
    return { ok: false, error: `Couldn't reach the API (${reason}).` };
  }
}
