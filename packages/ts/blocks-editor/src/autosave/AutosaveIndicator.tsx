/**
 * `<AutosaveIndicator>` — the tiny "Saved · 12s ago" pill that lives
 * in the editor's top toolbar.
 *
 * Driven entirely by the `AutosaveState` returned from `useAutosave`.
 * The brand asks for an emerald-bright pulse dot followed by an
 * Instrument Serif italic timestamp on the forest top bar — see the
 * `.save-status` block in `docs/design/ui_kits/editor/index.html`.
 *
 * The component is stateless: it formats `state.lastSavedAt` against
 * the current wall clock (provided by `nowFn` so tests can pin the
 * clock) and re-renders every 30s so the "12s ago" / "1m ago"
 * label stays current without burning a `setInterval` per second.
 * The clock tick is cancelled cleanly on unmount.
 *
 * Why not bake this into the top-bar component? Two reasons. (1) The
 * autosave UI is the one piece the admin app *can* drop into its own
 * chrome — placing it next to the existing publish/schedule buttons
 * is a one-line concern. (2) Keeping the indicator small + headless-
 * but-visual matches how `<LockBanner>` works in the same folder; the
 * two components are friends, not siblings.
 *
 * Status mapping (mirrors the `AutosaveStatus` order in
 * `types.ts`):
 *   - `idle`   → no dot, neutral label ("Draft").
 *   - `saving` → pulsing emerald dot, label "Saving…".
 *   - `saved`  → steady emerald dot, label "Saved · <relative>".
 *   - `error`  → danger dot, label "Save failed".
 */
'use client';

import { useEffect, useState, type CSSProperties } from 'react';
import type { AutosaveState } from './types.ts';

export interface AutosaveIndicatorProps {
  /** The autosave state, typically straight from `useAutosave`. */
  state: AutosaveState;
  /**
   * Visual surface. `'forest'` is the default — the editor top bar is
   * a forest-dark surface. `'cream'` swaps to the on-cream colour
   * tokens (used in the `<LockBanner>`-adjacent footer affordance).
   */
  surface?: 'forest' | 'cream';
  /**
   * Injection seam for `Date.now` so the relative-timestamp tests
   * can pin the clock. Defaults to the real `Date.now`.
   */
  nowFn?: () => number;
  /** Optional className for the host pill. */
  className?: string;
}

/**
 * Format `lastSavedAt` as a relative-time label. We don't pull in a
 * date library — the budget is "12s ago" / "1m ago" / "12m ago" /
 * "an hour ago". Past that the user has bigger problems.
 */
function formatRelative(at: Date, now: number): string {
  const ms = now - at.getTime();
  if (ms < 0 || !Number.isFinite(ms)) return 'just now';
  const seconds = Math.round(ms / 1000);
  if (seconds < 5) return 'just now';
  if (seconds < 60) return `${seconds}s ago`;
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.round(hours / 24);
  return `${days}d ago`;
}

/** Surface tokens for the pill + dot. */
function pillStyle(surface: 'forest' | 'cream'): CSSProperties {
  if (surface === 'cream') {
    return {
      display: 'inline-flex',
      alignItems: 'center',
      gap: 'var(--s-2, 8px)',
      padding: '4px 10px',
      background: 'var(--paper-2, #EFEBE0)',
      border: '1px solid var(--border, #D9D2C0)',
      borderRadius: 'var(--r-pill, 999px)',
      fontFamily:
        "var(--font-sans, 'Geist', -apple-system, system-ui, sans-serif)",
      fontSize: 'var(--t-xs, 12px)',
      color: 'var(--fg-muted, #4A5C52)',
      lineHeight: 1,
    };
  }
  return {
    display: 'inline-flex',
    alignItems: 'center',
    gap: 'var(--s-2, 8px)',
    padding: '4px 10px',
    background: 'var(--forest-2, #18261E)',
    border: '1px solid var(--forest-border, #2C3D33)',
    borderRadius: 'var(--r-pill, 999px)',
    fontFamily:
      "var(--font-sans, 'Geist', -apple-system, system-ui, sans-serif)",
    fontSize: 'var(--t-xs, 12px)',
    color: 'var(--fg-on-forest-muted, #A8B5AC)',
    lineHeight: 1,
  };
}

const dotBase: CSSProperties = {
  width: 6,
  height: 6,
  borderRadius: 999,
  flexShrink: 0,
};

const dotEmerald: CSSProperties = {
  ...dotBase,
  background: 'var(--emerald-bright, #34D399)',
};

const dotPulsing: CSSProperties = {
  ...dotEmerald,
  animation: 'gonext-autosave-pulse 2s var(--ease, cubic-bezier(0.2, 0.7, 0.2, 1)) infinite',
};

const dotDanger: CSSProperties = {
  ...dotBase,
  background: 'var(--danger, #DC2626)',
};

const dotIdle: CSSProperties = {
  ...dotBase,
  background: 'var(--fg-faint, #94A199)',
};

const timestampStyle: CSSProperties = {
  fontFamily:
    "var(--font-serif, 'Instrument Serif', Georgia, serif)",
  fontStyle: 'italic',
  // Optical bump to match the +5% scale-up other italic accents use
  // (Headline's italic-accent rule, the topbar crumb italic, etc.).
  fontSize: '1.05em',
  letterSpacing: '-0.005em',
};

/**
 * Inline `<style>` block. Keeping the keyframes co-located keeps the
 * package self-contained — admin apps that load tokens.css don't need
 * to also know about an editor-specific keyframe. The dot only
 * pulses in `saving` state, so the cost of always emitting the rule
 * is one tiny stylesheet entry per indicator instance (de-duped by
 * the browser).
 */
function PulseKeyframes() {
  return (
    <style>{`
      @keyframes gonext-autosave-pulse {
        0%, 100% { opacity: 1; }
        50%      { opacity: 0.35; }
      }
    `}</style>
  );
}

export function AutosaveIndicator({
  state,
  surface = 'forest',
  nowFn,
  className,
}: AutosaveIndicatorProps) {
  // Re-render every 30s so the relative timestamp stays current. We
  // don't bother with a 1Hz tick — "12s ago" granularity is plenty
  // visible at a 30s cadence and the indicator pixel area is tiny.
  const [now, setNow] = useState<number>(() => (nowFn ?? Date.now)());
  useEffect(() => {
    const tick = () => setNow((nowFn ?? Date.now)());
    const id = setInterval(tick, 30_000);
    return () => clearInterval(id);
  }, [nowFn]);

  let dotStyle: CSSProperties;
  let label: React.ReactNode;
  let ariaLabel: string;

  switch (state.status) {
    case 'saving':
      dotStyle = dotPulsing;
      label = 'Saving…';
      ariaLabel = 'Saving';
      break;
    case 'saved': {
      dotStyle = dotEmerald;
      const ts =
        state.lastSavedAt !== null
          ? formatRelative(state.lastSavedAt, now)
          : 'just now';
      label = (
        <>
          Saved <span style={{ color: 'inherit' }}>·</span>{' '}
          <span
            data-testid="autosave-indicator-timestamp"
            style={timestampStyle}
          >
            {ts}
          </span>
        </>
      );
      ariaLabel = `Saved ${ts}`;
      break;
    }
    case 'error':
      dotStyle = dotDanger;
      label = 'Save failed';
      ariaLabel = state.error ?? 'Save failed';
      break;
    case 'idle':
    default:
      dotStyle = dotIdle;
      label = 'Draft';
      ariaLabel = 'Unsaved draft';
      break;
  }

  return (
    <span
      role="status"
      aria-live="polite"
      aria-label={ariaLabel}
      className={className}
      data-testid="autosave-indicator"
      data-status={state.status}
      data-surface={surface}
      style={pillStyle(surface)}
    >
      <PulseKeyframes />
      <span
        data-testid="autosave-indicator-dot"
        aria-hidden="true"
        style={dotStyle}
      />
      <span data-testid="autosave-indicator-label">{label}</span>
    </span>
  );
}
