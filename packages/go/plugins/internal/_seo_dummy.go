// Package internal hosts the dummy-host-bus helper used by the
// gonext-seo example plugin's tests.
//
// This file is named with a LEADING UNDERSCORE so the Go tool ignores
// it for production builds. The Go spec (cmd/go: "Build Constraints"
// and the package-loader file-name filter) skips any file beginning
// with "_" or "."; that gives us a place to park a documented copy of
// the dummy-host code for editor navigation without polluting the
// production binary.
//
// The active copy of the dummy bus lives in
// examples/plugins/seo/dummy_host_test.go (the *_test.go suffix scopes
// it to `go test` invocations, the natural place for it).
//
// Why both files exist:
//
//   - The dummy_host_test.go file is reachable from the example's test
//     suite, which is what runs in CI.
//   - This _seo_dummy.go file is the path docs/04-seo-plugin-tutorial.md
//     points readers at when they want to grok "how does the test
//     contract prove the WASM ABI works without TinyGo?". An IDE that
//     follows the link lands here and sees the same logic, with the
//     long-form rationale.
//
// If either copy drifts from the other, the example's tests fail —
// runFilterThroughBus is the symbol the test calls, and it's defined
// only in dummy_host_test.go.
package internal

// Intentionally empty. The doc comment above is the file's payload.
