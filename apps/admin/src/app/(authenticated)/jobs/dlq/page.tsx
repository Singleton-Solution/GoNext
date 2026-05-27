/**
 * DLQ list page — server component.
 *
 * Fetches the first page of archived tasks for the selected queue and
 * hands it to the client island for interactive behaviour. Cookies are
 * forwarded so the API sees the admin's session.
 *
 * Brand: Living-Systems (#432). The page surface is cream paper-2.
 * The page-head follows the handoff's instrument-panel feel — calm,
 * dense, with the signature italic accent on `*queue*`. The italic
 * sits on the second word so the headline reads "Dead-letter *queue*"
 * with the serif emphasising what surface the operator is on.
 *
 * Issue #262.
 */
import { type ReactElement, Suspense } from 'react';
import Link from 'next/link';
import { ChevronLeft } from 'lucide-react';
import { serverApiFetch } from '@/lib/server-api';
import { Headline } from '@/components/ui/headline';
import { Card } from '@/components/ui/card';
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
  try {
    const res = await serverApiFetch(
      `/api/v1/admin/jobs/dlq?queue=${encodeURIComponent(queue)}&limit=30`,
    );
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

/**
 * Skeleton — paper-3 rows on the panel surface. Eight strips to fill
 * the usual viewport without feeling like a loading bar.
 */
function DLQSkeleton(): ReactElement {
  return (
    <Card
      aria-busy="true"
      aria-live="polite"
      data-testid="dlq-skeleton"
      className="overflow-hidden"
    >
      <span className="sr-only">Loading archived tasks…</span>
      <div className="flex flex-col gap-1 p-2">
        {Array.from({ length: 8 }).map((_, idx) => (
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
      data-testid="dlq-failure"
      className="border-danger/30 bg-danger-soft/20 p-6"
    >
      <h2 className="font-display text-lg font-bold text-ink">
        Couldn&apos;t load the DLQ
      </h2>
      <p className="mt-2 font-sans text-sm text-fg-muted">
        We couldn&apos;t fetch archived tasks from the API ({reason}). If
        you don&apos;t have the{' '}
        <code className="rounded-xs bg-paper-3 px-1 font-mono text-2xs text-ink-soft">
          jobs.admin
        </code>{' '}
        capability, that explains it — ask an admin to grant it. Otherwise,
        try refreshing.
      </p>
    </Card>
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
    <section
      data-testid="dlq-list-page"
      className="flex flex-col gap-6"
    >
      {/* Page head — operator surface: calm, instrument-panel feel.
          The italic accent on "queue" follows the brand rule
          ("the second word, the noun being acted on"). */}
      <div className="flex items-end justify-between gap-6 border-b border-border pb-6">
        <div className="flex flex-col gap-3">
          <span className="font-sans text-2xs font-medium uppercase tracking-[0.12em] text-emerald-deep">
            Jobs · Failed
          </span>
          <Headline as="h1" size="page">
            Dead-letter <em>queue</em>.
          </Headline>
          <p className="max-w-[540px] font-sans text-sm text-fg-muted">
            Background tasks whose handlers exhausted their retry budget.
            Inspect the payload, replay if the upstream issue is resolved,
            or discard if the work is no longer relevant. Use redact to
            mask sensitive fields before sharing.
          </p>
        </div>
        <Link
          href="/jobs"
          className="inline-flex shrink-0 items-center gap-1 font-sans text-sm text-fg-subtle transition-colors hover:text-ink"
        >
          <ChevronLeft className="h-[13px] w-[13px]" aria-hidden="true" />
          Back to jobs
        </Link>
      </div>
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
