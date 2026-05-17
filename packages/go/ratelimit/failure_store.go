package ratelimit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"
)

// FailureStore tracks per-account failed-login counters and lockout
// expirations. It is the durable backing store for
// LoginAttemptLimiter's account-lockout feature.
//
// The interface is keyed by an opaque user identifier (whatever the
// caller's user table uses as a primary key — typically a UUID string).
// The store deliberately does NOT key on email: emails change, users
// merge, and using an external identifier prevents the limiter from
// becoming an enumeration oracle by keying on something that doesn't
// exist for unknown accounts.
//
// Two implementations ship with this package:
//
//   - MemoryFailureStore: process-local, in-memory. Suitable for tests
//     and single-instance dev; restarts wipe state.
//   - PostgresFailureStore: backed by users.failed_login_count and
//     users.locked_until columns. Survives restarts and is shared
//     across a multi-replica fleet.
//
// Issue #195 requires the durable implementation: in-memory lockouts
// are defeated by either a process restart or a multi-instance deploy
// (the attacker can simply retry against another replica).
type FailureStore interface {
	// IncrementFailure adds one to the consecutive-failure counter for
	// userID and returns the post-increment count and the current
	// lockedUntil timestamp (which may be the zero value if the account
	// is not locked). The implementation MUST be atomic with respect to
	// concurrent IncrementFailure / ClearFailures calls on the same
	// userID — Postgres uses UPDATE ... RETURNING for this, Memory uses
	// a mutex.
	IncrementFailure(ctx context.Context, userID string) (count int, lockedUntil time.Time, err error)

	// ClearFailures resets the counter to 0 and the lockout to the zero
	// time for userID. Called after a successful credential check.
	ClearFailures(ctx context.Context, userID string) error

	// GetFailures returns the current counter and lockout state without
	// mutating either. Used by IsLocked.
	GetFailures(ctx context.Context, userID string) (count int, lockedUntil time.Time, err error)

	// SetLockedUntil sets the lockout expiry on userID. Called by
	// LoginAttemptLimiter once a failure trips the threshold.
	SetLockedUntil(ctx context.Context, userID string, until time.Time) error
}

// MemoryFailureStore keeps lockout state in process memory. State is
// lost on restart and not shared across replicas — use only in tests
// and single-instance dev. For production multi-instance, use
// PostgresFailureStore.
type MemoryFailureStore struct {
	mu   sync.Mutex
	rows map[string]memFailureRow
}

type memFailureRow struct {
	count       int
	lockedUntil time.Time
}

// NewMemoryFailureStore returns an empty in-memory FailureStore.
func NewMemoryFailureStore() *MemoryFailureStore {
	return &MemoryFailureStore{rows: make(map[string]memFailureRow)}
}

// IncrementFailure atomically bumps the per-user counter.
func (s *MemoryFailureStore) IncrementFailure(_ context.Context, userID string) (int, time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row := s.rows[userID]
	row.count++
	s.rows[userID] = row
	return row.count, row.lockedUntil, nil
}

// ClearFailures removes any state for userID.
func (s *MemoryFailureStore) ClearFailures(_ context.Context, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.rows, userID)
	return nil
}

// GetFailures reports the current state.
func (s *MemoryFailureStore) GetFailures(_ context.Context, userID string) (int, time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row := s.rows[userID]
	return row.count, row.lockedUntil, nil
}

// SetLockedUntil writes the lockout expiry.
func (s *MemoryFailureStore) SetLockedUntil(_ context.Context, userID string, until time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	row := s.rows[userID]
	row.lockedUntil = until
	s.rows[userID] = row
	return nil
}

// PostgresFailureStore persists lockout state in the users table —
// specifically the failed_login_count and locked_until columns. It is
// the production implementation: lockouts survive process restarts and
// are shared across every replica connected to the same database.
//
// Schema expectations (the columns must already exist; the migration
// adding them ships in a separate follow-up PR — see this PR's body
// for the tracking issue):
//
//	ALTER TABLE users
//	  ADD COLUMN failed_login_count integer NOT NULL DEFAULT 0,
//	  ADD COLUMN locked_until timestamptz NULL;
//
// The store is intentionally minimal — no schema management, no
// migration, no connection pooling — because those belong to the
// caller's bootstrap code, not to a leaf rate-limit package.
//
// userID maps to the users.id primary key. The Table and IDColumn
// fields let callers override defaults if their schema differs (some
// deployments use uuid; others bigint).
type PostgresFailureStore struct {
	// db is a *sql.DB or anything that implements the same methods.
	db pgExecutor

	// Table is the users table name. Defaults to "users". Identifiers
	// are NOT escaped — pass only trusted constants.
	Table string

	// IDColumn is the column matched against userID. Defaults to "id".
	IDColumn string

	// CountColumn defaults to "failed_login_count".
	CountColumn string

	// LockedUntilColumn defaults to "locked_until".
	LockedUntilColumn string
}

// pgExecutor is the subset of database/sql we actually use. Defining
// our own interface keeps the dependency surface tiny and lets tests
// swap in a fake without spinning up a real Postgres.
//
// The QueryRowContext signature returns a scanner abstraction rather
// than *sql.Row because the concrete *sql.Row is awkward to fake
// (its Scan plumbing pulls from the driver layer). The interface keeps
// unit tests light while remaining trivially satisfied by *sql.DB —
// see sqlDBAdapter below.
type pgExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) rowScanner
}

// rowScanner is the subset of *sql.Row we depend on.
type rowScanner interface {
	Scan(dest ...any) error
}

// sqlDBAdapter wraps a *sql.DB to fit pgExecutor. It exists because
// *sql.DB.QueryRowContext returns *sql.Row (concrete type), but our
// interface returns rowScanner — a single-method wrapper bridges them.
type sqlDBAdapter struct {
	db *sql.DB
}

func (a sqlDBAdapter) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return a.db.ExecContext(ctx, query, args...)
}

func (a sqlDBAdapter) QueryRowContext(ctx context.Context, query string, args ...any) rowScanner {
	return a.db.QueryRowContext(ctx, query, args...)
}

// ErrFailureStorePostgresNilDB is returned by NewPostgresFailureStore
// when the supplied *sql.DB is nil.
var ErrFailureStorePostgresNilDB = errors.New("ratelimit.NewPostgresFailureStore: db is nil")

// NewPostgresFailureStore constructs a PostgresFailureStore using the
// default column names. Returns an error if db is nil. To customize
// table or column names, mutate the returned struct before use.
func NewPostgresFailureStore(db *sql.DB) (*PostgresFailureStore, error) {
	if db == nil {
		return nil, ErrFailureStorePostgresNilDB
	}
	return &PostgresFailureStore{
		db:                sqlDBAdapter{db: db},
		Table:             "users",
		IDColumn:          "id",
		CountColumn:       "failed_login_count",
		LockedUntilColumn: "locked_until",
	}, nil
}

// newPostgresFailureStoreWithExecutor is the internal constructor used
// by tests to inject a fake pgExecutor. It bypasses the *sql.DB type
// so we don't need a real Postgres in unit tests.
func newPostgresFailureStoreWithExecutor(db pgExecutor) *PostgresFailureStore {
	return &PostgresFailureStore{
		db:                db,
		Table:             "users",
		IDColumn:          "id",
		CountColumn:       "failed_login_count",
		LockedUntilColumn: "locked_until",
	}
}

// IncrementFailure runs UPDATE ... RETURNING so the increment-and-read
// is one atomic statement.
func (s *PostgresFailureStore) IncrementFailure(ctx context.Context, userID string) (int, time.Time, error) {
	q := fmt.Sprintf(
		"UPDATE %s SET %s = %s + 1 WHERE %s = $1 RETURNING %s, %s",
		s.Table, s.CountColumn, s.CountColumn, s.IDColumn, s.CountColumn, s.LockedUntilColumn,
	)
	var count int
	var lockedUntil sql.NullTime
	err := s.db.QueryRowContext(ctx, q, userID).Scan(&count, &lockedUntil)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("ratelimit.PostgresFailureStore.IncrementFailure: %w", err)
	}
	if lockedUntil.Valid {
		return count, lockedUntil.Time, nil
	}
	return count, time.Time{}, nil
}

// ClearFailures zeroes the counter and the lock timestamp.
func (s *PostgresFailureStore) ClearFailures(ctx context.Context, userID string) error {
	q := fmt.Sprintf(
		"UPDATE %s SET %s = 0, %s = NULL WHERE %s = $1",
		s.Table, s.CountColumn, s.LockedUntilColumn, s.IDColumn,
	)
	if _, err := s.db.ExecContext(ctx, q, userID); err != nil {
		return fmt.Errorf("ratelimit.PostgresFailureStore.ClearFailures: %w", err)
	}
	return nil
}

// GetFailures reads the current state.
func (s *PostgresFailureStore) GetFailures(ctx context.Context, userID string) (int, time.Time, error) {
	q := fmt.Sprintf(
		"SELECT %s, %s FROM %s WHERE %s = $1",
		s.CountColumn, s.LockedUntilColumn, s.Table, s.IDColumn,
	)
	var count int
	var lockedUntil sql.NullTime
	err := s.db.QueryRowContext(ctx, q, userID).Scan(&count, &lockedUntil)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, time.Time{}, nil
	}
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("ratelimit.PostgresFailureStore.GetFailures: %w", err)
	}
	if lockedUntil.Valid {
		return count, lockedUntil.Time, nil
	}
	return count, time.Time{}, nil
}

// SetLockedUntil writes the lockout expiry.
func (s *PostgresFailureStore) SetLockedUntil(ctx context.Context, userID string, until time.Time) error {
	q := fmt.Sprintf(
		"UPDATE %s SET %s = $1 WHERE %s = $2",
		s.Table, s.LockedUntilColumn, s.IDColumn,
	)
	if _, err := s.db.ExecContext(ctx, q, until, userID); err != nil {
		return fmt.Errorf("ratelimit.PostgresFailureStore.SetLockedUntil: %w", err)
	}
	return nil
}
