'use server';

/**
 * Plugin admin — server actions.
 *
 * Thin wrappers over the host's `/api/v1/plugins/*` REST endpoints. The
 * actions live on the server so the session cookie is read via
 * `next/headers` rather than relying on browser-side credential
 * forwarding (the admin and API live on different origins in dev).
 *
 * Endpoint TODOs
 * ==============
 * At the time this PR lands, the plugin REST endpoints are still being
 * wired by issues #340 / #341 (install + activate routes). The action
 * paths used below are the canonical projection of the lifecycle
 * Manager methods. If the host returns 404 we surface a friendly
 * "endpoint not deployed yet" message so the admin doesn't crash the
 * happy-path navigation.
 *
 *   POST   /api/v1/plugins/install       — multipart upload (bundle + manifest)
 *   POST   /api/v1/plugins/:name/activate
 *   POST   /api/v1/plugins/:name/deactivate
 *   DELETE /api/v1/plugins/:name         — uninstall
 *
 * The actions do not call `revalidatePath` here because the list page
 * is already `force-dynamic`; the client triggers a `router.refresh()`
 * after a successful action to re-render server data.
 */
import { cookies } from 'next/headers';
import { apiBaseUrl } from '@/lib/api-client';
import type { ActionResult } from './types';

/** Build a Cookie header from the request's cookie jar (server-only). */
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

/**
 * Wrap a fetch call in the same error shape every action returns so the
 * client can render results uniformly.
 */
async function callHost(
  path: string,
  init: RequestInit,
): Promise<ActionResult> {
  const url = `${apiBaseUrl.replace(/\/$/, '')}${path}`;
  const cookie = await cookieHeader();
  const headers = new Headers(init.headers);
  if (cookie && !headers.has('Cookie')) headers.set('Cookie', cookie);
  headers.set('Accept', 'application/json');

  try {
    const res = await fetch(url, { ...init, headers, cache: 'no-store' });
    if (res.status === 404) {
      // The endpoint may legitimately not exist yet (issues #340/#341).
      // Surface as a user-facing reason rather than a crash.
      return {
        ok: false,
        error:
          'The plugin REST endpoint is not deployed yet. ' +
          'Tracked in issues #340 and #341.',
      };
    }
    if (!res.ok) {
      const body = await res.text().catch(() => '');
      const message = body
        ? `Request failed (HTTP ${res.status}): ${body.slice(0, 200)}`
        : `Request failed (HTTP ${res.status} ${res.statusText})`;
      return { ok: false, error: message };
    }
    return { ok: true };
  } catch (err) {
    const reason = err instanceof Error ? err.message : 'network error';
    return { ok: false, error: `Couldn't reach the API (${reason}).` };
  }
}

/**
 * Install a plugin from a multipart upload. The form is expected to
 * carry at least one of:
 *   - `bundle`  — the `.gnplugin` archive (preferred path)
 *   - `manifest` — a manifest.json blob for marketplace-stub installs
 *
 * The host accepts either; for the upload path it parses the bundle
 * and extracts the manifest itself, so we just forward the body
 * verbatim and let it 4xx if neither is present.
 */
export async function installPlugin(formData: FormData): Promise<ActionResult> {
  const hasBundle = formData.get('bundle');
  const hasManifest = formData.get('manifest');
  if (!hasBundle && !hasManifest) {
    return {
      ok: false,
      error:
        'Pick a plugin bundle (.gnplugin) or a manifest.json before installing.',
    };
  }
  // Confirm flag — the install handler refuses unless the operator has
  // explicitly acknowledged the capability review.
  const confirmed = formData.get('capabilities_acknowledged');
  if (confirmed !== 'on' && confirmed !== 'true') {
    return {
      ok: false,
      error:
        'You must acknowledge the capability review before installing.',
    };
  }
  return callHost('/api/v1/plugins/install', {
    method: 'POST',
    body: formData,
  });
}

/**
 * Flip an inactive (or installed) plugin to active. The host will
 * refuse if any declared dependency isn't satisfied; we surface that as
 * an error string verbatim.
 */
export async function activatePlugin(name: string): Promise<ActionResult> {
  if (!name) return { ok: false, error: 'Missing plugin name.' };
  return callHost(`/api/v1/plugins/${encodeURIComponent(name)}/activate`, {
    method: 'POST',
  });
}

/**
 * Flip an active plugin back to inactive. Idempotent on the host side —
 * a no-op if it's already inactive.
 */
export async function deactivatePlugin(name: string): Promise<ActionResult> {
  if (!name) return { ok: false, error: 'Missing plugin name.' };
  return callHost(`/api/v1/plugins/${encodeURIComponent(name)}/deactivate`, {
    method: 'POST',
  });
}

/**
 * Tear down a plugin row entirely. The host pre-conditions the
 * transition (must be inactive first) so the UI prompts the operator
 * to deactivate before uninstalling.
 */
export async function uninstallPlugin(name: string): Promise<ActionResult> {
  if (!name) return { ok: false, error: 'Missing plugin name.' };
  return callHost(`/api/v1/plugins/${encodeURIComponent(name)}`, {
    method: 'DELETE',
  });
}
