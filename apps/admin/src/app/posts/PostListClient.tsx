'use client';

/**
 * Client island for the Posts list.
 *
 * Receives the server-fetched first page as `initialData` and takes over
 * for all interactive behaviour:
 *
 *  - Sort columns (click header — URL-synced via `?sort=-date`)
 *  - Status filter chips (URL-synced via `?status=draft`)
 *  - Debounced search box (URL-synced via `?q=hello`)
 *  - Row-selection + bulk action dropdown (Trash / Restore — stubbed for
 *    now per acceptance criteria, both just log to console).
 *  - Pagination via "Load more" — chosen over numbered pages because it
 *    composes naturally with cursor-based pagination and is easier to
 *    skeleton-load. Numbered pages can be added later if power users
 *    need direct page jumps.
 *
 * Why the URL is the source of truth: docs/05-admin-api.md §1 mandates
 * deep-linkable list views. Reloading the page on `?status=draft&q=foo`
 * must restore the same view; pushing back/forward should navigate the
 * filter history.
 *
 * The component avoids the dual-source-of-truth trap by deriving the
 * draft search input value from local state (so typing feels instant)
 * and committing it to the URL via a 250ms debounce.
 *
 * REST dependency: this component reaches `/api/v1/posts` for the
 * "load more" path. If issue #76 hasn't landed yet, the catch block
 * shows an inline "Couldn't load more" message rather than crashing.
 */
import Link from 'next/link';
import { usePathname, useRouter, useSearchParams } from 'next/navigation';
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  useTransition,
  type ChangeEvent,
  type ReactElement,
} from 'react';
import { api, ApiError } from '../api-client';
import {
  POST_COLUMNS,
  STATUS_FILTERS,
  StatusBadge,
  formatDate,
  parseSort,
  postEditHref,
  serializeSort,
  type Post,
  type PostListResponse,
  type PostStatus,
  type SortField,
} from './columns';
import styles from './posts.module.css';

/** Debounce window for the search box. Short enough to feel responsive. */
const SEARCH_DEBOUNCE_MS = 250;

export interface PostListClientProps {
  initialData: PostListResponse;
  /** Optional fetch override — used by tests to avoid real network. */
  fetcher?: (query: string) => Promise<PostListResponse>;
}

export function PostListClient({
  initialData,
  fetcher,
}: PostListClientProps): ReactElement {
  const router = useRouter();
  const pathname = usePathname() ?? '/posts';
  const searchParams = useSearchParams();

  const currentStatus = (searchParams?.get('status') ?? 'any') as
    | 'any'
    | PostStatus;
  const currentQuery = searchParams?.get('q') ?? '';
  const currentSort = parseSort(searchParams?.get('sort') ?? null);

  // Local mirror of the search input so typing is instant. We push the
  // value into the URL on a trailing 250ms debounce.
  const [searchDraft, setSearchDraft] = useState(currentQuery);
  // Keep the input in sync if the URL changes from elsewhere (back/forward).
  useEffect(() => {
    setSearchDraft(currentQuery);
  }, [currentQuery]);

  // Posts are sorted/filtered server-side; client only handles the
  // "load more" append and the optimistic selection state. The initial
  // page is whatever the server delivered.
  const [posts, setPosts] = useState<Post[]>(initialData.posts);
  const [cursor, setCursor] = useState<string | null>(initialData.nextCursor);
  const [loadMoreError, setLoadMoreError] = useState<string | null>(null);
  const [isPending, startTransition] = useTransition();
  const [isLoadingMore, setIsLoadingMore] = useState(false);

  // Selection state — keys are post ids.
  const [selected, setSelected] = useState<ReadonlySet<string>>(new Set());
  const [bulkAction, setBulkAction] = useState<'trash' | 'restore' | ''>('');

  // Reset paginated state whenever the filter/sort/search change in the URL.
  useEffect(() => {
    setPosts(initialData.posts);
    setCursor(initialData.nextCursor);
    setSelected(new Set());
    setLoadMoreError(null);
  }, [initialData]);

  /**
   * Push the next URL state. Always uses replaceState semantics for
   * search box typing (we don't want every keystroke in history) and
   * pushState for explicit user actions like filter clicks.
   */
  const pushParams = useCallback(
    (
      mutate: (params: URLSearchParams) => void,
      options: { replace?: boolean } = {},
    ) => {
      const next = new URLSearchParams(searchParams?.toString() ?? '');
      mutate(next);
      const query = next.toString();
      const url = query ? `${pathname}?${query}` : pathname;
      startTransition(() => {
        if (options.replace) {
          router.replace(url);
        } else {
          router.push(url);
        }
      });
    },
    [pathname, router, searchParams],
  );

  // Debounced URL commit for the search box.
  const searchDebounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(() => {
    return () => {
      if (searchDebounceRef.current) {
        clearTimeout(searchDebounceRef.current);
      }
    };
  }, []);

  const handleSearchChange = useCallback(
    (event: ChangeEvent<HTMLInputElement>): void => {
      const value = event.target.value;
      setSearchDraft(value);
      if (searchDebounceRef.current) {
        clearTimeout(searchDebounceRef.current);
      }
      searchDebounceRef.current = setTimeout(() => {
        pushParams(
          (params) => {
            if (value) {
              params.set('q', value);
            } else {
              params.delete('q');
            }
          },
          { replace: true },
        );
      }, SEARCH_DEBOUNCE_MS);
    },
    [pushParams],
  );

  const handleStatusClick = useCallback(
    (value: 'any' | PostStatus): void => {
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

  const handleSortClick = useCallback(
    (field: SortField): void => {
      pushParams((params) => {
        const next =
          currentSort && currentSort.field === field
            ? {
                field,
                direction:
                  currentSort.direction === 'asc'
                    ? ('desc' as const)
                    : ('asc' as const),
              }
            : { field, direction: 'asc' as const };
        params.set('sort', serializeSort(next));
      });
    },
    [currentSort, pushParams],
  );

  const handleSelectAll = useCallback(
    (event: ChangeEvent<HTMLInputElement>): void => {
      if (event.target.checked) {
        setSelected(new Set(posts.map((p) => p.id)));
      } else {
        setSelected(new Set());
      }
    },
    [posts],
  );

  const handleSelectRow = useCallback(
    (id: string): void => {
      setSelected((prev) => {
        const next = new Set(prev);
        if (next.has(id)) {
          next.delete(id);
        } else {
          next.add(id);
        }
        return next;
      });
    },
    [],
  );

  const handleApplyBulk = useCallback((): void => {
    if (!bulkAction || selected.size === 0) return;
    // Stubbed per acceptance criteria — real wiring lands once the
    // bulk-action API (doc 05 §2.3) is implemented. We log so it's
    // visible in dev tools and obvious in tests.
    // eslint-disable-next-line no-console
    console.log('[posts] bulk action', bulkAction, Array.from(selected));
  }, [bulkAction, selected]);

  const handleLoadMore = useCallback(async (): Promise<void> => {
    if (!cursor) return;
    setIsLoadingMore(true);
    setLoadMoreError(null);
    try {
      const params = new URLSearchParams(searchParams?.toString() ?? '');
      params.set('limit', '20');
      params.set('after', cursor);
      const query = params.toString();
      const next = fetcher
        ? await fetcher(query)
        : await api.get<PostListResponse>(`/api/v1/posts?${query}`);
      setPosts((prev) => [...prev, ...next.posts]);
      setCursor(next.nextCursor);
    } catch (err) {
      const msg =
        err instanceof ApiError
          ? `Couldn't load more (HTTP ${err.status})`
          : "Couldn't load more posts";
      setLoadMoreError(msg);
    } finally {
      setIsLoadingMore(false);
    }
  }, [cursor, fetcher, searchParams]);

  const allSelected = useMemo(
    () => posts.length > 0 && selected.size === posts.length,
    [posts.length, selected.size],
  );

  // Empty state — server returned no posts and the user has no active
  // filter narrowing things. We render the "no posts yet" CTA so first
  // run feels considered. If the user *does* have a filter, we show a
  // friendlier "no matches" instead.
  if (posts.length === 0) {
    const hasFilter = currentQuery !== '' || currentStatus !== 'any';
    return (
      <div>
        <PostsToolbar
          searchDraft={searchDraft}
          currentStatus={currentStatus}
          onSearchChange={handleSearchChange}
          onStatusClick={handleStatusClick}
          bulkAction={bulkAction}
          onBulkActionChange={(v) => setBulkAction(v)}
          onApplyBulk={handleApplyBulk}
          selectedCount={0}
          isPending={isPending}
        />
        <div className={styles.empty} role="status">
          {hasFilter ? (
            <>
              <h2>No posts match those filters</h2>
              <p>Try a different search term or clear the status filter.</p>
            </>
          ) : (
            <>
              <h2>No posts yet</h2>
              <p>Start by creating your first post.</p>
              <Link
                href={{ pathname: '/posts/new' }}
                className={styles.primaryAction}
              >
                Create your first
              </Link>
            </>
          )}
        </div>
      </div>
    );
  }

  return (
    <div>
      <PostsToolbar
        searchDraft={searchDraft}
        currentStatus={currentStatus}
        onSearchChange={handleSearchChange}
        onStatusClick={handleStatusClick}
        bulkAction={bulkAction}
        onBulkActionChange={(v) => setBulkAction(v)}
        onApplyBulk={handleApplyBulk}
        selectedCount={selected.size}
        isPending={isPending}
      />

      <div className={styles.tableWrap}>
        <table className={styles.table} aria-label="Posts">
          <thead>
            <tr>
              <th scope="col" style={{ width: 32 }}>
                <input
                  type="checkbox"
                  aria-label="Select all posts"
                  checked={allSelected}
                  onChange={handleSelectAll}
                />
              </th>
              {POST_COLUMNS.map((col) => (
                <th key={col.id} scope="col">
                  {col.sortField ? (
                    <button
                      type="button"
                      className={styles.sortButton}
                      onClick={() => handleSortClick(col.sortField as SortField)}
                      aria-label={`Sort by ${col.label}`}
                    >
                      {col.label}
                      {currentSort?.field === col.sortField && (
                        <span
                          className={styles.sortArrow}
                          aria-hidden="true"
                        >
                          {currentSort.direction === 'asc' ? '↑' : '↓'}
                        </span>
                      )}
                    </button>
                  ) : (
                    col.label
                  )}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {posts.map((post) => (
              <tr key={post.id}>
                <td>
                  <input
                    type="checkbox"
                    aria-label={`Select ${post.title}`}
                    checked={selected.has(post.id)}
                    onChange={() => handleSelectRow(post.id)}
                  />
                </td>
                <td className={styles.titleCell}>
                  <Link
                    href={{ pathname: postEditHref(post.id) }}
                  >
                    {post.title || '(untitled)'}
                  </Link>
                </td>
                <td>{post.author.displayName}</td>
                <td>
                  <StatusBadge status={post.status} />
                </td>
                <td>{formatDate(post.date)}</td>
                <td>{post.commentsCount}</td>
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

interface PostsToolbarProps {
  searchDraft: string;
  currentStatus: 'any' | PostStatus;
  onSearchChange: (event: ChangeEvent<HTMLInputElement>) => void;
  onStatusClick: (value: 'any' | PostStatus) => void;
  bulkAction: 'trash' | 'restore' | '';
  onBulkActionChange: (value: 'trash' | 'restore' | '') => void;
  onApplyBulk: () => void;
  selectedCount: number;
  isPending: boolean;
}

function PostsToolbar({
  searchDraft,
  currentStatus,
  onSearchChange,
  onStatusClick,
  bulkAction,
  onBulkActionChange,
  onApplyBulk,
  selectedCount,
  isPending,
}: PostsToolbarProps): ReactElement {
  return (
    <div
      className={styles.toolbar}
      aria-busy={isPending ? 'true' : 'false'}
    >
      <input
        type="search"
        className={styles.search}
        placeholder="Search posts"
        aria-label="Search posts"
        value={searchDraft}
        onChange={onSearchChange}
      />
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
      <div className={styles.bulkBar}>
        <label htmlFor="bulk-action" className="muted">
          Bulk:
        </label>
        <select
          id="bulk-action"
          className={styles.bulkSelect}
          value={bulkAction}
          onChange={(e) =>
            onBulkActionChange(e.target.value as 'trash' | 'restore' | '')
          }
        >
          <option value="">Choose…</option>
          <option value="trash">Move to Trash</option>
          <option value="restore">Restore</option>
        </select>
        <button
          type="button"
          className={styles.bulkApply}
          onClick={onApplyBulk}
          disabled={!bulkAction || selectedCount === 0}
        >
          Apply{selectedCount > 0 ? ` (${selectedCount})` : ''}
        </button>
      </div>
    </div>
  );
}
