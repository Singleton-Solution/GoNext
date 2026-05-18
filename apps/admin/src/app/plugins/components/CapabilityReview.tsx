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
    border: '1px solid var(--color-border, #e4e6ea)',
    borderRadius: 6,
    background: 'var(--color-surface, #ffffff)',
    overflow: 'hidden',
  },
  panelHeader: {
    padding: '12px 16px',
    background: '#f9fafb',
    borderBottom: '1px solid var(--color-border, #e4e6ea)',
  },
  panelTitle: {
    margin: 0,
    fontSize: 16,
    fontWeight: 600,
  },
  panelSubtitle: {
    margin: '4px 0 0',
    fontSize: 13,
    color: 'var(--color-text-muted, #6b7280)',
  },
  empty: {
    padding: '20px 16px',
    color: 'var(--color-text-muted, #6b7280)',
    fontSize: 14,
  },
  list: {
    listStyle: 'none',
    margin: 0,
    padding: 0,
  },
  row: {
    display: 'flex',
    alignItems: 'flex-start',
    gap: 12,
    padding: '12px 16px',
    borderTop: '1px solid var(--color-border, #e4e6ea)',
  },
  rowSensitive: {
    background: '#fff7ed',
  },
  rowUnknown: {
    background: '#fef9c3',
  },
  marker: {
    fontSize: 18,
    lineHeight: 1,
    marginTop: 2,
  },
  body: {
    flex: 1,
    minWidth: 0,
  },
  human: {
    margin: 0,
    fontSize: 14,
    fontWeight: 500,
    color: 'var(--color-text, #1c2024)',
  },
  meta: {
    marginTop: 4,
    fontSize: 12,
    color: 'var(--color-text-muted, #6b7280)',
    display: 'flex',
    flexWrap: 'wrap',
    gap: 8,
  },
  tag: {
    border: '1px solid var(--color-border, #e4e6ea)',
    borderRadius: 4,
    padding: '1px 6px',
    background: '#ffffff',
    fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
  },
  warnTag: {
    border: '1px solid #fdba74',
    background: '#ffedd5',
    color: '#9a3412',
    borderRadius: 4,
    padding: '1px 6px',
    fontWeight: 600,
  },
  footer: {
    padding: '12px 16px',
    borderTop: '1px solid var(--color-border, #e4e6ea)',
    background: '#fafafa',
    display: 'flex',
    alignItems: 'flex-start',
    gap: 12,
  },
  checkbox: {
    marginTop: 2,
  },
  consentText: {
    flex: 1,
    fontSize: 13,
    color: 'var(--color-text, #1c2024)',
    margin: 0,
  },
};

function markerFor(risk: CapabilityRisk, unknown: boolean): string {
  if (unknown) return '!';
  return risk === 'sensitive' ? '!' : '·';
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
                <span aria-hidden="true" style={styles.marker}>
                  {markerFor(cap.risk, isUnknown)}
                </span>
                <div style={styles.body}>
                  <p style={styles.human}>{cap.human}</p>
                  <div style={styles.meta}>
                    <span style={styles.tag}>{cap.id}</span>
                    {cap.risk === 'sensitive' ? (
                      <span style={styles.warnTag}>Sensitive</span>
                    ) : null}
                    {isUnknown ? (
                      <span style={styles.warnTag}>Unrecognised</span>
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
          <span
            id="capabilities_acknowledged_help"
            style={{
              display: 'block',
              marginTop: 2,
              color: 'var(--color-text-muted, #6b7280)',
            }}
          >
            You can uninstall the plugin from the plugins list at any time to
            revoke these permissions.
          </span>
        </label>
      </div>
    </section>
  );
}
