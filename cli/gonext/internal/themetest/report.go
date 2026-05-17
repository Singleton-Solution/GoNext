package themetest

import (
	"fmt"
	"io"
)

// Status is the outcome of a single Check.
type Status string

const (
	// StatusPass means the check ran and the theme satisfies the assertion.
	StatusPass Status = "PASS"
	// StatusFail means the check ran and the theme violates the assertion.
	StatusFail Status = "FAIL"
	// StatusSkip means the check was not run — typically because the
	// underlying runtime is not yet available. A skipped check does not
	// cause a non-zero exit; the runner still treats the run as passing.
	StatusSkip Status = "SKIP"
	// StatusNote is informational only. Reported under --verbose; never
	// affects exit code. Used for "advisory" findings (e.g. recommended
	// but optional templates that are missing).
	StatusNote Status = "NOTE"
)

// Check is a single row in the report.
type Check struct {
	// ID is a stable kebab-case identifier (e.g. "theme-json.present").
	// Stable across releases so CI gates and marketplace ingest can pin
	// behaviour to specific checks.
	ID string `json:"id"`
	// Title is a short human-readable description.
	Title string `json:"title"`
	// Status is the outcome.
	Status Status `json:"status"`
	// Message is the human-readable detail. On Pass it may be empty.
	// On Fail it explains the violation. On Skip it explains why the
	// check could not run.
	Message string `json:"message,omitempty"`
}

// Report is the aggregate result of a contract run.
type Report struct {
	// ThemePath is the absolute path to the directory that was tested.
	ThemePath string `json:"themePath"`
	// ThemeName is the theme's "name" from package.json, if it could
	// be read. Empty otherwise.
	ThemeName string `json:"themeName,omitempty"`
	// ThemeType is "block", "classic", or "unknown" depending on what
	// package.json declared (or what the on-disk layout suggested).
	ThemeType string `json:"themeType,omitempty"`
	// Checks is the ordered list of rows, in the order they ran.
	Checks []Check `json:"checks"`
}

// Add appends a Check. Returned for chaining in tests.
func (r *Report) Add(c Check) *Report {
	r.Checks = append(r.Checks, c)
	return r
}

// Passed reports whether the run should exit 0. The rule is:
// every check must be PASS, SKIP, or NOTE. Any FAIL fails the run.
func (r *Report) Passed() bool {
	for _, c := range r.Checks {
		if c.Status == StatusFail {
			return false
		}
	}
	return true
}

// Summary returns a one-line count of statuses, e.g.
// "5 pass, 0 fail, 4 skip".
func (r *Report) Summary() string {
	var pass, fail, skip, note int
	for _, c := range r.Checks {
		switch c.Status {
		case StatusPass:
			pass++
		case StatusFail:
			fail++
		case StatusSkip:
			skip++
		case StatusNote:
			note++
		}
	}
	return fmt.Sprintf("%d pass, %d fail, %d skip, %d note", pass, fail, skip, note)
}

// WriteText writes a human-readable rendering of the report to w. Each
// row is "STATUS  id  — message". If verbose is false, StatusNote rows
// are omitted from the per-row listing (they still count in the summary).
func (r *Report) WriteText(w io.Writer, verbose bool) error {
	if r.ThemeName != "" {
		if _, err := fmt.Fprintf(w, "Theme: %s (%s)\n", r.ThemeName, r.ThemeType); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "Path:  %s\n\n", r.ThemePath); err != nil {
		return err
	}
	for _, c := range r.Checks {
		if c.Status == StatusNote && !verbose {
			continue
		}
		line := fmt.Sprintf("%-4s  %s", c.Status, c.ID)
		if c.Message != "" {
			line += "  — " + c.Message
		}
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "\n%s\n", r.Summary()); err != nil {
		return err
	}
	return nil
}
