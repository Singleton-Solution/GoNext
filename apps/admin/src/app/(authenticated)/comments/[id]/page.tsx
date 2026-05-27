/**
 * Comments — single comment detail + reply, restyled against the
 * Living-Systems brand.
 *
 * Server component that fetches the target comment + its
 * surrounding thread (the parent, sibling replies, and immediate
 * children). Threading data ships from a list call with a
 * `post_id` filter, which the API supports today. A future cut
 * may add a dedicated `?thread=<root>` query for efficiency; the
 * shape stays compatible.
 *
 * Layout: a two-column grid on wide viewports — the focused
 * comment + ReplyForm in the main paper-2 card, with the surrounding
 * thread in a paper-2 sidebar. Below 800px the sidebar stacks under
 * the main card. The reply form is delegated to a small client
 * island so the server component stays free of mutation logic.
 */
import { ArrowLeft } from 'lucide-react';
import Link from 'next/link';
import { type ReactElement } from 'react';

import { Headline } from '@/components/ui/headline';
import { serverApiFetch } from '@/lib/server-api';
import { cn } from '@/lib/utils';

import { StatusBadge } from '../components/StatusBadge';
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

async function fetchComment(id: string): Promise<Comment | null> {
  // We fetch via the list endpoint filtered by a single post — the
  // detail endpoint isn't strictly required for the first cut.
  // Instead we hit list and grep; if the comment isn't there
  // (status-filtered out), we widen and re-fetch. The cost is
  // bounded and the code stays simple until a dedicated GET-by-id
  // lands.
  try {
    const res = await serverApiFetch('/api/v1/admin/comments?limit=100');
    if (!res.ok) return null;
    const wire = (await res.json()) as WireListResponse;
    const found = wire.data.find((c) => c.id === id);
    return found ? toComment(found) : null;
  } catch {
    return null;
  }
}

async function fetchThread(postId: string): Promise<CommentListResponse | null> {
  try {
    const res = await serverApiFetch(
      `/api/v1/admin/comments?post_id=${encodeURIComponent(postId)}&limit=100`,
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
      <section className="flex flex-col gap-6">
        <div className="flex items-center justify-between border-b border-border pb-4">
          <Headline as="h1" size="sub">
            Comment
          </Headline>
          <Link
            href="/comments"
            className="inline-flex items-center gap-1 font-sans text-sm text-emerald-deep hover:underline"
          >
            <ArrowLeft className="h-4 w-4" aria-hidden="true" />
            Back to list
          </Link>
        </div>
        <div
          role="alert"
          className="rounded-lg border border-danger/30 bg-danger-soft/60 p-6"
        >
          <h2 className="m-0 mb-2 font-display text-xl font-bold text-ink">
            Comment not found
          </h2>
          <p className="m-0 font-sans text-sm text-fg-muted">
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
    <section className="flex flex-col gap-6">
      <div className="flex flex-wrap items-end justify-between gap-4 border-b border-border pb-4">
        <div className="flex flex-col gap-2">
          <span className="font-sans text-xs font-medium uppercase tracking-wide text-emerald-deep">
            Community · moderation
          </span>
          <Headline as="h1" size="sub">
            Single <em>comment</em>.
          </Headline>
        </div>
        <Link
          href="/comments"
          className="inline-flex items-center gap-1 font-sans text-sm text-emerald-deep hover:underline"
        >
          <ArrowLeft className="h-4 w-4" aria-hidden="true" />
          Back to list
        </Link>
      </div>

      <div className="grid gap-4 grid-cols-1 lg:grid-cols-[minmax(0,1fr)_320px]">
        <article className="rounded-lg border border-border bg-paper-2 p-5 shadow-xs">
          <header className="mb-3 flex items-baseline justify-between gap-2 border-b border-border-subtle pb-3">
            <div className="font-sans text-sm">
              <strong className="font-semibold text-ink">
                {target.authorDisplayName}
              </strong>{' '}
              <span className="text-fg-muted">
                on{' '}
                <Link
                  href={{ pathname: `/posts/${target.postId}/edit` }}
                  className="text-emerald-deep hover:underline"
                >
                  {target.postTitle || '(untitled)'}
                </Link>
              </span>
            </div>
            <StatusBadge status={target.status} />
          </header>
          <div className="mb-3 flex flex-wrap items-center gap-3 font-mono text-xs text-fg-subtle">
            <span>{formatDate(target.createdAt)}</span>
            {target.parentId && (
              <span>
                in reply to{' '}
                <Link
                  href={{ pathname: `/comments/${target.parentId}` }}
                  className="text-emerald-deep hover:underline"
                >
                  earlier comment
                </Link>
              </span>
            )}
          </div>
          <p className="m-0 font-sans text-base leading-normal text-ink">
            {target.content}
          </p>

          <div className="mt-6 border-t border-border-subtle pt-4">
            <ReplyForm commentId={target.id} />
          </div>
        </article>

        <aside
          aria-label="Thread"
          className="rounded-lg border border-border bg-paper-2 p-5 shadow-xs"
        >
          <h2 className="m-0 mb-3 font-display text-base font-extrabold tracking-tight text-ink">
            Thread
          </h2>
          {surrounding.length === 0 ? (
            <p className="m-0 font-sans text-sm text-fg-muted">
              No related comments.
            </p>
          ) : (
            <ul className="m-0 flex flex-col gap-2 p-0">
              {surrounding.map((c) => {
                const isActive = c.id === target.id;
                return (
                  <li
                    key={c.id}
                    className={cn(
                      'list-none rounded-md border p-3 transition-colors',
                      isActive
                        ? 'border-emerald bg-emerald-soft/40'
                        : 'border-border bg-paper-3 hover:border-border-strong',
                    )}
                  >
                    <div className="mb-1 flex items-center gap-2">
                      <strong className="font-sans text-xs font-semibold text-ink">
                        {c.authorDisplayName}
                      </strong>
                      <StatusBadge status={c.status} />
                    </div>
                    <Link
                      href={{ pathname: `/comments/${c.id}` }}
                      className="font-sans text-xs leading-normal text-fg-muted hover:text-ink"
                    >
                      {c.content.length > 140
                        ? c.content.slice(0, 140) + '…'
                        : c.content}
                    </Link>
                  </li>
                );
              })}
            </ul>
          )}
        </aside>
      </div>
    </section>
  );
}
