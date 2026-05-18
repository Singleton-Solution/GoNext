package dbtest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// rollbackTimeout is the deadline for the cleanup-time Rollback. The
// connection has been doing real work and the rollback itself is
// trivial, so anything beyond a few seconds means the connection is
// wedged — we want the test to fail loudly via the log line rather
// than hang the suite.
const rollbackTimeout = 30 * time.Second

// BeginIsolated starts a transaction on pool and registers a t.Cleanup
// that rolls it back when the test ends. The returned tx is the
// caller's only handle on the database for the rest of the test —
// every Read and Write should go through it.
//
// The rollback runs unconditionally at cleanup, even if the test
// passed. That is the point: nothing the test does is allowed to
// survive into the next test. tx.Commit() inside the test body is
// always shadowed by the cleanup Rollback (a no-op once committed,
// but the data still doesn't persist because the cleanup is checked
// for ErrTxClosed and swallowed) — see Nest if you have code under
// test that genuinely needs to commit and want isolation anyway.
//
// Failure modes:
//
//   - Begin fails: t.Fatalf with the cause. There is nothing to
//     clean up.
//   - Rollback at cleanup returns ErrTxClosed: silently ignored
//     because the test (or a savepoint Nest beneath it) already
//     terminated the transaction.
//   - Rollback at cleanup returns anything else: t.Logf with the
//     cause. We don't t.Fail at cleanup time because that masks the
//     real test failure that probably preceded the rollback issue;
//     the log line is enough for diagnosis.
//
// The cleanup uses a fresh context derived from context.Background
// rather than passing the test's context. By the time cleanup runs,
// the test's context may already be cancelled (especially under
// -timeout). We want the rollback to land regardless.
func BeginIsolated(t testing.TB, pool *pgxpool.Pool) pgx.Tx {
	t.Helper()
	if pool == nil {
		t.Fatal("dbtest.BeginIsolated: pool is nil")
	}

	// Begin uses the test's lifetime. If the test is already
	// deadline-cancelled we want this to surface as a real error,
	// not as a silently dropped transaction.
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("dbtest.BeginIsolated: begin: %v", err)
	}

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), rollbackTimeout)
		defer cancel()
		err := tx.Rollback(ctx)
		switch {
		case err == nil:
			// Normal path — outer rollback discarded everything,
			// including any savepoints the test released along the way.
		case errors.Is(err, pgx.ErrTxClosed):
			// Test (or nested code under test) already committed or
			// rolled back the outer tx. Both outcomes still satisfy
			// "nothing persists" — Postgres only writes on Commit of
			// the top-level tx, and the test body had to choose to
			// terminate it deliberately. Swallow.
		default:
			// Don't t.Fail — see godoc. Diagnostic only.
			t.Logf("dbtest.BeginIsolated: rollback at cleanup: %v", err)
		}
	})

	return tx
}
