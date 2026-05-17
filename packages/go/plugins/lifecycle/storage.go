package lifecycle

import (
	"context"
	"time"
)

// Storage is the persistence interface for plugin rows.
//
// Two implementations live in this package:
//
//   - MemoryStorage — process-local, used by tests and short-lived
//     dev setups. No persistence, no eviction.
//   - PostgresStorage — production. Backed by the plugins table
//     documented in doc.go.
//
// Implementations MUST be safe for concurrent use across goroutines.
// The UpdateState method is the single source of CAS semantics: it
// applies the new state only when the row's current state matches
// expectedFrom. A concurrent caller observing the same expectedFrom
// loses the race and gets ErrInvalidTransition. The Manager relies on
// this contract — read-modify-write through Get + UpdateState would
// re-introduce the race.
type Storage interface {
	// Insert persists a new row. Returns ErrAlreadyExists if the slug
	// already has a row.
	//
	// The Plugin's InstalledAt, UpdatedAt, and RowVersion are set by
	// the implementation if zero; callers should normally leave them
	// for the storage layer.
	Insert(ctx context.Context, p Plugin) error

	// Get returns the row identified by slug, or ErrNotFound.
	Get(ctx context.Context, slug string) (Plugin, error)

	// List returns every row, ordered by slug ASC for deterministic
	// test assertions. There is no pagination — the plugin table is
	// expected to hold tens to hundreds of rows, not millions.
	List(ctx context.Context) ([]Plugin, error)

	// UpdateState atomically transitions a row from expectedFrom to
	// newState. It returns ErrInvalidTransition (wrapped with the
	// slug) if no row exists at expectedFrom — that's the lost-race
	// signal the Manager hands back to callers.
	//
	// fields lets the caller set ActivatedAt / LastError / ErrorAt in
	// the same write; nil leaves them untouched. The implementation
	// bumps RowVersion and refreshes UpdatedAt regardless.
	UpdateState(ctx context.Context, slug string, expectedFrom, newState State, fields *StateUpdateFields) error

	// Delete removes the row identified by slug. Returns ErrNotFound
	// if the row no longer exists.
	//
	// The caller (Manager.Uninstall) is responsible for placing the
	// row in PendingUninstall before calling Delete; Storage doesn't
	// enforce that, so tests with bespoke needs can clean up freely.
	Delete(ctx context.Context, slug string) error
}

// StateUpdateFields lets UpdateState callers atomically write a
// transition-specific field set alongside the state CAS. Each pointer
// field is "if non-nil, write this value". A zero-but-non-nil time
// (e.g. &time.Time{}) clears the column.
//
// We use pointers rather than separate methods (UpdateStateAndActivatedAt,
// UpdateStateAndError, ...) to keep the Storage interface narrow. The
// Manager is the only caller; the verbosity stays out of test code.
type StateUpdateFields struct {
	// ActivatedAt, when non-nil, overwrites the plugin row's
	// ActivatedAt column. Set on successful Activate.
	ActivatedAt *time.Time

	// LastError, when non-nil, overwrites the plugin row's
	// LastError column. The empty-string case (when the pointer
	// dereferences to "") is meaningful: it clears any prior error
	// text. Used by Reset.
	LastError *string

	// ErrorAt, when non-nil, overwrites the ErrorAt column. As with
	// LastError, a non-nil pointer to a zero time clears the column.
	ErrorAt *time.Time
}
