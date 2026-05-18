package health

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/metrics"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// freshRecorder builds a Recorder against a private metrics
// registry. Each test gets its own registry so concurrent runs do
// not collide on the Prometheus duplicate-registration guard.
func freshRecorder(t *testing.T) *recorder {
	t.Helper()
	return NewRecorder(metrics.NewRegistry())
}

// readCounterVecValue extracts the value for one (plugin, hook,
// result) tuple out of a CounterVec. Returns 0 if the series has
// not been observed yet — that's the same shape the report code
// sees and matches the Prometheus client_golang behaviour.
func readCounterVecValue(t *testing.T, v *prometheus.CounterVec, labels ...string) float64 {
	t.Helper()
	c, err := v.GetMetricWithLabelValues(labels...)
	if err != nil {
		t.Fatalf("GetMetricWithLabelValues: %v", err)
	}
	var m dto.Metric
	if err := c.Write(&m); err != nil {
		t.Fatalf("counter.Write: %v", err)
	}
	if m.Counter == nil || m.Counter.Value == nil {
		return 0
	}
	return *m.Counter.Value
}

func TestObserveInvocation_UpdatesCounterAndHistogram(t *testing.T) {
	r := freshRecorder(t)
	r.ObserveInvocation("acme.spellcheck", "post.before_save", ResultOK, 5*time.Millisecond)
	r.ObserveInvocation("acme.spellcheck", "post.before_save", ResultOK, 10*time.Millisecond)
	r.ObserveInvocation("acme.spellcheck", "post.before_save", ResultError, 7*time.Millisecond)

	if got := readCounterVecValue(t, r.metrics.invocations, "acme.spellcheck", "post.before_save", ResultOK); got != 2 {
		t.Errorf("invocations{ok} = %v, want 2", got)
	}
	if got := readCounterVecValue(t, r.metrics.invocations, "acme.spellcheck", "post.before_save", ResultError); got != 1 {
		t.Errorf("invocations{error} = %v, want 1", got)
	}

	h, err := r.metrics.duration.GetMetricWithLabelValues("acme.spellcheck", "post.before_save")
	if err != nil {
		t.Fatalf("GetMetricWithLabelValues: %v", err)
	}
	var m dto.Metric
	if err := h.(prometheus.Metric).Write(&m); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if m.Histogram == nil || m.Histogram.GetSampleCount() != 3 {
		t.Errorf("histogram sample count = %v, want 3", m.Histogram.GetSampleCount())
	}
}

func TestObserveTrap_RingBufferCapturesNewestFirst(t *testing.T) {
	r := NewRecorderWithRing(metrics.NewRegistry(), 3)
	for i := 0; i < 5; i++ {
		r.ObserveTrap("acme.spellcheck", "wasm trap: integer divide by zero",
			TrapDetail{Hook: "post.before_save", Reason: "x", Payload: []byte{byte(i)}})
	}
	traps := r.RecentTraps("acme.spellcheck")
	if len(traps) != 3 {
		t.Fatalf("expected ring to cap at 3, got %d", len(traps))
	}
	// Newest first: the last push was payload byte 4.
	if len(traps[0].Payload) != 1 || traps[0].Payload[0] != 4 {
		t.Errorf("ring[0].Payload = %v, want [4]", traps[0].Payload)
	}
	if traps[2].Payload[0] != 2 {
		t.Errorf("ring[2].Payload = %v, want [2]", traps[2].Payload)
	}
	// Ring entries must have unique, monotonically increasing IDs.
	if traps[0].ID <= traps[1].ID {
		t.Errorf("expected newer-first IDs, got %d, %d", traps[0].ID, traps[1].ID)
	}
}

func TestObserveTrap_NormalisesReason(t *testing.T) {
	r := freshRecorder(t)
	r.ObserveTrap("acme.spellcheck", "wasm error: integer divide by zero",
		TrapDetail{Hook: "h", Reason: "wasm error: integer divide by zero"})
	if got := readCounterVecValue(t, r.metrics.traps, "acme.spellcheck", "integer_divide_by_zero"); got != 1 {
		t.Errorf("traps{integer_divide_by_zero} = %v, want 1", got)
	}
}

func TestObserveCapabilityDenied_IncrementsCounter(t *testing.T) {
	r := freshRecorder(t)
	r.ObserveCapabilityDenied("acme.spellcheck", "http.fetch")
	r.ObserveCapabilityDenied("acme.spellcheck", "http.fetch")
	if got := readCounterVecValue(t, r.metrics.capabilityDenials, "acme.spellcheck", "http.fetch"); got != 2 {
		t.Errorf("denials{http.fetch} = %v, want 2", got)
	}
}

func TestFindTrap_RoundTripsID(t *testing.T) {
	r := freshRecorder(t)
	r.ObserveTrap("acme.spellcheck", "wasm trap: unreachable",
		TrapDetail{Hook: "post.before_save", Reason: "x", Payload: []byte("p")})
	traps := r.RecentTraps("acme.spellcheck")
	if len(traps) != 1 {
		t.Fatalf("expected 1 trap, got %d", len(traps))
	}
	ev, ok := r.FindTrap("acme.spellcheck", traps[0].ID)
	if !ok {
		t.Fatal("FindTrap returned !ok for live ID")
	}
	if ev.Reason != "wasm trap: unreachable" {
		t.Errorf("ev.Reason = %q", ev.Reason)
	}
	if _, ok := r.FindTrap("acme.spellcheck", 0); ok {
		t.Error("FindTrap returned ok for unknown ID")
	}
}

func TestPlugins_SortedDeterministic(t *testing.T) {
	r := freshRecorder(t)
	r.ObserveTrap("z", "x", TrapDetail{})
	r.ObserveTrap("a", "x", TrapDetail{})
	r.ObserveTrap("m", "x", TrapDetail{})
	got := r.Plugins()
	want := []string{"a", "m", "z"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("Plugins() = %v, want %v", got, want)
	}
}

func TestRecorder_ConcurrentObservation(t *testing.T) {
	r := freshRecorder(t)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r.ObserveInvocation("acme.spellcheck", "h", ResultOK, time.Microsecond)
			r.ObserveTrap("acme.spellcheck", "wasm trap: integer divide by zero",
				TrapDetail{Hook: "h", Payload: []byte{byte(i)}})
			r.ObserveCapabilityDenied("acme.spellcheck", "http.fetch")
		}(i)
	}
	wg.Wait()
	if got := readCounterVecValue(t, r.metrics.invocations, "acme.spellcheck", "h", ResultOK); got != 16 {
		t.Errorf("invocations = %v, want 16", got)
	}
	if got := readCounterVecValue(t, r.metrics.traps, "acme.spellcheck", "integer_divide_by_zero"); got != 16 {
		t.Errorf("traps = %v, want 16", got)
	}
	if got := readCounterVecValue(t, r.metrics.capabilityDenials, "acme.spellcheck", "http.fetch"); got != 16 {
		t.Errorf("denials = %v, want 16", got)
	}
}

func TestNormaliseReason_BoundedCardinality(t *testing.T) {
	cases := map[string]string{
		"":                                        "unknown",
		"wasm error: integer divide by zero":      "integer_divide_by_zero",
		"stack overflow":                          "stack_overflow",
		"out of bounds memory access":             "out_of_bounds",
		"unreachable instruction":                 "unreachable",
		"context canceled":                        "context_cancelled",
		"guest panic: boom":                       "panic",
		"fuel exhausted":                          "fuel_exhausted",
		"runtime: oom while growing memory":       "out_of_memory",
		"something the runtime did not classify":  "other",
	}
	for in, want := range cases {
		if got := normaliseReason(in); got != want {
			t.Errorf("normaliseReason(%q) = %q, want %q", in, got, want)
		}
	}
}
