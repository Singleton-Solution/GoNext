/**
 * Small colored pill that renders the current lifecycle state for a
 * plugin. The colour scheme matches the rest of the admin status
 * badges (UsersList, PostListClient) — green for healthy, amber for
 * transitional, red for errored — so an operator scanning the list
 * sees a consistent semantic.
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
  installed: { bg: '#e0e7ff', fg: '#3730a3', label: 'Installed' },
  active: { bg: '#dcfce7', fg: '#166534', label: 'Active' },
  inactive: { bg: '#f3f4f6', fg: '#4b5563', label: 'Inactive' },
  pending_uninstall: { bg: '#fef3c7', fg: '#92400e', label: 'Uninstalling…' },
  errored: { bg: '#fee2e2', fg: '#991b1b', label: 'Errored' },
};

export interface PluginStatusBadgeProps {
  state: PluginState;
}

export function PluginStatusBadge({
  state,
}: PluginStatusBadgeProps): ReactElement {
  const spec = PALETTE[state];
  const style: CSSProperties = {
    display: 'inline-block',
    padding: '2px 8px',
    borderRadius: 999,
    background: spec.bg,
    color: spec.fg,
    fontSize: 12,
    fontWeight: 600,
    letterSpacing: '0.01em',
  };
  return (
    <span
      data-state={state}
      style={style}
      aria-label={`Status: ${spec.label}`}
    >
      {spec.label}
    </span>
  );
}
