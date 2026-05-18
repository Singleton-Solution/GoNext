package verify

import (
	"errors"
	"fmt"
)

// DefaultMinFidelity is the gate threshold the issue brief pins:
// 95% of all checks must pass for an import to be considered
// "complete". Surfaced as a public constant so CLI flag-default
// help text and callers wanting "the same number the gate uses by
// default" share one source of truth.
const DefaultMinFidelity = 0.95

// Gate decides whether a verification Report clears the operator-
// configured fidelity threshold. The zero value applies
// DefaultMinFidelity; callers passing an explicit value can lower
// the bar for staging environments or raise it for production
// migrations where data loss is unacceptable.
//
// Gate is intentionally tiny — it owns one decision. Anything more
// elaborate (per-check thresholds, severity weighting) belongs in
// the comparator that produces the failures, not in the gate.
type Gate struct {
	// MinFidelity is the inclusive lower bound on Report.Fidelity
	// that counts as a pass. The zero value is treated as
	// DefaultMinFidelity. Values are clamped to [0, 1] in Decide.
	MinFidelity float64
}

// ErrGate is the sentinel that wraps every failed gate decision.
// Callers use errors.Is to distinguish a gate denial from a fatal
// verifier error.
var ErrGate = errors.New("verify: gate failed")

// Decide returns (ok, err) for a given report.
//
//   - ok == true / err == nil: the report's fidelity meets or
//     exceeds MinFidelity.
//   - ok == false / err wraps ErrGate: the threshold wasn't met.
//     The error message includes the observed fidelity and the
//     required minimum so an operator reading a CI failure log
//     gets the context they need to act.
//
// Decide is read-only: it does not mutate the report (call
// report.Finalize() first if you've been hand-modifying counters;
// Verifier.Run already does this).
func (g Gate) Decide(report *Report) (bool, error) {
	min := g.MinFidelity
	if min == 0 {
		min = DefaultMinFidelity
	}
	if min < 0 {
		min = 0
	}
	if min > 1 {
		min = 1
	}
	if report == nil {
		return false, fmt.Errorf("%w: nil report", ErrGate)
	}
	if report.Fidelity >= min {
		return true, nil
	}
	return false, fmt.Errorf(
		"%w: fidelity %.4f below required %.4f (checks=%d passed=%d failed=%d)",
		ErrGate, report.Fidelity, min,
		report.ChecksTotal, report.Passed, report.Failed,
	)
}
