package dbtest_test

import (
	"context"
	"testing"

	"github.com/Singleton-Solution/GoNext/packages/go/testutil/dbtest"
)

// TestExample_Documentation is the doctest. It's a complete, working
// usage of every public symbol in the package, kept compact enough
// that someone reading the source can grok the whole API at a
// glance. It runs as a regular test under -race so it's verified
// alongside the rest of the suite — that's the point of doctests:
// docs that can't lie.
func TestExample_Documentation(t *testing.T) {
	pool := setupPool(t)
	if pool == nil {
		return
	}
	ctx := context.Background()

	// 1. Start an isolated transaction. The cleanup is wired
	//    automatically — at the end of the test, everything written
	//    through tx is rolled back.
	tx := dbtest.BeginIsolated(t, pool)

	// 2. Seed fixtures inside the tx.
	dbtest.Seed(t, tx, `
		INSERT INTO widgets(label) VALUES ('alice'), ('bob');
	`)

	// 3. Drive a Querier-shaped store against the same tx via
	//    WrapPool. Production code typically takes a Querier so
	//    tests and production agree on the interface.
	store := &fakeStore{db: dbtest.WrapPool(tx)}
	if err := store.Insert(ctx, "carol"); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	// 4. Nest a savepoint around code under test that does its own
	//    Commit/Rollback. The savepoint catches both — the outer
	//    rollback at cleanup is still authoritative.
	inner := dbtest.Nest(t, tx)
	if _, err := inner.Exec(ctx, "INSERT INTO widgets(label) VALUES ('dave')"); err != nil {
		t.Fatalf("inner insert: %v", err)
	}
	if err := inner.Commit(ctx); err != nil { // RELEASE SAVEPOINT, not COMMIT TRANSACTION
		t.Fatalf("inner commit: %v", err)
	}

	// 5. Assert against the tx — the test sees its own writes.
	n, err := store.Count(ctx)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n != 4 {
		t.Errorf("got %d, want 4 (alice, bob, carol, dave)", n)
	}

	// 6. From outside the tx — using the bare pool — nothing has
	//    actually been committed.
	if got := countWidgets(t, pool); got != 0 {
		t.Errorf("outside tx: got %d, want 0", got)
	}

	// t.Cleanup will now rollback. The next test starts clean.
}
