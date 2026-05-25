package plugin

import (
	"bytes"
	"strings"
	"testing"
)

// TestSignDryRunPrintsIdentity confirms the --dry-run path computes the
// digest and identity from the bundle and emits them to stdout without
// invoking cosign.
func TestSignDryRunPrintsIdentity(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bundle := writeTestBundle(t, dir, "seo", map[string]any{
		"slug":         "seo",
		"version":      "1.0.0",
		"capabilities": []string{"posts.read"},
	})
	var stdout, stderr bytes.Buffer
	code := runSign([]string{
		"--identity", "github.com/Singleton-Solution",
		"--dry-run",
		bundle,
	}, &stdout, &stderr)
	if code != ExitOK {
		t.Fatalf("exit %d (stderr=%s)", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"Digest:",
		"Identity: github.com/Singleton-Solution",
		"--dry-run",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q: %s", want, out)
		}
	}
}

// TestSignMissingIdentity ensures keyless mode rejects a missing
// --identity flag at usage time before any IO.
func TestSignMissingIdentity(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bundle := writeTestBundle(t, dir, "seo", map[string]any{
		"slug":    "seo",
		"version": "1.0.0",
	})
	var stdout, stderr bytes.Buffer
	code := runSign([]string{"--dry-run", bundle}, &stdout, &stderr)
	if code != ExitUsage {
		t.Fatalf("expected ExitUsage, got %d (stderr=%s)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "--identity is required") {
		t.Errorf("missing helpful error: %s", stderr.String())
	}
}

// TestSignBadIdentity confirms a malformed identity is rejected with a
// usage-class exit before cosign is touched.
func TestSignBadIdentity(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bundle := writeTestBundle(t, dir, "seo", map[string]any{
		"slug": "seo", "version": "1.0.0",
	})
	var stdout, stderr bytes.Buffer
	code := runSign([]string{
		"--identity", "bitbucket.org/foo",
		"--dry-run",
		bundle,
	}, &stdout, &stderr)
	if code != ExitUsage {
		t.Fatalf("expected ExitUsage, got %d", code)
	}
}

// TestSignBundleNotFound covers the case where the user typos the
// bundle path. The CLI should report the underlying IO error and exit
// with ExitFail (not ExitUsage — the args were structurally fine).
func TestSignBundleNotFound(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := runSign([]string{
		"--identity", "github.com/Singleton-Solution",
		"--dry-run",
		"/tmp/does-not-exist-12345.gnplugin",
	}, &stdout, &stderr)
	if code != ExitFail {
		t.Fatalf("expected ExitFail, got %d (stderr=%s)", code, stderr.String())
	}
}
