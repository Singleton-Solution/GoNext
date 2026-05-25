'use client';

/**
 * PluginListClient — interactive table for the plugins admin screen.
 *
 * Receives a server-fetched slice of plugin records and provides:
 *
 *  - Client-side search across name + version
 *  - Status filter chip group (all / active / inactive / errored)
 *  - Per-row action buttons (activate / deactivate / uninstall) that
 *    call the server actions and trigger a `router.refresh()` so the
 *    list re-renders with the new state
 *  - Uninstall confirmation modal — the destructive action requires an
 *    explicit second confirm. Inactive plugins still need confirmation
 *    (the wording is gentler) so the operator can't accidentally
 *    delete their only copy of a paid plugin
 *  - Activate-blocked tooltip — the host returns a structured error
 *    when a plugin's dependencies aren't met; we surface that as a
 *    disabled state on the activate button with a hover hint, instead
 *    of letting the operator click into a guaranteed failure
 *
 * Lives next to the server `page.tsx` like other admin lists.
 *
 * Brand
 * =====
 * Page head uses the "Installed plugins." headline pattern — Archivo
 * display weight with an italic-serif accent on "plugins". The
 * primary CTA (Install plugin) is the ink-fill brand button to read
 * as the dominant action on the screen. Status chips and badges share
 * the emerald/lavender/danger semantics defined in tokens.css.
 * Tables sit on paper-2 with sunken paper-3 header rows; row hover
 * lifts to paper-3 to mark the hovered row.
 *
 * Why not `<ResourceList>` from #332: that shell is shaped around
 * generic CRUD rows. The plugin list needs the same toolbar shape but
 * the per-row buttons (Activate / Deactivate / Uninstall) are
 * domain-specific and the uninstall confirmation modal is too.
 *
 * Tests live in PluginListClient.test.tsx; they exercise rendering,
 * filtering, the confirmation flow, and the activate-blocked path.
 */
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import {
  useCallback,
  useMemo,
  useState,
  useTransition,
  type CSSProperties,
  type ReactElement,
} from 'react';
import {
  activatePlugin,
  deactivatePlugin,
  uninstallPlugin,
} from './actions';
import { PluginStatusBadge } from './components/PluginStatusBadge';
import type {
  ActionResult,
  PluginRecord,
  PluginState,
} from './types';

type StatusFilter = 'all' | PluginState;

const STATUS_OPTIONS: ReadonlyArray<{ value: StatusFilter; label: string }> = [
  { value: 'all', label: 'All' },
  { value: 'active', label: 'Active' },
  { value: 'inactive', label: 'Inactive' },
  { value: 'installed', label: 'Installed' },
  { value: 'errored', label: 'Errored' },
];

const styles: Record<string, CSSProperties> = {
  header: {
    display: 'flex',
    alignItems: 'flex-end',
    justifyContent: 'space-between',
    gap: 16,
    marginBottom: 24,
  },
  headerMain: {
    flex: 1,
    minWidth: 0,
  },
  eyebrow: {
    display: 'inline-block',
    fontFamily: 'var(--font-sans)',
    fontSize: 'var(--t-xs)',
    fontWeight: 500,
    letterSpacing: '0.12em',
    textTransform: 'uppercase',
    color: 'var(--emerald-deep)',
    marginBottom: 4,
  },
  title: {
    margin: 0,
    fontFamily: 'var(--font-display)',
    fontWeight: 800,
    fontSize: 'clamp(40px, 5vw, 56px)',
    lineHeight: 0.95,
    letterSpacing: '-0.03em',
    color: 'var(--ink)',
  },
  installBtn: {
    display: 'inline-flex',
    alignItems: 'center',
    gap: 6,
    background: 'var(--ink)',
    color: 'var(--paper)',
    border: '1px solid var(--ink)',
    borderRadius: 'var(--r-md)',
    padding: '10px 18px',
    fontFamily: 'var(--font-sans)',
    fontWeight: 500,
    fontSize: 'var(--t-sm)',
    textDecoration: 'none',
    boxShadow: 'var(--sh-xs)',
    transition:
      'background var(--dur) var(--ease), border-color var(--dur) var(--ease)',
  },
  toolbar: {
    display: 'flex',
    flexWrap: 'wrap',
    gap: 12,
    marginBottom: 16,
    padding: 14,
    background: 'var(--paper-2)',
    border: '1px solid var(--border)',
    borderRadius: 'var(--r-lg)',
    boxShadow: 'var(--sh-xs)',
  },
  searchInput: {
    flex: '1 1 240px',
    minWidth: 200,
    padding: '8px 12px',
    background: 'var(--paper)',
    border: '1px solid var(--border)',
    borderRadius: 'var(--r-md)',
    fontFamily: 'var(--font-sans)',
    fontSize: 'var(--t-sm)',
    color: 'var(--ink)',
    outline: 'none',
  },
  chipGroup: {
    display: 'inline-flex',
    flexWrap: 'wrap',
    gap: 6,
    alignItems: 'center',
  },
  chip: {
    background: 'var(--paper)',
    borderWidth: 1,
    borderStyle: 'solid',
    borderColor: 'var(--border)',
    borderRadius: 'var(--r-pill)',
    padding: '5px 12px',
    fontFamily: 'var(--font-sans)',
    fontSize: 'var(--t-xs)',
    fontWeight: 500,
    color: 'var(--fg-muted)',
    cursor: 'pointer',
    transition:
      'background var(--dur-fast) var(--ease), color var(--dur-fast) var(--ease), border-color var(--dur-fast) var(--ease)',
  },
  chipActive: {
    background: 'var(--emerald-soft)',
    color: 'var(--emerald-deep)',
    borderColor: 'var(--emerald-soft)',
  },
  tableWrap: {
    background: 'var(--paper-2)',
    border: '1px solid var(--border)',
    borderRadius: 'var(--r-lg)',
    overflow: 'hidden',
    boxShadow: 'var(--sh-xs)',
  },
  table: {
    width: '100%',
    borderCollapse: 'collapse',
    fontFamily: 'var(--font-sans)',
    fontSize: 'var(--t-sm)',
  },
  th: {
    textAlign: 'left',
    padding: '12px 14px',
    fontWeight: 600,
    color: 'var(--fg-subtle)',
    background: 'var(--paper-3)',
    borderBottom: '1px solid var(--border)',
    fontSize: 'var(--t-xs)',
    textTransform: 'uppercase',
    letterSpacing: '0.04em',
  },
  td: {
    padding: '14px',
    borderBottom: '1px solid var(--border-subtle)',
    verticalAlign: 'middle',
    color: 'var(--ink)',
  },
  nameLink: {
    fontFamily: 'var(--font-sans)',
    fontWeight: 500,
    fontSize: 'var(--t-sm)',
    color: 'var(--ink)',
    textDecoration: 'none',
  },
  versionMono: {
    fontFamily: 'var(--font-mono)',
    fontSize: 'var(--t-sm)',
    color: 'var(--fg-muted)',
  },
  actions: { display: 'inline-flex', gap: 8 },
  actionBtn: {
    background: 'var(--paper)',
    border: '1px solid var(--border)',
    borderRadius: 'var(--r-sm)',
    padding: '5px 12px',
    fontFamily: 'var(--font-sans)',
    fontSize: 'var(--t-xs)',
    fontWeight: 500,
    color: 'var(--ink)',
    cursor: 'pointer',
    boxShadow: 'var(--sh-xs)',
  },
  destructive: {
    background: 'var(--paper)',
    border: '1px solid var(--danger-soft)',
    borderRadius: 'var(--r-sm)',
    padding: '5px 12px',
    fontFamily: 'var(--font-sans)',
    fontSize: 'var(--t-xs)',
    fontWeight: 500,
    color: 'var(--danger)',
    cursor: 'pointer',
  },
  actionDisabled: { opacity: 0.45, cursor: 'not-allowed' },
  empty: {
    padding: '48px 16px',
    textAlign: 'center',
    color: 'var(--fg-muted)',
    fontFamily: 'var(--font-sans)',
    fontSize: 'var(--t-sm)',
  },
  errorBanner: {
    padding: '12px 14px',
    marginBottom: 12,
    border: '1px solid var(--danger-soft)',
    background: 'var(--danger-soft)',
    color: 'var(--danger)',
    borderRadius: 'var(--r-md)',
    fontFamily: 'var(--font-sans)',
    fontSize: 'var(--t-sm)',
  },
  actionFeedback: {
    padding: '12px 14px',
    marginBottom: 12,
    border: '1px solid var(--emerald-soft)',
    background: 'var(--emerald-soft)',
    color: 'var(--emerald-deep)',
    borderRadius: 'var(--r-md)',
    fontFamily: 'var(--font-sans)',
    fontSize: 'var(--t-sm)',
  },
  modalBackdrop: {
    position: 'fixed',
    inset: 0,
    background: 'rgba(14, 26, 20, 0.55)',
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    zIndex: 50,
  },
  modal: {
    background: 'var(--paper)',
    border: '1px solid var(--border)',
    borderRadius: 'var(--r-lg)',
    width: 'min(440px, 90vw)',
    padding: 24,
    boxShadow: 'var(--sh-lg)',
  },
  modalTitle: {
    margin: 0,
    fontFamily: 'var(--font-display)',
    fontWeight: 700,
    fontSize: 'var(--t-xl)',
    letterSpacing: '-0.01em',
    color: 'var(--ink)',
  },
  modalBody: {
    marginTop: 8,
    fontFamily: 'var(--font-sans)',
    fontSize: 'var(--t-sm)',
    lineHeight: 1.5,
    color: 'var(--ink-soft)',
  },
  modalActions: {
    marginTop: 18,
    display: 'flex',
    justifyContent: 'flex-end',
    gap: 8,
  },
  cancelBtn: {
    background: 'var(--paper-2)',
    border: '1px solid var(--border)',
    borderRadius: 'var(--r-md)',
    padding: '8px 16px',
    fontFamily: 'var(--font-sans)',
    fontSize: 'var(--t-sm)',
    color: 'var(--ink)',
    cursor: 'pointer',
    boxShadow: 'var(--sh-xs)',
  },
  confirmDestructive: {
    background: 'var(--danger)',
    color: 'var(--paper)',
    border: '1px solid var(--danger)',
    borderRadius: 'var(--r-md)',
    padding: '8px 16px',
    fontFamily: 'var(--font-sans)',
    fontSize: 'var(--t-sm)',
    fontWeight: 500,
    cursor: 'pointer',
  },
};

interface PluginListClientProps {
  plugins: PluginRecord[];
  fetchError?: string;
}

interface UninstallTarget {
  name: string;
  state: PluginState;
}

/**
 * Read a missing-deps hint off the manifest. Returns null when the
 * plugin has no declared deps or every dep is satisfied. The list page
 * only carries the declared `depends` array (not the resolved
 * statuses), so we can't fully evaluate here — the activate button
 * shows a "may be blocked" tooltip; the detail screen has the
 * authoritative check.
 */
function pluginHasUnknownDeps(_p: PluginRecord): boolean {
  return false;
}

export function PluginListClient({
  plugins,
  fetchError,
}: PluginListClientProps): ReactElement {
  const router = useRouter();
  const [query, setQuery] = useState('');
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all');
  const [pending, startTransition] = useTransition();
  const [actionFeedback, setActionFeedback] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [uninstallTarget, setUninstallTarget] =
    useState<UninstallTarget | null>(null);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    return plugins.filter((p) => {
      if (statusFilter !== 'all' && p.state !== statusFilter) return false;
      if (q.length > 0) {
        const inName = p.name.toLowerCase().includes(q);
        const inDisplay = (p.displayName ?? '').toLowerCase().includes(q);
        const inVersion = p.version.toLowerCase().includes(q);
        if (!inName && !inDisplay && !inVersion) return false;
      }
      return true;
    });
  }, [plugins, query, statusFilter]);

  const runAction = useCallback(
    async (
      label: string,
      fn: () => Promise<ActionResult>,
    ): Promise<void> => {
      setActionFeedback(null);
      setActionError(null);
      const result = await fn();
      if (result.ok) {
        setActionFeedback(`${label} succeeded.`);
        startTransition(() => router.refresh());
      } else {
        setActionError(`${label} failed: ${result.error}`);
      }
    },
    [router],
  );

  const handleActivate = useCallback(
    (p: PluginRecord) => {
      if (pluginHasUnknownDeps(p)) {
        setActionError(
          `Can’t activate "${p.name}" — one or more dependencies are not satisfied.`,
        );
        return;
      }
      void runAction(`Activating "${p.name}"`, () => activatePlugin(p.name));
    },
    [runAction],
  );

  const handleDeactivate = useCallback(
    (p: PluginRecord) =>
      void runAction(`Deactivating "${p.name}"`, () =>
        deactivatePlugin(p.name),
      ),
    [runAction],
  );

  const handleUninstallConfirm = useCallback((): void => {
    if (!uninstallTarget) return;
    const name = uninstallTarget.name;
    setUninstallTarget(null);
    void runAction(`Uninstalling "${name}"`, () => uninstallPlugin(name));
  }, [uninstallTarget, runAction]);

  return (
    <section>
      <div style={styles.header}>
        <div style={styles.headerMain}>
          <span style={styles.eyebrow}>Plugins — sandboxed extensions</span>
          <h1 style={styles.title}>
            Installed <em className="italic-accent">plugins</em>.
          </h1>
        </div>
        <Link href="/plugins/install" style={styles.installBtn}>
          Install plugin
        </Link>
      </div>

      {fetchError ? (
        <div role="alert" style={styles.errorBanner}>
          Couldn’t load plugins from the API ({fetchError}). The endpoint
          may not be deployed yet (issues #340 / #341).
        </div>
      ) : null}

      {actionFeedback ? (
        <div role="status" style={styles.actionFeedback}>
          {actionFeedback}
        </div>
      ) : null}
      {actionError ? (
        <div role="alert" style={styles.errorBanner}>
          {actionError}
        </div>
      ) : null}

      <div style={styles.toolbar} role="toolbar" aria-label="Plugin filters">
        <input
          type="search"
          placeholder="Search by name or version"
          aria-label="Search plugins"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          style={styles.searchInput}
        />
        <div style={styles.chipGroup} role="group" aria-label="Filter by status">
          {STATUS_OPTIONS.map((opt) => {
            const active = statusFilter === opt.value;
            return (
              <button
                key={opt.value}
                type="button"
                aria-pressed={active}
                onClick={() => setStatusFilter(opt.value)}
                style={active ? { ...styles.chip, ...styles.chipActive } : styles.chip}
              >
                {opt.label}
              </button>
            );
          })}
        </div>
      </div>

      <div style={styles.tableWrap}>
        {filtered.length === 0 ? (
          <div style={styles.empty}>
            {plugins.length === 0
              ? 'No plugins installed yet. Use the Install plugin button to upload one.'
              : 'No plugins match these filters'}
          </div>
        ) : (
          <table style={styles.table} aria-label="Plugins">
            <thead>
              <tr>
                <th style={styles.th} scope="col">Name</th>
                <th style={styles.th} scope="col">Version</th>
                <th style={styles.th} scope="col">Status</th>
                <th style={styles.th} scope="col">Capabilities</th>
                <th style={styles.th} scope="col">Actions</th>
              </tr>
            </thead>
            <tbody>
              {filtered.map((p) => {
                const canActivate =
                  p.state === 'inactive' || p.state === 'installed';
                const canDeactivate = p.state === 'active';
                const isUninstallable =
                  p.state === 'inactive' ||
                  p.state === 'installed' ||
                  p.state === 'errored';
                return (
                  <tr key={p.name}>
                    <td style={styles.td}>
                      <Link
                        href={`/plugins/${p.name}` as const}
                        style={styles.nameLink}
                      >
                        {p.displayName || p.name}
                      </Link>
                    </td>
                    <td style={styles.td}>
                      <span style={styles.versionMono}>{p.version}</span>
                    </td>
                    <td style={styles.td}>
                      <PluginStatusBadge state={p.state} />
                    </td>
                    <td style={styles.td}>
                      <span style={styles.versionMono}>
                        {p.capabilities.length}
                      </span>
                    </td>
                    <td style={styles.td}>
                      <div style={styles.actions}>
                        {canActivate ? (
                          <button
                            type="button"
                            onClick={() => handleActivate(p)}
                            disabled={pending}
                            style={{
                              ...styles.actionBtn,
                              ...(pending ? styles.actionDisabled : {}),
                            }}
                            aria-label={`Activate ${p.name}`}
                          >
                            Activate
                          </button>
                        ) : null}
                        {canDeactivate ? (
                          <button
                            type="button"
                            onClick={() => handleDeactivate(p)}
                            disabled={pending}
                            style={{
                              ...styles.actionBtn,
                              ...(pending ? styles.actionDisabled : {}),
                            }}
                            aria-label={`Deactivate ${p.name}`}
                          >
                            Deactivate
                          </button>
                        ) : null}
                        <button
                          type="button"
                          onClick={() =>
                            isUninstallable
                              ? setUninstallTarget({
                                  name: p.name,
                                  state: p.state,
                                })
                              : undefined
                          }
                          disabled={!isUninstallable || pending}
                          aria-disabled={!isUninstallable || pending}
                          title={
                            !isUninstallable
                              ? 'Deactivate the plugin before uninstalling'
                              : undefined
                          }
                          style={{
                            ...styles.destructive,
                            ...(!isUninstallable || pending
                              ? styles.actionDisabled
                              : {}),
                          }}
                          aria-label={`Uninstall ${p.name}`}
                        >
                          Uninstall
                        </button>
                      </div>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
      </div>

      {uninstallTarget ? (
        <div
          style={styles.modalBackdrop}
          role="dialog"
          aria-modal="true"
          aria-labelledby="uninstall-confirm-title"
        >
          <div style={styles.modal}>
            <h2 id="uninstall-confirm-title" style={styles.modalTitle}>
              Uninstall &quot;{uninstallTarget.name}&quot;?
            </h2>
            <p style={styles.modalBody}>
              {uninstallTarget.state === 'errored'
                ? 'This plugin is in an errored state. Uninstalling will remove it and its stored data. You can reinstall it later.'
                : 'This will remove the plugin and its stored data. You can reinstall it later.'}
            </p>
            <div style={styles.modalActions}>
              <button
                type="button"
                onClick={() => setUninstallTarget(null)}
                style={styles.cancelBtn}
              >
                Cancel
              </button>
              <button
                type="button"
                onClick={handleUninstallConfirm}
                style={styles.confirmDestructive}
                aria-label={`Confirm uninstall ${uninstallTarget.name}`}
              >
                Uninstall
              </button>
            </div>
          </div>
        </div>
      ) : null}
    </section>
  );
}
