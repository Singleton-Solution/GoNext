package customizer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ActiveThemeOptionKey is the options-table key where the seeder stores
// the slug of the active theme. Re-declared (rather than imported from
// theme/seed) so the customizer package does not pull the embed.FS that
// the seeder transitively brings in — the customizer is a read+write
// surface, not a bundler.
const ActiveThemeOptionKey = "core.active_theme"

// OverridesKeyPrefix is the prefix under which per-theme override blobs
// live. The full key is "theme_mods.<slug>"; we keep the prefix as a
// constant so audit log filters and the admin UI can both reference one
// canonical string.
const OverridesKeyPrefix = "theme_mods."

// OverridesKey returns the options-table key for the given theme slug.
// The slug is stored as-is — the caller is responsible for having
// already validated it against the active-theme row.
func OverridesKey(slug string) string {
	return OverridesKeyPrefix + slug
}

// ErrNoActiveTheme is returned by ActiveThemeSlug when the options row
// is absent. The seeder guarantees it on first boot, so this should
// only happen in tests that drive the customizer against an empty
// database or on a deployment whose seed has been wiped.
var ErrNoActiveTheme = errors.New("customizer: no active theme")

// Querier is the subset of pgxpool.Pool the store uses. Exposed as an
// interface so tests can drive the store with an in-process fake and so
// production callers can pass a transaction handle when they need the
// customizer reads + writes to share a tx with the surrounding work.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (CommandTag, error)
}

// CommandTag is the minimal Exec-result surface the store needs.
// pgconn.CommandTag already satisfies it directly; the pool adapter
// returns a small wrapper so the value-vs-pointer mismatch between the
// pool and this interface doesn't leak.
type CommandTag interface {
	RowsAffected() int64
}

// readActiveThemeSQL fetches the JSONB value of the active-theme row.
// We pull the raw text via #>> '{}' so the scan target is a Go string,
// matching the form the seeder writes (a bare JSONB text value).
const readActiveThemeSQL = `SELECT value #>> '{}' FROM options WHERE key = $1`

// readOverridesSQL fetches the JSONB blob for a given overrides key.
// The result is the whole JSON document so the caller can pass it
// through to clients without re-serializing.
const readOverridesSQL = `SELECT value FROM options WHERE key = $1`

// writeOverridesSQL upserts the overrides row. autoload is FALSE
// because the renderer reads via the per-request settings store cache
// path; autoloading at boot would prime cache with values the API-only
// replicas never need.
//
// `namespace` and explicit `updated_at` are omitted: the options
// table from migration 000008 has neither (`updated_at` defaults to
// now() and is bumped by the options_touch trigger). An earlier
// draft of this writer referenced both; PATCH was 500'ing in
// production before the fix.
const writeOverridesSQL = `
INSERT INTO options (key, value, autoload)
VALUES ($1, $2, FALSE)
ON CONFLICT (key) DO UPDATE
    SET value = EXCLUDED.value
`

// deleteOverridesSQL clears the overrides row for a slug. Used by the
// Reset action. Deleting (rather than writing an empty object) keeps
// the row count proportional to "themes ever customized" and matches
// what a clean install looks like.
const deleteOverridesSQL = `DELETE FROM options WHERE key = $1`

// Store is the persistence seam used by the handlers. Two concrete
// implementations exist: PgxStore (production) and MemoryStore
// (tests). The interface is small on purpose — every method has a
// single SQL statement behind it, and growing this surface means the
// handler has grown a database dependency that should be inspected.
type Store interface {
	// ActiveThemeSlug returns the slug stored under core.active_theme.
	// Returns ErrNoActiveTheme when the row is absent.
	ActiveThemeSlug(ctx context.Context) (string, error)

	// ReadOverrides returns the raw override JSON for the given theme
	// slug. A nil result with a nil error means "no overrides stored";
	// callers render the GET response with an empty object in that
	// case.
	ReadOverrides(ctx context.Context, slug string) (json.RawMessage, error)

	// WriteOverrides upserts the overrides blob for the slug. The
	// payload is JSON-encoded by the caller after validation.
	WriteOverrides(ctx context.Context, slug string, raw json.RawMessage) error

	// DeleteOverrides removes the overrides row for the slug. A
	// missing row is not an error — Reset is idempotent.
	DeleteOverrides(ctx context.Context, slug string) error
}

// PgxStore is the production Store backed by Postgres via pgx. Wrap a
// *pgxpool.Pool with PoolAdapter to satisfy the Querier interface; the
// store itself does not import pgxpool to keep test wiring lean.
type PgxStore struct {
	DB Querier
}

// NewPgxStore returns a PgxStore writing through q. Production callers
// pass PoolAdapter{pool}; tests pass an in-memory fake.
func NewPgxStore(q Querier) *PgxStore {
	return &PgxStore{DB: q}
}

// ActiveThemeSlug implements Store.
func (s *PgxStore) ActiveThemeSlug(ctx context.Context) (string, error) {
	var slug string
	err := s.DB.QueryRow(ctx, readActiveThemeSQL, ActiveThemeOptionKey).Scan(&slug)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return "", ErrNoActiveTheme
	case err != nil:
		return "", fmt.Errorf("customizer: read active theme: %w", err)
	}
	if slug == "" {
		return "", ErrNoActiveTheme
	}
	return slug, nil
}

// ReadOverrides implements Store.
func (s *PgxStore) ReadOverrides(ctx context.Context, slug string) (json.RawMessage, error) {
	var raw []byte
	err := s.DB.QueryRow(ctx, readOverridesSQL, OverridesKey(slug)).Scan(&raw)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("customizer: read overrides %q: %w", slug, err)
	}
	return json.RawMessage(raw), nil
}

// WriteOverrides implements Store.
func (s *PgxStore) WriteOverrides(ctx context.Context, slug string, raw json.RawMessage) error {
	if len(raw) == 0 {
		// An empty payload would write `null` and we'd never round-trip
		// it to anything useful — the renderer would see "row exists,
		// merge null over manifest" which is a no-op masquerading as an
		// active customization. Delete instead.
		return s.DeleteOverrides(ctx, slug)
	}
	_, err := s.DB.Exec(ctx, writeOverridesSQL, OverridesKey(slug), []byte(raw))
	if err != nil {
		return fmt.Errorf("customizer: write overrides %q: %w", slug, err)
	}
	return nil
}

// DeleteOverrides implements Store.
func (s *PgxStore) DeleteOverrides(ctx context.Context, slug string) error {
	_, err := s.DB.Exec(ctx, deleteOverridesSQL, OverridesKey(slug))
	if err != nil {
		return fmt.Errorf("customizer: delete overrides %q: %w", slug, err)
	}
	return nil
}

// MemoryStore is the test-friendly Store. It is safe for concurrent use
// by handlers (Mount runs every request against the same value), so
// callers do not need a mutex of their own.
type MemoryStore struct {
	Slug      string
	Overrides map[string]json.RawMessage
}

// NewMemoryStore returns a MemoryStore with no overrides. Pass the
// active-theme slug; the customizer requires it to be set.
func NewMemoryStore(slug string) *MemoryStore {
	return &MemoryStore{Slug: slug, Overrides: map[string]json.RawMessage{}}
}

// ActiveThemeSlug implements Store.
func (m *MemoryStore) ActiveThemeSlug(_ context.Context) (string, error) {
	if m.Slug == "" {
		return "", ErrNoActiveTheme
	}
	return m.Slug, nil
}

// ReadOverrides implements Store.
func (m *MemoryStore) ReadOverrides(_ context.Context, slug string) (json.RawMessage, error) {
	v, ok := m.Overrides[slug]
	if !ok {
		return nil, nil
	}
	return v, nil
}

// WriteOverrides implements Store.
func (m *MemoryStore) WriteOverrides(_ context.Context, slug string, raw json.RawMessage) error {
	if len(raw) == 0 {
		delete(m.Overrides, slug)
		return nil
	}
	// Copy the buffer so a caller mutating the slice later doesn't
	// retroactively corrupt the store.
	dup := make(json.RawMessage, len(raw))
	copy(dup, raw)
	m.Overrides[slug] = dup
	return nil
}

// DeleteOverrides implements Store.
func (m *MemoryStore) DeleteOverrides(_ context.Context, slug string) error {
	delete(m.Overrides, slug)
	return nil
}
