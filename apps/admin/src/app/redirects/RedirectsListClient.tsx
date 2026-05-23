'use client';

/**
 * Redirects list client island.
 *
 * Renders a table of redirect rules with per-row delete and an inline
 * search input bound to the source_path filter. Tabs at the top
 * switch between "All rules" (paginated list) and "Recent hits" (top
 * traffic ordered by hit_count DESC).
 */
import { useCallback, useEffect, useMemo, useState, type ReactElement } from 'react';
import Link from 'next/link';
import {
  deleteRedirect,
  listRedirects,
  listTopRedirects,
} from './actions';
import type { Redirect, RedirectListResponse } from './types';

const PAGE_LIMIT = 30;

type Tab = 'all' | 'top';

interface Props {
  initialData: RedirectListResponse;
}

function statusBadge(status: number): ReactElement {
  const palette: Record<number, string> = {
    301: '#0ea5e9',
    302: '#a855f7',
    307: '#f59e0b',
    308: '#22c55e',
  };
  const color = palette[status] ?? '#9ca3af';
  return (
    <span
      aria-label={`HTTP status ${status}`}
      style={{
        display: 'inline-block',
        padding: '2px 8px',
        borderRadius: 'var(--radius)',
        background: color,
        color: 'white',
        fontSize: 12,
        fontWeight: 600,
        minWidth: 36,
        textAlign: 'center',
      }}
    >
      {status}
    </span>
  );
}

function formatRelative(iso?: string): string {
  if (!iso) return '—';
  const t = new Date(iso).getTime();
  if (!Number.isFinite(t)) return iso;
  const diffSec = Math.floor((Date.now() - t) / 1000);
  if (diffSec < 60) return `${diffSec}s ago`;
  if (diffSec < 3600) return `${Math.floor(diffSec / 60)}m ago`;
  if (diffSec < 86_400) return `${Math.floor(diffSec / 3600)}h ago`;
  return `${Math.floor(diffSec / 86_400)}d ago`;
}

export function RedirectsListClient({ initialData }: Props): ReactElement {
  const [tab, setTab] = useState<Tab>('all');
  const [rows, setRows] = useState<Redirect[]>(initialData.data);
  const [search, setSearch] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const reload = useCallback(async () => {
    setBusy(true);
    setError(null);
    try {
      const data =
        tab === 'all'
          ? await listRedirects({ limit: PAGE_LIMIT, search: search || undefined })
          : await listTopRedirects(PAGE_LIMIT);
      setRows(data.data);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'failed to load redirects');
    } finally {
      setBusy(false);
    }
  }, [tab, search]);

  useEffect(() => {
    // Skip the first effect — we already have initialData.
    if (tab === 'all' && !search) return;
    void reload();
  }, [tab, search, reload]);

  const onDelete = useCallback(
    async (row: Redirect) => {
      if (!confirm(`Delete redirect ${row.source_path} → ${row.destination_path}?`)) {
        return;
      }
      try {
        await deleteRedirect(row.id);
        setRows((prev) => prev.filter((r) => r.id !== row.id));
      } catch (err) {
        setError(err instanceof Error ? err.message : 'delete failed');
      }
    },
    [],
  );

  const visibleRows = useMemo(() => rows, [rows]);

  return (
    <div>
      <div role="tablist" aria-label="Redirect view" style={{ display: 'flex', gap: 8, marginBottom: 12 }}>
        <button
          role="tab"
          aria-selected={tab === 'all'}
          type="button"
          onClick={() => setTab('all')}
          className={tab === 'all' ? 'tab tab--active' : 'tab'}
        >
          All rules
        </button>
        <button
          role="tab"
          aria-selected={tab === 'top'}
          type="button"
          onClick={() => setTab('top')}
          className={tab === 'top' ? 'tab tab--active' : 'tab'}
        >
          Recent hits
        </button>
      </div>

      {tab === 'all' && (
        <div style={{ marginBottom: 12 }}>
          <input
            type="search"
            placeholder="Filter by source path…"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            aria-label="Filter by source path"
            style={{ width: '100%', padding: 8 }}
          />
        </div>
      )}

      {error && (
        <div role="alert" style={{ padding: 8, marginBottom: 12, background: '#fee2e2', color: '#7f1d1d' }}>
          {error}
        </div>
      )}

      <table style={{ width: '100%', borderCollapse: 'collapse' }}>
        <thead>
          <tr>
            <th style={{ textAlign: 'left' }}>Status</th>
            <th style={{ textAlign: 'left' }}>Source</th>
            <th style={{ textAlign: 'left' }}>Destination</th>
            <th style={{ textAlign: 'left' }}>Type</th>
            <th style={{ textAlign: 'right' }}>Hits</th>
            <th style={{ textAlign: 'left' }}>Last hit</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {visibleRows.length === 0 && !busy ? (
            <tr>
              <td colSpan={7} style={{ padding: 24, textAlign: 'center', color: 'var(--color-muted)' }}>
                {tab === 'top' ? 'No redirects have been hit yet.' : 'No redirects yet.'}
              </td>
            </tr>
          ) : (
            visibleRows.map((row) => (
              <tr key={row.id}>
                <td>{statusBadge(row.status)}</td>
                <td>
                  <code>{row.source_path}</code>
                </td>
                <td>
                  <code>{row.destination_path}</code>
                </td>
                <td>{row.is_regex ? 'Regex' : 'Literal'}</td>
                <td style={{ textAlign: 'right' }}>{row.hit_count.toLocaleString()}</td>
                <td>{formatRelative(row.last_hit_at)}</td>
                <td>
                  <Link href={`/redirects/${row.id}`} className="secondary-action" style={{ marginRight: 8 }}>
                    Edit
                  </Link>
                  <button type="button" onClick={() => onDelete(row)} className="danger-action">
                    Delete
                  </button>
                </td>
              </tr>
            ))
          )}
        </tbody>
      </table>
    </div>
  );
}
