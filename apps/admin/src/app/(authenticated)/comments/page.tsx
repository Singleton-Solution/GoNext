/**
 * Comments — admin list screen.
 *
 * Server component that fetches the first page of comments from
 * `/api/v1/admin/comments` and hands the result to the
 * `CommentListClient` island. The split mirrors the Posts list so
 * the first render is fast (no client hydration to fetch comments)
 * while still getting a rich interactive table.
 *
 * Filter state lives in the URL: `?status=pending` etc. Reload of
 * a filtered URL restores the same view, and browser back/forward
 * works without extra wiring.
 *
 * Visual treatment: Headline (`Comment <em>moderation</em>.`) on
 * the cream surface, with a filter-chip toolbar and ResourceList-
 * style table inside the CommentListClient island. The skeleton +
 * error states use the brand's paper-2 + danger-soft tokens.
 */
import { Suspense, type ReactElement } from 'react';

import { Headline } from '@/components/ui/headline';
import { serverApiFetch } from '@/lib/server-api';

import { CommentListClient } from './CommentListClient';
import {
  toListResponse,
  type CommentListResponse,
  type CommentStatus,
  type WireListResponse,
} from './types';

export const dynamic = 'force-dynamic';

function Skeleton(): ReactElement {
  return (
    <div
      className="overflow-hidden rounded-lg border border-border bg-paper-2"
      aria-busy="true"
      aria-live="polite"
    >
      <div className="p-4">
        <span className="sr-only">Loading comments…</span>
        {Array.from({ length: 6 }).map((_, idx) => (
          <div
            key={idx}
            className="my-3 h-4 rounded-xs"
            style={{
              background:
                'linear-gradient(90deg, var(--paper-3) 0%, var(--paper-2) 50%, var(--paper-3) 100%)',
              backgroundSize: '200% 100%',
            }}
          />
        ))}
      </div>
    </div>
  );
}

function FetchFailureState({ reason }: { reason: string }): ReactElement {
  return (
    <div
      role="alert"
      className="rounded-lg border border-danger/30 bg-danger-soft/60 p-6"
    >
      <h2 className="m-0 mb-2 font-display text-xl font-bold text-ink">
        Couldn&apos;t load comments
      </h2>
      <p className="m-0 font-sans text-sm text-fg-muted">
        We couldn&apos;t fetch the list from the GoNext API ({reason}). Try
        reloading; if it keeps failing the admin API may not be reachable
        yet.
      </p>
    </div>
  );
}

/**
 * Server-side fetch helper. Forwards the session cookie via
 * `serverApiFetch` so the API sees the operator. Returns `null` on
 * any failure so the caller can render a friendly state without
 * crashing the layout.
 */
async function fetchInitialComments(
  params: { status?: string; postId?: string; userId?: string },
): Promise<{ data: CommentListResponse | null; error: string | null }> {
  const qs = new URLSearchParams();
  if (params.status) qs.set('status', params.status);
  if (params.postId) qs.set('post_id', params.postId);
  if (params.userId) qs.set('user_id', params.userId);
  qs.set('limit', '30');

  try {
    const res = await serverApiFetch(`/api/v1/admin/comments?${qs.toString()}`);
    if (!res.ok) {
      return { data: null, error: `HTTP ${res.status}` };
    }
    const wire = (await res.json()) as WireListResponse;
    return {
      data: toListResponse({
        data: Array.isArray(wire.data) ? wire.data : [],
        pagination: wire.pagination ?? { next_cursor: '' },
      }),
      error: null,
    };
  } catch (err) {
    const reason = err instanceof Error ? err.message : 'network error';
    return { data: null, error: reason };
  }
}

interface PageProps {
  // Next 15: searchParams is a Promise.
  searchParams?: Promise<Record<string, string | string[] | undefined>>;
}

async function CommentsListServer({
  searchParams,
}: PageProps): Promise<ReactElement> {
  const params = (await searchParams) ?? {};

  const statusRaw =
    typeof params.status === 'string' ? params.status : undefined;
  const allowed: ReadonlyArray<CommentStatus> = [
    'pending',
    'approved',
    'spam',
    'trash',
  ];
  const status = allowed.includes(statusRaw as CommentStatus)
    ? statusRaw
    : undefined;
  const postId = typeof params.post_id === 'string' ? params.post_id : undefined;
  const userId = typeof params.user_id === 'string' ? params.user_id : undefined;

  const { data, error } = await fetchInitialComments({ status, postId, userId });
  if (error || !data) {
    return <FetchFailureState reason={error ?? 'no data'} />;
  }
  return <CommentListClient initialData={data} />;
}

export default function CommentsPage(props: PageProps): ReactElement {
  return (
    <section className="flex flex-col gap-6">
      <div className="flex flex-col gap-2 border-b border-border pb-4">
        <span className="font-sans text-xs font-medium uppercase tracking-wide text-emerald-deep">
          Community
        </span>
        <Headline as="h1" size="sub">
          Comment <em>moderation</em>.
        </Headline>
        <p className="m-0 max-w-[540px] font-sans text-sm text-fg-muted">
          Approve, spam, or trash incoming threads. The queue filters by{' '}
          <em className="font-serif italic text-emerald-deep">status</em> and
          syncs to the URL so reloads keep their place.
        </p>
      </div>
      <Suspense fallback={<Skeleton />}>
        <CommentsListServer searchParams={props.searchParams} />
      </Suspense>
    </section>
  );
}
