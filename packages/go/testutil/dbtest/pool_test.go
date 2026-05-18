package dbtest_test

import (
	"context"
	"testing"

	"github.com/Singleton-Solution/GoNext/packages/go/testutil/dbtest"
)

// fakeStore is a tiny stand-in for the kind of repository code that
// takes a Querier-shaped argument and runs SQL against it. The real
// equivalents in the codebase are settings.PostgresStore,
// audit.PostgresStore, etc. — they all use the same Exec/Query
// surface.
type fakeStore struct{ db dbtest.Querier }

func (s *fakeStore) Insert(ctx context.Context, label string) error {
	_, err := s.db.Exec(ctx, "INSERT INTO widgets(label) VALUES ($1)", label)
	return err
}

func (s *fakeStore) Count(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRow(ctx, "SELECT COUNT(*) FROM widgets").Scan(&n)
	return n, err
}

// TestWrapPool_DrivesStoreCodeAgainstTx exercises the realistic case:
// a store written against a *pgxpool.Pool-like surface (Querier) runs
// against a transaction with no code changes. The test still gets
// auto-rollback.
func TestWrapPool_DrivesStoreCodeAgainstTx(t *testing.T) {
	pool := setupPool(t)
	if pool == nil {
		return
	}
	tx := dbtest.BeginIsolated(t, pool)
	ctx := context.Background()

	store := &fakeStore{db: dbtest.WrapPool(tx)}

	if err := store.Insert(ctx, "via-store"); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	got, err := store.Count(ctx)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if got != 1 {
		t.Errorf("inside tx: got %d, want 1", got)
	}

	// And of course, outside the tx, nothing exists.
	if outside := countWidgets(t, pool); outside != 0 {
		t.Errorf("outside tx: got %d, want 0", outside)
	}
}

// TestWrapPool_NilReturnsNil documents the defensive zero-value path.
func TestWrapPool_NilReturnsNil(t *testing.T) {
	if q := dbtest.WrapPool(nil); q != nil {
		t.Errorf("WrapPool(nil) = %v, want nil", q)
	}
}
