package reusable

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Querier is the read/write surface shared by *pgxpool.Pool and pgx.Tx.
// Matches the same shape testutil/dbtest.Querier exposes so this store
// can be exercised inside a per-test transaction without ceremony.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// PgxStore is the production Store backed by Postgres via pgx.
type PgxStore struct {
	db Querier
}

// NewPgxStore wraps a Querier in the production Store. The pool/tx is
// borrowed (not owned); callers manage its lifecycle.
func NewPgxStore(db Querier) *PgxStore {
	return &PgxStore{db: db}
}

const insertSQL = `
INSERT INTO reusable_blocks (name, attrs, content)
VALUES ($1, $2, $3)
RETURNING id, created_at, updated_at
`

// Create inserts a new entry.
func (s *PgxStore) Create(ctx context.Context, e Entry) (Entry, error) {
	if err := validate(e); err != nil {
		return Entry{}, fmt.Errorf("%w: %s", ErrInvalidEntry, err.Error())
	}
	attrs := e.Attrs
	if len(attrs) == 0 {
		attrs = json.RawMessage(`{}`)
	}
	content := e.Content
	if len(content) == 0 {
		content = json.RawMessage(`[]`)
	}
	row := s.db.QueryRow(ctx, insertSQL, e.Name, attrs, content)
	if err := row.Scan(&e.ID, &e.CreatedAt, &e.UpdatedAt); err != nil {
		return Entry{}, fmt.Errorf("reusable: insert: %w", err)
	}
	e.Attrs = attrs
	e.Content = content
	return e, nil
}

const selectByIDSQL = `
SELECT id, name, attrs, content, created_at, updated_at
FROM reusable_blocks
WHERE id = $1
`

// Get fetches one entry by ID.
func (s *PgxStore) Get(ctx context.Context, id uuid.UUID) (Entry, error) {
	row := s.db.QueryRow(ctx, selectByIDSQL, id)
	out, err := scanEntry(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Entry{}, fmt.Errorf("%w: id=%s", ErrNotFound, id)
		}
		return Entry{}, fmt.Errorf("reusable: get: %w", err)
	}
	return out, nil
}

const updateSQL = `
UPDATE reusable_blocks
SET name = $2, attrs = $3, content = $4, updated_at = NOW()
WHERE id = $1
RETURNING created_at, updated_at
`

// Update mutates the editable fields of an existing entry.
func (s *PgxStore) Update(ctx context.Context, e Entry) (Entry, error) {
	if err := validate(e); err != nil {
		return Entry{}, fmt.Errorf("%w: %s", ErrInvalidEntry, err.Error())
	}
	if e.ID == uuid.Nil {
		return Entry{}, fmt.Errorf("%w: zero ID", ErrInvalidEntry)
	}
	attrs := e.Attrs
	if len(attrs) == 0 {
		attrs = json.RawMessage(`{}`)
	}
	content := e.Content
	if len(content) == 0 {
		content = json.RawMessage(`[]`)
	}
	row := s.db.QueryRow(ctx, updateSQL, e.ID, e.Name, attrs, content)
	if err := row.Scan(&e.CreatedAt, &e.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Entry{}, fmt.Errorf("%w: id=%s", ErrNotFound, e.ID)
		}
		return Entry{}, fmt.Errorf("reusable: update: %w", err)
	}
	e.Attrs = attrs
	e.Content = content
	return e, nil
}

const deleteSQL = `DELETE FROM reusable_blocks WHERE id = $1`

// Delete removes the entry with the given ID. Idempotent: deleting
// an unknown ID returns nil.
func (s *PgxStore) Delete(ctx context.Context, id uuid.UUID) error {
	if _, err := s.db.Exec(ctx, deleteSQL, id); err != nil {
		return fmt.Errorf("reusable: delete: %w", err)
	}
	return nil
}

const listBaseSQL = `
SELECT id, name, attrs, content, created_at, updated_at
FROM reusable_blocks
`

// List returns entries sorted by created_at DESC.
//
// We build the SQL with placeholder slots based on the filter rather
// than concatenating an unbounded WHERE — placeholder-only queries
// keep the prepared-statement cache warm and force every input
// through pgx parameterisation. The same pattern is used in
// redirects/store.go.
func (s *PgxStore) List(ctx context.Context, f ListFilter) ([]Entry, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	args := make([]any, 0, 3)
	where := ""
	if f.NameContains != "" {
		args = append(args, "%"+f.NameContains+"%")
		where = "WHERE name ILIKE $" + itoa(len(args))
	}
	if !f.Before.IsZero() {
		args = append(args, f.Before)
		clause := "created_at < $" + itoa(len(args))
		if where == "" {
			where = "WHERE " + clause
		} else {
			where += " AND " + clause
		}
	}
	args = append(args, limit)
	sql := listBaseSQL + where + " ORDER BY created_at DESC, id ASC LIMIT $" + itoa(len(args))

	rows, err := s.db.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("reusable: list: %w", err)
	}
	defer rows.Close()
	out := make([]Entry, 0, limit)
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, fmt.Errorf("reusable: list scan: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reusable: list rows: %w", err)
	}
	return out, nil
}

const getManySQL = `
SELECT id, name, attrs, content, created_at, updated_at
FROM reusable_blocks
WHERE id = ANY($1)
`

// GetMany fetches every entry whose ID is in ids. Missing IDs are
// silently omitted.
func (s *PgxStore) GetMany(ctx context.Context, ids []uuid.UUID) ([]Entry, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := s.db.Query(ctx, getManySQL, ids)
	if err != nil {
		return nil, fmt.Errorf("reusable: get_many: %w", err)
	}
	defer rows.Close()
	out := make([]Entry, 0, len(ids))
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, fmt.Errorf("reusable: get_many scan: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reusable: get_many rows: %w", err)
	}
	return out, nil
}

// scannable is the subset of pgx.Row / pgx.Rows that Scan needs.
type scannable interface {
	Scan(dest ...any) error
}

func scanEntry(row scannable) (Entry, error) {
	var e Entry
	if err := row.Scan(&e.ID, &e.Name, &e.Attrs, &e.Content, &e.CreatedAt, &e.UpdatedAt); err != nil {
		return Entry{}, err
	}
	return e, nil
}

// itoa is a small int → ascii helper used to build $N placeholders.
// We avoid strconv to keep this file's import set tight; the values
// here are small positive ints.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	if n < 0 {
		n = -n
	}
	var buf [4]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
