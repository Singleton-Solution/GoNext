'use client';

/**
 * ImpersonationBanner — fixed bar at the very top of the admin shell
 * that appears when the current session is an impersonation.
 *
 * On mount the component GETs /api/v1/auth/impersonation; if the
 * response carries `impersonation: true` it renders a yellow warning
 * bar reading "Signed in as <target> on behalf of <actor>. Exit".
 * Clicking Exit hits DELETE /api/v1/auth/impersonation and reloads
 * the page so the rest of the chrome re-renders against the actor's
 * restored session.
 *
 * The banner deliberately fails closed: if the API is unreachable or
 * returns an error, no banner is rendered. The cost of a missed
 * impersonation indicator (the operator forgets they're impersonating)
 * is annoying but reversible; the cost of a false-positive (banner
 * appears on a normal session) is more confusing.
 */
import { useEffect, useState, type ReactElement } from 'react';
import { api } from '@/lib/api-client';

interface WhoamiResponse {
  impersonation: boolean;
  actor_user_id?: string;
  target_user_id?: string;
}

export function ImpersonationBanner(): ReactElement | null {
  const [state, setState] = useState<WhoamiResponse | null>(null);
  const [exiting, setExiting] = useState(false);

  useEffect(() => {
    let cancelled = false;
    api
      .get<WhoamiResponse>('/api/v1/auth/impersonation')
      .then((data) => {
        if (!cancelled) setState(data);
      })
      .catch(() => {
        // Silent: see file header.
      });
    return () => {
      cancelled = true;
    };
  }, []);

  if (!state || !state.impersonation) return null;

  const onExit = async () => {
    setExiting(true);
    try {
      await api.delete('/api/v1/auth/impersonation');
      window.location.assign('/');
    } catch {
      setExiting(false);
    }
  };

  return (
    <div
      role="alert"
      className="impersonation-banner"
      data-testid="impersonation-banner"
    >
      <span>
        Signed in as <strong>{state.target_user_id}</strong> on behalf of{' '}
        <strong>{state.actor_user_id}</strong>.
      </span>
      <button type="button" onClick={() => void onExit()} disabled={exiting}>
        Exit impersonation
      </button>
    </div>
  );
}
