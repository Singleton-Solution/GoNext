/**
 * Webhooks list page — server component.
 *
 * Fetches the first page of subscriptions and hands it to the client
 * island. Cookies are forwarded so the API sees the admin's session.
 * On any HTTP failure we render a friendly state rather than throwing
 * — operators without `webhooks.manage` see the same blank panel.
 */
import { cookies } from 'next/headers';
import Link from 'next/link';
import { Suspense, type ReactElement } from 'react';
import { apiBaseUrl } from '../api-client';
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
    <div aria-busy="true" aria-live="polite" style={{ padding: 16 }}>
      <span className="visually-hidden">Loading webhook subscriptions…</span>
      {Array.from({ length: 4 }).map((_, idx) => (
        <div
          key={idx}
          style={{
            height: 32,
            margin: '8px 0',
            background: 'var(--color-border)',
            opacity: 0.4,
            borderRadius: 'var(--radius)',
          }}
        />
      ))}
    </div>
  );
}

function FailureState({ reason }: { reason: string }): ReactElement {
  return (
    <div role="alert" style={{ padding: 16 }}>
      <h2>Couldn&apos;t load webhooks</h2>
      <p className="muted">
        We couldn&apos;t fetch your subscriptions from the API ({reason}). If you
        don&apos;t have the <code>webhooks.manage</code> capability, that
        explains it — ask an admin to grant it.
      </p>
    </div>
  );
}

export default async function WebhooksPage(): Promise<ReactElement> {
  const { data, error } = await fetchInitialSubscriptions();

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
        <h1 style={{ margin: 0 }}>Webhooks</h1>
        <Link href="/webhooks/new" className="primary-action">
          New subscription
        </Link>
      </div>
      <p className="muted" style={{ marginTop: 0 }}>
        Outbound HTTP notifications. Each subscription receives a signed
        POST when one of its subscribed events fires. Use the Test
        button to verify a fresh endpoint, or open a row to see its
        recent deliveries.
      </p>
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
