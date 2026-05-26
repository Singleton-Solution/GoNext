/**
 * Client-side counterparts to api.ts. Same endpoints, but the
 * fetches go through the browser (credentials: 'include') instead
 * of forwarding cookies server-side. Split into a separate file so
 * the server component never accidentally pulls in browser-only
 * code at SSR time.
 */

import { apiBaseUrl } from '@/lib/api-client';
import type { InstallResponse, ThemesListResponse } from './types';

export async function fetchThemesListClient(): Promise<ThemesListResponse | null> {
  try {
    const res = await fetch(`${apiBaseUrl}/api/v1/admin/themes`, {
      method: 'GET',
      credentials: 'include',
      headers: { Accept: 'application/json' },
    });
    if (!res.ok) return null;
    const body = (await res.json()) as ThemesListResponse;
    return {
      themes: Array.isArray(body.themes) ? body.themes : [],
      active_slug: body.active_slug ?? '',
    };
  } catch {
    return null;
  }
}

export async function installTheme(file: File): Promise<InstallResponse> {
  const formData = new FormData();
  formData.append('file', file, file.name);
  const res = await fetch(`${apiBaseUrl}/api/v1/admin/themes/install`, {
    method: 'POST',
    credentials: 'include',
    body: formData,
  });
  if (!res.ok) {
    throw new Error(await extractError(res, 'Install failed.'));
  }
  return (await res.json()) as InstallResponse;
}

export async function activateTheme(slug: string): Promise<void> {
  const res = await fetch(`${apiBaseUrl}/api/v1/admin/themes/activate`, {
    method: 'POST',
    credentials: 'include',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ slug }),
  });
  if (!res.ok) {
    throw new Error(await extractError(res, 'Activate failed.'));
  }
}

async function extractError(res: Response, fallback: string): Promise<string> {
  try {
    const body = (await res.json()) as { error?: { message?: string } };
    return body?.error?.message ?? fallback;
  } catch {
    return fallback;
  }
}
