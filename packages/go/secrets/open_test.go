package secrets

import (
	"errors"
	"strings"
	"testing"
)

func TestOpen(t *testing.T) {
	cases := []struct {
		name      string
		spec      string
		wantType  string // a marker tag returned by the assertType helper
		wantErr   bool
		errSubstr string
	}{
		{name: "env scheme", spec: "env:", wantType: "*secrets.EnvStore"},
		{name: "env scheme tolerates trailing chars", spec: "env:ignored", wantType: "*secrets.EnvStore"},
		{name: "noop scheme", spec: "noop:", wantType: "*secrets.NoopStore"},
		{name: "file scheme absolute path", spec: "file:/run/secrets", wantType: "*secrets.FileStore"},
		{name: "file scheme triple-slash form", spec: "file:///etc/secrets", wantType: "*secrets.FileStore"},
		{name: "file scheme without path", spec: "file:", wantErr: true, errSubstr: "needs a directory"},
		{name: "vault reserved", spec: "vault://addr", wantErr: true, errSubstr: "not yet implemented"},
		{name: "aws-sm reserved", spec: "aws-sm://us-east-1", wantErr: true, errSubstr: "not yet implemented"},
		{name: "unknown scheme", spec: "consul://x", wantErr: true, errSubstr: "unknown scheme"},
		{name: "no scheme", spec: "just-a-string", wantErr: true, errSubstr: "missing scheme"},
		{name: "empty spec", spec: "", wantErr: true, errSubstr: "missing scheme"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store, err := Open(c.spec)
			if c.wantErr {
				if err == nil {
					t.Fatalf("Open(%q): expected error", c.spec)
				}
				if c.errSubstr != "" && !strings.Contains(err.Error(), c.errSubstr) {
					t.Errorf("Open(%q): err = %q, want substring %q", c.spec, err.Error(), c.errSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Open(%q): unexpected error %v", c.spec, err)
			}
			if store == nil {
				t.Fatalf("Open(%q): nil store, nil err", c.spec)
			}
			got := storeTypeName(store)
			if got != c.wantType {
				t.Errorf("Open(%q): type = %s, want %s", c.spec, got, c.wantType)
			}
		})
	}
}

func TestOpen_FileSchemeDirRoundTrip(t *testing.T) {
	// Confirm Open("file:<dir>") wires through to a FileStore that reads
	// from <dir>. Use a real tempdir + a real file to exercise the path.
	dir := t.TempDir()
	if err := writeTestFile(dir, "KEY.txt", "v"); err != nil {
		t.Fatalf("writeTestFile: %v", err)
	}

	s, err := Open("file:" + dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	got, err := s.Get("KEY")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "v" {
		t.Errorf("Get = %q, want %q", got, "v")
	}

	// And missing keys still return ErrNotFound via the round-tripped store.
	_, err = s.Get("MISSING")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("missing key: err = %v, want errors.Is ErrNotFound", err)
	}
}

func storeTypeName(s Store) string {
	switch s.(type) {
	case *EnvStore:
		return "*secrets.EnvStore"
	case *FileStore:
		return "*secrets.FileStore"
	case *NoopStore:
		return "*secrets.NoopStore"
	default:
		return "unknown"
	}
}
