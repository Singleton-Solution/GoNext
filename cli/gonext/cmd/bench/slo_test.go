package bench

import (
	"strings"
	"testing"
	"time"

	"github.com/Singleton-Solution/GoNext/cli/gonext/cmd/bench/scenarios"
)

func TestEvaluateSLO_PassPath(t *testing.T) {
	rep := Report{
		Bucket:    scenarios.SLO{P95: 250 * time.Millisecond, P99: 500 * time.Millisecond, MaxErrorRate: 0.01},
		Requests:  1000,
		P95:       200 * time.Millisecond,
		P99:       400 * time.Millisecond,
		ErrorRate: 0.005,
	}
	EvaluateSLO(&rep)
	if !rep.SLO.Passed {
		t.Errorf("expected pass, got %+v", rep.SLO)
	}
	if len(rep.SLO.Violations) != 0 {
		t.Errorf("expected no violations, got %v", rep.SLO.Violations)
	}
}

func TestEvaluateSLO_P95Fail(t *testing.T) {
	rep := Report{
		Bucket:    scenarios.SLO{P95: 250 * time.Millisecond, P99: 500 * time.Millisecond, MaxErrorRate: 0.01},
		Requests:  100,
		P95:       300 * time.Millisecond, // over budget
		P99:       400 * time.Millisecond,
		ErrorRate: 0.0,
	}
	EvaluateSLO(&rep)
	if rep.SLO.Passed {
		t.Errorf("expected fail, got pass")
	}
	if !containsAny(rep.SLO.Violations, "p95") {
		t.Errorf("expected p95 violation, got %v", rep.SLO.Violations)
	}
}

func TestEvaluateSLO_P99Fail(t *testing.T) {
	rep := Report{
		Bucket:    scenarios.SLO{P95: 250 * time.Millisecond, P99: 500 * time.Millisecond},
		Requests:  100,
		P95:       200 * time.Millisecond,
		P99:       600 * time.Millisecond, // over budget
	}
	EvaluateSLO(&rep)
	if rep.SLO.Passed {
		t.Errorf("expected fail")
	}
	if !containsAny(rep.SLO.Violations, "p99") {
		t.Errorf("expected p99 violation, got %v", rep.SLO.Violations)
	}
}

func TestEvaluateSLO_ErrorRateFail(t *testing.T) {
	rep := Report{
		Bucket:    scenarios.SLO{MaxErrorRate: 0.01},
		Requests:  100,
		ErrorRate: 0.05,
	}
	EvaluateSLO(&rep)
	if rep.SLO.Passed {
		t.Errorf("expected fail")
	}
	if !containsAny(rep.SLO.Violations, "error rate") {
		t.Errorf("expected error-rate violation, got %v", rep.SLO.Violations)
	}
}

func TestEvaluateSLO_EmptyReportFails(t *testing.T) {
	rep := Report{
		Bucket:   scenarios.SLO{P95: time.Second, P99: time.Second, MaxErrorRate: 1.0},
		Requests: 0,
	}
	EvaluateSLO(&rep)
	if rep.SLO.Passed {
		t.Errorf("empty run must FAIL; got pass")
	}
	if !containsAny(rep.SLO.Violations, "no samples") {
		t.Errorf("expected 'no samples' violation, got %v", rep.SLO.Violations)
	}
}

func TestEvaluateSLO_MultipleViolationsListed(t *testing.T) {
	rep := Report{
		Bucket:    scenarios.SLO{P95: 100 * time.Millisecond, P99: 200 * time.Millisecond, MaxErrorRate: 0.01},
		Requests:  100,
		P95:       300 * time.Millisecond,
		P99:       500 * time.Millisecond,
		ErrorRate: 0.5,
	}
	EvaluateSLO(&rep)
	if rep.SLO.Passed {
		t.Errorf("expected fail")
	}
	if len(rep.SLO.Violations) != 3 {
		t.Errorf("expected 3 violations, got %d: %v", len(rep.SLO.Violations), rep.SLO.Violations)
	}
}

func TestEvaluateSLO_ZeroBucketFieldsSkipped(t *testing.T) {
	// A scenario with no P99 budget should not flag p99.
	rep := Report{
		Bucket:    scenarios.SLO{P95: 250 * time.Millisecond}, // P99 and MaxErrorRate zero
		Requests:  10,
		P95:       100 * time.Millisecond,
		P99:       10 * time.Second, // would trip if checked
		ErrorRate: 0.5,
	}
	EvaluateSLO(&rep)
	if !rep.SLO.Passed {
		t.Errorf("zero-budget fields should be skipped; got violations %v", rep.SLO.Violations)
	}
}

func containsAny(haystack []string, needle string) bool {
	for _, s := range haystack {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}
