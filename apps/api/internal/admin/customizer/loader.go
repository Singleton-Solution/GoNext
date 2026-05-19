package customizer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Singleton-Solution/GoNext/packages/go/theme"
)

// FilesystemLoader returns a ThemeLoader that reads
// "<themeDir>/<slug>/theme.json" and runs theme.Parse on the bytes.
// Used by the production wiring in main.go; tests substitute their own
// ThemeLoader closure to avoid touching disk.
//
// We do NOT validate the parsed manifest here — the seeder already
// validated what landed on disk at boot, and a customizer GET should
// not 500 on a manifest the operator can't fix from this surface. The
// validator runs again when an override is submitted, against the
// merged value, so any drift surfaces at PUT time.
func FilesystemLoader(themeDir string) ThemeLoader {
	return func(_ context.Context, slug string) (*theme.ThemeJSON, error) {
		path := filepath.Join(themeDir, slug, "theme.json")
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read theme manifest %q: %w", path, err)
		}
		parsed, err := theme.Parse(data)
		if err != nil {
			return nil, fmt.Errorf("parse theme manifest %q: %w", path, err)
		}
		return parsed, nil
	}
}

// PoolAdapter wraps a *pgxpool.Pool so it satisfies the Querier
// interface used by PgxStore. The pool's Exec returns a
// pgconn.CommandTag value (which has RowsAffected() int64), but the
// signature mismatch with the CommandTag interface means we wrap the
// result in a one-field shim.
type PoolAdapter struct {
	Pool *pgxpool.Pool
}

// QueryRow forwards to the pool.
func (a PoolAdapter) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return a.Pool.QueryRow(ctx, sql, args...)
}

// Exec forwards to the pool, wrapping the resulting CommandTag in a
// signature-only shim.
func (a PoolAdapter) Exec(ctx context.Context, sql string, args ...any) (CommandTag, error) {
	tag, err := a.Pool.Exec(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return tagShim{rows: tag.RowsAffected()}, nil
}

// tagShim is the smallest CommandTag implementation: it carries only
// the rows-affected count, which is the only datum the store inspects.
type tagShim struct{ rows int64 }

// RowsAffected returns the rows-affected count.
func (t tagShim) RowsAffected() int64 { return t.rows }
