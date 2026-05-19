// Package comments hosts the admin REST surface for comment moderation.
//
// The endpoints (mounted under /api/v1/admin/comments) cover the
// operator workflow:
//
//	GET   /api/v1/admin/comments               — paginated list view,
//	                                              filtered by status,
//	                                              post_id and user_id.
//	PATCH /api/v1/admin/comments/{id}          — single-row status
//	                                              transition (approve,
//	                                              spam, trash, pending).
//	POST  /api/v1/admin/comments/bulk          — atomic bulk action
//	                                              against a list of IDs
//	                                              (approve / spam / trash).
//	POST  /api/v1/admin/comments/{id}/reply    — create a child comment
//	                                              under the same post.
//
// All endpoints are gated by the moderate_comments capability. The list
// shape joins the post title and the author display name so the UI can
// render a self-contained table row without a round-trip per comment.
//
// Threading
// =========
// Comments are stored with an `ltree` path materialised by a DB
// trigger (migration 000006). The reply handler relies on the trigger
// to compute path = parent.path || self.label, so the Go side never
// has to assemble an ltree literal — it just inserts a row with
// parent_id set and reads back the resulting path.
//
// Backends
// ========
// The Store interface has two concrete implementations:
//
//   - MemoryStore: lives in this package; used by unit tests and the
//     no-DB development fall-through. It models the moderation state
//     transitions and the bulk-action atomicity property in plain Go.
//   - The Postgres backend lands in a follow-up issue once the schema
//     migration's joins (posts.title, users.display_name) are wired to
//     the wider DAO layer. Plugging it in is a Deps swap.
//
// The handler is identical for both, so the swap is local.
package comments
