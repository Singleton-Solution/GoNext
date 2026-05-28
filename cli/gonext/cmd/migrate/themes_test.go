package migrate

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// makeBundledThemes builds a fixture under root that resembles the
// /themes tree the cli/gonext Dockerfile bakes into the image: a
// top-level README, plus N theme directories each carrying a
// theme.json and a couple of payload files. Returns the root path so
// callers can pass it as src to seedThemes.
func makeBundledThemes(t *testing.T, root string, slugs ...string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir bundled root: %v", err)
	}
	// A top-level README that must NOT be copied — it's documentation,
	// not a theme. seedThemes filters non-directory entries.
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("docs\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	for _, slug := range slugs {
		dir := filepath.Join(root, slug)
		if err := os.MkdirAll(filepath.Join(dir, "templates"), 0o755); err != nil {
			t.Fatalf("mkdir %q: %v", dir, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "theme.json"),
			[]byte(`{"slug":"`+slug+`"}`), 0o644); err != nil {
			t.Fatalf("write theme.json: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "style.css"),
			[]byte("body{}"), 0o644); err != nil {
			t.Fatalf("write style.css: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "templates", "index.html"),
			[]byte("<html></html>"), 0o644); err != nil {
			t.Fatalf("write index.html: %v", err)
		}
	}
}

func TestSeedThemes_CopiesEverythingIntoEmptyDst(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	makeBundledThemes(t, src, "gn-hello", "gn-pro")

	if err := seedThemes(src, dst, slog.Default()); err != nil {
		t.Fatalf("seedThemes: %v", err)
	}

	for _, slug := range []string{"gn-hello", "gn-pro"} {
		if _, err := os.Stat(filepath.Join(dst, slug, "theme.json")); err != nil {
			t.Errorf("missing %s/theme.json after seed: %v", slug, err)
		}
		if _, err := os.Stat(filepath.Join(dst, slug, "templates", "index.html")); err != nil {
			t.Errorf("missing %s/templates/index.html after seed: %v", slug, err)
		}
	}
	// README.md is documentation, not payload: it must not pollute the
	// runtime volume.
	if _, err := os.Stat(filepath.Join(dst, "README.md")); !os.IsNotExist(err) {
		t.Errorf("README.md should not be copied to destination; err=%v", err)
	}
}

func TestSeedThemes_IsNoOpWhenDestinationHasThemes(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	makeBundledThemes(t, src, "gn-hello")

	// Pre-populate destination with an operator-curated theme. Its
	// payload uses a sentinel value the bundled copy never carries —
	// after a second seedThemes run we expect the sentinel to be
	// intact (i.e. no overwrite).
	curated := filepath.Join(dst, "gn-hello")
	if err := os.MkdirAll(curated, 0o755); err != nil {
		t.Fatalf("mkdir curated: %v", err)
	}
	sentinel := []byte("OPERATOR-CURATED")
	if err := os.WriteFile(filepath.Join(curated, "theme.json"), sentinel, 0o644); err != nil {
		t.Fatalf("write curated theme.json: %v", err)
	}

	if err := seedThemes(src, dst, slog.Default()); err != nil {
		t.Fatalf("seedThemes: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(curated, "theme.json"))
	if err != nil {
		t.Fatalf("read curated theme.json: %v", err)
	}
	if string(got) != string(sentinel) {
		t.Errorf("curated theme overwritten: got=%q, want=%q", string(got), string(sentinel))
	}
}

func TestSeedThemes_IgnoresNonThemeChildDirectories(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	makeBundledThemes(t, src, "gn-hello")
	// A bare directory with no theme.json — must be skipped, not copied.
	if err := os.MkdirAll(filepath.Join(src, "leftover"), 0o755); err != nil {
		t.Fatalf("mkdir leftover: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "leftover", "junk.txt"),
		[]byte("junk"), 0o644); err != nil {
		t.Fatalf("write junk: %v", err)
	}

	if err := seedThemes(src, dst, slog.Default()); err != nil {
		t.Fatalf("seedThemes: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "leftover")); !os.IsNotExist(err) {
		t.Errorf("non-theme directory %q should not be copied; err=%v",
			"leftover", err)
	}
}

func TestSeedThemes_TolerantOfCruftInDestination(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	makeBundledThemes(t, src, "gn-hello")
	// A file at the destination root (e.g. .gitkeep) and an empty
	// directory (lost+found-style) must NOT inhibit the seed: they're
	// not theme directories and a renderer would ignore them anyway.
	if err := os.WriteFile(filepath.Join(dst, ".gitkeep"), nil, 0o644); err != nil {
		t.Fatalf("write .gitkeep: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dst, "lost+found"), 0o755); err != nil {
		t.Fatalf("mkdir lost+found: %v", err)
	}

	if err := seedThemes(src, dst, slog.Default()); err != nil {
		t.Fatalf("seedThemes: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "gn-hello", "theme.json")); err != nil {
		t.Errorf("seed should have run despite cruft; missing gn-hello: %v", err)
	}
}

func TestSeedThemes_MissingSourceIsSoftWarning(t *testing.T) {
	dst := t.TempDir()
	// Point at a path that does not exist. seedThemes treats this as a
	// soft warning (logged, no error returned) so an operator running
	// the CLI outside the Compose image — where /themes is genuinely
	// absent — doesn't see a hard boot failure.
	src := filepath.Join(t.TempDir(), "does-not-exist")
	if err := seedThemes(src, dst, slog.Default()); err != nil {
		t.Errorf("missing source should be soft warning, got error: %v", err)
	}
}

func TestSeedThemes_SourceIsFile_HardError(t *testing.T) {
	dst := t.TempDir()
	srcFile := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(srcFile, []byte("x"), 0o644); err != nil {
		t.Fatalf("write srcFile: %v", err)
	}
	if err := seedThemes(srcFile, dst, slog.Default()); err == nil {
		t.Errorf("expected error when source is a file, got nil")
	}
}

func TestResolveBundledThemesDir_EnvOverride(t *testing.T) {
	t.Setenv(EnvBundledThemesDir, "/custom/src")
	if got := resolveBundledThemesDir(); got != "/custom/src" {
		t.Errorf("got %q, want %q", got, "/custom/src")
	}
	t.Setenv(EnvBundledThemesDir, "")
	if got := resolveBundledThemesDir(); got != DefaultBundledThemesDir {
		t.Errorf("default fallthrough: got %q, want %q", got, DefaultBundledThemesDir)
	}
}

func TestResolveVolumeThemesDir_EnvOverride(t *testing.T) {
	t.Setenv(EnvVolumeThemesDir, "/custom/dst")
	if got := resolveVolumeThemesDir(); got != "/custom/dst" {
		t.Errorf("got %q, want %q", got, "/custom/dst")
	}
	t.Setenv(EnvVolumeThemesDir, "")
	if got := resolveVolumeThemesDir(); got != DefaultVolumeThemesDir {
		t.Errorf("default fallthrough: got %q, want %q", got, DefaultVolumeThemesDir)
	}
}

// TestRun_Up_AcceptsSeedThemesFlag is the migrate-level smoke test for
// the new flag: it must parse cleanly. We drive the misconfiguration
// path with an unset DATABASE_URL so loadConfig refuses *after* the
// flag has been accepted.
func TestRun_Up_AcceptsSeedThemesFlag(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	for _, arg := range []string{
		"--seed-themes=false",
		"--seed-themes=true",
		"-seed-themes=false",
	} {
		t.Run(arg, func(t *testing.T) {
			var stdout, stderr testWriter
			code := Run([]string{"up", arg}, &stdout, &stderr)
			if code != ExitUsage {
				t.Errorf("exit: got %d, want %d", code, ExitUsage)
			}
			if !contains(stderr.String(), "DATABASE_URL") {
				t.Errorf("expected DATABASE_URL error, got stderr=%q", stderr.String())
			}
		})
	}
}

// testWriter is a tiny strings.Builder wrapper. We avoid pulling in
// bytes.Buffer/strings.Contains in this file because the package-level
// migrate_test.go already uses those names; keeping local helpers
// avoids redeclaration noise.
type testWriter struct {
	b []byte
}

func (w *testWriter) Write(p []byte) (int, error) { w.b = append(w.b, p...); return len(p), nil }
func (w *testWriter) String() string              { return string(w.b) }

func contains(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
