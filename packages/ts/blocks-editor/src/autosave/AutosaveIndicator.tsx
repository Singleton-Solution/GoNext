/**
 * `<AutosaveIndicator>` — the tiny status pip the editor's topbar
 * renders next to the Publish button. Feeds off the `AutosaveState`
 * shape returned by `useAutosave`.
 *
 * Visual contract per the brand mock
 * (`docs/design/ui_kits/editor/index.html`):
 *
 *  - An emerald-bright dot at rest; the dot pulses while a save is
 *    in flight. The dot flips to `--danger` on error.
 *  - A relative timestamp in `--font-serif` italic ("12s ago"), which
 *    is the brand's editorial accent. Updates locally every ~10s so
 *    the topbar doesn't drift away from reality.
 *  - Two tone presets: `light` (cream chrome) and `dark` (the forest
 *    topbar). The editor's topbar is dark, so `tone="dark"` is the
 *    common case; the component defaults to `light` to be safe in
 *    isolation.
 *
 * The relative timestamp is a tiny inline helper so we don't drag in
 * a date library. It honours the four buckets the design mock shows:
 * `just now`, `Ns ago`, `Nm ago`, `Nh ago`. Anything older renders as
 * an absolute time.
 */
'use client';

import { useEffect, useState } from 'react';
import type { AutosaveState, AutosaveStatus } from './types.ts';

export interface AutosaveIndicatorProps {
  /** The current autosave state. Typically returned by `useAutosave`. */
  state: AutosaveState;
  /**
   * Visual tone. `dark` for the forest topbar; `light` otherwise.
   * Defaults to `light`.
   */
  tone?: 'light' | 'dark';
  /**
   * How often to refresh the relative timestamp. Default 10s. Tests
   * pass `0` to disable the interval.
   */
  refreshIntervalMs?: number;
  /**
   * Injection point for "now" so tests can pin time. Default
   * `() => new Date()`.
   */
  now?: () => Date;
}

/**
 * The autosave status pip. Pure presentation — pair with `useAutosave`.
 */
export function AutosaveIndicator({
  state,
  tone = 'light',
  refreshIntervalMs = 10_000,
  now = () => new Date(),
}: AutosaveIndicatorProps) {
  // Force a re-render every `refreshIntervalMs` so the relative
  // timestamp drifts forward. `now()` is read at every render — this
  // tick is the only reason we re-render between state changes.
  const [, setTick] = useState(0);
  useEffect(() => {
    if (refreshIntervalMs <= 0) return;
    const id = setInterval(() => setTick((n) => n + 1), refreshIntervalMs);
    return () => clearInterval(id);
  }, [refreshIntervalMs]);

  const label = labelForStatus(state.status);
  const timestamp = relativeTimestamp(state.lastSavedAt, now());

  return (
    <span
      className="gonext-autosave-indicator"
      data-testid="autosave-indicator"
      data-status={state.status}
      data-tone={tone}
      role="status"
      aria-live="polite"
    >
      <span
        aria-hidden="true"
        className="gonext-autosave-indicator__dot"
        data-testid="autosave-indicator-dot"
      />
      <span
        className="gonext-autosave-indicator__label"
        data-testid="autosave-indicator-label"
      >
        {label}
      </span>
      {timestamp !== null ? (
        <>
          <span aria-hidden="true">·</span>
          <span
            className="gonext-autosave-indicator__time"
            data-testid="autosave-indicator-time"
          >
            {timestamp}
          </span>
        </>
      ) : null}
    </span>
  );
}

/**
 * Map status → user-facing label. The labels deliberately stay terse
 * to fit inside the pill in the topbar.
 */
function labelForStatus(status: AutosaveStatus): string {
  switch (status) {
    case 'idle':
      return 'Idle';
    case 'saving':
      return 'Saving';
    case 'saved':
      return 'Saved';
    case 'error':
      return 'Save failed';
  }
}

/**
 * Format the gap between `last` and `now` in the design-mock buckets:
 *
 *  - `null` last → `null` (the indicator hides the timestamp entirely).
 *  - <5s → `"just now"`
 *  - <60s → `"Ns ago"`
 *  - <1h → `"Nm ago"`
 *  - <24h → `"Nh ago"`
 *  - older → ISO date string (no relative format).
 *
 * Exported only for tests; not part of the package's public API.
 */
export function relativeTimestamp(
  last: Date | null,
  now: Date,
): string | null {
  if (last === null) return null;
  const deltaMs = now.getTime() - last.getTime();
  if (deltaMs < 5_000) return 'just now';
  if (deltaMs < 60_000) return `${Math.floor(deltaMs / 1_000)}s ago`;
  if (deltaMs < 3_600_000) return `${Math.floor(deltaMs / 60_000)}m ago`;
  if (deltaMs < 86_400_000) return `${Math.floor(deltaMs / 3_600_000)}h ago`;
  return last.toISOString().slice(0, 10);
}
