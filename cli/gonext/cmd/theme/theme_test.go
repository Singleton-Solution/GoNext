package theme

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeValidTheme materialises a theme that should pass all non-runtime checks.
func writeValidTheme(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"package.json": `{
  "name": "@acme/theme-hello",
  "version": "1.0.0",
  "gonext": {
    "kind": "theme",
    "type": "classic",
    "engineVersion": ">=1.0.0"
  }
}`,
		"theme.json": `{
  "version": 1,
  "title": "Hello",
  "settings": { "color": {}, "typography": {}, "spacing": {} }
}`,
		"templates/index.tsx": "export default function I(){return null}\n",
	}
	for rel, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestRun_NoArgsShowsUsageAndExitsTwo(t *testing.T) {
	var out, errb bytes.Buffer
	code := Run(nil, &out, &errb)
	if code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "gonext theme") {
		t.Errorf("usage not printed to stderr: %q", errb.String())
	}
}

func TestRun_Help(t *testing.T) {
	var out, errb bytes.Buffer
	code := Run([]string{"--help"}, &out, &errb)
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "test <dir>") {
		t.Errorf("expected subcommand list in help: %q", out.String())
	}
}

func TestRun_UnknownSubcommand(t *testing.T) {
	var out, errb bytes.Buffer
	code := Run([]string{"nope"}, &out, &errb)
	if code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "unknown subcommand") {
		t.Errorf("expected unknown-subcommand message: %q", errb.String())
	}
}

func TestRun_TestSubcommand_Help(t *testing.T) {
	var out, errb bytes.Buffer
	code := Run([]string{"test", "--help"}, &out, &errb)
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	combined := out.String() + errb.String()
	if !strings.Contains(combined, "gonext theme test") {
		t.Errorf("help text missing command name: %q", combined)
	}
}

func TestRun_TestSubcommand_MissingArg(t *testing.T) {
	var out, errb bytes.Buffer
	code := Run([]string{"test"}, &out, &errb)
	if code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
}

func TestRun_TestSubcommand_Success(t *testing.T) {
	dir := writeValidTheme(t)

	var out, errb bytes.Buffer
	code := Run([]string{"test", dir}, &out, &errb)
	if code != 0 {
		t.Errorf("exit = %d, want 0 (stderr=%q)", code, errb.String())
		for _, line := range strings.Split(out.String(), "\n") {
			if strings.HasPrefix(line, "FAIL") {
				t.Logf("%s", line)
			}
		}
	}
	if !strings.Contains(out.String(), "PASS") {
		t.Errorf("expected at least one PASS row in output: %q", out.String())
	}
}

func TestRun_TestSubcommand_FailingTheme(t *testing.T) {
	// Empty dir → multiple FAILs (no theme.json, no package.json, no templates).
	dir := t.TempDir()
	var out, errb bytes.Buffer
	code := Run([]string{"test", dir}, &out, &errb)
	if code != 1 {
		t.Errorf("exit = %d, want 1 for failing theme", code)
	}
}

func TestRun_TestSubcommand_BadPath(t *testing.T) {
	var out, errb bytes.Buffer
	code := Run([]string{"test", "/path/that/definitely/does/not/exist/here"}, &out, &errb)
	if code != 2 {
		t.Errorf("exit = %d, want 2 for unreadable path", code)
	}
}

func TestRun_TestSubcommand_JSONOutput(t *testing.T) {
	dir := writeValidTheme(t)
	var out, errb bytes.Buffer
	code := Run([]string{"test", "--json", dir}, &out, &errb)
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	var parsed struct {
		ThemeName string `json:"themeName"`
		ThemeType string `json:"themeType"`
		Checks    []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(out.Bytes(), &parsed); err != nil {
		t.Fatalf("JSON output not parseable: %v\n%s", err, out.String())
	}
	if parsed.ThemeName != "@acme/theme-hello" {
		t.Errorf("themeName = %q, want @acme/theme-hello", parsed.ThemeName)
	}
	if parsed.ThemeType != "classic" {
		t.Errorf("themeType = %q, want classic", parsed.ThemeType)
	}
	if len(parsed.Checks) == 0 {
		t.Errorf("expected at least one check row in JSON output")
	}
}

func TestRun_TestSubcommand_VerboseShowsNotes(t *testing.T) {
	dir := writeValidTheme(t)
	var out, errb bytes.Buffer
	code := Run([]string{"test", "--verbose", dir}, &out, &errb)
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "NOTE") {
		t.Errorf("expected NOTE rows in verbose output: %q", out.String())
	}
}
