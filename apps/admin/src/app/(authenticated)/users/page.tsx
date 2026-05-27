/**
 * Users — admin list page.
 *
 * Server component that fetches the first page of users from
 * `GET /api/v1/users?limit=20` (docs/05-admin-api.md §2.8) and hands them to
 * the client-side `<UsersList>` for filtering, search, and row navigation.
 *
 * The REST endpoint may not be merged at the time this page lands (PR #78 is
 * concurrent), so the fetch degrades gracefully:
 *   - 404 / network failure  → render the empty state with a soft notice
 *   - non-array payload      → render the empty state silently
 *
 * This keeps the admin shell navigable in environments where the API is
 * still being scaffolded. Real loading/error states ship with `<ResourceList>`
 * in issue #25.
 *
 * Out of scope here per the issue: role assignment UI, bulk actions, server
 * pagination beyond the first 20 rows, and the create/invite form (just a
 * placeholder route).
 */
import type { ReactElement } from 'react';
import { serverApiFetch } from '@/lib/server-api';
import { UsersList } from './UsersList';
import type { AdminUser, UsersListResponse } from './types';

// The list reflects live data; opt out of static caching so the rendered
// HTML doesn't go stale between deploys.
export const dynamic = 'force-dynamic';

/**
 * Pluck the user array out of whichever response shape the API returns.
 * The contract isn't frozen yet, so we accept the three common envelopes.
 */
function extractUsers(payload: unknown): AdminUser[] {
  if (Array.isArray(payload)) return payload as AdminUser[];
  if (!payload || typeof payload !== 'object') return [];
  const obj = payload as UsersListResponse;
  if (Array.isArray(obj.users)) return obj.users;
  if (Array.isArray(obj.data)) return obj.data;
  if (Array.isArray(obj.items)) return obj.items;
  return [];
}

interface FetchResult {
  users: AdminUser[];
  error?: string;
}

/**
 * Server-side fetch — runs on the Next.js server, not in the browser, so we
 * can't reuse the browser-oriented `apiRequest` helper (which sends cookies
 * via `credentials: 'include'`). `serverApiFetch` forwards the inbound
 * session cookie via `next/headers`, so the API auth middleware sees the
 * operator instead of returning 401 to an anonymous request. Failures
 * still degrade to the empty state rather than crashing the page.
 */
async function fetchUsers(): Promise<FetchResult> {
  try {
    const res = await serverApiFetch('/api/v1/users?limit=20');
    if (!res.ok) {
      return { users: [], error: `HTTP ${res.status}` };
    }
    const payload = (await res.json()) as unknown;
    return { users: extractUsers(payload) };
  } catch (err) {
    const message = err instanceof Error ? err.message : 'network error';
    return { users: [], error: message };
  }
}

export default async function UsersPage(): Promise<ReactElement> {
  const { users, error } = await fetchUsers();
  return <UsersList users={users} fetchError={error} />;
}
