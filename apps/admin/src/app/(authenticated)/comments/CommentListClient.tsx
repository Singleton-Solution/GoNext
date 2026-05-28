'use client';

/**
 * Client island for the Comments list, restyled against the
 * Living-Systems brand.
 *
 * Mirrors the PostListClient pattern: a server component fetches the
 * first page and hands it to this island, which owns:
 *
 *  - Status filter chips (URL-synced via `?status=pending|approved|spam|trash`).
 *  - Row selection + bulk action bar (Approve / Spam / Trash).
 *  - Per-row quick actions (Approve / Spam / Trash buttons that hit
 *    the single-row PATCH endpoint).
 *  - "Load more" pagination via the API's opaque next_cursor.
 *
 * Visual treatment: emerald-soft active chips + paper-3 inactive
 * chips, paper-2 table chrome with paper-3 head row, lavender
 * avatar circles, monospace timestamps. The quick-action row uses
 * the shared `<Button>` ghost variant so the row's CTA cluster
 * reads as auxiliary rather than competing with the bulk bar.
 *
 * REST dependency: `/api/v1/admin/comments` is the freshly-landed
 * endpoint in `apps/api/internal/admin/comments`. The component is
 * defensive against fetch failures so a backend outage degrades to
 * "couldn't load more" inline messages rather than a white page.
 */
import Link from 'next/link';
import { usePathname, useRouter, useSearchParams } from 'next/navigation';
import {
  useCallback,
  useEffect,
  useMemo,
  useState,
  useTransition,
  type ReactElement,
} from 'react';

import { Button } from '@/components/ui/button';
import { api, ApiError } from '@/lib/api-client';
import { cn } from '@/lib/utils';

import { postEditHref } from '../posts/columns';
import { BulkActionBar } from './components/BulkActionBar';
import { StatusBadge } from './components/StatusBadge';
import {
  excerpt,
  STATUS_FILTERS,
  toListResponse,
  type BulkAction,
  type Comment,
  type CommentListResponse,
  type CommentStatus,
  type WireComment,
  type WireListResponse,
} from './types';

export interface CommentListClientProps {
  initialData: CommentListResponse;
  /** Fetch override used by tests to avoid real network. */
  fetcher?: (query: string) => Promise<CommentListResponse>;
  /** PATCH override used by tests to avoid real network. */
  patcher?: (id: string, status: CommentStatus) => Promise<Comment>;
  /** Bulk override used by tests to avoid real network. */
  bulker?: (ids: string[], action: BulkAction) => Promise<Comment[]>;
}

const QUICK_ACTIONS: readonly { label: string; status: CommentStatus }[] = [
  { label: 'Approve', status: 'approved' },
  { label: 'Spam', status: 'spam' },
  { label: 'Trash', status: 'trash' },
];

function avatarInitial(name: string): string {
  const trimmed = name.trim();
  if (trimmed === '') return '?';
  return trimmed[0]!.toUpperCase();
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

export function CommentListClient({
  initialData,
  fetcher,
  patcher,
  bulker,
}: CommentListClientProps): ReactElement {
  const router = useRouter();
  const pathname = usePathname() ?? '/comments';
  const searchParams = useSearchParams();

  const currentStatus = (searchParams?.get('status') ?? 'any') as
    | 'any'
    | CommentStatus;

  const [comments, setComments] = useState<Comment[]>(initialData.data);
  const [cursor, setCursor] = useState<string>(
    initialData.pagination.nextCursor,
  );
  const [selected, setSelected] = useState<ReadonlySet<string>>(new Set());
  const [loadMoreError, setLoadMoreError] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [isPending, startTransition] = useTransition();
  const [isLoadingMore, setIsLoadingMore] = useState(false);
  const [isBulkPending, setIsBulkPending] = useState(false);
  const [busyId, setBusyId] = useState<string | null>(null);

  // Reset paginated state when the underlying server fetch
  // returns a new initial page (filter change navigation).
  useEffect(() => {
    setComments(initialData.data);
    setCursor(initialData.pagination.nextCursor);
    setSelected(new Set());
    setLoadMoreError(null);
    setActionError(null);
  }, [initialData]);

  /** Push a URL change with merged search params. */
  const pushParams = useCallback(
    (mutate: (params: URLSearchParams) => void): void => {
      const next = new URLSearchParams(searchParams?.toString() ?? '');
      mutate(next);
      const query = next.toString();
      const url = query ? `${pathname}?${query}` : pathname;
      startTransition(() => {
        router.push(url);
      });
    },
    [pathname, router, searchParams],
  );

  const handleStatusClick = useCallback(
    (value: 'any' | CommentStatus): void => {
      pushParams((params) => {
        if (value === 'any') {
          params.delete('status');
        } else {
          params.set('status', value);
        }
      });
    },
    [pushParams],
  );

  const handleSelectAll = useCallback(
    (checked: boolean): void => {
      if (checked) {
        setSelected(new Set(comments.map((c) => c.id)));
      } else {
        setSelected(new Set());
      }
    },
    [comments],
  );

  const handleSelectRow = useCallback((id: string): void => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) {
        next.delete(id);
      } else {
        next.add(id);
      }
      return next;
    });
  }, []);

  const applyPatch = useCallback(
    async (id: string, status: CommentStatus): Promise<void> => {
      setBusyId(id);
      setActionError(null);
      try {
        const updated = patcher
          ? await patcher(id, status)
          : await api
              .patch<WireComment>(`/api/v1/admin/comments/${id}`, {
                status,
              })
              .then((w) => ({
                id: w.id,
                postId: w.post_id,
                postTitle: w.post_title,
                parentId: w.parent_id,
                path: w.path,
                authorUserId: w.author_user_id,
                authorDisplayName: w.author_display_name,
                content: w.content,
                contentFormat: w.content_format,
                status: w.status,
                createdAt: w.created_at,
                updatedAt: w.updated_at,
              }));
        setComments((prev) =>
          prev.map((c) => (c.id === id ? { ...c, ...updated } : c)),
        );
      } catch (err) {
        const msg =
          err instanceof ApiError
            ? `Action failed (HTTP ${err.status})`
            : 'Action failed';
        setActionError(msg);
      } finally {
        setBusyId(null);
      }
    },
    [patcher],
  );

  const applyBulk = useCallback(
    async (action: BulkAction): Promise<void> => {
      const ids = Array.from(selected);
      if (ids.length === 0) return;
      setIsBulkPending(true);
      setActionError(null);
      try {
        const updated = bulker
          ? await bulker(ids, action)
          : await api
              .post<{ updated: WireComment[]; count: number }>(
                '/api/v1/admin/comments/bulk',
                { ids, action },
              )
              .then((res) =>
                res.updated.map((w) => ({
                  id: w.id,
                  postId: w.post_id,
                  postTitle: w.post_title,
                  parentId: w.parent_id,
                  path: w.path,
                  authorUserId: w.author_user_id,
                  authorDisplayName: w.author_display_name,
                  content: w.content,
                  contentFormat: w.content_format,
                  status: w.status,
                  createdAt: w.created_at,
                  updatedAt: w.updated_at,
                })),
              );
        const byId = new Map(updated.map((c) => [c.id, c] as const));
        setComments((prev) =>
          prev.map((c) => (byId.has(c.id) ? byId.get(c.id)! : c)),
        );
        setSelected(new Set());
      } catch (err) {
        const msg =
          err instanceof ApiError
            ? `Bulk failed (HTTP ${err.status})`
            : 'Bulk failed';
        setActionError(msg);
      } finally {
        setIsBulkPending(false);
      }
    },
    [bulker, selected],
  );

  const handleLoadMore = useCallback(async (): Promise<void> => {
    if (!cursor) return;
    setIsLoadingMore(true);
    setLoadMoreError(null);
    try {
      const params = new URLSearchParams(searchParams?.toString() ?? '');
      params.set('cursor', cursor);
      const query = params.toString();
      const next = fetcher
        ? await fetcher(query)
        : toListResponse(
            await api.get<WireListResponse>(`/api/v1/admin/comments?${query}`),
          );
      setComments((prev) => [...prev, ...next.data]);
      setCursor(next.pagination.nextCursor);
    } catch (err) {
      const msg =
        err instanceof ApiError
          ? `Couldn't load more (HTTP ${err.status})`
          : "Couldn't load more comments";
      setLoadMoreError(msg);
    } finally {
      setIsLoadingMore(false);
    }
  }, [cursor, fetcher, searchParams]);

  const allSelected = useMemo(
    () => comments.length > 0 && selected.size === comments.length,
    [comments.length, selected.size],
  );

  if (comments.length === 0) {
    const hasFilter = currentStatus !== 'any';
    return (
      <div className="flex flex-col gap-4">
        <CommentsToolbar
          currentStatus={currentStatus}
          onStatusClick={handleStatusClick}
          selectedCount={0}
          onApplyBulk={applyBulk}
          isBulkPending={isBulkPending}
          isPending={isPending}
        />
        <div
          role="status"
          className="rounded-lg border border-dashed border-border bg-paper-2 px-8 py-12 text-center"
        >
          {hasFilter ? (
            <>
              <h2 className="m-0 mb-2 font-display text-xl font-bold text-ink">
                No comments match that filter
              </h2>
              <p className="m-0 font-sans text-sm text-fg-muted">
                Try a different status, or clear the filter.
              </p>
            </>
          ) : (
            <>
              <h2 className="m-0 mb-2 font-display text-xl font-bold text-ink">
                No comments <em className="font-serif italic font-normal text-emerald-deep">yet</em>.
              </h2>
              <p className="m-0 font-sans text-sm text-fg-muted">
                When your posts attract conversation, it shows up here.
              </p>
            </>
          )}
        </div>
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-4">
      <CommentsToolbar
        currentStatus={currentStatus}
        onStatusClick={handleStatusClick}
        selectedCount={selected.size}
        onApplyBulk={applyBulk}
        isBulkPending={isBulkPending}
        isPending={isPending}
      />

      {actionError && (
        <p role="alert" className="m-0 font-sans text-sm text-danger">
          {actionError}
        </p>
      )}

      <div className="overflow-hidden rounded-lg border border-border bg-paper-2 shadow-xs">
        <table className="w-full border-collapse font-sans text-sm">
          <thead className="bg-paper-3">
            <tr>
              <th scope="col" className="border-b border-border px-3 py-2 text-left" style={{ width: 32 }}>
                <input
                  type="checkbox"
                  aria-label="Select all comments"
                  checked={allSelected}
                  onChange={(e) => handleSelectAll(e.target.checked)}
                  className="accent-emerald"
                />
              </th>
              <th
                scope="col"
                className="border-b border-border px-3 py-2 text-left"
                style={{ width: 48 }}
              >
                <span className="sr-only">Avatar</span>
              </th>
              <th
                scope="col"
                className="border-b border-border px-3 py-2 text-left font-mono text-[10px] font-semibold uppercase tracking-wide text-fg-subtle"
              >
                Author
              </th>
              <th
                scope="col"
                className="border-b border-border px-3 py-2 text-left font-mono text-[10px] font-semibold uppercase tracking-wide text-fg-subtle"
              >
                Comment
              </th>
              <th
                scope="col"
                className="border-b border-border px-3 py-2 text-left font-mono text-[10px] font-semibold uppercase tracking-wide text-fg-subtle"
              >
                On post
              </th>
              <th
                scope="col"
                className="border-b border-border px-3 py-2 text-left font-mono text-[10px] font-semibold uppercase tracking-wide text-fg-subtle"
              >
                Status
              </th>
              <th
                scope="col"
                className="border-b border-border px-3 py-2 text-left font-mono text-[10px] font-semibold uppercase tracking-wide text-fg-subtle"
              >
                Date
              </th>
              <th
                scope="col"
                className="border-b border-border px-3 py-2 text-left font-mono text-[10px] font-semibold uppercase tracking-wide text-fg-subtle"
              >
                Actions
              </th>
            </tr>
          </thead>
          <tbody>
            {comments.map((c, i) => (
              <tr
                key={c.id}
                className={cn(
                  'transition-colors hover:bg-paper-3',
                  i === comments.length - 1
                    ? ''
                    : 'border-b border-border-subtle',
                )}
              >
                <td className="px-3 py-3 align-top">
                  <input
                    type="checkbox"
                    aria-label={`Select comment by ${c.authorDisplayName}`}
                    checked={selected.has(c.id)}
                    onChange={() => handleSelectRow(c.id)}
                    className="accent-emerald"
                  />
                </td>
                <td className="px-3 py-3 align-top">
                  <span
                    aria-hidden="true"
                    className="inline-flex h-8 w-8 items-center justify-center rounded-pill border border-border bg-lavender-soft font-display text-xs font-bold text-lavender-deep"
                  >
                    {avatarInitial(c.authorDisplayName)}
                  </span>
                </td>
                <td className="px-3 py-3 align-top font-sans text-sm text-ink">
                  {c.authorDisplayName}
                </td>
                <td className="max-w-[360px] px-3 py-3 align-top">
                  <Link
                    href={{ pathname: `/comments/${c.id}` }}
                    className="font-sans text-sm leading-normal text-ink underline-offset-2 hover:underline hover:text-emerald-deep"
                  >
                    {excerpt(c.content, 200)}
                  </Link>
                </td>
                <td className="px-3 py-3 align-top">
                  <Link
                    href={postEditHref(c.postId)}
                    className="font-sans text-xs text-emerald-deep hover:underline"
                  >
                    {c.postTitle || '(untitled)'}
                  </Link>
                </td>
                <td className="px-3 py-3 align-top">
                  <StatusBadge status={c.status} />
                </td>
                <td className="px-3 py-3 align-top font-mono text-xs tabular-nums text-fg-muted">
                  {formatDate(c.createdAt)}
                </td>
                <td className="px-3 py-3 align-top">
                  <div className="flex flex-wrap gap-1">
                    {QUICK_ACTIONS.map((a) => (
                      <Button
                        key={a.status}
                        type="button"
                        variant="ghost"
                        size="sm"
                        onClick={() => void applyPatch(c.id, a.status)}
                        disabled={busyId === c.id || c.status === a.status}
                        aria-label={`${a.label} comment by ${c.authorDisplayName}`}
                        className="h-7 px-2 font-sans text-xs"
                      >
                        {a.label}
                      </Button>
                    ))}
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {loadMoreError && (
        <p role="alert" className="m-0 font-sans text-sm text-danger">
          {loadMoreError}
        </p>
      )}

      {cursor && (
        <div className="flex justify-center">
          <Button
            type="button"
            variant="default"
            size="sm"
            onClick={handleLoadMore}
            disabled={isLoadingMore}
          >
            {isLoadingMore ? 'Loading…' : 'Load more'}
          </Button>
        </div>
      )}
    </div>
  );
}

interface CommentsToolbarProps {
  currentStatus: 'any' | CommentStatus;
  onStatusClick: (value: 'any' | CommentStatus) => void;
  selectedCount: number;
  onApplyBulk: (action: BulkAction) => void | Promise<void>;
  isBulkPending: boolean;
  isPending: boolean;
}

function CommentsToolbar({
  currentStatus,
  onStatusClick,
  selectedCount,
  onApplyBulk,
  isBulkPending,
  isPending,
}: CommentsToolbarProps): ReactElement {
  return (
    <div
      className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-border bg-paper-2 p-3 shadow-xs"
      aria-busy={isPending ? 'true' : 'false'}
    >
      <div
        className="inline-flex flex-wrap gap-2"
        role="group"
        aria-label="Filter by status"
      >
        {STATUS_FILTERS.map((f) => {
          const active = currentStatus === f.value;
          return (
            <button
              key={f.value}
              type="button"
              aria-pressed={active}
              onClick={() => onStatusClick(f.value)}
              className={cn(
                'rounded-pill border px-3 py-[3px] font-sans text-xs font-medium transition-colors duration-[160ms] ease-brand',
                active
                  ? 'border-emerald bg-emerald-soft text-emerald-deep shadow-xs'
                  : 'border-border bg-transparent text-fg-muted hover:border-emerald hover:bg-emerald-soft/40 hover:text-emerald-deep',
              )}
            >
              {f.label}
            </button>
          );
        })}
      </div>
      <BulkActionBar
        selectedCount={selectedCount}
        onApply={onApplyBulk}
        isPending={isBulkPending}
      />
    </div>
  );
}
