package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgxQuerier is the subset of *pgxpool.Pool that PostgresStorage uses.
//
// Exposing the interface (rather than taking *pgxpool.Pool directly)
// keeps the store testable with a pgxmock-style fake and lets callers
// substitute pgx.Tx (which implements the same methods) when they
// want a transition to participate in a larger transaction.
type PgxQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// pgxpool.Pool already satisfies PgxQuerier verbatim — pgx.Tx does too.

// PostgresStorage persists plugin rows via INSERT/UPDATE/DELETE against
// the plugins table documented in doc.go.
//
// The migration that CREATEs the table is deferred (issue #6 / a
// follow-up; the schema is recorded in doc.go so the column contract is
// frozen). Calls against a database without the table will fail with
// the usual pgx UndefinedTable error at the first INSERT.
type PostgresStorage struct {
	db PgxQuerier

	// NowFunc, if set, replaces time.Now for InstalledAt / UpdatedAt
	// defaults the store fills in when the caller leaves them zero.
	NowFunc func() time.Time
}

// NewPostgresStorage wraps a *pgxpool.Pool. The pool's lifecycle is the
// caller's responsibility — the store does not call Close.
func NewPostgresStorage(pool *pgxpool.Pool) *PostgresStorage {
	if pool == nil {
		panic("lifecycle.NewPostgresStorage: pool is required")
	}
	return &PostgresStorage{db: pool}
}

// NewPostgresStorageWithQuerier is the test seam — it lets callers
// inject a fake or a pgx.Tx-wrapping adapter. Production code uses
// NewPostgresStorage.
func NewPostgresStorageWithQuerier(q PgxQuerier) *PostgresStorage {
	if q == nil {
		panic("lifecycle.NewPostgresStorage: querier is required")
	}
	return &PostgresStorage{db: q}
}

func (s *PostgresStorage) now() time.Time {
	if s.NowFunc != nil {
		return s.NowFunc()
	}
	return time.Now()
}

const insertSQL = `
INSERT INTO plugins (
    slug, version, abi_version, manifest, state, capabilities,
    last_error, error_at, installed_at, activated_at, row_version, updated_at
) VALUES (
    $1, $2, $3, $4::JSONB, $5, $6::JSONB,
    $7, NULLIF($8::TIMESTAMPTZ, 'epoch'::TIMESTAMPTZ), $9,
    NULLIF($10::TIMESTAMPTZ, 'epoch'::TIMESTAMPTZ), $11, $12
)
`

// Insert persists a new row. Translates the unique-violation error code
// into ErrAlreadyExists; everything else is returned wrapped.
func (s *PostgresStorage) Insert(ctx context.Context, p Plugin) error {
	if p.Slug == "" {
		return fmt.Errorf("lifecycle/postgres: Insert: slug is required")
	}
	if !p.State.Valid() {
		return fmt.Errorf("lifecycle/postgres: Insert: invalid state %q", p.State)
	}

	now := s.now().UTC()
	if p.InstalledAt.IsZero() {
		p.InstalledAt = now
	}
	if p.UpdatedAt.IsZero() {
		p.UpdatedAt = now
	}
	if p.RowVersion == 0 {
		p.RowVersion = 1
	}

	capsJSON, err := json.Marshal(p.Capabilities)
	if err != nil {
		return fmt.Errorf("lifecycle/postgres: marshal capabilities: %w", err)
	}
	if p.Capabilities == nil {
		// Send a JSON array literal rather than "null" so the column
		// default ('[]'::JSONB) is honored.
		capsJSON = []byte("[]")
	}

	manifest := p.Manifest
	if len(manifest) == 0 {
		manifest = []byte("{}")
	}

	tag, err := s.db.Exec(ctx, insertSQL,
		p.Slug, p.Version, p.ABIVersion, string(manifest), string(p.State), string(capsJSON),
		p.LastError, p.ErrorAt, p.InstalledAt, p.ActivatedAt, p.RowVersion, p.UpdatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%w: %q", ErrAlreadyExists, p.Slug)
		}
		return fmt.Errorf("lifecycle/postgres: insert: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("lifecycle/postgres: insert: expected 1 row, got %d", tag.RowsAffected())
	}
	return nil
}

const selectColumns = `
    slug, version, abi_version, manifest, state, capabilities,
    last_error,
    COALESCE(error_at,     'epoch'::TIMESTAMPTZ),
    installed_at,
    COALESCE(activated_at, 'epoch'::TIMESTAMPTZ),
    row_version, updated_at
`

// Get returns one row, or ErrNotFound.
func (s *PostgresStorage) Get(ctx context.Context, slug string) (Plugin, error) {
	row := s.db.QueryRow(ctx, `SELECT `+selectColumns+` FROM plugins WHERE slug = $1`, slug)
	p, err := scanPlugin(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Plugin{}, fmt.Errorf("%w: %q", ErrNotFound, slug)
		}
		return Plugin{}, fmt.Errorf("lifecycle/postgres: get: %w", err)
	}
	return p, nil
}

// List returns every plugin row, ordered by slug.
//
// On a clean install where the plugins table hasn't been created yet
// (SQLSTATE 42P01 — undefined_table), List returns an empty slice
// instead of an error. Semantically "no table" and "no rows" are the
// same thing for the lifecycle reader path: no plugins are installed.
// This keeps read-only callers (e.g. the admin sidebar's
// /api/v1/admin/plugin-pages endpoint) from failing with 500 on a
// fresh database before any plugin has been installed.
func (s *PostgresStorage) List(ctx context.Context) ([]Plugin, error) {
	rows, err := s.db.Query(ctx, `SELECT `+selectColumns+` FROM plugins ORDER BY slug ASC`)
	if err != nil {
		if isUndefinedTable(err) {
			return []Plugin{}, nil
		}
		return nil, fmt.Errorf("lifecycle/postgres: list: %w", err)
	}
	defer rows.Close()

	var out []Plugin
	for rows.Next() {
		p, err := scanPlugin(rows)
		if err != nil {
			return nil, fmt.Errorf("lifecycle/postgres: list scan: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("lifecycle/postgres: list rows: %w", err)
	}
	return out, nil
}

const updateStateSQL = `
UPDATE plugins
   SET state         = $1,
       row_version   = row_version + 1,
       updated_at    = $2,
       activated_at  = COALESCE($3, activated_at),
       last_error    = COALESCE($4, last_error),
       error_at      = COALESCE($5, error_at)
 WHERE slug = $6 AND state = $7
`

// UpdateState applies the conditional UPDATE. RowsAffected = 0 means
// either the row no longer exists or its state has been changed by a
// concurrent caller — both are ErrInvalidTransition from our point of
// view.
func (s *PostgresStorage) UpdateState(ctx context.Context, slug string, expectedFrom, newState State, fields *StateUpdateFields) error {
	if !newState.Valid() {
		return fmt.Errorf("lifecycle/postgres: UpdateState: invalid newState %q", newState)
	}
	if !expectedFrom.Valid() {
		return fmt.Errorf("lifecycle/postgres: UpdateState: invalid expectedFrom %q", expectedFrom)
	}

	var (
		activatedAt any = nil
		lastError   any = nil
		errorAt     any = nil
	)
	if fields != nil {
		if fields.ActivatedAt != nil {
			activatedAt = (*fields.ActivatedAt).UTC()
		}
		if fields.LastError != nil {
			lastError = *fields.LastError
		}
		if fields.ErrorAt != nil {
			t := (*fields.ErrorAt).UTC()
			errorAt = t
		}
	}

	tag, err := s.db.Exec(ctx, updateStateSQL,
		string(newState),
		s.now().UTC(),
		activatedAt, lastError, errorAt,
		slug, string(expectedFrom),
	)
	if err != nil {
		return fmt.Errorf("lifecycle/postgres: update state: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return transitionError(slug, "UpdateState", expectedFrom, "")
	}
	return nil
}

// Delete removes the row identified by slug. Returns ErrNotFound when
// no row was deleted (already gone, or never existed).
func (s *PostgresStorage) Delete(ctx context.Context, slug string) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM plugins WHERE slug = $1`, slug)
	if err != nil {
		return fmt.Errorf("lifecycle/postgres: delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: %q", ErrNotFound, slug)
	}
	return nil
}

// scanPlugin reads a single row. pgxScannable is satisfied by both
// pgx.Row and pgx.Rows.
type pgxScannable interface {
	Scan(dest ...any) error
}

func scanPlugin(s pgxScannable) (Plugin, error) {
	var (
		p           Plugin
		manifest    []byte
		caps        []byte
		state       string
		activatedAt time.Time
		errorAt     time.Time
	)
	if err := s.Scan(
		&p.Slug, &p.Version, &p.ABIVersion, &manifest, &state, &caps,
		&p.LastError, &errorAt, &p.InstalledAt, &activatedAt,
		&p.RowVersion, &p.UpdatedAt,
	); err != nil {
		return Plugin{}, err
	}
	p.State = State(state)
	if len(manifest) > 0 {
		p.Manifest = manifest
	}
	if len(caps) > 0 {
		if err := json.Unmarshal(caps, &p.Capabilities); err != nil {
			return Plugin{}, fmt.Errorf("unmarshal capabilities: %w", err)
		}
	}
	// 'epoch' sentinel = the column was NULL. Mirror the IsZero check
	// callers expect.
	if !isEpoch(activatedAt) {
		p.ActivatedAt = activatedAt
	}
	if !isEpoch(errorAt) {
		p.ErrorAt = errorAt
	}
	return p, nil
}

// isEpoch reports whether t is the SQL epoch — our chosen "this column
// was NULL" sentinel value. We do this rather than scanning into
// sql.NullTime because the field on Plugin is a plain time.Time and we
// want time.IsZero() to remain the caller-facing check.
func isEpoch(t time.Time) bool {
	return t.IsZero() || t.Unix() == 0
}

// isUniqueViolation reports whether err is a Postgres unique-violation
// (SQLSTATE 23505). The pgx PgError type carries the SQLSTATE; we
// errors.As against it so the check survives wrapping.
//
// We use a local interface alias (instead of importing pgconn.PgError
// directly) so PostgresStorage stays testable without pulling pgconn
// into test code. *pgconn.PgError satisfies sqlStater because pgconn's
// concrete type has a SQLState() method.
func isUniqueViolation(err error) bool {
	for ; err != nil; err = errors.Unwrap(err) {
		if s, ok := err.(sqlStater); ok {
			return s.SQLState() == "23505"
		}
	}
	return false
}

// isUndefinedTable reports whether err is a Postgres undefined-table
// (SQLSTATE 42P01). Used by List to treat a missing plugins table on a
// clean install as an empty result rather than a 500-worthy error.
func isUndefinedTable(err error) bool {
	for ; err != nil; err = errors.Unwrap(err) {
		if s, ok := err.(sqlStater); ok {
			return s.SQLState() == "42P01"
		}
	}
	return false
}

// sqlStater is the minimal interface our isUniqueViolation needs. The
// concrete *pgconn.PgError type implements it. Re-declaring it locally
// keeps PostgresStorage testable without importing pgconn solely for
// the type assertion.
type sqlStater interface {
	SQLState() string
}

// Ensure PostgresStorage satisfies Storage at compile time.
var _ Storage = (*PostgresStorage)(nil)
