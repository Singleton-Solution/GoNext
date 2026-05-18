// Package dbtest gives every integration test its own private slice of
// Postgres by wrapping the test body in a transaction that is always
// rolled back at cleanup. Tests never see each other's writes, even
// when they share a single container.
//
// The problem: containers.Postgres (testutil/containers) hands every
// test in a package the same Postgres instance. Without isolation,
// test A's INSERTs leak into test B's reads — fixtures collide,
// ordering matters, and parallel tests fight over rows. The standard
// fix is "run each test inside a tx and roll it back at the end" —
// nothing the test does survives Cleanup, so the next test starts
// from the same baseline regardless of order.
//
// Typical use:
//
//	func TestSomething(t *testing.T) {
//	    pool := setupPostgres(t)               // shared across tests
//	    tx := dbtest.BeginIsolated(t, pool)    // private tx; rolled back at cleanup
//
//	    // Drive the code under test against tx (it satisfies
//	    // db.Querier and PgxQuerier — same QueryRow/Query/Exec
//	    // surface as *pgxpool.Pool).
//	    store := mything.NewStoreWithQuerier(tx)
//	    if err := store.Save(ctx, "x"); err != nil { ... }
//
//	    // Anything written here vanishes at t.Cleanup. No
//	    // TRUNCATE, no DELETE FROM, no test ordering.
//	}
//
// When the code under test runs its own COMMIT/ROLLBACK — repositories
// that take a *pgxpool.Pool and Begin internally, for example — wrap
// the outer tx in a savepoint:
//
//	tx := dbtest.BeginIsolated(t, pool)
//	inner := dbtest.Nest(t, tx)               // SAVEPOINT
//	store.RunBusinessTxn(ctx, inner)          // their Commit only
//	                                          // releases the savepoint;
//	                                          // outer rollback still
//	                                          // discards everything.
//
// Fixtures:
//
//	dbtest.Seed(t, tx, `INSERT INTO posts(...) VALUES (...)`)
//
// The fixture SQL runs inside the tx, so it disappears at cleanup
// just like the rest of the test's writes.
//
// What this package is NOT:
//
//   - It is not a replacement for end-to-end tests that exercise
//     real COMMIT-driven behaviour (replication, triggers that fire
//     on actual commit, LISTEN/NOTIFY). The outer rollback hides
//     anything that requires the WAL to flush.
//
//   - It does not parallelise tests. t.Parallel() inside a shared
//     pool is still safe (each test holds its own connection inside
//     its tx), but the pool must be sized accordingly — pgxpool's
//     MaxConns has to cover the parallel concurrency.
//
// See docs/11-testing-ci.md and the per-file comments below for the
// full rationale.
package dbtest
