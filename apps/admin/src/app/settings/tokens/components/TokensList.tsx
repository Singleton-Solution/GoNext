'use client';

/**
 * TokensList — client component rendering the operator's active PATs.
 *
 * The list is loaded once on mount and refreshed after every successful
 * revoke. We avoid SWR/react-query for now because the surface is small
 * (one fetch + one DELETE), the component lifecycle is short (the page
 * is a leaf), and pulling in a cache library would be more
 * infrastructure than the feature deserves.
 */

import type { ReactElement } from 'react';
import { useCallback, useEffect, useState } from 'react';
import Link from 'next/link';
import { ApiError } from '../../../api-client';
import { listTokens, revokeToken } from '../api';
import type { TokenView } from '../types';

type LoadState =
  | { kind: 'idle' }
  | { kind: 'loading' }
  | { kind: 'ready'; tokens: TokenView[] }
  | { kind: 'error'; message: string };

function formatRelative(iso: string | null | undefined): string {
  if (!iso) return 'never';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return 'unknown';
  return d.toLocaleString();
}

export function TokensList(): ReactElement {
  const [state, setState] = useState<LoadState>({ kind: 'idle' });
  const [revoking, setRevoking] = useState<string | null>(null);

  const load = useCallback(async () => {
    setState({ kind: 'loading' });
    try {
      const tokens = await listTokens();
      setState({ kind: 'ready', tokens });
    } catch (err) {
      const message =
        err instanceof ApiError
          ? `API error ${err.status}: ${err.statusText}`
          : err instanceof Error
            ? err.message
            : 'Failed to load tokens';
      setState({ kind: 'error', message });
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const onRevoke = useCallback(
    async (id: string) => {
      if (!window.confirm('Revoke this token? Any CI job or script using it will start receiving 401 errors.')) {
        return;
      }
      setRevoking(id);
      try {
        await revokeToken(id);
        await load();
      } catch (err) {
        const message =
          err instanceof ApiError
            ? `API error ${err.status}: ${err.statusText}`
            : err instanceof Error
              ? err.message
              : 'Revoke failed';
        window.alert(message);
      } finally {
        setRevoking(null);
      }
    },
    [load],
  );

  if (state.kind === 'idle' || state.kind === 'loading') {
    return <p data-testid="tokens-loading">Loading…</p>;
  }
  if (state.kind === 'error') {
    return (
      <p role="alert" data-testid="tokens-error">
        {state.message}
      </p>
    );
  }

  if (state.tokens.length === 0) {
    return (
      <div data-testid="tokens-empty">
        <p className="muted">You haven’t created any tokens yet.</p>
        <p>
          <Link href="/settings/tokens/new" className="btn btn-primary">
            Create your first token
          </Link>
        </p>
      </div>
    );
  }

  return (
    <table className="tokens-table" data-testid="tokens-table">
      <thead>
        <tr>
          <th scope="col">Name</th>
          <th scope="col">Prefix</th>
          <th scope="col">Scopes</th>
          <th scope="col">Last used</th>
          <th scope="col">Expires</th>
          <th scope="col" aria-label="actions" />
        </tr>
      </thead>
      <tbody>
        {state.tokens.map((t) => (
          <tr key={t.id}>
            <td>{t.name}</td>
            <td>
              <code>{`gnp_${t.prefix}…`}</code>
            </td>
            <td>{t.scopes.join(', ')}</td>
            <td>{formatRelative(t.last_used_at)}</td>
            <td>{t.expires_at ? formatRelative(t.expires_at) : 'never'}</td>
            <td>
              <button
                type="button"
                onClick={() => void onRevoke(t.id)}
                disabled={revoking === t.id}
                aria-label={`Revoke ${t.name}`}
                className="btn btn-danger"
              >
                {revoking === t.id ? 'Revoking…' : 'Revoke'}
              </button>
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
