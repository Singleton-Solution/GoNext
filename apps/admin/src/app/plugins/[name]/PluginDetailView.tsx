'use client';

/**
 * PluginDetailView — read-only detail rendering for one plugin row.
 *
 * Receives the full record (manifest, dependencies, error block) and
 * renders the canonical sections in document order:
 *
 *   1. Title + state badge + version
 *   2. Manifest summary card — name, description, author, entry, abi
 *   3. Declared capabilities table — every cap with its human-readable
 *      description; the same component the install screen uses
 *   4. Dependencies status table — each declared dep with its resolved
 *      version and a satisfied / unsatisfied chip
 *   5. Install / activation timestamps
 *   6. Last-error block when the lifecycle row is in `errored`
 *
 * Pure rendering. The list page owns the action buttons; the detail
 * view is intentionally read-only at this scale (the spec calls out
 * the install / uninstall flow on the list and install pages, not
 * here). A follow-up issue can add per-detail actions once the host
 * exposes a "rotate signing key" / "view audit log" surface.
 */
import type { CSSProperties, ReactElement } from 'react';
import {
  resolveCapabilities,
  type CapabilityInfo,
} from '../capability-registry';
import { PluginStatusBadge } from '../components/PluginStatusBadge';
import type { PluginDependencyStatus, PluginRecord } from '../types';

const styles: Record<string, CSSProperties> = {
  header: {
    display: 'flex',
    alignItems: 'center',
    gap: 12,
    marginBottom: 8,
    flexWrap: 'wrap',
  },
  title: { margin: 0, fontSize: 22, fontWeight: 600 },
  version: {
    fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
    fontSize: 14,
    color: 'var(--color-text-muted, #6b7280)',
  },
  subtitle: {
    margin: '0 0 24px',
    color: 'var(--color-text-muted, #6b7280)',
    fontSize: 14,
  },
  section: {
    background: 'var(--color-surface, #ffffff)',
    border: '1px solid var(--color-border, #e4e6ea)',
    borderRadius: 6,
    padding: 16,
    marginBottom: 16,
  },
  sectionTitle: {
    margin: 0,
    fontSize: 14,
    fontWeight: 600,
    textTransform: 'uppercase',
    letterSpacing: '0.04em',
    color: 'var(--color-text-muted, #6b7280)',
    marginBottom: 12,
  },
  metaTable: { width: '100%', borderCollapse: 'collapse', fontSize: 14 },
  metaTd: {
    padding: '6px 8px',
    borderBottom: '1px solid var(--color-border, #e4e6ea)',
    verticalAlign: 'top',
  },
  metaKey: {
    color: 'var(--color-text-muted, #6b7280)',
    width: 160,
    fontWeight: 500,
  },
  monoBlock: {
    margin: 0,
    padding: 12,
    background: '#f9fafb',
    border: '1px solid var(--color-border, #e4e6ea)',
    borderRadius: 6,
    fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
    fontSize: 13,
    overflowX: 'auto',
    whiteSpace: 'pre-wrap',
    wordBreak: 'break-word',
  },
  errorBlock: {
    background: '#fef2f2',
    border: '1px solid #fecaca',
    color: '#7f1d1d',
    borderRadius: 6,
    padding: 16,
  },
  capList: { listStyle: 'none', margin: 0, padding: 0 },
  capRow: {
    display: 'flex',
    alignItems: 'flex-start',
    gap: 12,
    padding: '8px 0',
    borderTop: '1px solid var(--color-border, #e4e6ea)',
  },
  capId: {
    fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
    fontSize: 13,
    color: 'var(--color-text-muted, #6b7280)',
    minWidth: 140,
  },
  capDesc: { fontSize: 14 },
  sensitive: {
    border: '1px solid #fdba74',
    background: '#ffedd5',
    color: '#9a3412',
    borderRadius: 4,
    padding: '1px 6px',
    fontSize: 12,
    fontWeight: 600,
    marginLeft: 6,
  },
  depChipSatisfied: {
    background: '#dcfce7',
    color: '#166534',
    borderRadius: 999,
    padding: '2px 8px',
    fontSize: 12,
    fontWeight: 600,
  },
  depChipUnsatisfied: {
    background: '#fee2e2',
    color: '#991b1b',
    borderRadius: 999,
    padding: '2px 8px',
    fontSize: 12,
    fontWeight: 600,
  },
};

function formatTimestamp(raw: string | null): string {
  if (!raw) return '—';
  const d = new Date(raw);
  if (Number.isNaN(d.getTime())) return raw;
  return d.toLocaleString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  });
}

function CapabilityRow({ cap }: { cap: CapabilityInfo }): ReactElement {
  return (
    <li style={styles.capRow} data-capability-id={cap.id}>
      <span style={styles.capId}>{cap.id}</span>
      <span style={styles.capDesc}>
        {cap.human}
        {cap.risk === 'sensitive' ? (
          <span style={styles.sensitive}>Sensitive</span>
        ) : null}
      </span>
    </li>
  );
}

function DependencyRow({
  dep,
}: {
  dep: PluginDependencyStatus;
}): ReactElement {
  return (
    <li
      style={styles.capRow}
      data-dep-name={dep.name}
      data-dep-satisfied={dep.satisfied ? 'true' : 'false'}
    >
      <span style={styles.capId}>{dep.name}</span>
      <span style={styles.capDesc}>
        Requires <code>{dep.version}</code>
        {dep.resolvedVersion ? (
          <>
            {' '}— installed: <code>{dep.resolvedVersion}</code>
          </>
        ) : (
          <> — not installed</>
        )}
        <span
          style={
            dep.satisfied
              ? styles.depChipSatisfied
              : styles.depChipUnsatisfied
          }
          aria-label={
            dep.satisfied ? 'Dependency satisfied' : 'Dependency unsatisfied'
          }
        >
          {dep.satisfied ? 'Satisfied' : 'Unsatisfied'}
        </span>
        {!dep.satisfied && dep.reason ? (
          <span style={{ marginLeft: 8, color: '#7f1d1d' }}>{dep.reason}</span>
        ) : null}
      </span>
    </li>
  );
}

export interface PluginDetailViewProps {
  plugin: PluginRecord;
}

export function PluginDetailView({
  plugin,
}: PluginDetailViewProps): ReactElement {
  const { manifest, capabilities, lastError, dependenciesStatus } = plugin;
  const resolvedCaps = resolveCapabilities(capabilities);

  return (
    <section>
      <div style={styles.header}>
        <h1 style={styles.title}>{plugin.displayName || plugin.name}</h1>
        <span style={styles.version}>v{plugin.version}</span>
        <PluginStatusBadge state={plugin.state} />
      </div>
      <p style={styles.subtitle}>
        {manifest?.description ??
          'Plugin detail — manifest summary, declared capabilities, and dependency status.'}
      </p>

      <div style={styles.section} data-section="manifest">
        <h2 style={styles.sectionTitle}>Manifest</h2>
        <table style={styles.metaTable}>
          <tbody>
            <tr>
              <td style={{ ...styles.metaTd, ...styles.metaKey }}>Slug</td>
              <td style={styles.metaTd}>
                <code>{plugin.name}</code>
              </td>
            </tr>
            <tr>
              <td style={{ ...styles.metaTd, ...styles.metaKey }}>Version</td>
              <td style={styles.metaTd}>
                <code>{plugin.version}</code>
              </td>
            </tr>
            {manifest?.apiVersion ? (
              <tr>
                <td style={{ ...styles.metaTd, ...styles.metaKey }}>
                  API version
                </td>
                <td style={styles.metaTd}>
                  <code>{manifest.apiVersion}</code>
                </td>
              </tr>
            ) : null}
            {manifest?.author ? (
              <tr>
                <td style={{ ...styles.metaTd, ...styles.metaKey }}>Author</td>
                <td style={styles.metaTd}>{manifest.author}</td>
              </tr>
            ) : null}
            {manifest?.homepage ? (
              <tr>
                <td style={{ ...styles.metaTd, ...styles.metaKey }}>
                  Homepage
                </td>
                <td style={styles.metaTd}>
                  <a href={manifest.homepage} rel="noreferrer noopener">
                    {manifest.homepage}
                  </a>
                </td>
              </tr>
            ) : null}
            {manifest?.entry ? (
              <tr>
                <td style={{ ...styles.metaTd, ...styles.metaKey }}>Entry</td>
                <td style={styles.metaTd}>
                  <code>{manifest.entry}</code>
                </td>
              </tr>
            ) : null}
            {manifest?.requires?.abi != null ? (
              <tr>
                <td style={{ ...styles.metaTd, ...styles.metaKey }}>
                  ABI version
                </td>
                <td style={styles.metaTd}>
                  <code>{manifest.requires.abi}</code>
                </td>
              </tr>
            ) : null}
            <tr>
              <td style={{ ...styles.metaTd, ...styles.metaKey }}>
                Installed
              </td>
              <td style={styles.metaTd}>{formatTimestamp(plugin.installedAt)}</td>
            </tr>
            <tr>
              <td style={{ ...styles.metaTd, ...styles.metaKey }}>
                Last activated
              </td>
              <td style={styles.metaTd}>{formatTimestamp(plugin.activatedAt)}</td>
            </tr>
          </tbody>
        </table>
      </div>

      <div style={styles.section} data-section="capabilities">
        <h2 style={styles.sectionTitle}>
          Capabilities ({capabilities.length})
        </h2>
        {capabilities.length === 0 ? (
          <p style={{ margin: 0, color: 'var(--color-text-muted, #6b7280)' }}>
            This plugin doesn’t request any special permissions.
          </p>
        ) : (
          <ul style={styles.capList} data-testid="detail-capability-list">
            {resolvedCaps.map((cap, idx) => (
              <CapabilityRow key={`${cap.id}-${idx}`} cap={cap} />
            ))}
          </ul>
        )}
      </div>

      <div style={styles.section} data-section="dependencies">
        <h2 style={styles.sectionTitle}>
          Dependencies ({dependenciesStatus?.length ?? 0})
        </h2>
        {!dependenciesStatus || dependenciesStatus.length === 0 ? (
          <p style={{ margin: 0, color: 'var(--color-text-muted, #6b7280)' }}>
            This plugin doesn’t depend on any other plugins.
          </p>
        ) : (
          <ul style={styles.capList} data-testid="detail-dependency-list">
            {dependenciesStatus.map((dep, idx) => (
              <DependencyRow key={`${dep.name}-${idx}`} dep={dep} />
            ))}
          </ul>
        )}
      </div>

      {lastError ? (
        <div
          style={styles.errorBlock}
          role="alert"
          data-section="last-error"
        >
          <h2
            style={{ ...styles.sectionTitle, color: '#7f1d1d', marginBottom: 8 }}
          >
            Last error
          </h2>
          <p style={{ margin: 0 }}>
            <strong>{formatTimestamp(lastError.at)}</strong>
            <br />
            {lastError.message}
          </p>
        </div>
      ) : null}

      {plugin.manifestRaw ? (
        <div style={styles.section} data-section="manifest-raw">
          <h2 style={styles.sectionTitle}>Raw manifest</h2>
          <pre style={styles.monoBlock}>{plugin.manifestRaw}</pre>
        </div>
      ) : null}
    </section>
  );
}
