package containers_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/testutil/containers"

	// pgx stdlib driver — registered as "pgx" with database/sql.
	_ "github.com/jackc/pgx/v5/stdlib"
)

// TestPostgres_AcceptsConnections starts a real Postgres container and
// runs SELECT 1 against it. If Docker isn't available the helper skips
// the test cleanly — there's no way to assert behaviour against a
// container we can't start, and an integration test that "passes"
// without actually integrating would be a lie.
func TestPostgres_AcceptsConnections(t *testing.T) {
	dsn := containers.Postgres(t)
	if dsn == "" {
		// helper called t.Skip — t is already marked skipped, just bail.
		return
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var got int
	if err := db.QueryRowContext(ctx, "SELECT 1").Scan(&got); err != nil {
		t.Fatalf("SELECT 1: %v", err)
	}
	if got != 1 {
		t.Fatalf("SELECT 1: got %d, want 1", got)
	}
}

// TestPostgres_WithDB checks that WithDB actually changes the initial
// database — current_database() should match what we asked for, not the
// "gonext_test" default.
func TestPostgres_WithDB(t *testing.T) {
	const dbName = "orders_test"

	dsn := containers.Postgres(t, containers.WithDB(dbName))
	if dsn == "" {
		return
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var got string
	if err := db.QueryRowContext(ctx, "SELECT current_database()").Scan(&got); err != nil {
		t.Fatalf("current_database: %v", err)
	}
	if got != dbName {
		t.Fatalf("current_database = %q, want %q", got, dbName)
	}
}
