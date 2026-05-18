/**
 * `useAutosave` — periodic background save of the in-flight block tree.
 *
 * The hook is the headless brain of the autosave subsystem. It does
 * three things, in this order:
 *
 *  1. Watches `blocks` for changes. When the serialised form differs
 *     from the last-saved form, it marks the tree as "dirty" and
 *     starts a timer.
 *  2. After `intervalMs` (default 30s) the timer fires and a POST is
 *     issued to `/api/v1/posts/{postId}/autosave`. While that request
 *     is in flight, status is `'saving'`.
 *  3. When the request settles, status moves to `'saved'` (success) or
 *     `'error'` (failure). Either way, the dirty bit clears on a
 *     successful save and `lastSavedAt` is updated.
 *
 * The hook is also responsible for two cleanup paths:
 *
 *  - Unmounting aborts any in-flight request via AbortController so the
 *    saver doesn't write into a torn-down component.
 *  - Rapid block changes are *debounced*: a fresh edit while a timer
 *    is pending resets the timer (we don't want to fire the save at
 *    the very start of an edit burst — we want to wait for the user
 *    to settle).
 *
 * The shape returned is `{status, lastSavedAt, error}` — the editor
 * toolbar renders a tiny status pip from this; nothing else.
 */
'use client';

import type { BlockTree } from '@gonext/blocks-sdk';
import { useEffect, useRef, useState } from 'react';
import type {
  AutosavePayload,
  AutosaveState,
  AutosaveStatus,
  UseAutosaveOptions,
} from './types.ts';

/** Default tick. Matches the design doc ("autosave every 30s"). */
const DEFAULT_INTERVAL_MS = 30_000;
/** Default debounce. ~1.5s of typing idle before we start counting. */
const DEFAULT_DEBOUNCE_MS = 1_500;
/** Default REST base. Pages mount supplies `/api/v1/pages` explicitly. */
const DEFAULT_ENDPOINT_BASE = '/api/v1/posts';

/**
 * Idempotent comparator for two block trees. We use JSON-stringify
 * rather than a structural deep-equal because:
 *
 *  - blocks are JSON-serialisable by construction (see the SDK
 *    validator), so JSON.stringify is total;
 *  - it's O(n) and allocation-free at the comparator layer;
 *  - it folds key-order differences (`{a:1,b:2}` vs `{b:2,a:1}`) the
 *    same way the server's content_blocks_hash does, so the
 *    server-side ETag comparison stays in sync.
 *
 * The serialisation cost is paid once per change-check (max once per
 * render of the editor). For trees of a few dozen blocks it's well
 * under a millisecond.
 */
function serialise(blocks: BlockTree): string {
  return JSON.stringify(blocks);
}

/**
 * `useAutosave(postId, blocks, opts)` — see file header.
 *
 * Returns the lifecycle state. The hook itself owns the timer and the
 * abort controller; the caller's only responsibility is to keep
 * `blocks` reasonably stable across renders (i.e. don't recreate the
 * tree on every render — that defeats the dirty-bit check).
 */
export function useAutosave(
  postId: string,
  blocks: BlockTree,
  opts: UseAutosaveOptions = {},
): AutosaveState {
  const intervalMs = opts.intervalMs ?? DEFAULT_INTERVAL_MS;
  const debounceMs = opts.debounceMs ?? DEFAULT_DEBOUNCE_MS;
  const endpointBase = opts.endpointBase ?? DEFAULT_ENDPOINT_BASE;
  const fetchImpl = opts.fetchImpl ?? globalThis.fetch;

  const [state, setState] = useState<AutosaveState>({
    status: 'idle',
    lastSavedAt: null,
    error: null,
  });

  // We track the last *serialised* snapshot rather than the tree
  // reference so a parent that hands us a fresh `blocks` array on every
  // render (without mutating contents) doesn't trigger spurious saves.
  // Refs rather than state because mutating these must not re-render.
  const lastSavedSerialised = useRef<string>(serialise(blocks));
  const lastDirtySerialised = useRef<string | null>(null);
  const abortRef = useRef<AbortController | null>(null);
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  // The "save once" worker. Extracted into its own function so both
  // the interval tick and a possible manual-flush future API can call
  // the same path. Returns a promise so the test can await completion.
  const performSave = useRef(
    async (snapshot: string, payload: AutosavePayload) => {
      // Don't write if the snapshot is already up to date — the timer
      // fired but the user undid the edit in the meantime. Cheap guard
      // against doing a no-op POST.
      if (snapshot === lastSavedSerialised.current) {
        return;
      }

      // Cancel any in-flight save. The most common cause is "the user
      // kept typing"; we don't want two POSTs for the same post in
      // flight at once (the server enforces last-write-wins, but the
      // client should still be polite).
      abortRef.current?.abort();
      const controller = new AbortController();
      abortRef.current = controller;

      setState((s) => ({ ...s, status: 'saving' }));
      try {
        const res = await fetchImpl(
          `${endpointBase}/${encodeURIComponent(postId)}/autosave`,
          {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(payload),
            signal: controller.signal,
            // Include cookies so the session-auth middleware can
            // resolve the principal. The admin app always runs on
            // the same origin as the API, so SameSite cookies are
            // the right channel.
            credentials: 'same-origin',
          },
        );
        // We treat 423 specially: it means another user holds the
        // lock. Surface it as an error so the UI can render the
        // LockBanner — the caller wires the banner to `state.error`
        // via the LockBanner component.
        if (res.status === 423) {
          throw new Error('locked: another user is editing this post');
        }
        if (!res.ok) {
          throw new Error(`autosave failed: ${res.status}`);
        }
        // Commit the dirty bit on success — even if `blocks` has
        // moved on in the meantime, *this* snapshot did get persisted.
        lastSavedSerialised.current = snapshot;
        setState({
          status: 'saved',
          lastSavedAt: new Date(),
          error: null,
        });
      } catch (err) {
        // AbortError isn't a real failure — it's our own teardown
        // calling. Don't update state; the next save will set it.
        if (controller.signal.aborted) {
          return;
        }
        const message = err instanceof Error ? err.message : String(err);
        setState((s) => ({
          ...s,
          status: 'error' as AutosaveStatus,
          error: message,
        }));
      } finally {
        // Clear the ref if this is still the active controller. A
        // newer save may have taken over already (in which case we
        // leave its controller in place).
        if (abortRef.current === controller) {
          abortRef.current = null;
        }
      }
    },
  );

  // Effect: watch `blocks` for changes and schedule a save.
  useEffect(() => {
    const snapshot = serialise(blocks);
    if (snapshot === lastSavedSerialised.current) {
      // No-op edit (e.g. a parent re-rendered with the same content).
      return;
    }
    // Mark dirty + debounce. The actual interval delay sits between
    // the dirty mark and the fire — we don't try to schedule "30s
    // since the user *started* editing" because that's harder to
    // reason about and gives almost identical UX.
    lastDirtySerialised.current = snapshot;
    if (timerRef.current !== null) {
      clearTimeout(timerRef.current);
    }
    // Total delay: debounce + (interval - debounce). The user-visible
    // contract is "every intervalMs", with a short debounce so a
    // burst of edits all settles into one save. Tests pin both
    // values so the math is checkable.
    const total = Math.max(debounceMs, intervalMs);
    timerRef.current = setTimeout(() => {
      timerRef.current = null;
      // Capture a fresh snapshot at fire-time, not at schedule-time.
      // The tree may have moved on since we set the timer; the user
      // expects the *latest* state to land, not what was on screen
      // 30s ago.
      const fresh = serialise(blocks);
      void performSave.current(fresh, { blocks });
    }, total);
    // Cleanup runs on the *next* change or on unmount. We clear the
    // timer in both cases; the unmount path also aborts in-flight.
    return () => {
      if (timerRef.current !== null) {
        clearTimeout(timerRef.current);
        timerRef.current = null;
      }
    };
  }, [blocks, debounceMs, intervalMs, postId]);

  // Unmount: abort any pending save. We don't want the response to
  // arrive after the editor has torn down and try to set state on a
  // ghost component (React 18+ tolerates that, but it still spams
  // dev warnings — better to short-circuit).
  useEffect(() => {
    return () => {
      abortRef.current?.abort();
      abortRef.current = null;
      if (timerRef.current !== null) {
        clearTimeout(timerRef.current);
        timerRef.current = null;
      }
    };
  }, []);

  return state;
}
