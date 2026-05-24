'use client';

/**
 * Webhooks list client island.
 *
 * Renders a table of subscriptions with per-row actions (Test, Edit,
 * Disable/Enable, Delete). Test runs synchronously via the API and
 * surfaces the result in an inline notice; Disable/Enable refreshes
 * the row; Delete confirms first because the action is irreversible.
 */
import Link from 'next/link';
import {
  useCallback,
  useEffect,
  useMemo,
  useState,
  type ReactElement,
} from 'react';
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
      <div role="alert" style={{ padding: 16 }}>
        <strong>Couldn&apos;t load subscriptions.</strong>{' '}
        <button type="button" onClick={() => void refetch()}>
          Retry
        </button>
        <p className="muted">{error.message}</p>
      </div>
    );
  }

  if (rows.length === 0) {
    return (
      <div style={{ padding: 16 }}>
        <strong>No webhook subscriptions yet.</strong>
        <p className="muted">
          <Link href="/webhooks/new">Create one</Link> to start receiving
          signed event notifications at an HTTPS endpoint of your choice.
        </p>
      </div>
    );
  }

  return (
    <>
      <table
        aria-label="Webhook subscriptions"
        style={{ width: '100%', borderCollapse: 'collapse' }}
      >
        <thead>
          <tr style={{ textAlign: 'left' }}>
            <th>Name</th>
            <th>URL</th>
            <th>Events</th>
            <th>Status</th>
            <th>Last delivery</th>
            <th aria-label="Actions" />
          </tr>
        </thead>
        <tbody>
          {rows.map((sub) => (
            <tr
              key={sub.id}
              data-testid={`subscription-row-${sub.id}`}
              style={{ borderTop: '1px solid var(--color-border, #eee)' }}
            >
              <td>
                <Link href={`/webhooks/${encodeURIComponent(sub.id)}`}>
                  {sub.name}
                </Link>
              </td>
              <td>
                <code style={{ fontSize: 12 }}>{sub.url}</code>
              </td>
              <td>
                {sub.events.length === 0 ? (
                  <span className="muted">(none)</span>
                ) : (
                  <span>{sub.events.join(', ')}</span>
                )}
              </td>
              <td>
                <StatusBadge subscription={sub} />
              </td>
              <td title={sub.last_delivery_at ?? ''}>
                {formatRelative(sub.last_delivery_at)}
                {sub.consecutive_failures > 0 ? (
                  <span
                    className="muted"
                    style={{ marginLeft: 6, fontSize: 12 }}
                  >
                    ({sub.consecutive_failures} consecutive failures)
                  </span>
                ) : null}
              </td>
              <td style={{ whiteSpace: 'nowrap' }}>
                <button
                  type="button"
                  onClick={() => void handleTest(sub)}
                  disabled={busyRowId === sub.id}
                  aria-label={`Test ${sub.name}`}
                >
                  Test
                </button>{' '}
                <Link href={`/webhooks/${encodeURIComponent(sub.id)}`}>
                  Edit
                </Link>{' '}
                <button
                  type="button"
                  onClick={() => void handleToggleActive(sub)}
                  disabled={busyRowId === sub.id}
                  aria-label={`${sub.active ? 'Disable' : 'Enable'} ${sub.name}`}
                >
                  {sub.active ? 'Disable' : 'Enable'}
                </button>{' '}
                <button
                  type="button"
                  onClick={() => void handleDelete(sub)}
                  disabled={busyRowId === sub.id}
                  aria-label={`Delete ${sub.name}`}
                  style={{ color: 'var(--color-danger, #a00)' }}
                >
                  Delete
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>

      {noticeRow ? (
        <div
          role="status"
          aria-live="polite"
          data-testid="test-result"
          style={{
            marginTop: 12,
            padding: 12,
            borderRadius: 4,
            background: noticeRow.error
              ? 'var(--color-danger-bg, #fbd5d5)'
              : noticeRow.result?.delivered
                ? 'var(--color-success-bg, #d6f5d6)'
                : 'var(--color-warn-bg, #fff4d6)',
          }}
        >
          {noticeRow.error ? (
            <span>Test failed: {noticeRow.error}</span>
          ) : noticeRow.result ? (
            <span>
              {noticeRow.result.delivered ? 'Delivered' : 'Subscriber responded'}
              {' — '}
              HTTP {noticeRow.result.response_code} in{' '}
              {noticeRow.result.duration_ms} ms
              {noticeRow.result.error ? ` (${noticeRow.result.error})` : ''}
            </span>
          ) : null}
        </div>
      ) : null}

      {loading ? <p className="muted">Loading…</p> : null}

      {cursor ? (
        <p style={{ marginTop: 12 }}>
          <button type="button" onClick={() => void refetch(cursor)}>
            Load more
          </button>
        </p>
      ) : null}
    </>
  );
}
