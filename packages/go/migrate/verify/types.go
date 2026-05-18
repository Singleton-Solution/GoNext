package verify

import (
	"errors"
	"fmt"
	"time"
)

// Severity classifies how much a failure should weigh on the gate
// decision. Both severities count the same toward the fidelity
// ratio (one check, one tally), but the operator-facing report
// groups them separately so warn-level drift is visible without
// being mistaken for a hard data-loss event.
type Severity string

const (
	// SeverityWarn marks a non-blocking discrepancy: a missing
	// piece of metadata, a permalink shape that doesn't match
	// exactly but resolves, a comment whose ordering shifted.
	// Operators can usually proceed past warn-level failures.
	SeverityWarn Severity = "warn"

	// SeverityError marks a hard fidelity loss: a missing post, a
	// title mutation, a deleted comment subtree, a term that
	// silently changed taxonomy. These are the rows the gate's
	// MinFidelity threshold is designed to catch.
	SeverityError Severity = "error"
)

// Failure is one record-scoped check failure recorded on a Report.
// The fields are string-y so callers (CLI, JSON consumers) don't
// have to know about the source record's typed shape — Source and
// Target carry whatever the comparator wants the operator to see
// when triaging the row.
type Failure struct {
	// CheckName identifies the comparator that produced the
	// failure. Conventions:
	//   - "posts.count"          — top-level cardinality check
	//   - "posts.title"          — per-post title comparison
	//   - "posts.content"        — content round-trip
	//   - "posts.status"         — status preserved
	//   - "posts.author"         — author resolved
	//   - "terms.count"          — term cardinality
	//   - "terms.name"           — per-term name/slug/taxonomy
	//   - "comments.count"       — per-post comment cardinality
	//   - "comments.path"        — ltree depth shape preserved
	//   - "users.count"          — author cardinality
	//   - "users.email"          — email preserved
	//   - "users.must_reset"     — meta.must_reset_password flag
	//   - "permalinks.resolve"   — WP <link> resolves on GoNext side
	CheckName string

	// Severity is one of SeverityWarn / SeverityError. See those
	// constants for guidance on which to use.
	Severity Severity

	// Reason is a human-readable explanation of what went wrong.
	// Suitable for CLI output; mirrored on the JSON projection.
	Reason string

	// Source is a short identifier for the source-side record —
	// for posts the wp:post_id, for terms the slug, for comments
	// the wp:comment_id, etc. Empty when the failure is
	// aggregate (e.g. a count mismatch with no single offending
	// record).
	Source string

	// Target is the GoNext-side identifier (UUID stringified)
	// when one was resolved, or the value the comparator found
	// when it differed from Source. Empty when not applicable.
	Target string
}

// Report summarises a single Verifier.Run invocation.
//
// The counters are accumulated as the comparators run: each
// individual check (one per record per comparator) bumps
// ChecksTotal, and either Passed or Failed depending on the
// outcome. The fidelity ratio is recomputed in Finalize once the
// run completes — callers should not divide Passed/ChecksTotal
// themselves because the rounding policy is centralised there.
type Report struct {
	// ChecksTotal is the number of individual checks the
	// verifier ran. Each comparator decides what "one check"
	// means for it (typically: one per source record, plus a
	// top-level cardinality check), so the totals across runs on
	// different fixtures are not directly comparable.
	ChecksTotal int

	// Passed is the number of checks that found no failure.
	// Passed + Failed == ChecksTotal once Finalize has run.
	Passed int

	// Failed is the number of checks that recorded a Failure.
	// Always equals len(Failures) after Finalize.
	Failed int

	// Fidelity is the ratio Passed / ChecksTotal in [0, 1].
	// Zero when ChecksTotal is zero (an empty WXR has nothing to
	// verify). Computed by Finalize, not the live increment path,
	// so the field is meaningful only after Verifier.Run returns.
	Fidelity float64

	// Failures is the per-record failure list. Iterated in
	// comparator order, then source order within a comparator.
	// Never nil-checked by callers: an empty slice means "no
	// failures" and an unset slice means the same thing.
	Failures []Failure

	// Took is the wall-clock duration of the Run. Pinned to zero
	// when Verifier.Now is nil and Run is invoked without a real
	// clock (rare; only the test stubs do that).
	Took time.Duration
}

// AddPass records a successful check. CheckName is informational —
// we don't keep per-check pass counts (only Failures carry that
// granularity), but the parameter exists so the call sites are
// symmetric with AddFailure and a future caller can extend the
// Report shape without changing every call.
func (r *Report) AddPass(_ string) {
	if r == nil {
		return
	}
	r.ChecksTotal++
	r.Passed++
}

// AddFailure records a check failure. The Failure is appended to
// Failures and the counters are bumped. Severity controls
// presentation only; both severities count once against fidelity.
func (r *Report) AddFailure(f Failure) {
	if r == nil {
		return
	}
	r.ChecksTotal++
	r.Failed++
	r.Failures = append(r.Failures, f)
}

// Finalize computes the Fidelity ratio. Called by Verifier.Run
// before it hands the Report to the caller. Idempotent so callers
// who recompute (e.g. after stripping warn-level failures) can
// invoke it again without double-counting.
func (r *Report) Finalize() {
	if r == nil {
		return
	}
	if r.ChecksTotal == 0 {
		r.Fidelity = 0
		return
	}
	r.Fidelity = float64(r.Passed) / float64(r.ChecksTotal)
}

// HasErrors reports whether the Report carries any error-severity
// failures. Warn-level entries do not contribute. This is the
// signal the CLI uses to decide whether to surface the failures on
// stderr; the gate uses Fidelity instead.
func (r *Report) HasErrors() bool {
	if r == nil {
		return false
	}
	for _, f := range r.Failures {
		if f.Severity == SeverityError {
			return true
		}
	}
	return false
}

// ErrVerify wraps every error the verifier surfaces as a fatal
// outcome (DB unreachable, source unreadable, WXR malformed past
// recovery). Per-record failures land on Report.Failures and do not
// produce an ErrVerify.
var ErrVerify = errors.New("verify: fatal")

// wrapVerifyErr decorates a low-level error as ErrVerify so callers
// can match with errors.Is. Returns nil on a nil input so call sites
// can write `return wrapVerifyErr(err)` without an outer nil check.
func wrapVerifyErr(stage string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %s: %v", ErrVerify, stage, err)
}
