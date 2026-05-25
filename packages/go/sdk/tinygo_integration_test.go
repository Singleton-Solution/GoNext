package sdk

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// tinygo_integration_test.go is the build-and-verify gate for the
// SDK's wasm-target output. It only runs when `tinygo` is on PATH —
// otherwise the test skips, so CI without the toolchain still
// passes.
//
// What we verify:
//
//   - The sdk-go-hello example compiles with TinyGo against the SDK
//     under `-target=wasi`. The plugin's main.go pulls in the SDK,
//     which exercises every host_wasm.go //go:wasmimport stanza —
//     so a mistyped import or a broken `//go:wasmexport` annotation
//     surfaces here.
//
//   - The output is non-empty and exports the required ABI symbols
//     (gn_handle_hook, gn_alloc, gn_free). We grep for the symbol
//     names; a real CI would run `wasm-tools inspect` but grep is
//     the lowest-dependency option.
//
// This test is the "does the SDK actually compile to wasm" guard.
// The handler-logic tests in examples/plugins/sdk-go-hello/main_test.go
// cover the Go-level behaviour; this test covers the build product.

func TestTinyGoBuildExample(t *testing.T) {
	if _, err := exec.LookPath("tinygo"); err != nil {
		t.Skip("tinygo not on PATH; skipping wasm-build integration test")
	}

	// Walk up to the repo root, then over to the example. The SDK
	// lives at packages/go/sdk; the example at
	// examples/plugins/sdk-go-hello.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	exampleDir := filepath.Join(wd, "..", "..", "..", "examples", "plugins", "sdk-go-hello")
	if _, err := os.Stat(filepath.Join(exampleDir, "main.go")); err != nil {
		t.Fatalf("example not found at %s: %v", exampleDir, err)
	}

	out := filepath.Join(t.TempDir(), "plugin.wasm")
	cmd := exec.Command("tinygo", "build", "-target=wasi", "-no-debug", "-o", out, ".")
	cmd.Dir = exampleDir
	cmd.Env = append(os.Environ(), "GOFLAGS=") // clear any GOFLAGS that might pick up -mod=vendor
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tinygo build failed: %v\n%s", err, output)
	}

	st, err := os.Stat(out)
	if err != nil {
		t.Fatalf("stat output: %v", err)
	}
	if st.Size() == 0 {
		t.Fatal("tinygo produced an empty wasm")
	}

	// Verify the required exports are present in the binary.
	// Symbol names appear verbatim in the wasm export section —
	// a grep is the cheapest reliable check.
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	required := []string{"gn_handle_hook", "gn_alloc", "gn_free"}
	for _, sym := range required {
		if !strings.Contains(string(data), sym) {
			t.Errorf("missing required export %q in wasm", sym)
		}
	}
}
