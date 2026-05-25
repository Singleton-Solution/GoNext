'use client';

/**
 * TokensList — operator's active Personal Access Tokens.
 *
 * Restyled against the Living-Systems brand:
 *  - Card surface (paper-2) with brand-tokenised borders and shadow.
 *  - Token prefix renders in Geist Mono (`font-mono`) so the "this is
 *    a credential" cue is unambiguous.
 *  - Scopes render as lavender chips per the handoff (.tag--lavender).
 *  - Revoke button uses the destructive variant — the same red used by
 *    every other irreversible action across the admin.
 *
 * Behaviour (load → DELETE → refresh) is preserved verbatim from the
 * pre-brand implementation; the data fetching surface (`listTokens` /
 * `revokeToken`) is unchanged.
 */

import type { ReactElement } from 'react';
import { useCallback, useEffect, useState } from 'react';
import Link from 'next/link';
import { KeyRound, ShieldOff, Trash2 } from 'lucide-react';

import { ApiError } from '@/lib/api-client';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';

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
  return d.toLocaleString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  });
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
      if (
        !window.confirm(
          'Revoke this token? Any CI job or script using it will start receiving 401 errors.',
        )
      ) {
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
    return (
      <p
        data-testid="tokens-loading"
        className="font-sans text-sm text-fg-muted"
      >
        Loading…
      </p>
    );
  }

  if (state.kind === 'error') {
    return (
      <p
        role="alert"
        data-testid="tokens-error"
        className="rounded-md border border-danger/30 bg-danger-soft px-3 py-2 font-sans text-sm text-danger"
      >
        {state.message}
      </p>
    );
  }

  if (state.tokens.length === 0) {
    return (
      <div
        data-testid="tokens-empty"
        className="flex flex-col items-center gap-4 rounded-lg border border-dashed border-border bg-paper-3 px-6 py-10 text-center"
      >
        <span
          aria-hidden="true"
          className="flex h-12 w-12 items-center justify-center rounded-pill bg-emerald-soft text-emerald-deep"
        >
          <KeyRound className="h-5 w-5" />
        </span>
        <div className="flex flex-col gap-1">
          <p className="font-display text-base font-bold text-ink">
            No tokens yet
          </p>
          <p className="font-sans text-sm text-fg-muted">
            Create one to authenticate the CLI, CI jobs, or external scripts.
          </p>
        </div>
        <Button asChild variant="emerald">
          <Link href="/settings/tokens/new">Create your first token</Link>
        </Button>
      </div>
    );
  }

  return (
    <div className="overflow-hidden rounded-lg border border-border bg-paper-2 shadow-xs">
      <table
        className="w-full border-collapse font-sans text-sm"
        data-testid="tokens-table"
      >
        <thead>
          <tr className="border-b border-border bg-paper-3 text-left">
            <th
              className="px-4 py-3 text-xs font-medium uppercase tracking-[0.06em] text-fg-subtle"
              scope="col"
            >
              Name
            </th>
            <th
              className="px-4 py-3 text-xs font-medium uppercase tracking-[0.06em] text-fg-subtle"
              scope="col"
            >
              Prefix
            </th>
            <th
              className="px-4 py-3 text-xs font-medium uppercase tracking-[0.06em] text-fg-subtle"
              scope="col"
            >
              Scopes
            </th>
            <th
              className="px-4 py-3 text-xs font-medium uppercase tracking-[0.06em] text-fg-subtle"
              scope="col"
            >
              Last used
            </th>
            <th
              className="px-4 py-3 text-xs font-medium uppercase tracking-[0.06em] text-fg-subtle"
              scope="col"
            >
              Expires
            </th>
            <th
              className="px-4 py-3"
              scope="col"
              aria-label="actions"
            />
          </tr>
        </thead>
        <tbody>
          {state.tokens.map((t) => (
            <tr
              key={t.id}
              className="border-b border-border last:border-b-0 transition-colors duration-[160ms] hover:bg-paper-3"
            >
              <td className="px-4 py-3 align-middle">
                <span className="font-display text-sm font-bold text-ink">
                  {t.name}
                </span>
              </td>
              <td className="px-4 py-3 align-middle">
                <code className="rounded-sm border border-border bg-paper-3 px-1.5 py-0.5 font-mono text-xs text-ink">
                  gnp_{t.prefix}…
                </code>
              </td>
              <td className="px-4 py-3 align-middle">
                <div className="flex flex-wrap gap-1">
                  {t.scopes.length === 0 ? (
                    <Badge variant="default" className="opacity-70">
                      (none)
                    </Badge>
                  ) : (
                    t.scopes.map((scope) => (
                      <Badge
                        key={scope}
                        variant="lavender"
                        data-scope={scope}
                      >
                        {scope}
                      </Badge>
                    ))
                  )}
                </div>
              </td>
              <td className="px-4 py-3 align-middle font-mono text-xs text-fg-muted">
                {formatRelative(t.last_used_at)}
              </td>
              <td className="px-4 py-3 align-middle font-mono text-xs text-fg-muted">
                {t.expires_at ? formatRelative(t.expires_at) : 'never'}
              </td>
              <td className="px-4 py-3 align-middle text-right">
                <Button
                  type="button"
                  variant="destructive"
                  size="sm"
                  onClick={() => void onRevoke(t.id)}
                  disabled={revoking === t.id}
                  aria-label={`Revoke ${t.name}`}
                  data-testid={`revoke-${t.id}`}
                >
                  {revoking === t.id ? (
                    <>
                      <ShieldOff className="h-3.5 w-3.5" aria-hidden="true" />
                      Revoking…
                    </>
                  ) : (
                    <>
                      <Trash2 className="h-3.5 w-3.5" aria-hidden="true" />
                      Revoke
                    </>
                  )}
                </Button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
