/**
 * DLQ list page — server component.
 *
 * Fetches the first page of archived tasks for the selected queue and
 * hands it to the client island for interactive behaviour. Cookies are
 * forwarded so the API sees the admin's session.
 *
 * Issue #262.
 */
import { cookies } from 'next/headers';
import Link from 'next/link';
import { Suspense, type ReactElement } from 'react';
import { apiBaseUrl } from '../../api-client';
import { DLQListClient } from './DLQListClient';
import type { DLQListResponse } from './types';

export const dynamic = 'force-dynamic';

/**
 * Fetch the first page of archived tasks. Returns `null` on failure so
 * the caller can render a friendly state without throwing the whole
 * page. This is the same pattern the Posts list uses — operators get a
 * blank panel they can refresh, not a stack trace.
 */
async function fetchInitialDLQ(
  queue: string,
): Promise<{ data: DLQListResponse | null; error: string | null }> {
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

  const url = `${apiBaseUrl}/api/v1/admin/jobs/dlq?queue=${encodeURIComponent(queue)}&limit=30`;
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
    const json = (await res.json()) as DLQListResponse;
    return {
      data: {
        data: Array.isArray(json.data) ? json.data : [],
        pagination: json.pagination ?? { next_cursor: '' },
      },
      error: null,
    };
  } catch (err) {
    const reason = err instanceof Error ? err.message : 'network error';
    return { data: null, error: reason };
  }
}

function DLQSkeleton(): ReactElement {
  return (
    <div aria-busy="true" aria-live="polite" style={{ padding: 16 }}>
      <span className="visually-hidden">Loading archived tasks…</span>
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
      <h2>Couldn&apos;t load the DLQ</h2>
      <p className="muted">
        We couldn&apos;t fetch archived tasks from the API ({reason}). If
        you don&apos;t have the <code>jobs.admin</code> capability, that
        explains it — ask an admin to grant it. Otherwise, try refreshing.
      </p>
    </div>
  );
}

type PageProps = {
  searchParams?: Promise<Record<string, string | string[] | undefined>>;
};

export default async function DLQPage(props: PageProps): Promise<ReactElement> {
  const params = (await props.searchParams) ?? {};
  const rawQueue = params.queue;
  const queue =
    typeof rawQueue === 'string' && rawQueue.length > 0
      ? rawQueue
      : 'default';

  const { data, error } = await fetchInitialDLQ(queue);

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
        <h1 style={{ margin: 0 }}>Dead-letter queue</h1>
        <Link href="/jobs" className="muted">
          ← Back to jobs
        </Link>
      </div>
      <p className="muted" style={{ marginTop: 0 }}>
        Background tasks whose handlers exhausted their retry budget.
        Inspect the payload, replay if the upstream issue is resolved,
        or discard if the work is no longer relevant. Use redact to
        mask sensitive fields before sharing a screenshot.
      </p>
      <Suspense fallback={<DLQSkeleton />}>
        {error || !data ? (
          <FailureState reason={error ?? 'no data'} />
        ) : (
          <DLQListClient initialQueue={queue} initialData={data} />
        )}
      </Suspense>
    </section>
  );
}
