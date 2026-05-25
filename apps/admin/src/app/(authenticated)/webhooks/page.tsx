/**
 * Webhooks list page — server component.
 *
 * Fetches the first page of subscriptions and hands it to the client
 * island. Cookies are forwarded so the API sees the admin's session.
 * On any HTTP failure we render a friendly state rather than throwing
 * — operators without `webhooks.manage` see the same blank panel.
 *
 * Brand: Living-Systems (#432). Page-head follows the same calm
 * instrument-panel feel as the DLQ surface — Headline ("Webhook
 * *subscriptions*."), eyebrow, Geist body, primary CTA to create.
 */
import { cookies } from 'next/headers';
import Link from 'next/link';
import { Plus } from 'lucide-react';
import { Suspense, type ReactElement } from 'react';
import { apiBaseUrl } from '@/lib/api-client';
import { Headline } from '@/components/ui/headline';
import { Card } from '@/components/ui/card';
import { Button } from '@/components/ui/button';
import { WebhooksListClient } from './WebhooksListClient';
import type { SubscriptionListResponse } from './types';

export const dynamic = 'force-dynamic';

async function fetchInitialSubscriptions(): Promise<{
  data: SubscriptionListResponse | null;
  error: string | null;
}> {
  let cookieHeader = '';
  try {
    const store = await cookies();
    cookieHeader = store
      .getAll()
      .map((c) => `${c.name}=${c.value}`)
      .join('; ');
  } catch {
    cookieHeader = '';
  }
  const url = `${apiBaseUrl}/api/v1/admin/webhooks?limit=30`;
  try {
    const res = await fetch(url, {
      method: 'GET',
      headers: {
        Accept: 'application/json',
        ...(cookieHeader ? { Cookie: cookieHeader } : {}),
      },
      cache: 'no-store',
    });
    if (!res.ok) {
      return { data: null, error: `HTTP ${res.status}` };
    }
    const json = (await res.json()) as SubscriptionListResponse;
    return {
      data: {
        data: Array.isArray(json.data) ? json.data : [],
        pagination: json.pagination ?? { next_cursor: '' },
      },
      error: null,
    };
  } catch (err) {
    return { data: null, error: err instanceof Error ? err.message : 'network error' };
  }
}

function ListSkeleton(): ReactElement {
  return (
    <Card
      aria-busy="true"
      aria-live="polite"
      data-testid="webhooks-skeleton"
      className="overflow-hidden"
    >
      <span className="sr-only">Loading webhook subscriptions…</span>
      <div className="flex flex-col gap-1 p-2">
        {Array.from({ length: 6 }).map((_, idx) => (
          <div
            key={idx}
            className="h-8 animate-pulse rounded-sm bg-paper-3/60"
          />
        ))}
      </div>
    </Card>
  );
}

function FailureState({ reason }: { reason: string }): ReactElement {
  return (
    <Card
      role="alert"
      data-testid="webhooks-failure"
      className="border-danger/30 bg-danger-soft/20 p-6"
    >
      <h2 className="font-display text-lg font-bold text-ink">
        Couldn&apos;t load webhooks
      </h2>
      <p className="mt-2 font-sans text-sm text-fg-muted">
        We couldn&apos;t fetch your subscriptions from the API ({reason}). If
        you don&apos;t have the{' '}
        <code className="rounded-xs bg-paper-3 px-1 font-mono text-2xs text-ink-soft">
          webhooks.manage
        </code>{' '}
        capability, that explains it — ask an admin to grant it.
      </p>
    </Card>
  );
}

export default async function WebhooksPage(): Promise<ReactElement> {
  const { data, error } = await fetchInitialSubscriptions();

  return (
    <section
      data-testid="webhooks-list-page"
      className="flex flex-col gap-6"
    >
      <div className="flex items-end justify-between gap-6 border-b border-border pb-6">
        <div className="flex flex-col gap-3">
          <span className="font-sans text-2xs font-medium uppercase tracking-[0.12em] text-emerald-deep">
            Integrations · Outbound
          </span>
          <Headline as="h1" size="page">
            Webhook <em>subscriptions</em>.
          </Headline>
          <p className="max-w-[540px] font-sans text-sm text-fg-muted">
            Outbound HTTP notifications. Each subscription receives a signed
            POST when one of its events fires. Test verifies a fresh
            endpoint; open a row to see recent deliveries.
          </p>
        </div>
        <Button asChild variant="emerald">
          <Link href="/webhooks/new">
            <Plus className="h-[14px] w-[14px]" aria-hidden="true" />
            New subscription
          </Link>
        </Button>
      </div>
      <Suspense fallback={<ListSkeleton />}>
        {error || !data ? (
          <FailureState reason={error ?? 'no data'} />
        ) : (
          <WebhooksListClient initialData={data} />
        )}
      </Suspense>
    </section>
  );
}
