package plugin

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// conformanceManifest is a v1-schema manifest that satisfies every
// built-in conformance scenario.
const conformanceManifest = `{
  "$schema": "https://wpc.dev/schemas/plugin-manifest-v1.json",
  "slug": "gn-seo",
  "name": "GN SEO",
  "version": "1.0.0",
  "abi_version": 1,
  "license": "MIT",
  "server": { "wasm": "server/plugin.wasm" },
  "capabilities": {"posts.read": {}, "posts.write": {}, "kv": {}},
  "hooks": {
    "actions": [{"name": "save_post", "handler": "onSave"}]
  },
  "jobs": ["gn-seo.recompute"]
}`

// failingConformanceManifest has hooks but no capabilities — the
// capabilities.match-usage scenario fails on this.
const failingConformanceManifest = `{
  "$schema": "https://wpc.dev/schemas/plugin-manifest-v1.json",
  "slug": "gn-bad",
  "name": "Bad",
  "version": "1.0.0",
  "abi_version": 1,
  "license": "MIT",
  "server": { "wasm": "server/plugin.wasm" },
  "capabilities": {},
  "hooks": {
    "actions": [{"name": "save_post", "handler": "onSave"}]
  }
}`

func TestRunTest_Conformance_HappyPath(t *testing.T) {
	dir := writeBundleDir(t, []byte(conformanceManifest), validHeader)
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	got := Run([]string{"test", "--suite=conformance", dir}, stdout, stderr)
	if got != ExitOK {
		t.Fatalf("exit = %d; want %d. stderr=%q stdout=%q",
			got, ExitOK, stderr.String(), stdout.String())
	}
	out := stdout.String()
	for _, sub := range []string{"capabilities.declared", "init.idempotent", "PASS"} {
		if !strings.Contains(out, sub) {
			t.Errorf("conformance output missing %q; got %q", sub, out)
		}
	}
}

func TestRunTest_Conformance_Failing(t *testing.T) {
	dir := writeBundleDir(t, []byte(failingConformanceManifest), validHeader)
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	got := Run([]string{"test", "--suite=conformance", dir}, stdout, stderr)
	if got != ExitFail {
		t.Fatalf("exit = %d; want %d. stderr=%q stdout=%q",
			got, ExitFail, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), "FAIL") {
		t.Errorf("expected FAIL row; got %q", stdout.String())
	}
}

func TestRunTest_Conformance_JSON(t *testing.T) {
	dir := writeBundleDir(t, []byte(conformanceManifest), validHeader)
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	got := Run([]string{"test", "--suite=conformance", "--json", dir}, stdout, stderr)
	if got != ExitOK {
		t.Fatalf("exit = %d; want %d. stderr=%q",
			got, ExitOK, stderr.String())
	}
	var doc struct {
		Bundle  string `json:"bundle"`
		Suite   string `json:"suite"`
		Pass    bool   `json:"pass"`
		Results []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"results"`
	}
	if err := json.NewDecoder(stdout).Decode(&doc); err != nil {
		t.Fatalf("decode JSON: %v\nstdout=%s", err, stdout.String())
	}
	if doc.Suite != "conformance" {
		t.Errorf("suite = %q; want conformance", doc.Suite)
	}
	if !doc.Pass {
		t.Errorf("pass = false; want true. results=%+v", doc.Results)
	}
	if len(doc.Results) == 0 {
		t.Errorf("expected at least one result, got 0")
	}
}

func TestRunTest_Conformance_RecordFixtures(t *testing.T) {
	dir := writeBundleDir(t, []byte(conformanceManifest), validHeader)
	fixtures := filepath.Join(t.TempDir(), "out")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	got := Run([]string{
		"test", "--suite=conformance",
		"--record-fixtures=" + fixtures, dir,
	}, stdout, stderr)
	if got != ExitOK {
		t.Fatalf("exit = %d; want %d. stderr=%q", got, ExitOK, stderr.String())
	}
	entries, err := os.ReadDir(fixtures)
	if err != nil {
		t.Fatalf("read fixtures dir: %v", err)
	}
	if len(entries) == 0 {
		t.Errorf("expected fixtures dumped; got 0 entries")
	}
}

func TestRunTest_RecordFixtures_RequiresConformance(t *testing.T) {
	dir := writeBundleDir(t, []byte(minimalManifest), validHeader)
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	got := Run([]string{"test", "--record-fixtures=/tmp/x", dir}, stdout, stderr)
	if got != ExitUsage {
		t.Fatalf("exit = %d; want ExitUsage. stderr=%q stdout=%q",
			got, stderr.String(), stdout.String())
	}
	if !strings.Contains(stderr.String(), "requires --suite=conformance") {
		t.Errorf("stderr missing diagnostic; got %q", stderr.String())
	}
}

func TestRunTest_UnknownSuite(t *testing.T) {
	dir := writeBundleDir(t, []byte(minimalManifest), validHeader)
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	got := Run([]string{"test", "--suite=banana", dir}, stdout, stderr)
	if got != ExitUsage {
		t.Fatalf("exit = %d; want ExitUsage. stderr=%q stdout=%q",
			got, stderr.String(), stdout.String())
	}
	if !strings.Contains(stderr.String(), "unknown suite") {
		t.Errorf("stderr missing diagnostic; got %q", stderr.String())
	}
}

func TestRunTest_DefaultSuite_StillWorks(t *testing.T) {
	// Sanity: passing --suite=default (explicit) routes through the
	// default path and reports the contract checks, not the
	// conformance scenarios.
	dir := writeBundleDir(t, []byte(minimalManifest), validHeader)
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	got := Run([]string{"test", "--suite=default", dir}, stdout, stderr)
	if got != ExitOK {
		t.Fatalf("exit = %d; want %d. stderr=%q", got, ExitOK, stderr.String())
	}
	if !strings.Contains(stdout.String(), "manifest.schema") {
		t.Errorf("default suite missing manifest.schema row; got %q", stdout.String())
	}
}

func TestRunTest_Conformance_SEOExample(t *testing.T) {
	// End-to-end: drive `gonext plugin test --suite=conformance` against
	// the real seo example bundle. This is the "smoke-test against
	// examples/plugins/seo" the issue calls for.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	bundle := filepath.Clean(filepath.Join(wd,
		"..", "..", "..", "..", "examples", "plugins", "seo"))
	if _, err := os.Stat(filepath.Join(bundle, "manifest.json")); err != nil {
		t.Skipf("seo example not found at %s: %v", bundle, err)
	}
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	got := Run([]string{"test", "--suite=conformance", bundle}, stdout, stderr)
	if got != ExitOK {
		t.Fatalf("seo conformance: exit = %d; want %d.\nstdout=%s\nstderr=%s",
			got, ExitOK, stdout.String(), stderr.String())
	}
}
