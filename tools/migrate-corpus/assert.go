// `assert` runs `gonext migrate wp --dry-run --file <fixture>` against every
// fixture under fixtures/wxr/, parses the human-readable report counters,
// and compares them against fixtures/expected/<slug>.json. Exits non-zero
// on the first mismatch with a unified diff of expected-vs-actual.
//
// This is the CI hook for issue #219: the workflow at
// `.github/workflows/migrate-corpus.yml` calls this command and treats
// any non-zero exit as a regression.

package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Expected is the shape of fixtures/expected/<slug>.json.
type Expected struct {
	Authors     int `json:"authors"`
	Categories  int `json:"categories"`
	Tags        int `json:"tags"`
	Posts       int `json:"posts"`
	Attachments int `json:"attachments"`
	Comments    int `json:"comments"`

	// ErrorsMax is the per-record-error upper bound. The importer is
	// healthy when len(report.Errors) <= ErrorsMax. We use a ceiling
	// (rather than an exact count) so a fixture that legitimately
	// trips a soft warning can still pass without forcing every
	// fixture file to encode warning counts.
	ErrorsMax int `json:"errors_max"`
}

// Actual is the parsed report from gonext migrate wp --dry-run.
type Actual struct {
	Authors     int
	Categories  int
	Tags        int
	Posts       int
	Attachments int
	Comments    int
	Errors      int
}

// runAssert is the entry point for the `gonext-corpus assert` subcommand.
// It is wired into main.go's dispatch table.
func runAssert(argv []string) error {
	fs := flag.NewFlagSet("assert", flag.ContinueOnError)
	fixturesDir := fs.String("fixtures", "./fixtures", "fixtures directory (must contain wxr/ and expected/)")
	gonextBin := fs.String("bin", "gonext", "path to the gonext CLI binary")
	if err := fs.Parse(argv); err != nil {
		return err
	}
	wxrDir := filepath.Join(*fixturesDir, "wxr")
	expectedDir := filepath.Join(*fixturesDir, "expected")

	entries, err := os.ReadDir(wxrDir)
	if err != nil {
		return fmt.Errorf("read wxr dir %q: %w", wxrDir, err)
	}
	slugs := []string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".xml") {
			continue
		}
		slugs = append(slugs, strings.TrimSuffix(name, ".xml"))
	}
	sort.Strings(slugs)
	if len(slugs) == 0 {
		return fmt.Errorf("no .xml fixtures found under %s", wxrDir)
	}

	var failures []string
	for _, slug := range slugs {
		fixturePath := filepath.Join(wxrDir, slug+".xml")
		expectedPath := filepath.Join(expectedDir, slug+".json")
		exp, err := loadExpected(expectedPath)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: load expected: %v", slug, err))
			continue
		}
		actual, err := runDryRun(*gonextBin, fixturePath)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: dry-run: %v", slug, err))
			continue
		}
		if diff := diffActual(exp, actual); diff != "" {
			failures = append(failures, fmt.Sprintf("%s: %s", slug, diff))
			continue
		}
		fmt.Printf("PASS  %s — %d posts / %d media / %d comments\n",
			slug, actual.Posts, actual.Attachments, actual.Comments)
	}
	if len(failures) > 0 {
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "FAILURES:")
		for _, f := range failures {
			fmt.Fprintf(os.Stderr, "  - %s\n", f)
		}
		return fmt.Errorf("%d fixture(s) failed assertion", len(failures))
	}
	fmt.Printf("\nall %d fixture(s) match expected output\n", len(slugs))
	return nil
}

// loadExpected reads and unmarshals fixtures/expected/<slug>.json.
func loadExpected(p string) (Expected, error) {
	b, err := os.ReadFile(filepath.Clean(p))
	if err != nil {
		return Expected{}, err
	}
	var e Expected
	if err := json.Unmarshal(b, &e); err != nil {
		return Expected{}, fmt.Errorf("decode: %w", err)
	}
	return e, nil
}

// runDryRun shells out to `gonext migrate wp --dry-run --file <p>` and
// parses the report block from stdout. The report is space-aligned and
// stable; we tokenise on the colon delimiter.
func runDryRun(bin, fixture string) (Actual, error) {
	cmd := exec.Command(bin, "migrate", "wp", "--dry-run", "--file", fixture) //nolint:gosec // bin path is operator-supplied via --bin flag
	out, err := cmd.CombinedOutput()
	if err != nil {
		return Actual{}, fmt.Errorf("%w (output: %s)", err, string(out))
	}
	return parseReport(string(out))
}

// parseReport extracts the numeric counters from the dry-run output.
// Lines of interest look like:
//
//	  authors:     1
//	  categories:  3
//	  ...
//
// Anything else is ignored, so noise on stderr or extra lines won't
// trip the parse.
func parseReport(s string) (Actual, error) {
	a := Actual{}
	scanner := bufio.NewScanner(strings.NewReader(s))
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		idx := strings.Index(line, ":")
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		n, err := strconv.Atoi(val)
		if err != nil {
			continue
		}
		switch key {
		case "authors":
			a.Authors = n
		case "categories":
			a.Categories = n
		case "tags":
			a.Tags = n
		case "posts":
			a.Posts = n
		case "attachments":
			a.Attachments = n
		case "comments":
			a.Comments = n
		case "errors":
			a.Errors = n
		}
	}
	if err := scanner.Err(); err != nil {
		return Actual{}, fmt.Errorf("scan: %w", err)
	}
	return a, nil
}

// diffActual returns a short, single-line description of the first
// mismatching field, or "" when every field matches the expected
// values. We bail on the first diff because the operator-facing
// report is more useful when concise.
func diffActual(e Expected, a Actual) string {
	checks := []struct {
		name        string
		want, got   int
		isCeiling   bool
		ceilingName string
	}{
		{name: "authors", want: e.Authors, got: a.Authors},
		{name: "categories", want: e.Categories, got: a.Categories},
		{name: "tags", want: e.Tags, got: a.Tags},
		{name: "posts", want: e.Posts, got: a.Posts},
		{name: "attachments", want: e.Attachments, got: a.Attachments},
		{name: "comments", want: e.Comments, got: a.Comments},
	}
	for _, c := range checks {
		if c.want != c.got {
			return fmt.Sprintf("%s: want %d got %d", c.name, c.want, c.got)
		}
	}
	if a.Errors > e.ErrorsMax {
		return fmt.Sprintf("errors: %d > errors_max %d", a.Errors, e.ErrorsMax)
	}
	return ""
}
