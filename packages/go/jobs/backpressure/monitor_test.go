package backpressure

// monitor_test.go lives in the package (not `backpressure_test`) so it
// can exercise the unexported setDepth helper that lets Gate +
// Middleware tests drive depth transitions without standing up a real
// Inspector. Public-API behavior of Monitor (Run, Depth) is still
// covered via the QueueInspector interface using a stub below — we
// don't reach into private state for those assertions.

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hibiken/asynq"
)

// fakeInspector is a controllable QueueInspector: callers set the
// Pending count per queue, optionally inject an error, and the
// Monitor's poll loop will read those values. Safe for concurrent
// use; the Monitor's goroutine reads while the test goroutine writes.
type fakeInspector struct {
	mu      sync.Mutex
	pending map[string]int
	err     error
	calls   atomic.Int64
}

func newFakeInspector() *fakeInspector {
	return &fakeInspector{pending: map[string]int{}}
}

func (f *fakeInspector) GetQueueInfo(queue string) (*asynq.QueueInfo, error) {
	f.calls.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return &asynq.QueueInfo{Queue: queue, Pending: f.pending[queue]}, nil
}

func (f *fakeInspector) set(queue string, depth int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pending[queue] = depth
}

func (f *fakeInspector) setErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

// TestMonitorDepthInitial verifies Depth returns 0 for queues whether
// or not they're configured, before the first sample. The fail-open
// contract: zero depth means "every priority passes" at the Gate.
func TestMonitorDepthInitial(t *testing.T) {
	m := &Monitor{
		Inspector: newFakeInspector(),
		Thresholds: []Threshold{
			{Queue: "webhook", SoftLimit: 10, HardLimit: 20},
		},
	}
	if got := m.Depth("webhook"); got != 0 {
		t.Errorf("initial Depth(webhook) = %d, want 0", got)
	}
	if got := m.Depth("unknown"); got != 0 {
		t.Errorf("initial Depth(unknown) = %d, want 0", got)
	}
}

// TestMonitorRunPropagatesDepth verifies the goroutine-driven path:
// Run polls the Inspector, depth changes propagate into Depth. We
// busy-wait with a timeout rather than rely on a fixed sleep so the
// test stays robust under heavy CI load.
func TestMonitorRunPropagatesDepth(t *testing.T) {
	fi := newFakeInspector()
	fi.set("webhook", 7)
	m := &Monitor{
		Inspector: fi,
		Thresholds: []Threshold{
			{Queue: "webhook", SoftLimit: 10, HardLimit: 20},
		},
		Interval: 10 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		_ = m.Run(ctx)
		close(done)
	}()

	// Sample once happens before the first tick, so 7 should appear
	// almost immediately. Poll with a short timeout to absorb CI jitter.
	if !waitFor(t, 2*time.Second, func() bool { return m.Depth("webhook") == 7 }) {
		t.Fatalf("Depth(webhook) never reached 7 (got %d)", m.Depth("webhook"))
	}

	// Bump the depth and verify the next tick picks it up.
	fi.set("webhook", 42)
	if !waitFor(t, 2*time.Second, func() bool { return m.Depth("webhook") == 42 }) {
		t.Fatalf("Depth(webhook) never reached 42 (got %d)", m.Depth("webhook"))
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after ctx cancel")
	}
}

// TestMonitorRunRetainsLastOnError verifies the resilience contract:
// when GetQueueInfo errors, Depth keeps the previously observed
// value rather than flipping to 0 (which would open the gate during
// a transient Redis blip — exactly the wrong behavior).
func TestMonitorRunRetainsLastOnError(t *testing.T) {
	fi := newFakeInspector()
	fi.set("webhook", 30)
	m := &Monitor{
		Inspector: fi,
		Thresholds: []Threshold{
			{Queue: "webhook", SoftLimit: 10, HardLimit: 20},
		},
		Interval: 10 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = m.Run(ctx) }()

	if !waitFor(t, 2*time.Second, func() bool { return m.Depth("webhook") == 30 }) {
		t.Fatalf("Depth(webhook) never reached 30; got %d", m.Depth("webhook"))
	}

	fi.setErr(errors.New("simulated redis outage"))
	// Sleep a few intervals to let the inspector error happen; depth
	// must remain at 30.
	time.Sleep(100 * time.Millisecond)
	if got := m.Depth("webhook"); got != 30 {
		t.Errorf("Depth after errors: got %d, want 30 (last good value)", got)
	}
}

// TestMonitorRunWithoutInspector verifies that a Monitor with a nil
// Inspector doesn't crash; Run is a no-op loop until ctx is canceled.
// Useful for test wiring that wants the Monitor's API without the
// goroutine.
func TestMonitorRunNoInspector(t *testing.T) {
	m := &Monitor{
		Thresholds: []Threshold{
			{Queue: "webhook", SoftLimit: 10, HardLimit: 20},
		},
		Interval: 5 * time.Millisecond,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := m.Run(ctx); err != nil {
		t.Fatalf("Run with nil inspector: %v", err)
	}
}

// TestMonitorSetDepth exercises the test-only helper exposed for the
// Gate + Middleware tests. Since the helper writes to the underlying
// atomic without mutating the map, it must be race-clean with
// concurrent Depth readers.
func TestMonitorSetDepth(t *testing.T) {
	m := &Monitor{
		Thresholds: []Threshold{
			{Queue: "webhook", SoftLimit: 10, HardLimit: 20},
		},
	}
	m.setDepth("webhook", 17)
	if got := m.Depth("webhook"); got != 17 {
		t.Errorf("Depth after setDepth = %d, want 17", got)
	}
	// Unknown queues are silently ignored by setDepth (the depths map
	// is built once at init from Thresholds), which keeps the
	// concurrent-Depth-read fast path race-free.
	m.setDepth("unconfigured", 99)
	if got := m.Depth("unconfigured"); got != 0 {
		t.Errorf("Depth(unconfigured) after setDepth = %d, want 0", got)
	}
}

// TestMonitorConcurrentDepthReads is a race-detector flush: many
// goroutines call Depth while a writer flips setDepth. The test
// passes if -race reports no warnings.
func TestMonitorConcurrentDepthReads(t *testing.T) {
	m := &Monitor{
		Thresholds: []Threshold{
			{Queue: "q", SoftLimit: 10, HardLimit: 20},
		},
	}
	m.setDepth("q", 0)

	const goroutines = 8
	const iters = 5000
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iters; j++ {
				_ = m.Depth("q")
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < iters; j++ {
			m.setDepth("q", j%30)
		}
	}()
	wg.Wait()
}

// waitFor polls cond every 5ms until it returns true or the timeout
// elapses. Returns true on success. Used in tests that depend on the
// Monitor goroutine catching up; preferred over a fixed time.Sleep
// because it absorbs CI jitter without flapping.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}
