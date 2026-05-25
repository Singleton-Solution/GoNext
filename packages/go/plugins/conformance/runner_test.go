package conformance

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Singleton-Solution/GoNext/packages/go/plugins/fakehost"
)

// writeBundle writes a directory-form bundle with the given
// manifest JSON. Returns the bundle directory path.
func writeBundle(t *testing.T, manifest []byte) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), manifest, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return dir
}

func TestSuite_Run_HappyPath(t *testing.T) {
	dir := writeBundle(t, []byte(`{
		"$schema": "https://wpc.dev/schemas/plugin-manifest-v1.json",
		"slug": "gn-seo",
		"name": "SEO",
		"version": "1.0.0",
		"capabilities": {"posts.read": {}, "posts.write": {}, "kv": {}},
		"hooks": {
			"actions": [{"name": "save_post", "handler": "onSave"}]
		},
		"jobs": ["gn-seo.recompute"]
	}`))
	s := NewSuite()
	r, err := s.Run(context.Background(), dir)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !r.Pass {
		for _, sc := range r.Results {
			if sc.Status == StatusFail {
				t.Logf("FAIL %s: %s", sc.Name, sc.Message)
			}
		}
		t.Fatalf("expected pass")
	}
}

func TestSuite_Run_MissingBundle(t *testing.T) {
	s := NewSuite()
	r, err := s.Run(context.Background(), filepath.Join(t.TempDir(), "no-such"))
	if err != nil {
		t.Fatalf("Run shouldn't return io error: %v", err)
	}
	if r.Pass {
		t.Fatalf("expected fail on missing manifest")
	}
}

func TestSuite_Run_BadManifest(t *testing.T) {
	dir := writeBundle(t, []byte("not-json"))
	s := NewSuite()
	r, err := s.Run(context.Background(), dir)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r.Pass {
		t.Fatalf("expected fail")
	}
}

func TestSuite_Run_SEOExample(t *testing.T) {
	// Locate examples/plugins/seo relative to the test file. The
	// path is `../../../../examples/plugins/seo` from this file's
	// directory (packages/go/plugins/conformance).
	_, thisFile, _, _ := runtime.Caller(0)
	bundle := filepath.Clean(filepath.Join(filepath.Dir(thisFile),
		"..", "..", "..", "..", "examples", "plugins", "seo"))
	if _, err := os.Stat(filepath.Join(bundle, "manifest.json")); err != nil {
		t.Skipf("seo example not found at %s: %v", bundle, err)
	}
	s := NewSuite()
	r, err := s.Run(context.Background(), bundle)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// The seo manifest is the legacy form — it should pass all
	// scenarios except limits.budget (which is Skipped pending
	// the WASM runner).
	failed := []string{}
	for _, sc := range r.Results {
		if sc.Status == StatusFail {
			failed = append(failed, sc.Name+": "+sc.Message)
		}
	}
	if len(failed) > 0 {
		t.Fatalf("seo example failed scenarios:\n  %s", strings.Join(failed, "\n  "))
	}
	if !r.Pass {
		t.Fatalf("expected pass; got %+v", r)
	}
}

func TestRecordFixtures_WritesPerScenarioFile(t *testing.T) {
	dir := writeBundle(t, []byte(`{
		"$schema": "https://wpc.dev/schemas/plugin-manifest-v1.json",
		"slug": "gn-x",
		"name": "X",
		"version": "1.0.0",
		"capabilities": {"kv": {}},
		"hooks": {}
	}`))
	fixturesDir := filepath.Join(t.TempDir(), "fixtures")
	s := NewSuite()
	s.RecordFixtures = fixturesDir
	_, err := s.Run(context.Background(), dir)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	entries, err := os.ReadDir(fixturesDir)
	if err != nil {
		t.Fatalf("read fixtures: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("expected at least one fixture")
	}
}

func TestRunner_PanicRecovers(t *testing.T) {
	dir := writeBundle(t, []byte(`{
		"$schema": "https://wpc.dev/schemas/plugin-manifest-v1.json",
		"slug": "gn-x", "name": "X", "version": "1.0.0",
		"capabilities": {}, "hooks": {}
	}`))
	s := NewSuite()
	s.Scenarios = []Scenario{{
		Name: "panic.test",
		Run: func(_ context.Context, _ *Manifest, _ *fakehost.Host) ScenarioResult {
			panic("kaboom")
		},
	}}
	r, _ := s.Run(context.Background(), dir)
	if r.Pass {
		t.Fatalf("expected fail (panic recovered as fail)")
	}
	if len(r.Results) != 1 || r.Results[0].Status != StatusFail {
		t.Fatalf("expected single failed scenario, got %+v", r.Results)
	}
	if !strings.Contains(r.Results[0].Message, "kaboom") {
		t.Fatalf("expected panic message, got %s", r.Results[0].Message)
	}
}

func TestRunner_NilRun_Fails(t *testing.T) {
	dir := writeBundle(t, []byte(`{
		"$schema": "https://wpc.dev/schemas/plugin-manifest-v1.json",
		"slug": "gn-x", "name": "X", "version": "1.0.0",
		"capabilities": {}, "hooks": {}
	}`))
	s := NewSuite()
	s.Scenarios = []Scenario{{Name: "no-op.test", Run: nil}}
	r, _ := s.Run(context.Background(), dir)
	if r.Pass {
		t.Fatalf("expected fail")
	}
}
