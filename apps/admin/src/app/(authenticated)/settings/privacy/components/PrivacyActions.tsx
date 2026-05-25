'use client';

/**
 * PrivacyActions — the interactive surface of /settings/privacy.
 *
 * Two cards, one per action. The card surfaces are the paper-2
 * pattern shared across /settings; the destructive card carries a
 * subtle red border so the operator never confuses it for the safe
 * export action.
 *
 * State machine:
 *
 *   exportState:
 *     idle  → loading → success({ jobId, pollUrl })
 *                    \-> error(message)
 *
 *   deleteState:
 *     idle  → confirming → loading → done
 *                       \-> error(message)
 *
 * The export does not poll on its own; the operator follows the
 * polling URL surfaced in the success banner. That URL is wired by a
 * follow-up issue and lives at /api/v1/account/data/export/{jobId}.
 */

import type { FormEvent, ReactElement } from 'react';
import { useState } from 'react';
import { AlertTriangle, Download, Loader2, Trash2 } from 'lucide-react';

import { ApiError, api } from '@/lib/api-client';
import { Button } from '@/components/ui/button';

interface ExportSuccess {
  jobId: string;
  pollUrl: string;
}

type ExportState =
  | { kind: 'idle' }
  | { kind: 'loading' }
  | { kind: 'success'; data: ExportSuccess }
  | { kind: 'error'; message: string };

type DeleteState =
  | { kind: 'idle' }
  | { kind: 'confirming' }
  | { kind: 'loading' }
  | { kind: 'done' }
  | { kind: 'error'; message: string };

interface ExportResponse {
  job_id: string;
  status: string;
  poll_url: string;
  created_at: string;
}

interface DeleteResponse {
  anonymized_at: string;
  scheduled_purge_at: string;
}

function describeError(err: unknown): string {
  if (err instanceof ApiError) {
    return err.message || `Request failed with HTTP ${err.status}.`;
  }
  if (err instanceof Error) return err.message;
  return 'Unknown error.';
}

export function PrivacyActions(): ReactElement {
  const [exportState, setExportState] = useState<ExportState>({ kind: 'idle' });
  const [deleteState, setDeleteState] = useState<DeleteState>({ kind: 'idle' });
  const [password, setPassword] = useState('');
  const [passwordConfirm, setPasswordConfirm] = useState('');

  async function handleExport(): Promise<void> {
    setExportState({ kind: 'loading' });
    try {
      const resp = await api.get<ExportResponse>('/api/v1/account/data/export');
      setExportState({
        kind: 'success',
        data: { jobId: resp.job_id, pollUrl: resp.poll_url },
      });
    } catch (err) {
      setExportState({ kind: 'error', message: describeError(err) });
    }
  }

  async function handleDelete(e: FormEvent<HTMLFormElement>): Promise<void> {
    e.preventDefault();
    if (password !== passwordConfirm) {
      setDeleteState({
        kind: 'error',
        message: 'Passwords do not match. Type the same password twice to confirm.',
      });
      return;
    }
    setDeleteState({ kind: 'loading' });
    try {
      await api.post<DeleteResponse>('/api/v1/account/data/delete', {
        password,
        password_confirm: passwordConfirm,
      });
      setDeleteState({ kind: 'done' });
      // Clear the password material from React state ASAP — the
      // user agent's BFCache may keep the form alive after navigation.
      setPassword('');
      setPasswordConfirm('');
    } catch (err) {
      setDeleteState({ kind: 'error', message: describeError(err) });
    }
  }

  return (
    <div className="flex flex-col gap-6">
      {/* --- Export card -------------------------------------------- */}
      <article className="paper-2 flex flex-col gap-4 rounded-lg border border-border bg-paper p-6">
        <header className="flex flex-col gap-1">
          <h2 className="font-serif text-xl font-semibold text-fg">
            Download your data
          </h2>
          <p className="font-sans text-sm text-fg-muted">
            Generates a ZIP containing your profile, posts you authored,
            comments, uploaded media, and your audit-log rows. Limit: one
            export per day per account.
          </p>
        </header>

        <div className="flex items-center gap-3">
          <Button
            variant="emerald"
            onClick={handleExport}
            disabled={exportState.kind === 'loading'}
            data-testid="privacy-export-cta"
          >
            {exportState.kind === 'loading' ? (
              <>
                <Loader2 className="h-4 w-4 animate-spin" aria-hidden="true" />
                Queueing export
              </>
            ) : (
              <>
                <Download className="h-4 w-4" aria-hidden="true" />
                Export my data
              </>
            )}
          </Button>
        </div>

        {exportState.kind === 'success' && (
          <div
            className="rounded-md border border-emerald-deep/40 bg-emerald-light/20 p-4 text-sm"
            role="status"
            aria-live="polite"
          >
            <p className="font-sans text-fg">
              Export queued. Job id <code className="font-mono text-xs">{exportState.data.jobId}</code>.
            </p>
            <p className="mt-1 font-sans text-xs text-fg-muted">
              Check status:{' '}
              <a
                href={exportState.data.pollUrl}
                className="font-mono text-emerald-deep underline-offset-2 hover:underline"
              >
                {exportState.data.pollUrl}
              </a>
            </p>
          </div>
        )}

        {exportState.kind === 'error' && (
          <div
            className="rounded-md border border-red-700/40 bg-red-700/10 p-4 text-sm"
            role="alert"
          >
            <p className="font-sans text-fg">Export failed: {exportState.message}</p>
          </div>
        )}
      </article>

      {/* --- Delete card -------------------------------------------- */}
      <article className="paper-2 flex flex-col gap-4 rounded-lg border-2 border-red-700/30 bg-paper p-6">
        <header className="flex flex-col gap-1">
          <h2 className="inline-flex items-center gap-2 font-serif text-xl font-semibold text-fg">
            <AlertTriangle className="h-5 w-5 text-red-700" aria-hidden="true" />
            Delete account
          </h2>
          <p className="font-sans text-sm text-fg-muted">
            Anonymises every record we hold for you in place: posts and
            comments you authored become &quot;Deleted User&quot;,
            uploaded media is unattached, your profile is wiped. The
            account is fully purged 30 days later. There is no undo
            after the purge runs.
          </p>
        </header>

        {deleteState.kind === 'done' ? (
          <div
            className="rounded-md border border-emerald-deep/40 bg-emerald-light/20 p-4 text-sm"
            role="status"
            aria-live="polite"
          >
            <p className="font-sans text-fg">
              Account anonymised. Your session will end shortly; the final
              purge is scheduled for 30 days from now.
            </p>
          </div>
        ) : (
          <form className="flex flex-col gap-3" onSubmit={handleDelete}>
            <label className="flex flex-col gap-1">
              <span className="font-sans text-xs font-medium uppercase tracking-[0.08em] text-fg-muted">
                Current password
              </span>
              <input
                type="password"
                required
                autoComplete="current-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                disabled={deleteState.kind === 'loading'}
                className="rounded-md border border-border bg-paper px-3 py-2 font-sans text-sm text-fg outline-none focus:border-red-700/60"
                data-testid="privacy-delete-password"
              />
            </label>
            <label className="flex flex-col gap-1">
              <span className="font-sans text-xs font-medium uppercase tracking-[0.08em] text-fg-muted">
                Confirm password
              </span>
              <input
                type="password"
                required
                autoComplete="current-password"
                value={passwordConfirm}
                onChange={(e) => setPasswordConfirm(e.target.value)}
                disabled={deleteState.kind === 'loading'}
                className="rounded-md border border-border bg-paper px-3 py-2 font-sans text-sm text-fg outline-none focus:border-red-700/60"
                data-testid="privacy-delete-password-confirm"
              />
            </label>

            {deleteState.kind === 'error' && (
              <div
                className="rounded-md border border-red-700/40 bg-red-700/10 p-3 text-sm"
                role="alert"
              >
                {deleteState.message}
              </div>
            )}

            <div className="flex items-center gap-3">
              <Button
                type="submit"
                variant="destructive"
                disabled={
                  deleteState.kind === 'loading' ||
                  password.length === 0 ||
                  passwordConfirm.length === 0
                }
                data-testid="privacy-delete-cta"
              >
                {deleteState.kind === 'loading' ? (
                  <>
                    <Loader2 className="h-4 w-4 animate-spin" aria-hidden="true" />
                    Deleting account
                  </>
                ) : (
                  <>
                    <Trash2 className="h-4 w-4" aria-hidden="true" />
                    Permanently delete my account
                  </>
                )}
              </Button>
            </div>
          </form>
        )}
      </article>
    </div>
  );
}
