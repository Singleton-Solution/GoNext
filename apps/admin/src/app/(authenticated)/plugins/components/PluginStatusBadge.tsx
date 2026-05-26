/**
 * Small colored pill that renders the current lifecycle state for a
 * plugin. The colour scheme matches the rest of the admin status
 * badges — emerald for active (alive), fg-muted for inactive (quiet
 * but installed), warning for transitional, and danger for errored —
 * so an operator scanning the list sees a consistent semantic.
 *
 * Brand
 * =====
 * Uses the canonical "Living systems" semantic palette directly off
 * the design tokens, mirroring how `.tag--emerald`, `.tag--warning`,
 * and `.tag--danger` are defined in tokens.css. Active plugins glow
 * emerald-soft / emerald-deep — the alive/positive treatment shared
 * with the marketplace surface. Inactive plugins land on paper-3 with
 * `--fg-muted` to read as quietly present, not absent. Errored
 * plugins use danger-soft / danger so they stand out without shouting.
 *
 * Pure: state in / span out. Lives in `components/` so the list and
 * detail screens can share it without round-tripping through a client
 * island. The badge is intentionally a `<span>` (not a `<button>` or
 * `<status>`) — semantic information is carried via the `aria-label`
 * on the wrapper plus a `data-state` attribute the test suite uses for
 * style assertions.
 */
import type { CSSProperties, ReactElement } from 'react';
import type { PluginState } from '../types';

const PALETTE: Record<PluginState, { bg: string; fg: string; label: string }> = {
  installed: {
    bg: 'var(--lavender-soft)',
    fg: 'var(--lavender-deep)',
    label: 'Installed',
  },
  active: {
    bg: 'var(--emerald-soft)',
    fg: 'var(--emerald-deep)',
    label: 'Active',
  },
  inactive: {
    bg: 'var(--paper-3)',
    fg: 'var(--fg-muted)',
    label: 'Inactive',
  },
  pending_uninstall: {
    bg: 'var(--warning-soft)',
    fg: 'var(--warning)',
    label: 'Uninstalling…',
  },
  errored: {
    bg: 'var(--danger-soft)',
    fg: 'var(--danger)',
    label: 'Errored',
  },
};

export interface PluginStatusBadgeProps {
  state: PluginState;
}

export function PluginStatusBadge({
  state,
}: PluginStatusBadgeProps): ReactElement {
  const spec = PALETTE[state];
  const style: CSSProperties = {
    display: 'inline-flex',
    alignItems: 'center',
    gap: 6,
    padding: '2px 10px',
    borderRadius: 'var(--r-pill)',
    background: spec.bg,
    color: spec.fg,
    fontFamily: 'var(--font-sans)',
    fontSize: 'var(--t-xs)',
    fontWeight: 500,
    letterSpacing: '0.01em',
    lineHeight: 1.4,
  };
  const dotStyle: CSSProperties = {
    width: 6,
    height: 6,
    borderRadius: 999,
    background: 'currentColor',
    opacity: 0.9,
    flex: '0 0 auto',
  };
  return (
    <span
      data-state={state}
      style={style}
      aria-label={`Status: ${spec.label}`}
    >
      <span aria-hidden="true" style={dotStyle} />
      {spec.label}
    </span>
  );
}
