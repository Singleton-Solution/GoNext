package migrate

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParsePluginsList(t *testing.T) {
	t.Parallel()
	got := parsePluginsList(" advanced-custom-fields , wpforms-lite,, woocommerce ")
	want := []string{"advanced-custom-fields", "wpforms-lite", "woocommerce"}
	if !equalSlice(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestParsePluginsList_Dedupe(t *testing.T) {
	t.Parallel()
	got := parsePluginsList("a,b,a,c,b")
	want := []string{"a", "b", "c"}
	if !equalSlice(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestLookupReplacement_Known(t *testing.T) {
	t.Parallel()
	e := LookupReplacement("advanced-custom-fields")
	if e.Status != StatusBuiltIn {
		t.Errorf("status: got %q want %q", e.Status, StatusBuiltIn)
	}
	if !strings.Contains(e.Title, "Advanced Custom Fields") {
		t.Errorf("title: %q", e.Title)
	}
}

func TestLookupReplacement_CaseInsensitive(t *testing.T) {
	t.Parallel()
	a := LookupReplacement("WooCommerce")
	b := LookupReplacement("woocommerce")
	if a.Status != b.Status || a.Title != b.Title {
		t.Errorf("case-insensitive lookup failed: %+v vs %+v", a, b)
	}
}

func TestLookupReplacement_Unknown(t *testing.T) {
	t.Parallel()
	e := LookupReplacement("never-heard-of-it")
	if e.Status != StatusUnknown {
		t.Errorf("status: got %q want %q", e.Status, StatusUnknown)
	}
	if e.Title != "Never Heard Of It" {
		t.Errorf("title: got %q", e.Title)
	}
}

func TestBuildReport_HasAllSlugs(t *testing.T) {
	t.Parallel()
	slugs := []string{"advanced-custom-fields", "woocommerce", "wordpress-seo"}
	report := buildReport(slugs)
	if !strings.Contains(report, "# WordPress plugin replacement guide") {
		t.Error("missing header")
	}
	for _, s := range slugs {
		if !strings.Contains(report, "`"+s+"`") {
			t.Errorf("report missing slug %q", s)
		}
	}
	// Each section status should appear.
	if !strings.Contains(report, "Built-in") {
		t.Error("missing built-in status")
	}
	if !strings.Contains(report, "No equivalent yet") {
		t.Error("missing none status")
	}
}

func TestRunReplacements_WritesFile(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	out := filepath.Join(tmp, "report.md")
	var stdout, stderr bytes.Buffer
	code := runReplacements(
		[]string{"--plugins=advanced-custom-fields,woocommerce", "--out=" + out},
		&stdout, &stderr,
	)
	if code != ExitOK {
		t.Fatalf("code: got %d want %d (stderr=%q)", code, ExitOK, stderr.String())
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(body), "WooCommerce") {
		t.Error("report missing WooCommerce")
	}
}

func TestRunReplacements_MissingFlags(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := runReplacements(nil, &stdout, &stderr)
	if code != ExitUsage {
		t.Errorf("code: got %d want %d", code, ExitUsage)
	}
}

func TestRunReplacements_Scan(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	for _, slug := range []string{"woocommerce", "advanced-custom-fields", "fake-plugin"} {
		if err := os.MkdirAll(filepath.Join(tmp, slug), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	// Also stash a file (not a dir) to confirm it's ignored.
	if err := os.WriteFile(filepath.Join(tmp, "not-a-plugin.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// And a dotted dir to confirm it's skipped.
	if err := os.MkdirAll(filepath.Join(tmp, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	out := filepath.Join(tmp, "report.md")
	var stdout, stderr bytes.Buffer
	code := runReplacements(
		[]string{"--scan=" + tmp, "--out=" + out},
		&stdout, &stderr,
	)
	if code != ExitOK {
		t.Fatalf("code: got %d want %d (stderr=%q)", code, ExitOK, stderr.String())
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(body), "woocommerce") {
		t.Error("missing woocommerce")
	}
	if strings.Contains(string(body), ".git") {
		t.Error("dotted dir leaked into report")
	}
	if strings.Contains(string(body), "not-a-plugin") {
		t.Error("non-dir file leaked into report")
	}
}

func TestRegistrySlugs_Sorted(t *testing.T) {
	t.Parallel()
	slugs := RegistrySlugs()
	if len(slugs) < 25 {
		t.Errorf("registry too small: %d", len(slugs))
	}
	for i := 1; i < len(slugs); i++ {
		if slugs[i-1] >= slugs[i] {
			t.Errorf("registry not sorted at %d: %q >= %q", i, slugs[i-1], slugs[i])
		}
	}
}

func TestTitleizeSlug(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"advanced-custom-fields": "Advanced Custom Fields",
		"woocommerce":            "Woocommerce",
		"":                       "(unnamed)",
		"-leading":               " Leading",
	}
	for in, want := range cases {
		if got := titleizeSlug(in); got != want {
			t.Errorf("titleize(%q): got %q want %q", in, got, want)
		}
	}
}

func TestResolvedOutPath(t *testing.T) {
	t.Parallel()
	if got := resolvedOutPath(""); got != "replacement-report.md" {
		t.Errorf("default: %q", got)
	}
	if got := resolvedOutPath("./x//../y"); got != "y" {
		t.Errorf("clean: %q", got)
	}
}

func equalSlice(a, b []string) bool {
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
