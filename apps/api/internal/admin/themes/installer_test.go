package themes_test

import (
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	adminthemes "github.com/Singleton-Solution/GoNext/apps/api/internal/admin/themes"
)

// validManifest is a minimal but complete theme.json that passes the
// theme package validator. Reused across the installer tests below.
const validManifest = `{
  "$schema": "https://gonext.dev/schemas/theme.json/v1",
  "version": 1,
  "title": "Test Theme",
  "settings": {
    "color": {
      "palette": [{ "slug": "ink", "name": "Ink", "color": "#000000" }]
    }
  }
}`

// buildZip is the test helper that produces an in-memory ZIP archive
// from a name → bytes map. Used by every installer test to skip the
// "write a file, read it back, hand it to Install" round-trip.
func buildZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		f, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := f.Write([]byte(body)); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func TestInstall_NestedLayout(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	zipBytes := buildZip(t, map[string]string{
		"my-theme/theme.json":          validManifest,
		"my-theme/style.css":           "body { color: black; }",
		"my-theme/templates/index.tsx": "export default function Index() { return null; }",
	})
	res, err := adminthemes.Install(dir, zipBytes)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if res.Slug != "my-theme" {
		t.Errorf("slug = %q; want my-theme", res.Slug)
	}
	if _, err := os.Stat(filepath.Join(dir, "my-theme", "theme.json")); err != nil {
		t.Errorf("manifest not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "my-theme", "style.css")); err != nil {
		t.Errorf("style.css not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "my-theme", "templates", "index.tsx")); err != nil {
		t.Errorf("templates/index.tsx not written: %v", err)
	}
}

func TestInstall_FlatLayout(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	zipBytes := buildZip(t, map[string]string{
		"theme.json": validManifest,
		"style.css":  "body { color: black; }",
	})
	res, err := adminthemes.Install(dir, zipBytes)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	// Flat archive falls back to the slugified title.
	if res.Slug != "test-theme" {
		t.Errorf("slug = %q; want test-theme", res.Slug)
	}
	if _, err := os.Stat(filepath.Join(dir, "test-theme", "style.css")); err != nil {
		t.Errorf("style.css not written: %v", err)
	}
}

func TestInstall_MissingManifest(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	zipBytes := buildZip(t, map[string]string{
		"some-theme/style.css": "body {}",
	})
	_, err := adminthemes.Install(dir, zipBytes)
	if !errors.Is(err, adminthemes.ErrZipMissingManifest) {
		t.Errorf("err = %v; want ErrZipMissingManifest", err)
	}
}

func TestInstall_InvalidManifest(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	zipBytes := buildZip(t, map[string]string{
		// Version: 2 trips the schema-version validator.
		"bad-theme/theme.json": `{"version": 2, "settings": {}}`,
	})
	_, err := adminthemes.Install(dir, zipBytes)
	if !errors.Is(err, adminthemes.ErrInvalidManifest) {
		t.Errorf("err = %v; want ErrInvalidManifest", err)
	}
	// Nothing should be on disk.
	if _, statErr := os.Stat(filepath.Join(dir, "bad-theme")); statErr == nil {
		t.Errorf("dir was created despite invalid manifest")
	}
}

func TestInstall_ConflictExisting(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Pre-create the destination so the second install collides.
	if err := os.MkdirAll(filepath.Join(dir, "my-theme"), 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	zipBytes := buildZip(t, map[string]string{
		"my-theme/theme.json": validManifest,
	})
	_, err := adminthemes.Install(dir, zipBytes)
	if !errors.Is(err, adminthemes.ErrThemeExists) {
		t.Errorf("err = %v; want ErrThemeExists", err)
	}
}

func TestInstall_PathTraversal(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Build a zip by hand because buildZip's keys go through the
	// stdlib's normalization. We want the entry name to carry a
	// literal "..".
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	manifest, _ := zw.Create("evil/theme.json")
	_, _ = manifest.Write([]byte(validManifest))
	escape, _ := zw.Create("evil/../etc/passwd")
	_, _ = escape.Write([]byte("root:0:0"))
	_ = zw.Close()

	_, err := adminthemes.Install(dir, buf.Bytes())
	if !errors.Is(err, adminthemes.ErrUnsafePath) {
		t.Errorf("err = %v; want ErrUnsafePath", err)
	}
}

func TestInstall_AbsolutePath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	m, _ := zw.Create("/theme.json")
	_, _ = m.Write([]byte(validManifest))
	_ = zw.Close()
	_, err := adminthemes.Install(dir, buf.Bytes())
	// Either ErrZipMissingManifest (the absolute path doesn't match
	// our "theme.json at root" pattern) or ErrUnsafePath is
	// acceptable — both reject the upload.
	if err == nil {
		t.Errorf("absolute path accepted; expected rejection")
	}
}

func TestInstall_EmptyArchive(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	zipBytes := buildZip(t, map[string]string{})
	_, err := adminthemes.Install(dir, zipBytes)
	if !errors.Is(err, adminthemes.ErrZipMissingManifest) {
		t.Errorf("err = %v; want ErrZipMissingManifest", err)
	}
}

func TestInstall_OversizedUpload(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	huge := bytes.Repeat([]byte{0}, adminthemes.MaxThemeZipSize+1)
	_, err := adminthemes.Install(dir, huge)
	if err == nil {
		t.Errorf("oversized upload accepted")
	}
}

func TestListInstalled_SkipsBrokenThemes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Valid theme.
	if err := os.MkdirAll(filepath.Join(dir, "good"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "good", "theme.json"), []byte(validManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	// Broken: no theme.json.
	if err := os.MkdirAll(filepath.Join(dir, "broken"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Hidden: starts with dot.
	if err := os.MkdirAll(filepath.Join(dir, ".hidden"), 0o755); err != nil {
		t.Fatal(err)
	}

	themes, err := adminthemes.ListInstalled(nil, dir)
	if err != nil {
		t.Fatalf("ListInstalled: %v", err)
	}
	if len(themes) != 1 {
		t.Fatalf("len = %d; want 1; got %+v", len(themes), themes)
	}
	if themes[0].Slug != "good" {
		t.Errorf("slug = %q; want good", themes[0].Slug)
	}
	if themes[0].Title != "Test Theme" {
		t.Errorf("title = %q; want Test Theme", themes[0].Title)
	}
}

func TestListInstalled_MissingDir(t *testing.T) {
	t.Parallel()
	themes, err := adminthemes.ListInstalled(nil, filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("ListInstalled: %v", err)
	}
	if len(themes) != 0 {
		t.Errorf("len = %d; want 0", len(themes))
	}
}

func TestThemeInstalled(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "ok"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ok", "theme.json"), []byte(validManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if !adminthemes.ThemeInstalled(dir, "ok") {
		t.Errorf("ok should be installed")
	}
	if adminthemes.ThemeInstalled(dir, "missing") {
		t.Errorf("missing should not be installed")
	}
	if adminthemes.ThemeInstalled("", "ok") {
		t.Errorf("empty dir should report not installed")
	}
}
