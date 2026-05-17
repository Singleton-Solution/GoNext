/**
 * Settings API helpers.
 *
 * Thin wrappers over `api-client` that handle the two registry endpoints:
 *
 *  - `GET /api/v1/settings?group=<group>` — returns a flat `key -> value` map
 *    of all settings in that group. Used by the server component to pre-fill
 *    each form on first paint.
 *  - `PATCH /api/v1/settings` — accepts a sparse `{ [key]: value }` body and
 *    persists only the keys present. Used by the client form on submit.
 *
 * The server component tolerates an `ApiError` from the GET (registry not yet
 * wired through the gateway) by rendering the form with empty values + a
 * banner. The client component surfaces PATCH errors as an inline toast.
 */
import { api, ApiError } from '../api-client';
import type { SettingsGroup, SettingsValues } from './types';

/**
 * Fetch all settings in `group`. Returns an empty map when the endpoint is
 * unreachable (registry not yet shipped through the gateway) so the page
 * gracefully degrades to an "API not available" banner instead of a 500.
 */
export async function fetchSettings(
  group: SettingsGroup,
): Promise<{ values: SettingsValues; available: boolean }> {
  try {
    const values = await api.get<SettingsValues>(
      `/api/v1/settings?group=${encodeURIComponent(group)}`,
    );
    return { values: values ?? {}, available: true };
  } catch (error) {
    // Any failure (network, 404 because registry isn't wired yet, 5xx) is
    // treated as "API not available" so the admin still loads. The form
    // itself flags the unavailability to the user.
    if (error instanceof ApiError || error instanceof Error) {
      return { values: {}, available: false };
    }
    throw error;
  }
}

/**
 * Persist a sparse patch. Resolves with the updated values on success, throws
 * `ApiError` on failure — the caller is responsible for surfacing the toast.
 */
export function patchSettings(patch: SettingsValues): Promise<SettingsValues> {
  return api.patch<SettingsValues>('/api/v1/settings', patch);
}
