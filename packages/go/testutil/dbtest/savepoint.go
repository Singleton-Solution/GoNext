package dbtest

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

// Nest creates a savepoint inside an outer transaction and returns a
// pgx.Tx scoped to it. Use this when the code under test does its own
// Commit or Rollback — the savepoint catches both:
//
//   - inner.Commit() in the code under test releases the savepoint
//     (RELEASE SAVEPOINT) without touching the outer transaction. The
//     outer rollback in BeginIsolated still discards the work.
//   - inner.Rollback() in the code under test rolls back to the
//     savepoint, leaving the outer tx state intact up to the
//     savepoint boundary.
//
// In other words, the savepoint makes a piece of code that genuinely
// needs to commit safely composable with the per-test isolation
// pattern.
//
// pgx's Tx.Begin() is documented as "starts a pseudo nested
// transaction" implemented with a SAVEPOINT, which is exactly what
// we want. Nest is a thin, type-safer wrapper that also registers a
// best-effort cleanup rollback so the savepoint is reclaimed even if
// the code under test forgot to commit/rollback. The outer tx's
// cleanup will reclaim it too, but doing it locally keeps the
// connection state tidy if multiple Nests are layered.
//
// Failure modes mirror BeginIsolated:
//   - Begin (savepoint) fails: t.Fatalf.
//   - Cleanup rollback returns ErrTxClosed: swallowed.
//   - Cleanup rollback returns anything else: t.Logf, no t.Fail.
func Nest(t testing.TB, tx pgx.Tx) pgx.Tx {
	t.Helper()
	if tx == nil {
		t.Fatal("dbtest.Nest: tx is nil")
	}

	sp, err := tx.Begin(context.Background())
	if err != nil {
		t.Fatalf("dbtest.Nest: savepoint: %v", err)
	}

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), rollbackTimeout)
		defer cancel()
		err := sp.Rollback(ctx)
		switch {
		case err == nil:
		case errors.Is(err, pgx.ErrTxClosed):
			// Test already committed or rolled back the savepoint —
			// fine, the outer tx will discard everything anyway.
		default:
			t.Logf("dbtest.Nest: savepoint rollback at cleanup: %v", err)
		}
	})

	return sp
}
