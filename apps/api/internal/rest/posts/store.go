package posts

import (
	"context"
	"errors"
)

// Sentinel errors returned by Store implementations. Handlers translate
// these into REST status codes:
//
//   - ErrNotFound       -> 404 not_found
//   - ErrVersionConflict -> 412 version_mismatch
//   - ErrDuplicateSlug  -> 409 duplicate_slug
//
// The Store contract is intentionally narrow: every method takes a
// post type discriminator so the same implementation backs both
// /api/v1/posts (post_type='post') and /api/v1/pages (post_type='page').
var (
	ErrNotFound        = errors.New("posts: not found")
	ErrVersionConflict = errors.New("posts: version conflict")
	ErrDuplicateSlug   = errors.New("posts: duplicate slug")
)

// Store is the persistence abstraction for the posts package. The
// production implementation ([PgStore]) talks to Postgres via pgxpool;
// tests use [MemoryStore] to exercise handler behavior without spinning
// up a real database.
//
// Every method takes the post type as the first argument after ctx so
// the same Store backs the post and page mounts. Implementations MUST
// scope every query by post_type — a posts mount must never see page
// rows and vice versa.
type Store interface {
	// List returns posts of the given post_type that match filter,
	// ordered by id ascending (so the cursor is stable). The returned
	// slice may have up to filter.Limit + 1 entries — implementations
	// fetch one extra row so the handler can detect "more pages" and
	// build a next_cursor without a separate count query.
	List(ctx context.Context, postType string, filter ListFilter) ([]Post, error)

	// Get returns one post by id, scoped to post_type. ErrNotFound when
	// no row matches (including post_type mismatch).
	Get(ctx context.Context, postType, id string) (Post, error)

	// Create inserts a new row. authorID is the principal's user id;
	// the caller (handler) has already validated capability. Returns
	// the created Post with id, version, and timestamps populated.
	Create(ctx context.Context, postType, authorID string, in CreateInput) (Post, error)

	// Update applies a sparse SET to the row identified by id. The
	// expectedVersion is matched in the UPDATE WHERE clause; a
	// 0-row outcome surfaces as ErrVersionConflict. Returns the
	// updated row (version bumped).
	Update(ctx context.Context, postType, id string, expectedVersion int, in UpdateInput) (Post, error)

	// Trash moves the row to status='trash' using the same
	// optimistic-concurrency contract as Update. Soft-delete only —
	// the row stays in place so admins can restore it. Hard delete is
	// a future endpoint.
	Trash(ctx context.Context, postType, id string, expectedVersion int) (Post, error)
}
