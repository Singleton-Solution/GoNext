package bench

import (
	"fmt"
)

// SLOVerdict is the bucket-vs-result comparison written into every
// Report. The CLI uses Passed to set the process exit code.
type SLOVerdict struct {
	Passed     bool     `json:"passed"`
	Violations []string `json:"violations,omitempty"`
}

// EvaluateSLO checks the report's percentiles + error rate against the
// scenario's bucket and mutates r.SLO with the verdict.
//
// Rules:
//   - Empty runs (Requests == 0) FAIL with a synthetic "no samples"
//     violation — a green report with zero data would be misleading
//     in CI.
//   - p95 > bucket.P95            -> violation.
//   - p99 > bucket.P99            -> violation.
//   - error_rate > MaxErrorRate   -> violation.
//
// Each rule is independent so the human-readable report can list every
// reason a run failed in one pass.
func EvaluateSLO(r *Report) {
	v := SLOVerdict{Passed: true}

	if r.Requests == 0 {
		v.Passed = false
		v.Violations = append(v.Violations, "no samples collected — runner never observed a response")
		r.SLO = v
		return
	}

	if r.Bucket.P95 > 0 && r.P95 > r.Bucket.P95 {
		v.Passed = false
		v.Violations = append(v.Violations,
			fmt.Sprintf("p95 %s exceeds budget %s", r.P95, r.Bucket.P95))
	}
	if r.Bucket.P99 > 0 && r.P99 > r.Bucket.P99 {
		v.Passed = false
		v.Violations = append(v.Violations,
			fmt.Sprintf("p99 %s exceeds budget %s", r.P99, r.Bucket.P99))
	}
	if r.Bucket.MaxErrorRate > 0 && r.ErrorRate > r.Bucket.MaxErrorRate {
		v.Passed = false
		v.Violations = append(v.Violations,
			fmt.Sprintf("error rate %.2f%% exceeds budget %.2f%%",
				r.ErrorRate*100, r.Bucket.MaxErrorRate*100))
	}

	r.SLO = v
}
