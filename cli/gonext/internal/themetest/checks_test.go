package themetest

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTheme materialises a fixture theme into a fresh temp dir.
// files maps a relative path (forward-slashed) to its content.
func writeTheme(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
	return dir
}

// validClassicTheme returns the minimum set of files a classic theme needs
// to pass every non-runtime check.
func validClassicTheme() map[string]string {
	return map[string]string{
		"package.json": `{
  "name": "@acme/theme-hello",
  "version": "1.0.0",
  "gonext": {
    "kind": "theme",
    "type": "classic",
    "engineVersion": ">=1.0.0 <2.0.0",
    "textDomain": "hello"
  }
}`,
		"theme.json": `{
  "$schema": "https://gonext.dev/schemas/theme.json/v1",
  "version": 1,
  "title": "Hello",
  "settings": {
    "color": { "palette": [] },
    "typography": { "fontSizes": [] },
    "spacing": { "padding": true }
  }
}`,
		"templates/index.tsx":  "export default function Index(){return null}\n",
		"templates/single.tsx": "export default function Single(){return null}\n",
		"templates/page.tsx":   "export default function Page(){return null}\n",
		"templates/404.tsx":    "export default function NotFound(){return null}\n",
		"parts/header.tsx":     "export default function Header(){return null}\n",
		"parts/footer.tsx":     "export default function Footer(){return null}\n",
	}
}

// hasCheck returns the index of the check with the given ID (and whether
// it was found). Tests use this rather than scanning by string match.
func findCheck(r *Report, id string) (Check, bool) {
	for _, c := range r.Checks {
		if c.ID == id {
			return c, true
		}
	}
	return Check{}, false
}

func TestRun_ValidClassicTheme(t *testing.T) {
	dir := writeTheme(t, validClassicTheme())
	r, err := Run(dir)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !r.Passed() {
		t.Errorf("expected Passed(); got summary %q", r.Summary())
		for _, c := range r.Checks {
			if c.Status == StatusFail {
				t.Logf("FAIL %s: %s", c.ID, c.Message)
			}
		}
	}
	if r.ThemeName != "@acme/theme-hello" {
		t.Errorf("ThemeName = %q, want @acme/theme-hello", r.ThemeName)
	}
	if r.ThemeType != "classic" {
		t.Errorf("ThemeType = %q, want classic", r.ThemeType)
	}
}

func TestRun_ValidBlockTheme(t *testing.T) {
	files := validClassicTheme()
	// Replace classic tsx templates with .json block templates.
	for k := range files {
		if strings.HasPrefix(k, "templates/") && strings.HasSuffix(k, ".tsx") {
			delete(files, k)
		}
	}
	files["templates/index.json"] = `{"name":"index","blocks":[]}`
	files["templates/single.json"] = `{"name":"single","blocks":[]}`
	// Flip declared type to block.
	files["package.json"] = strings.Replace(files["package.json"], `"classic"`, `"block"`, 1)

	r, err := Run(writeTheme(t, files))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !r.Passed() {
		for _, c := range r.Checks {
			if c.Status == StatusFail {
				t.Logf("FAIL %s: %s", c.ID, c.Message)
			}
		}
		t.Fatalf("expected Passed(); got %q", r.Summary())
	}
	c, ok := findCheck(r, "theme.kind-detected")
	if !ok || c.Status != StatusPass {
		t.Errorf("theme.kind-detected = %v, want PASS", c)
	}
}

func TestRun_MissingThemeJSON(t *testing.T) {
	files := validClassicTheme()
	delete(files, "theme.json")
	r, _ := Run(writeTheme(t, files))
	c, _ := findCheck(r, "theme-json.present")
	if c.Status != StatusFail {
		t.Errorf("theme-json.present = %v, want FAIL", c.Status)
	}
	if r.Passed() {
		t.Errorf("Passed() = true, want false when theme.json missing")
	}
}

func TestRun_MalformedThemeJSON(t *testing.T) {
	files := validClassicTheme()
	files["theme.json"] = "{not valid json"
	r, _ := Run(writeTheme(t, files))
	c, _ := findCheck(r, "theme-json.json-valid")
	if c.Status != StatusFail {
		t.Errorf("theme-json.json-valid = %v, want FAIL", c.Status)
	}
}

func TestRun_WrongThemeJSONVersion(t *testing.T) {
	files := validClassicTheme()
	files["theme.json"] = `{"version": 2}`
	r, _ := Run(writeTheme(t, files))
	c, _ := findCheck(r, "theme-json.version")
	if c.Status != StatusFail {
		t.Errorf("theme-json.version = %v, want FAIL", c.Status)
	}
	if !strings.Contains(c.Message, "unsupported") {
		t.Errorf("expected message to flag unsupported version, got %q", c.Message)
	}
}

func TestRun_MissingPackageJSON(t *testing.T) {
	files := validClassicTheme()
	delete(files, "package.json")
	r, _ := Run(writeTheme(t, files))
	c, _ := findCheck(r, "package-json.present")
	if c.Status != StatusFail {
		t.Errorf("package-json.present = %v, want FAIL", c.Status)
	}
}

func TestRun_WrongGoNextKind(t *testing.T) {
	files := validClassicTheme()
	files["package.json"] = strings.Replace(files["package.json"], `"kind": "theme"`, `"kind": "plugin"`, 1)
	r, _ := Run(writeTheme(t, files))
	c, _ := findCheck(r, "package-json.gonext-kind")
	if c.Status != StatusFail {
		t.Errorf("package-json.gonext-kind = %v, want FAIL", c.Status)
	}
}

func TestRun_MissingIndexTemplate(t *testing.T) {
	files := validClassicTheme()
	delete(files, "templates/index.tsx")
	r, _ := Run(writeTheme(t, files))
	c, _ := findCheck(r, "templates.index")
	if c.Status != StatusFail {
		t.Errorf("templates.index = %v, want FAIL", c.Status)
	}
}

func TestRun_IndexAsHTMLSatisfiesEntry(t *testing.T) {
	files := validClassicTheme()
	delete(files, "templates/index.tsx")
	files["templates/index.html"] = "<!doctype html><title>x</title>"
	// Switch declared type to "classic" with html-only entry — kind-detect should still pass
	// because .html does not flag as block (block is .json only).
	r, _ := Run(writeTheme(t, files))
	c, _ := findCheck(r, "templates.index")
	if c.Status != StatusPass {
		t.Errorf("templates.index = %v, want PASS when index.html exists", c.Status)
	}
}

func TestRun_KindMismatch(t *testing.T) {
	// Declared classic but ships *only* .json templates.
	files := validClassicTheme()
	for k := range files {
		if strings.HasPrefix(k, "templates/") && strings.HasSuffix(k, ".tsx") {
			delete(files, k)
		}
	}
	files["templates/index.json"] = `{"name":"index","blocks":[]}`
	r, _ := Run(writeTheme(t, files))
	c, _ := findCheck(r, "theme.kind-detected")
	if c.Status != StatusFail {
		t.Errorf("theme.kind-detected = %v, want FAIL on mismatch", c.Status)
	}
}

func TestRun_MixedTemplatesNote(t *testing.T) {
	// Both .tsx and .json templates → NOTE (intentional dual-form themes
	// during migration shouldn't FAIL the gate).
	files := validClassicTheme()
	files["templates/single.json"] = `{"name":"single","blocks":[]}`
	r, _ := Run(writeTheme(t, files))
	c, _ := findCheck(r, "theme.kind-detected")
	if c.Status != StatusNote {
		t.Errorf("theme.kind-detected = %v, want NOTE on mixed templates", c.Status)
	}
}

func TestRun_TemplatePartFileMissing(t *testing.T) {
	files := validClassicTheme()
	files["theme.json"] = `{
  "version": 1,
  "title": "Hello",
  "templateParts": [
    { "name": "header", "title": "Header", "area": "header" },
    { "name": "missing", "title": "Nope", "area": "uncategorized" }
  ]
}`
	r, _ := Run(writeTheme(t, files))
	if c, ok := findCheck(r, "theme-json.template-part:header"); !ok || c.Status != StatusPass {
		t.Errorf("template-part:header = %v, want PASS", c)
	}
	if c, ok := findCheck(r, "theme-json.template-part:missing"); !ok || c.Status != StatusFail {
		t.Errorf("template-part:missing = %v, want FAIL", c)
	}
}

func TestRun_NameConventions(t *testing.T) {
	cases := []struct {
		name       string
		pkgName    string
		wantStatus Status
	}{
		{"theme-prefixed scoped passes", "@acme/theme-foo", StatusPass},
		{"scoped without theme prefix notes", "@acme/foo", StatusNote},
		{"unscoped notes", "foo-theme", StatusNote},
		{"invalid fails", "Has Spaces", StatusFail},
		{"empty fails", "", StatusFail},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			files := validClassicTheme()
			pkg := map[string]any{
				"name":    tc.pkgName,
				"version": "1.0.0",
				"gonext": map[string]any{
					"kind":          "theme",
					"type":          "classic",
					"engineVersion": ">=1",
				},
			}
			b, _ := json.Marshal(pkg)
			files["package.json"] = string(b)
			r, _ := Run(writeTheme(t, files))
			c, ok := findCheck(r, "package-json.name")
			if !ok {
				t.Fatal("missing package-json.name check")
			}
			if c.Status != tc.wantStatus {
				t.Errorf("status = %v, want %v (message: %q)", c.Status, tc.wantStatus, c.Message)
			}
		})
	}
}

func TestRun_ReservedRuntimeChecksSkipped(t *testing.T) {
	r, _ := Run(writeTheme(t, validClassicTheme()))
	for _, id := range []string{
		"runtime.render",
		"runtime.block-variations",
		"runtime.a11y",
		"runtime.bundle-budget",
		"runtime.ssr-parity",
	} {
		c, ok := findCheck(r, id)
		if !ok {
			t.Errorf("missing reserved check %q", id)
			continue
		}
		if c.Status != StatusSkip {
			t.Errorf("%s status = %v, want SKIP", id, c.Status)
		}
	}
}

func TestRun_NotADirectory(t *testing.T) {
	f := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(f); err == nil {
		t.Errorf("expected error when target is not a directory")
	}
}

func TestRun_NonexistentPath(t *testing.T) {
	if _, err := Run("/nonexistent-theme-path-/very/unlikely"); err == nil {
		t.Errorf("expected error for missing path")
	}
}

func TestReport_WriteText(t *testing.T) {
	r := &Report{ThemePath: "/tmp/x", ThemeName: "@acme/theme-x", ThemeType: "classic"}
	r.Add(Check{ID: "a", Title: "a", Status: StatusPass})
	r.Add(Check{ID: "b", Title: "b", Status: StatusFail, Message: "oops"})
	r.Add(Check{ID: "c", Title: "c", Status: StatusNote, Message: "fyi"})

	var buf bytes.Buffer
	if err := r.WriteText(&buf, false); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "PASS  a") {
		t.Errorf("missing PASS row: %q", out)
	}
	if !strings.Contains(out, "FAIL  b") {
		t.Errorf("missing FAIL row: %q", out)
	}
	if strings.Contains(out, "NOTE") {
		t.Errorf("NOTE row should be omitted when verbose=false; got %q", out)
	}

	buf.Reset()
	if err := r.WriteText(&buf, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "NOTE  c") {
		t.Errorf("NOTE row missing when verbose=true: %q", buf.String())
	}
}

func TestReport_PassedFalseOnAnyFail(t *testing.T) {
	r := &Report{}
	r.Add(Check{ID: "a", Status: StatusPass})
	r.Add(Check{ID: "b", Status: StatusSkip})
	r.Add(Check{ID: "c", Status: StatusNote})
	if !r.Passed() {
		t.Errorf("expected Passed() with no FAIL rows")
	}
	r.Add(Check{ID: "d", Status: StatusFail})
	if r.Passed() {
		t.Errorf("expected !Passed() after FAIL row added")
	}
}
