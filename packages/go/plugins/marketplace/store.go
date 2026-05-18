package marketplace

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgxQuerier is the subset of *pgxpool.Pool that the marketplace
// stores need. Exposing it as an interface keeps the stores testable
// with a fake and lets callers substitute pgx.Tx when the marketplace
// write needs to participate in a larger transaction.
//
// *pgxpool.Pool and pgx.Tx both satisfy PgxQuerier verbatim.
type PgxQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Store is the umbrella handle that bundles the five per-table stores.
//
// Callers normally construct one Store at process boot via NewStore and
// reach for the sub-store they need (`s.Listings.Create`,
// `s.Ratings.Submit`). Each sub-store is independently usable; the
// bundle exists to keep the wiring boilerplate small.
type Store struct {
	Listings *Listings
	Versions *Versions
	Compat   *CompatStore
	Ratings  *Ratings
	Events   *Events
}

// NewStore wraps a *pgxpool.Pool into the five sub-stores. Panics on
// a nil pool — silently degrading to a no-op store hides the
// configuration error.
func NewStore(pool *pgxpool.Pool) *Store {
	if pool == nil {
		panic("marketplace.NewStore: pool is required")
	}
	return NewStoreWithQuerier(pool)
}

// NewStoreWithQuerier is the test seam — production code uses
// NewStore. The querier is shared across all sub-stores; using a
// pgx.Tx here lets a caller scope every marketplace write inside a
// single transaction.
func NewStoreWithQuerier(q PgxQuerier) *Store {
	if q == nil {
		panic("marketplace.NewStoreWithQuerier: querier is required")
	}
	return &Store{
		Listings: NewListings(q),
		Versions: NewVersions(q),
		Compat:   NewCompatStore(q),
		Ratings:  NewRatings(q),
		Events:   NewEvents(q),
	}
}

// nowFunc is the time source the stores reach for when stamping
// CreatedAt etc. Each sub-store carries its own pointer so tests can
// pin one without bleeding into the others.
type nowFunc func() time.Time

func resolveNow(fn nowFunc) time.Time {
	if fn != nil {
		return fn().UTC()
	}
	return time.Now().UTC()
}

// =============================================================================
// SQLSTATE helpers
// =============================================================================

// isUniqueViolation reports whether err is a Postgres unique-violation
// (SQLSTATE 23505). Translates into ErrAlreadyExists at the store
// boundary.
//
// We unwrap up the chain because pgxpool layers wrap the original
// *pgconn.PgError under its own context.
func isUniqueViolation(err error) bool {
	for ; err != nil; err = errors.Unwrap(err) {
		if s, ok := err.(sqlStater); ok {
			return s.SQLState() == "23505"
		}
	}
	return false
}

// isCheckViolation reports whether err is a Postgres check-constraint
// violation (SQLSTATE 23514). Used by Submit to translate an invalid
// stars value into ErrInvalidInput even when the application-side
// guard misses (e.g. caller-supplied int that wraps a negative).
func isCheckViolation(err error) bool {
	for ; err != nil; err = errors.Unwrap(err) {
		if s, ok := err.(sqlStater); ok {
			return s.SQLState() == "23514"
		}
	}
	return false
}

// sqlStater is the minimal interface our SQLSTATE-detection helpers
// need. *pgconn.PgError implements it. Re-declared locally so tests
// can drop a stub that doesn't drag pgconn into test code.
type sqlStater interface {
	SQLState() string
}
