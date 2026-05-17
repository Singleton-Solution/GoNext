'use client';

/**
 * UsersList — interactive table for the admin users page.
 *
 * Receives a server-fetched initial slice of users and provides client-side
 * search (handle/email substring) and filter (status, role) over that slice.
 *
 * Intentionally self-contained: the shared `<ResourceList>` primitives land
 * in issue #25. To avoid touching a moving target we duplicate the small
 * primitives we need (toolbar, table, status badge) inline here. Consolidation
 * is a separate cleanup once #25 is merged.
 *
 * Styling: per the task scope this page uses inline styles only — no Tailwind,
 * no new global classes — so it stays trivially reviewable in isolation from
 * the design-system extraction (#34).
 */
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { useMemo, useState, type CSSProperties, type ReactElement } from 'react';
import { maskEmail } from './mask';
import {
  ALL,
  type AdminUser,
  type RoleFilter,
  type StatusFilter,
  type UserRole,
  type UserStatus,
} from './types';

interface UsersListProps {
  users: AdminUser[];
  /** Truthy when the API call rejected; lets us show a soft notice above the table. */
  fetchError?: string;
}

const STATUS_OPTIONS: ReadonlyArray<{ value: StatusFilter; label: string }> = [
  { value: ALL, label: 'All statuses' },
  { value: 'active', label: 'Active' },
  { value: 'suspended', label: 'Suspended' },
  { value: 'deleted', label: 'Deleted' },
];

const ROLE_OPTIONS: ReadonlyArray<{ value: RoleFilter; label: string }> = [
  { value: ALL, label: 'All roles' },
  { value: 'super_admin', label: 'Super admin' },
  { value: 'admin', label: 'Admin' },
  { value: 'editor', label: 'Editor' },
  { value: 'author', label: 'Author' },
  { value: 'contributor', label: 'Contributor' },
  { value: 'subscriber', label: 'Subscriber' },
];

const STATUS_BADGE: Record<UserStatus, { bg: string; fg: string; label: string }> = {
  active: { bg: '#dcfce7', fg: '#166534', label: 'Active' },
  suspended: { bg: '#fee2e2', fg: '#991b1b', label: 'Suspended' },
  deleted: { bg: '#e5e7eb', fg: '#4b5563', label: 'Deleted' },
};

const ROLE_LABEL: Record<UserRole, string> = {
  super_admin: 'Super admin',
  admin: 'Admin',
  editor: 'Editor',
  author: 'Author',
  contributor: 'Contributor',
  subscriber: 'Subscriber',
};

/**
 * Format a last-seen timestamp into a short, locale-friendly string. Renders
 * an em-dash for users who have never signed in (last_seen_at === null).
 */
function formatLastSeen(value: string | null): string {
  if (!value) return '—';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  });
}

// ---------------------------------------------------------------------------
// Styles
// ---------------------------------------------------------------------------

const styles: Record<string, CSSProperties> = {
  header: {
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'space-between',
    gap: 16,
    marginBottom: 16,
  },
  title: {
    margin: 0,
    fontSize: 20,
    fontWeight: 600,
  },
  inviteBtn: {
    display: 'inline-flex',
    alignItems: 'center',
    gap: 6,
    background: 'var(--color-accent, #2563eb)',
    color: '#ffffff',
    border: 0,
    borderRadius: 6,
    padding: '8px 14px',
    fontWeight: 500,
    fontSize: 14,
    textDecoration: 'none',
  },
  toolbar: {
    display: 'flex',
    flexWrap: 'wrap',
    gap: 12,
    marginBottom: 12,
    padding: 12,
    background: 'var(--color-surface, #ffffff)',
    border: '1px solid var(--color-border, #e4e6ea)',
    borderRadius: 6,
  },
  searchInput: {
    flex: '1 1 240px',
    minWidth: 200,
    padding: '6px 10px',
    border: '1px solid var(--color-border, #e4e6ea)',
    borderRadius: 6,
    fontSize: 14,
    background: '#ffffff',
  },
  select: {
    padding: '6px 10px',
    border: '1px solid var(--color-border, #e4e6ea)',
    borderRadius: 6,
    fontSize: 14,
    background: '#ffffff',
    minWidth: 140,
  },
  tableWrap: {
    background: 'var(--color-surface, #ffffff)',
    border: '1px solid var(--color-border, #e4e6ea)',
    borderRadius: 6,
    overflow: 'hidden',
  },
  table: {
    width: '100%',
    borderCollapse: 'collapse',
    fontSize: 14,
  },
  th: {
    textAlign: 'left',
    padding: '10px 12px',
    fontWeight: 600,
    color: 'var(--color-text-muted, #6b7280)',
    background: '#fafafa',
    borderBottom: '1px solid var(--color-border, #e4e6ea)',
    fontSize: 12,
    textTransform: 'uppercase',
    letterSpacing: '0.04em',
  },
  td: {
    padding: '12px',
    borderBottom: '1px solid var(--color-border, #e4e6ea)',
    verticalAlign: 'middle',
  },
  row: {
    cursor: 'pointer',
  },
  handle: {
    fontWeight: 500,
    color: 'var(--color-text, #1c2024)',
  },
  muted: {
    color: 'var(--color-text-muted, #6b7280)',
  },
  empty: {
    padding: '40px 16px',
    textAlign: 'center',
    color: 'var(--color-text-muted, #6b7280)',
  },
  errorBanner: {
    padding: '10px 12px',
    marginBottom: 12,
    border: '1px solid #fecaca',
    background: '#fef2f2',
    color: '#991b1b',
    borderRadius: 6,
    fontSize: 13,
  },
};

function badgeStyle(status: UserStatus): CSSProperties {
  const spec = STATUS_BADGE[status];
  return {
    display: 'inline-block',
    padding: '2px 8px',
    borderRadius: 999,
    background: spec.bg,
    color: spec.fg,
    fontSize: 12,
    fontWeight: 600,
    textTransform: 'capitalize',
  };
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export function UsersList({ users, fetchError }: UsersListProps): ReactElement {
  const router = useRouter();
  const [query, setQuery] = useState('');
  const [statusFilter, setStatusFilter] = useState<StatusFilter>(ALL);
  const [roleFilter, setRoleFilter] = useState<RoleFilter>(ALL);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    return users.filter((u) => {
      if (statusFilter !== ALL && u.status !== statusFilter) return false;
      if (roleFilter !== ALL && u.role !== roleFilter) return false;
      if (q.length > 0) {
        const inHandle = u.handle.toLowerCase().includes(q);
        const inEmail = u.email.toLowerCase().includes(q);
        if (!inHandle && !inEmail) return false;
      }
      return true;
    });
  }, [users, query, statusFilter, roleFilter]);

  return (
    <section>
      <div style={styles.header}>
        <h1 style={styles.title}>Users</h1>
        <Link href="/users/new" style={styles.inviteBtn}>
          Invite user
        </Link>
      </div>

      {fetchError ? (
        <div role="alert" style={styles.errorBanner}>
          Couldn&apos;t load users from the API ({fetchError}). Showing an empty
          list — the endpoint may not be deployed yet.
        </div>
      ) : null}

      <div style={styles.toolbar} role="toolbar" aria-label="Users filters">
        <input
          type="search"
          placeholder="Search by handle or email"
          aria-label="Search users"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          style={styles.searchInput}
        />
        <select
          aria-label="Filter by status"
          value={statusFilter}
          onChange={(e) => setStatusFilter(e.target.value as StatusFilter)}
          style={styles.select}
        >
          {STATUS_OPTIONS.map((opt) => (
            <option key={opt.value} value={opt.value}>
              {opt.label}
            </option>
          ))}
        </select>
        <select
          aria-label="Filter by role"
          value={roleFilter}
          onChange={(e) => setRoleFilter(e.target.value as RoleFilter)}
          style={styles.select}
        >
          {ROLE_OPTIONS.map((opt) => (
            <option key={opt.value} value={opt.value}>
              {opt.label}
            </option>
          ))}
        </select>
      </div>

      <div style={styles.tableWrap}>
        {filtered.length === 0 ? (
          <div style={styles.empty}>
            {users.length === 0 ? 'No users yet' : 'No users match these filters'}
          </div>
        ) : (
          <table style={styles.table}>
            <thead>
              <tr>
                <th style={styles.th} scope="col">Handle</th>
                <th style={styles.th} scope="col">Email</th>
                <th style={styles.th} scope="col">Name</th>
                <th style={styles.th} scope="col">Role</th>
                <th style={styles.th} scope="col">Status</th>
                <th style={styles.th} scope="col">Last seen</th>
              </tr>
            </thead>
            <tbody>
              {filtered.map((u) => (
                <tr
                  key={u.id}
                  style={styles.row}
                  onClick={() => router.push(`/users/${u.id}`)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' || e.key === ' ') {
                      e.preventDefault();
                      router.push(`/users/${u.id}`);
                    }
                  }}
                  tabIndex={0}
                  aria-label={`Open ${u.handle}`}
                >
                  <td style={styles.td}>
                    <span style={styles.handle}>@{u.handle}</span>
                  </td>
                  <td style={styles.td}>
                    <span style={styles.muted}>{maskEmail(u.email)}</span>
                  </td>
                  <td style={styles.td}>{u.display_name || '—'}</td>
                  <td style={styles.td}>{ROLE_LABEL[u.role] ?? u.role}</td>
                  <td style={styles.td}>
                    <span
                      data-status={u.status}
                      style={badgeStyle(u.status)}
                    >
                      {STATUS_BADGE[u.status].label}
                    </span>
                  </td>
                  <td style={styles.td}>
                    <span style={styles.muted}>{formatLastSeen(u.last_seen_at)}</span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </section>
  );
}
