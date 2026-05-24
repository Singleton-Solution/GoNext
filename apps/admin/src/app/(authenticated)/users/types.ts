/**
 * Shared types for the admin users surface.
 *
 * Mirrors the user shape returned by `GET /api/v1/users` per
 * docs/05-admin-api.md §2.8 and §6. Kept colocated under the route so the
 * page is fully self-contained until issue #25 lands the shared
 * `<ResourceList>` primitives we can lift these into.
 */

/** Lifecycle status surfaced in the list. `all` is a filter sentinel, not a server value. */
export type UserStatus = 'active' | 'suspended' | 'deleted';

/** Canonical roles per docs/06-auth-permissions.md. */
export type UserRole =
  | 'super_admin'
  | 'admin'
  | 'editor'
  | 'author'
  | 'contributor'
  | 'subscriber';

export interface AdminUser {
  /** Server-generated identifier (ULID/UUID). */
  id: string;
  /** Unique short handle, e.g. `alice`. */
  handle: string;
  /** Full email — the list view renders a partial mask, never the raw value. */
  email: string;
  /** Human-facing display name. May be empty for invited-but-not-onboarded users. */
  display_name: string;
  status: UserStatus;
  /** Highest-privilege role assigned to the user. */
  role: UserRole;
  /** RFC 3339 timestamp of the user's last authenticated session. Null = never seen. */
  last_seen_at: string | null;
}

/** Filter sentinel meaning "no filter applied". */
export const ALL = 'all' as const;

/** Tuple unions used by the filter controls in the client component. */
export type StatusFilter = UserStatus | typeof ALL;
export type RoleFilter = UserRole | typeof ALL;

/**
 * Loose envelope for `GET /api/v1/users`. The endpoint may not be merged yet
 * (PR #78), so the page tolerates a few common response shapes and falls
 * back to an empty list otherwise.
 */
export interface UsersListResponse {
  users?: AdminUser[];
  data?: AdminUser[];
  items?: AdminUser[];
  total?: number;
}
