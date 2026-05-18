package dbtest_test

import (
	"context"
	"testing"

	"github.com/Singleton-Solution/GoNext/packages/go/testutil/dbtest"
)

// TestSeed_VisibleInsideTx_RolledBackOutside is the headline contract:
// Seed writes are visible to the test through tx, and disappear at
// cleanup.
func TestSeed_VisibleInsideTx_RolledBackOutside(t *testing.T) {
	pool := setupPool(t)
	if pool == nil {
		return
	}

	t.Run("seeded fixtures", func(t *testing.T) {
		tx := dbtest.BeginIsolated(t, pool)
		ctx := context.Background()

		dbtest.Seed(t, tx, `
			INSERT INTO widgets(label) VALUES ('seed-1'), ('seed-2'), ('seed-3');
		`)

		var n int
		if err := tx.QueryRow(ctx, "SELECT COUNT(*) FROM widgets").Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		if n != 3 {
			t.Errorf("inside tx after Seed: got %d, want 3", n)
		}
	})

	if got := countWidgets(t, pool); got != 0 {
		t.Errorf("after Seed test: got %d, want 0", got)
	}
}

// TestSeed_MultiStatement verifies that a fixture with several
// statements (separated by ;) is fed to Postgres in one go and all
// statements take effect.
func TestSeed_MultiStatement(t *testing.T) {
	pool := setupPool(t)
	if pool == nil {
		return
	}
	tx := dbtest.BeginIsolated(t, pool)
	ctx := context.Background()

	dbtest.Seed(t, tx, `
		INSERT INTO widgets(label) VALUES ('a');
		INSERT INTO widgets(label) VALUES ('b');
		INSERT INTO widgets(label) VALUES ('c');
	`)

	var n int
	if err := tx.QueryRow(ctx, "SELECT COUNT(*) FROM widgets").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 3 {
		t.Errorf("got %d, want 3", n)
	}
}

// TestSeed_EmptyIsNoOp documents that Seed("") and Seed("  ") are
// silently ignored. This lets callers pass a constant fixture string
// that may be empty for some cases.
func TestSeed_EmptyIsNoOp(t *testing.T) {
	pool := setupPool(t)
	if pool == nil {
		return
	}
	tx := dbtest.BeginIsolated(t, pool)

	dbtest.Seed(t, tx, "")
	dbtest.Seed(t, tx, "   \n\t  ")

	// If either had attempted to Exec, we'd have an error or stale
	// state — the assertion is just "no t.Fatal".
}
