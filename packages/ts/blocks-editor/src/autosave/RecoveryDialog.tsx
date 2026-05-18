/**
 * `<RecoveryDialog>` — "we found unsaved work, want it back?".
 *
 * Rendered on editor mount. The component:
 *
 *  1. Calls GET `/api/v1/posts/{id}/autosave` to ask whether the
 *     current user has an autosave for this post.
 *  2. If the server returns 204 (no autosave) it stays silent.
 *  3. If the server returns 200, it compares the autosave's
 *     `updated_at` against the canonical post's `updated_at`. Only
 *     renders the dialog when the autosave is *newer* — otherwise
 *     the saved version is already up to date and the autosave is
 *     stale leftover (which the 7-day sweep will eventually GC).
 *  4. Surfaces two actions: **Restore** (calls `onRestore(blocks)`
 *     with the autosaved tree) and **Discard** (calls `onDiscard()`
 *     and dismisses the dialog).
 *
 * The dialog is intentionally minimal — the consuming editor app
 * supplies the visual chrome via `className`. Keeping the markup
 * structural-only keeps the SDK package free of CSS dependencies.
 */
'use client';

import type { BlockTree } from '@gonext/blocks-sdk';
import { useEffect, useState } from 'react';
import type { AutosaveResponse } from './types.ts';

export interface RecoveryDialogProps {
  /** The post we're editing. Required. */
  postId: string;
  /**
   * The canonical post's `updated_at`. The dialog only renders when
   * the autosave is *newer* than this — a stale autosave (e.g. the
   * user explicitly saved, then closed the tab) shouldn't surface
   * the prompt at all. ISO-8601 string for round-trip safety.
   */
  canonicalUpdatedAt: string;
  /**
   * Called when the user clicks "Restore". The autosaved block tree
   * is passed in; the editor app is responsible for slotting it
   * into its block-tree state.
   */
  onRestore: (blocks: BlockTree) => void;
  /**
   * Called when the user clicks "Discard". The dialog dismisses
   * itself; the autosave is *not* deleted server-side (the 7-day
   * sweep will reap it eventually).
   */
  onDiscard: () => void;
  /**
   * Override the REST base path. Defaults to `/api/v1/posts`. The
   * pages mount supplies `/api/v1/pages`.
   */
  endpointBase?: string;
  /** Test-injectable fetch. Defaults to `globalThis.fetch`. */
  fetchImpl?: typeof fetch;
  /** Optional className for the outer wrapper. */
  className?: string;
}

const DEFAULT_ENDPOINT_BASE = '/api/v1/posts';

type DialogPhase =
  | { kind: 'loading' }
  | { kind: 'hidden' } // resolved: nothing to restore
  | { kind: 'visible'; blocks: BlockTree; updatedAt: string };

export function RecoveryDialog({
  postId,
  canonicalUpdatedAt,
  onRestore,
  onDiscard,
  endpointBase = DEFAULT_ENDPOINT_BASE,
  fetchImpl,
  className,
}: RecoveryDialogProps) {
  const [phase, setPhase] = useState<DialogPhase>({ kind: 'loading' });
  const f = fetchImpl ?? globalThis.fetch;

  useEffect(() => {
    let cancelled = false;
    const ac = new AbortController();
    (async () => {
      try {
        const res = await f(
          `${endpointBase}/${encodeURIComponent(postId)}/autosave`,
          { signal: ac.signal, credentials: 'same-origin' },
        );
        if (cancelled) return;
        // 204 → no autosave. The most common case; render nothing.
        if (res.status === 204) {
          setPhase({ kind: 'hidden' });
          return;
        }
        if (!res.ok) {
          // Treat any other error as "no autosave to restore". We
          // don't surface a generic error here — the user can still
          // edit; we just don't have a draft to offer.
          setPhase({ kind: 'hidden' });
          return;
        }
        const body = (await res.json()) as AutosaveResponse;
        // Stale-autosave check. Date.parse coerces to epoch ms; if
        // the autosave wasn't updated after the canonical save,
        // skip the prompt.
        const autoTime = Date.parse(body.updated_at);
        const canonTime = Date.parse(canonicalUpdatedAt);
        if (
          !Number.isFinite(autoTime) ||
          !Number.isFinite(canonTime) ||
          autoTime <= canonTime
        ) {
          setPhase({ kind: 'hidden' });
          return;
        }
        setPhase({
          kind: 'visible',
          blocks: body.blocks,
          updatedAt: body.updated_at,
        });
      } catch (err) {
        if (ac.signal.aborted || cancelled) return;
        // Network blip; pretend there's no autosave so the editor
        // doesn't block on it. The next session can offer recovery.
        setPhase({ kind: 'hidden' });
      }
    })();
    return () => {
      cancelled = true;
      ac.abort();
    };
  }, [canonicalUpdatedAt, endpointBase, f, postId]);

  if (phase.kind !== 'visible') {
    return null;
  }

  const handleRestore = () => {
    onRestore(phase.blocks);
    setPhase({ kind: 'hidden' });
  };
  const handleDiscard = () => {
    onDiscard();
    setPhase({ kind: 'hidden' });
  };

  // Render the dialog. We use semantic roles + ARIA labels so the
  // editor app's screen-reader users hear "unsaved draft found"
  // without needing app-side wiring.
  return (
    <div
      className={className}
      role="dialog"
      aria-modal="true"
      aria-labelledby="autosave-recovery-title"
      data-testid="autosave-recovery-dialog"
    >
      <h2 id="autosave-recovery-title">Unsaved changes found</h2>
      <p>
        We found an autosaved draft of this post that is newer than the
        last saved version. Would you like to restore it?
      </p>
      <p>
        <small data-testid="autosave-recovery-timestamp">
          Last autosaved: {phase.updatedAt}
        </small>
      </p>
      <div>
        <button
          type="button"
          onClick={handleRestore}
          data-testid="autosave-recovery-restore"
        >
          Restore
        </button>
        <button
          type="button"
          onClick={handleDiscard}
          data-testid="autosave-recovery-discard"
        >
          Discard
        </button>
      </div>
    </div>
  );
}
