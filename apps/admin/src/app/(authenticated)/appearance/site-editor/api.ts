/**
 * Site Editor Lite API client.
 *
 * Thin wrappers over the shared `apiRequest` helper that surface the
 * three site-editor endpoints. Kept in their own module so tests can
 * mock them with `vi.mock('./api')` without poking the shared fetch
 * helper.
 */
import { api } from '@/lib/api-client';
import type {
  SiteEditorPartsResponse,
  SiteEditorPutPayload,
  SiteEditorPutResponse,
} from './types';

/** Root path for every endpoint. The trailing path varies per verb. */
export const SITE_EDITOR_BASE = '/api/v1/admin/site_editor';

/**
 * Fetch the list of parts for the active theme. The handler resolves
 * each part's blocks server-side (override-first), so the client
 * doesn't have to do a per-part follow-up.
 */
export function fetchParts(signal?: AbortSignal): Promise<SiteEditorPartsResponse> {
  return api.get<SiteEditorPartsResponse>(`${SITE_EDITOR_BASE}/parts`, { signal });
}

/**
 * Save an override BlockTree for the named part. The server validates
 * the tree (each block name must resolve in the registry) and persists
 * it via the options table. Returns the saved tree so the client can
 * fold it back into local state.
 */
export function putPart(
  name: string,
  payload: SiteEditorPutPayload,
  signal?: AbortSignal,
): Promise<SiteEditorPutResponse> {
  return api.put<SiteEditorPutResponse>(
    `${SITE_EDITOR_BASE}/parts/${encodeURIComponent(name)}`,
    payload,
    { signal },
  );
}

/**
 * Remove the override for the named part. The next read falls back to
 * the on-disk theme part. Idempotent: deleting a part without an
 * override returns 204 just the same as deleting one that had it.
 */
export function deletePart(name: string, signal?: AbortSignal): Promise<void> {
  return api.delete<void>(
    `${SITE_EDITOR_BASE}/parts/${encodeURIComponent(name)}`,
    { signal },
  );
}
