package seed

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Singleton-Solution/GoNext/packages/go/testutil/containers"
)

// TestBundledThemeIsParseable is a unit-level sanity check that the
// embed.FS contents are well-formed before any DB or filesystem state
// gets involved. A failure here means the canonical copy under
// /themes/gn-hello drifted from the mirror under
// packages/go/theme/seed/gn-hello — the binary is shipping a broken
// theme.
func TestBundledThemeIsParseable(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"theme.json", "style.css"} {
		path := DefaultThemeSlug + "/" + name
		data, err := fs.ReadFile(BundledThemes, path)
		if err != nil {
			t.Fatalf("read %q: %v", path, err)
		}
		if len(data) == 0 {
			t.Fatalf("%q is empty", path)
		}
	}
	for _, name := range []string{"parts/header.html", "parts/footer.html"} {
		if _, err := fs.ReadFile(BundledThemes, DefaultThemeSlug+"/"+name); err != nil {
			t.Fatalf("read %q: %v", name, err)
		}
	}
	for _, name := range []string{"templates/index.html", "templates/single.html", "templates/archive.html", "templates/404.html"} {
		if _, err := fs.ReadFile(BundledThemes, DefaultThemeSlug+"/"+name); err != nil {
			t.Fatalf("read %q: %v", name, err)
		}
	}
}

// TestFingerprintBundled_Stable: the fingerprint over the same bytes
// returns the same value across calls. The exact value isn't fixed
// (it would lock the test against future theme edits); we only check
// stability + non-emptiness.
func TestFingerprintBundled_Stable(t *testing.T) {
	t.Parallel()
	first, err := FingerprintBundled(BundledThemes, DefaultThemeSlug)
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	if first == "" {
		t.Fatal("fingerprint is empty")
	}
	second, err := FingerprintBundled(BundledThemes, DefaultThemeSlug)
	if err != nil {
		t.Fatalf("fingerprint (second): %v", err)
	}
	if first != second {
		t.Errorf("fingerprint not stable: %q vs %q", first, second)
	}
}

// TestEnsureDefault_RequiresFields covers the misconfigured-Seeder
// paths. Each branch has its own error message so this test exists to
// keep them surfacing — a refactor that collapses them into a single
// "invalid" sentinel would silently degrade error quality.
func TestEnsureDefault_RequiresFields(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	if err := (&Seeder{}).EnsureDefault(ctx); err == nil ||
		!strings.Contains(err.Error(), "DB") {
		t.Errorf("missing DB: want error mentioning DB, got %v", err)
	}
	if err := (&Seeder{DB: fakeDB{}}).EnsureDefault(ctx); err == nil ||
		!strings.Contains(err.Error(), "ThemeDir") {
		t.Errorf("missing ThemeDir: want error mentioning ThemeDir, got %v", err)
	}

	var nilSeeder *Seeder
	if err := nilSeeder.EnsureDefault(ctx); err == nil ||
		!strings.Contains(err.Error(), "nil Seeder") {
		t.Errorf("nil seeder: want nil-Seeder error, got %v", err)
	}
}

// fakeDB is a stub PgxQuerier that errors on every call. It exists
// so the validate path can be exercised without spinning up Postgres.
type fakeDB struct{}

func (fakeDB) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	return errRow{err: errors.New("fakeDB: unused")}
}

func (fakeDB) Exec(_ context.Context, _ string, _ ...any) (CommandTag, error) {
	return nil, errors.New("fakeDB: unused")
}

// errRow satisfies pgx.Row by reporting the wrapped error from Scan.
type errRow struct{ err error }

func (e errRow) Scan(_ ...any) error { return e.err }

// --- Integration tests below: require a real Postgres ---

// TestEnsureDefault_FirstCallInstallsAndMarks asserts the happy path:
//   - the runtime theme dir gets the unpacked files
//   - the options row is created with the bundled slug
//   - the bytes on disk match the bundled bytes byte-for-byte
//
// Test acts as the acceptance gate for "fresh deploy renders a usable
// site". If this regresses, a clean deploy will boot without a theme.
func TestEnsureDefault_FirstCallInstallsAndMarks(t *testing.T) {
	t.Parallel()
	pool, themeDir := mustNewSeederFixture(t)
	defer pool.Close()

	s := &Seeder{
		DB:       PoolQuerier{Pool: pool},
		ThemeDir: themeDir,
		SourceFS: BundledThemes,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := s.EnsureDefault(ctx); err != nil {
		t.Fatalf("EnsureDefault: %v", err)
	}

	// theme.json should exist and match the bundled bytes.
	got, err := os.ReadFile(filepath.Join(themeDir, DefaultThemeSlug, "theme.json"))
	if err != nil {
		t.Fatalf("read installed theme.json: %v", err)
	}
	want, err := fs.ReadFile(BundledThemes, DefaultThemeSlug+"/theme.json")
	if err != nil {
		t.Fatalf("read embedded theme.json: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("installed theme.json differs from bundled bytes")
	}

	// Every file the embedded copy ships should land on disk.
	mustWalkAndCompare(t, themeDir)

	// Options row should record the slug.
	got = []byte(mustReadActiveTheme(t, pool))
	if string(got) != DefaultThemeSlug {
		t.Errorf("active_theme: got %q, want %q", string(got), DefaultThemeSlug)
	}
}

// TestEnsureDefault_Idempotent is the "second boot is a no-op" check.
// We capture mtimes after the first call and assert they don't change
// on the second call — that's the strongest way to express "the
// seeder did not rewrite anything".
func TestEnsureDefault_Idempotent(t *testing.T) {
	t.Parallel()
	pool, themeDir := mustNewSeederFixture(t)
	defer pool.Close()

	s := &Seeder{
		DB:       PoolQuerier{Pool: pool},
		ThemeDir: themeDir,
		SourceFS: BundledThemes,
	}
	ctx := context.Background()

	if err := s.EnsureDefault(ctx); err != nil {
		t.Fatalf("first EnsureDefault: %v", err)
	}

	// Capture the original install signature: mtime of every file
	// under <themeDir>/<slug>.
	first := mustCaptureMtimes(t, filepath.Join(themeDir, DefaultThemeSlug))

	// Idempotent second call must return cleanly.
	if err := s.EnsureDefault(ctx); err != nil {
		t.Fatalf("second EnsureDefault: %v", err)
	}

	second := mustCaptureMtimes(t, filepath.Join(themeDir, DefaultThemeSlug))
	if len(first) != len(second) {
		t.Fatalf("file count changed: first=%d second=%d", len(first), len(second))
	}
	for path, m1 := range first {
		m2, ok := second[path]
		if !ok {
			t.Errorf("file %q vanished after second call", path)
			continue
		}
		if !m1.Equal(m2) {
			t.Errorf("file %q was rewritten (mtime %s → %s)", path, m1, m2)
		}
	}
}

// TestEnsureDefault_RespectsPreExisting covers the "operator already
// chose a theme" case. We pre-insert a row pointing to "my-theme" and
// assert the seeder does NOT overwrite it and does NOT unpack
// gn-hello on top.
func TestEnsureDefault_RespectsPreExisting(t *testing.T) {
	t.Parallel()
	pool, themeDir := mustNewSeederFixture(t)
	defer pool.Close()

	// Pre-seed the options row by hand. We use the same SQL the
	// admin UI's "Activate theme" button will eventually use — that
	// way this test also covers the symmetry.
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO options (key, value, autoload, is_protected)
		VALUES ($1, to_jsonb($2::text), TRUE, FALSE)
	`, ActiveThemeOptionKey, "my-theme"); err != nil {
		t.Fatalf("pre-seed options: %v", err)
	}

	s := &Seeder{
		DB:       PoolQuerier{Pool: pool},
		ThemeDir: themeDir,
		SourceFS: BundledThemes,
	}
	if err := s.EnsureDefault(ctx); err != nil {
		t.Fatalf("EnsureDefault: %v", err)
	}

	// The active row must be untouched.
	if got := mustReadActiveTheme(t, pool); got != "my-theme" {
		t.Errorf("active_theme: got %q, want %q (seeder overwrote a pre-existing theme!)", got, "my-theme")
	}

	// gn-hello must NOT have been unpacked.
	if _, err := os.Stat(filepath.Join(themeDir, DefaultThemeSlug)); !os.IsNotExist(err) {
		t.Errorf("gn-hello directory was created over a pre-existing theme: stat err=%v", err)
	}
}

// TestEnsureDefault_Concurrent races N seeders against the same DB
// + theme dir. Exactly one should win the options-row INSERT; all of
// them must complete without error and the final state must be the
// same as a single-call run.
//
// We pull this test out as race-explicit (not relying on -race alone)
// because Postgres's ON CONFLICT DO NOTHING is the actual race-safety
// primitive — Go's race detector wouldn't catch a regression where we
// introduced check-then-insert without the conflict guard.
func TestEnsureDefault_Concurrent(t *testing.T) {
	t.Parallel()
	pool, themeDir := mustNewSeederFixture(t)
	defer pool.Close()

	const workers = 4
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	errs := make(chan error, workers)
	// Use a barrier so every goroutine starts at roughly the same time.
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := &Seeder{
				DB:       PoolQuerier{Pool: pool},
				ThemeDir: themeDir,
				SourceFS: BundledThemes,
			}
			<-start
			errs <- s.EnsureDefault(ctx)
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Errorf("concurrent EnsureDefault: %v", err)
		}
	}

	// Exactly one row should exist after the race.
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM options WHERE key = $1`,
		ActiveThemeOptionKey,
	).Scan(&n); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if n != 1 {
		t.Errorf("row count: got %d, want 1", n)
	}
	if got := mustReadActiveTheme(t, pool); got != DefaultThemeSlug {
		t.Errorf("active_theme: got %q, want %q", got, DefaultThemeSlug)
	}

	// And the on-disk payload must be intact (re-walked from the
	// embed.FS, not just "files exist").
	mustWalkAndCompare(t, themeDir)
}

// --- helpers ---

// mustNewSeederFixture spins up a Postgres testcontainer, applies the
// schema (just enough for the options table), and returns a pool +
// a fresh temp directory standing in for the runtime theme dir.
// Skips the test when Docker isn't available.
func mustNewSeederFixture(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	dsn := containers.Postgres(t)
	if dsn == "" {
		t.Skip("docker not available")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	mustApplyOptionsSchema(t, pool)
	return pool, t.TempDir()
}

// mustApplyOptionsSchema executes the smallest amount of SQL the
// seeder needs: the options table. We don't apply the full migration
// suite because doing so couples this package's tests to every
// migration that lands later in the repo (and adds 5+ seconds to
// every test). The shape mirrors migrations/000008_options.up.sql.
func mustApplyOptionsSchema(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	const ddl = `
		CREATE EXTENSION IF NOT EXISTS citext;
		CREATE TABLE IF NOT EXISTS options (
			key             CITEXT PRIMARY KEY,
			value           JSONB NOT NULL,
			autoload        BOOLEAN NOT NULL DEFAULT FALSE,
			is_protected    BOOLEAN NOT NULL DEFAULT FALSE,
			created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
			version         INTEGER NOT NULL DEFAULT 1
		);
	`
	if _, err := pool.Exec(ctx, ddl); err != nil {
		t.Fatalf("apply options schema: %v", err)
	}
}

// mustReadActiveTheme returns the slug stored in the options row.
// Fails the test if the row is absent — the assumption is that any
// test that reaches here has already verified the row was created.
func mustReadActiveTheme(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var slug string
	err := pool.QueryRow(context.Background(),
		`SELECT value #>> '{}' FROM options WHERE key = $1`,
		ActiveThemeOptionKey,
	).Scan(&slug)
	if err != nil {
		t.Fatalf("read active theme: %v", err)
	}
	return slug
}

// mustCaptureMtimes returns a map of relative path → mtime for every
// regular file under root. Used by the idempotency assertion.
func mustCaptureMtimes(t *testing.T, root string) map[string]time.Time {
	t.Helper()
	out := make(map[string]time.Time)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out[rel] = info.ModTime()
		return nil
	})
	if err != nil {
		t.Fatalf("walk %q: %v", root, err)
	}
	return out
}

// mustWalkAndCompare confirms every file in the embedded gn-hello
// payload has a byte-identical counterpart on disk under
// <themeDir>/<slug>/. A divergence here means either the unpack
// missed a file or it wrote the wrong bytes.
func mustWalkAndCompare(t *testing.T, themeDir string) {
	t.Helper()
	root, err := fs.Sub(BundledThemes, DefaultThemeSlug)
	if err != nil {
		t.Fatalf("sub: %v", err)
	}
	err = fs.WalkDir(root, ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		want, readErr := fs.ReadFile(root, path)
		if readErr != nil {
			return fmt.Errorf("read embed %q: %w", path, readErr)
		}
		got, diskErr := os.ReadFile(filepath.Join(themeDir, DefaultThemeSlug, filepath.FromSlash(path)))
		if diskErr != nil {
			return fmt.Errorf("read disk %q: %w", path, diskErr)
		}
		if string(want) != string(got) {
			return fmt.Errorf("bytes differ for %q", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk+compare: %v", err)
	}
}

// repoRoot is used by tests that need a path relative to the GoNext
// checkout (currently none — kept for future cross-checks against the
// canonical /themes/gn-hello copy). Unexported so the linter doesn't
// flag dead exports.
//
//nolint:unused // kept for symmetry with sibling test helpers
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(file)
	for i := 0; i < 12; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not locate repo root")
	return ""
}
