package users

import (
	"context"
	"errors"
	"time"
)

// DefaultListLimit is the page size when the client supplies no
// `limit` query param. Matches the rest of the public REST surface.
const DefaultListLimit = 30

// MaxListLimit caps the page size. Higher than this stresses the
// database's index on (created_at, id) and forces a wider client
// render budget; clients that need more pages should paginate.
const MaxListLimit = 100

// User is the public view of a user row. Sensitive fields (email,
// password material, capabilities, IP/UA telemetry) are NEVER on
// this struct — this is the wire shape, and an absent field is a
// privacy guarantee, not an oversight.
//
// The shape mirrors the GraphQL Public user type so the two surfaces
// stay aligned. When the public API surfaces an "author" relation
// (post.author), the embedded object is this exact struct.
type User struct {
	// ID is the user's UUID v7.
	ID string `json:"id"`

	// Handle is the public login handle (also the URL slug for
	// /authors/{handle} on the public site). citext on the DB
	// side; we lowercase here for consistency.
	Handle string `json:"handle"`

	// DisplayName is the human-readable name shown on bylines.
	// Nullable: a freshly-registered user with no display name
	// renders as their handle on the public site, but we surface
	// null here so the client can decide on the fallback.
	DisplayName *string `json:"display_name,omitempty"`

	// CreatedAt is the account join time. Not the publication
	// time of any specific post — just identity provenance for
	// the "member since" line on author pages.
	CreatedAt time.Time `json:"created_at"`
}

// ListFilter narrows the list query. All fields are optional; the
// handler builds it from query params.
type ListFilter struct {
	// HandlePrefix, when non-empty, restricts the result to users
	// whose handle starts with the given prefix. Case-insensitive
	// (citext on the DB side). Useful for "@-mention" autocomplete
	// on the public site.
	HandlePrefix string

	// Limit caps the page size; clamped to MaxListLimit by the
	// handler.
	Limit int

	// After is the decoded cursor — the "created_at:id" tuple of
	// the last row of the previous page. Empty means "start at
	// the beginning".
	After string
}

// Store is the persistence boundary for the public users surface.
// Two backends — in-memory for tests, Postgres for production —
// implement this interface.
//
// Note this is distinct from the admin/users Store: the admin store
// surfaces sensitive fields, while this one's row type omits them.
// Keeping the interfaces separate means the public surface cannot
// accidentally pick up a "GetWithCapabilities" method through a
// shared interface and surface them.
type Store interface {
	// List returns a page of public users ordered by created_at DESC,
	// id DESC as the tie-breaker. The store fetches limit+1 rows so
	// the handler knows whether to surface a next cursor.
	List(ctx context.Context, f ListFilter) ([]User, error)

	// GetByID looks up a single user. Returns ErrNotFound when the
	// id doesn't match — the handler maps to a 404 without leaking
	// the difference between "soft-deleted" and "never existed".
	GetByID(ctx context.Context, id string) (User, error)

	// GetByHandle is the convenience lookup for /api/v1/users/{handle}
	// when the path segment is not a UUID. Case-insensitive on the
	// store side.
	GetByHandle(ctx context.Context, handle string) (User, error)
}

// ErrNotFound is returned by store reads when no row matches. The
// handler maps this to HTTP 404.
var ErrNotFound = errors.New("rest/users: not found")
