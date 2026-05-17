/**
 * Posts list — column definitions and shared types.
 *
 * Keeping the column descriptors out of the client component makes them
 * easy to unit test in isolation and gives us a single place to add new
 * columns when plugin-registered columns (doc 05 §2.3) ship later.
 *
 * The `Post` shape mirrors the REST contract sketched in
 * `docs/05-admin-api.md` §3.1 (sparse fieldset projection). The fields we
 * read are the minimum set the list screen needs — title, author display
 * name, status, dates, and a comments aggregate. Anything richer is
 * deferred to the edit screen.
 */
import type { ReactElement } from 'react';
import styles from './posts.module.css';

/** Canonical post status set used across the admin. */
export type PostStatus =
  | 'publish'
  | 'draft'
  | 'pending'
  | 'private'
  | 'future'
  | 'trash';

/** Shape of a single post row as returned by `/api/v1/posts`. */
export interface Post {
  id: string;
  title: string;
  status: PostStatus;
  /** ISO8601 timestamp — either published_at or modified_at, server's choice. */
  date: string;
  author: {
    id: string;
    displayName: string;
  };
  commentsCount: number;
}

/** Shape of the list response — `posts` + a cursor for "load more". */
export interface PostListResponse {
  posts: Post[];
  /** When present, more results are available; pass back as `after=<cursor>`. */
  nextCursor: string | null;
  /** Total count of matching rows; used for display only. */
  total: number;
}

/** Sortable fields the API accepts in `?sort=`. */
export type SortField = 'title' | 'date' | 'author' | 'comments';
export type SortDirection = 'asc' | 'desc';

export interface SortSpec {
  field: SortField;
  direction: SortDirection;
}

/** Parse a `?sort=-date` style query value into a SortSpec. */
export function parseSort(raw: string | null): SortSpec | null {
  if (!raw) return null;
  const direction: SortDirection = raw.startsWith('-') ? 'desc' : 'asc';
  const field = raw.startsWith('-') ? raw.slice(1) : raw;
  if (
    field === 'title' ||
    field === 'date' ||
    field === 'author' ||
    field === 'comments'
  ) {
    return { field, direction };
  }
  return null;
}

/** Serialise a SortSpec back into the `?sort=-date` form. */
export function serializeSort(spec: SortSpec): string {
  return spec.direction === 'desc' ? `-${spec.field}` : spec.field;
}

/**
 * Status badge — small coloured pill that maps a status string onto an
 * accessible label + visual treatment.
 */
export function StatusBadge({ status }: { status: PostStatus }): ReactElement {
  const className =
    status === 'publish'
      ? `${styles.badge} ${styles.badgePublished}`
      : status === 'trash'
        ? `${styles.badge} ${styles.badgeTrash}`
        : status === 'draft' || status === 'pending'
          ? `${styles.badge} ${styles.badgeDraft}`
          : styles.badge;

  const label =
    status === 'publish'
      ? 'Published'
      : status === 'draft'
        ? 'Draft'
        : status === 'pending'
          ? 'Pending'
          : status === 'private'
            ? 'Private'
            : status === 'future'
              ? 'Scheduled'
              : 'Trash';

  return (
    <span className={className} aria-label={`Status: ${label}`}>
      {label}
    </span>
  );
}

/**
 * Format an ISO date for the table. Uses `toLocaleDateString` so the user
 * sees their own locale; falls back to the raw value on parse error.
 */
export function formatDate(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleDateString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
  });
}

/**
 * Build the `/posts/{id}/edit` href used by the title cell. Centralised so
 * a route move only touches one file.
 */
export function postEditHref(id: string): string {
  return `/posts/${encodeURIComponent(id)}/edit`;
}

/** Column id, header label, and whether it's sortable. */
export interface ColumnSpec {
  id: 'title' | 'author' | 'status' | 'date' | 'comments';
  label: string;
  sortField: SortField | null;
}

export const POST_COLUMNS: readonly ColumnSpec[] = [
  { id: 'title', label: 'Title', sortField: 'title' },
  { id: 'author', label: 'Author', sortField: 'author' },
  { id: 'status', label: 'Status', sortField: null },
  { id: 'date', label: 'Date', sortField: 'date' },
  { id: 'comments', label: 'Comments', sortField: 'comments' },
];

/** Filter-chip definitions; URL-synced via `?status=`. */
export interface StatusFilter {
  value: 'any' | PostStatus;
  label: string;
}

export const STATUS_FILTERS: readonly StatusFilter[] = [
  { value: 'any', label: 'All' },
  { value: 'publish', label: 'Published' },
  { value: 'draft', label: 'Drafts' },
  { value: 'pending', label: 'Pending' },
  { value: 'trash', label: 'Trash' },
];
