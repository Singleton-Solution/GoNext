'use client';

/**
 * <WebhookDetailClient> — edit form + recent deliveries table.
 *
 * Brand: Living-Systems (#432). The page is a two-section operator
 * surface:
 *
 *   1. Header — Headline ("Subscription: *name*"), StatusBadge,
 *      mono URL, and a Test/Disable/Delete toolbar pinned to the
 *      top-right. The italic accent lands on the subscription name.
 *   2. Configuration Card — paper-2 panel holding the edit form
 *      (Input/Label primitives, EventCatalog with lavender accents).
 *   3. Deliveries Card — newest-first table with response_code in a
 *      mono badge, duration in Geist Mono tabular-nums. Expanding a
 *      row reveals the preview in a paper-3 recessed surface.
 */
import { useRouter } from 'next/navigation';
import {
  ChevronLeft,
  Power,
  Trash2,
  Zap,
} from 'lucide-react';
import {
  Fragment,
  useCallback,
  useEffect,
  useMemo,
  useState,
  type ChangeEvent,
  type FormEvent,
  type ReactElement,
} from 'react';
import Link from 'next/link';
import { Headline } from '@/components/ui/headline';
import { Card } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
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

/**
 * Status → Badge variant. Deliveries inherit the same emerald / lavender
 * / danger palette as the subscription StatusBadge.
 */
function deliveryVariant(
  status: Delivery['status'],
): 'emerald' | 'lavender' | 'danger' | 'outline' | 'default' {
  switch (status) {
    case 'success':
      return 'emerald';
    case 'retry':
      return 'lavender';
    case 'failed':
      return 'danger';
    case 'test':
      return 'outline';
    default:
      return 'default';
  }
}

/**
 * HTTP response code → mono badge variant. 2xx → emerald, 3xx →
 * lavender, 4xx → warning, 5xx → danger. Calm at-a-glance signal.
 */
function codeBadgeClass(code: number): string {
  if (code === 0) return 'bg-paper-3 text-fg-subtle border-border';
  if (code >= 500) return 'bg-danger-soft text-danger border-transparent';
  if (code >= 400) return 'bg-warning-soft text-warning border-transparent';
  if (code >= 300) return 'bg-lavender-soft text-lavender-deep border-transparent';
  if (code >= 200) return 'bg-emerald-soft text-emerald-deep border-transparent';
  return 'bg-paper-3 text-fg-subtle border-border';
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
    <section
      data-testid="webhook-detail-page"
      className="flex flex-col gap-6"
    >
      {/* Page head with breadcrumb + italic-accent on the name. */}
      <div className="flex flex-wrap items-end justify-between gap-6 border-b border-border pb-6">
        <div className="flex flex-col gap-3">
          <span className="font-sans text-2xs font-medium uppercase tracking-[0.12em] text-emerald-deep">
            Integrations · Outbound · Detail
          </span>
          <Headline as="h1" size="sub">
            Subscription: <em>{sub.name}</em>
          </Headline>
          <div className="flex flex-wrap items-center gap-2">
            <StatusBadge subscription={sub} />
            <code className="font-mono text-2xs text-fg-subtle">
              {sub.url}
            </code>
          </div>
        </div>
        <Link
          href="/webhooks"
          className="inline-flex shrink-0 items-center gap-1 font-sans text-sm text-fg-subtle transition-colors hover:text-ink"
        >
          <ChevronLeft className="h-[13px] w-[13px]" aria-hidden="true" />
          Back to list
        </Link>
      </div>

      {/* Action toolbar */}
      <div className="flex flex-wrap items-center gap-2">
        <Button
          type="button"
          variant="emerald"
          onClick={() => void handleTest()}
        >
          <Zap className="h-[14px] w-[14px]" aria-hidden="true" />
          Test
        </Button>
        <Button
          type="button"
          variant="default"
          onClick={() => void handleToggleActive()}
        >
          <Power className="h-[14px] w-[14px]" aria-hidden="true" />
          {sub.active ? 'Disable' : 'Enable'}
        </Button>
        <Button
          type="button"
          variant="destructive"
          onClick={() => void handleDelete()}
        >
          <Trash2 className="h-[14px] w-[14px]" aria-hidden="true" />
          Delete
        </Button>
      </div>

      {testResult ? (
        <div
          role="status"
          data-testid="test-result"
          className={
            testResult.delivered
              ? 'rounded-md border border-emerald/30 bg-emerald-soft px-4 py-3 font-sans text-sm text-emerald-deep'
              : 'rounded-md border border-warning/30 bg-warning-soft px-4 py-3 font-sans text-sm text-warning'
          }
        >
          {testResult.delivered ? 'Delivered' : 'Subscriber responded'} —{' '}
          <code className="font-mono text-xs">
            HTTP {testResult.response_code}
          </code>{' '}
          in{' '}
          <code className="font-mono text-xs tabular-nums">
            {testResult.duration_ms} ms
          </code>
        </div>
      ) : null}
      {testError ? (
        <div
          role="alert"
          className="rounded-md border border-danger/30 bg-danger-soft px-4 py-3 font-sans text-sm text-danger"
        >
          Test failed: {testError}
        </div>
      ) : null}
      {error ? (
        <div
          role="alert"
          className="rounded-md border border-danger/30 bg-danger-soft px-4 py-3 font-sans text-sm text-danger"
        >
          {error}
        </div>
      ) : null}

      {/* Edit form */}
      <Card className="overflow-hidden">
        <div className="border-b border-border bg-paper-2 px-6 py-4">
          <h2 className="font-display text-sm font-bold uppercase tracking-[0.08em] text-fg-subtle">
            Configuration
          </h2>
        </div>
        <form
          onSubmit={(ev) => void handleSubmit(ev)}
          className="flex flex-col gap-5 px-6 py-5"
        >
          <div className="flex flex-col gap-[6px]">
            <Label htmlFor="sub-name">Name</Label>
            <Input
              id="sub-name"
              type="text"
              value={form.name}
              onChange={handleField('name')}
              required
              maxLength={200}
            />
          </div>
          <div className="flex flex-col gap-[6px]">
            <Label htmlFor="sub-url">Endpoint URL</Label>
            <Input
              id="sub-url"
              type="url"
              value={form.url}
              onChange={handleField('url')}
              required
            />
          </div>
          <EventCatalog value={form.events} onChange={handleEvents} />
          <div className="flex items-center gap-3">
            <Button
              type="submit"
              variant="emerald"
              disabled={saving || !dirty}
            >
              {saving ? 'Saving…' : 'Save changes'}
            </Button>
            {saved && !dirty ? (
              <span
                role="status"
                className="font-sans text-xs text-emerald-deep"
              >
                Saved
              </span>
            ) : null}
          </div>
        </form>
      </Card>

      {/* Deliveries panel */}
      <Card className="overflow-hidden">
        <div className="flex items-center justify-between border-b border-border bg-paper-2 px-4 py-3">
          <div className="flex items-center gap-3">
            <h2 className="font-display text-sm font-bold text-ink">
              Recent deliveries
            </h2>
            <span className="rounded-pill bg-paper-3 px-2 py-[2px] font-mono text-2xs tabular-nums text-fg-subtle">
              {delRows.length}
            </span>
          </div>
        </div>
        {delRows.length === 0 ? (
          <div className="p-6 font-sans text-sm text-fg-muted">
            No deliveries yet. Use Test to send a synthetic event, or
            wait for a real one to fire.
          </div>
        ) : (
          <table
            aria-label="Recent deliveries"
            className="w-full border-collapse bg-paper"
          >
            <thead>
              <tr className="bg-paper-2 text-left">
                <th className="border-b border-border-subtle px-4 py-3 font-sans text-xs font-medium text-fg-subtle">
                  Event
                </th>
                <th className="border-b border-border-subtle px-4 py-3 font-sans text-xs font-medium text-fg-subtle">
                  Attempt
                </th>
                <th className="border-b border-border-subtle px-4 py-3 font-sans text-xs font-medium text-fg-subtle">
                  Status
                </th>
                <th className="border-b border-border-subtle px-4 py-3 font-sans text-xs font-medium text-fg-subtle">
                  Code
                </th>
                <th className="border-b border-border-subtle px-4 py-3 font-sans text-xs font-medium text-fg-subtle">
                  Duration
                </th>
                <th className="border-b border-border-subtle px-4 py-3 font-sans text-xs font-medium text-fg-subtle">
                  When
                </th>
                <th
                  aria-label="Expand row"
                  className="border-b border-border-subtle px-4 py-3"
                />
              </tr>
            </thead>
            <tbody>
              {delRows.map((d) => (
                <Fragment key={d.id}>
                  <tr
                    data-testid={`delivery-row-${d.id}`}
                    className="border-b border-border-subtle transition-colors hover:bg-paper-2/60 last:border-b-0"
                  >
                    <td className="px-4 py-3 align-middle">
                      <code className="font-mono text-xs font-medium text-ink">
                        {d.event_type}
                      </code>
                      <div className="mt-[2px] font-mono text-2xs text-fg-subtle">
                        {d.event_id}
                      </div>
                    </td>
                    <td className="px-4 py-3 align-middle">
                      <span className="font-mono text-xs tabular-nums text-fg-muted">
                        #{d.attempt}
                      </span>
                    </td>
                    <td className="px-4 py-3 align-middle">
                      <Badge variant={deliveryVariant(d.status)} dot>
                        {d.status}
                      </Badge>
                    </td>
                    <td className="px-4 py-3 align-middle">
                      {/* Response code rendered as a mono badge — same
                          shape as a tag but the digits feel like an
                          instrument-panel readout. */}
                      <code
                        className={`inline-flex items-center rounded-sm border px-2 py-[2px] font-mono text-xs tabular-nums ${codeBadgeClass(d.response_code)}`}
                      >
                        {d.response_code || '—'}
                      </code>
                    </td>
                    <td className="px-4 py-3 align-middle">
                      <span className="font-mono text-xs tabular-nums text-fg-muted">
                        {d.duration_ms} ms
                      </span>
                    </td>
                    <td
                      title={d.delivered_at}
                      className="px-4 py-3 align-middle font-mono text-2xs tabular-nums text-fg-subtle"
                    >
                      {formatTime(d.delivered_at)}
                    </td>
                    <td className="px-4 py-3 align-middle text-right">
                      {d.response_body_preview || d.error ? (
                        <Button
                          type="button"
                          variant="ghost"
                          size="sm"
                          onClick={() =>
                            setExpanded((cur) =>
                              cur === d.id ? null : d.id,
                            )
                          }
                          aria-expanded={expanded === d.id}
                          className="h-7 px-2"
                        >
                          {expanded === d.id ? 'Hide' : 'Show'}
                        </Button>
                      ) : null}
                    </td>
                  </tr>
                  {expanded === d.id ? (
                    <tr>
                      <td
                        colSpan={7}
                        className="border-b border-border-subtle bg-paper-2 px-4 py-3"
                      >
                        {d.error ? (
                          <div className="mb-2 flex items-center gap-2 font-sans text-xs text-danger">
                            <strong>Error:</strong>
                            <code className="font-mono text-2xs">
                              {d.error}
                            </code>
                          </div>
                        ) : null}
                        {d.response_body_preview ? (
                          <div>
                            <div className="mb-1 font-display text-2xs font-bold uppercase tracking-[0.08em] text-fg-subtle">
                              Response body (preview)
                            </div>
                            <pre className="max-h-[260px] overflow-auto whitespace-pre-wrap rounded-md border border-border bg-paper-3 p-3 font-mono text-2xs leading-relaxed text-ink-soft">
                              {d.response_body_preview}
                            </pre>
                          </div>
                        ) : null}
                      </td>
                    </tr>
                  ) : null}
                </Fragment>
              ))}
            </tbody>
          </table>
        )}
        {delCursor ? (
          <div className="flex justify-end border-t border-border bg-paper-2 px-4 py-3">
            <Button
              type="button"
              variant="default"
              size="sm"
              onClick={() => void handleLoadMore()}
              disabled={delLoading}
            >
              {delLoading ? 'Loading…' : 'Load more deliveries'}
            </Button>
          </div>
        ) : null}
      </Card>
    </section>
  );
}
