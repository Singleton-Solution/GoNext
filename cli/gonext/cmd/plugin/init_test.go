package plugin

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// init_test.go validates the `gonext plugin init` scaffolder: argument
// parsing, template rendering, the slug-substitution pass, and the
// "don't clobber existing files" safety. The render itself is straight
// string-replacement so the tests focus on outcomes (files written,
// substitutions applied) rather than internal mechanics.

func TestRunInitWritesExpectedFiles(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "my-plugin")

	var stdout, stderr bytes.Buffer
	code := runInit([]string{"--template=go", "--name=acme-hello", target}, &stdout, &stderr)
	if code != ExitOK {
		t.Fatalf("init failed: code=%d stderr=%s", code, stderr.String())
	}

	wantFiles := []string{"main.go", "manifest.json", "go.mod", "Makefile", ".gitignore"}
	for _, f := range wantFiles {
		path := filepath.Join(target, f)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected file %s to exist: %v", f, err)
		}
	}
}

func TestRunInitSlugSubstitution(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "my-plugin")

	var stdout, stderr bytes.Buffer
	code := runInit([]string{"--name=acme-hello", target}, &stdout, &stderr)
	if code != ExitOK {
		t.Fatalf("init failed: code=%d stderr=%s", code, stderr.String())
	}

	mainBytes, err := os.ReadFile(filepath.Join(target, "main.go"))
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	if !strings.Contains(string(mainBytes), `"acme-hello"`) {
		t.Errorf("main.go missing slug; first 200 chars: %s", string(mainBytes)[:200])
	}
	if strings.Contains(string(mainBytes), "{{PLUGIN_NAME}}") {
		t.Errorf("main.go still contains unreplaced template token")
	}

	manifestBytes, err := os.ReadFile(filepath.Join(target, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest.json: %v", err)
	}
	if !strings.Contains(string(manifestBytes), `"name": "acme-hello"`) {
		t.Errorf("manifest.json missing slug substitution: %s", manifestBytes)
	}
}

func TestRunInitDefaultsToBasename(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "MyPlugin")

	var stdout, stderr bytes.Buffer
	code := runInit([]string{target}, &stdout, &stderr)
	if code != ExitOK {
		t.Fatalf("init failed: code=%d stderr=%s", code, stderr.String())
	}

	manifestBytes, err := os.ReadFile(filepath.Join(target, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest.json: %v", err)
	}
	// MyPlugin -> myplugin (lowercased; no separators to dash).
	if !strings.Contains(string(manifestBytes), `"name": "myplugin"`) {
		t.Errorf("manifest.json: expected sanitized slug 'myplugin', got: %s", manifestBytes)
	}
}

func TestRunInitRefusesToClobber(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "my-plugin")

	// First run succeeds.
	var stdout, stderr bytes.Buffer
	if code := runInit([]string{target}, &stdout, &stderr); code != ExitOK {
		t.Fatalf("first init failed: %d %s", code, stderr.String())
	}

	// Second run without --force fails.
	stdout.Reset()
	stderr.Reset()
	code := runInit([]string{target}, &stdout, &stderr)
	if code != ExitFail {
		t.Errorf("expected ExitFail on second init, got %d", code)
	}
	if !strings.Contains(stderr.String(), "already exists") {
		t.Errorf("expected 'already exists' in stderr, got: %s", stderr.String())
	}

	// Third run with --force succeeds.
	stdout.Reset()
	stderr.Reset()
	code = runInit([]string{"--force", target}, &stdout, &stderr)
	if code != ExitOK {
		t.Errorf("expected ExitOK with --force, got %d: %s", code, stderr.String())
	}
}

func TestRunInitUnknownTemplate(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "my-plugin")

	var stdout, stderr bytes.Buffer
	code := runInit([]string{"--template=rust", target}, &stdout, &stderr)
	if code != ExitUsage {
		t.Errorf("expected ExitUsage, got %d", code)
	}
	if !strings.Contains(stderr.String(), "unknown template") {
		t.Errorf("expected 'unknown template' in stderr: %s", stderr.String())
	}
}

func TestRunInitMissingArg(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runInit([]string{}, &stdout, &stderr)
	if code != ExitUsage {
		t.Errorf("expected ExitUsage, got %d", code)
	}
}

func TestSanitizeSlug(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"hello", "hello"},
		{"Hello", "hello"},
		{"my-plugin", "my-plugin"},
		{"my_plugin", "my-plugin"},
		{"My Plugin", "my-plugin"},
		{"hello.world", "hello-world"},
		{"---trim---", "trim"},
		{"a--b", "a-b"},
		{"!@#$%", "my-plugin"},
		{"", "my-plugin"},
	}
	for _, tc := range cases {
		got := sanitizeSlug(tc.in)
		if got != tc.want {
			t.Errorf("sanitizeSlug(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestPluginInitCommandWired(t *testing.T) {
	// Verify that `gonext plugin init` dispatches through the Run
	// entry point — guards against accidental removal from the
	// switch in plugin.go.
	dir := t.TempDir()
	target := filepath.Join(dir, "wired-test")

	var stdout, stderr bytes.Buffer
	code := Run([]string{"init", target}, &stdout, &stderr)
	if code != ExitOK {
		t.Fatalf("Run init failed: %d %s", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(target, "main.go")); err != nil {
		t.Errorf("Run init did not produce main.go: %v", err)
	}
}
