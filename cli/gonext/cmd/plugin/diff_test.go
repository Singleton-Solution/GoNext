package plugin

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestBundle(t *testing.T, dir, name string, manifest map[string]any) string {
	t.Helper()
	body, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	path := filepath.Join(dir, name+".gnplugin")
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	mw, err := zw.Create("manifest.json")
	if err != nil {
		t.Fatalf("create manifest entry: %v", err)
	}
	if _, err := mw.Write(body); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	ww, err := zw.Create("server/plugin.wasm")
	if err != nil {
		t.Fatalf("create wasm entry: %v", err)
	}
	// Minimal WASM header so the bundle layout looks plausible.
	if _, err := ww.Write([]byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write bundle: %v", err)
	}
	return path
}

func TestDiffAddedRemovedUnchanged(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	oldPath := writeTestBundle(t, dir, "old", map[string]any{
		"slug":         "seo",
		"version":      "1.0.0",
		"capabilities": []string{"posts.read", "kv.read"},
	})
	newPath := writeTestBundle(t, dir, "new", map[string]any{
		"slug":         "seo",
		"version":      "1.1.0",
		"capabilities": []string{"posts.read", "kv.write", "http.fetch"},
	})

	var stdout, stderr bytes.Buffer
	code := runDiff([]string{"--json", oldPath, newPath}, &stdout, &stderr)
	if code != ExitOK {
		t.Fatalf("runDiff exit %d (stderr=%s)", code, stderr.String())
	}

	var got CapabilityDiff
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v\nstdout=%s", err, stdout.String())
	}

	wantAdded := []string{"http.fetch", "kv.write"}
	wantRemoved := []string{"kv.read"}
	wantUnchanged := []string{"posts.read"}
	if !equalSlice(got.Added, wantAdded) {
		t.Errorf("Added = %v, want %v", got.Added, wantAdded)
	}
	if !equalSlice(got.Removed, wantRemoved) {
		t.Errorf("Removed = %v, want %v", got.Removed, wantRemoved)
	}
	if !equalSlice(got.Unchanged, wantUnchanged) {
		t.Errorf("Unchanged = %v, want %v", got.Unchanged, wantUnchanged)
	}
	if got.Slug != "seo" || got.NewVersion != "1.1.0" || got.OldVersion != "1.0.0" {
		t.Errorf("metadata = %+v", got)
	}
}

func TestDiffHumanOutput(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	oldPath := writeTestBundle(t, dir, "old", map[string]any{
		"slug":         "seo",
		"version":      "1.0.0",
		"capabilities": []string{"a", "b"},
	})
	newPath := writeTestBundle(t, dir, "new", map[string]any{
		"slug":         "seo",
		"version":      "2.0.0",
		"capabilities": []string{"a", "c"},
	})
	var stdout, stderr bytes.Buffer
	code := runDiff([]string{oldPath, newPath}, &stdout, &stderr)
	if code != ExitOK {
		t.Fatalf("exit %d (stderr=%s)", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "Added:") || !strings.Contains(out, "+ c") {
		t.Errorf("missing added section: %s", out)
	}
	if !strings.Contains(out, "Removed:") || !strings.Contains(out, "- b") {
		t.Errorf("missing removed section: %s", out)
	}
	if !strings.Contains(out, "Unchanged:") {
		t.Errorf("missing unchanged section: %s", out)
	}
}

func TestDiffNoChanges(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	manifest := map[string]any{
		"slug":         "seo",
		"version":      "1.0.0",
		"capabilities": []string{"a", "b"},
	}
	oldPath := writeTestBundle(t, dir, "old", manifest)
	newPath := writeTestBundle(t, dir, "new", manifest)
	var stdout, stderr bytes.Buffer
	code := runDiff([]string{oldPath, newPath}, &stdout, &stderr)
	if code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(stdout.String(), "No capability changes") {
		t.Errorf("expected no-change message: %s", stdout.String())
	}
}

func TestDiffLegacyMapShape(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Legacy lifecycle-style capabilities as a map.
	oldPath := writeTestBundle(t, dir, "old", map[string]any{
		"slug":    "seo",
		"version": "1.0.0",
		"capabilities": map[string]any{
			"posts.read": map[string]any{"scope": "all"},
		},
	})
	newPath := writeTestBundle(t, dir, "new", map[string]any{
		"slug":         "seo",
		"version":      "1.1.0",
		"capabilities": []string{"posts.read", "http.fetch"},
	})
	var stdout, stderr bytes.Buffer
	code := runDiff([]string{"--json", oldPath, newPath}, &stdout, &stderr)
	if code != ExitOK {
		t.Fatalf("exit %d (stderr=%s)", code, stderr.String())
	}
	var got CapabilityDiff
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !equalSlice(got.Added, []string{"http.fetch"}) {
		t.Errorf("Added = %v", got.Added)
	}
}

func TestDiffMissingArg(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := runDiff([]string{"only-one.gnplugin"}, &stdout, &stderr)
	if code != ExitUsage {
		t.Fatalf("expected ExitUsage, got %d", code)
	}
}

func equalSlice(a, b []string) bool {
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
