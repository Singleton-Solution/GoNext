/**
 * Theme Customizer — server-only API helpers.
 *
 * Companion to `./api.ts`; this file is the server-side variant of
 * `fetchActive` that forwards the operator's session cookie via
 * `next/headers`. Kept separate because `next/headers` cannot be
 * bundled into a Client Component and the client-side `./api.ts`
 * module IS imported by mutation forms that run in the browser.
 *
 * The error envelope shape matches `./api.ts::fetchActive` so the
 * page component can swap between them without branching.
 */
import 'server-only';
import { serverApiFetch } from '@/lib/server-api';
import type { ActiveResponse } from './types';

export async function fetchActiveServer(): Promise<
  | { available: true; data: ActiveResponse }
  | { available: false; error: string }
> {
  try {
    const res = await serverApiFetch('/api/v1/admin/customizer/active');
    if (!res.ok) {
      return {
        available: false,
        error: `API error ${res.status}: ${res.statusText}`,
      };
    }
    const data = (await res.json()) as ActiveResponse;
    return { available: true, data };
  } catch (error) {
    const message = error instanceof Error ? error.message : 'unknown error';
    return { available: false, error: message };
  }
}
