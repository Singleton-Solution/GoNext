/**
 * /users/[id] — admin single-user detail.
 *
 * Server entry-point: synthesises a placeholder `AdminUser` from the route
 * id so the surface is browsable in environments where `GET /api/v1/users/:id`
 * isn't deployed yet (the endpoint is tracked alongside #331 — the list
 * page). When the backend exists, swap the `synthesiseUser` call for a
 * `fetchUser` helper analogous to the list page; the `<UserDetail>` client
 * component contract doesn't change.
 *
 * The audit-log is similarly stubbed: a tiny preset of recent events that
 * exercises the audit timeline rendering. Real audit data will come from
 * `GET /api/v1/audit?subject_id=…` in a follow-up.
 */
import type { ReactElement } from 'react';

import { UserDetail, type AuditEvent } from './UserDetail';
import type { AdminUser } from '../types';

export const dynamic = 'force-dynamic';

function synthesiseUser(id: string): AdminUser {
  // Lightweight fallback so the surface renders for any id during local
  // dev / staging. Production swaps this for the real fetch.
  return {
    id,
    handle: `user-${id.slice(0, 6)}`,
    email: `user-${id.slice(0, 6)}@example.com`,
    display_name: '',
    status: 'active',
    role: 'author',
    last_seen_at: null,
  };
}

function synthesiseAudit(id: string): AuditEvent[] {
  const now = Date.now();
  return [
    {
      id: `${id}-evt-1`,
      at: new Date(now - 1000 * 60 * 12).toISOString(),
      action: 'Signed in',
      source: '203.0.113.42 · Firefox / macOS',
    },
    {
      id: `${id}-evt-2`,
      at: new Date(now - 1000 * 60 * 60 * 5).toISOString(),
      action: 'Updated profile',
      source: 'admin web',
    },
    {
      id: `${id}-evt-3`,
      at: new Date(now - 1000 * 60 * 60 * 24 * 3).toISOString(),
      action: 'Generated personal access token',
      source: 'gnp_AbCdEf…',
    },
  ];
}

export default async function UserDetailPage({
  params,
}: {
  params: Promise<{ id: string }>;
}): Promise<ReactElement> {
  const { id } = await params;
  const user = synthesiseUser(id);
  const audit = synthesiseAudit(id);
  return <UserDetail user={user} audit={audit} />;
}
