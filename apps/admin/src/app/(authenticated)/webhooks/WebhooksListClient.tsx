'use client';

/**
 * Webhooks list client island.
 *
 * Renders a table of subscriptions with per-row actions (Test, Edit,
 * Disable/Enable, Delete). Test runs synchronously via the API and
 * surfaces the result in an inline notice; Disable/Enable refreshes
 * the row; Delete confirms first because the action is irreversible.
 *
 * Brand: Living-Systems (#432). The table sits inside a paper-2 panel
 * Card with a paper-2 head row, paper rows that highlight on hover via
 * paper-2/40, and per-row tool buttons that match the index.html
 * row-tools pattern (icon-only buttons that reveal on hover). The
 * mono row identity (Geist Mono) makes URLs scannable.
 */
import Link from 'next/link';
import {
  Pencil,
  Power,
  Trash2,
  Zap,
} from 'lucide-react';
import {
  useCallback,
  useEffect,
  useMemo,
  useState,
  type ReactElement,
} from 'react';
import { Card } from '@/components/ui/card';
import { Button } from '@/components/ui/button';
import {
  deleteSubscription,
  disableSubscription,
  enableSubscription,
  listSubscriptions,
  testSubscription,
} from './actions';
import { StatusBadge } from './components/StatusBadge';
import type {
  Subscription,
  SubscriptionListResponse,
  TestResult,
} from './types';

const PAGE_LIMIT = 30;

export interface WebhooksListClientProps {
  initialData: SubscriptionListResponse;
}

interface TestNotice {
  id: string;
  result?: TestResult;
  error?: string;
}

function formatRelative(iso?: string): string {
  if (!iso) return '—';
  const t = new Date(iso).getTime();
  if (!Number.isFinite(t)) return iso;
  const diffMs = Date.now() - t;
  const sec = Math.floor(diffMs / 1000);
  if (sec < 60) return `${sec}s ago`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m ago`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr}h ago`;
  const day = Math.floor(hr / 24);
  return `${day}d ago`;
}

export function WebhooksListClient({
  initialData,
}: WebhooksListClientProps): ReactElement {
  const [rows, setRows] = useState<Subscription[]>(initialData.data);
  const [cursor, setCursor] = useState<string>(
    initialData.pagination.next_cursor ?? '',
  );
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const [busyRowId, setBusyRowId] = useState<string | null>(null);
  const [notice, setNotice] = useState<TestNotice | null>(null);

  const refetch = useCallback(async (c?: string) => {
    setLoading(true);
    setError(null);
    try {
      const res = await listSubscriptions({ limit: PAGE_LIMIT, cursor: c });
      setRows(res.data);
      setCursor(res.pagination.next_cursor ?? '');
    } catch (err) {
      setError(err instanceof Error ? err : new Error(String(err)));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    setRows(initialData.data);
    setCursor(initialData.pagination.next_cursor ?? '');
  }, [initialData]);

  const handleTest = useCallback(async (sub: Subscription) => {
    setBusyRowId(sub.id);
    setNotice(null);
    try {
      const result = await testSubscription(sub.id);
      setNotice({ id: sub.id, result });
    } catch (err) {
      setNotice({
        id: sub.id,
        error: err instanceof Error ? err.message : String(err),
      });
    } finally {
      setBusyRowId(null);
    }
  }, []);

  const handleToggleActive = useCallback(async (sub: Subscription) => {
    setBusyRowId(sub.id);
    try {
      const updated = sub.active
        ? await disableSubscription(sub.id)
        : await enableSubscription(sub.id);
      setRows((prev) => prev.map((r) => (r.id === sub.id ? updated : r)));
    } catch (err) {
      setError(err instanceof Error ? err : new Error(String(err)));
    } finally {
      setBusyRowId(null);
    }
  }, []);

  const handleDelete = useCallback(async (sub: Subscription) => {
    const ok = typeof window !== 'undefined'
      ? window.confirm(
          `Delete subscription "${sub.name}"? This cannot be undone. The audit log for this subscription will also be removed.`,
        )
      : true;
    if (!ok) return;
    setBusyRowId(sub.id);
    try {
      await deleteSubscription(sub.id);
      setRows((prev) => prev.filter((r) => r.id !== sub.id));
    } catch (err) {
      setError(err instanceof Error ? err : new Error(String(err)));
    } finally {
      setBusyRowId(null);
    }
  }, []);

  const noticeRow = useMemo(() => {
    if (!notice) return null;
    const row = rows.find((r) => r.id === notice.id);
    return row ? notice : null;
  }, [notice, rows]);

  if (error) {
    return (
      <Card
        role="alert"
        data-testid="webhooks-error"
        className="border-danger/30 bg-danger-soft/20 p-6"
      >
        <strong className="font-display text-sm font-bold text-ink">
          Couldn&apos;t load subscriptions.
        </strong>
        <p className="mt-2 font-sans text-xs text-fg-muted">
          {error.message}
        </p>
        <Button
          type="button"
          variant="default"
          size="sm"
          onClick={() => void refetch()}
          className="mt-3"
        >
          Retry
        </Button>
      </Card>
    );
  }

  if (rows.length === 0) {
    return (
      <Card
        data-testid="webhooks-empty"
        className="border-dashed bg-paper p-6"
      >
        <strong className="font-display text-sm font-bold text-ink">
          No webhook subscriptions yet.
        </strong>
        <p className="mt-2 font-sans text-xs text-fg-muted">
          <Link
            href="/webhooks/new"
            className="text-emerald-deep hover:underline"
          >
            Create one
          </Link>{' '}
          to start receiving signed event notifications at an HTTPS
          endpoint of your choice.
        </p>
      </Card>
    );
  }

  return (
    <div className="flex flex-col gap-3">
      <Card className="overflow-hidden">
        {/* Panel head — mirrors index.html .panel__head */}
        <div className="flex items-center justify-between border-b border-border bg-paper-2 px-4 py-3">
          <div className="flex items-center gap-3">
            <h3 className="font-display text-sm font-bold text-ink">
              All subscriptions
            </h3>
            <span className="rounded-pill bg-paper-3 px-2 py-[2px] font-mono text-2xs tabular-nums text-fg-subtle">
              {rows.length}
            </span>
          </div>
        </div>

        <table
          aria-label="Webhook subscriptions"
          className="w-full border-collapse bg-paper"
        >
          <thead>
            <tr className="bg-paper-2 text-left">
              <th className="border-b border-border-subtle px-4 py-3 font-sans text-xs font-medium text-fg-subtle">
                Name
              </th>
              <th className="border-b border-border-subtle px-4 py-3 font-sans text-xs font-medium text-fg-subtle">
                URL
              </th>
              <th className="border-b border-border-subtle px-4 py-3 font-sans text-xs font-medium text-fg-subtle">
                Events
              </th>
              <th className="border-b border-border-subtle px-4 py-3 font-sans text-xs font-medium text-fg-subtle">
                Status
              </th>
              <th className="border-b border-border-subtle px-4 py-3 font-sans text-xs font-medium text-fg-subtle">
                Last delivery
              </th>
              <th
                aria-label="Actions"
                className="border-b border-border-subtle px-4 py-3"
              />
            </tr>
          </thead>
          <tbody>
            {rows.map((sub) => (
              <tr
                key={sub.id}
                data-testid={`subscription-row-${sub.id}`}
                className="group border-b border-border-subtle transition-colors hover:bg-paper-2/60 last:border-b-0"
              >
                <td className="px-4 py-3 align-middle">
                  <Link
                    href={`/webhooks/${encodeURIComponent(sub.id)}`}
                    className="font-sans text-sm font-medium text-ink transition-colors hover:text-emerald-deep"
                  >
                    {sub.name}
                  </Link>
                  <div className="mt-[2px] font-mono text-2xs text-fg-subtle">
                    {sub.id}
                  </div>
                </td>
                <td className="px-4 py-3 align-middle">
                  <code className="block max-w-[280px] truncate font-mono text-xs text-fg-muted">
                    {sub.url}
                  </code>
                </td>
                <td className="px-4 py-3 align-middle">
                  {sub.events.length === 0 ? (
                    <span className="font-sans text-xs italic text-fg-faint">
                      (none)
                    </span>
                  ) : (
                    <span className="font-mono text-2xs text-fg-muted">
                      {sub.events.join(', ')}
                    </span>
                  )}
                </td>
                <td className="px-4 py-3 align-middle">
                  <StatusBadge subscription={sub} />
                </td>
                <td
                  title={sub.last_delivery_at ?? ''}
                  className="px-4 py-3 align-middle font-sans text-xs tabular-nums text-fg-subtle"
                >
                  {formatRelative(sub.last_delivery_at)}
                  {sub.consecutive_failures > 0 ? (
                    <div className="mt-[2px] font-mono text-2xs text-danger">
                      {sub.consecutive_failures} consecutive failures
                    </div>
                  ) : null}
                </td>
                <td className="whitespace-nowrap px-4 py-3 text-right align-middle">
                  {/* Row-tools cluster — index.html pattern. Tools fade
                      into full opacity on row hover but stay reachable
                      via keyboard. */}
                  <div className="inline-flex items-center gap-1 opacity-60 transition-opacity group-hover:opacity-100 focus-within:opacity-100">
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      onClick={() => void handleTest(sub)}
                      disabled={busyRowId === sub.id}
                      aria-label={`Test ${sub.name}`}
                      title="Send a synthetic event"
                      className="h-7 px-2"
                    >
                      <Zap className="h-[13px] w-[13px]" aria-hidden="true" />
                      Test
                    </Button>
                    <Button
                      asChild
                      variant="ghost"
                      size="sm"
                      className="h-7 px-2"
                    >
                      <Link href={`/webhooks/${encodeURIComponent(sub.id)}`}>
                        <Pencil
                          className="h-[13px] w-[13px]"
                          aria-hidden="true"
                        />
                        Edit
                      </Link>
                    </Button>
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      onClick={() => void handleToggleActive(sub)}
                      disabled={busyRowId === sub.id}
                      aria-label={`${sub.active ? 'Disable' : 'Enable'} ${sub.name}`}
                      className="h-7 px-2"
                    >
                      <Power className="h-[13px] w-[13px]" aria-hidden="true" />
                      {sub.active ? 'Disable' : 'Enable'}
                    </Button>
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      onClick={() => void handleDelete(sub)}
                      disabled={busyRowId === sub.id}
                      aria-label={`Delete ${sub.name}`}
                      className="h-7 px-2 text-danger hover:bg-danger-soft hover:text-danger"
                    >
                      <Trash2
                        className="h-[13px] w-[13px]"
                        aria-hidden="true"
                      />
                      Delete
                    </Button>
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </Card>

      {noticeRow ? (
        <div
          role="status"
          aria-live="polite"
          data-testid="test-result"
          className={
            noticeRow.error
              ? 'rounded-md border border-danger/30 bg-danger-soft px-4 py-3 font-sans text-sm text-danger'
              : noticeRow.result?.delivered
                ? 'rounded-md border border-emerald/30 bg-emerald-soft px-4 py-3 font-sans text-sm text-emerald-deep'
                : 'rounded-md border border-warning/30 bg-warning-soft px-4 py-3 font-sans text-sm text-warning'
          }
        >
          {noticeRow.error ? (
            <span>Test failed: {noticeRow.error}</span>
          ) : noticeRow.result ? (
            <span>
              {noticeRow.result.delivered
                ? 'Delivered'
                : 'Subscriber responded'}
              {' — '}
              <code className="font-mono text-xs">
                HTTP {noticeRow.result.response_code}
              </code>{' '}
              in{' '}
              <code className="font-mono text-xs tabular-nums">
                {noticeRow.result.duration_ms} ms
              </code>
              {noticeRow.result.error ? ` (${noticeRow.result.error})` : ''}
            </span>
          ) : null}
        </div>
      ) : null}

      {loading ? (
        <p
          aria-busy="true"
          className="font-sans text-xs text-fg-subtle"
        >
          Loading…
        </p>
      ) : null}

      {cursor ? (
        <div className="self-end">
          <Button
            type="button"
            variant="default"
            size="sm"
            onClick={() => void refetch(cursor)}
          >
            Load more
          </Button>
        </div>
      ) : null}
    </div>
  );
}
