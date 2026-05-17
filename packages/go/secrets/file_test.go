package secrets

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileStore_Get(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("DATABASE_URL.txt", "postgres://x\n")
	write("PEPPER.txt", "raw-bytes")
	write("EMPTY.txt", "")
	write("ONLY_NEWLINE.txt", "\n")
	write("CRLF.txt", "value\r\n")

	cases := []struct {
		name    string
		key     string
		want    string
		wantErr error
	}{
		{name: "happy path strips trailing newline", key: "DATABASE_URL", want: "postgres://x"},
		{name: "no trailing newline preserved as-is", key: "PEPPER", want: "raw-bytes"},
		{name: "empty file is not-found", key: "EMPTY", wantErr: ErrNotFound},
		{name: "newline-only file is not-found", key: "ONLY_NEWLINE", wantErr: ErrNotFound},
		{name: "CRLF stripped", key: "CRLF", want: "value"},
		{name: "missing file is not-found", key: "DOES_NOT_EXIST", wantErr: ErrNotFound},
	}

	s := NewFileStore(dir)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := s.Get(c.key)
			if c.wantErr != nil {
				if !errors.Is(err, c.wantErr) {
					t.Fatalf("Get(%q): err = %v, want errors.Is %v", c.key, err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Get(%q): unexpected error %v", c.key, err)
			}
			if got != c.want {
				t.Errorf("Get(%q) = %q, want %q", c.key, got, c.want)
			}
		})
	}
}

func TestFileStore_RejectsPathTraversal(t *testing.T) {
	dir := t.TempDir()
	// Plant a file outside the directory to make sure a traversal attempt
	// can't reach it.
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("nope"), 0o600); err != nil {
		t.Fatalf("write outside: %v", err)
	}

	s := NewFileStore(dir)
	keys := []string{
		"../outside",
		"foo/bar",
		`foo\bar`,
		"..",
		"",
	}
	for _, k := range keys {
		t.Run(k, func(t *testing.T) {
			_, err := s.Get(k)
			if err == nil {
				t.Fatalf("Get(%q): expected error, got nil", k)
			}
			// Must not have read the planted file's content.
			if strings.Contains(err.Error(), "nope") {
				t.Errorf("error leaked content from outside dir: %v", err)
			}
		})
	}
}

func TestFileStore_ErrorDoesNotLeakValue(t *testing.T) {
	dir := t.TempDir()
	const sentinel = "leaky-secret-shhh"
	if err := os.WriteFile(filepath.Join(dir, "K.txt"), []byte(sentinel), 0o000); err != nil {
		t.Fatalf("write: %v", err)
	}
	defer func() {
		_ = os.Chmod(filepath.Join(dir, "K.txt"), 0o600)
	}()

	s := NewFileStore(dir)
	_, err := s.Get("K")
	// On some platforms (root in CI) the permission isn't enforced, so we
	// only assert redaction when an error actually occurs.
	if err != nil && strings.Contains(err.Error(), sentinel) {
		t.Errorf("error message leaked secret value: %v", err)
	}
}

func TestFileStore_ConstructionDoesNotRequireDir(t *testing.T) {
	// FileStore must tolerate a non-existent dir at construction time so
	// processes can start before the orchestrator mounts the volume.
	s := NewFileStore("/this/path/should/not/exist/at/test/time")
	_, err := s.Get("ANYTHING")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Get on missing dir: want ErrNotFound, got %v", err)
	}
}
