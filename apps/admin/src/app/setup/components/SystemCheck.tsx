'use client';

/**
 * SystemCheck — renders a friendly summary of the deployment's readiness
 * for the install wizard.
 *
 * For a fresh install the meaningful checks are:
 *
 *   - The API responded to /setup/status at all (no, you can't render
 *     this page if it didn't — the parent component would have already
 *     rendered an error state — but we still show "API reachable" as a
 *     reassurance for the operator).
 *   - The install lock is NOT yet set. If it were, the parent already
 *     redirected to /admin/login.
 *
 * Subsequent issues will extend this surface with real probes (DB
 * write permission, Redis ping, etc.) once the corresponding endpoints
 * land. For now the component is intentionally lean.
 */
import type { ReactElement } from 'react';
import type { SetupStatus } from '../types';

export interface SystemCheckProps {
  status: SetupStatus;
}

interface CheckRow {
  label: string;
  ok: boolean;
  detail?: string;
}

/**
 * Renders the readiness rows. Each row is keyed off a boolean so the
 * UI can show a colored dot per axis; nothing here makes blocking
 * decisions (the wizard does that).
 */
export function SystemCheck({ status }: SystemCheckProps): ReactElement {
  const checks: CheckRow[] = [
    {
      label: 'API reachable',
      ok: true,
      detail: 'GET /api/v1/setup/status returned 200',
    },
    {
      label: 'Installation lock',
      ok: !status.installation_completed,
      detail: status.installation_completed
        ? 'GoNext is already installed.'
        : 'Ready to install.',
    },
    {
      label: 'Users present',
      ok: status.user_count === 0,
      detail:
        status.user_count === 0
          ? 'No users yet — bootstrap will create the first one.'
          : `${status.user_count} user(s) present.`,
    },
  ];
  return (
    <ul className="setup-checks" aria-label="System check">
      {checks.map((c) => (
        <li
          key={c.label}
          className={c.ok ? 'setup-checks__row setup-checks__row--ok' : 'setup-checks__row'}
        >
          <span
            className="setup-checks__dot"
            aria-hidden="true"
            style={{ background: c.ok ? '#16a34a' : '#dc2626' }}
          />
          <span className="setup-checks__label">{c.label}</span>
          {c.detail ? <span className="setup-checks__detail muted">{c.detail}</span> : null}
        </li>
      ))}
    </ul>
  );
}
