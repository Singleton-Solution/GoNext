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
 */
import { cookies } from 'next/headers';
import { Suspense, type ReactElement } from 'react';
import { apiBaseUrl } from '@/lib/api-client';
import { CommentListClient } from './CommentListClient';
import styles from './comments.module.css';
import {
  toListResponse,
  type CommentListResponse,
  type CommentStatus,
  type WireListResponse,
} from './types';

export const dynamic = 'force-dynamic';

function Skeleton(): ReactElement {
  return (
    <div className={styles.tableWrap} aria-busy="true" aria-live="polite">
      <div style={{ padding: 'var(--space-4)' }}>
        <span className="visually-hidden">Loading comments…</span>
        {Array.from({ length: 6 }).map((_, idx) => (
          <div
            key={idx}
            style={{
              height: 18,
              margin: 'var(--space-3) 0',
              background:
                'linear-gradient(90deg, #eef0f3 0%, #f6f7f9 50%, #eef0f3 100%)',
              backgroundSize: '200% 100%',
              borderRadius: 4,
            }}
          />
        ))}
      </div>
    </div>
  );
}

function FetchFailureState({ reason }: { reason: string }): ReactElement {
  return (
    <div className={styles.error} role="alert">
      <h2>Couldn&apos;t load comments</h2>
      <p className="muted">
        We couldn&apos;t fetch the list from the GoNext API ({reason}).
        Try reloading; if it keeps failing the admin API may not be
        reachable yet.
      </p>
    </div>
  );
}

/**
 * Server-side fetch helper. Forwards the session cookie so the API
 * sees the operator. Returns `null` on any failure so the caller can
 * render a friendly state without crashing the layout.
 */
async function fetchInitialComments(
  params: { status?: string; postId?: string; userId?: string },
): Promise<{ data: CommentListResponse | null; error: string | null }> {
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

  const qs = new URLSearchParams();
  if (params.status) qs.set('status', params.status);
  if (params.postId) qs.set('post_id', params.postId);
  if (params.userId) qs.set('user_id', params.userId);
  qs.set('limit', '30');

  const url = `${apiBaseUrl}/api/v1/admin/comments?${qs.toString()}`;
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
    <section>
      <div className={styles.headerBar}>
        <h1>Comments</h1>
      </div>
      <Suspense fallback={<Skeleton />}>
        <CommentsListServer searchParams={props.searchParams} />
      </Suspense>
    </section>
  );
}
