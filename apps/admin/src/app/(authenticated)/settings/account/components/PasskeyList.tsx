/**
 * PasskeyList — client component rendering the signed-in user's
 * passkeys and offering "Add passkey" + per-row "Remove" buttons.
 *
 * The whole subtree is a client component because navigator.credentials
 * is unavailable during SSR and the list mutates in response to
 * user action (add/remove). State management is local — useState +
 * useEffect, no Zustand / React Query for now.
 */
'use client';

import type { ReactElement } from 'react';
import { useCallback, useEffect, useState } from 'react';
import { KeyRound, Plus, Trash2 } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';

import { deletePasskey, listPasskeys, registerPasskey } from '../api';
import type { PasskeyView } from '../api';

export function PasskeyList(): ReactElement {
  const [rows, setRows] = useState<PasskeyView[] | null>(null);
  const [name, setName] = useState('My passkey');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    try {
      const got = await listPasskeys();
      setRows(got);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const onAdd = useCallback(async () => {
    setBusy(true);
    setError(null);
    try {
      await registerPasskey(name.trim() || 'Passkey');
      await refresh();
      setName('My passkey');
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      // "cancelled" is a normal user gesture, not an error worth
      // shouting about — we surface it as a quieter line.
      setError(msg === 'cancelled' ? 'Registration cancelled.' : msg);
    } finally {
      setBusy(false);
    }
  }, [name, refresh]);

  const onDelete = useCallback(
    async (id: string) => {
      setBusy(true);
      setError(null);
      try {
        await deletePasskey(id);
        await refresh();
      } catch (err) {
        setError(err instanceof Error ? err.message : String(err));
      } finally {
        setBusy(false);
      }
    },
    [refresh],
  );

  return (
    <div
      className="flex flex-col gap-5 rounded-lg border border-border bg-paper-2 p-6 shadow-xs"
      data-testid="passkey-list"
    >
      <div className="flex items-start justify-between gap-4">
        <div className="flex flex-col gap-1">
          <h2 className="font-display text-xl font-semibold text-ink">
            Passkeys
          </h2>
          <p className="text-sm text-fg-muted">
            Sign in with a hardware key, a passkey on your phone, or a
            platform authenticator. We never see the underlying credential —
            only the public key.
          </p>
        </div>
      </div>

      {/* Add row */}
      <div className="flex flex-col gap-3 border-t border-border pt-5">
        <div className="flex flex-wrap items-end gap-3">
          <div className="flex flex-1 min-w-[200px] flex-col gap-1">
            <Label htmlFor="passkey-name" className="text-fg-subtle">
              Friendly name
            </Label>
            <Input
              id="passkey-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="e.g. iPhone, YubiKey 5C"
              disabled={busy}
              data-testid="passkey-name-input"
            />
          </div>
          <Button
            variant="emerald"
            disabled={busy}
            onClick={() => void onAdd()}
            data-testid="passkey-add"
          >
            <Plus aria-hidden="true" width={14} height={14} />
            Add passkey
          </Button>
        </div>
        {error ? (
          <p
            role="alert"
            className="rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-xs text-amber-900"
            data-testid="passkey-error"
          >
            {error}
          </p>
        ) : null}
      </div>

      {/* List */}
      <ul className="flex flex-col gap-2" data-testid="passkey-rows">
        {rows === null ? (
          <li className="text-sm text-fg-muted">Loading…</li>
        ) : rows.length === 0 ? (
          <li className="flex items-center gap-3 rounded-md border border-dashed border-border p-4 text-sm text-fg-muted">
            <KeyRound aria-hidden="true" width={14} height={14} />
            No passkeys yet. Add one above to sign in without a password.
          </li>
        ) : (
          rows.map((row) => (
            <li
              key={row.id}
              className="flex items-center justify-between rounded-md border border-border bg-paper px-4 py-3"
              data-testid={`passkey-row-${row.id}`}
            >
              <div className="flex flex-col gap-0.5">
                <span className="font-sans text-sm font-semibold text-ink">
                  {row.name}
                </span>
                <span className="font-mono text-xs text-fg-subtle">
                  Added {new Date(row.created_at).toLocaleString()}
                  {row.last_used_at
                    ? ` · last used ${new Date(row.last_used_at).toLocaleString()}`
                    : ' · never used'}
                </span>
              </div>
              <Button
                variant="default"
                size="sm"
                disabled={busy}
                onClick={() => void onDelete(row.id)}
                aria-label={`Remove passkey ${row.name}`}
              >
                <Trash2 aria-hidden="true" width={13} height={13} />
                Remove
              </Button>
            </li>
          ))
        )}
      </ul>
    </div>
  );
}
