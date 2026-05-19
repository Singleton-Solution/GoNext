/**
 * Theme Customizer — API helpers.
 *
 * Thin wrappers over `api-client` for the three customizer routes:
 *
 *  - `GET /api/v1/admin/customizer/active` — returns the active theme
 *    manifest + persisted overrides.
 *  - `PUT /api/v1/admin/customizer/active` — saves a partial override.
 *  - `DELETE /api/v1/admin/customizer/active` — clears overrides.
 *
 * The fetch wrapper handles JSON encoding, credentials, and the
 * ApiError -> {available, error} translation so the page component can
 * render a graceful error banner instead of throwing.
 */
import { api, ApiError } from '../../api-client';
import type { ActiveResponse, ThemeOverrides } from './types';

/**
 * Fetch the active theme manifest plus current overrides. On any
 * failure (API down, registry not wired, etc.) returns `available:false`
 * so the customizer page can paint an error banner without crashing.
 */
export async function fetchActive(): Promise<
  | { available: true; data: ActiveResponse }
  | { available: false; error: string }
> {
  try {
    const data = await api.get<ActiveResponse>('/api/v1/admin/customizer/active');
    return { available: true, data };
  } catch (error) {
    const message =
      error instanceof ApiError
        ? `API error ${error.status}: ${error.statusText}`
        : error instanceof Error
          ? error.message
          : 'unknown error';
    return { available: false, error: message };
  }
}

/**
 * Persist a partial override. Resolves with the updated `ActiveResponse`
 * on success; throws `ApiError` on validation failure or other server
 * errors so the form can surface a toast.
 */
export function saveOverrides(
  overrides: ThemeOverrides,
): Promise<ActiveResponse> {
  return api.put<ActiveResponse>(
    '/api/v1/admin/customizer/active',
    overrides,
  );
}

/**
 * Clear the persisted overrides for the active theme. Returns void on
 * success (204); throws ApiError otherwise.
 */
export async function resetOverrides(): Promise<void> {
  await api.delete('/api/v1/admin/customizer/active');
}
