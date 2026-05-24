/**
 * DLQ detail page — server component.
 *
 * Loads a single archived task by ID and hands it to the client island.
 * The queue query parameter is required because the Asynq Inspector
 * keys lookups by (queue, id) — a task ID alone is not enough.
 */
import { cookies } from 'next/headers';
import Link from 'next/link';
import { Suspense, type ReactElement } from 'react';
import { apiBaseUrl } from '@/lib/api-client';
import { DLQDetailClient } from './DLQDetailClient';
import type { ArchivedTask } from '../types';

export const dynamic = 'force-dynamic';

async function fetchTask(
  id: string,
  queue: string,
): Promise<{ data: ArchivedTask | null; error: string | null }> {
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

  const url = `${apiBaseUrl}/api/v1/admin/jobs/dlq/${encodeURIComponent(id)}?queue=${encodeURIComponent(queue)}`;
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
    const json = (await res.json()) as ArchivedTask;
    return { data: json, error: null };
  } catch (err) {
    const reason = err instanceof Error ? err.message : 'network error';
    return { data: null, error: reason };
  }
}

function DetailSkeleton(): ReactElement {
  return (
    <div aria-busy="true" aria-live="polite" style={{ padding: 16 }}>
      <span className="visually-hidden">Loading task…</span>
      <div
        style={{
          height: 24,
          margin: '8px 0',
          width: '40%',
          background: 'var(--color-border)',
          opacity: 0.4,
          borderRadius: 'var(--radius)',
        }}
      />
      <div
        style={{
          height: 200,
          margin: '8px 0',
          background: 'var(--color-border)',
          opacity: 0.4,
          borderRadius: 'var(--radius)',
        }}
      />
    </div>
  );
}

function FailureState({
  reason,
  queue,
}: {
  reason: string;
  queue: string;
}): ReactElement {
  return (
    <section role="alert" style={{ padding: 16 }}>
      <h1>Task not available</h1>
      <p className="muted">
        We couldn&apos;t load this task ({reason}). It may have been
        replayed or discarded already.
      </p>
      <Link href={`/jobs/dlq?queue=${encodeURIComponent(queue)}`}>
        ← Back to DLQ
      </Link>
    </section>
  );
}

type PageProps = {
  params: Promise<{ id: string }>;
  searchParams?: Promise<Record<string, string | string[] | undefined>>;
};

export default async function DLQDetailPage(
  props: PageProps,
): Promise<ReactElement> {
  const { id } = await props.params;
  const sp = (await props.searchParams) ?? {};
  const rawQueue = sp.queue;
  const queue =
    typeof rawQueue === 'string' && rawQueue.length > 0
      ? rawQueue
      : 'default';

  const { data, error } = await fetchTask(id, queue);

  return (
    <Suspense fallback={<DetailSkeleton />}>
      {error || !data ? (
        <FailureState reason={error ?? 'no data'} queue={queue} />
      ) : (
        <DLQDetailClient initialTask={data} />
      )}
    </Suspense>
  );
}
