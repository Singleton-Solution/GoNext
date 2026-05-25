package reusable

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

// RefBlockType is the canonical wire name for a reference to a
// reusable block. The renderer dispatches on this string; the editor
// emits it when an author inserts a reusable block.
const RefBlockType = "core/block"

// MissingBlockType is the sentinel substituted in the resolved tree
// when a `core/block` reference points at a deleted (or never-existed)
// entry. The renderer is expected to surface it as an inert error
// chip rather than crash the page.
const MissingBlockType = "core/missing"

// Entry is a single named reusable block. Mirrors the columns of the
// reusable_blocks table 1:1.
//
// Content is a json.RawMessage rather than a typed BlockTree so this
// package stays decoupled from any specific block-tree representation
// — the editor produces JSON, the renderer consumes it, this layer
// just shuttles bytes. Callers that need typed access decode against
// their own schema.
type Entry struct {
	// ID is the entry's UUID, embedded as `ref` in every `core/block`
	// reference in a post's content_blocks.
	ID uuid.UUID `json:"id"`

	// Name is the human label shown in the inserter. Two entries can
	// share a name; the UUID is the durable identifier.
	Name string `json:"name"`

	// Attrs is free-form metadata (icon, category hint, visibility
	// flags). Not the block tree.
	Attrs json.RawMessage `json:"attrs"`

	// Content is the block tree itself — an array of root Block
	// nodes, matching posts.content_blocks's shape.
	Content json.RawMessage `json:"content"`

	// CreatedAt / UpdatedAt are wall clocks; UpdatedAt re-stamps on
	// every Update call.
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ListFilter narrows the List query. Empty fields are treated as "no
// filter". The cursor is keyed on (created_at DESC, id) for stable
// pagination.
type ListFilter struct {
	// NameContains, when non-empty, restricts the result to entries
	// whose name contains the substring (case-insensitive).
	NameContains string

	// Limit is the max number of rows to return. Defaults to 50 when
	// zero. Capped at 200 by the store.
	Limit int

	// Before, when non-zero, returns rows created strictly earlier
	// than this timestamp — the cursor.
	Before time.Time
}

// Errors returned by Store implementations. Callers errors.Is against
// these sentinels rather than string-match.
var (
	// ErrInvalidEntry is returned by Create/Update when the entry's
	// fields fail validation (empty name, invalid JSON in attrs or
	// content). The store does not persist invalid entries.
	ErrInvalidEntry = errors.New("reusable: invalid entry")

	// ErrNotFound is returned by Get/Update/Delete when no entry
	// matches the given ID.
	ErrNotFound = errors.New("reusable: not found")
)

// Store is the persistence contract for reusable blocks. Two concrete
// backends ship:
//
//   - MemoryStore (this package): backs tests and the no-DB
//     development fall-through.
//   - PgxStore: parameterised SQL against reusable_blocks.
//
// Implementations MUST be safe for concurrent use — the admin
// handler holds a single Store reference and may interleave reads
// (List, Get, Resolve) with writes (Create, Update, Delete).
type Store interface {
	// Create persists a new entry. ID is assigned by the store and
	// returned; callers passing in a zero ID is the expected path.
	// CreatedAt/UpdatedAt are stamped by the store.
	Create(ctx context.Context, e Entry) (Entry, error)

	// Get fetches an entry by ID. Returns ErrNotFound if absent.
	Get(ctx context.Context, id uuid.UUID) (Entry, error)

	// Update mutates the editable fields (name, attrs, content) of
	// an existing entry, re-stamping UpdatedAt. Returns ErrNotFound
	// when no entry matches.
	Update(ctx context.Context, e Entry) (Entry, error)

	// Delete removes the entry with the given ID. Returns nil even
	// when no row matches — idempotent delete matches the rest of
	// the admin surface.
	Delete(ctx context.Context, id uuid.UUID) error

	// List returns matching entries sorted by created_at DESC. The
	// returned slice carries at most filter.Limit rows; callers
	// detect "has next page" by checking len(result) == filter.Limit.
	List(ctx context.Context, f ListFilter) ([]Entry, error)

	// GetMany fetches every entry whose ID is in ids. Missing IDs
	// are silently omitted — the renderer's resolver uses this to
	// fan out a single round-trip per page render. Order of the
	// returned slice is unspecified; callers should re-index by ID.
	GetMany(ctx context.Context, ids []uuid.UUID) ([]Entry, error)
}

// validate enforces the constraints the underlying table CHECK rules
// also enforce. Keeping the check in code means the in-memory store
// and the Postgres store fail the same way for the same input — no
// "well, the memory store is lenient" trap.
func validate(e Entry) error {
	if len(e.Name) == 0 {
		return errors.New("reusable: invalid entry: name empty")
	}
	if len(e.Name) > 256 {
		return errors.New("reusable: invalid entry: name too long (max 256)")
	}
	// Attrs and Content default to "{}" / "[]" but a caller can pass
	// nil to mean the same thing. Empty raw messages are caught here
	// before they reach the DB and fail with a less helpful error.
	if len(e.Attrs) > 0 && !json.Valid(e.Attrs) {
		return errors.New("reusable: invalid entry: attrs is not valid JSON")
	}
	if len(e.Content) > 0 && !json.Valid(e.Content) {
		return errors.New("reusable: invalid entry: content is not valid JSON")
	}
	return nil
}
