package conformance

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadFixtureScenarios_Empty_NoError(t *testing.T) {
	got, err := LoadFixtureScenarios(t.TempDir())
	if err != nil {
		t.Fatalf("LoadFixtureScenarios: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0, got %d", len(got))
	}
}

func TestLoadFixtureScenarios_NonexistentDir_NoError(t *testing.T) {
	got, err := LoadFixtureScenarios(filepath.Join(t.TempDir(), "no", "such"))
	if err != nil {
		t.Fatalf("LoadFixtureScenarios: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0, got %d", len(got))
	}
}

func TestLoadFixtureScenarios_RoundTrip(t *testing.T) {
	// Step 1: build a bundle + run conformance with --record-fixtures
	// to produce a fixture.
	bundleDir := writeBundle(t, []byte(`{
		"$schema": "https://wpc.dev/schemas/plugin-manifest-v1.json",
		"slug": "gn-x",
		"name": "X",
		"version": "1.0.0",
		"capabilities": {},
		"hooks": {},
		"jobs": ["gn-x.run"]
	}`))
	fixturesDir := filepath.Join(t.TempDir(), "fix")
	s := NewSuite()
	s.RecordFixtures = fixturesDir
	if _, err := s.Run(context.Background(), bundleDir); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Step 2: load the fixtures back as scenarios.
	loaded, err := LoadFixtureScenarios(fixturesDir)
	if err != nil {
		t.Fatalf("LoadFixtureScenarios: %v", err)
	}
	if len(loaded) == 0 {
		t.Fatalf("expected scenarios from recorded fixtures")
	}

	// Step 3: replay them.
	s2 := NewSuite()
	s2.Scenarios = append(s2.Scenarios, loaded...)
	r2, err := s2.Run(context.Background(), bundleDir)
	if err != nil {
		t.Fatalf("Run replay: %v", err)
	}
	// We don't require Pass — the replay assertion may legitimately
	// fail for some scenarios — but the loaded scenarios must
	// appear in the results.
	hasFixture := false
	for _, res := range r2.Results {
		if strings.HasPrefix(res.Name, "fixture.") {
			hasFixture = true
			break
		}
	}
	if !hasFixture {
		t.Fatalf("expected at least one fixture.* result, got %+v", r2.Results)
	}
}

func TestReadFixture_BadJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.json")
	if err := os.WriteFile(path, []byte("not-json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := readFixture(path); err == nil {
		t.Fatal("expected error on garbage JSON")
	}
}

func TestReadFixture_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.json")
	in := Report{
		Bundle: "/tmp/example",
		Suite:  "conformance",
		Pass:   true,
		Results: []ScenarioResult{
			{Name: "x", Status: StatusPass},
		},
	}
	b, _ := json.MarshalIndent(in, "", "  ")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := readFixture(path)
	if err != nil {
		t.Fatalf("readFixture: %v", err)
	}
	if got.Bundle != in.Bundle || len(got.Results) != 1 || got.Results[0].Name != "x" {
		t.Fatalf("round trip mismatch: %+v", got)
	}
}
