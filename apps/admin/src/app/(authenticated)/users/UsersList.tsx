'use client';

/**
 * UsersList — admin users surface, restyled against the Living-Systems brand.
 *
 * Two changes vs. the original (PR #331) implementation:
 *
 *   1. Visual language: the previously-inline `<h1>`/`<table>`/`<select>` are
 *      replaced with the brand primitives from `src/components/ui/`. The page
 *      head is a `<Headline>` carrying the italic-accent rule
 *      ("All *users*."), filters and search live on a paper-3 toolbar surface,
 *      and the table rows render an Avatar + lavender/emerald role chip per
 *      the handoff.
 *
 *   2. Self-contained: data-shape, mask, and behaviour are intentionally
 *      unchanged so existing UsersList tests keep covering the substantive
 *      behaviour. Brand additions get their own snapshot tests below.
 *
 * The italic-accent colour swap matches the rules in
 * `docs/design/colors_and_type.css`: emerald-deep on cream surfaces (this
 * page) — same rule that fires on every other admin headline in the app.
 */
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { UserRound, Mail } from 'lucide-react';
import { useMemo, useState, type ReactElement } from 'react';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Avatar, AvatarFallback } from '@/components/ui/avatar';
import { Headline } from '@/components/ui/headline';
import { Input } from '@/components/ui/input';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { cn } from '@/lib/utils';

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

/**
 * Status pill colour map — the design language carries semantic colour in
 * `--success-*` / `--danger-*` / `--paper-3`. We keep the legacy
 * `data-status` attribute on the rendered chip so existing tests still pass.
 */
const STATUS_BADGE: Record<
  UserStatus,
  { variant: 'success' | 'danger' | 'default'; label: string }
> = {
  active: { variant: 'success', label: 'Active' },
  suspended: { variant: 'danger', label: 'Suspended' },
  deleted: { variant: 'default', label: 'Deleted' },
};

/**
 * Role-to-chip map for the brand language. The handoff calls for emerald
 * on admin roles and lavender on editor; the rest fall through to the muted
 * default surface so the active-vs-passive role contrast is visible at a
 * glance.
 */
const ROLE_BADGE: Record<
  UserRole,
  { variant: 'emerald' | 'lavender' | 'default'; label: string }
> = {
  super_admin: { variant: 'emerald', label: 'Super admin' },
  admin: { variant: 'emerald', label: 'Admin' },
  editor: { variant: 'lavender', label: 'Editor' },
  author: { variant: 'default', label: 'Author' },
  contributor: { variant: 'default', label: 'Contributor' },
  subscriber: { variant: 'default', label: 'Subscriber' },
};

/**
 * Format a last-seen timestamp into a short, locale-friendly string. Renders
 * an em-dash for users who have never signed in.
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

/**
 * Two-character initials from a display name (falling back to handle). Used
 * inside the AvatarFallback so empty avatars still feel intentional.
 */
function initialsFor(user: AdminUser): string {
  const source = user.display_name?.trim() || user.handle;
  const parts = source.split(/\s+/).filter(Boolean);
  if (parts.length === 0) return '··';
  if (parts.length === 1) return parts[0].slice(0, 2).toUpperCase();
  return (parts[0][0] + parts[parts.length - 1][0]).toUpperCase();
}

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
    <section className="flex flex-col gap-6">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div className="flex flex-col gap-2">
          <span className="font-sans text-xs font-medium uppercase tracking-[0.12em] text-emerald-deep">
            People
          </span>
          <Headline as="h1" size="sub">
            All <em>users</em>.
          </Headline>
          <p className="font-sans text-sm text-fg-muted">
            Everyone who can sign in — by role, status, last seen.
          </p>
        </div>
        <Button asChild variant="emerald" size="default">
          <Link href="/users/new">
            <UserRound className="h-4 w-4" aria-hidden="true" />
            Invite user
          </Link>
        </Button>
      </div>

      {fetchError ? (
        <div
          role="alert"
          className="rounded-md border border-danger/30 bg-danger-soft px-3 py-2 font-sans text-sm text-danger"
        >
          Couldn&apos;t load users from the API ({fetchError}). Showing an empty
          list — the endpoint may not be deployed yet.
        </div>
      ) : null}

      <div
        className="flex flex-wrap gap-3 rounded-lg border border-border bg-paper-3 p-3"
        role="toolbar"
        aria-label="Users filters"
      >
        <div className="relative min-w-[240px] flex-1">
          <Input
            type="search"
            placeholder="Search by handle or email"
            aria-label="Search users"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            className="bg-paper-2"
          />
        </div>
        <div className="w-[180px]">
          <Select
            value={statusFilter}
            onValueChange={(v) => setStatusFilter(v as StatusFilter)}
          >
            <SelectTrigger
              aria-label="Filter by status"
              className="bg-paper-2"
            >
              <SelectValue placeholder="All statuses" />
            </SelectTrigger>
            <SelectContent>
              {STATUS_OPTIONS.map((opt) => (
                <SelectItem key={opt.value} value={opt.value}>
                  {opt.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <div className="w-[180px]">
          <Select
            value={roleFilter}
            onValueChange={(v) => setRoleFilter(v as RoleFilter)}
          >
            <SelectTrigger
              aria-label="Filter by role"
              className="bg-paper-2"
            >
              <SelectValue placeholder="All roles" />
            </SelectTrigger>
            <SelectContent>
              {ROLE_OPTIONS.map((opt) => (
                <SelectItem key={opt.value} value={opt.value}>
                  {opt.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      </div>

      <div className="overflow-hidden rounded-lg border border-border bg-paper-2 shadow-xs">
        {filtered.length === 0 ? (
          <div className="px-4 py-10 text-center font-sans text-sm text-fg-muted">
            {users.length === 0 ? 'No users yet' : 'No users match these filters'}
          </div>
        ) : (
          <table className="w-full border-collapse font-sans text-sm">
            <thead>
              <tr className="border-b border-border bg-paper-3">
                <th
                  className="px-4 py-3 text-left text-xs font-medium uppercase tracking-[0.06em] text-fg-subtle"
                  scope="col"
                >
                  User
                </th>
                <th
                  className="px-4 py-3 text-left text-xs font-medium uppercase tracking-[0.06em] text-fg-subtle"
                  scope="col"
                >
                  Email
                </th>
                <th
                  className="px-4 py-3 text-left text-xs font-medium uppercase tracking-[0.06em] text-fg-subtle"
                  scope="col"
                >
                  Role
                </th>
                <th
                  className="px-4 py-3 text-left text-xs font-medium uppercase tracking-[0.06em] text-fg-subtle"
                  scope="col"
                >
                  Status
                </th>
                <th
                  className="px-4 py-3 text-left text-xs font-medium uppercase tracking-[0.06em] text-fg-subtle"
                  scope="col"
                >
                  Last seen
                </th>
              </tr>
            </thead>
            <tbody>
              {filtered.map((u) => {
                const role = ROLE_BADGE[u.role] ?? {
                  variant: 'default' as const,
                  label: u.role,
                };
                const status = STATUS_BADGE[u.status];
                return (
                  <tr
                    key={u.id}
                    className={cn(
                      'cursor-pointer border-b border-border last:border-b-0',
                      'transition-colors duration-[160ms]',
                      'hover:bg-paper-3 focus-within:bg-paper-3',
                    )}
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
                    <td className="px-4 py-3 align-middle">
                      <div className="flex items-center gap-3">
                        <Avatar className="h-8 w-8">
                          <AvatarFallback aria-hidden="true">
                            {initialsFor(u)}
                          </AvatarFallback>
                        </Avatar>
                        <div className="flex flex-col leading-tight">
                          <span className="font-display text-sm font-bold text-ink">
                            @{u.handle}
                          </span>
                          <span className="text-xs text-fg-muted">
                            {u.display_name || '—'}
                          </span>
                        </div>
                      </div>
                    </td>
                    <td className="px-4 py-3 align-middle text-fg-muted">
                      <span className="inline-flex items-center gap-1.5">
                        <Mail
                          className="h-3.5 w-3.5 opacity-70"
                          aria-hidden="true"
                        />
                        {maskEmail(u.email)}
                      </span>
                    </td>
                    <td className="px-4 py-3 align-middle">
                      <Badge variant={role.variant} data-role={u.role}>
                        {role.label}
                      </Badge>
                    </td>
                    <td className="px-4 py-3 align-middle">
                      <Badge
                        variant={status.variant}
                        data-status={u.status}
                        dot={u.status === 'active'}
                      >
                        {status.label}
                      </Badge>
                    </td>
                    <td className="px-4 py-3 align-middle font-mono text-xs text-fg-muted">
                      {formatLastSeen(u.last_seen_at)}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
      </div>
    </section>
  );
}
