/**
 * CapabilityReview — the centerpiece of the plugin install flow.
 *
 * Renders every capability the candidate manifest requests as a row
 * with an operator-voice description ("Send transactional email on
 * behalf of this site") so a human can make an informed consent
 * decision *before* the install request is fired.
 *
 * Design notes
 * ============
 *
 *  - "Verbatim" is non-negotiable: every capability in the manifest
 *    shows up, in declaration order, with no dedup and no hiding. If
 *    the same id is declared twice we render two rows; if an id is
 *    not in the local registry we render an "Unrecognised" row that
 *    warns the operator. The host registry is authoritative for the
 *    final decision — this screen exists to make sure the operator
 *    sees what they're agreeing to.
 *
 *  - Sensitive capabilities (`email.send`, `http.fetch`, anything the
 *    host registry flagged Sensitive) get a high-contrast warning
 *    treatment so they don't blend into the list. The whole point of
 *    the screen is that "Send email" should never look like "Read
 *    posts".
 *
 *  - The acknowledgement checkbox is required for the install action
 *    to proceed; the parent form propagates it via FormData. We do not
 *    auto-tick it — defaulting consent to true would defeat the
 *    purpose of the screen.
 *
 *  - Brand: each capability row is its own `--paper-2` card with the
 *    canonical hairline border. Sensitive rows swap to a `--lavender`
 *    accent — the secondary brand colour, signalling "look here". An
 *    emerald checkmark appears once the consent box is ticked,
 *    confirming the row has been reviewed alongside the panel. Both
 *    treatments stay accessible: the row carries a data attribute,
 *    `aria-label`s spell out the risk, and the marker is text not
 *    colour.
 *
 * Pure-ish: takes a manifest's `capabilities` array, renders the
 * review UI, and emits the acknowledgement state via the controlled
 * `onAcknowledgeChange` callback. No side effects; no fetch.
 *
 * This file is a Client Component because it owns the acknowledgement
 * checkbox state. The empty-state and read-only renderings could be
 * RSC, but splitting that out adds more complexity than it saves at
 * this scale.
 */
'use client';

import type { CSSProperties, ReactElement } from 'react';
import {
  resolveCapabilities,
  type CapabilityInfo,
  type CapabilityRisk,
} from '../capability-registry';

const styles: Record<string, CSSProperties> = {
  panel: {
    border: '1px solid var(--border)',
    borderRadius: 'var(--r-lg)',
    background: 'var(--paper-2)',
    overflow: 'hidden',
    boxShadow: 'var(--sh-xs)',
  },
  panelHeader: {
    padding: '16px 20px',
    background: 'var(--paper)',
    borderBottom: '1px solid var(--border)',
  },
  panelTitle: {
    margin: 0,
    fontFamily: 'var(--font-sans)',
    fontWeight: 600,
    fontSize: 'var(--t-lg)',
    color: 'var(--ink)',
    letterSpacing: '-0.005em',
  },
  panelSubtitle: {
    margin: '4px 0 0',
    fontFamily: 'var(--font-sans)',
    fontSize: 'var(--t-sm)',
    color: 'var(--fg-muted)',
  },
  empty: {
    padding: '24px 20px',
    color: 'var(--fg-muted)',
    fontFamily: 'var(--font-sans)',
    fontSize: 'var(--t-sm)',
  },
  list: {
    listStyle: 'none',
    margin: 0,
    padding: '12px',
    display: 'flex',
    flexDirection: 'column',
    gap: 8,
  },
  row: {
    display: 'flex',
    alignItems: 'flex-start',
    gap: 12,
    padding: '14px 16px',
    background: 'var(--paper-2)',
    border: '1px solid var(--border)',
    borderRadius: 'var(--r-md)',
    transition: 'border-color var(--dur) var(--ease)',
  },
  rowSensitive: {
    background: 'var(--lavender-soft)',
    borderColor: 'var(--lavender-soft)',
  },
  rowUnknown: {
    background: 'var(--warning-soft)',
    borderColor: 'var(--warning-soft)',
  },
  marker: {
    display: 'inline-flex',
    alignItems: 'center',
    justifyContent: 'center',
    width: 22,
    height: 22,
    borderRadius: 999,
    fontFamily: 'var(--font-sans)',
    fontWeight: 700,
    fontSize: 12,
    lineHeight: 1,
    flex: '0 0 auto',
    marginTop: 1,
  },
  markerLow: {
    background: 'var(--paper)',
    color: 'var(--fg-muted)',
    border: '1px solid var(--border)',
  },
  markerSensitive: {
    background: 'var(--lavender)',
    color: '#ffffff',
  },
  markerUnknown: {
    background: 'var(--warning)',
    color: '#ffffff',
  },
  markerConsented: {
    background: 'var(--emerald)',
    color: 'var(--emerald-ink)',
    border: '1px solid var(--emerald)',
  },
  body: {
    flex: 1,
    minWidth: 0,
  },
  human: {
    margin: 0,
    fontFamily: 'var(--font-sans)',
    fontWeight: 500,
    fontSize: 'var(--t-sm)',
    color: 'var(--ink)',
  },
  meta: {
    marginTop: 6,
    display: 'flex',
    flexWrap: 'wrap',
    gap: 6,
    alignItems: 'center',
  },
  capId: {
    fontFamily: 'var(--font-mono)',
    fontSize: 'var(--t-xs)',
    color: 'var(--fg-muted)',
    background: 'var(--paper)',
    border: '1px solid var(--border)',
    borderRadius: 'var(--r-xs)',
    padding: '1px 6px',
  },
  tagSensitive: {
    fontFamily: 'var(--font-sans)',
    fontSize: 'var(--t-2xs)',
    color: 'var(--lavender-deep)',
    background: 'var(--paper)',
    border: '1px solid var(--lavender)',
    borderRadius: 'var(--r-xs)',
    padding: '1px 6px',
    fontWeight: 600,
    letterSpacing: '0.04em',
    textTransform: 'uppercase',
  },
  tagUnknown: {
    fontFamily: 'var(--font-sans)',
    fontSize: 'var(--t-2xs)',
    color: 'var(--warning)',
    background: 'var(--paper)',
    border: '1px solid var(--warning)',
    borderRadius: 'var(--r-xs)',
    padding: '1px 6px',
    fontWeight: 600,
    letterSpacing: '0.04em',
    textTransform: 'uppercase',
  },
  footer: {
    padding: '14px 20px',
    borderTop: '1px solid var(--border)',
    background: 'var(--paper)',
    display: 'flex',
    alignItems: 'flex-start',
    gap: 12,
  },
  checkbox: {
    marginTop: 3,
    width: 16,
    height: 16,
    accentColor: 'var(--emerald)',
    cursor: 'pointer',
  },
  consentText: {
    flex: 1,
    fontFamily: 'var(--font-sans)',
    fontSize: 'var(--t-sm)',
    color: 'var(--ink)',
    margin: 0,
    cursor: 'pointer',
  },
  consentHelp: {
    display: 'block',
    marginTop: 4,
    color: 'var(--fg-muted)',
    fontSize: 'var(--t-xs)',
  },
};

function markerSymbol(risk: CapabilityRisk, unknown: boolean, acknowledged: boolean): string {
  if (acknowledged && !unknown) return '✓';
  if (unknown) return '!';
  return risk === 'sensitive' ? '!' : '·';
}

function markerStyle(
  risk: CapabilityRisk,
  unknown: boolean,
  acknowledged: boolean,
): CSSProperties {
  if (acknowledged && !unknown) {
    return { ...styles.marker, ...styles.markerConsented };
  }
  if (unknown) {
    return { ...styles.marker, ...styles.markerUnknown };
  }
  if (risk === 'sensitive') {
    return { ...styles.marker, ...styles.markerSensitive };
  }
  return { ...styles.marker, ...styles.markerLow };
}

export interface CapabilityReviewProps {
  /** Capability ids declared by the candidate manifest, in order. */
  capabilities: readonly string[];
  /** Whether the operator has ticked the consent box. */
  acknowledged: boolean;
  /** Notified when the consent box flips. */
  onAcknowledgeChange: (next: boolean) => void;
  /** Optional disabled flag — used during pending install. */
  disabled?: boolean;
}

/**
 * Render the per-capability review with a closing consent checkbox.
 *
 * Empty capability list: the manifest didn't ask for anything, so we
 * render a calm "no special permissions" panel rather than the warning
 * list. The acknowledgement checkbox is still present because consent
 * is a property of the install operation, not the cap list.
 */
export function CapabilityReview({
  capabilities,
  acknowledged,
  onAcknowledgeChange,
  disabled,
}: CapabilityReviewProps): ReactElement {
  const resolved: CapabilityInfo[] = resolveCapabilities(capabilities);
  const sensitiveCount = resolved.filter((c) => c.risk === 'sensitive').length;

  return (
    <section style={styles.panel} aria-label="Capability review">
      <header style={styles.panelHeader}>
        <h3 style={styles.panelTitle}>
          Capability review
          {capabilities.length > 0
            ? ` (${capabilities.length}${
                sensitiveCount > 0 ? `, ${sensitiveCount} sensitive` : ''
              })`
            : ''}
        </h3>
        <p style={styles.panelSubtitle}>
          The plugin is requesting the following permissions. Review them
          carefully — you can revoke any of them by uninstalling the plugin.
        </p>
      </header>

      {capabilities.length === 0 ? (
        <div style={styles.empty}>
          This plugin doesn’t request any special permissions.
        </div>
      ) : (
        <ul style={styles.list} data-testid="capability-review-list">
          {resolved.map((cap, idx) => {
            const isUnknown =
              cap.description ===
              'Unknown capability — not in the host registry.';
            const rowStyle: CSSProperties = {
              ...styles.row,
              ...(cap.risk === 'sensitive' ? styles.rowSensitive : {}),
              ...(isUnknown ? styles.rowUnknown : {}),
            };
            return (
              <li
                // Index is part of the key because a manifest may legitimately
                // declare the same cap twice; we render both, in order.
                key={`${cap.id}-${idx}`}
                style={rowStyle}
                data-capability-id={cap.id}
                data-capability-risk={cap.risk}
              >
                <span
                  aria-hidden="true"
                  style={markerStyle(cap.risk, isUnknown, acknowledged)}
                >
                  {markerSymbol(cap.risk, isUnknown, acknowledged)}
                </span>
                <div style={styles.body}>
                  <p style={styles.human}>{cap.human}</p>
                  <div style={styles.meta}>
                    <span style={styles.capId}>{cap.id}</span>
                    {cap.risk === 'sensitive' ? (
                      <span style={styles.tagSensitive}>Sensitive</span>
                    ) : null}
                    {isUnknown ? (
                      <span style={styles.tagUnknown}>Unrecognised</span>
                    ) : null}
                  </div>
                </div>
              </li>
            );
          })}
        </ul>
      )}

      <div style={styles.footer}>
        <input
          id="capabilities_acknowledged"
          name="capabilities_acknowledged"
          type="checkbox"
          checked={acknowledged}
          onChange={(e) => onAcknowledgeChange(e.target.checked)}
          disabled={disabled}
          style={styles.checkbox}
          aria-describedby="capabilities_acknowledged_help"
        />
        <label htmlFor="capabilities_acknowledged" style={styles.consentText}>
          <strong>I’ve reviewed the capabilities above</strong> and accept that
          this plugin will be granted them once installed.
          <span id="capabilities_acknowledged_help" style={styles.consentHelp}>
            You can uninstall the plugin from the plugins list at any time to
            revoke these permissions.
          </span>
        </label>
      </div>
    </section>
  );
}
