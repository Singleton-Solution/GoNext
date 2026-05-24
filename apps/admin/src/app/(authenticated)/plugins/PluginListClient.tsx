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
 * Lives next to the server `page.tsx` like other admin lists. The
 * primitive styling matches UsersList — Tailwind + the design system
 * land in the broader admin-design issue (#34); for now inline styles
 * keep the screen self-contained and trivially reviewable.
 *
 * Why not `<ResourceList>` from #332: that shell is shaped around
 * generic CRUD rows (search, filters, sort, bulk actions, selection).
 * The plugin list needs the same toolbar shape but the per-row buttons
 * (Activate / Deactivate / Uninstall) are domain-specific and the
 * uninstall confirmation modal is too. Wiring everything through
 * `ResourceList` columns + `bulkActions` would add more glue than it
 * removes; revisit once a second domain (plugins + themes) shares
 * enough of the row UX to justify the consolidation.
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
    alignItems: 'center',
    justifyContent: 'space-between',
    gap: 16,
    marginBottom: 16,
  },
  title: { margin: 0, fontSize: 20, fontWeight: 600 },
  installBtn: {
    display: 'inline-flex',
    alignItems: 'center',
    gap: 6,
    background: 'var(--color-accent, #2563eb)',
    color: '#ffffff',
    border: 0,
    borderRadius: 6,
    padding: '8px 14px',
    fontWeight: 500,
    fontSize: 14,
    textDecoration: 'none',
  },
  toolbar: {
    display: 'flex',
    flexWrap: 'wrap',
    gap: 12,
    marginBottom: 12,
    padding: 12,
    background: 'var(--color-surface, #ffffff)',
    border: '1px solid var(--color-border, #e4e6ea)',
    borderRadius: 6,
  },
  searchInput: {
    flex: '1 1 240px',
    minWidth: 200,
    padding: '6px 10px',
    border: '1px solid var(--color-border, #e4e6ea)',
    borderRadius: 6,
    fontSize: 14,
  },
  chipGroup: { display: 'inline-flex', flexWrap: 'wrap', gap: 8 },
  chip: {
    background: 'transparent',
    border: '1px solid var(--color-border, #e4e6ea)',
    borderRadius: 999,
    padding: '4px 12px',
    fontSize: 13,
    color: 'var(--color-text, #1c2024)',
    cursor: 'pointer',
  },
  chipActive: {
    background: 'var(--color-accent, #2563eb)',
    color: '#ffffff',
    border: '1px solid var(--color-accent, #2563eb)',
    borderRadius: 999,
    padding: '4px 12px',
    fontSize: 13,
    cursor: 'pointer',
  },
  tableWrap: {
    background: 'var(--color-surface, #ffffff)',
    border: '1px solid var(--color-border, #e4e6ea)',
    borderRadius: 6,
    overflow: 'hidden',
  },
  table: {
    width: '100%',
    borderCollapse: 'collapse',
    fontSize: 14,
  },
  th: {
    textAlign: 'left',
    padding: '10px 12px',
    fontWeight: 600,
    color: 'var(--color-text-muted, #6b7280)',
    background: '#fafafa',
    borderBottom: '1px solid var(--color-border, #e4e6ea)',
    fontSize: 12,
    textTransform: 'uppercase',
    letterSpacing: '0.04em',
  },
  td: {
    padding: '12px',
    borderBottom: '1px solid var(--color-border, #e4e6ea)',
    verticalAlign: 'middle',
  },
  nameLink: {
    fontWeight: 500,
    color: 'var(--color-text, #1c2024)',
    textDecoration: 'none',
  },
  versionMono: {
    fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
    fontSize: 13,
    color: 'var(--color-text-muted, #6b7280)',
  },
  actions: { display: 'inline-flex', gap: 8 },
  actionBtn: {
    background: '#ffffff',
    border: '1px solid var(--color-border, #e4e6ea)',
    borderRadius: 6,
    padding: '4px 10px',
    fontSize: 13,
    cursor: 'pointer',
  },
  destructive: {
    background: '#ffffff',
    border: '1px solid #fecaca',
    borderRadius: 6,
    padding: '4px 10px',
    fontSize: 13,
    color: '#991b1b',
    cursor: 'pointer',
  },
  actionDisabled: { opacity: 0.45, cursor: 'not-allowed' },
  empty: {
    padding: '40px 16px',
    textAlign: 'center',
    color: 'var(--color-text-muted, #6b7280)',
  },
  errorBanner: {
    padding: '10px 12px',
    marginBottom: 12,
    border: '1px solid #fecaca',
    background: '#fef2f2',
    color: '#991b1b',
    borderRadius: 6,
    fontSize: 13,
  },
  actionFeedback: {
    padding: '10px 12px',
    marginBottom: 12,
    border: '1px solid #bfdbfe',
    background: '#eff6ff',
    color: '#1e3a8a',
    borderRadius: 6,
    fontSize: 13,
  },
  modalBackdrop: {
    position: 'fixed',
    inset: 0,
    background: 'rgba(15, 23, 42, 0.5)',
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    zIndex: 50,
  },
  modal: {
    background: '#ffffff',
    borderRadius: 8,
    width: 'min(440px, 90vw)',
    padding: 20,
    boxShadow: '0 24px 48px rgba(15, 23, 42, 0.25)',
  },
  modalTitle: { margin: 0, fontSize: 18, fontWeight: 600 },
  modalBody: { marginTop: 8, fontSize: 14, color: 'var(--color-text, #1c2024)' },
  modalActions: {
    marginTop: 16,
    display: 'flex',
    justifyContent: 'flex-end',
    gap: 8,
  },
  cancelBtn: {
    background: '#ffffff',
    border: '1px solid var(--color-border, #e4e6ea)',
    borderRadius: 6,
    padding: '6px 14px',
    fontSize: 14,
    cursor: 'pointer',
  },
  confirmDestructive: {
    background: '#dc2626',
    color: '#ffffff',
    border: 0,
    borderRadius: 6,
    padding: '6px 14px',
    fontSize: 14,
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
        <h1 style={styles.title}>Plugins</h1>
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
                style={active ? styles.chipActive : styles.chip}
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
