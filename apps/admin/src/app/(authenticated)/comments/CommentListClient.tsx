'use client';

/**
 * Client island for the Comments list.
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
import { api, ApiError } from '@/lib/api-client';
import { BulkActionBar } from './components/BulkActionBar';
import { StatusBadge } from './components/StatusBadge';
import styles from './comments.module.css';
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
          : (await api.patch<WireComment>(`/api/v1/admin/comments/${id}`, {
              status,
            }).then((w) => ({
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
            })));
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
          : (await api
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
              ));
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
      <div>
        <CommentsToolbar
          currentStatus={currentStatus}
          onStatusClick={handleStatusClick}
          selectedCount={0}
          onApplyBulk={applyBulk}
          isBulkPending={isBulkPending}
          isPending={isPending}
        />
        <div className={styles.empty} role="status">
          {hasFilter ? (
            <>
              <h2>No comments match that filter</h2>
              <p>Try a different status, or clear the filter.</p>
            </>
          ) : (
            <>
              <h2>No comments yet</h2>
              <p>When your posts attract conversation, it shows up here.</p>
            </>
          )}
        </div>
      </div>
    );
  }

  return (
    <div>
      <CommentsToolbar
        currentStatus={currentStatus}
        onStatusClick={handleStatusClick}
        selectedCount={selected.size}
        onApplyBulk={applyBulk}
        isBulkPending={isBulkPending}
        isPending={isPending}
      />

      {actionError && (
        <p className="muted" role="alert" style={{ marginBottom: 12 }}>
          {actionError}
        </p>
      )}

      <div className={styles.tableWrap}>
        <table className={styles.table} aria-label="Comments">
          <thead>
            <tr>
              <th scope="col" style={{ width: 32 }}>
                <input
                  type="checkbox"
                  aria-label="Select all comments"
                  checked={allSelected}
                  onChange={(e) => handleSelectAll(e.target.checked)}
                />
              </th>
              <th scope="col" style={{ width: 48 }}>
                <span className="visually-hidden">Avatar</span>
              </th>
              <th scope="col">Author</th>
              <th scope="col">Comment</th>
              <th scope="col">On post</th>
              <th scope="col">Status</th>
              <th scope="col">Date</th>
              <th scope="col">Actions</th>
            </tr>
          </thead>
          <tbody>
            {comments.map((c) => (
              <tr key={c.id}>
                <td>
                  <input
                    type="checkbox"
                    aria-label={`Select comment by ${c.authorDisplayName}`}
                    checked={selected.has(c.id)}
                    onChange={() => handleSelectRow(c.id)}
                  />
                </td>
                <td>
                  <span className={styles.avatar} aria-hidden="true">
                    {avatarInitial(c.authorDisplayName)}
                  </span>
                </td>
                <td>{c.authorDisplayName}</td>
                <td className={styles.excerpt}>
                  <Link href={{ pathname: `/comments/${c.id}` }}>
                    {excerpt(c.content, 200)}
                  </Link>
                </td>
                <td>
                  <Link
                    href={{ pathname: `/posts/${c.postId}/edit` }}
                    className={styles.postLink}
                  >
                    {c.postTitle || '(untitled)'}
                  </Link>
                </td>
                <td>
                  <StatusBadge status={c.status} />
                </td>
                <td>{formatDate(c.createdAt)}</td>
                <td>
                  <div className={styles.quickActions}>
                    {QUICK_ACTIONS.map((a) => (
                      <button
                        key={a.status}
                        type="button"
                        className={styles.quickAction}
                        onClick={() => void applyPatch(c.id, a.status)}
                        disabled={busyId === c.id || c.status === a.status}
                        aria-label={`${a.label} comment by ${c.authorDisplayName}`}
                      >
                        {a.label}
                      </button>
                    ))}
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {loadMoreError && (
        <p className="muted" role="alert" style={{ marginTop: 12 }}>
          {loadMoreError}
        </p>
      )}

      {cursor && (
        <div className={styles.loadMoreWrap}>
          <button
            type="button"
            className={styles.bulkApply}
            onClick={handleLoadMore}
            disabled={isLoadingMore}
          >
            {isLoadingMore ? 'Loading…' : 'Load more'}
          </button>
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
      className={styles.toolbar}
      aria-busy={isPending ? 'true' : 'false'}
    >
      <div
        className={styles.chipGroup}
        role="group"
        aria-label="Filter by status"
      >
        {STATUS_FILTERS.map((f) => {
          const active = currentStatus === f.value;
          return (
            <button
              key={f.value}
              type="button"
              className={
                active ? `${styles.chip} ${styles.chipActive}` : styles.chip
              }
              aria-pressed={active}
              onClick={() => onStatusClick(f.value)}
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
