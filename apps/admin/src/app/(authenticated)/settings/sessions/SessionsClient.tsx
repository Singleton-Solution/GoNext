'use client';

/**
 * Active-sessions self-service client — issue #205.
 *
 * On mount: GET /api/v1/auth/sessions and render one row per live
 * session. Each row carries a Revoke action; a top-of-list "Sign out
 * of all other devices" button hits DELETE /api/v1/auth/sessions
 * (which the server scopes to "everything except the current one").
 *
 * Optimistic updates: a successful Revoke removes the row immediately;
 * a successful "Revoke all other" removes every non-current row in a
 * single state update. Errors revert the action and surface a banner.
 */
import { useCallback, useEffect, useState, type ReactElement } from 'react';
import { api, ApiError } from '@/lib/api-client';
import type { SessionListResponse, SessionView } from './types';

type LoadState =
  | { kind: 'loading' }
  | { kind: 'ready'; sessions: SessionView[] }
  | { kind: 'error'; message: string };

function formatStamp(iso: string): string {
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

export function SessionsClient(): ReactElement {
  const [state, setState] = useState<LoadState>({ kind: 'loading' });
  const [busy, setBusy] = useState<string>('');

  const reload = useCallback(async () => {
    setState({ kind: 'loading' });
    try {
      const data = await api.get<SessionListResponse>('/api/v1/auth/sessions');
      setState({ kind: 'ready', sessions: data.sessions ?? [] });
    } catch (err) {
      const message =
        err instanceof ApiError ? `Load failed (${err.status})` : 'Load failed';
      setState({ kind: 'error', message });
    }
  }, []);

  useEffect(() => {
    void reload();
  }, [reload]);

  const onRevoke = useCallback(
    async (id: string) => {
      setBusy(id);
      const before = state.kind === 'ready' ? state.sessions : null;
      // Optimistic remove.
      if (before) {
        setState({
          kind: 'ready',
          sessions: before.filter((s) => s.id !== id),
        });
      }
      try {
        await api.delete(`/api/v1/auth/sessions/${id}`);
      } catch (err) {
        // Revert.
        if (before) setState({ kind: 'ready', sessions: before });
        const message =
          err instanceof ApiError ? `Revoke failed (${err.status})` : 'Revoke failed';
        setState({ kind: 'error', message });
      } finally {
        setBusy('');
      }
    },
    [state],
  );

  const onRevokeAllOther = useCallback(async () => {
    setBusy('all');
    const before = state.kind === 'ready' ? state.sessions : null;
    if (before) {
      setState({
        kind: 'ready',
        sessions: before.filter((s) => s.current),
      });
    }
    try {
      await api.delete('/api/v1/auth/sessions');
    } catch (err) {
      if (before) setState({ kind: 'ready', sessions: before });
      const message =
        err instanceof ApiError
          ? `Bulk revoke failed (${err.status})`
          : 'Bulk revoke failed';
      setState({ kind: 'error', message });
    } finally {
      setBusy('');
    }
  }, [state]);

  return (
    <div className="sessions-page">
      <h1>Active sessions</h1>
      <p>
        Every device you have signed in from. Revoking a session signs out the
        corresponding browser immediately.
      </p>

      {state.kind === 'loading' && <p>Loading…</p>}
      {state.kind === 'error' && (
        <div role="alert" className="sessions-page__error">
          {state.message}
          <button type="button" onClick={() => void reload()}>
            Retry
          </button>
        </div>
      )}

      {state.kind === 'ready' && (
        <>
          <div className="sessions-page__bulk">
            <button
              type="button"
              onClick={() => void onRevokeAllOther()}
              disabled={busy === 'all' || state.sessions.filter((s) => !s.current).length === 0}
            >
              Sign out of all other devices
            </button>
          </div>

          <ul className="sessions-page__list">
            {state.sessions.map((s) => (
              <li
                key={s.id}
                className={s.current ? 'is-current' : undefined}
                aria-current={s.current ? 'true' : undefined}
              >
                <div className="sessions-page__row-main">
                  <strong>{s.device_label || 'Unknown device'}</strong>
                  {s.current && <span className="sessions-page__chip">This device</span>}
                  <span className="sessions-page__ip">{s.ip || 'IP unavailable'}</span>
                </div>
                <div className="sessions-page__row-meta">
                  <span>Created {formatStamp(s.created_at)}</span>
                  <span>Last seen {formatStamp(s.last_seen_at)}</span>
                </div>
                {!s.current && (
                  <button
                    type="button"
                    onClick={() => void onRevoke(s.id)}
                    disabled={busy === s.id}
                    aria-label={`Revoke session ${s.id}`}
                  >
                    Revoke
                  </button>
                )}
              </li>
            ))}
            {state.sessions.length === 0 && (
              <li className="sessions-page__empty">No active sessions.</li>
            )}
          </ul>
        </>
      )}
    </div>
  );
}
