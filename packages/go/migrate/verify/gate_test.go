package verify

import (
	"errors"
	"strings"
	"testing"
)

// TestGate_Decide_Pass and _Fail cover the gate's only branch.
func TestGate_Decide_Pass(t *testing.T) {
	r := &Report{ChecksTotal: 100, Passed: 96, Failed: 4}
	r.Finalize()
	if got := r.Fidelity; got < 0.95 {
		t.Fatalf("setup: fidelity %v < 0.95", got)
	}
	ok, err := Gate{MinFidelity: 0.95}.Decide(r)
	if !ok {
		t.Errorf("Decide: ok=false, want true")
	}
	if err != nil {
		t.Errorf("Decide: err=%v, want nil", err)
	}
}

func TestGate_Decide_Fail(t *testing.T) {
	r := &Report{ChecksTotal: 100, Passed: 94, Failed: 6}
	r.Finalize()
	ok, err := Gate{MinFidelity: 0.95}.Decide(r)
	if ok {
		t.Errorf("Decide: ok=true, want false")
	}
	if err == nil || !errors.Is(err, ErrGate) {
		t.Errorf("Decide: err=%v, want errors.Is(err, ErrGate)", err)
	}
	if !strings.Contains(err.Error(), "0.94") {
		t.Errorf("Decide error message should include observed fidelity, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "0.95") {
		t.Errorf("Decide error message should include required minimum, got %q", err.Error())
	}
}

// TestGate_Decide_DefaultThreshold covers the zero-value path: a
// Gate{} should reject < 0.95 and accept >= 0.95.
func TestGate_Decide_DefaultThreshold(t *testing.T) {
	pass := &Report{ChecksTotal: 100, Passed: 96, Failed: 4}
	pass.Finalize()
	if ok, _ := (Gate{}).Decide(pass); !ok {
		t.Errorf("zero-value gate should accept 0.96 fidelity")
	}
	fail := &Report{ChecksTotal: 100, Passed: 94, Failed: 6}
	fail.Finalize()
	if ok, _ := (Gate{}).Decide(fail); ok {
		t.Errorf("zero-value gate should reject 0.94 fidelity")
	}
}

// TestGate_Decide_Boundary checks the inclusive lower bound: exactly
// MinFidelity is a pass.
func TestGate_Decide_Boundary(t *testing.T) {
	r := &Report{ChecksTotal: 100, Passed: 95, Failed: 5}
	r.Finalize()
	if ok, err := (Gate{MinFidelity: 0.95}).Decide(r); !ok || err != nil {
		t.Errorf("Decide at boundary: ok=%v err=%v", ok, err)
	}
}

// TestGate_Decide_NilReport returns ErrGate rather than panicking.
func TestGate_Decide_NilReport(t *testing.T) {
	ok, err := Gate{MinFidelity: 0.95}.Decide(nil)
	if ok {
		t.Errorf("Decide(nil): ok=true, want false")
	}
	if err == nil || !errors.Is(err, ErrGate) {
		t.Errorf("Decide(nil): err=%v, want ErrGate", err)
	}
}

// TestGate_Decide_ClampOutOfRange ensures the gate doesn't pass on
// pathological MinFidelity values (negative or >1).
func TestGate_Decide_ClampOutOfRange(t *testing.T) {
	r := &Report{ChecksTotal: 10, Passed: 5, Failed: 5}
	r.Finalize()
	// MinFidelity=-1 → clamps to 0 → always passes.
	if ok, _ := (Gate{MinFidelity: -1}).Decide(r); !ok {
		t.Errorf("MinFidelity=-1 should clamp to 0 and pass")
	}
	// MinFidelity=2 → clamps to 1 → only 100% passes.
	if ok, _ := (Gate{MinFidelity: 2}).Decide(r); ok {
		t.Errorf("MinFidelity=2 should clamp to 1 and require fidelity=1.0")
	}
	perfect := &Report{ChecksTotal: 10, Passed: 10, Failed: 0}
	perfect.Finalize()
	if ok, _ := (Gate{MinFidelity: 2}).Decide(perfect); !ok {
		t.Errorf("MinFidelity=2 with fidelity=1.0 should pass")
	}
}

// TestReport_Finalize_Empty handles the zero-checks case without
// dividing by zero.
func TestReport_Finalize_Empty(t *testing.T) {
	r := &Report{}
	r.Finalize()
	if r.Fidelity != 0 {
		t.Errorf("Fidelity on empty report: got %v want 0", r.Fidelity)
	}
}

// TestReport_AddPass_AddFailure_Tallies covers the counter
// bookkeeping shape so future refactors don't drift.
func TestReport_AddPass_AddFailure_Tallies(t *testing.T) {
	r := &Report{}
	r.AddPass("ok")
	r.AddPass("ok")
	r.AddFailure(Failure{CheckName: "boom", Severity: SeverityError, Reason: "x"})
	if r.ChecksTotal != 3 {
		t.Errorf("ChecksTotal: got %d want 3", r.ChecksTotal)
	}
	if r.Passed != 2 {
		t.Errorf("Passed: got %d want 2", r.Passed)
	}
	if r.Failed != 1 {
		t.Errorf("Failed: got %d want 1", r.Failed)
	}
	if len(r.Failures) != 1 {
		t.Errorf("len(Failures): got %d want 1", len(r.Failures))
	}
	r.Finalize()
	if r.Fidelity < 0.66 || r.Fidelity > 0.67 {
		t.Errorf("Fidelity ~ 2/3: got %v", r.Fidelity)
	}
}

// TestReport_HasErrors filters warn vs error.
func TestReport_HasErrors(t *testing.T) {
	r := &Report{}
	r.AddFailure(Failure{CheckName: "x", Severity: SeverityWarn, Reason: "w"})
	if r.HasErrors() {
		t.Errorf("warn-only report should report HasErrors=false")
	}
	r.AddFailure(Failure{CheckName: "y", Severity: SeverityError, Reason: "e"})
	if !r.HasErrors() {
		t.Errorf("error-bearing report should report HasErrors=true")
	}
}

// TestSourceDepthHistogram covers the source-side depth derivation
// independently of any DB call. Mirrors the histogram shape the
// comments comparator expects.
func TestSourceDepthHistogram(t *testing.T) {
	// Two roots, one with a deeper subtree: c1 → c3 → c4; c2 is
	// a second top-level. Yields {1:2, 2:1, 3:1}.
	cs := makeComments("c1", "0", "c2", "0", "c3", "c1", "c4", "c3")
	h := sourceDepthHistogram(cs)
	if h[1] != 2 {
		t.Errorf("depth 1: got %d want 2", h[1])
	}
	if h[2] != 1 {
		t.Errorf("depth 2: got %d want 1", h[2])
	}
	if h[3] != 1 {
		t.Errorf("depth 3: got %d want 1", h[3])
	}
}

// TestEqualHistograms covers byte-equality on a few cases.
func TestEqualHistograms(t *testing.T) {
	a := map[int]int{1: 2, 2: 1}
	if !equalHistograms(a, map[int]int{1: 2, 2: 1}) {
		t.Errorf("identical histograms should compare equal")
	}
	if equalHistograms(a, map[int]int{1: 2}) {
		t.Errorf("different sizes should compare unequal")
	}
	if equalHistograms(a, map[int]int{1: 2, 2: 2}) {
		t.Errorf("different values should compare unequal")
	}
}

// TestFormatHistogram emits stable key ordering.
func TestFormatHistogram(t *testing.T) {
	h := map[int]int{3: 1, 1: 2, 2: 1}
	if got := formatHistogram(h); got != "{1:2,2:1,3:1}" {
		t.Errorf("formatHistogram: got %q want %q", got, "{1:2,2:1,3:1}")
	}
	if got := formatHistogram(map[int]int{}); got != "{}" {
		t.Errorf("formatHistogram(empty): got %q want %q", got, "{}")
	}
}

// TestSlugFromPath covers a few WP link shapes.
func TestSlugFromPath(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/hello-world/", "hello-world"},
		{"/2024/03/14/hello-world/", "hello-world"},
		{"hello-world", "hello-world"},
		{"/", ""},
		{"", ""},
	}
	for _, tc := range cases {
		if got := slugFromPath(tc.in); got != tc.want {
			t.Errorf("slugFromPath(%q) = %q want %q", tc.in, got, tc.want)
		}
	}
}

// TestExtractPath covers URL and bare-path inputs.
func TestExtractPath(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://ten.example.com/hello-world/", "/hello-world/"},
		{"/hello-world/", "/hello-world/"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := extractPath(tc.in); got != tc.want {
			t.Errorf("extractPath(%q) = %q want %q", tc.in, got, tc.want)
		}
	}
}
