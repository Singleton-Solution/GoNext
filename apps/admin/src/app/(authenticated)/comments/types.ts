/**
 * Shared types for the Comments admin surface.
 *
 * The shapes mirror the JSON returned by the GoNext API
 * (`apps/api/internal/admin/comments`). Keeping them in one place
 * means the list, detail, status badge, and bulk-action components
 * compile against a single contract.
 *
 * The admin endpoint (`/api/v1/admin/comments`) emits a richer row
 * shape than the public spec's `Comment` schema (it joins the post
 * title, threads, etc.), so the row interfaces below are NOT derived
 * from the spec wholesale. The status enum, however, IS shared with
 * the public API and is sourced from there — issue #514 follow-up so
 * an enum value change shows up as a type error here.
 */
import type { components } from '@gonext/api-types';

/** Canonical moderation states. Mirrors the API's Status type. */
export type CommentStatus = components['schemas']['Comment']['status'];

/** Bulk action verbs accepted by `/api/v1/admin/comments/bulk`. */
export type BulkAction = 'approve' | 'spam' | 'trash';

/**
 * One row of the admin comment list. Mirrors apps/api/internal/admin/comments
 * `Comment`. Fields with `?` are optional in the on-wire JSON.
 */
export interface Comment {
  id: string;
  postId: string;
  postTitle: string;
  parentId?: string;
  path: string;
  authorUserId?: string;
  authorDisplayName: string;
  content: string;
  contentFormat: string;
  status: CommentStatus;
  createdAt: string;
  updatedAt: string;
}

/** Server response envelope for GET /api/v1/admin/comments. */
export interface CommentListResponse {
  data: Comment[];
  pagination: {
    nextCursor: string;
    prevCursor?: string;
  };
}

/** Filter chip definition for the toolbar. */
export interface StatusFilter {
  value: 'any' | CommentStatus;
  label: string;
}

export const STATUS_FILTERS: readonly StatusFilter[] = [
  { value: 'any', label: 'All' },
  { value: 'pending', label: 'Pending' },
  { value: 'approved', label: 'Approved' },
  { value: 'spam', label: 'Spam' },
  { value: 'trash', label: 'Trash' },
];

/**
 * Wire-shape JSON snake_case keys -> camelCase. The API hands us
 * snake_case (Go is the source of truth); the UI prefers camelCase
 * because that's the React idiom. This translator lives here so the
 * mapping is one place instead of duplicated across components.
 */
export interface WireComment {
  id: string;
  post_id: string;
  post_title: string;
  parent_id?: string;
  path: string;
  author_user_id?: string;
  author_display_name: string;
  content: string;
  content_format: string;
  status: CommentStatus;
  created_at: string;
  updated_at: string;
}

export function toComment(w: WireComment): Comment {
  return {
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
  };
}

export interface WireListResponse {
  data: WireComment[];
  pagination: {
    next_cursor: string;
    prev_cursor?: string;
  };
}

export function toListResponse(w: WireListResponse): CommentListResponse {
  return {
    data: w.data.map(toComment),
    pagination: {
      nextCursor: w.pagination.next_cursor,
      prevCursor: w.pagination.prev_cursor,
    },
  };
}

/**
 * Excerpt content to a maximum length, appending an ellipsis when
 * truncated. Used by the list row's "comment excerpt" column.
 * Operates on the raw content string; the API hands us sanitised
 * HTML so this is purely a length cap.
 */
export function excerpt(content: string, maxLen = 200): string {
  if (content.length <= maxLen) return content;
  return content.slice(0, maxLen).trimEnd() + '…';
}
