package plugintest

import (
	"fmt"
	"io"
	"strings"
)

// Status is the outcome of a single [Check].
//
// The set is deliberately small. "skipped" exists because some checks in the
// contract (see [docs/11-testing-ci.md] §7.1) need the WASM host, which has
// not landed yet — they emit a row with this status so the report shape
// stays stable as runtime support arrives.
type Status string

const (
	StatusPass    Status = "pass"
	StatusFail    Status = "fail"
	StatusSkipped Status = "skipped"
)

// Check is one row in a contract [Report]. The JSON tags are part of the
// public, marketplace-ingestible shape — do not rename without bumping the
// report schema.
type Check struct {
	// Name is a stable, dotted identifier (e.g. "manifest.schema",
	// "wasm.module"). Marketplace dashboards key on this — keep it stable.
	Name string `json:"name"`

	// Status is one of pass | fail | skipped.
	Status Status `json:"status"`

	// Message is a short human-readable summary. For passes it can be empty
	// or a one-line confirmation; for fails it's the diagnostic; for skips
	// it explains why.
	Message string `json:"message,omitempty"`

	// Reason is the canonical machine reason for a skip (e.g.
	// "runtime-not-available"). Empty for pass/fail.
	Reason string `json:"reason,omitempty"`
}

// Report is the result of running the contract checks against one bundle.
// The JSON shape is the marketplace publish payload — fields are append-only
// and changes go through a schema bump.
type Report struct {
	// Bundle is the path the runner was asked to validate.
	Bundle string `json:"bundle"`

	// Pass is true if no [Check] in [Report.Checks] failed. Skipped checks
	// do not count against Pass — the alternative would mean every report
	// fails until the WASM host lands.
	Pass bool `json:"pass"`

	// Checks is the ordered list of rows. Order matches the contract
	// section order in docs/11-testing-ci.md §7.1.
	Checks []Check `json:"checks"`
}

// Add appends a [Check] to the report and recomputes [Report.Pass].
func (r *Report) Add(c Check) {
	r.Checks = append(r.Checks, c)
	r.Pass = r.computePass()
}

// computePass returns true iff no check has [StatusFail].
func (r *Report) computePass() bool {
	for _, c := range r.Checks {
		if c.Status == StatusFail {
			return false
		}
	}
	return true
}

// WriteHuman writes the human-readable PASS/FAIL/SKIP table to w. The format
// is intentionally simple — one row per check — so it parses well in CI logs
// and reads cleanly in a terminal.
func (r *Report) WriteHuman(w io.Writer) error {
	for _, c := range r.Checks {
		tag := strings.ToUpper(string(c.Status))
		// Pad to a fixed width so columns line up.
		line := fmt.Sprintf("%-7s  %s", tag, c.Name)
		if c.Message != "" {
			line += "  " + c.Message
		} else if c.Reason != "" {
			line += "  (" + c.Reason + ")"
		}
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	summary := "OK"
	if !r.Pass {
		summary = "FAIL"
	}
	if _, err := fmt.Fprintf(w, "\n%s — %d checks (%d failed, %d skipped)\n",
		summary, len(r.Checks), r.failCount(), r.skipCount()); err != nil {
		return err
	}
	return nil
}

func (r *Report) failCount() int {
	n := 0
	for _, c := range r.Checks {
		if c.Status == StatusFail {
			n++
		}
	}
	return n
}

func (r *Report) skipCount() int {
	n := 0
	for _, c := range r.Checks {
		if c.Status == StatusSkipped {
			n++
		}
	}
	return n
}

// Pass returns a passing [Check] with the given name and optional message.
func Pass(name, msg string) Check {
	return Check{Name: name, Status: StatusPass, Message: msg}
}

// Fail returns a failing [Check] with the given name and diagnostic.
func Fail(name string, err error) Check {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	return Check{Name: name, Status: StatusFail, Message: msg}
}

// Skip returns a [StatusSkipped] [Check] with the given reason string. The
// canonical reason for "the WASM host hasn't shipped yet" is
// "runtime-not-available".
func Skip(name, reason string) Check {
	msg := ""
	switch reason {
	case "runtime-not-available":
		msg = "runtime not yet available"
	}
	return Check{Name: name, Status: StatusSkipped, Message: msg, Reason: reason}
}
