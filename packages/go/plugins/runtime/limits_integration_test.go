package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/plugins/runtime/limits"
)

// TestLimitsTightLoopCompiles is the canary for wasmTightLoop. If the
// hand-authored bytes drift, all the deadline tests below fail with
// cryptic "function not found" errors; a dedicated load-only check
// keeps that attribution clean.
func TestLimitsTightLoopCompiles(t *testing.T) {
	rt, _ := newTestRuntime(t)
	mod, err := rt.LoadModule(context.Background(), "tightloop", wasmTightLoop)
	if err != nil {
		t.Fatalf("LoadModule tightloop: %v", err)
	}
	if mod.WazeroModule().ExportedFunction("spin") == nil {
		t.Error("spin export missing")
	}
	_ = mod.Close(context.Background())
}

// TestRuntime_WithLimits_SoftCPUTimeout: a tight-loop guest is killed
// by the soft deadline. With WithCloseOnContextDone(true) on the
// wazero runtime, the ctx cancel surfaces as a trap and Module.Call
// returns promptly.
func TestRuntime_WithLimits_SoftCPUTimeout(t *testing.T) {
	rt, _ := newTestRuntime(t, WithLimits(limits.Limits{
		CPUTimeoutSoft: 100 * time.Millisecond,
		CPUTimeoutHard: 2 * time.Second,
	}))

	mod, err := rt.LoadModule(context.Background(), "tightloop", wasmTightLoop)
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}
	defer mod.Close(context.Background())

	start := time.Now()
	_, err = mod.Call(context.Background(), "spin")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Call returned nil err on infinite-loop guest; expected timeout trap")
	}
	if elapsed > 1500*time.Millisecond {
		t.Errorf("Call took %v; soft deadline (100ms) didn't fire promptly", elapsed)
	}
	if elapsed < 50*time.Millisecond {
		t.Errorf("Call returned at %v, expected at least near soft deadline (100ms)", elapsed)
	}

	// The error should be a trap.
	var trap *TrapError
	if !errors.As(err, &trap) {
		t.Errorf("Call err = %T(%v); want *TrapError", err, err)
	}
}

// TestRuntime_WithLimits_HardCPUTimeout: when soft is 0 and hard is
// set, the hard deadline is the sole CPU budget. The tight-loop guest
// still terminates within the configured envelope.
func TestRuntime_WithLimits_HardCPUTimeout(t *testing.T) {
	rt, _ := newTestRuntime(t, WithLimits(limits.Limits{
		CPUTimeoutSoft: 0,
		CPUTimeoutHard: 150 * time.Millisecond,
	}))

	mod, err := rt.LoadModule(context.Background(), "tightloop", wasmTightLoop)
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}
	defer mod.Close(context.Background())

	start := time.Now()
	_, err = mod.Call(context.Background(), "spin")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("Call returned nil err; expected hard-deadline trap")
	}
	if elapsed > 1500*time.Millisecond {
		t.Errorf("Call took %v; hard deadline (150ms) didn't fire promptly", elapsed)
	}
}

// TestRuntime_WithLimits_NoTimeoutNoHang: a runtime constructed with
// zero CPU limits + no limits at all still completes a fast call. The
// purpose is to confirm the limits path doesn't accidentally add a
// deadline where the user wanted none.
func TestRuntime_WithLimits_NoTimeoutFastCall(t *testing.T) {
	rt, _ := newTestRuntime(t, WithLimits(limits.Limits{}))
	mod, err := rt.LoadModule(context.Background(), "add", wasmAdd)
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}
	defer mod.Close(context.Background())

	results, err := mod.Call(context.Background(), "add", 5, 7)
	if err != nil {
		t.Fatalf("add Call: %v", err)
	}
	if got := int32(results[0]); got != 12 {
		t.Errorf("add(5,7) = %d, want 12", got)
	}
}

// TestRuntime_WithLimits_MemoryPagesPropagated: WithLimits + a memory
// cap below the bigmem fixture's request (1024 pages) must reject
// loading the fixture, demonstrating the limit reached the wazero
// runtime config.
func TestRuntime_WithLimits_MemoryPagesPropagated(t *testing.T) {
	rt, _ := newTestRuntime(t, WithLimits(limits.Limits{
		MemoryPages: 64, // 4 MiB, below bigmem's 1024 pages
	}))
	_, err := rt.LoadModule(context.Background(), "bigmem", wasmBigMem)
	if err == nil {
		t.Fatal("LoadModule bigmem: nil err; expected memory-cap rejection")
	}
	var compileErr *CompileError
	if !errors.As(err, &compileErr) {
		// LoadModule wraps instantiation failures in a plain error too;
		// either is acceptable as long as the wazero error surfaces.
		if !strings.Contains(err.Error(), "memory") &&
			!strings.Contains(err.Error(), "pages") &&
			!strings.Contains(err.Error(), "minimum size") {
			t.Errorf("LoadModule err = %v; want it to mention the memory limit", err)
		}
	}
}

// TestRuntime_WithLimits_InvalidRejected: New with a Limits where
// hard < soft must return an error rather than silently truncating.
func TestRuntime_WithLimits_InvalidRejected(t *testing.T) {
	_, err := New(context.Background(), WithLimits(limits.Limits{
		CPUTimeoutSoft: 2 * time.Second,
		CPUTimeoutHard: 1 * time.Second,
	}))
	if err == nil {
		t.Fatal("New with invalid limits = nil err; want validation error")
	}
}

// TestRuntime_Enforcer_AcquirePool simulates a pool acquiring N
// instances. It exercises the runtime's Enforcer() accessor and the
// per-plugin instance counter, which is the surface the pool (#9)
// will consume.
func TestRuntime_Enforcer_AcquirePool(t *testing.T) {
	rt, _ := newTestRuntime(t, WithLimits(limits.Limits{
		MaxInstancesPerPlugin: 2,
	}))
	enf := rt.Enforcer()
	if enf == nil {
		t.Fatal("Enforcer() returned nil")
	}

	r1, err := enf.Acquire("plug")
	if err != nil {
		t.Fatalf("Acquire #1: %v", err)
	}
	r2, err := enf.Acquire("plug")
	if err != nil {
		t.Fatalf("Acquire #2: %v", err)
	}
	if _, err := enf.Acquire("plug"); !errors.Is(err, limits.ErrInstanceLimitReached) {
		t.Errorf("Acquire #3 err = %v, want ErrInstanceLimitReached", err)
	}
	r1()
	if _, err := enf.Acquire("plug"); err != nil {
		t.Errorf("Acquire after release = %v, want nil", err)
	}
	r2()
}

// TestRuntime_DefaultLimits_NoHang asserts the default envelope keeps a
// runaway plugin from hanging the test suite for more than 5s. We arm
// a parent ctx with a 7s ceiling; if the default hard deadline is set
// correctly, Call returns well within it.
func TestRuntime_DefaultLimits_NoHang(t *testing.T) {
	rt, _ := newTestRuntime(t) // no options — defaults apply
	mod, err := rt.LoadModule(context.Background(), "tightloop", wasmTightLoop)
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}
	defer mod.Close(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
	defer cancel()

	start := time.Now()
	_, err = mod.Call(ctx, "spin")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("spin returned nil err under default limits")
	}
	// Default hard is 5s. We expect Call to return well within 7s.
	if elapsed > 6500*time.Millisecond {
		t.Errorf("Call took %v under default limits; expected ~%v",
			elapsed, limits.DefaultCPUTimeoutHard)
	}
}

// TestRuntime_WithLimits_AfterMemoryOption: order matters per the
// option docs — WithMemoryLimitPages applied AFTER WithLimits wins.
func TestRuntime_WithLimits_AfterMemoryOption(t *testing.T) {
	rt, err := New(context.Background(),
		WithLimits(limits.Limits{MemoryPages: 256}),
		WithMemoryLimitPages(64),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background()) })

	if _, err := rt.LoadModule(context.Background(), "bigmem128", wasmBigMem); err == nil {
		t.Error("bigmem load succeeded; WithMemoryLimitPages(64) didn't take effect")
	}
}
