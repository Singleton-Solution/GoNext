package plugin

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDedupSorted(t *testing.T) {
	got := dedupSorted([]string{"http.fetch", "db.read", "http.fetch", "a"})
	want := []string{"a", "db.read", "http.fetch"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v; want %v", got, want)
	}
	if got := dedupSorted(nil); got != nil {
		t.Errorf("nil input should produce nil; got %v", got)
	}
}

func TestDiffSorted(t *testing.T) {
	added, removed := diffSorted(
		[]string{"a", "b", "c"},
		[]string{"a", "c", "d"},
	)
	if !reflect.DeepEqual(added, []string{"d"}) {
		t.Errorf("added = %v; want [d]", added)
	}
	if !reflect.DeepEqual(removed, []string{"b"}) {
		t.Errorf("removed = %v; want [b]", removed)
	}
}

func TestDiffSorted_FullTurnover(t *testing.T) {
	added, removed := diffSorted([]string{"a", "b"}, []string{"c", "d"})
	if !reflect.DeepEqual(added, []string{"c", "d"}) {
		t.Errorf("added = %v", added)
	}
	if !reflect.DeepEqual(removed, []string{"a", "b"}) {
		t.Errorf("removed = %v", removed)
	}
}

func TestWriteCapDiff_FirstBuild(t *testing.T) {
	var buf bytes.Buffer
	writeCapDiff(&buf, nil, []string{"http.fetch", "db.read"})
	out := buf.String()
	if !strings.Contains(out, "capabilities:") {
		t.Errorf("missing header; got %q", out)
	}
	for _, want := range []string{"= http.fetch", "= db.read"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q; got %q", want, out)
		}
	}
}

func TestWriteCapDiff_NoChange(t *testing.T) {
	var buf bytes.Buffer
	writeCapDiff(&buf, []string{"a", "b"}, []string{"a", "b"})
	if buf.Len() != 0 {
		t.Errorf("expected no output for no-change; got %q", buf.String())
	}
}

func TestWriteCapDiff_AddedAndRemoved(t *testing.T) {
	var buf bytes.Buffer
	writeCapDiff(&buf, []string{"a", "b"}, []string{"a", "c"})
	out := buf.String()
	if !strings.Contains(out, "+ c") {
		t.Errorf("missing add marker; got %q", out)
	}
	if !strings.Contains(out, "- b") {
		t.Errorf("missing remove marker; got %q", out)
	}
}

func TestWriteCapDiff_BothEmpty(t *testing.T) {
	var buf bytes.Buffer
	writeCapDiff(&buf, nil, nil)
	if buf.Len() != 0 {
		t.Errorf("expected no output for both-empty; got %q", buf.String())
	}
}

func TestReadManifestCapabilities_OK(t *testing.T) {
	dir := t.TempDir()
	body := `{"apiVersion":"gonext.io/v1","name":"x","version":"0.1.0","capabilities":["b","a","b"]}`
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	got, err := readManifestCapabilities(dir)
	if err != nil {
		t.Fatalf("readManifestCapabilities: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("got %v; want [a b]", got)
	}
}

func TestReadManifestCapabilities_Missing(t *testing.T) {
	dir := t.TempDir()
	_, err := readManifestCapabilities(dir)
	if err == nil {
		t.Fatalf("want error when manifest missing")
	}
}

func TestReadManifestCapabilities_Invalid(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte("not json"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	_, err := readManifestCapabilities(dir)
	if err == nil {
		t.Fatalf("want error on bad JSON")
	}
}
