/**
 * `usePostLock` — acquire and hold the post_lock for the lifetime of
 * the editor mount.
 *
 * Two paths to consider:
 *
 *  1. *Happy path.* The hook calls POST `/api/v1/posts/{id}/lock`
 *     on mount. The server invokes `acquire_post_lock()` in
 *     Postgres. When it returns "you have it", the hook returns
 *     `{locked: true, lockedBy: null}` and the editor renders normally.
 *     Every `heartbeatMs` (default 60s) the hook re-invokes the same
 *     endpoint to push `expires_at` forward; if the user closes the
 *     tab, the heartbeat stops and the lock expires naturally.
 *
 *  2. *Locked path.* The server says "user X already holds it". The
 *     hook returns `{locked: false, lockedBy: {userId, displayName,
 *     expiresAt}}` and the editor renders the LockBanner instead of
 *     accepting writes. The heartbeat still runs (every `heartbeatMs`)
 *     so the moment the other user's lock expires, we pick it up
 *     automatically — no reload required.
 *
 * Unmount releases the lock via DELETE if we hold it. The server
 * tolerates a missing release (the lock expires anyway), so a network
 * failure on the way out is fine.
 */
'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import type { PostLockHolder, PostLockState } from './types.ts';

/** Default heartbeat. Slightly less than the server-side TTL/2 so a single
 *  missed ping doesn't expire the lock. The migration sets a 2-minute
 *  default TTL; 60s heartbeat means we tolerate one missed beat.
 */
const DEFAULT_HEARTBEAT_MS = 60_000;
/** Default endpoint base. Pages mount supplies `/api/v1/pages`. */
const DEFAULT_ENDPOINT_BASE = '/api/v1/posts';

export interface UsePostLockOptions {
  heartbeatMs?: number;
  endpointBase?: string;
  fetchImpl?: typeof fetch;
}

/**
 * Server response from POST /lock. Mirrors what the Go handler will
 * eventually return. On a successful acquire the body is empty; on a
 * "somebody else has it" response the body carries the holder info.
 */
interface LockAcquireResponse {
  /** Set when somebody else holds the lock. */
  holder?: {
    user_id: string;
    display_name: string;
    expires_at: string;
  };
}

/**
 * `usePostLock(postId, opts)` — see file header.
 *
 * Returns `{locked, lockedBy, refreshLock}`. The hook owns the
 * heartbeat timer; the caller doesn't need to touch it.
 */
export function usePostLock(
  postId: string,
  opts: UsePostLockOptions = {},
): PostLockState {
  const heartbeatMs = opts.heartbeatMs ?? DEFAULT_HEARTBEAT_MS;
  const endpointBase = opts.endpointBase ?? DEFAULT_ENDPOINT_BASE;
  const fetchImpl = opts.fetchImpl ?? globalThis.fetch;

  const [state, setState] = useState<{
    locked: boolean;
    lockedBy: PostLockHolder | null;
  }>({ locked: false, lockedBy: null });

  const heldByMe = useRef(false);
  const timerRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const abortRef = useRef<AbortController | null>(null);

  /** Single acquire/heartbeat pass. Idempotent on the server. */
  const acquireOnce = useCallback(async (): Promise<void> => {
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;

    try {
      const res = await fetchImpl(
        `${endpointBase}/${encodeURIComponent(postId)}/lock`,
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          signal: controller.signal,
          credentials: 'same-origin',
        },
      );
      if (!res.ok && res.status !== 409) {
        // 4xx other than 409 means we shouldn't keep heartbeating —
        // typically 401 (logged out) or 403 (no edit cap). Mark
        // un-acquired and let the editor handle the consequences
        // (it'll show the auth banner, not the lock banner).
        heldByMe.current = false;
        setState({ locked: false, lockedBy: null });
        return;
      }
      const body = (await res.json()) as LockAcquireResponse;
      if (body.holder !== undefined) {
        // Somebody else holds it. Track that fact so the heartbeat
        // keeps polling, and surface the holder to the UI.
        heldByMe.current = false;
        setState({
          locked: false,
          lockedBy: {
            userId: body.holder.user_id,
            displayName: body.holder.display_name,
            expiresAt: body.holder.expires_at,
          },
        });
        return;
      }
      // Empty holder == "you have it now".
      heldByMe.current = true;
      setState({ locked: true, lockedBy: null });
    } catch (err) {
      if (controller.signal.aborted) {
        return; // teardown; don't touch state
      }
      // Network failure. We log via the editor's toast layer (the
      // hook itself stays headless), but we don't flip `locked` to
      // false — a transient hiccup shouldn't yank the editor out
      // from under the user. The *next* heartbeat will recover.
    } finally {
      if (abortRef.current === controller) {
        abortRef.current = null;
      }
    }
  }, [endpointBase, fetchImpl, postId]);

  /**
   * Public manual-refresh handle. The LockBanner uses this when the
   * user clicks "Try Again" to see if the other editor's lock has
   * expired.
   */
  const refreshLock = useCallback(async () => {
    await acquireOnce();
  }, [acquireOnce]);

  // Acquire on mount; heartbeat every `heartbeatMs`; release on unmount.
  useEffect(() => {
    // Fire-and-forget; we surface state via setState inside.
    void acquireOnce();

    timerRef.current = setInterval(() => {
      void acquireOnce();
    }, heartbeatMs);

    return () => {
      if (timerRef.current !== null) {
        clearInterval(timerRef.current);
        timerRef.current = null;
      }
      // Best-effort release. If we don't hold it (because somebody
      // else does), skip the DELETE — there's no row to release.
      if (heldByMe.current) {
        // Use keepalive=true so the browser still ships the request
        // when the tab is closing. We can't await it (the page is
        // tearing down), and a missed release is harmless (the lock
        // expires anyway), but the explicit release shortens the
        // window where a re-opened editor sees a stale lock.
        try {
          void fetchImpl(
            `${endpointBase}/${encodeURIComponent(postId)}/lock`,
            {
              method: 'DELETE',
              credentials: 'same-origin',
              keepalive: true,
            },
          );
        } catch {
          // Best-effort. Already covered by the expiry path.
        }
      }
      abortRef.current?.abort();
      abortRef.current = null;
    };
  }, [acquireOnce, endpointBase, fetchImpl, heartbeatMs, postId]);

  return { locked: state.locked, lockedBy: state.lockedBy, refreshLock };
}
