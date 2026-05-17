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

// validHeader is the smallest legal WebAssembly v1 binary header.
var validHeader = []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}

const minimalManifest = `{
  "$schema": "https://wpc.dev/schemas/plugin-manifest-v1.json",
  "slug": "gn-seo",
  "name": "GN SEO",
  "version": "1.0.0",
  "abi_version": 1,
  "license": "MIT",
  "server": { "wasm": "server/plugin.wasm" }
}`

func writeBundleDir(t *testing.T, manifestBytes, wasmBytes []byte) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), manifestBytes, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	server := filepath.Join(dir, "server")
	if err := os.MkdirAll(server, 0o755); err != nil {
		t.Fatalf("mkdir server: %v", err)
	}
	if err := os.WriteFile(filepath.Join(server, "plugin.wasm"), wasmBytes, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	return dir
}

func writeBundleZip(t *testing.T, manifestBytes, wasmBytes []byte) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.gnplugin")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	zw := zip.NewWriter(f)
	mw, _ := zw.Create("manifest.json")
	if _, err := mw.Write(manifestBytes); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	ww, _ := zw.Create("server/plugin.wasm")
	if _, err := ww.Write(wasmBytes); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("file close: %v", err)
	}
	return path
}

func TestRun_HelpAndUnknown(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantExit int
		wantOut  string // substring expected on stdout (or "" if stderr)
		wantErr  string // substring expected on stderr (or "")
	}{
		{name: "no args", args: nil, wantExit: ExitUsage, wantErr: "Usage:"},
		{name: "help", args: []string{"help"}, wantExit: ExitOK, wantOut: "Usage:"},
		{name: "--help", args: []string{"--help"}, wantExit: ExitOK, wantOut: "Subcommands:"},
		{name: "unknown subcommand", args: []string{"nope"}, wantExit: ExitUsage, wantErr: "unknown subcommand"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			got := Run(tc.args, stdout, stderr)
			if got != tc.wantExit {
				t.Errorf("exit = %d; want %d. stderr=%q", got, tc.wantExit, stderr.String())
			}
			if tc.wantOut != "" && !strings.Contains(stdout.String(), tc.wantOut) {
				t.Errorf("stdout missing %q; got %q", tc.wantOut, stdout.String())
			}
			if tc.wantErr != "" && !strings.Contains(stderr.String(), tc.wantErr) {
				t.Errorf("stderr missing %q; got %q", tc.wantErr, stderr.String())
			}
		})
	}
}

func TestRunTest_HappyPath_Human(t *testing.T) {
	dir := writeBundleDir(t, []byte(minimalManifest), validHeader)
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	got := Run([]string{"test", dir}, stdout, stderr)
	if got != ExitOK {
		t.Fatalf("exit = %d; want %d. stderr=%q", got, ExitOK, stderr.String())
	}
	out := stdout.String()
	for _, sub := range []string{"PASS", "manifest.schema", "wasm.module", "SKIPPED", "wasm.instantiate"} {
		if !strings.Contains(out, sub) {
			t.Errorf("human output missing %q; got %q", sub, out)
		}
	}
}

func TestRunTest_HappyPath_JSON(t *testing.T) {
	path := writeBundleZip(t, []byte(minimalManifest), validHeader)
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	got := Run([]string{"test", "--json", path}, stdout, stderr)
	if got != ExitOK {
		t.Fatalf("exit = %d; want %d. stderr=%q", got, ExitOK, stderr.String())
	}
	var doc struct {
		Bundle string `json:"bundle"`
		Pass   bool   `json:"pass"`
		Checks []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"checks"`
	}
	if err := json.NewDecoder(stdout).Decode(&doc); err != nil {
		t.Fatalf("decode JSON: %v\nstdout=%s", err, stdout.String())
	}
	if doc.Bundle != path {
		t.Errorf("bundle = %q; want %q", doc.Bundle, path)
	}
	if !doc.Pass {
		t.Errorf("pass = false; want true. checks=%+v", doc.Checks)
	}
	if len(doc.Checks) < 9 {
		t.Errorf("checks count = %d; want >= 9", len(doc.Checks))
	}
}

func TestRunTest_FailingBundle(t *testing.T) {
	// Bundle whose WASM has bad magic — wasm.module should fail.
	bad := []byte{0xde, 0xad, 0xbe, 0xef, 0x01, 0x00, 0x00, 0x00}
	dir := writeBundleDir(t, []byte(minimalManifest), bad)
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	got := Run([]string{"test", dir}, stdout, stderr)
	if got != ExitFail {
		t.Fatalf("exit = %d; want %d. stderr=%q", got, ExitFail, stderr.String())
	}
	if !strings.Contains(stdout.String(), "FAIL") {
		t.Errorf("expected FAIL row in output; got %q", stdout.String())
	}
}

func TestRunTest_MissingArg(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	got := Run([]string{"test"}, stdout, stderr)
	if got != ExitUsage {
		t.Errorf("exit = %d; want %d", got, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "missing bundle path") {
		t.Errorf("stderr missing diagnostic; got %q", stderr.String())
	}
}

func TestRunTest_ExtraArgs(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	got := Run([]string{"test", "/a", "/b"}, stdout, stderr)
	if got != ExitUsage {
		t.Errorf("exit = %d; want %d", got, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "extra argument") {
		t.Errorf("stderr missing diagnostic; got %q", stderr.String())
	}
}

func TestRunTest_OpenError(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	got := Run([]string{"test", "/no/such/path"}, stdout, stderr)
	if got != ExitFail {
		t.Errorf("exit = %d; want %d", got, ExitFail)
	}
	if !strings.Contains(stderr.String(), "gonext plugin test:") {
		t.Errorf("stderr missing diagnostic prefix; got %q", stderr.String())
	}
}

func TestRunTest_OpenError_JSON(t *testing.T) {
	// JSON mode should still emit a parseable failing report on open errors,
	// so the marketplace ingestor doesn't get a partial blob.
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	got := Run([]string{"test", "--json", "/no/such/path"}, stdout, stderr)
	if got != ExitFail {
		t.Errorf("exit = %d; want %d", got, ExitFail)
	}
	var doc map[string]any
	if err := json.NewDecoder(stdout).Decode(&doc); err != nil {
		t.Fatalf("decode JSON: %v\nstdout=%s", err, stdout.String())
	}
	if doc["pass"] != false {
		t.Errorf("pass = %v; want false", doc["pass"])
	}
}

func TestRunTest_HelpFlag(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	got := Run([]string{"test", "--help"}, stdout, stderr)
	if got != ExitOK {
		t.Errorf("exit = %d; want %d. stderr=%q", got, ExitOK, stderr.String())
	}
	// flag package writes help to the FlagSet's output (we wired it to stderr).
	combined := stdout.String() + stderr.String()
	if !strings.Contains(combined, "--json") {
		t.Errorf("help missing --json flag description; got stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}
