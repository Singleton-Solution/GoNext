/**
 * Shared types for the public comments surface.
 *
 * Mirrors `apps/api/internal/rest/comments` — keeping the fields in
 * sync is the price of avoiding a generated-types step for a small
 * surface area. Add a runtime parser (zod, valibot) once the shape
 * grows past what a focused review can catch in PR.
 */
export interface PublicComment {
  /** UUID of the comment. */
  id: string;
  /** Owning post id. */
  post_id: string;
  /** Immediate parent comment id, or empty for top-level. */
  parent_id?: string;
  /** Materialised ltree path (dotted UUID labels). */
  path: string;
  /** Depth in the thread (nlevel(path)); 1 for top-level. */
  depth: number;
  /** Display name; "Anonymous" fallback handled server-side. */
  author_display_name: string;
  /** Sanitised content; safe to render as HTML. */
  content: string;
  /** ISO-8601 timestamp. */
  created_at: string;
}

/**
 * Server response for POST /api/v1/posts/{id}/comments.
 * `pending: true` means the row was created in moderation; the UI
 * displays the awaiting-moderation notice in that case.
 */
export interface SubmitResponse {
  comment: PublicComment;
  pending: boolean;
}

/**
 * The shape returned by GET /api/v1/posts/{id}/comments.
 * `data` is empty when the post has no approved comments yet.
 */
export interface CommentsList {
  data: PublicComment[];
  pagination: {
    next_cursor: string;
  };
}

/**
 * Renderable thread node. The thread renderer builds these from the
 * flat list by grouping on parent_id. We expose the type from the
 * module entry so tests can construct nodes without rebuilding the
 * tree-from-flat logic.
 */
export interface ThreadNode {
  comment: PublicComment;
  children: ThreadNode[];
}
