package backpressure_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/Singleton-Solution/GoNext/packages/go/jobs/backpressure"
)

// stubSource is a trivial DepthSource for the tests in this file. It
// stores a single int and returns it for every queue lookup; tests
// that need per-queue depths use stubSourceMap below. We keep both
// types here (rather than in monitor_test.go) so the Gate tests have
// no dependency on the Monitor internals — the Gate is a pure
// function of (Threshold, depth, priority) and the tests should
// document that.
type stubSource struct {
	mu     sync.RWMutex
	depths map[string]int
}

func newStubSource() *stubSource {
	return &stubSource{depths: map[string]int{}}
}

func (s *stubSource) Depth(queue string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.depths[queue]
}

func (s *stubSource) set(queue string, depth int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.depths[queue] = depth
}

// TestNewGateRejectsInvalidThresholds pins the validation contract.
// Each subcase is a configuration we'd want to fail at boot rather
// than silently misbehave at request time.
func TestNewGateRejectsInvalidThresholds(t *testing.T) {
	src := newStubSource()
	cases := []struct {
		name string
		in   []backpressure.Threshold
	}{
		{"empty queue", []backpressure.Threshold{{Queue: "", SoftLimit: 10, HardLimit: 20}}},
		{"zero soft", []backpressure.Threshold{{Queue: "q", SoftLimit: 0, HardLimit: 10}}},
		{"negative soft", []backpressure.Threshold{{Queue: "q", SoftLimit: -1, HardLimit: 10}}},
		{"hard below soft", []backpressure.Threshold{{Queue: "q", SoftLimit: 10, HardLimit: 5}}},
		{"duplicate queue", []backpressure.Threshold{
			{Queue: "q", SoftLimit: 10, HardLimit: 20},
			{Queue: "q", SoftLimit: 30, HardLimit: 40},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := backpressure.NewGate(src, tc.in)
			if !errors.Is(err, backpressure.ErrInvalidThreshold) {
				t.Fatalf("want ErrInvalidThreshold, got %v", err)
			}
		})
	}
}

// TestNewGateRejectsNilSource is a contract check — a nil DepthSource
// would panic at the first Allow call in production, so we fail fast.
func TestNewGateRejectsNilSource(t *testing.T) {
	_, err := backpressure.NewGate(nil, []backpressure.Threshold{{Queue: "q", SoftLimit: 1, HardLimit: 2}})
	if !errors.Is(err, backpressure.ErrInvalidThreshold) {
		t.Fatalf("want ErrInvalidThreshold for nil source, got %v", err)
	}
}

// TestGateAllowBelowSoftLimit pins the "happy path" — when the queue
// is below SoftLimit, every priority including Background passes.
// This is the most common state in production and the case where
// shedding would be most disruptive (false positives turn into
// dropped jobs).
func TestGateAllowBelowSoftLimit(t *testing.T) {
	src := newStubSource()
	src.set("webhook", 5) // soft=10 → below
	gate, err := backpressure.NewGate(src, []backpressure.Threshold{
		{Queue: "webhook", SoftLimit: 10, HardLimit: 20},
	})
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}
	for _, p := range []backpressure.Priority{
		backpressure.Background,
		backpressure.Normal,
		backpressure.Important,
		backpressure.Critical,
	} {
		if err := gate.Allow("webhook", p); err != nil {
			t.Errorf("priority=%s below soft limit: want nil, got %v", p, err)
		}
	}
}

// TestGateAllowBetweenSoftAndHard verifies the middle band: Normal
// and Background are shed; Important and Critical still pass. This
// is the design's degraded-mode signal — non-essential traffic backs
// off while transactional flows continue.
func TestGateAllowBetweenSoftAndHard(t *testing.T) {
	src := newStubSource()
	src.set("webhook", 15) // soft=10, hard=20 → middle band
	gate, err := backpressure.NewGate(src, []backpressure.Threshold{
		{Queue: "webhook", SoftLimit: 10, HardLimit: 20},
	})
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}

	if err := gate.Allow("webhook", backpressure.Background); !errors.Is(err, backpressure.ErrShed) {
		t.Errorf("Background middle band: want ErrShed, got %v", err)
	}
	if err := gate.Allow("webhook", backpressure.Normal); !errors.Is(err, backpressure.ErrShed) {
		t.Errorf("Normal middle band: want ErrShed, got %v", err)
	}
	if err := gate.Allow("webhook", backpressure.Important); err != nil {
		t.Errorf("Important middle band: want nil, got %v", err)
	}
	if err := gate.Allow("webhook", backpressure.Critical); err != nil {
		t.Errorf("Critical middle band: want nil, got %v", err)
	}
}

// TestGateAllowAboveHard verifies the strictest band: only Critical
// passes. Important is shed here even though it survives the middle
// band — the design intent is that above HardLimit, every additional
// task makes drain time worse, and only security-critical flows
// (whose failure mode is worse than queue latency) get through.
func TestGateAllowAboveHard(t *testing.T) {
	src := newStubSource()
	src.set("webhook", 25) // soft=10, hard=20 → above hard
	gate, err := backpressure.NewGate(src, []backpressure.Threshold{
		{Queue: "webhook", SoftLimit: 10, HardLimit: 20},
	})
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}

	for _, p := range []backpressure.Priority{
		backpressure.Background,
		backpressure.Normal,
		backpressure.Important,
	} {
		if err := gate.Allow("webhook", p); !errors.Is(err, backpressure.ErrShed) {
			t.Errorf("priority=%s above hard: want ErrShed, got %v", p, err)
		}
	}
	if err := gate.Allow("webhook", backpressure.Critical); err != nil {
		t.Errorf("Critical above hard: want nil (never shed), got %v", err)
	}
}

// TestGateAllowAtBoundaries pins the off-by-one behavior at exactly
// SoftLimit and HardLimit. The "≥ SoftLimit" wording in the docs has
// historically tripped reviewers; this test makes the threshold edge
// behavior explicit.
func TestGateAllowAtBoundaries(t *testing.T) {
	src := newStubSource()
	gate, err := backpressure.NewGate(src, []backpressure.Threshold{
		{Queue: "webhook", SoftLimit: 10, HardLimit: 20},
	})
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}

	// At exactly SoftLimit: Normal/Background shed, Important/Critical pass.
	src.set("webhook", 10)
	if err := gate.Allow("webhook", backpressure.Normal); !errors.Is(err, backpressure.ErrShed) {
		t.Errorf("Normal at SoftLimit: want ErrShed, got %v", err)
	}
	if err := gate.Allow("webhook", backpressure.Important); err != nil {
		t.Errorf("Important at SoftLimit: want nil, got %v", err)
	}

	// At exactly HardLimit: Important also shed, Critical passes.
	src.set("webhook", 20)
	if err := gate.Allow("webhook", backpressure.Important); !errors.Is(err, backpressure.ErrShed) {
		t.Errorf("Important at HardLimit: want ErrShed, got %v", err)
	}
	if err := gate.Allow("webhook", backpressure.Critical); err != nil {
		t.Errorf("Critical at HardLimit: want nil, got %v", err)
	}
}

// TestGateAllowUnconfiguredQueue verifies the fail-open contract:
// queues without a registered threshold are admitted regardless of
// priority. This lets operators add new queues without atomically
// updating the gate config (a real footgun if it failed-closed).
func TestGateAllowUnconfiguredQueue(t *testing.T) {
	src := newStubSource()
	src.set("unknown", 9999)
	gate, err := backpressure.NewGate(src, []backpressure.Threshold{
		{Queue: "webhook", SoftLimit: 10, HardLimit: 20},
	})
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}
	if err := gate.Allow("unknown", backpressure.Background); err != nil {
		t.Errorf("unconfigured queue: want nil, got %v", err)
	}
}

// TestGateAllowCollapsedBand exercises the SoftLimit == HardLimit
// case. The middle band has zero width; the gate should jump from
// "everyone passes" to "only Critical passes" at the limit.
func TestGateAllowCollapsedBand(t *testing.T) {
	src := newStubSource()
	gate, err := backpressure.NewGate(src, []backpressure.Threshold{
		{Queue: "q", SoftLimit: 10, HardLimit: 10},
	})
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}

	src.set("q", 9)
	if err := gate.Allow("q", backpressure.Background); err != nil {
		t.Errorf("below collapsed band: want nil, got %v", err)
	}
	src.set("q", 10)
	if err := gate.Allow("q", backpressure.Important); !errors.Is(err, backpressure.ErrShed) {
		t.Errorf("at collapsed band: want ErrShed for Important, got %v", err)
	}
	if err := gate.Allow("q", backpressure.Critical); err != nil {
		t.Errorf("at collapsed band: want nil for Critical, got %v", err)
	}
}

// TestGateAllowNil verifies a nil *Gate is a no-op admission. This
// matches the convention used by *Middleware.Handler and lets callers
// wire conditional backpressure (off in dev, on in prod) without a
// branch at every Allow call site.
func TestGateAllowNil(t *testing.T) {
	var g *backpressure.Gate
	if err := g.Allow("q", backpressure.Background); err != nil {
		t.Errorf("nil gate Allow: want nil, got %v", err)
	}
}

// TestGateAllowConcurrent stresses the Gate with many goroutines
// reading the depth and calling Allow concurrently. With -race this
// catches any accidentally shared mutable state in the Gate or
// DepthSource. The bulk of the work is a tight loop calling Allow;
// we want race-clean behavior, not throughput numbers.
func TestGateAllowConcurrent(t *testing.T) {
	src := newStubSource()
	src.set("q", 5)
	gate, err := backpressure.NewGate(src, []backpressure.Threshold{
		{Queue: "q", SoftLimit: 10, HardLimit: 20},
	})
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}

	const goroutines = 16
	const iters = 5000
	var wg sync.WaitGroup
	priorities := []backpressure.Priority{
		backpressure.Background,
		backpressure.Normal,
		backpressure.Important,
		backpressure.Critical,
	}
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < iters; j++ {
				_ = gate.Allow("q", priorities[(idx+j)%len(priorities)])
			}
		}(i)
	}

	// While the readers run, a writer flips depth across all bands so
	// the race detector exercises both the read-while-write path and
	// the band-transition logic.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < iters; j++ {
			switch j % 3 {
			case 0:
				src.set("q", 5)
			case 1:
				src.set("q", 15)
			case 2:
				src.set("q", 25)
			}
		}
	}()

	wg.Wait()
}

// TestPriorityString pins the label values used by the Prometheus
// counter. The metric is part of the observability contract;
// renaming a label breaks dashboards. Test fails loudly on accidental
// rename.
func TestPriorityString(t *testing.T) {
	cases := map[backpressure.Priority]string{
		backpressure.Background: "background",
		backpressure.Normal:     "normal",
		backpressure.Important:  "important",
		backpressure.Critical:   "critical",
		backpressure.Priority(999): "unknown",
	}
	for p, want := range cases {
		if got := p.String(); got != want {
			t.Errorf("Priority(%d).String() = %q, want %q", int(p), got, want)
		}
	}
}

// TestGateThresholdLookup verifies the diagnostic accessor returns
// the registered Threshold and a missing-flag for unconfigured queues.
func TestGateThresholdLookup(t *testing.T) {
	src := newStubSource()
	gate, err := backpressure.NewGate(src, []backpressure.Threshold{
		{Queue: "webhook", SoftLimit: 10, HardLimit: 20},
	})
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}
	got, ok := gate.Threshold("webhook")
	if !ok || got.SoftLimit != 10 || got.HardLimit != 20 {
		t.Errorf("Threshold(webhook) = %+v, %v; want soft=10 hard=20 ok=true", got, ok)
	}
	if _, ok := gate.Threshold("missing"); ok {
		t.Errorf("Threshold(missing): want ok=false")
	}
}

// TestIsShed is a thin wrapper test for the exported predicate, but
// catches a real regression risk: if a future refactor wraps ErrShed
// in something errors.Is doesn't unwrap, every middleware caller's
// error branching would silently break.
func TestIsShed(t *testing.T) {
	src := newStubSource()
	src.set("q", 100)
	gate, err := backpressure.NewGate(src, []backpressure.Threshold{
		{Queue: "q", SoftLimit: 10, HardLimit: 20},
	})
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}
	shedErr := gate.Allow("q", backpressure.Background)
	if !backpressure.IsShed(shedErr) {
		t.Errorf("IsShed(Allow shed) = false, want true")
	}
	if backpressure.IsShed(nil) {
		t.Errorf("IsShed(nil) = true, want false")
	}
	if backpressure.IsShed(errors.New("unrelated")) {
		t.Errorf("IsShed(unrelated) = true, want false")
	}
}
