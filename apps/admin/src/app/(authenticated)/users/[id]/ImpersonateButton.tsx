'use client';

/**
 * ImpersonateButton — POST /api/v1/admin/users/{id}/impersonate.
 *
 * Visible only when the current viewer is a super_admin and the target
 * is not the viewer themselves. Successful response swaps the session
 * cookie (the server Set-Cookies it inline) and reloads the page so
 * the rest of the admin shell re-renders against the impersonated
 * identity — including the <ImpersonationBanner> mounted in the
 * authenticated layout.
 */
import { useState, type ReactElement } from 'react';
import { api, ApiError } from '@/lib/api-client';

interface Props {
  targetUserId: string;
  /** True when the viewer carries the super_admin role. */
  canImpersonate: boolean;
  /** Hide the button when targetUserId === viewer's own user ID. */
  isSelf: boolean;
}

interface ImpersonateResponse {
  impersonated_user_id: string;
  actor_user_id: string;
  expires_in_seconds: number;
}

export function ImpersonateButton({
  targetUserId,
  canImpersonate,
  isSelf,
}: Props): ReactElement | null {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  if (!canImpersonate || isSelf) return null;

  const onClick = async () => {
    setBusy(true);
    setError('');
    try {
      await api.post<ImpersonateResponse>(
        `/api/v1/admin/users/${targetUserId}/impersonate`,
      );
      // The cookie was rewritten by the API; the next render needs a
      // fresh server-side fetch to pick up the new principal.
      window.location.assign('/');
    } catch (err) {
      const message =
        err instanceof ApiError ? `Impersonate failed (${err.status})` : 'Impersonate failed';
      setError(message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <button type="button" onClick={() => void onClick()} disabled={busy}>
        Impersonate
      </button>
      {error && (
        <span role="alert" className="impersonate-error">
          {error}
        </span>
      )}
    </>
  );
}
