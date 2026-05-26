package themes

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ActiveThemeOptionKey is the options-table key that records the
// currently-active theme slug. Mirrors the constant of the same name
// in apps/api/internal/admin/customizer — re-declared here so this
// package doesn't pull customizer's surface into its own dependency
// graph (customizer is the read+overrides flow; this package is the
// install+switch flow, and the two are intentionally siblings).
const ActiveThemeOptionKey = "core.active_theme"

// ErrNoActiveTheme is returned by ActiveStore.Get when the options
// row is absent. The theme seeder writes it on first boot, so this
// path is reached only on a fresh database or after an explicit wipe.
var ErrNoActiveTheme = errors.New("themes: no active theme")

// ActiveStore reads + writes the active-theme slug on the options
// table. Two implementations exist: PgxActiveStore (production,
// wraps pgxpool.Pool) and MemoryActiveStore (tests).
type ActiveStore interface {
	// Get returns the slug stored under core.active_theme.
	// ErrNoActiveTheme when the row is missing.
	Get(ctx context.Context) (string, error)

	// Set upserts the active-theme slug. Callers validate that the
	// slug exists on disk before invoking this — the store does not
	// check the filesystem.
	Set(ctx context.Context, slug string) error
}

// readActiveSQL fetches the JSONB value (as text) of the active
// theme option. The seeder writes it as a JSONB string literal, so
// we extract the text via the #>> '{}' operator.
const readActiveSQL = `SELECT value #>> '{}' FROM options WHERE key = $1`

// writeActiveSQL upserts the active-theme row. The seeder writes
// JSONB; we mirror that here by passing the slug as a JSON string
// literal ("gn-hello" → JSON value "gn-hello"). autoload is TRUE
// because every renderer wakeup needs this key — keeping it in the
// autoload set avoids one cache miss per cold boot.
const writeActiveSQL = `
INSERT INTO options (key, value, autoload, namespace, updated_at)
VALUES ($1, to_jsonb($2::text), TRUE, 'core', now())
ON CONFLICT (key) DO UPDATE
    SET value = EXCLUDED.value,
        updated_at = now()
`

// PgxActiveStore is the production ActiveStore backed by pgx.
type PgxActiveStore struct {
	Pool *pgxpool.Pool
}

// Get implements ActiveStore.
func (s *PgxActiveStore) Get(ctx context.Context) (string, error) {
	if s.Pool == nil {
		return "", ErrNoActiveTheme
	}
	var slug string
	err := s.Pool.QueryRow(ctx, readActiveSQL, ActiveThemeOptionKey).Scan(&slug)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return "", ErrNoActiveTheme
	case err != nil:
		return "", fmt.Errorf("themes: read active: %w", err)
	}
	if slug == "" {
		return "", ErrNoActiveTheme
	}
	return slug, nil
}

// Set implements ActiveStore.
func (s *PgxActiveStore) Set(ctx context.Context, slug string) error {
	if s.Pool == nil {
		return errors.New("themes: pool is nil")
	}
	if _, err := s.Pool.Exec(ctx, writeActiveSQL, ActiveThemeOptionKey, slug); err != nil {
		return fmt.Errorf("themes: write active: %w", err)
	}
	return nil
}

// MemoryActiveStore is the test-friendly ActiveStore. Safe for
// concurrent use by tests that hit Mount with parallel requests.
type MemoryActiveStore struct {
	Slug string
}

// Get implements ActiveStore.
func (m *MemoryActiveStore) Get(_ context.Context) (string, error) {
	if m.Slug == "" {
		return "", ErrNoActiveTheme
	}
	return m.Slug, nil
}

// Set implements ActiveStore.
func (m *MemoryActiveStore) Set(_ context.Context, slug string) error {
	m.Slug = slug
	return nil
}
