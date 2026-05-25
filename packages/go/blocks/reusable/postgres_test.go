package reusable

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	migratepg "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/Singleton-Solution/GoNext/packages/go/testutil/containers"
)

type pgFixture struct {
	pool *pgxpool.Pool
	dsn  string
}

func newPgFixture(t *testing.T) *pgFixture {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: skip with -short")
	}
	dsn := containers.Postgres(t)
	if dsn == "" {
		t.Skip("docker not available")
	}
	dir := repoMigrationsDir(t)
	if err := migrateUp(dsn, dir); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	return &pgFixture{pool: pool, dsn: dsn}
}

func repoMigrationsDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// packages/go/blocks/reusable → up four to repo root.
	dir := filepath.Join(wd, "..", "..", "..", "..", "migrations")
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("migrations dir not found at %s: %v", dir, err)
	}
	return dir
}

func migrateUp(dsn, dir string) error {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer func() { _ = db.Close() }()
	driver, err := migratepg.WithInstance(db, &migratepg.Config{})
	if err != nil {
		return fmt.Errorf("driver: %w", err)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("abs: %w", err)
	}
	m, err := migrate.NewWithDatabaseInstance("file://"+filepath.ToSlash(abs), "postgres", driver)
	if err != nil {
		return fmt.Errorf("new: %w", err)
	}
	m.Log = quietLogger{}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("up: %w", err)
	}
	return nil
}

type quietLogger struct{}

func (quietLogger) Printf(string, ...interface{}) {}
func (quietLogger) Verbose() bool                  { return false }

func TestPgxStore_CreateRoundTrip(t *testing.T) {
	fx := newPgFixture(t)
	store := NewPgxStore(fx.pool)
	ctx := context.Background()

	created, err := store.Create(ctx, Entry{
		Name:    "Hero CTA",
		Attrs:   json.RawMessage(`{"icon":"star"}`),
		Content: json.RawMessage(`[{"type":"core/paragraph","attributes":{"text":"hi"}}]`),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == uuid.Nil {
		t.Fatal("Create did not return an ID")
	}
	if created.CreatedAt.IsZero() {
		t.Fatal("CreatedAt not set")
	}

	got, err := store.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "Hero CTA" {
		t.Fatalf("Get returned %+v", got)
	}
}

func TestPgxStore_UpdateAndDelete(t *testing.T) {
	fx := newPgFixture(t)
	store := NewPgxStore(fx.pool)
	ctx := context.Background()

	created, err := store.Create(ctx, Entry{Name: "v1"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	updated := created
	updated.Name = "v2"
	updated.Content = json.RawMessage(`[{"type":"core/heading","attributes":{"text":"new"}}]`)
	got, err := store.Update(ctx, updated)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.Name != "v2" {
		t.Fatalf("Update did not change name: %+v", got)
	}
	if !got.UpdatedAt.After(created.UpdatedAt) && !got.UpdatedAt.Equal(created.UpdatedAt) {
		t.Fatalf("UpdatedAt regressed: %v -> %v", created.UpdatedAt, got.UpdatedAt)
	}

	if err := store.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Get(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after Delete: expected ErrNotFound, got %v", err)
	}
	// Idempotent.
	if err := store.Delete(ctx, created.ID); err != nil {
		t.Fatalf("idempotent Delete: %v", err)
	}
}

func TestPgxStore_ListAndFilter(t *testing.T) {
	fx := newPgFixture(t)
	store := NewPgxStore(fx.pool)
	ctx := context.Background()
	for _, n := range []string{"alpha", "beta-1", "BETA-2", "gamma"} {
		if _, err := store.Create(ctx, Entry{Name: n}); err != nil {
			t.Fatalf("Create %q: %v", n, err)
		}
	}

	all, err := store.List(ctx, ListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) < 4 {
		t.Fatalf("List len = %d, want >= 4", len(all))
	}

	// ILIKE substring filter.
	filt, err := store.List(ctx, ListFilter{NameContains: "beta"})
	if err != nil {
		t.Fatalf("List filt: %v", err)
	}
	if len(filt) != 2 {
		t.Fatalf("filtered len = %d, want 2", len(filt))
	}
}

func TestPgxStore_GetMany(t *testing.T) {
	fx := newPgFixture(t)
	store := NewPgxStore(fx.pool)
	ctx := context.Background()

	ids := make([]uuid.UUID, 3)
	for i := range ids {
		e, err := store.Create(ctx, Entry{Name: "n"})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		ids[i] = e.ID
	}

	got, err := store.GetMany(ctx, append(ids, uuid.New()))
	if err != nil {
		t.Fatalf("GetMany: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("GetMany returned %d, want 3", len(got))
	}
}

func TestPgxStore_ResolveRefsRoundTrip(t *testing.T) {
	fx := newPgFixture(t)
	store := NewPgxStore(fx.pool)
	ctx := context.Background()

	inner := []BlockNode{
		{Type: "core/paragraph", Attributes: mustEncode(t, map[string]string{"text": "inside"})},
	}
	entry, err := store.Create(ctx, Entry{
		Name:    "snippet",
		Content: mustEncode(t, inner),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	input := []BlockNode{
		{Type: RefBlockType, Attributes: mustEncode(t, map[string]string{"ref": entry.ID.String()})},
	}
	resolved, err := ResolveRefs(ctx, store, input)
	if err != nil {
		t.Fatalf("ResolveRefs: %v", err)
	}
	if len(resolved) != 1 || resolved[0].Type != "core/paragraph" {
		t.Fatalf("resolve mismatch: %+v", resolved)
	}
}
