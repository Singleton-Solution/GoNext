/**
 * Webhook subscription detail page — server component.
 *
 * Loads the subscription + the first page of deliveries in parallel
 * and hands them to the client island. Both fetches share the cookie
 * forwarding pattern used by the other admin pages.
 */
import { cookies } from 'next/headers';
import Link from 'next/link';
import { Suspense, type ReactElement } from 'react';
import { apiBaseUrl } from '../../api-client';
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
    <div role="alert" style={{ padding: 16 }}>
      <h2>Couldn&apos;t load subscription</h2>
      <p className="muted">
        The API returned {reason}. The subscription may have been deleted,
        or you may lack the <code>webhooks.manage</code> capability.
      </p>
      <Link href="/webhooks">&larr; Back to list</Link>
    </div>
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
    <section>
      <div
        style={{
          display: 'flex',
          alignItems: 'baseline',
          gap: 16,
          marginBottom: 16,
        }}
      >
        <h1 style={{ margin: 0 }}>
          {data?.subscription.name ?? 'Webhook subscription'}
        </h1>
        <Link href="/webhooks" className="muted">
          &larr; Back to list
        </Link>
      </div>
      <Suspense fallback={<p className="muted">Loading…</p>}>
        {error || !data ? (
          <FailureState reason={error ?? 'no data'} />
        ) : (
          <WebhookDetailClient
            subscription={data.subscription}
            deliveries={data.deliveries}
          />
        )}
      </Suspense>
    </section>
  );
}
