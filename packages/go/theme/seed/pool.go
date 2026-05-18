package seed

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PoolQuerier wraps a *pgxpool.Pool to satisfy PgxQuerier. The pool's
// own Exec returns a pgconn.CommandTag (a struct value) that already
// has the RowsAffected() int64 method, so the adapter is essentially
// a method-signature alignment — there is no runtime cost.
//
// Production callers reach for this directly:
//
//	s := &seed.Seeder{
//	    DB:       seed.PoolQuerier{Pool: pool},
//	    ThemeDir: cfg.Theme.Dir,
//	    SourceFS: seed.BundledThemes,
//	}
//
// Tests that drive the seeder with a fake or with a pgx.Tx skip the
// adapter and implement PgxQuerier directly.
type PoolQuerier struct {
	Pool *pgxpool.Pool
}

// QueryRow forwards to the underlying pool.
func (p PoolQuerier) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return p.Pool.QueryRow(ctx, sql, args...)
}

// Exec forwards to the pool and converts the returned
// pgconn.CommandTag (whose RowsAffected method already matches our
// CommandTag interface signature) into the interface value the
// seeder expects.
func (p PoolQuerier) Exec(ctx context.Context, sql string, args ...any) (CommandTag, error) {
	tag, err := p.Pool.Exec(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return pgconnTag{tag.RowsAffected()}, nil
}

// pgconnTag is a tiny carrier for the RowsAffected count. We don't
// return the pgconn.CommandTag value directly because that would
// leak a pgconn dependency into every caller's type signatures via
// the CommandTag interface return; carrying just the int64 keeps
// the interface contract minimal.
type pgconnTag struct{ rows int64 }

func (t pgconnTag) RowsAffected() int64 { return t.rows }
