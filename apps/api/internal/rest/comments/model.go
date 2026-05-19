package comments

import (
	"context"
	"errors"
	"time"
)

// Status mirrors the CHECK constraint on comments.status in migration
// 000006. We duplicate the type rather than import the admin package
// to keep the public surface independent of the admin moderation
// internals (separate import graphs, separate testability).
type Status string

const (
	StatusPending  Status = "pending"
	StatusApproved Status = "approved"
	StatusSpam     Status = "spam"
)

// Comment is the public view of a single comment. It deliberately
// omits PII (email, ip, user_agent) and moderation telemetry
// (spam_score, status when approved). The list endpoint emits these
// rows; the submit endpoint returns the freshly-created comment
// (which may still be pending — see Created.Pending).
type Comment struct {
	// ID is the comment's UUID.
	ID string `json:"id"`

	// PostID is the owning post.
	PostID string `json:"post_id"`

	// ParentID is the immediate parent comment, or empty for a
	// top-level comment. Frontend renderers use this to wire the
	// "in reply to" surface.
	ParentID string `json:"parent_id,omitempty"`

	// Path is the materialised ltree path as a dotted string. The
	// frontend uses this both for thread ordering (sort lexicographic)
	// and depth computation (count of '.' + 1, capped at the design's
	// 6-level limit).
	Path string `json:"path"`

	// Depth is a convenience: nlevel(path). Computed server-side so
	// the frontend doesn't have to parse the ltree.
	Depth int `json:"depth"`

	// AuthorDisplayName is "<commenter>" — the joined users.display_name
	// for logged-in commenters, the snapshotted author_name for
	// anonymous ones. Never empty; "Anonymous" is the fallback.
	AuthorDisplayName string `json:"author_display_name"`

	// Content is the sanitised comment body. Safe to render as HTML.
	Content string `json:"content"`

	// CreatedAt is when the comment was submitted.
	CreatedAt time.Time `json:"created_at"`
}

// Created is the response body of the POST endpoint. The shape is
// {comment, pending}: the comment as the API now sees it, plus a
// boolean the frontend uses to decide whether to render the "your
// comment is awaiting moderation" notice or to slot the row into
// the live thread.
type Created struct {
	Comment Comment `json:"comment"`
	Pending bool    `json:"pending"`
}

// ListFilter narrows the list query. PostID is required; the cursor
// is optional and resumes mid-thread.
type ListFilter struct {
	// PostID restricts the result to comments on this post. Required;
	// the handler validates non-empty before calling the store.
	PostID string

	// AfterPath is a cursor in ltree-path lexicographic order. The
	// store returns rows with path > AfterPath. Empty means "start at
	// the beginning of the thread".
	AfterPath string

	// AfterID is the tie-breaker for rows with identical paths
	// (impossible in practice because the path embeds the row's UUID,
	// but the field exists so the cursor format is forward-compatible
	// when we later add a top-level "newest first" sort).
	AfterID string

	// Limit caps the page size. Defaults to 50 when zero; capped at
	// 100 by the handler.
	Limit int
}

// ListPage is the result of a list call: the page itself plus a flag
// signalling whether more rows exist beyond the current page.
type ListPage struct {
	// Comments are the rows for the current page, sorted by
	// path ASC (thread-natural order).
	Comments []Comment

	// HasNext is true when at least one row exists beyond the
	// returned page. The handler uses this to issue a next_cursor.
	HasNext bool
}

// SubmitInput captures the validated submit payload. The handler
// fills the network identity fields from r; the store persists them
// for moderation but never returns them.
type SubmitInput struct {
	// PostID is the owning post (path-segment from the route).
	PostID string

	// ParentID is the parent comment, or empty for top-level. The
	// store validates that the parent exists and belongs to PostID.
	ParentID string

	// AuthorUserID is the logged-in user's ID, or empty for anonymous.
	// When set, the store ignores AuthorName/AuthorEmail in favour of
	// the user's profile, but the snapshot is still persisted on the
	// row for moderation continuity.
	AuthorUserID string

	// AuthorName is the display name for anonymous commenters.
	// Required when AuthorUserID is empty; ignored otherwise (the
	// joined users.display_name takes precedence on the list shape).
	AuthorName string

	// AuthorEmail is the optional contact for anonymous commenters.
	// Never returned via the API; only used for gravatar lookup and
	// for moderator follow-ups.
	AuthorEmail string

	// Content is the sanitised comment body.
	Content string

	// AuthorIP is the source IP (best-effort; behind a proxy we use
	// X-Forwarded-For's leftmost entry). Persisted for the rate
	// limiter and spam scorer; never returned.
	AuthorIP string

	// AuthorUserAgent is the User-Agent header. Persisted for the
	// spam scorer; never returned.
	AuthorUserAgent string
}

// Store is the persistence contract for the public comments package.
// Two concrete backends are envisaged:
//
//   - MemoryStore (this package): backs tests and the no-DB
//     development fall-through.
//   - Postgres lands once the wider DAO ships.
type Store interface {
	// List returns approved comments on PostID, sorted by path
	// ascending (thread-natural order).
	List(ctx context.Context, f ListFilter) (ListPage, error)

	// Submit creates a new comment row. The store assigns the ID,
	// derives the path (via the DB trigger / mirror in the memory
	// store), and applies the initial status from initialStatus.
	// Returns the persisted comment.
	Submit(ctx context.Context, in SubmitInput, initialStatus Status) (Comment, error)

	// PostExists reports whether the given post id is known. Used by
	// the submit handler to fail-fast with 404 before touching the
	// network-identity persistence path.
	PostExists(ctx context.Context, postID string) (bool, error)

	// CommentsByIP returns the number of comments the given IP has
	// submitted since since (inclusive). The submit handler uses this
	// as a rate limiter / spam signal. since=zero-value means "all
	// time", which is the wrong question; callers always pass a
	// bounded window.
	CommentsByIP(ctx context.Context, ip string, since time.Time) (int, error)
}

// ErrNotFound is returned when a post or parent comment doesn't exist.
// The handler maps this to a 404 response.
var ErrNotFound = errors.New("rest/comments: not found")

// ErrParentMismatch is returned when the supplied parent_id belongs
// to a different post than the request path's post id. The handler
// maps this to a 400.
var ErrParentMismatch = errors.New("rest/comments: parent comment belongs to a different post")
