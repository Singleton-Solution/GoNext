package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgxQuerier is the subset of *pgxpool.Pool that PostgresStore uses.
// Exposed (rather than taking *pgxpool.Pool directly) so tests can
// drive the store with a fake and so callers can pass a pgx.Tx when
// they need to read/write settings inside a larger transaction.
type PgxQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgxCommandTag, error)
}

// pgxCommandTag is a tiny shim so PgxQuerier's Exec signature doesn't
// drag pgconn into this file (the canonical CommandTag lives in
// github.com/jackc/pgx/v5/pgconn, but for our purposes we only need
// "did it succeed", which the bare interface gives us). Production
// callers pass a *pgxpool.Pool which satisfies the broader pgx.Pool
// surface; this shim lets us keep the fake querier in tests minimal.
//
// Why not just `any`? Because `any` accepts a `nil` return from a
// broken mock and the call site would crash on `.RowsAffected()`. A
// named (interface) type keeps the contract explicit.
type pgxCommandTag interface {
	RowsAffected() int64
}

// poolAdapter wraps *pgxpool.Pool to satisfy PgxQuerier. The pool's
// Exec returns a pgconn.CommandTag (a struct), which already has a
// RowsAffected() int64 method, so the adapter is just a thin
// signature-only wrapper.
type poolAdapter struct{ pool *pgxpool.Pool }

func (a poolAdapter) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return a.pool.QueryRow(ctx, sql, args...)
}
func (a poolAdapter) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return a.pool.Query(ctx, sql, args...)
}
func (a poolAdapter) Exec(ctx context.Context, sql string, args ...any) (pgxCommandTag, error) {
	tag, err := a.pool.Exec(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	// pgconn.CommandTag is a struct value that has RowsAffected() —
	// wrap it in a tiny tagWrapper rather than returning it directly,
	// so the interface contract is satisfied at the method level.
	return tagWrapper{tag.RowsAffected()}, nil
}

// tagWrapper is the no-op implementation of pgxCommandTag that the
// pool adapter returns. It carries only the rows-affected count, which
// is the only Exec-result datum PostgresStore actually inspects.
type tagWrapper struct{ rows int64 }

func (t tagWrapper) RowsAffected() int64 { return t.rows }

// PostgresStore reads and writes the options table created by
// migration 000008. The table shape is documented in
// docs/01-core-cms.md §10.11:
//
//	CREATE TABLE options (
//	    key             CITEXT PRIMARY KEY,
//	    value           JSONB NOT NULL,
//	    autoload        BOOLEAN NOT NULL DEFAULT FALSE,
//	    is_protected    BOOLEAN NOT NULL DEFAULT FALSE,
//	    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
//	    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
//	    version         INTEGER NOT NULL DEFAULT 1
//	);
//
// The actual migration is 000008. Earlier drafts of this doc + the
// upsertSQL referenced a `namespace` column that never made it into
// the migration; the writer was 500'ing in production until the
// non-existent column was stripped. See `namespaceFor` below for
// the un-shipped per-plugin uninstall convention.
//
// The store maintains an L1 cache (a sync.Map keyed by setting key)
// so the hot Read path doesn't round-trip to Postgres. Write
// invalidates the relevant entry as part of the persistence path —
// there is intentionally no TTL on the cache because settings change
// rarely (operator action only), and a stale Read could surface as
// "I changed the site name and the admin still shows the old one",
// which is the kind of bug that makes operators distrust the platform.
type PostgresStore struct {
	db       PgxQuerier
	registry *Registry

	// cache is the L1: sync.Map[string]any. Used as a map of key to
	// "the value as last read or written through this store". A nil
	// value indicates "we've seen this key and it was absent in the
	// store" — distinct from "we've never looked" (no map entry). The
	// distinction matters because Read should NOT round-trip to
	// Postgres if a previous Read already established the key wasn't
	// present; it should return the default. We use a sentinel because
	// sync.Map can't distinguish "stored nil" from "not present".
	cache sync.Map
}

// pgCacheTombstone is the sentinel stored in the L1 cache to indicate
// "this key exists in the registry but is not present in the options
// table". Storing a tombstone (rather than just leaving the cache
// empty) lets us short-circuit subsequent Reads of unset keys without
// a second SQL round trip.
type pgCacheTombstone struct{}

// NewPostgresStore wraps a *pgxpool.Pool. The pool's lifecycle is the
// caller's responsibility — the store does not call Close on it.
func NewPostgresStore(pool *pgxpool.Pool, reg *Registry) *PostgresStore {
	return &PostgresStore{
		db:       poolAdapter{pool: pool},
		registry: reg,
	}
}

// NewPostgresStoreWithQuerier is the test seam: it lets callers swap
// in a fake or a pgx.Tx. Production code should use NewPostgresStore.
func NewPostgresStoreWithQuerier(q PgxQuerier, reg *Registry) *PostgresStore {
	return &PostgresStore{db: q, registry: reg}
}

// readSQL fetches a single value by key from the options table.
//
// We don't filter by namespace because the key is already
// namespace-prefixed by convention (core.*, plugin:<slug>.*). The
// `namespace` column exists for migration tooling and per-plugin
// uninstall queries (DELETE WHERE namespace = 'plugin:foo'); the hot
// read path doesn't need it.
const readSQL = `SELECT value FROM options WHERE key = $1`

// upsertSQL writes a value with its autoload bit from the registry.
// Postgres's ON CONFLICT lets us keep this single statement instead
// of branching SELECT-then-INSERT-or-UPDATE on the caller side, which
// also avoids the lost-update race window.
//
// updated_at is intentionally omitted from the INSERT column list:
// the options_touch trigger (migration 000008) sets it on every
// UPDATE, and the column default (now()) covers the INSERT case.
// Setting it explicitly here would still work but is dead code.
const upsertSQL = `
INSERT INTO options (key, value, autoload)
VALUES ($1, $2, $3)
ON CONFLICT (key) DO UPDATE
    SET value = EXCLUDED.value,
        autoload = EXCLUDED.autoload
`

// autoloadSQL fetches every key with autoload = true. The boot path
// calls this once into a map and primes the in-memory cache. Order is
// arbitrary; the caller is a map insert anyway.
const autoloadSQL = `SELECT key, value FROM options WHERE autoload = TRUE`

// bulkReadSQL fetches multiple keys in one round trip. The keys are
// passed as a TEXT[] argument; ANY($1) is the standard pgx idiom for
// "key matches any of these".
const bulkReadSQL = `SELECT key, value FROM options WHERE key = ANY($1)`

// Read returns the current value for key, applying the registered
// Default if the key is not present in the options table. Hot path —
// goes through the L1 cache.
func (s *PostgresStore) Read(ctx context.Context, key string) (any, error) {
	entry, ok := s.registry.settingFor(key)
	if !ok {
		return nil, ErrUnknownKey
	}

	if cached, hit := s.cache.Load(key); hit {
		if _, tombstone := cached.(pgCacheTombstone); tombstone {
			return entry.Setting.Default, nil
		}
		return cached, nil
	}

	var raw []byte
	err := s.db.QueryRow(ctx, readSQL, key).Scan(&raw)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		s.cache.Store(key, pgCacheTombstone{})
		return entry.Setting.Default, nil
	case err != nil:
		return nil, fmt.Errorf("settings: read %q: %w", key, err)
	}

	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("settings: unmarshal %q: %w", key, err)
	}
	s.cache.Store(key, v)
	return v, nil
}

// Write validates value against the schema and persists it via
// upsertSQL. On success, invalidates the L1 cache entry — the next
// Read will re-fetch (or repopulate the cache from the in-flight
// value; we store the new value in the cache directly to keep the
// hot path warm).
func (s *PostgresStore) Write(ctx context.Context, key string, value any) error {
	entry, ok := s.registry.settingFor(key)
	if !ok {
		return ErrUnknownKey
	}
	if err := validate(entry, value); err != nil {
		return err
	}

	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("settings: marshal %q: %w", key, err)
	}

	tag, err := s.db.Exec(ctx, upsertSQL, key, encoded, entry.Setting.Autoload)
	if err != nil {
		// On Exec failure, the row may or may not have been written.
		// We invalidate the cache rather than seed it — the operator
		// will retry, and the next Read will be authoritative.
		s.cache.Delete(key)
		return fmt.Errorf("settings: write %q: %w", key, err)
	}
	_ = tag // RowsAffected is informational; ON CONFLICT always reports 1.

	// Cache the new value directly. We could just invalidate, but
	// "write then immediately read" is a common admin-UI shape (PUT
	// returns, GET runs to refresh) and seeding the cache here saves
	// a round trip.
	s.cache.Store(key, value)
	return nil
}

// BulkRead returns values for keys in one SQL round trip. Defaults
// are applied for keys absent from the options table.
func (s *PostgresStore) BulkRead(ctx context.Context, keys []string) (map[string]any, error) {
	out := make(map[string]any, len(keys))

	// First pass: pick up cache hits, collect misses.
	misses := keys[:0:0]
	for _, key := range keys {
		entry, ok := s.registry.settingFor(key)
		if !ok {
			continue
		}
		if cached, hit := s.cache.Load(key); hit {
			if _, tombstone := cached.(pgCacheTombstone); tombstone {
				out[key] = entry.Setting.Default
			} else {
				out[key] = cached
			}
			continue
		}
		misses = append(misses, key)
	}
	if len(misses) == 0 {
		return out, nil
	}

	rows, err := s.db.Query(ctx, bulkReadSQL, misses)
	if err != nil {
		return nil, fmt.Errorf("settings: bulk read: %w", err)
	}
	defer rows.Close()

	seen := make(map[string]struct{}, len(misses))
	for rows.Next() {
		var (
			key string
			raw []byte
		)
		if err := rows.Scan(&key, &raw); err != nil {
			return nil, fmt.Errorf("settings: bulk read scan: %w", err)
		}
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("settings: bulk read unmarshal %q: %w", key, err)
		}
		out[key] = v
		s.cache.Store(key, v)
		seen[key] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("settings: bulk read rows: %w", err)
	}

	// Any miss that didn't come back from the SQL is "not present" —
	// fall back to the registered Default and tombstone the cache.
	for _, key := range misses {
		if _, found := seen[key]; found {
			continue
		}
		entry, _ := s.registry.settingFor(key)
		out[key] = entry.Setting.Default
		s.cache.Store(key, pgCacheTombstone{})
	}

	return out, nil
}

// LoadAutoload returns the values for every Setting with Autoload=true,
// applying defaults for keys not yet in the options table. Called once
// at boot; the result also primes the L1 cache.
func (s *PostgresStore) LoadAutoload(ctx context.Context) (map[string]any, error) {
	// Build the set of expected autoload keys from the registry. We
	// intersect this with what SQL returns so a stale row in the options
	// table (an unregistered key marked autoload — possible after a
	// plugin uninstall) doesn't leak into the result.
	expected := make(map[string]any, 16)
	for _, setting := range s.registry.List() {
		if setting.Autoload {
			expected[setting.Key] = setting.Default
		}
	}

	rows, err := s.db.Query(ctx, autoloadSQL)
	if err != nil {
		return nil, fmt.Errorf("settings: load autoload: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			key string
			raw []byte
		)
		if err := rows.Scan(&key, &raw); err != nil {
			return nil, fmt.Errorf("settings: autoload scan: %w", err)
		}
		// Skip unregistered keys. See note above.
		if _, registered := expected[key]; !registered {
			continue
		}
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("settings: autoload unmarshal %q: %w", key, err)
		}
		expected[key] = v
		s.cache.Store(key, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("settings: autoload rows: %w", err)
	}

	return expected, nil
}

// Invalidate explicitly drops the L1 cache entry for key. Use it when
// an out-of-band mutation (a manual SQL update, a CLI tool that wrote
// directly to the table) has changed the canonical value and the
// store needs to refresh. Most callers should not need this — Write
// invalidates automatically.
func (s *PostgresStore) Invalidate(key string) {
	s.cache.Delete(key)
}

// InvalidateAll drops every L1 cache entry. Use it after a bulk import
// or restore.
func (s *PostgresStore) InvalidateAll() {
	s.cache.Range(func(k, _ any) bool {
		s.cache.Delete(k)
		return true
	})
}

// cacheLen returns the current L1 cache size. Test-only.
func (s *PostgresStore) cacheLen() int {
	n := 0
	s.cache.Range(func(_, _ any) bool {
		n++
		return true
	})
	return n
}

// cacheGet returns the current L1 cache entry for key, if any.
// Test-only.
func (s *PostgresStore) cacheGet(key string) (any, bool) {
	v, ok := s.cache.Load(key)
	return v, ok
}

// namespaceFor was used to populate the `options.namespace` column.
// That column does not exist in migration 000008 (the live schema);
// the column reference was unreviewed prose that shipped as code.
// The helper is intentionally retained — when the per-plugin uninstall
// sweep ships its own migration adding `namespace`, the writer can
// pass `namespaceFor(key)` back into the upsert without reinventing
// the convention. Keep this in lockstep with the docstring at the
// top of this file so plugin authors can predict what column they
// land under.
//
// Convention: "core.*" → "core"; "plugin:<slug>.*" or "<slug>.*" →
// "plugin:<slug>". Conservative — anything we can't classify becomes
// "core" rather than a guessed namespace.
func namespaceFor(key string) string {
	// Core keys.
	if len(key) >= 5 && key[:5] == "core." {
		return "core"
	}
	// Already-prefixed plugin keys (no dot before "plugin:" — explicit form).
	if len(key) >= 7 && key[:7] == "plugin:" {
		// e.g. "plugin:foo.bar" → "plugin:foo"
		for i := 7; i < len(key); i++ {
			if key[i] == '.' {
				return key[:i]
			}
		}
		return key
	}
	// Bare "<slug>.<rest>" — treat the first dotted segment as the
	// plugin slug. The slug-namespace convention is the plugin
	// reviewer's enforcement responsibility; this function just
	// classifies what was written.
	for i := 0; i < len(key); i++ {
		if key[i] == '.' {
			return "plugin:" + key[:i]
		}
	}
	return "core"
}

// Ensure PostgresStore satisfies Store at compile time.
var _ Store = (*PostgresStore)(nil)
