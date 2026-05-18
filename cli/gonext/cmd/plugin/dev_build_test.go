package plugin

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeCall captures one invocation of CommandRunner.Run so tests can
// assert on the argv the build orchestrator emits.
type fakeCall struct {
	Dir  string
	Name string
	Args []string
}

// fakeRunner is a CommandRunner that records each call. The Hook lets
// a test stage side effects (e.g. plant a cargo artifact) keyed on the
// command name.
type fakeRunner struct {
	Calls []fakeCall
	// Err, if non-nil, is returned from every Run call (after the
	// call is recorded). Tests use this to exercise the build-failure
	// path.
	Err error
	// Hook, if non-nil, runs after a call is recorded and before Err
	// is considered. It can plant files on disk to mimic a real
	// toolchain's output.
	Hook func(call fakeCall) error
}

// Run satisfies CommandRunner.
func (f *fakeRunner) Run(_ context.Context, dir, name string, args []string, _, _ io.Writer) error {
	c := fakeCall{Dir: dir, Name: name, Args: append([]string(nil), args...)}
	f.Calls = append(f.Calls, c)
	if f.Hook != nil {
		if err := f.Hook(c); err != nil {
			return err
		}
	}
	return f.Err
}

func TestBuildArtifact_TinyGo(t *testing.T) {
	dir := t.TempDir()
	r := &fakeRunner{
		Hook: func(c fakeCall) error {
			// Simulate tinygo writing the artifact.
			out := c.Args[len(c.Args)-3] // -o <path> -target=wasi .
			return os.WriteFile(out, []byte{0x00, 0x61, 0x73, 0x6d}, 0o644)
		},
	}

	if err := buildArtifact(context.Background(), r, dir, LangTinyGo, io.Discard, io.Discard); err != nil {
		t.Fatalf("buildArtifact: %v", err)
	}
	if len(r.Calls) != 1 {
		t.Fatalf("want 1 call; got %d: %+v", len(r.Calls), r.Calls)
	}
	c := r.Calls[0]
	if c.Name != "tinygo" {
		t.Errorf("name = %q; want tinygo", c.Name)
	}
	wantArgs := []string{"build", "-o", filepath.Join(dir, "build", "plugin.wasm"), "-target=wasi", "."}
	if !equalSlices(c.Args, wantArgs) {
		t.Errorf("args = %v; want %v", c.Args, wantArgs)
	}
	if _, err := os.Stat(filepath.Join(dir, "build", "plugin.wasm")); err != nil {
		t.Errorf("artifact not produced: %v", err)
	}
}

func TestBuildArtifact_Rust_CollectsArtifact(t *testing.T) {
	dir := t.TempDir()
	r := &fakeRunner{
		Hook: func(c fakeCall) error {
			// Simulate cargo writing target/wasm32-wasi/release/foo.wasm.
			releaseDir := filepath.Join(dir, "target", "wasm32-wasi", "release")
			if err := os.MkdirAll(releaseDir, 0o755); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(releaseDir, "myplugin.wasm"),
				[]byte{0x00, 0x61, 0x73, 0x6d}, 0o644)
		},
	}
	if err := buildArtifact(context.Background(), r, dir, LangRust, io.Discard, io.Discard); err != nil {
		t.Fatalf("buildArtifact: %v", err)
	}
	if len(r.Calls) != 1 {
		t.Fatalf("want 1 call; got %d", len(r.Calls))
	}
	if r.Calls[0].Name != "cargo" {
		t.Errorf("name = %q; want cargo", r.Calls[0].Name)
	}
	if !equalSlices(r.Calls[0].Args, []string{"build", "--target", "wasm32-wasi", "--release"}) {
		t.Errorf("args = %v", r.Calls[0].Args)
	}
	out := filepath.Join(dir, "build", "plugin.wasm")
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read normalised artifact: %v", err)
	}
	if !bytes.HasPrefix(body, []byte{0x00, 0x61, 0x73, 0x6d}) {
		t.Errorf("normalised artifact missing wasm magic; got %x", body)
	}
}

func TestBuildArtifact_Rust_MultipleArtifactsErrors(t *testing.T) {
	dir := t.TempDir()
	r := &fakeRunner{
		Hook: func(c fakeCall) error {
			releaseDir := filepath.Join(dir, "target", "wasm32-wasi", "release")
			if err := os.MkdirAll(releaseDir, 0o755); err != nil {
				return err
			}
			for _, n := range []string{"a.wasm", "b.wasm"} {
				if err := os.WriteFile(filepath.Join(releaseDir, n), []byte{0x00}, 0o644); err != nil {
					return err
				}
			}
			return nil
		},
	}
	err := buildArtifact(context.Background(), r, dir, LangRust, io.Discard, io.Discard)
	if err == nil {
		t.Fatalf("want error when multiple .wasm artifacts present")
	}
	if !strings.Contains(err.Error(), "multiple") {
		t.Errorf("error %q does not mention multiple artifacts", err)
	}
}

func TestBuildArtifact_ToolchainFailure(t *testing.T) {
	dir := t.TempDir()
	r := &fakeRunner{Err: errors.New("tinygo: exit 1")}
	err := buildArtifact(context.Background(), r, dir, LangTinyGo, io.Discard, io.Discard)
	if err == nil {
		t.Fatalf("want error when runner fails")
	}
	if !strings.Contains(err.Error(), "exit 1") {
		t.Errorf("error %q does not wrap runner error", err)
	}
}

func TestBuildArtifact_UnsupportedLanguage(t *testing.T) {
	dir := t.TempDir()
	r := &fakeRunner{}
	err := buildArtifact(context.Background(), r, dir, Language("fortran"), io.Discard, io.Discard)
	if err == nil {
		t.Fatalf("want error for unsupported language")
	}
}

func equalSlices(a, b []string) bool {
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
