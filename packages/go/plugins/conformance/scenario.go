package conformance

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/plugins/fakehost"
)

// Status is the outcome of one [Scenario]. We deliberately use the
// same status set as the plugintest package so the report shape is
// homogeneous: a marketplace dashboard that knows how to display
// plugintest rows displays conformance rows for free.
type Status string

const (
	StatusPass    Status = "pass"
	StatusFail    Status = "fail"
	StatusSkipped Status = "skipped"
)

// Scenario is one test case in the suite. Scenarios are constructed
// either programmatically (the BuiltinScenarios returned below) or
// loaded from a YAML/JSON fixture (see fixture.go).
//
// A scenario receives:
//
//   - The parsed manifest of the bundle under test.
//   - A fresh [fakehost.Host] (per-scenario isolation).
//   - A context with a 5s default deadline (the runner can shorten
//     this; the synthetic-budget scenarios pin a 1s deadline).
//
// It returns a single [ScenarioResult]. If the function panics, the
// runner captures the panic and reports it as a Fail with the panic
// reason in the message.
type Scenario struct {
	// Name is a stable dotted identifier (e.g. "capabilities.match",
	// "hooks.vocabulary"). Marketplace ingest keys on this — keep
	// stable across releases.
	Name string

	// Description is a one-line human summary. Optional.
	Description string

	// Run is the scenario body. Must be non-nil. The fakehost.Host
	// is fresh per call; the scenario MUST NOT capture it across
	// calls.
	Run func(ctx context.Context, m *Manifest, h *fakehost.Host) ScenarioResult
}

// ScenarioResult is the outcome of one Scenario.
type ScenarioResult struct {
	// Name mirrors Scenario.Name. Filled in by the runner so the
	// scenario body doesn't have to remember.
	Name string `json:"name"`

	// Status is one of pass | fail | skipped.
	Status Status `json:"status"`

	// Message is a short human-readable summary.
	Message string `json:"message,omitempty"`

	// Reason is the canonical machine reason (e.g.
	// "runtime-not-available" for scenarios that need the host).
	// Empty for pass/fail.
	Reason string `json:"reason,omitempty"`

	// Events is the recorded fakehost trace at the moment the
	// scenario finished. Useful for debugging failures; omitted
	// from the JSON if empty.
	Events []fakehost.Event `json:"events,omitempty"`

	// Duration is how long the scenario took. Captured for the
	// fuel/timeout scenario to assert respect-the-budget.
	Duration time.Duration `json:"duration_ms,omitempty"`
}

// Report aggregates all scenario results. The JSON shape is what
// the `--suite=conformance --json` mode emits and what the
// marketplace ingestor consumes.
type Report struct {
	// Bundle is the path the runner was asked to validate.
	Bundle string `json:"bundle"`

	// Suite is the suite tag ("conformance"). Reserved for when we
	// add other named suites (e.g. "security", "performance").
	Suite string `json:"suite"`

	// Pass is true iff no scenario failed. Skipped scenarios do
	// not count against Pass.
	Pass bool `json:"pass"`

	// Results is the ordered list of scenario outcomes. Order
	// matches BuiltinScenarios + any user-supplied fixtures sorted
	// by Name.
	Results []ScenarioResult `json:"results"`
}

// Add appends a result and recomputes Pass.
func (r *Report) Add(s ScenarioResult) {
	r.Results = append(r.Results, s)
	r.Pass = r.computePass()
}

func (r *Report) computePass() bool {
	for _, s := range r.Results {
		if s.Status == StatusFail {
			return false
		}
	}
	return true
}

// WriteHuman writes a terminal-friendly summary. Each row is one
// scenario; the summary tallies pass/fail/skip.
func (r *Report) WriteHuman(w io.Writer) error {
	for _, s := range r.Results {
		mark := "OK"
		switch s.Status {
		case StatusFail:
			mark = "FAIL"
		case StatusSkipped:
			mark = "SKIP"
		}
		line := fmt.Sprintf("  %-4s  %s", mark, s.Name)
		if s.Message != "" {
			line += "  " + s.Message
		} else if s.Reason != "" {
			line += "  (" + s.Reason + ")"
		}
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	summary := "PASS"
	if !r.Pass {
		summary = "FAIL"
	}
	fail, skip := 0, 0
	for _, s := range r.Results {
		switch s.Status {
		case StatusFail:
			fail++
		case StatusSkipped:
			skip++
		}
	}
	_, err := fmt.Fprintf(w, "\nconformance %s — %d scenarios (%d failed, %d skipped)\n",
		summary, len(r.Results), fail, skip)
	return err
}

// WriteJSON emits the report as pretty JSON.
func (r *Report) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// Pass returns a passing ScenarioResult.
func Pass(name, msg string) ScenarioResult {
	return ScenarioResult{Name: name, Status: StatusPass, Message: msg}
}

// Fail returns a failing ScenarioResult.
func Fail(name string, err error) ScenarioResult {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	return ScenarioResult{Name: name, Status: StatusFail, Message: msg}
}

// Skip returns a skipped ScenarioResult. reason is the canonical
// short token; msg is the human-friendly version.
func Skip(name, reason, msg string) ScenarioResult {
	return ScenarioResult{Name: name, Status: StatusSkipped, Message: msg, Reason: reason}
}

// stableNames returns the names of scenarios in alphabetic order so
// the result list is deterministic across runs. We sort by name so
// new scenarios slot in naturally; relying on `range` order in a
// map would yield random output and break golden tests.
func stableNames(scenarios []Scenario) []string {
	out := make([]string, len(scenarios))
	for i, s := range scenarios {
		out[i] = s.Name
	}
	sort.Strings(out)
	return out
}

// joinStrings is fmt.Stringers-friendly. We avoid pulling
// strings.Join into every call site.
func joinStrings(in []string, sep string) string {
	return strings.Join(in, sep)
}
