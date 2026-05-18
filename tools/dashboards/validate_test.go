package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestValidateRepoDashboards is the smoke test that runs the validator
// against the actual JSON committed in deploy/grafana/dashboards. If a
// future PR adds a dashboard that doesn't meet the contract, this test
// fails and the validate.go-driven CI gate fails too — keeping the two
// surfaces in lockstep.
func TestValidateRepoDashboards(t *testing.T) {
	// Walk up to the repo root. tools/dashboards is two levels deep.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	root := filepath.Join(cwd, "..", "..")
	dir := filepath.Join(root, "deploy", "grafana", "dashboards")
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("dashboards dir not present at %s: %v", dir, err)
	}

	files, err := collect(dir)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(files) == 0 {
		t.Fatalf("expected at least one dashboard under %s", dir)
	}
	for _, f := range files {
		if errs := validateFile(f); len(errs) > 0 {
			t.Errorf("%s: %v", f, errs)
		}
	}
}

// TestValidateRejectsMissingDatasource forces the negative path so a
// future refactor of the validator can't silently accept a panel
// without a datasource.
func TestValidateRejectsMissingDatasource(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	body, err := json.Marshal(map[string]any{
		"schemaVersion": 38,
		"title":         "bad",
		"panels": []any{
			map[string]any{
				"id":    1,
				"title": "no ds",
				"type":  "stat",
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	errs := validateFile(path)
	if len(errs) == 0 {
		t.Fatalf("expected validation failure for panel without datasource")
	}
}

// TestValidateRejectsEmptyPanels makes sure a JSON file that parses
// but has no panels fails — the dashboard would be a blank pane in
// Grafana and is almost certainly a regression rather than a deliberate
// choice.
func TestValidateRejectsEmptyPanels(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.json")
	body := []byte(`{"schemaVersion": 38, "title": "empty", "panels": []}`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	errs := validateFile(path)
	if len(errs) == 0 {
		t.Fatalf("expected validation failure for empty panels list")
	}
}

// TestValidateRejectsMissingSchemaVersion guards against accidental
// removal of the top-level schemaVersion field — Grafana requires it
// to render an import.
func TestValidateRejectsMissingSchemaVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nosv.json")
	body := []byte(`{"title": "no schemaVersion", "panels": [{"id":1,"datasource":"Prometheus"}]}`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	errs := validateFile(path)
	if len(errs) == 0 {
		t.Fatalf("expected validation failure for missing schemaVersion")
	}
}

// TestValidateAcceptsStringDatasource confirms the legacy form where
// `datasource` is just a name string (older Grafana exports) still
// passes — we don't want a stricter shape than Grafana itself accepts.
func TestValidateAcceptsStringDatasource(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.json")
	body := []byte(`{"schemaVersion":38,"title":"legacy","panels":[{"id":1,"type":"stat","datasource":"Prometheus"}]}`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	errs := validateFile(path)
	if len(errs) != 0 {
		t.Fatalf("expected legacy string datasource to pass, got: %v", errs)
	}
}
