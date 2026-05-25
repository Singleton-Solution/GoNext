/**
 * DLQ detail page — server component.
 *
 * Loads a single archived task by ID and hands it to the client island.
 * The queue query parameter is required because the Asynq Inspector
 * keys lookups by (queue, id) — a task ID alone is not enough.
 *
 * Brand: Living-Systems (#432). The Headline lives in the client island
 * because the task type is part of the dynamic data we load — the page
 * head is a serene cream-paper rule with the task type rendered as the
 * italic accent so an operator immediately sees what kind of failure
 * they're inspecting.
 */
import { cookies } from 'next/headers';
import Link from 'next/link';
import { ChevronLeft } from 'lucide-react';
import { Suspense, type ReactElement } from 'react';
import { apiBaseUrl } from '@/lib/api-client';
import { Card } from '@/components/ui/card';
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
    <Card
      aria-busy="true"
      aria-live="polite"
      data-testid="dlq-detail-skeleton"
      className="overflow-hidden p-6"
    >
      <span className="sr-only">Loading task…</span>
      <div className="h-6 w-2/5 animate-pulse rounded-sm bg-paper-3/60" />
      <div className="mt-3 h-[200px] animate-pulse rounded-sm bg-paper-3/60" />
    </Card>
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
    <section role="alert" data-testid="dlq-detail-failure">
      <Card className="border-danger/30 bg-danger-soft/20 p-6">
        <h1 className="font-display text-xl font-bold text-ink">
          Task not available
        </h1>
        <p className="mt-2 font-sans text-sm text-fg-muted">
          We couldn&apos;t load this task ({reason}). It may have been
          replayed or discarded already.
        </p>
        <Link
          href={`/jobs/dlq?queue=${encodeURIComponent(queue)}`}
          className="mt-3 inline-flex items-center gap-1 font-sans text-sm text-emerald-deep hover:underline"
        >
          <ChevronLeft className="h-[13px] w-[13px]" aria-hidden="true" />
          Back to DLQ
        </Link>
      </Card>
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
