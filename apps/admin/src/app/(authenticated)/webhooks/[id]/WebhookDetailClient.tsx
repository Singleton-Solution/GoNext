'use client';

/**
 * <WebhookDetailClient> — edit form + recent deliveries table.
 *
 * The page is split into two panels:
 *
 *   1. Edit panel — name / url / events / active. Saved via PATCH.
 *   2. Deliveries panel — newest-first table with response_code,
 *      duration_ms, and a body preview drawer.
 *
 * A small toolbar at the top wires the same Test/Disable/Delete
 * actions the list view exposes, so an operator can act on a
 * subscription without bouncing back.
 */
import { useRouter } from 'next/navigation';
import {
  useCallback,
  useEffect,
  useMemo,
  useState,
  type ChangeEvent,
  type FormEvent,
  type ReactElement,
} from 'react';
import {
  deleteSubscription,
  disableSubscription,
  enableSubscription,
  listDeliveries,
  testSubscription,
  updateSubscription,
} from '../actions';
import { EventCatalog } from '../components/EventCatalog';
import { StatusBadge } from '../components/StatusBadge';
import type {
  Delivery,
  DeliveryListResponse,
  Subscription,
  TestResult,
} from '../types';

export interface WebhookDetailClientProps {
  subscription: Subscription;
  deliveries: DeliveryListResponse;
}

const PAGE_LIMIT = 30;

function formatTime(iso: string): string {
  if (!iso) return '—';
  const t = new Date(iso);
  return Number.isNaN(t.getTime()) ? iso : t.toISOString();
}

function statusTone(status: Delivery['status']): string {
  switch (status) {
    case 'success':
      return 'var(--color-success, #16591a)';
    case 'retry':
      return 'var(--color-warn, #7a5300)';
    case 'failed':
      return 'var(--color-danger, #7a1f1f)';
    case 'test':
      return 'var(--color-info, #1e3a8a)';
    default:
      return 'inherit';
  }
}

export function WebhookDetailClient({
  subscription,
  deliveries,
}: WebhookDetailClientProps): ReactElement {
  const router = useRouter();
  const [sub, setSub] = useState<Subscription>(subscription);
  const [form, setForm] = useState({
    name: subscription.name,
    url: subscription.url,
    events: new Set(subscription.events),
  });
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);

  const [delRows, setDelRows] = useState<Delivery[]>(deliveries.data);
  const [delCursor, setDelCursor] = useState<string>(
    deliveries.pagination.next_cursor ?? '',
  );
  const [delLoading, setDelLoading] = useState(false);
  const [expanded, setExpanded] = useState<number | null>(null);

  const [testResult, setTestResult] = useState<TestResult | null>(null);
  const [testError, setTestError] = useState<string | null>(null);

  useEffect(() => {
    setSub(subscription);
    setForm({
      name: subscription.name,
      url: subscription.url,
      events: new Set(subscription.events),
    });
  }, [subscription]);

  const dirty = useMemo(() => {
    if (form.name !== sub.name) return true;
    if (form.url !== sub.url) return true;
    if (form.events.size !== sub.events.length) return true;
    for (const ev of form.events) {
      if (!sub.events.includes(ev)) return true;
    }
    return false;
  }, [form, sub]);

  const handleField = useCallback(
    (key: 'name' | 'url') => (ev: ChangeEvent<HTMLInputElement>) => {
      setForm((prev) => ({ ...prev, [key]: ev.target.value }));
      setSaved(false);
    },
    [],
  );

  const handleEvents = useCallback((next: ReadonlySet<string>) => {
    setForm((prev) => ({ ...prev, events: new Set(next) }));
    setSaved(false);
  }, []);

  const handleSubmit = useCallback(
    async (ev: FormEvent<HTMLFormElement>) => {
      ev.preventDefault();
      setSaving(true);
      setError(null);
      setSaved(false);
      try {
        const updated = await updateSubscription(sub.id, {
          name: form.name.trim(),
          url: form.url.trim(),
          events: Array.from(form.events),
        });
        setSub(updated);
        setSaved(true);
      } catch (err) {
        setError(err instanceof Error ? err.message : String(err));
      } finally {
        setSaving(false);
      }
    },
    [form, sub.id],
  );

  const handleTest = useCallback(async () => {
    setTestResult(null);
    setTestError(null);
    try {
      const res = await testSubscription(sub.id);
      setTestResult(res);
    } catch (err) {
      setTestError(err instanceof Error ? err.message : String(err));
    }
  }, [sub.id]);

  const handleToggleActive = useCallback(async () => {
    try {
      const next = sub.active
        ? await disableSubscription(sub.id)
        : await enableSubscription(sub.id);
      setSub(next);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }, [sub.active, sub.id]);

  const handleDelete = useCallback(async () => {
    const ok = typeof window !== 'undefined'
      ? window.confirm(
          `Delete subscription "${sub.name}"? This cannot be undone.`,
        )
      : true;
    if (!ok) return;
    try {
      await deleteSubscription(sub.id);
      router.push('/webhooks');
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }, [router, sub.id, sub.name]);

  const handleLoadMore = useCallback(async () => {
    if (!delCursor) return;
    setDelLoading(true);
    try {
      const next = await listDeliveries(sub.id, {
        limit: PAGE_LIMIT,
        cursor: delCursor,
      });
      setDelRows((prev) => [...prev, ...next.data]);
      setDelCursor(next.pagination.next_cursor ?? '');
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setDelLoading(false);
    }
  }, [delCursor, sub.id]);

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 24 }}>
      <header
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: 12,
          flexWrap: 'wrap',
        }}
      >
        <StatusBadge subscription={sub} />
        <span className="muted">{sub.url}</span>
        <span style={{ marginLeft: 'auto', display: 'flex', gap: 8 }}>
          <button type="button" onClick={() => void handleTest()}>
            Test
          </button>
          <button type="button" onClick={() => void handleToggleActive()}>
            {sub.active ? 'Disable' : 'Enable'}
          </button>
          <button
            type="button"
            onClick={() => void handleDelete()}
            style={{ color: 'var(--color-danger, #a00)' }}
          >
            Delete
          </button>
        </span>
      </header>

      {testResult ? (
        <div
          role="status"
          data-testid="test-result"
          style={{
            padding: 12,
            borderRadius: 4,
            background: testResult.delivered
              ? 'var(--color-success-bg, #d6f5d6)'
              : 'var(--color-warn-bg, #fff4d6)',
          }}
        >
          {testResult.delivered ? 'Delivered' : 'Subscriber responded'} —
          HTTP {testResult.response_code} in {testResult.duration_ms} ms
        </div>
      ) : null}
      {testError ? (
        <div
          role="alert"
          style={{
            padding: 12,
            borderRadius: 4,
            background: 'var(--color-danger-bg, #fbd5d5)',
          }}
        >
          Test failed: {testError}
        </div>
      ) : null}
      {error ? (
        <div
          role="alert"
          style={{
            padding: 12,
            borderRadius: 4,
            background: 'var(--color-danger-bg, #fbd5d5)',
          }}
        >
          {error}
        </div>
      ) : null}

      <form
        onSubmit={(ev) => void handleSubmit(ev)}
        style={{
          display: 'flex',
          flexDirection: 'column',
          gap: 12,
          padding: 16,
          border: '1px solid var(--color-border, #ddd)',
          borderRadius: 4,
        }}
      >
        <h2 style={{ marginTop: 0 }}>Configuration</h2>
        <label>
          Name
          <input
            type="text"
            value={form.name}
            onChange={handleField('name')}
            required
            maxLength={200}
            style={{ display: 'block', width: '100%', marginTop: 4 }}
          />
        </label>
        <label>
          Endpoint URL
          <input
            type="url"
            value={form.url}
            onChange={handleField('url')}
            required
            style={{ display: 'block', width: '100%', marginTop: 4 }}
          />
        </label>
        <EventCatalog value={form.events} onChange={handleEvents} />
        <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
          <button type="submit" disabled={saving || !dirty}>
            {saving ? 'Saving…' : 'Save changes'}
          </button>
          {saved && !dirty ? (
            <span role="status" className="muted">
              Saved
            </span>
          ) : null}
        </div>
      </form>

      <section>
        <h2 style={{ marginTop: 0 }}>Recent deliveries</h2>
        {delRows.length === 0 ? (
          <p className="muted">
            No deliveries yet. Use Test to send a synthetic event, or
            wait for a real one to fire.
          </p>
        ) : (
          <table
            aria-label="Recent deliveries"
            style={{ width: '100%', borderCollapse: 'collapse' }}
          >
            <thead>
              <tr style={{ textAlign: 'left' }}>
                <th>Event</th>
                <th>Attempt</th>
                <th>Status</th>
                <th>Code</th>
                <th>Duration</th>
                <th>When</th>
                <th aria-label="Expand row" />
              </tr>
            </thead>
            <tbody>
              {delRows.map((d) => (
                <>
                  <tr
                    key={d.id}
                    data-testid={`delivery-row-${d.id}`}
                    style={{
                      borderTop: '1px solid var(--color-border, #eee)',
                    }}
                  >
                    <td>
                      <code style={{ fontSize: 12 }}>{d.event_type}</code>
                      <div className="muted" style={{ fontSize: 11 }}>
                        {d.event_id}
                      </div>
                    </td>
                    <td>#{d.attempt}</td>
                    <td style={{ color: statusTone(d.status) }}>{d.status}</td>
                    <td>{d.response_code || '—'}</td>
                    <td>{d.duration_ms} ms</td>
                    <td title={d.delivered_at}>{formatTime(d.delivered_at)}</td>
                    <td>
                      {d.response_body_preview || d.error ? (
                        <button
                          type="button"
                          onClick={() =>
                            setExpanded((cur) => (cur === d.id ? null : d.id))
                          }
                          aria-expanded={expanded === d.id}
                        >
                          {expanded === d.id ? 'Hide' : 'Show'}
                        </button>
                      ) : null}
                    </td>
                  </tr>
                  {expanded === d.id ? (
                    <tr>
                      <td colSpan={7} style={{ background: 'var(--color-surface, #fafafa)', padding: 12 }}>
                        {d.error ? (
                          <div>
                            <strong>Error:</strong>{' '}
                            <code>{d.error}</code>
                          </div>
                        ) : null}
                        {d.response_body_preview ? (
                          <div style={{ marginTop: 8 }}>
                            <strong>Response body (preview):</strong>
                            <pre
                              style={{
                                whiteSpace: 'pre-wrap',
                                background: 'var(--color-code-bg, #f0f0f0)',
                                padding: 8,
                                borderRadius: 4,
                              }}
                            >
                              {d.response_body_preview}
                            </pre>
                          </div>
                        ) : null}
                      </td>
                    </tr>
                  ) : null}
                </>
              ))}
            </tbody>
          </table>
        )}
        {delCursor ? (
          <p style={{ marginTop: 12 }}>
            <button
              type="button"
              onClick={() => void handleLoadMore()}
              disabled={delLoading}
            >
              {delLoading ? 'Loading…' : 'Load more deliveries'}
            </button>
          </p>
        ) : null}
      </section>
    </div>
  );
}
