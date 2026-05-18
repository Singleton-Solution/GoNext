package dbtest_test

import (
	"context"
	"testing"

	"github.com/Singleton-Solution/GoNext/packages/go/testutil/dbtest"
)

// TestNest_InnerCommitDoesNotEscapeOuter covers the headline case:
// the code under test does its own Commit, but because we wrapped it
// in a savepoint, the outer tx's rollback still wipes the row.
func TestNest_InnerCommitDoesNotEscapeOuter(t *testing.T) {
	pool := setupPool(t)
	if pool == nil {
		return
	}

	t.Run("inner commit", func(t *testing.T) {
		outer := dbtest.BeginIsolated(t, pool)
		ctx := context.Background()

		// Simulate code under test: it gets a "tx" (really a
		// savepoint), writes, and commits. To it, the data is
		// durable. To the test, it'll vanish at cleanup.
		inner := dbtest.Nest(t, outer)
		if _, err := inner.Exec(ctx, "INSERT INTO widgets(label) VALUES ('inner-commit')"); err != nil {
			t.Fatalf("inner insert: %v", err)
		}
		if err := inner.Commit(ctx); err != nil {
			t.Fatalf("inner commit (release savepoint): %v", err)
		}

		// After the inner commit, the outer tx still sees the row
		// (the savepoint was released, not rolled back).
		var n int
		if err := outer.QueryRow(ctx, "SELECT COUNT(*) FROM widgets").Scan(&n); err != nil {
			t.Fatalf("count in outer: %v", err)
		}
		if n != 1 {
			t.Errorf("outer should see released savepoint row, got %d", n)
		}

		// From outside both txs, nothing is committed at the top
		// level — the row is still uncommitted.
		if got := countWidgets(t, pool); got != 0 {
			t.Errorf("outside: got %d, want 0 — inner commit must not escape outer tx", got)
		}
	})

	// After the subtest's cleanup → outer rollback, nothing persists
	// at the top level. This is the critical guarantee.
	if got := countWidgets(t, pool); got != 0 {
		t.Errorf("after subtest: got %d, want 0", got)
	}
}

// TestNest_InnerRollbackPreservesOuterUpToSavepoint is the other
// half of the savepoint contract: an inner Rollback rolls back to
// the savepoint, not past it. State written in the outer tx BEFORE
// the savepoint must still be visible after the inner rollback.
func TestNest_InnerRollbackPreservesOuterUpToSavepoint(t *testing.T) {
	pool := setupPool(t)
	if pool == nil {
		return
	}

	outer := dbtest.BeginIsolated(t, pool)
	ctx := context.Background()

	// Write something in the outer tx FIRST.
	if _, err := outer.Exec(ctx, "INSERT INTO widgets(label) VALUES ('outer-pre')"); err != nil {
		t.Fatalf("outer insert: %v", err)
	}

	// Now nest and write something destined to be rolled back.
	inner := dbtest.Nest(t, outer)
	if _, err := inner.Exec(ctx, "INSERT INTO widgets(label) VALUES ('inner-doomed')"); err != nil {
		t.Fatalf("inner insert: %v", err)
	}
	if err := inner.Rollback(ctx); err != nil {
		t.Fatalf("inner rollback: %v", err)
	}

	// The outer-pre row survives (it was before the savepoint), but
	// the inner-doomed row is gone.
	rows, err := outer.Query(ctx, "SELECT label FROM widgets ORDER BY id")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	var labels []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("scan: %v", err)
		}
		labels = append(labels, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}

	if len(labels) != 1 || labels[0] != "outer-pre" {
		t.Errorf("after inner rollback to savepoint: got %v, want [outer-pre]", labels)
	}
}

// TestNest_NestedSavepoints verifies a savepoint inside a savepoint
// also composes — at no point does any layer's commit/rollback leak
// to the top-level transaction.
func TestNest_NestedSavepoints(t *testing.T) {
	pool := setupPool(t)
	if pool == nil {
		return
	}

	outer := dbtest.BeginIsolated(t, pool)
	ctx := context.Background()

	level1 := dbtest.Nest(t, outer)
	if _, err := level1.Exec(ctx, "INSERT INTO widgets(label) VALUES ('lvl1')"); err != nil {
		t.Fatalf("lvl1 insert: %v", err)
	}

	level2 := dbtest.Nest(t, level1)
	if _, err := level2.Exec(ctx, "INSERT INTO widgets(label) VALUES ('lvl2')"); err != nil {
		t.Fatalf("lvl2 insert: %v", err)
	}
	// lvl2 commits — released into lvl1.
	if err := level2.Commit(ctx); err != nil {
		t.Fatalf("lvl2 commit: %v", err)
	}
	// lvl1 commits — released into outer.
	if err := level1.Commit(ctx); err != nil {
		t.Fatalf("lvl1 commit: %v", err)
	}

	// Both rows are visible inside outer.
	var n int
	if err := outer.QueryRow(ctx, "SELECT COUNT(*) FROM widgets").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Errorf("outer should see both rows, got %d", n)
	}

	// But not committed to the database.
	if got := countWidgets(t, pool); got != 0 {
		t.Errorf("outside: got %d, want 0", got)
	}
}
