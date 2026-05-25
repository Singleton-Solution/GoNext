'use client';

/**
 * UserDetail — single-user admin surface, brand-tokenized.
 *
 * Renders one user's profile + role select + status switch + audit log.
 * The component is intentionally presentational: the parent server page
 * hands it a `user` snapshot and a list of recent events; we own the
 * local UI state (the editor's draft role / status), and call back when
 * the operator clicks Save.
 *
 * Why a client island? Two reasons:
 *  - the role <Select> and status switch are interactive,
 *  - the page needs an Avatar component (Radix), which is client-only.
 *
 * The save handler isn't wired to a real PATCH endpoint yet — the
 * underlying admin user-update API is tracked in a follow-up. The
 * handler is a no-op that flashes a "Saved" pill, which keeps this
 * screen demoable in environments without the backend.
 */
import { useState, type ReactElement } from 'react';
import Link from 'next/link';
import {
  ArrowLeft,
  Check,
  CircleAlert,
  UserRound,
  ShieldCheck,
} from 'lucide-react';

import { Avatar, AvatarFallback } from '@/components/ui/avatar';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent } from '@/components/ui/card';
import { Headline } from '@/components/ui/headline';
import { Label } from '@/components/ui/label';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Switch } from '@/components/ui/switch';
import { cn } from '@/lib/utils';

import type { AdminUser, UserRole } from '../types';

export interface AuditEvent {
  id: string;
  /** RFC 3339 timestamp. */
  at: string;
  /** Short human-readable action — e.g. "Signed in", "Changed role". */
  action: string;
  /** Optional source descriptor — IP, browser, script. */
  source?: string;
}

interface UserDetailProps {
  user: AdminUser;
  /** Most-recent audit events for this user, newest first. */
  audit: ReadonlyArray<AuditEvent>;
}

const ROLE_LABEL: Record<UserRole, string> = {
  super_admin: 'Super admin',
  admin: 'Admin',
  editor: 'Editor',
  author: 'Author',
  contributor: 'Contributor',
  subscriber: 'Subscriber',
};

const ROLE_ORDER: ReadonlyArray<UserRole> = [
  'super_admin',
  'admin',
  'editor',
  'author',
  'contributor',
  'subscriber',
];

function initialsFor(user: AdminUser): string {
  const source = user.display_name?.trim() || user.handle;
  const parts = source.split(/\s+/).filter(Boolean);
  if (parts.length === 0) return '··';
  if (parts.length === 1) return parts[0].slice(0, 2).toUpperCase();
  return (parts[0][0] + parts[parts.length - 1][0]).toUpperCase();
}

function formatDate(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  });
}

export function UserDetail({ user, audit }: UserDetailProps): ReactElement {
  const [role, setRole] = useState<UserRole>(user.role);
  const [active, setActive] = useState<boolean>(user.status === 'active');
  const [saved, setSaved] = useState<boolean>(false);

  const dirty = role !== user.role || active !== (user.status === 'active');

  const onSave = (): void => {
    // PATCH /api/v1/users/:id lands with the role-edit issue. For now we
    // optimistically flash a "Saved" indicator so the surface is demoable.
    setSaved(true);
    window.setTimeout(() => setSaved(false), 1800);
  };

  return (
    <section className="flex flex-col gap-6">
      <div className="flex flex-col gap-2">
        <Link
          href="/users"
          className="inline-flex items-center gap-1.5 font-sans text-xs font-medium text-fg-muted transition-colors hover:text-emerald-deep"
        >
          <ArrowLeft className="h-3.5 w-3.5" aria-hidden="true" />
          Back to users
        </Link>
        <Headline as="h1" size="sub">
          @{user.handle}, <em>in detail</em>.
        </Headline>
      </div>

      <Card className="border-border bg-paper-2 shadow-xs">
        <CardContent className="flex flex-col gap-5 p-6 sm:flex-row sm:items-start">
          <Avatar className="h-20 w-20 border-2 border-paper-3">
            <AvatarFallback className="text-lg" aria-hidden="true">
              {initialsFor(user)}
            </AvatarFallback>
          </Avatar>
          <div className="flex flex-1 flex-col gap-2">
            <div className="flex flex-wrap items-center gap-2">
              <span className="font-display text-xl font-bold text-ink">
                {user.display_name || `@${user.handle}`}
              </span>
              <Badge variant={active ? 'success' : 'danger'} dot>
                {active ? 'Active' : 'Suspended'}
              </Badge>
            </div>
            <dl className="grid grid-cols-1 gap-x-6 gap-y-1 text-sm sm:grid-cols-[max-content_1fr]">
              <dt className="font-sans text-xs uppercase tracking-[0.06em] text-fg-subtle">
                Email
              </dt>
              <dd className="font-mono text-fg-muted">{user.email}</dd>
              <dt className="font-sans text-xs uppercase tracking-[0.06em] text-fg-subtle">
                Handle
              </dt>
              <dd className="font-mono text-fg-muted">@{user.handle}</dd>
              <dt className="font-sans text-xs uppercase tracking-[0.06em] text-fg-subtle">
                User ID
              </dt>
              <dd className="font-mono text-fg-muted">{user.id}</dd>
            </dl>
          </div>
        </CardContent>
      </Card>

      <Card className="border-border bg-paper-2 shadow-xs">
        <CardContent className="flex flex-col gap-5 p-6">
          <div className="flex items-center gap-2">
            <ShieldCheck
              className="h-4 w-4 text-emerald-deep"
              aria-hidden="true"
            />
            <h2 className="font-display text-sm font-bold uppercase tracking-[0.06em] text-ink">
              Permissions
            </h2>
          </div>

          <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
            <div className="flex flex-col gap-2">
              <Label htmlFor="user-role">Role</Label>
              <Select
                value={role}
                onValueChange={(v) => setRole(v as UserRole)}
              >
                <SelectTrigger id="user-role" aria-label="User role">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {ROLE_ORDER.map((r) => (
                    <SelectItem key={r} value={r}>
                      {ROLE_LABEL[r]}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <p className="font-sans text-xs text-fg-subtle">
                Role gates every capability — pick the smallest grant that
                gets the job done.
              </p>
            </div>

            <div className="flex flex-col gap-2">
              <Label htmlFor="user-status">Status</Label>
              <div className="flex items-center gap-3 rounded-md border border-border bg-paper-3 px-3 py-2">
                <Switch
                  id="user-status"
                  checked={active}
                  onCheckedChange={(v) => setActive(Boolean(v))}
                  aria-label="Account active"
                />
                <span className="font-sans text-sm text-ink">
                  {active ? 'Account is active' : 'Account is disabled'}
                </span>
              </div>
              <p className="font-sans text-xs text-fg-subtle">
                Disabling an account ends sessions and blocks sign-in
                immediately.
              </p>
            </div>
          </div>

          <div className="flex items-center justify-end gap-3 border-t border-border pt-4">
            {saved ? (
              <span
                className="inline-flex items-center gap-1.5 font-sans text-xs font-medium text-emerald-deep"
                role="status"
              >
                <Check className="h-3.5 w-3.5" aria-hidden="true" />
                Saved
              </span>
            ) : null}
            <Button
              variant="emerald"
              onClick={onSave}
              disabled={!dirty}
              data-testid="user-detail-save"
            >
              Save changes
            </Button>
          </div>
        </CardContent>
      </Card>

      <Card className="border-border bg-paper-2 shadow-xs">
        <CardContent className="flex flex-col gap-4 p-6">
          <div className="flex items-center gap-2">
            <UserRound
              className="h-4 w-4 text-fg-muted"
              aria-hidden="true"
            />
            <h2 className="font-display text-sm font-bold uppercase tracking-[0.06em] text-ink">
              Recent activity
            </h2>
          </div>

          {audit.length === 0 ? (
            <div
              className="flex items-center gap-2 rounded-md border border-dashed border-border bg-paper-3 px-3 py-4 font-sans text-sm text-fg-muted"
              data-testid="audit-empty"
            >
              <CircleAlert className="h-4 w-4" aria-hidden="true" />
              No recorded events yet.
            </div>
          ) : (
            <ul
              className="flex flex-col"
              data-testid="audit-log"
              aria-label="Recent activity"
            >
              {audit.map((ev, idx) => (
                <li
                  key={ev.id}
                  className={cn(
                    'flex items-start gap-3 py-2.5',
                    idx !== audit.length - 1 && 'border-b border-border',
                  )}
                >
                  <span
                    aria-hidden="true"
                    className="mt-1.5 h-1.5 w-1.5 flex-shrink-0 rounded-pill bg-emerald-deep"
                  />
                  <div className="flex flex-1 flex-col gap-0.5">
                    <span className="font-sans text-sm text-ink">
                      {ev.action}
                    </span>
                    {ev.source ? (
                      <span className="font-mono text-xs text-fg-subtle">
                        {ev.source}
                      </span>
                    ) : null}
                  </div>
                  <span className="font-mono text-xs text-fg-muted">
                    {formatDate(ev.at)}
                  </span>
                </li>
              ))}
            </ul>
          )}
        </CardContent>
      </Card>
    </section>
  );
}
