package comments

import (
	"context"
	"errors"
	"time"
)

// Status is the moderation state of a comment. The set mirrors the
// CHECK constraint on comments.status in migration 000006 so the Go
// side and the DB agree without a translation table.
type Status string

const (
	StatusPending  Status = "pending"
	StatusApproved Status = "approved"
	StatusSpam     Status = "spam"
	StatusTrash    Status = "trash"
)

// AllStatuses enumerates the valid moderation states. Used by the
// handler to validate the ?status= query parameter and the PATCH body.
// Iteration order is stable so error messages list values consistently.
var AllStatuses = []Status{
	StatusPending,
	StatusApproved,
	StatusSpam,
	StatusTrash,
}

// IsValidStatus reports whether s is one of the canonical moderation
// states. Empty strings are rejected so callers don't accidentally
// "reset" a comment to the zero value.
func IsValidStatus(s Status) bool {
	for _, v := range AllStatuses {
		if v == s {
			return true
		}
	}
	return false
}

// BulkAction enumerates the verbs the bulk endpoint accepts. The
// "pending" state is intentionally absent — bulk-undeleting comments
// is rare and the single-row PATCH covers it without growing the
// bulk surface. If product asks for it later, add it here and to
// applyBulk in store.go.
type BulkAction string

const (
	BulkApprove BulkAction = "approve"
	BulkSpam    BulkAction = "spam"
	BulkTrash   BulkAction = "trash"
)

// AllBulkActions enumerates the verbs the bulk endpoint accepts.
var AllBulkActions = []BulkAction{
	BulkApprove,
	BulkSpam,
	BulkTrash,
}

// IsValidBulkAction reports whether a is one of the canonical bulk
// verbs.
func IsValidBulkAction(a BulkAction) bool {
	for _, v := range AllBulkActions {
		if v == a {
			return true
		}
	}
	return false
}

// StatusForBulkAction maps a bulk verb to the moderation state it
// applies. Keeping this in one place makes the bulk handler trivial
// and means a future "archive" action lands as one map entry, not a
// spaghetti of switch statements.
func StatusForBulkAction(a BulkAction) Status {
	switch a {
	case BulkApprove:
		return StatusApproved
	case BulkSpam:
		return StatusSpam
	case BulkTrash:
		return StatusTrash
	}
	return ""
}

// Comment is the admin-list view of a single comment. It carries the
// joined post + author fields so the UI can render a row without a
// per-comment lookup. The fields are a subset of the columns in
// migration 000006 — IP, user agent, spam score and raw meta are
// intentionally omitted from the list shape because they bloat the
// JSON and are only useful on the detail surface.
type Comment struct {
	// ID is the comment's UUID, the path-segment key for the
	// detail and action endpoints.
	ID string `json:"id"`

	// PostID is the owning post.
	PostID string `json:"post_id"`

	// PostTitle is the joined posts.title. Surfaced so the list row
	// can show "<author> on <post>" without a second fetch.
	PostTitle string `json:"post_title"`

	// ParentID is the immediate parent comment, or empty for a
	// top-level comment. The UI uses this to render the "reply to"
	// hint on threaded rows.
	ParentID string `json:"parent_id,omitempty"`

	// Path is the materialised ltree path as a dotted string (the
	// underscored-UUID labels of every ancestor including self).
	// Useful for clients that build a thread tree client-side; the
	// handler doesn't otherwise consume it.
	Path string `json:"path"`

	// AuthorUserID is the linked user, or empty for an anonymous
	// commenter. The display name is the source of truth for the UI.
	AuthorUserID string `json:"author_user_id,omitempty"`

	// AuthorDisplayName is the joined users.display_name when the
	// comment is linked to a user, otherwise the comments.author_name
	// snapshot. Always non-empty; "Anonymous" is the fallback.
	AuthorDisplayName string `json:"author_display_name"`

	// Content is the rendered comment body (HTML, plain, or markdown
	// per ContentFormat). The list endpoint returns the full string;
	// the UI is responsible for truncation/excerpting in the row.
	Content string `json:"content"`

	// ContentFormat is "html", "plain", or "markdown".
	ContentFormat string `json:"content_format"`

	// Status is the moderation state.
	Status Status `json:"status"`

	// CreatedAt is when the comment was submitted.
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt is when the comment was last modified.
	UpdatedAt time.Time `json:"updated_at"`
}

// ListFilter narrows the list query. Empty fields are treated as
// "no filter" — passing the zero value returns all comments
// regardless of status or post.
type ListFilter struct {
	// Status, when non-empty, restricts the result to comments in
	// that moderation state. Validated by the handler against
	// AllStatuses.
	Status Status

	// PostID, when non-empty, restricts the result to comments on
	// the given post. The handler does not validate that the post
	// exists — a non-matching UUID simply yields an empty page.
	PostID string

	// UserID, when non-empty, restricts the result to comments
	// authored by the given user. Anonymous comments are excluded
	// from the result when this filter is set.
	UserID string

	// Page is the 1-based page number. Defaults to 1 when zero.
	Page int

	// Limit is the maximum number of rows to return. Defaults to
	// 30 when zero; capped at 100 by the handler.
	Limit int
}

// ListPage is the result of a list call: the page itself plus a flag
// signalling whether more rows exist beyond the current page. The
// handler uses HasNext to decide whether to encode a next_cursor in
// the response envelope.
type ListPage struct {
	// Comments are the rows for the current page, sorted by
	// created_at descending.
	Comments []Comment

	// HasNext is true when at least one row exists beyond the
	// current page.
	HasNext bool
}

// Store is the persistence contract for the admin comments package.
// Two concrete backends are envisaged:
//
//   - MemoryStore (this package): backs tests and the no-DB
//     development fall-through.
//   - The Postgres backend lands once the wider DAO layer is wired.
//
// The interface is small on purpose — each method maps 1:1 to one
// of the four admin endpoints, with the rationale that adding a
// surface area later is cheaper than keeping a generic "query"
// shape consistent across two backends.
type Store interface {
	// List returns the matching comments, sorted by created_at DESC,
	// plus a flag telling the caller whether more pages exist.
	List(ctx context.Context, f ListFilter) (ListPage, error)

	// Get returns the comment with the given ID, or ErrNotFound.
	Get(ctx context.Context, id string) (Comment, error)

	// UpdateStatus transitions the comment's moderation state.
	// Returns the updated row, or ErrNotFound. The store stamps a
	// fresh UpdatedAt.
	UpdateStatus(ctx context.Context, id string, status Status) (Comment, error)

	// Bulk applies the given moderation state to every comment in
	// ids in a single transaction. If any ID is unknown, the whole
	// operation is rejected and the store is unchanged. Returns the
	// updated rows in the same order as ids.
	Bulk(ctx context.Context, ids []string, status Status) ([]Comment, error)

	// Reply creates a child comment under parentID. The store fills
	// post_id from the parent, sets parent_id, and persists path
	// (in the Postgres backend the trigger does the path work; in
	// the memory backend the helper computes it). Returns the new
	// comment.
	Reply(ctx context.Context, parentID string, authorUserID, authorName, content string) (Comment, error)
}

// ErrNotFound is returned when the store can't find a comment with
// the requested ID. The handler maps this to a 404 response.
var ErrNotFound = errors.New("admin/comments: not found")

// ErrBulkPartial is returned by Bulk when at least one ID in the
// batch is unknown. The store is unchanged. The handler maps this
// to a 422 so the client knows to reload its selection.
var ErrBulkPartial = errors.New("admin/comments: one or more ids not found")
