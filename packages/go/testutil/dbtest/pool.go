package dbtest

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Querier is the read/write surface shared by *pgxpool.Pool and pgx.Tx.
// Code under test that depends on Querier (rather than *pgxpool.Pool
// directly) can be exercised against a per-test transaction without
// any extra wiring.
//
// This is the same shape as packages/go/settings's PgxQuerier and
// jobs/outbox's PoolQuerier — a deliberate convergence so production
// stores and the test substrate speak the same language. If a new
// store needs a method that isn't on Querier yet, add it here (and
// to the stores' local interface) rather than reaching for
// *pgxpool.Pool.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// WrapPool adapts a pgx.Tx to Querier so callers expecting "something
// that looks like *pgxpool.Pool" can drive it against a single
// transaction. The returned value's Exec/Query/QueryRow forward
// directly to tx — there is no buffering, no extra round-trip, and
// no state of its own.
//
// Why not just return tx? pgx.Tx satisfies Querier already, but the
// signature exists for two reasons:
//
//  1. Discoverability. A reader scanning a test file for "what is
//     this passed into the store?" sees dbtest.WrapPool(tx) and
//     immediately knows the intent — every operation goes through
//     the test's transaction.
//  2. Future-proofing. If a code path ever needs Begin/CopyFrom or a
//     pool-only method, the wrapper can grow without forcing every
//     call site to switch types.
//
// The returned txPool also exposes Begin so it satisfies the more
// general "pool that can start a sub-tx" contract some stores want;
// internally that Begin issues a SAVEPOINT, the same trick Nest uses.
func WrapPool(tx pgx.Tx) Querier {
	if tx == nil {
		return nil
	}
	return &txPool{tx: tx}
}

// txPool is the concrete adapter returned by WrapPool. Kept private
// so callers can't depend on its identity — the public contract is
// Querier.
type txPool struct {
	tx pgx.Tx
}

// Exec forwards to the underlying tx.
func (p *txPool) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if p.tx == nil {
		return pgconn.CommandTag{}, errors.New("dbtest: txPool.Exec called with nil tx")
	}
	return p.tx.Exec(ctx, sql, args...)
}

// Query forwards to the underlying tx.
func (p *txPool) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if p.tx == nil {
		return nil, errors.New("dbtest: txPool.Query called with nil tx")
	}
	return p.tx.Query(ctx, sql, args...)
}

// QueryRow forwards to the underlying tx. pgx.Row defers errors until
// Scan, so the nil-check returns a row that will surface a clear
// error on first use rather than panicking at the call site.
func (p *txPool) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if p.tx == nil {
		return errRow{err: errors.New("dbtest: txPool.QueryRow called with nil tx")}
	}
	return p.tx.QueryRow(ctx, sql, args...)
}

// Begin starts a nested transaction (savepoint) on the wrapped tx.
// Returned so the wrapper satisfies callers that conditionally want
// to Begin against "the pool". The savepoint reclaim is left to the
// returned pgx.Tx's Commit/Rollback in the usual way; see Nest for
// the test-helper variant that wires t.Cleanup automatically.
func (p *txPool) Begin(ctx context.Context) (pgx.Tx, error) {
	if p.tx == nil {
		return nil, errors.New("dbtest: txPool.Begin called with nil tx")
	}
	return p.tx.Begin(ctx)
}

// errRow is a pgx.Row that returns the captured error from Scan. Used
// only on the nil-tx defensive path; not part of the happy path.
type errRow struct{ err error }

func (r errRow) Scan(...any) error { return r.err }
