package plugintest

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// writeBundleDir writes a directory-form bundle under t.TempDir(). Both
// manifestBytes and wasmBytes are written verbatim — tests use this to
// produce broken bundles.
func writeBundleDir(t *testing.T, manifestBytes, wasmBytes []byte, wasmRelPath string) string {
	t.Helper()
	dir := t.TempDir()
	if manifestBytes != nil {
		if err := os.WriteFile(filepath.Join(dir, "manifest.json"), manifestBytes, 0o644); err != nil {
			t.Fatalf("write manifest: %v", err)
		}
	}
	if wasmBytes != nil {
		if wasmRelPath == "" {
			wasmRelPath = defaultWASMPath
		}
		full := filepath.Join(dir, filepath.FromSlash(wasmRelPath))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir wasm parent: %v", err)
		}
		if err := os.WriteFile(full, wasmBytes, 0o644); err != nil {
			t.Fatalf("write wasm: %v", err)
		}
	}
	return dir
}

// writeBundleZip packs a manifest + WASM into a .gnplugin archive under
// t.TempDir() and returns the path.
func writeBundleZip(t *testing.T, manifestBytes, wasmBytes []byte) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.gnplugin")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	zw := zip.NewWriter(f)
	mw, err := zw.Create("manifest.json")
	if err != nil {
		t.Fatalf("zip create manifest: %v", err)
	}
	if _, err := mw.Write(manifestBytes); err != nil {
		t.Fatalf("zip write manifest: %v", err)
	}
	ww, err := zw.Create("server/plugin.wasm")
	if err != nil {
		t.Fatalf("zip create wasm: %v", err)
	}
	if _, err := ww.Write(wasmBytes); err != nil {
		t.Fatalf("zip write wasm: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("zip file close: %v", err)
	}
	return path
}

func TestRun_HappyPath_Directory(t *testing.T) {
	dir := writeBundleDir(t, []byte(minimalManifest), validHeader, "")
	report, err := Run(dir)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !report.Pass {
		buf := &bytes.Buffer{}
		_ = report.WriteHuman(buf)
		t.Fatalf("expected Pass=true; got false. report:\n%s", buf.String())
	}
	mustHave := []string{
		CheckManifestSchema,
		CheckBundleLayout,
		CheckCapabilities,
		CheckWASMModule,
		CheckWASMInstantiate,
		CheckHookRegister,
		CheckMigrations,
		CheckHashes,
		CheckDispatchBudget,
	}
	got := map[string]Status{}
	for _, c := range report.Checks {
		got[c.Name] = c.Status
	}
	for _, name := range mustHave {
		if _, ok := got[name]; !ok {
			t.Errorf("missing check row %q", name)
		}
	}
	// Reserved checks should all be skipped with the canonical reason.
	for _, name := range []string{CheckWASMInstantiate, CheckHookRegister, CheckMigrations, CheckHashes, CheckDispatchBudget} {
		if got[name] != StatusSkipped {
			t.Errorf("check %q status = %q; want skipped", name, got[name])
		}
	}
}

func TestRun_HappyPath_Zip(t *testing.T) {
	path := writeBundleZip(t, []byte(minimalManifest), validHeader)
	report, err := Run(path)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !report.Pass {
		buf := &bytes.Buffer{}
		_ = report.WriteHuman(buf)
		t.Fatalf("expected Pass=true; got false. report:\n%s", buf.String())
	}
}

func TestRun_Failures(t *testing.T) {
	cases := []struct {
		name           string
		manifest       []byte
		wasm           []byte
		wasmRel        string
		wantFailedName string
	}{
		{
			name:           "invalid manifest JSON",
			manifest:       []byte("not json"),
			wasm:           validHeader,
			wantFailedName: CheckManifestSchema,
		},
		{
			name:           "manifest missing slug",
			manifest:       []byte(`{"$schema":"https://wpc.dev/schemas/plugin-manifest-v1.json","name":"x","version":"1","abi_version":1,"license":"MIT","server":{"wasm":"server/plugin.wasm"}}`),
			wasm:           validHeader,
			wantFailedName: CheckManifestSchema,
		},
		{
			name:           "manifest declares unknown capability",
			manifest:       []byte(`{"$schema":"https://wpc.dev/schemas/plugin-manifest-v1.json","slug":"gn-x","name":"x","version":"1.0.0","abi_version":1,"license":"MIT","server":{"wasm":"server/plugin.wasm"},"capabilities":{"telepathy":true}}`),
			wasm:           validHeader,
			wantFailedName: CheckManifestSchema,
		},
		{
			name:           "wasm has bad magic",
			manifest:       []byte(minimalManifest),
			wasm:           []byte{0xde, 0xad, 0xbe, 0xef, 0x01, 0x00, 0x00, 0x00},
			wantFailedName: CheckWASMModule,
		},
		{
			name:           "wasm missing",
			manifest:       []byte(minimalManifest),
			wasm:           nil,
			wantFailedName: CheckBundleLayout,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeBundleDir(t, tc.manifest, tc.wasm, tc.wasmRel)
			report, err := Run(dir)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if report.Pass {
				t.Fatalf("expected report.Pass=false; got true")
			}
			found := false
			for _, c := range report.Checks {
				if c.Name == tc.wantFailedName && c.Status == StatusFail {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("expected failed check %q; got rows: %+v", tc.wantFailedName, report.Checks)
			}
		})
	}
}

func TestRun_MissingManifest(t *testing.T) {
	dir := writeBundleDir(t, nil, validHeader, "")
	report, err := Run(dir)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Pass {
		t.Fatal("expected Pass=false when manifest missing")
	}
	// First row should be the manifest read failure under CheckManifestSchema.
	var schemaCheck *Check
	for i := range report.Checks {
		if report.Checks[i].Name == CheckManifestSchema {
			schemaCheck = &report.Checks[i]
			break
		}
	}
	if schemaCheck == nil {
		t.Fatalf("expected a %q row", CheckManifestSchema)
	}
	if schemaCheck.Status != StatusFail {
		t.Errorf("CheckManifestSchema status = %q; want %q", schemaCheck.Status, StatusFail)
	}
}

func TestRun_OpenError(t *testing.T) {
	_, err := Run("/no/such/path")
	if err == nil {
		t.Fatal("expected error for nonexistent path")
	}
}

func TestReport_JSONShape(t *testing.T) {
	dir := writeBundleDir(t, []byte(minimalManifest), validHeader, "")
	report, err := Run(dir)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	buf := &bytes.Buffer{}
	enc := json.NewEncoder(buf)
	if err := enc.Encode(report); err != nil {
		t.Fatalf("encode: %v", err)
	}
	// Decode into a generic map and assert the top-level keys are stable.
	var got map[string]any
	if err := json.NewDecoder(buf).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, key := range []string{"bundle", "pass", "checks"} {
		if _, ok := got[key]; !ok {
			t.Errorf("report JSON missing top-level key %q", key)
		}
	}
}

func TestOpenBundle_UnsupportedExtension(t *testing.T) {
	// Create an empty regular file with a bogus extension.
	dir := t.TempDir()
	path := filepath.Join(dir, "thing.tar")
	if err := os.WriteFile(path, []byte("hi"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := OpenBundle(path); err == nil {
		t.Fatal("expected error for unsupported extension")
	}
}

// drain reads r to EOF and returns the bytes. Helper for tests that need to
// inspect a writer's contents without comparing structured fields.
func drain(t *testing.T, r io.Reader) string {
	t.Helper()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(b)
}

func TestReport_WriteHuman_Format(t *testing.T) {
	report := Report{Bundle: "/x", Pass: true}
	report.Add(Pass("a.b", "ok"))
	report.Add(Skip("c.d", "runtime-not-available"))

	buf := &bytes.Buffer{}
	if err := report.WriteHuman(buf); err != nil {
		t.Fatalf("WriteHuman: %v", err)
	}
	s := drain(t, buf)
	for _, sub := range []string{"PASS", "a.b", "SKIPPED", "c.d", "runtime not yet available", "OK"} {
		if !bytes.Contains([]byte(s), []byte(sub)) {
			t.Errorf("WriteHuman output missing %q; got:\n%s", sub, s)
		}
	}
}
