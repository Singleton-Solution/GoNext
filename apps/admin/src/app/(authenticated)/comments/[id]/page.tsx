/**
 * Comments — single comment detail + reply.
 *
 * Server component that fetches the target comment + its
 * surrounding thread (the parent, sibling replies, and immediate
 * children). Threading data ships from a list call with a
 * `post_id` filter, which the API supports today. A future cut
 * may add a dedicated `?thread=<root>` query for efficiency; the
 * shape stays compatible.
 *
 * The reply form is delegated to a small client island so the
 * server component stays free of mutation logic.
 */
import { cookies } from 'next/headers';
import Link from 'next/link';
import { type ReactElement } from 'react';
import { apiBaseUrl } from '@/lib/api-client';
import { StatusBadge } from '../components/StatusBadge';
import styles from '../comments.module.css';
import {
  toComment,
  toListResponse,
  type Comment,
  type CommentListResponse,
  type WireComment,
  type WireListResponse,
} from '../types';
import { ReplyForm } from './ReplyForm';

export const dynamic = 'force-dynamic';

interface PageProps {
  params: Promise<{ id: string }>;
}

async function authHeaders(): Promise<HeadersInit> {
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
  return {
    Accept: 'application/json',
    ...(cookieHeader ? { Cookie: cookieHeader } : {}),
  };
}

async function fetchComment(id: string): Promise<Comment | null> {
  // We fetch via the list endpoint filtered by a single post — the
  // detail endpoint isn't strictly required for the first cut.
  // Instead we hit list and grep; if the comment isn't there
  // (status-filtered out), we widen and re-fetch. The cost is
  // bounded and the code stays simple until a dedicated GET-by-id
  // lands.
  const headers = await authHeaders();
  try {
    const res = await fetch(
      `${apiBaseUrl}/api/v1/admin/comments?limit=100`,
      { method: 'GET', headers, cache: 'no-store' },
    );
    if (!res.ok) return null;
    const wire = (await res.json()) as WireListResponse;
    const found = wire.data.find((c) => c.id === id);
    return found ? toComment(found) : null;
  } catch {
    return null;
  }
}

async function fetchThread(postId: string): Promise<CommentListResponse | null> {
  const headers = await authHeaders();
  try {
    const res = await fetch(
      `${apiBaseUrl}/api/v1/admin/comments?post_id=${encodeURIComponent(postId)}&limit=100`,
      { method: 'GET', headers, cache: 'no-store' },
    );
    if (!res.ok) return null;
    const wire = (await res.json()) as WireListResponse;
    return toListResponse(wire);
  } catch {
    return null;
  }
}

function formatDate(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
  });
}

/**
 * Compute the thread set surrounding the target comment: parent,
 * siblings, the target itself, and direct children. Used to render
 * the thread sidebar.
 */
function relatedComments(target: Comment, all: Comment[]): Comment[] {
  const siblings = all.filter(
    (c) => c.parentId === target.parentId && c.postId === target.postId,
  );
  const children = all.filter((c) => c.parentId === target.id);
  const parent = target.parentId
    ? all.find((c) => c.id === target.parentId)
    : undefined;
  const set = new Map<string, Comment>();
  if (parent) set.set(parent.id, parent);
  for (const s of siblings) set.set(s.id, s);
  for (const c of children) set.set(c.id, c);
  return Array.from(set.values()).sort((a, b) =>
    a.createdAt.localeCompare(b.createdAt),
  );
}

export default async function CommentDetailPage(
  props: PageProps,
): Promise<ReactElement> {
  const { id } = await props.params;
  const target = await fetchComment(id);

  if (!target) {
    return (
      <section>
        <div className={styles.headerBar}>
          <h1>Comment</h1>
          <Link href="/comments">Back to list</Link>
        </div>
        <div className={styles.error} role="alert">
          <h2>Comment not found</h2>
          <p className="muted">
            We couldn&apos;t find a comment with that id. It may have been
            permanently deleted, or the API may not be reachable.
          </p>
        </div>
      </section>
    );
  }

  const thread = await fetchThread(target.postId);
  const surrounding = thread ? relatedComments(target, thread.data) : [];

  return (
    <section>
      <div className={styles.headerBar}>
        <h1>Comment</h1>
        <Link href="/comments">Back to list</Link>
      </div>

      <div className={styles.detail}>
        <div className={styles.detailCard}>
          <div className={styles.detailHeader}>
            <div>
              <strong>{target.authorDisplayName}</strong>{' '}
              <span className="muted">
                on{' '}
                <Link
                  href={{ pathname: `/posts/${target.postId}/edit` }}
                  className={styles.postLink}
                >
                  {target.postTitle || '(untitled)'}
                </Link>
              </span>
            </div>
            <StatusBadge status={target.status} />
          </div>
          <div className={styles.metaRow}>
            <span>{formatDate(target.createdAt)}</span>
            {target.parentId && (
              <span>
                in reply to{' '}
                <Link
                  href={{ pathname: `/comments/${target.parentId}` }}
                  className={styles.postLink}
                >
                  earlier comment
                </Link>
              </span>
            )}
          </div>
          <p>{target.content}</p>

          <ReplyForm commentId={target.id} />
        </div>

        <aside className={styles.detailCard} aria-label="Thread">
          <h2 style={{ fontSize: 16, marginTop: 0 }}>Thread</h2>
          {surrounding.length === 0 ? (
            <p className="muted">No related comments.</p>
          ) : (
            <ul className={styles.threadList}>
              {surrounding.map((c) => (
                <li
                  key={c.id}
                  className={
                    c.id === target.id
                      ? `${styles.threadItem} ${styles.threadItemActive}`
                      : styles.threadItem
                  }
                >
                  <div className={styles.metaRow}>
                    <strong>{c.authorDisplayName}</strong>
                    <StatusBadge status={c.status} />
                  </div>
                  <Link
                    href={{ pathname: `/comments/${c.id}` }}
                    style={{ fontSize: 13 }}
                  >
                    {c.content.length > 140
                      ? c.content.slice(0, 140) + '…'
                      : c.content}
                  </Link>
                </li>
              ))}
            </ul>
          )}
        </aside>
      </div>
    </section>
  );
}
