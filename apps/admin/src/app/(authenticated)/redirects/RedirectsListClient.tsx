'use client';

/**
 * Redirects list client island — Living-Systems brand surface.
 *
 * Wears the cream-paper card with three tabs ("All rules" /
 * "Recent hits" / "Bulk import"), a row layout that pairs the
 * source path → destination in mono, an emerald / lavender / plum
 * status chip, and a hit-count column.
 *
 * Brand mapping (see docs/design/ui_kits/admin/index.html and
 * the colour ramp in `src/styles/tokens.css`):
 *
 *   - 301 Moved Permanently      → emerald (the canonical
 *                                  "this is the new home" success state)
 *   - 302 Found                  → fg-muted (still here, just elsewhere)
 *   - 307 Temporary Redirect     → lavender (secondary accent)
 *   - 308 Permanent Redirect     → plum (lavender-deep) — the
 *                                  "force the method through" cousin
 */
import { useCallback, useEffect, useMemo, useState, type ReactElement } from 'react';
import Link from 'next/link';
import { Search, UploadCloud } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import {
  deleteRedirect,
  listRedirects,
  listTopRedirects,
} from './actions';
import type { Redirect, RedirectListResponse } from './types';

const PAGE_LIMIT = 30;

type Tab = 'all' | 'top' | 'import';

interface Props {
  initialData: RedirectListResponse;
}

/**
 * Status-code badge. We keep the integer prominent and lean on a
 * variant + dot to telegraph the meaning at a glance.
 */
function statusBadge(status: number): ReactElement {
  switch (status) {
    case 301:
      return (
        <Badge variant="emerald" dot aria-label="HTTP 301 Moved Permanently">
          <span className="font-mono font-medium">301</span>
        </Badge>
      );
    case 302:
      return (
        <Badge variant="default" dot aria-label="HTTP 302 Found">
          <span className="font-mono font-medium">302</span>
        </Badge>
      );
    case 307:
      return (
        <Badge variant="lavender" dot aria-label="HTTP 307 Temporary Redirect">
          <span className="font-mono font-medium">307</span>
        </Badge>
      );
    case 308:
      return (
        <Badge
          dot
          aria-label="HTTP 308 Permanent Redirect"
          className="border-transparent bg-lavender-soft text-lavender-deep"
        >
          <span className="font-mono font-medium">308</span>
        </Badge>
      );
    default:
      return (
        <Badge variant="outline" aria-label={`HTTP status ${status}`}>
          <span className="font-mono font-medium">{status}</span>
        </Badge>
      );
  }
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
        tab === 'top'
          ? await listTopRedirects(PAGE_LIMIT)
          : await listRedirects({ limit: PAGE_LIMIT, search: search || undefined });
      setRows(data.data);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'failed to load redirects');
    } finally {
      setBusy(false);
    }
  }, [tab, search]);

  useEffect(() => {
    // Skip the first effect when we still hold initialData and the
    // user hasn't touched the search box.
    if (tab === 'all' && !search) return;
    if (tab === 'import') return;
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
    <Tabs value={tab} onValueChange={(value) => setTab(value as Tab)}>
      <TabsList aria-label="Redirect view" data-testid="redirects-tablist">
        <TabsTrigger value="all" data-testid="tab-all">All rules</TabsTrigger>
        <TabsTrigger value="top" data-testid="tab-top">Recent hits</TabsTrigger>
        <TabsTrigger value="import" data-testid="tab-import">Bulk import</TabsTrigger>
      </TabsList>

      <TabsContent value="all">
        <RulesPanel
          rows={visibleRows}
          search={search}
          onSearchChange={setSearch}
          onDelete={onDelete}
          busy={busy}
          error={error}
          mode="all"
        />
      </TabsContent>

      <TabsContent value="top">
        <RulesPanel
          rows={visibleRows}
          onDelete={onDelete}
          busy={busy}
          error={error}
          mode="top"
        />
      </TabsContent>

      <TabsContent value="import">
        <BulkImportPanel />
      </TabsContent>
    </Tabs>
  );
}

interface RulesPanelProps {
  rows: Redirect[];
  search?: string;
  onSearchChange?: (next: string) => void;
  onDelete: (row: Redirect) => void;
  busy: boolean;
  error: string | null;
  mode: 'all' | 'top';
}

function RulesPanel({
  rows,
  search,
  onSearchChange,
  onDelete,
  busy,
  error,
  mode,
}: RulesPanelProps): ReactElement {
  return (
    <div className="card flex flex-col gap-3" data-testid="rules-panel">
      {mode === 'all' && onSearchChange && (
        <div className="relative">
          <Search
            aria-hidden="true"
            className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-fg-faint"
            size={16}
          />
          <Input
            type="search"
            placeholder="Filter by source path…"
            value={search ?? ''}
            onChange={(e) => onSearchChange(e.target.value)}
            aria-label="Filter by source path"
            data-testid="rules-search"
            className="pl-9"
          />
        </div>
      )}

      {error && (
        <div role="alert" className="rounded-md border border-danger/40 bg-danger-soft px-3 py-2 text-sm text-danger">
          {error}
        </div>
      )}

      <div className="overflow-x-auto rounded-md border border-border-subtle bg-paper">
        <table className="w-full border-collapse text-sm">
          <thead>
            <tr className="bg-paper-3 text-left">
              <th className="px-3 py-2 font-display text-xs font-medium uppercase tracking-wide text-fg-subtle">
                Status
              </th>
              <th className="px-3 py-2 font-display text-xs font-medium uppercase tracking-wide text-fg-subtle">
                Source &rarr; Destination
              </th>
              <th className="px-3 py-2 font-display text-xs font-medium uppercase tracking-wide text-fg-subtle">
                Type
              </th>
              <th className="px-3 py-2 text-right font-display text-xs font-medium uppercase tracking-wide text-fg-subtle">
                Hits
              </th>
              <th className="px-3 py-2 font-display text-xs font-medium uppercase tracking-wide text-fg-subtle">
                Last hit
              </th>
              <th aria-label="Row actions" />
            </tr>
          </thead>
          <tbody>
            {rows.length === 0 && !busy ? (
              <tr>
                <td colSpan={6} className="px-3 py-10 text-center text-fg-muted">
                  {mode === 'top'
                    ? 'No redirects have been hit yet.'
                    : 'No redirects yet. Create your first rule to keep old URLs alive.'}
                </td>
              </tr>
            ) : (
              rows.map((row) => (
                <tr
                  key={row.id}
                  data-testid={`redirect-row-${row.id}`}
                  className="border-t border-border-subtle align-middle hover:bg-paper-2"
                >
                  <td className="px-3 py-3">{statusBadge(row.status)}</td>
                  <td className="px-3 py-3">
                    <div className="flex flex-col gap-1">
                      <code className="font-mono text-xs text-ink">{row.source_path}</code>
                      <code className="font-mono text-xs text-emerald-deep">
                        &rarr; {row.destination_path}
                      </code>
                    </div>
                  </td>
                  <td className="px-3 py-3">
                    {row.is_regex ? (
                      <Badge variant="lavender">regex</Badge>
                    ) : (
                      <Badge variant="default">literal</Badge>
                    )}
                  </td>
                  <td className="px-3 py-3 text-right font-mono text-xs text-ink">
                    {row.hit_count.toLocaleString()}
                  </td>
                  <td className="px-3 py-3 text-xs text-fg-muted">
                    {formatRelative(row.last_hit_at)}
                  </td>
                  <td className="whitespace-nowrap px-3 py-3 text-right">
                    <Button asChild variant="ghost" size="sm">
                      <Link href={`/redirects/${row.id}`}>Edit</Link>
                    </Button>
                    <Button
                      variant="ghost"
                      size="sm"
                      type="button"
                      onClick={() => onDelete(row)}
                      className="text-danger hover:bg-danger-soft hover:text-danger"
                      data-testid={`delete-${row.id}`}
                    >
                      Delete
                    </Button>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}

/**
 * Placeholder for the bulk-import tab. The CSV-upload backend lands
 * in a follow-up issue; until then we show a thin paper-3 well so
 * the affordance is visible but obviously inert.
 */
function BulkImportPanel(): ReactElement {
  return (
    <div className="card flex flex-col items-center gap-3 text-center" data-testid="bulk-import-panel">
      <div className="flex h-12 w-12 items-center justify-center rounded-full bg-paper-3 text-emerald-deep">
        <UploadCloud aria-hidden="true" />
      </div>
      <h3 className="font-display text-xl font-semibold text-ink">
        Bulk import from CSV
      </h3>
      <p className="lead max-w-[52ch]">
        Drop a <code className="font-mono text-xs">source,destination,status,is_regex</code> file
        here to seed dozens of rules at once. The importer runs each line
        through the same validator the form uses, so malformed rows are
        rejected without ever touching the live engine.
      </p>
      <Button variant="default" disabled>
        Choose file…
      </Button>
      <p className="text-xs text-fg-subtle">
        Coming soon — track <code className="font-mono">issue/redirects-bulk</code>.
      </p>
    </div>
  );
}
