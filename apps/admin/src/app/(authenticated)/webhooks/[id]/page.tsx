/**
 * Webhook subscription detail page — server component.
 *
 * Loads the subscription + the first page of deliveries in parallel
 * and hands them to the client island. Both fetches share the cookie
 * forwarding pattern used by the other admin pages.
 *
 * Brand: Living-Systems (#432). The Headline lives in the client
 * island because the subscription name is part of the dynamic data we
 * load — the page head is a calm cream-paper rule with the name as
 * the italic accent so the operator immediately sees which endpoint
 * they're editing.
 */
import { cookies } from 'next/headers';
import Link from 'next/link';
import { ChevronLeft } from 'lucide-react';
import { Suspense, type ReactElement } from 'react';
import { apiBaseUrl } from '@/lib/api-client';
import { Card } from '@/components/ui/card';
import { WebhookDetailClient } from './WebhookDetailClient';
import type { DeliveryListResponse, Subscription } from '../types';

export const dynamic = 'force-dynamic';

async function fetchSubscription(
  id: string,
): Promise<{
  data: { subscription: Subscription; deliveries: DeliveryListResponse } | null;
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
  const headers: HeadersInit = {
    Accept: 'application/json',
    ...(cookieHeader ? { Cookie: cookieHeader } : {}),
  };
  const subUrl = `${apiBaseUrl}/api/v1/admin/webhooks/${encodeURIComponent(id)}`;
  const delUrl = `${apiBaseUrl}/api/v1/admin/webhooks/${encodeURIComponent(id)}/deliveries?limit=30`;
  try {
    const [subRes, delRes] = await Promise.all([
      fetch(subUrl, { headers, cache: 'no-store' }),
      fetch(delUrl, { headers, cache: 'no-store' }),
    ]);
    if (!subRes.ok) return { data: null, error: `HTTP ${subRes.status}` };
    const subscription = (await subRes.json()) as Subscription;
    const deliveries: DeliveryListResponse = delRes.ok
      ? ((await delRes.json()) as DeliveryListResponse)
      : { data: [], pagination: { next_cursor: '' } };
    return {
      data: { subscription, deliveries },
      error: null,
    };
  } catch (err) {
    return { data: null, error: err instanceof Error ? err.message : 'network error' };
  }
}

function FailureState({ reason }: { reason: string }): ReactElement {
  return (
    <section role="alert" data-testid="webhook-detail-failure">
      <Card className="border-danger/30 bg-danger-soft/20 p-6">
        <h1 className="font-display text-xl font-bold text-ink">
          Couldn&apos;t load subscription
        </h1>
        <p className="mt-2 font-sans text-sm text-fg-muted">
          The API returned {reason}. The subscription may have been deleted,
          or you may lack the{' '}
          <code className="rounded-xs bg-paper-3 px-1 font-mono text-2xs text-ink-soft">
            webhooks.manage
          </code>{' '}
          capability.
        </p>
        <Link
          href="/webhooks"
          className="mt-3 inline-flex items-center gap-1 font-sans text-sm text-emerald-deep hover:underline"
        >
          <ChevronLeft className="h-[13px] w-[13px]" aria-hidden="true" />
          Back to list
        </Link>
      </Card>
    </section>
  );
}

type PageProps = {
  params: Promise<{ id: string }>;
};

export default async function WebhookDetailPage(
  props: PageProps,
): Promise<ReactElement> {
  const params = await props.params;
  const { data, error } = await fetchSubscription(params.id);

  return (
    <Suspense fallback={<p className="font-sans text-sm text-fg-muted">Loading…</p>}>
      {error || !data ? (
        <FailureState reason={error ?? 'no data'} />
      ) : (
        <WebhookDetailClient
          subscription={data.subscription}
          deliveries={data.deliveries}
        />
      )}
    </Suspense>
  );
}
