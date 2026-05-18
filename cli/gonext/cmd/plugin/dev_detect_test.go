package plugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// touch creates an empty file at path, making parent directories as
// needed. Used by the detection tests to plant toolchain markers.
func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestResolveLanguage_ExplicitHints(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		hint string
		want Language
		err  bool
	}{
		{"go", LangTinyGo, false},
		{"tinygo", LangTinyGo, false},
		{"rust", LangRust, false},
		{"python", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.hint, func(t *testing.T) {
			got, err := resolveLanguage(dir, tc.hint)
			if tc.err {
				if err == nil {
					t.Fatalf("want error for hint %q; got %v", tc.hint, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q; want %q", got, tc.want)
			}
		})
	}
}

func TestDetectLanguage_GoMod(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "go.mod"))
	got, err := resolveLanguage(dir, "auto")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != LangTinyGo {
		t.Errorf("got %q; want %q", got, LangTinyGo)
	}
}

func TestDetectLanguage_Cargo(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "Cargo.toml"))
	got, err := resolveLanguage(dir, "auto")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != LangRust {
		t.Errorf("got %q; want %q", got, LangRust)
	}
}

func TestDetectLanguage_Ambiguous(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "go.mod"))
	touch(t, filepath.Join(dir, "Cargo.toml"))
	_, err := resolveLanguage(dir, "auto")
	if err == nil {
		t.Fatalf("want ambiguity error; got nil")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("error %q does not mention ambiguity", err)
	}
}

func TestDetectLanguage_Empty(t *testing.T) {
	dir := t.TempDir()
	_, err := resolveLanguage(dir, "auto")
	if err == nil {
		t.Fatalf("want failure for empty dir; got nil")
	}
	if !strings.Contains(err.Error(), "no go.mod or Cargo.toml") {
		t.Errorf("error %q does not name the marker files", err)
	}
}

func TestDetectLanguage_DirIsNotFile(t *testing.T) {
	// A directory called go.mod should not satisfy detection — only
	// regular files do.
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "go.mod"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	_, err := resolveLanguage(dir, "auto")
	if err == nil {
		t.Fatalf("want failure when go.mod is a directory")
	}
}
