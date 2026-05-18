package health

import (
	"math"
	"testing"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/metrics"
)

func TestBuildReport_AggregatesAllSurfaces(t *testing.T) {
	r := NewRecorder(metrics.NewRegistry())
	// 4 OK + 1 error + 1 trap dispatches.
	for i := 0; i < 4; i++ {
		r.ObserveInvocation("acme.spellcheck", "post.before_save", ResultOK, 3*time.Millisecond)
	}
	r.ObserveInvocation("acme.spellcheck", "post.before_save", ResultError, 50*time.Millisecond)
	r.ObserveInvocation("acme.spellcheck", "post.before_save", ResultTrap, 800*time.Millisecond)
	r.ObserveTrap("acme.spellcheck", "wasm error: integer divide by zero",
		TrapDetail{Hook: "post.before_save", Reason: "wasm error: integer divide by zero", Payload: []byte(`{"k":"v"}`)})
	r.ObserveCapabilityDenied("acme.spellcheck", "http.fetch")
	r.ObserveCapabilityDenied("acme.spellcheck", "http.fetch")

	rep, err := r.BuildReport("acme.spellcheck")
	if err != nil {
		t.Fatalf("BuildReport: %v", err)
	}
	if rep.Plugin != "acme.spellcheck" {
		t.Errorf("rep.Plugin = %q", rep.Plugin)
	}
	if rep.Invocations != 6 {
		t.Errorf("Invocations = %d, want 6", rep.Invocations)
	}
	if rep.Errors != 2 {
		t.Errorf("Errors = %d, want 2 (error + trap)", rep.Errors)
	}
	if rep.Traps != 1 {
		t.Errorf("Traps = %d, want 1", rep.Traps)
	}
	if rep.CapabilityDenied != 2 {
		t.Errorf("CapabilityDenied = %d, want 2", rep.CapabilityDenied)
	}
	if len(rep.RecentTraps) != 1 {
		t.Errorf("RecentTraps len = %d, want 1", len(rep.RecentTraps))
	}
	// Latency: P50 should sit within the bucket [1ms, 5ms] which is
	// where 4 of the 6 observations fall; P95 reaches into the
	// upper buckets.
	if rep.Latency.P50 <= 0 {
		t.Errorf("Latency.P50 = %v, want > 0", rep.Latency.P50)
	}
	if rep.Latency.P95 < rep.Latency.P50 {
		t.Errorf("Latency.P95 (%v) < P50 (%v)", rep.Latency.P95, rep.Latency.P50)
	}
	if rep.Latency.P99 < rep.Latency.P95 {
		t.Errorf("Latency.P99 (%v) < P95 (%v)", rep.Latency.P99, rep.Latency.P95)
	}
}

func TestBuildReport_UnknownPluginReturnsZeroReport(t *testing.T) {
	r := NewRecorder(metrics.NewRegistry())
	rep, err := r.BuildReport("never.seen")
	if err != nil {
		t.Fatalf("BuildReport: %v", err)
	}
	if rep.Invocations != 0 || rep.Errors != 0 || rep.Traps != 0 || rep.CapabilityDenied != 0 {
		t.Errorf("expected zero report, got %+v", rep)
	}
	if rep.Latency.P50 != 0 || rep.Latency.P95 != 0 || rep.Latency.P99 != 0 {
		t.Errorf("expected zero latency, got %+v", rep.Latency)
	}
	if len(rep.RecentTraps) != 0 {
		t.Errorf("expected empty RecentTraps, got %d", len(rep.RecentTraps))
	}
}

func TestInterpolateQuantile_TextbookCases(t *testing.T) {
	// Histogram with 100 observations:
	//   [0,    0.01) -> 10
	//   [0.01, 0.05) -> 50
	//   [0.05, 0.10) -> 30
	//   [0.10, 0.50) -> 10
	buckets := []histBucket{
		{upper: 0.01, cumulative: 10},
		{upper: 0.05, cumulative: 60},
		{upper: 0.10, cumulative: 90},
		{upper: 0.50, cumulative: 100},
	}
	cases := []struct {
		q       float64
		wantMin float64
		wantMax float64
	}{
		{0.50, 0.01, 0.05}, // P50 lands inside bucket 2
		{0.95, 0.10, 0.50}, // P95 lands inside bucket 4
		{0.99, 0.10, 0.50},
	}
	for _, c := range cases {
		got := interpolateQuantile(buckets, 100, c.q)
		if got < c.wantMin || got > c.wantMax {
			t.Errorf("q=%v: got %v, want in [%v, %v]", c.q, got, c.wantMin, c.wantMax)
		}
	}
}

func TestInterpolateQuantile_EmptyHistogram(t *testing.T) {
	if got := interpolateQuantile(nil, 0, 0.5); got != 0 {
		t.Errorf("empty histogram: got %v, want 0", got)
	}
}

func TestInterpolateQuantile_AboveAllBuckets(t *testing.T) {
	buckets := []histBucket{
		{upper: 0.01, cumulative: 5},
	}
	// q == 1.0 with rank > cumulative falls through to the last
	// bucket upper bound.
	got := interpolateQuantile(buckets, 10, 1.0)
	if math.Abs(got-0.01) > 1e-9 {
		t.Errorf("got %v, want 0.01", got)
	}
}
