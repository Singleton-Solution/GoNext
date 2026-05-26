/**
 * Themes admin API client — small fetch wrappers over the
 * /api/v1/admin/themes surface. Server-side calls forward the
 * inbound cookie header so the API auth middleware sees the
 * session; client-side calls rely on `credentials: 'include'` to
 * carry the cookie cross-origin (admin runs on :3001, api on :8080).
 */

import { apiBaseUrl } from '@/lib/api-client';
import type { InstallResponse, ThemesListResponse } from './types';

const LIST_URL = '/api/v1/admin/themes';
const INSTALL_URL = '/api/v1/admin/themes/install';
const ACTIVATE_URL = '/api/v1/admin/themes/activate';

/**
 * Server-side list fetch. Returns `null` on any non-2xx so the
 * caller can render an empty-state without short-circuiting the
 * whole page render.
 */
export async function fetchThemesList(cookieHeader: string): Promise<ThemesListResponse | null> {
  try {
    const res = await fetch(`${apiBaseUrl}${LIST_URL}`, {
      headers: {
        Accept: 'application/json',
        ...(cookieHeader ? { Cookie: cookieHeader } : {}),
      },
      cache: 'no-store',
    });
    if (!res.ok) {
      return null;
    }
    const body = (await res.json()) as ThemesListResponse;
    return {
      themes: Array.isArray(body.themes) ? body.themes : [],
      active_slug: body.active_slug ?? '',
    };
  } catch {
    return null;
  }
}

/**
 * Client-side install request. The caller hands a File (the .gntheme
 * ZIP); we wrap it in a FormData so the API's multipart parser
 * accepts it. Returns the resolved {slug, title} on success.
 */
export async function installTheme(file: File): Promise<InstallResponse> {
  const formData = new FormData();
  formData.append('file', file, file.name);
  const res = await fetch(`${apiBaseUrl}${INSTALL_URL}`, {
    method: 'POST',
    credentials: 'include',
    body: formData,
  });
  if (!res.ok) {
    const message = await extractError(res, 'Install failed.');
    throw new Error(message);
  }
  return (await res.json()) as InstallResponse;
}

/**
 * Client-side activate request. The API validates the slug exists
 * on disk before flipping core.active_theme; an unknown slug
 * surfaces as a 404 which we re-throw with the server's error
 * message preserved.
 */
export async function activateTheme(slug: string): Promise<void> {
  const res = await fetch(`${apiBaseUrl}${ACTIVATE_URL}`, {
    method: 'POST',
    credentials: 'include',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ slug }),
  });
  if (!res.ok) {
    const message = await extractError(res, 'Activate failed.');
    throw new Error(message);
  }
}

/**
 * Pull a human-readable message out of a non-2xx response. The API
 * uses {error: {code, message}} envelopes; we fall back to the
 * provided default when the body doesn't parse.
 */
async function extractError(res: Response, fallback: string): Promise<string> {
  try {
    const body = (await res.json()) as { error?: { message?: string } };
    return body?.error?.message ?? fallback;
  } catch {
    return fallback;
  }
}
