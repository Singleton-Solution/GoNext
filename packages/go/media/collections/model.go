package collections

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"
)

// MaxNameBytes mirrors the CHECK constraint in the migration. The
// limit is the source-of-truth at the DB layer; we mirror it here so
// the handler can 400 the offending request with a friendly message
// rather than waiting for a constraint-violation error from the
// driver.
const MaxNameBytes = 256

// MaxSlugBytes mirrors the CHECK on the slug column. Slugs are
// keyboard-friendly and live in URL paths (the admin tree's folder
// route uses the slug, not the UUID, to keep the URL operator-
// readable).
const MaxSlugBytes = 64

// MaxDepth caps the tree depth. Without a cap an operator could
// build a 200-level deep tree that the ltree GiST index can technically
// support but every UI we ship would render poorly. 12 covers every
// folder structure we have observed in WordPress-style migrations
// (the longest real-world example we found was 7 deep, in a media
// archive split by year/month/day) with margin.
const MaxDepth = 12

// slugPattern is the validator the migration's CHECK constraint
// encodes. We pre-compile it so the validator path doesn't allocate.
//
// Slugs must start with [a-z0-9] and may contain only [a-z0-9_-].
// No leading hyphens (would conflict with command-line flags in
// scripts that consume the API); no Unicode (URL-safe by
// construction).
var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// Collection is the wire shape returned by the REST endpoints. JSON
// tags double as the persisted column names; the path column rides
// the wire as a string ("marketing.2026.q1") rather than the
// driver-native pgtype.Ltree because every consumer (UI, plugins)
// parses it as a string.
type Collection struct {
	ID        string    `json:"id"`
	Slug      string    `json:"slug"`
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	ParentID  *string   `json:"parent_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Depth returns the 0-indexed depth of the collection in the tree.
// Root collections have depth 0; a collection at path "a.b.c" has
// depth 2.
func (c Collection) Depth() int {
	if c.Path == "" {
		return 0
	}
	return strings.Count(c.Path, ".")
}

// CreateInput is the payload Store.Create accepts. ParentID is
// optional; nil means "root collection".
type CreateInput struct {
	Slug     string
	Name     string
	ParentID *string
}

// UpdateInput is the payload Store.Rename accepts. Empty fields are
// ignored; a nil Slug means "leave the slug alone".
type UpdateInput struct {
	Slug *string
	Name *string
}

// MoveInput captures a parent change. NewParentID = nil means "move
// to root". The Store implementation guarantees that the move is
// atomic across the node and every descendant (their paths get
// rewritten in the same transaction).
type MoveInput struct {
	NewParentID *string
}

// ErrNotFound is the sentinel returned by store reads when the row
// is missing. Handlers translate to HTTP 404.
var ErrNotFound = errors.New("collections: not found")

// ErrSlugConflict is returned by Create / Rename when the slug would
// collide with a sibling. Handlers translate to HTTP 409.
var ErrSlugConflict = errors.New("collections: slug conflicts with sibling")

// ErrInvalidSlug is returned by validation when the slug fails the
// pattern. Handlers translate to HTTP 400.
var ErrInvalidSlug = errors.New("collections: invalid slug")

// ErrInvalidName is returned by validation when the name is empty or
// too long. Handlers translate to HTTP 400.
var ErrInvalidName = errors.New("collections: invalid name")

// ErrCycle is returned by Move when the target parent is the
// collection itself or one of its descendants. Handlers translate to
// HTTP 400 with a "would create a cycle" message.
var ErrCycle = errors.New("collections: move would create a cycle")

// ErrTooDeep is returned when a Create / Move would push the depth
// past MaxDepth. Handlers translate to HTTP 400.
var ErrTooDeep = errors.New("collections: tree depth exceeds maximum")

// validateSlug reports whether s satisfies the slug constraints
// from the migration's CHECK. Returns nil on success.
func validateSlug(s string) error {
	if s == "" || len(s) > MaxSlugBytes {
		return ErrInvalidSlug
	}
	if !slugPattern.MatchString(s) {
		return ErrInvalidSlug
	}
	return nil
}

// validateName reports whether n satisfies the name constraints.
func validateName(n string) error {
	n = strings.TrimSpace(n)
	if n == "" || len(n) > MaxNameBytes {
		return ErrInvalidName
	}
	return nil
}

// Store is the persistence boundary for collections. Two backends
// (memory, postgres); the wire surface is identical.
type Store interface {
	// Create inserts a new collection. The ID, Path, and
	// timestamps are filled by the store. ErrSlugConflict if a
	// sibling already owns the slug; ErrInvalidSlug / ErrInvalidName
	// on validation failure; ErrTooDeep if the parent is already at
	// MaxDepth-1.
	Create(ctx context.Context, in CreateInput) (Collection, error)

	// GetByID looks up a collection by its UUID. ErrNotFound if no
	// row matches.
	GetByID(ctx context.Context, id string) (Collection, error)

	// GetByPath looks up a collection by its full ltree path. Useful
	// for the admin URL handler, which routes /collections/marketing/2026
	// by joining the path segments.
	GetByPath(ctx context.Context, path string) (Collection, error)

	// List returns every collection, ordered by path (depth-first
	// pre-order). The admin tree sidebar consumes the result and
	// reconstructs the hierarchy locally; alternative is a
	// recursive descent per click, which would chatter.
	List(ctx context.Context) ([]Collection, error)

	// Children returns the direct children of parentID. nil parent
	// means "list the roots". Ordered by name.
	Children(ctx context.Context, parentID *string) ([]Collection, error)

	// Descendants returns every collection at or below the given
	// path (inclusive). Empty path means "every collection".
	Descendants(ctx context.Context, path string) ([]Collection, error)

	// Rename changes a collection's slug and/or name. Renaming the
	// slug rewrites the path for the collection and every
	// descendant (in a single transaction). ErrSlugConflict if the
	// new slug collides with an existing sibling.
	Rename(ctx context.Context, id string, in UpdateInput) (Collection, error)

	// Move re-parents a collection. The path for the collection
	// and every descendant is rewritten. ErrCycle if NewParentID
	// is the collection itself or one of its descendants;
	// ErrTooDeep if the move would push depth past MaxDepth;
	// ErrSlugConflict if the slug collides at the new parent.
	Move(ctx context.Context, id string, in MoveInput) (Collection, error)

	// Delete removes the collection and every descendant. The FK on
	// media has ON DELETE SET NULL semantics, so media inside the
	// deleted folder surfaces back to the root view.
	Delete(ctx context.Context, id string) error
}
