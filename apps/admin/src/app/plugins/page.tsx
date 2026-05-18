/**
 * Plugins — admin list screen.
 *
 * Server component that fetches the first page of installed plugins
 * from `GET /api/v1/plugins` and hands the result to the client island
 * `<PluginListClient>` for filter / search / per-row action wiring.
 *
 * Endpoint dependency
 * ===================
 * `GET /api/v1/plugins` is tracked alongside the install / activate
 * REST endpoints in issues #340 / #341. The fetch is defensive: on any
 * failure (network, 404 because the endpoint isn't deployed yet, 5xx)
 * we render an inline notice and the empty state so the surrounding
 * admin shell stays navigable.
 *
 * Auth
 * ====
 * Like every other admin server fetch, we forward the session cookie
 * via the explicit `Cookie` header (server-to-server fetches don't have
 * a browser to carry credentials). The admin's auth middleware
 * guarantees `cookies()` is populated by the time this runs.
 */
import { cookies } from 'next/headers';
import type { ReactElement } from 'react';
import { apiBaseUrl } from '../api-client';
import { PluginListClient } from './PluginListClient';
import type { PluginListResponse, PluginRecord } from './types';

export const dynamic = 'force-dynamic';

/**
 * Normalise whatever envelope the host returns into our PluginRecord[].
 * The host contract is still in flux (issue #340) so we accept both
 * `{ plugins: [...] }` and a bare top-level array. A non-list response
 * is treated as empty.
 */
function extractPlugins(payload: unknown): PluginRecord[] {
  if (Array.isArray(payload)) return payload as PluginRecord[];
  if (!payload || typeof payload !== 'object') return [];
  const envelope = payload as PluginListResponse;
  if (Array.isArray(envelope.plugins)) return envelope.plugins;
  return [];
}

interface FetchResult {
  plugins: PluginRecord[];
  error?: string;
}

async function fetchPlugins(): Promise<FetchResult> {
  let cookieHeader = '';
  try {
    const store = await cookies();
    cookieHeader = store
      .getAll()
      .map((c) => `${c.name}=${c.value}`)
      .join('; ');
  } catch {
    cookieHeader = '';
  }

  const url = `${apiBaseUrl.replace(/\/$/, '')}/api/v1/plugins`;
  try {
    const res = await fetch(url, {
      method: 'GET',
      headers: {
        Accept: 'application/json',
        ...(cookieHeader ? { Cookie: cookieHeader } : {}),
      },
      cache: 'no-store',
    });
    if (!res.ok) {
      return { plugins: [], error: `HTTP ${res.status}` };
    }
    const payload = (await res.json()) as unknown;
    return { plugins: extractPlugins(payload) };
  } catch (err) {
    const reason = err instanceof Error ? err.message : 'network error';
    return { plugins: [], error: reason };
  }
}

export default async function PluginsPage(): Promise<ReactElement> {
  const { plugins, error } = await fetchPlugins();
  return <PluginListClient plugins={plugins} fetchError={error} />;
}
