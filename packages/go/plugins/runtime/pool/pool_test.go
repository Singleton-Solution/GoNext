package pool

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tetratelabs/wazero/api"

	"github.com/Singleton-Solution/GoNext/packages/go/plugins/runtime"
)

// newTestRuntime builds a fresh runtime.Runtime that discards log
// output. Every pool test gets its own so test isolation is total
// (wazero per-process state is per-runtime, so two runtimes do not
// share module-name namespaces).
func newTestRuntime(t *testing.T) *runtime.Runtime {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	rt, err := runtime.New(context.Background(), runtime.WithLogger(logger))
	if err != nil {
		t.Fatalf("runtime.New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background()) })
	return rt
}

// newTestPool constructs a Pool with sensible test defaults: small
// max, short reap interval. Caller mutates the returned config-clone
// to tweak knobs.
func newTestPool(t *testing.T, override func(*Config)) *Pool {
	t.Helper()
	cfg := Config{
		Runtime:      newTestRuntime(t),
		WasmBytes:    wasmAdd,
		PluginName:   "test",
		MinInstances: 1,
		MaxInstances: 4,
		MaxIdleTime:  10 * time.Millisecond,
		ReapInterval: 5 * time.Millisecond,
	}
	if override != nil {
		override(&cfg)
	}
	p, err := NewPool(cfg)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = p.Close(ctx)
	})
	return p
}

// callAdd invokes the "add" export on the module and returns the
// summed result. Used to confirm a checked-out lease actually wraps a
// working module.
func callAdd(t *testing.T, mod *runtime.Module, a, b int32) int32 {
	t.Helper()
	if mod == nil {
		t.Fatalf("callAdd: nil module")
	}
	res, err := mod.Call(context.Background(), "add", api.EncodeI32(a), api.EncodeI32(b))
	if err != nil {
		t.Fatalf("module.Call(add): %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("expected 1 result, got %d", len(res))
	}
	return api.DecodeI32(res[0])
}

// TestCheckoutReturn_Cycle is the canary: a single checkout, one
// guest call, one return, and the pool ends up in its rest state.
func TestCheckoutReturn_Cycle(t *testing.T) {
	p := newTestPool(t, func(c *Config) {
		c.MinInstances = 1
		c.MaxInstances = 2
		c.MaxIdleTime = time.Hour // disable reaper churn for this test
	})

	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	l, err := p.Checkout(context.Background())
	if err != nil {
		t.Fatalf("Checkout: %v", err)
	}
	if got := callAdd(t, l.Module(), 2, 3); got != 5 {
		t.Errorf("add(2,3) = %d, want 5", got)
	}
	if err := l.Return(); err != nil {
		t.Errorf("Return: %v", err)
	}
	// Idempotent
	if err := l.Return(); err != nil {
		t.Errorf("second Return should be no-op, got %v", err)
	}

	stats := p.Stats()
	if stats.Live != 1 {
		t.Errorf("Live = %d, want 1", stats.Live)
	}
	if stats.Idle != 1 {
		t.Errorf("Idle = %d, want 1", stats.Idle)
	}
	if stats.Outstanding != 0 {
		t.Errorf("Outstanding = %d, want 0", stats.Outstanding)
	}
}

// TestCheckout_ConcurrentBlocksAtMax confirms that the (Max+1)th
// concurrent checkout blocks until a Return frees a slot.
func TestCheckout_ConcurrentBlocksAtMax(t *testing.T) {
	const max = 3
	p := newTestPool(t, func(c *Config) {
		c.MinInstances = 1
		c.MaxInstances = max
		c.MaxIdleTime = time.Hour
	})

	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Saturate.
	leases := make([]*Lease, max)
	for i := 0; i < max; i++ {
		l, err := p.Checkout(context.Background())
		if err != nil {
			t.Fatalf("Checkout %d: %v", i, err)
		}
		leases[i] = l
	}
	if got := p.Stats().Outstanding; got != int64(max) {
		t.Fatalf("Outstanding after saturation = %d, want %d", got, max)
	}

	// The (max+1)th blocks. Launch in a goroutine and confirm it
	// hasn't returned after a short wait.
	got := make(chan *Lease, 1)
	errCh := make(chan error, 1)
	go func() {
		l, err := p.Checkout(context.Background())
		if err != nil {
			errCh <- err
			return
		}
		got <- l
	}()
	select {
	case <-got:
		t.Fatal("Checkout returned a lease while pool was saturated")
	case err := <-errCh:
		t.Fatalf("Checkout errored while pool was saturated: %v", err)
	case <-time.After(50 * time.Millisecond):
		// expected — still blocked
	}

	// Free a slot and expect the blocked Checkout to unblock.
	if err := leases[0].Return(); err != nil {
		t.Fatalf("Return: %v", err)
	}
	select {
	case l := <-got:
		_ = l.Return()
	case err := <-errCh:
		t.Fatalf("Checkout failed after Return freed a slot: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("Checkout did not unblock after Return")
	}

	// Drain the rest.
	for i := 1; i < max; i++ {
		_ = leases[i].Return()
	}
}

// TestCheckout_CtxTimeoutNoLeak confirms that a ctx-cancelled
// Checkout does NOT leak (no phantom InUse, no orphan instance).
func TestCheckout_CtxTimeoutNoLeak(t *testing.T) {
	const max = 2
	p := newTestPool(t, func(c *Config) {
		c.MinInstances = 1
		c.MaxInstances = max
		c.MaxIdleTime = time.Hour
	})
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Saturate.
	leases := make([]*Lease, max)
	for i := 0; i < max; i++ {
		l, err := p.Checkout(context.Background())
		if err != nil {
			t.Fatalf("Checkout %d: %v", i, err)
		}
		leases[i] = l
	}

	// A ctx with a tight deadline should fail with a wrapped
	// DeadlineExceeded.
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()

	start := time.Now()
	l, err := p.Checkout(ctx)
	dur := time.Since(start)
	if err == nil {
		t.Fatalf("Checkout: expected error, got lease %v", l)
	}
	if !errors.Is(err, ErrCheckoutTimeout) {
		t.Errorf("error is not ErrCheckoutTimeout: %v", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error does not wrap DeadlineExceeded: %v", err)
	}
	// Should have unblocked roughly when the deadline fired, not
	// hung for the full default timeout.
	if dur > 500*time.Millisecond {
		t.Errorf("Checkout took %v after 25 ms deadline — bad", dur)
	}

	// No leak: Outstanding equals the leases we still hold.
	if got := p.Stats().Outstanding; got != int64(max) {
		t.Errorf("Outstanding after failed Checkout = %d, want %d", got, max)
	}

	// Drain.
	for _, l := range leases {
		_ = l.Return()
	}
}

// TestMaxUsesPerInstance_Rotation confirms that an instance is
// recycled after exactly MaxUsesPerInstance checkouts.
func TestMaxUsesPerInstance_Rotation(t *testing.T) {
	p := newTestPool(t, func(c *Config) {
		c.MinInstances = 1
		c.MaxInstances = 1
		c.MaxIdleTime = time.Hour
		c.MaxUsesPerInstance = 3
	})
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Capture the wazero module pointer for each checkout. After
	// MaxUsesPerInstance, the next checkout must return a NEW
	// instance (different pointer).
	first := mustModulePtr(t, p)
	second := mustModulePtr(t, p)
	third := mustModulePtr(t, p)
	if first != second || second != third {
		t.Fatalf("module pointer changed before cap exhausted: %p, %p, %p", first, second, third)
	}

	// The 4th checkout should land on a new instance (the 3rd
	// Return triggered recycle).
	fourth := mustModulePtr(t, p)
	if fourth == first {
		t.Errorf("expected a new module after MaxUsesPerInstance, got the same pointer %p", fourth)
	}
}

// mustModulePtr does Checkout → record module pointer → Return.
func mustModulePtr(t *testing.T, p *Pool) *runtime.Module {
	t.Helper()
	l, err := p.Checkout(context.Background())
	if err != nil {
		t.Fatalf("Checkout: %v", err)
	}
	m := l.Module()
	if err := l.Return(); err != nil {
		t.Fatalf("Return: %v", err)
	}
	return m
}

// TestMaxIdleTime_ReapAndRefill confirms the reaper closes idle
// instances and the refill brings the pool back to MinInstances.
func TestMaxIdleTime_ReapAndRefill(t *testing.T) {
	p := newTestPool(t, func(c *Config) {
		c.MinInstances = 1
		c.MaxInstances = 4
		c.MaxIdleTime = 20 * time.Millisecond
		c.ReapInterval = 5 * time.Millisecond
	})
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Fill to 4, return all → 4 idle.
	leases := make([]*Lease, 4)
	for i := range leases {
		l, err := p.Checkout(context.Background())
		if err != nil {
			t.Fatalf("Checkout: %v", err)
		}
		leases[i] = l
	}
	for _, l := range leases {
		_ = l.Return()
	}
	if got := p.Stats().Live; got != 4 {
		t.Fatalf("Live before reap = %d, want 4", got)
	}

	// Wait for the reaper to evict down to MinInstances. The reap
	// interval is 5 ms and the idle threshold is 20 ms, so within
	// ~50 ms we should be at the floor.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s := p.Stats()
		if s.Live == 1 && s.Idle == 1 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("reaper did not evict to MinInstances: stats=%+v", p.Stats())
}

// TestTrappedInstance_RecycledOnReturn confirms that an instance
// marked unusable (via Lease.MarkUnusable) is closed on Return, not
// returned to idle.
func TestTrappedInstance_RecycledOnReturn(t *testing.T) {
	p := newTestPool(t, func(c *Config) {
		c.MinInstances = 1
		c.MaxInstances = 1
		c.MaxIdleTime = time.Hour
	})
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	first := mustModulePtr(t, p)

	l, err := p.Checkout(context.Background())
	if err != nil {
		t.Fatalf("Checkout: %v", err)
	}
	beforeReturn := l.Module()
	if beforeReturn != first {
		t.Fatalf("unexpected module rotation pre-trap")
	}
	l.MarkUnusable()
	if err := l.Return(); err != nil {
		t.Fatalf("Return: %v", err)
	}

	// Next checkout must yield a NEW instance.
	next := mustModulePtr(t, p)
	if next == first {
		t.Errorf("trapped instance was reused: %p", next)
	}
}

// TestClose_DrainsLeases_NoLeaks runs Close while leases are
// outstanding and confirms the pool waits for Returns, closes idle
// cleanly, and ends up with zero live.
func TestClose_DrainsLeases_NoLeaks(t *testing.T) {
	p := newTestPool(t, func(c *Config) {
		c.MinInstances = 1
		c.MaxInstances = 4
		c.MaxIdleTime = time.Hour
	})
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Grab two leases, hold them, kick off Close in another
	// goroutine, then Return.
	l1, err := p.Checkout(context.Background())
	if err != nil {
		t.Fatalf("Checkout: %v", err)
	}
	l2, err := p.Checkout(context.Background())
	if err != nil {
		t.Fatalf("Checkout: %v", err)
	}

	closeDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		closeDone <- p.Close(ctx)
	}()

	// Close should NOT return until both leases are returned.
	select {
	case err := <-closeDone:
		t.Fatalf("Close returned prematurely (err=%v) — leases still outstanding", err)
	case <-time.After(30 * time.Millisecond):
	}

	// Subsequent Checkout must fail-fast with ErrPoolClosed.
	if _, err := p.Checkout(context.Background()); !errors.Is(err, ErrPoolClosed) {
		t.Errorf("Checkout after Close: want ErrPoolClosed, got %v", err)
	}

	_ = l1.Return()
	_ = l2.Return()

	// Now Close should finish.
	select {
	case err := <-closeDone:
		if err != nil {
			t.Errorf("Close: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not finish after all leases returned")
	}

	if got := p.Stats().Live; got != 0 {
		t.Errorf("Live after Close = %d, want 0", got)
	}
}

// TestClose_TimeoutWhileLeasesHeld confirms that Close honors its
// ctx deadline when leases are never returned.
func TestClose_TimeoutWhileLeasesHeld(t *testing.T) {
	p := newTestPool(t, func(c *Config) {
		c.MinInstances = 1
		c.MaxInstances = 2
		c.MaxIdleTime = time.Hour
	})
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	l, err := p.Checkout(context.Background())
	if err != nil {
		t.Fatalf("Checkout: %v", err)
	}
	defer l.Return()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := p.Close(ctx); err == nil {
		t.Fatalf("Close: expected ctx error, got nil")
	} else if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Close error does not wrap DeadlineExceeded: %v", err)
	}
}

// TestRace_ManyGoroutinesManyCheckouts is the stress test the issue
// calls for: 100 goroutines × 1000 checkouts each, run under -race.
// We dial the counts down a little from the spec when -short is
// active so unit-test runs stay snappy.
func TestRace_ManyGoroutinesManyCheckouts(t *testing.T) {
	goroutines := 100
	checkoutsPerG := 1000
	if testing.Short() {
		goroutines = 20
		checkoutsPerG = 100
	}

	p := newTestPool(t, func(c *Config) {
		c.MinInstances = 1
		c.MaxInstances = 8
		c.MaxIdleTime = 50 * time.Millisecond
		c.MaxUsesPerInstance = 50
		c.ReapInterval = 1 * time.Millisecond
	})
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	var (
		ok      atomic.Int64
		fail    atomic.Int64
		wg      sync.WaitGroup
	)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < checkoutsPerG; j++ {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				l, err := p.Checkout(ctx)
				cancel()
				if err != nil {
					fail.Add(1)
					continue
				}
				// Do a minimal guest call so we exercise the
				// module, not just the bookkeeping.
				if mod := l.Module(); mod != nil {
					_, _ = mod.Call(context.Background(), "add",
						api.EncodeI32(int32(j)), api.EncodeI32(1))
				}
				_ = l.Return()
				ok.Add(1)
			}
		}()
	}
	wg.Wait()

	want := int64(goroutines * checkoutsPerG)
	if got := ok.Load(); got != want {
		t.Errorf("successful checkouts = %d, want %d (failures=%d)", got, want, fail.Load())
	}

	// After all goroutines finished, the pool should be quiescent.
	stats := p.Stats()
	if stats.Outstanding != 0 {
		t.Errorf("Outstanding after race test = %d, want 0", stats.Outstanding)
	}
}

// TestNewPool_InvalidConfig exercises the validation guards.
func TestNewPool_InvalidConfig(t *testing.T) {
	rt := newTestRuntime(t)

	cases := []struct {
		name string
		cfg  Config
	}{
		{"no runtime", Config{WasmBytes: wasmAdd}},
		{"no bytes", Config{Runtime: rt}},
		{"min > max", Config{Runtime: rt, WasmBytes: wasmAdd, MinInstances: 5, MaxInstances: 2}},
		{"negative min", Config{Runtime: rt, WasmBytes: wasmAdd, MinInstances: -1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewPool(tc.cfg); !errors.Is(err, ErrInvalidConfig) {
				t.Errorf("expected ErrInvalidConfig, got %v", err)
			}
		})
	}
}

// TestCheckout_AfterClose returns ErrPoolClosed fast (no waiting).
func TestCheckout_AfterClose(t *testing.T) {
	p := newTestPool(t, nil)
	if err := p.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := p.Checkout(context.Background()); !errors.Is(err, ErrPoolClosed) {
		t.Errorf("Checkout after Close: want ErrPoolClosed, got %v", err)
	}
}

// TestLease_ModuleAfterReturn returns nil.
func TestLease_ModuleAfterReturn(t *testing.T) {
	p := newTestPool(t, nil)
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	l, err := p.Checkout(context.Background())
	if err != nil {
		t.Fatalf("Checkout: %v", err)
	}
	_ = l.Return()
	if mod := l.Module(); mod != nil {
		t.Errorf("Module after Return = %p, want nil", mod)
	}
}

// TestMetrics_PopulatedOnCheckoutAndRecycle confirms the metric
// counters move in the right direction. We don't assert exact wait
// timings — just that the counters are non-zero where they should
// be.
func TestMetrics_PopulatedOnCheckoutAndRecycle(t *testing.T) {
	m := NewMetrics(MetricsConfig{
		// nil Registerer → metrics created but not registered.
		// Fine for assertions that read .Inc()ed counter values
		// via the prom util collection helpers.
	})

	p := newTestPool(t, func(c *Config) {
		c.MinInstances = 1
		c.MaxInstances = 2
		c.MaxIdleTime = time.Hour
		c.MaxUsesPerInstance = 2
		c.Metrics = m
	})
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Three checkouts: third triggers a recycle (max=2).
	for i := 0; i < 3; i++ {
		l, err := p.Checkout(context.Background())
		if err != nil {
			t.Fatalf("Checkout %d: %v", i, err)
		}
		_ = l.Return()
	}

	if got := readCounter(t, m.CheckoutTotal); got != 3 {
		t.Errorf("checkout_total = %d, want 3", got)
	}
	if got := readCounterVec(t, m.RecycleTotal, RecycleReasonMaxUses); got < 1 {
		t.Errorf("recycle_total{reason=max_uses} = %d, want >=1", got)
	}
}
