/**
 * `<LockBanner>` — renders when another user holds the post_lock.
 *
 * The component is intentionally headless-but-visual: it owns the
 * minimal DOM (role="alert", a heading, the holder's display name,
 * and the expiry ETA) but exposes a `className` for the consuming
 * editor app to style.
 *
 * The banner does NOT acquire the lock — that's `usePostLock`'s job.
 * The banner only renders the *state* the hook reports. The two work
 * together at the editor top level:
 *
 *   const lock = usePostLock(postId);
 *   return lock.lockedBy ? <LockBanner ... /> : <Editor ... />;
 */
'use client';

import { useEffect, useState } from 'react';
import type { PostLockHolder } from './types.ts';

export interface LockBannerProps {
  /** The user currently holding the lock. Required. */
  lockedBy: PostLockHolder;
  /**
   * Called when the user clicks "Try again". The parent typically
   * wires this to `usePostLock`'s `refreshLock` so we re-acquire and
   * unmount on success.
   */
  onRefresh?: () => void;
  /** Optional className. */
  className?: string;
  /**
   * Injection seam for `Date.now` so the ETA-formatting tests can
   * pin the clock. Defaults to the real `Date.now`.
   */
  nowFn?: () => number;
}

/**
 * Format the expiry as a friendly relative time. We don't pull in a
 * date library — the cases are bounded and `Intl.RelativeTimeFormat`
 * ships in every modern engine.
 *
 * Returns "expires in 1m 23s" / "expires in 45s" / "expired" for the
 * three regimes. The banner re-renders every second via a timer so
 * the countdown stays live.
 */
function formatEta(expiresAt: string, now: number): string {
  const target = Date.parse(expiresAt);
  if (!Number.isFinite(target)) return 'expires soon';
  const ms = target - now;
  if (ms <= 0) return 'expired';
  const seconds = Math.floor(ms / 1000);
  if (seconds < 60) {
    return `expires in ${seconds}s`;
  }
  const minutes = Math.floor(seconds / 60);
  const remSeconds = seconds % 60;
  return `expires in ${minutes}m ${remSeconds}s`;
}

export function LockBanner({
  lockedBy,
  onRefresh,
  className,
  nowFn,
}: LockBannerProps) {
  // The current "now" used to compute the ETA. Bumped every second so
  // the countdown ticks. We don't try to be more clever than that —
  // a 1Hz repaint on a banner that's already a critical-modal kind of
  // surface is well within budget.
  const [now, setNow] = useState<number>(() => (nowFn ?? Date.now)());

  useEffect(() => {
    const tick = () => setNow((nowFn ?? Date.now)());
    const id = setInterval(tick, 1000);
    return () => clearInterval(id);
  }, [nowFn]);

  const eta = formatEta(lockedBy.expiresAt, now);

  return (
    <div
      className={className}
      role="alert"
      data-testid="autosave-lock-banner"
    >
      <h2>This post is currently being edited</h2>
      <p>
        <strong data-testid="autosave-lock-banner-holder">
          {lockedBy.displayName}
        </strong>{' '}
        has the editor open. Your edits would be discarded.
      </p>
      <p data-testid="autosave-lock-banner-eta">{eta}</p>
      {onRefresh !== undefined ? (
        <button
          type="button"
          onClick={onRefresh}
          data-testid="autosave-lock-banner-refresh"
        >
          Try again
        </button>
      ) : null}
    </div>
  );
}
