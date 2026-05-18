package marketplace

import (
	"context"
	"crypto/sha256"
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
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/Singleton-Solution/GoNext/packages/go/testutil/containers"
)

// =============================================================================
// Test fixture
// =============================================================================

// fixture is the per-test integration harness: a Postgres container,
// the full migration tree applied, an open pgxpool. Skipped when
// Docker isn't available; we run with the real Postgres because the
// schema-side CHECK constraints, UNIQUE indexes, and trigger
// behaviour are part of what these tests are actually testing.
type fixture struct {
	pool *pgxpool.Pool
	dsn  string
	dir  string
}

func newFixture(t *testing.T) *fixture {
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
	return &fixture{pool: pool, dsn: dsn, dir: dir}
}

// repoMigrationsDir resolves /migrations from the repo root.
// This file lives at packages/go/plugins/marketplace/store_test.go,
// so up four levels reaches the repo root.
func repoMigrationsDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	dir := filepath.Join(wd, "..", "..", "..", "..", "migrations")
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("migrations dir not found at %s: %v", dir, err)
	}
	return dir
}

// migrateUp applies every migration in dir against dsn. We don't
// reach for the migrate package's Run() because it depends on
// config.DatabaseConfig and pulls in a logger setup we don't need.
// The raw golang-migrate API is short and self-contained.
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
	m.Log = quietMigrateLogger{}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("up: %w", err)
	}
	return nil
}

// migrateDown rolls back every migration in dir against dsn.
func migrateDown(dsn, dir string) error {
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
	m.Log = quietMigrateLogger{}
	if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("down: %w", err)
	}
	return nil
}

// quietMigrateLogger discards migrate's progress chatter. The
// integration tests need a clean -v output.
type quietMigrateLogger struct{}

func (quietMigrateLogger) Printf(string, ...interface{}) {}
func (quietMigrateLogger) Verbose() bool                 { return false }

// seedUser inserts a row in `users` and returns its id. The users
// table has its own NOT NULL columns (email, handle) — we mint
// unique-per-call values so multiple seeded users in one test don't
// collide.
func seedUser(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	suffix := uuid.New().String()[:8]
	err := pool.QueryRow(context.Background(), `
		INSERT INTO users (email, handle)
		VALUES ($1, $2)
		RETURNING id
	`,
		"tester-"+suffix+"@example.com",
		"tester-"+suffix,
	).Scan(&id)
	if err != nil {
		t.Fatalf("seedUser: %v", err)
	}
	return id
}

// =============================================================================
// Validation tests (no Docker required)
// =============================================================================

func TestListingStatus_Valid(t *testing.T) {
	for _, s := range []ListingStatus{ListingDraft, ListingListed, ListingDelisted, ListingBanned} {
		if !s.Valid() {
			t.Errorf("%q should be valid", s)
		}
	}
	if ListingStatus("nope").Valid() {
		t.Error("nope should not be valid")
	}
	if ListingStatus("").Valid() {
		t.Error("empty should not be valid")
	}
}

func TestInstallEventType_Valid(t *testing.T) {
	for _, e := range []InstallEventType{EventInstalled, EventActivated, EventUninstalled, EventErrored} {
		if !e.Valid() {
			t.Errorf("%q should be valid", e)
		}
	}
	if InstallEventType("nope").Valid() {
		t.Error("nope should not be valid")
	}
}

func TestListings_Create_RejectsEmptySlug(t *testing.T) {
	l := NewListings(&panicQuerier{})
	_, err := l.Create(context.Background(), Listing{Name: "x"})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("got %v want ErrInvalidInput", err)
	}
}

func TestListings_Create_RejectsEmptyName(t *testing.T) {
	l := NewListings(&panicQuerier{})
	_, err := l.Create(context.Background(), Listing{Slug: "x"})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("got %v want ErrInvalidInput", err)
	}
}

func TestListings_Create_RejectsBadStatus(t *testing.T) {
	l := NewListings(&panicQuerier{})
	_, err := l.Create(context.Background(), Listing{Slug: "x", Name: "y", Status: "weird"})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("got %v want ErrInvalidInput", err)
	}
}

func TestVersions_Publish_RequiresWasm(t *testing.T) {
	v := NewVersions(&panicQuerier{})
	_, err := v.Publish(context.Background(), Version{
		ListingID: uuid.New(), Version: "1.0.0",
	}, nil)
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("got %v want ErrInvalidInput", err)
	}
}

func TestVersions_Publish_RequiresListing(t *testing.T) {
	v := NewVersions(&panicQuerier{})
	_, err := v.Publish(context.Background(), Version{
		Version: "1.0.0",
	}, []byte("hello"))
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("got %v want ErrInvalidInput", err)
	}
}

func TestRatings_Submit_RejectsOutOfRangeStars(t *testing.T) {
	r := NewRatings(&panicQuerier{})
	for _, s := range []int16{0, -1, 6, 100} {
		_, err := r.Submit(context.Background(), Rating{
			PluginVersionID: uuid.New(),
			UserID:          uuid.New(),
			Stars:           s,
		})
		if !errors.Is(err, ErrInvalidInput) {
			t.Errorf("stars=%d: got %v want ErrInvalidInput", s, err)
		}
	}
}

func TestEvents_RecordInstallEvent_RejectsEmptyHost(t *testing.T) {
	e := NewEvents(&panicQuerier{})
	_, err := e.RecordInstallEvent(context.Background(), InstallEvent{
		EventType: EventInstalled,
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("got %v want ErrInvalidInput", err)
	}
}

func TestEvents_RecordInstallEvent_RejectsBadEventType(t *testing.T) {
	e := NewEvents(&panicQuerier{})
	_, err := e.RecordInstallEvent(context.Background(), InstallEvent{
		HostID:    "h1",
		EventType: "weird",
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("got %v want ErrInvalidInput", err)
	}
}

func TestNewStore_PanicsOnNilPool(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic")
		}
	}()
	NewStore(nil)
}

func TestNewStoreWithQuerier_PanicsOnNilQuerier(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic")
		}
	}()
	NewStoreWithQuerier(nil)
}

func TestIsEpoch(t *testing.T) {
	if !isEpoch(time.Time{}) {
		t.Error("zero time should be epoch")
	}
	if !isEpoch(time.Unix(0, 0).UTC()) {
		t.Error("unix epoch should be epoch")
	}
	if isEpoch(time.Now()) {
		t.Error("now should not be epoch")
	}
}

func TestResolveNow_DefaultPath(t *testing.T) {
	got := resolveNow(nil)
	if got.IsZero() {
		t.Error("default resolveNow returned zero")
	}
}

// stubPgError simulates a Postgres error with a known SQLSTATE.
type stubPgError struct{ code string }

func (e *stubPgError) Error() string    { return "stub: " + e.code }
func (e *stubPgError) SQLState() string { return e.code }

func TestIsUniqueViolation(t *testing.T) {
	if !isUniqueViolation(&stubPgError{code: "23505"}) {
		t.Error("23505 should be detected")
	}
	if isUniqueViolation(&stubPgError{code: "23503"}) {
		t.Error("23503 (FK) should not match unique")
	}
	if isUniqueViolation(errors.New("plain")) {
		t.Error("plain error should not match")
	}
	wrapped := fmt.Errorf("wrapped: %w", &stubPgError{code: "23505"})
	if !isUniqueViolation(wrapped) {
		t.Error("wrapped 23505 should still be detected")
	}
	if isUniqueViolation(nil) {
		t.Error("nil should not match")
	}
}

func TestIsCheckViolation(t *testing.T) {
	if !isCheckViolation(&stubPgError{code: "23514"}) {
		t.Error("23514 should be detected")
	}
	if isCheckViolation(&stubPgError{code: "23505"}) {
		t.Error("23505 should not match check")
	}
}

// panicQuerier is a PgxQuerier that panics if any of its methods are
// called. Used by validation tests that expect to fail before any
// SQL runs.
type panicQuerier struct{}

func (panicQuerier) QueryRow(context.Context, string, ...any) pgx.Row {
	panic("panicQuerier: QueryRow should not be called")
}
func (panicQuerier) Query(context.Context, string, ...any) (pgx.Rows, error) {
	panic("panicQuerier: Query should not be called")
}
func (panicQuerier) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	panic("panicQuerier: Exec should not be called")
}

// =============================================================================
// Integration tests — Listings
// =============================================================================

func TestIntegration_Listings_CreateAndGet(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()
	store := NewStore(fx.pool)

	authorID := seedUser(t, fx.pool)

	created, err := store.Listings.Create(ctx, Listing{
		Slug:            "gn-seo",
		Name:            "GoNext SEO",
		Summary:         "SEO tooling for GoNext.",
		AuthorID:        authorID,
		HomepageURL:     "https://example.com/seo",
		LicenseSPDX:     "MIT",
		PrimaryCategory: "seo",
		Status:          ListingListed,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == uuid.Nil {
		t.Fatal("id was zero")
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Errorf("timestamps not stamped: %+v", created)
	}
	if created.AuthorID != authorID {
		t.Errorf("author: got %s want %s", created.AuthorID, authorID)
	}

	// Round-trip Get by id and by slug.
	got, err := store.Listings.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Slug != "gn-seo" || got.Name != "GoNext SEO" || got.Status != ListingListed {
		t.Errorf("got %+v", got)
	}

	bySlug, err := store.Listings.GetBySlug(ctx, "gn-seo")
	if err != nil {
		t.Fatalf("GetBySlug: %v", err)
	}
	if bySlug.ID != created.ID {
		t.Errorf("GetBySlug id mismatch: got %s want %s", bySlug.ID, created.ID)
	}
}

func TestIntegration_Listings_SlugUniqueness(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()
	store := NewStore(fx.pool)

	in := Listing{Slug: "gn-dup", Name: "Original"}
	if _, err := store.Listings.Create(ctx, in); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	_, err := store.Listings.Create(ctx, in)
	if !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("expected ErrAlreadyExists, got %v", err)
	}
}

func TestIntegration_Listings_GetNotFound(t *testing.T) {
	fx := newFixture(t)
	store := NewStore(fx.pool)
	_, err := store.Listings.Get(context.Background(), uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	_, err = store.Listings.GetBySlug(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestIntegration_Listings_ListByCategory(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()
	store := NewStore(fx.pool)

	if _, err := store.Listings.Create(ctx, Listing{
		Slug: "gn-seo-a", Name: "A", PrimaryCategory: "seo", Status: ListingListed,
	}); err != nil {
		t.Fatalf("create a: %v", err)
	}
	if _, err := store.Listings.Create(ctx, Listing{
		Slug: "gn-seo-b", Name: "B", PrimaryCategory: "seo", Status: ListingListed,
	}); err != nil {
		t.Fatalf("create b: %v", err)
	}
	// Same category but drafted → must NOT appear.
	if _, err := store.Listings.Create(ctx, Listing{
		Slug: "gn-seo-draft", Name: "D", PrimaryCategory: "seo", Status: ListingDraft,
	}); err != nil {
		t.Fatalf("create draft: %v", err)
	}
	// Different category.
	if _, err := store.Listings.Create(ctx, Listing{
		Slug: "gn-anl", Name: "Anl", PrimaryCategory: "analytics", Status: ListingListed,
	}); err != nil {
		t.Fatalf("create anl: %v", err)
	}

	got, err := store.Listings.ListByCategory(ctx, "seo")
	if err != nil {
		t.Fatalf("ListByCategory: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 listed seo rows, got %d", len(got))
	}
	for _, l := range got {
		if l.PrimaryCategory != "seo" || l.Status != ListingListed {
			t.Errorf("unexpected row: %+v", l)
		}
	}
}

func TestIntegration_Listings_Update(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()
	store := NewStore(fx.pool)

	created, err := store.Listings.Create(ctx, Listing{
		Slug: "gn-upd", Name: "Original", Status: ListingDraft,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	newName := "Renamed"
	newStatus := ListingListed
	updated, err := store.Listings.Update(ctx, created.ID, ListingUpdate{
		Name:   &newName,
		Status: &newStatus,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Name != "Renamed" || updated.Status != ListingListed {
		t.Errorf("update not applied: %+v", updated)
	}
	if !updated.UpdatedAt.After(created.UpdatedAt) {
		t.Errorf("UpdatedAt should advance (trigger): created=%v updated=%v",
			created.UpdatedAt, updated.UpdatedAt)
	}
}

func TestIntegration_Listings_Update_NotFound(t *testing.T) {
	fx := newFixture(t)
	store := NewStore(fx.pool)
	newName := "x"
	_, err := store.Listings.Update(context.Background(), uuid.New(), ListingUpdate{Name: &newName})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestIntegration_Listings_Delete(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()
	store := NewStore(fx.pool)

	created, err := store.Listings.Create(ctx, Listing{Slug: "gn-del", Name: "x"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.Listings.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Listings.Get(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound after Delete, got %v", err)
	}
	// Second delete is a no-op for the row but the contract returns
	// ErrNotFound.
	if err := store.Listings.Delete(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound on second delete, got %v", err)
	}
}

// =============================================================================
// Integration tests — Versions
// =============================================================================

func TestIntegration_Versions_PublishCapturesSHA256(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()
	store := NewStore(fx.pool)

	listing, err := store.Listings.Create(ctx, Listing{Slug: "gn-pub", Name: "x"})
	if err != nil {
		t.Fatalf("Create listing: %v", err)
	}

	wasm := []byte("\x00asm\x01\x00\x00\x00")
	expected := sha256.Sum256(wasm)

	pub, err := store.Versions.Publish(ctx, Version{
		ListingID:    listing.ID,
		Version:      "1.0.0",
		Manifest:     json.RawMessage(`{"slug":"gn-pub"}`),
		SignatureHex: "deadbeef",
	}, wasm)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(pub.WasmSHA256) != 32 {
		t.Fatalf("sha256 length: got %d want 32", len(pub.WasmSHA256))
	}
	for i := range expected {
		if pub.WasmSHA256[i] != expected[i] {
			t.Errorf("sha256 mismatch at byte %d: got %x want %x", i, pub.WasmSHA256[i], expected[i])
			break
		}
	}
	if pub.SignatureHex != "deadbeef" {
		t.Errorf("signature not captured: %q", pub.SignatureHex)
	}
	if pub.PublishedAt.IsZero() {
		t.Error("PublishedAt not set")
	}

	// Re-publish same (listing, version) → ErrAlreadyExists.
	_, err = store.Versions.Publish(ctx, Version{
		ListingID: listing.ID, Version: "1.0.0",
	}, wasm)
	if !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("expected ErrAlreadyExists, got %v", err)
	}
}

func TestIntegration_Versions_ListByListing(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()
	store := NewStore(fx.pool)

	listing, err := store.Listings.Create(ctx, Listing{Slug: "gn-list", Name: "x"})
	if err != nil {
		t.Fatalf("Create listing: %v", err)
	}

	for _, v := range []string{"1.0.0", "1.1.0", "2.0.0"} {
		// Distinct bytes per version so sha256 differs.
		wasm := []byte("wasm-" + v)
		if _, err := store.Versions.Publish(ctx, Version{
			ListingID: listing.ID, Version: v,
		}, wasm); err != nil {
			t.Fatalf("Publish %s: %v", v, err)
		}
		// Spread publish times to make the ORDER BY deterministic.
		time.Sleep(2 * time.Millisecond)
	}

	got, err := store.Versions.ListByListing(ctx, listing.ID)
	if err != nil {
		t.Fatalf("ListByListing: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 versions, got %d", len(got))
	}
	// DESC order = newest first.
	if got[0].Version != "2.0.0" || got[2].Version != "1.0.0" {
		t.Errorf("ordering: %v %v %v", got[0].Version, got[1].Version, got[2].Version)
	}
}

func TestIntegration_Versions_Deprecate(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()
	store := NewStore(fx.pool)

	listing, err := store.Listings.Create(ctx, Listing{Slug: "gn-dep", Name: "x"})
	if err != nil {
		t.Fatalf("Create listing: %v", err)
	}
	pub, err := store.Versions.Publish(ctx, Version{
		ListingID: listing.ID, Version: "1.0.0",
	}, []byte("wasm"))
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if !pub.DeprecatedAt.IsZero() {
		t.Error("DeprecatedAt should be zero before Deprecate")
	}

	dep, err := store.Versions.Deprecate(ctx, pub.ID)
	if err != nil {
		t.Fatalf("Deprecate: %v", err)
	}
	if dep.DeprecatedAt.IsZero() {
		t.Error("DeprecatedAt should be non-zero after Deprecate")
	}

	// Re-read confirms persistence.
	again, err := store.Versions.Get(ctx, pub.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if again.DeprecatedAt.IsZero() {
		t.Error("DeprecatedAt did not persist")
	}

	// Deprecating a missing version returns ErrNotFound.
	_, err = store.Versions.Deprecate(ctx, uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// =============================================================================
// Integration tests — Compat matrix
// =============================================================================

func TestIntegration_Compat_UpsertAndList(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()
	store := NewStore(fx.pool)

	listing, err := store.Listings.Create(ctx, Listing{Slug: "gn-cmt", Name: "x"})
	if err != nil {
		t.Fatalf("Create listing: %v", err)
	}
	ver, err := store.Versions.Publish(ctx, Version{
		ListingID: listing.ID, Version: "1.0.0",
	}, []byte("wasm"))
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// Two disjoint ranges → two rows.
	if err := store.Compat.Upsert(ctx, CompatRange{
		PluginVersionID: ver.ID,
		HostMin:         "1.0.0", HostMax: "1.9.9",
		Tested: true,
	}); err != nil {
		t.Fatalf("Upsert 1.x: %v", err)
	}
	if err := store.Compat.Upsert(ctx, CompatRange{
		PluginVersionID: ver.ID,
		HostMin:         "3.0.0", HostMax: "3.9.9",
		Tested: false,
	}); err != nil {
		t.Fatalf("Upsert 3.x: %v", err)
	}

	// Upserting the same tuple flips `tested` rather than inserting.
	if err := store.Compat.Upsert(ctx, CompatRange{
		PluginVersionID: ver.ID,
		HostMin:         "1.0.0", HostMax: "1.9.9",
		Tested: false,
	}); err != nil {
		t.Fatalf("Upsert flip: %v", err)
	}

	got, err := store.Compat.ListByVersion(ctx, ver.ID)
	if err != nil {
		t.Fatalf("ListByVersion: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(got))
	}
	// ORDER BY host_min: 1.x first, then 3.x.
	if got[0].HostMin != "1.0.0" || got[0].Tested != false {
		t.Errorf("row 0: %+v", got[0])
	}
	if got[1].HostMin != "3.0.0" {
		t.Errorf("row 1: %+v", got[1])
	}
}

func TestIntegration_Compat_RejectsInvertedRange(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()
	store := NewStore(fx.pool)

	listing, err := store.Listings.Create(ctx, Listing{Slug: "gn-inv", Name: "x"})
	if err != nil {
		t.Fatalf("Create listing: %v", err)
	}
	ver, err := store.Versions.Publish(ctx, Version{
		ListingID: listing.ID, Version: "1.0.0",
	}, []byte("wasm"))
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// min > max: rejected by the application layer's lex check
	// before SQL.
	err = store.Compat.Upsert(ctx, CompatRange{
		PluginVersionID: ver.ID,
		HostMin:         "z", HostMax: "a",
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput, got %v", err)
	}
}

// =============================================================================
// Integration tests — Ratings
// =============================================================================

func TestIntegration_Ratings_SubmitAndAggregate(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()
	store := NewStore(fx.pool)

	listing, err := store.Listings.Create(ctx, Listing{Slug: "gn-rate", Name: "x"})
	if err != nil {
		t.Fatalf("Create listing: %v", err)
	}
	ver, err := store.Versions.Publish(ctx, Version{
		ListingID: listing.ID, Version: "1.0.0",
	}, []byte("wasm"))
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// Empty case: zero rating returns zero aggregate, no error.
	agg, err := store.Ratings.Aggregate(ctx, ver.ID)
	if err != nil {
		t.Fatalf("empty Aggregate: %v", err)
	}
	if agg.Count != 0 || agg.Average != 0 {
		t.Errorf("empty agg: %+v", agg)
	}

	// Three users, three stars (4, 5, 2). Average = 11/3 ≈ 3.6667.
	user1 := seedUser(t, fx.pool)
	user2 := seedUser(t, fx.pool)
	user3 := seedUser(t, fx.pool)

	for _, r := range []Rating{
		{PluginVersionID: ver.ID, UserID: user1, Stars: 4, ReviewText: "good"},
		{PluginVersionID: ver.ID, UserID: user2, Stars: 5},
		{PluginVersionID: ver.ID, UserID: user3, Stars: 2, ReviewText: "meh"},
	} {
		if _, err := store.Ratings.Submit(ctx, r); err != nil {
			t.Fatalf("Submit: %v", err)
		}
	}

	agg, err = store.Ratings.Aggregate(ctx, ver.ID)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if agg.Count != 3 {
		t.Errorf("count: got %d want 3", agg.Count)
	}
	wantAvg := 11.0 / 3.0
	if agg.Average < wantAvg-0.001 || agg.Average > wantAvg+0.001 {
		t.Errorf("avg: got %v want ~%v", agg.Average, wantAvg)
	}
}

func TestIntegration_Ratings_SubmitUpsert(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()
	store := NewStore(fx.pool)

	listing, err := store.Listings.Create(ctx, Listing{Slug: "gn-rups", Name: "x"})
	if err != nil {
		t.Fatalf("Create listing: %v", err)
	}
	ver, err := store.Versions.Publish(ctx, Version{
		ListingID: listing.ID, Version: "1.0.0",
	}, []byte("wasm"))
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	user := seedUser(t, fx.pool)

	// First submission.
	first, err := store.Ratings.Submit(ctx, Rating{
		PluginVersionID: ver.ID, UserID: user, Stars: 5, ReviewText: "love it",
	})
	if err != nil {
		t.Fatalf("first Submit: %v", err)
	}

	// Same (version, user) again — overwrites stars and review.
	second, err := store.Ratings.Submit(ctx, Rating{
		PluginVersionID: ver.ID, UserID: user, Stars: 2, ReviewText: "changed my mind",
	})
	if err != nil {
		t.Fatalf("second Submit: %v", err)
	}
	if second.Stars != 2 || second.ReviewText != "changed my mind" {
		t.Errorf("upsert didn't overwrite: %+v", second)
	}
	// created_at is preserved: original moment of first rating.
	if !second.CreatedAt.Equal(first.CreatedAt) {
		t.Errorf("created_at should be preserved: first=%v second=%v",
			first.CreatedAt, second.CreatedAt)
	}

	// Aggregate reflects only the latest value — still 1 row.
	agg, err := store.Ratings.Aggregate(ctx, ver.ID)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if agg.Count != 1 || agg.Average != 2 {
		t.Errorf("agg after upsert: %+v", agg)
	}
}

// =============================================================================
// Integration tests — Install events
// =============================================================================

func TestIntegration_Events_AppendOnly(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()
	store := NewStore(fx.pool)

	listing, err := store.Listings.Create(ctx, Listing{Slug: "gn-evt", Name: "x"})
	if err != nil {
		t.Fatalf("Create listing: %v", err)
	}
	ver, err := store.Versions.Publish(ctx, Version{
		ListingID: listing.ID, Version: "1.0.0",
	}, []byte("wasm"))
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	for i, et := range []InstallEventType{
		EventInstalled, EventActivated, EventErrored, EventUninstalled,
	} {
		out, err := store.Events.RecordInstallEvent(ctx, InstallEvent{
			ListingID: listing.ID,
			VersionID: ver.ID,
			HostID:    "host-abc",
			EventType: et,
		})
		if err != nil {
			t.Fatalf("RecordInstallEvent[%d]: %v", i, err)
		}
		if out.ID == 0 {
			t.Errorf("event[%d] id was zero", i)
		}
		if out.HostID != "host-abc" || out.EventType != et {
			t.Errorf("event[%d] roundtrip: %+v", i, out)
		}
	}

	// Counts: 4 over a 1-minute window, 4 lifetime.
	count, err := store.Events.CountByListing(ctx, listing.ID, time.Minute)
	if err != nil {
		t.Fatalf("CountByListing: %v", err)
	}
	if count != 4 {
		t.Errorf("count window: got %d want 4", count)
	}
	count, err = store.Events.CountByListing(ctx, listing.ID, 0)
	if err != nil {
		t.Fatalf("CountByListing lifetime: %v", err)
	}
	if count != 4 {
		t.Errorf("count lifetime: got %d want 4", count)
	}

	// Verify monotonic ids: rows in event_id order match insertion order.
	rows, err := fx.pool.Query(ctx, `
		SELECT event_type FROM plugin_install_events
		 WHERE listing_id = $1
		 ORDER BY id ASC
	`, listing.ID)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var et string
		if err := rows.Scan(&et); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, et)
	}
	want := []string{"installed", "activated", "errored", "uninstalled"}
	if !equalStrings(got, want) {
		t.Errorf("event order: got %v want %v", got, want)
	}
}

// =============================================================================
// Integration tests — Migration up/down round-trip
// =============================================================================

// TestIntegration_Migrations_RoundTrip walks down from the top to a
// state where marketplace tables don't exist, then back up. This
// exercises every .down.sql we ship and surfaces unintended FK
// dependencies between adjacent migrations.
func TestIntegration_Migrations_RoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: skip with -short")
	}
	dsn := containers.Postgres(t)
	if dsn == "" {
		t.Skip("docker not available")
	}
	dir := repoMigrationsDir(t)

	if err := migrateUp(dsn, dir); err != nil {
		t.Fatalf("first up: %v", err)
	}

	// All five marketplace tables exist.
	mustHaveTables(t, dsn, true,
		"plugin_listings", "plugin_versions", "plugin_compat_matrix",
		"plugin_ratings", "plugin_install_events",
	)

	if err := migrateDown(dsn, dir); err != nil {
		t.Fatalf("down: %v", err)
	}
	mustHaveTables(t, dsn, false,
		"plugin_listings", "plugin_versions", "plugin_compat_matrix",
		"plugin_ratings", "plugin_install_events",
	)

	if err := migrateUp(dsn, dir); err != nil {
		t.Fatalf("re-up: %v", err)
	}
	mustHaveTables(t, dsn, true,
		"plugin_listings", "plugin_versions", "plugin_compat_matrix",
		"plugin_ratings", "plugin_install_events",
	)
}

func mustHaveTables(t *testing.T, dsn string, want bool, names ...string) {
	t.Helper()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	for _, name := range names {
		var exists bool
		err := db.QueryRowContext(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM information_schema.tables
				 WHERE table_schema = 'public'
				   AND table_name   = $1
			)
		`, name).Scan(&exists)
		if err != nil {
			t.Fatalf("check %s: %v", name, err)
		}
		if exists != want {
			t.Errorf("table %q: exists=%v want=%v", name, exists, want)
		}
	}
}

// =============================================================================
// helpers
// =============================================================================

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

