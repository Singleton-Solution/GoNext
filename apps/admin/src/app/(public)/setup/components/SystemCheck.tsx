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
 *
 * Visual treatment follows docs/design/ui_kits/onboarding/index.html:
 * each row is a small bordered tile on paper-2 with a Lucide
 * check-circle (emerald-soft pill) for an ok state, and an alert-circle
 * (danger-soft pill) for a fail. The detail text sits in fg-muted Geist,
 * the label in ink Geist 500 — matches the .step / .extras pattern from
 * the onboarding hero.
 */
import type { ReactElement } from 'react';
import { AlertCircle, CheckCircle2 } from 'lucide-react';

import { cn } from '@/lib/utils';
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
 * UI can show a Lucide icon badge per axis; nothing here makes blocking
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
    <ul
      className={cn(
        'setup-checks',
        'mt-6 flex flex-col gap-2 list-none p-0',
      )}
      aria-label="System check"
    >
      {checks.map((c) => (
        <li
          key={c.label}
          className={cn(
            'setup-checks__row',
            c.ok && 'setup-checks__row--ok',
            'flex items-start gap-3 rounded-md border border-border bg-paper px-4 py-3',
          )}
        >
          <span
            className={cn(
              'setup-checks__dot',
              'mt-[1px] flex h-5 w-5 shrink-0 items-center justify-center rounded-pill',
              c.ok
                ? 'bg-emerald-soft text-emerald-deep'
                : 'bg-danger-soft text-danger',
            )}
            aria-hidden="true"
          >
            {c.ok ? (
              <CheckCircle2 width={14} height={14} strokeWidth={2.25} />
            ) : (
              <AlertCircle width={14} height={14} strokeWidth={2.25} />
            )}
          </span>
          <span className="flex flex-1 flex-col gap-[2px]">
            <span
              className={cn(
                'setup-checks__label',
                'font-sans text-sm font-medium text-ink',
              )}
            >
              {c.label}
            </span>
            {c.detail ? (
              <span
                className={cn(
                  'setup-checks__detail',
                  'font-sans text-xs text-fg-muted',
                )}
              >
                {c.detail}
              </span>
            ) : null}
          </span>
        </li>
      ))}
    </ul>
  );
}
