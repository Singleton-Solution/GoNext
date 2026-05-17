package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// quietLogger returns a slog.Logger that discards everything.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// newTestCoalescer builds a Coalescer with a MemoryCounter and a quiet
// logger so test output stays clean.
func newTestCoalescer(t *testing.T) (*Coalescer, *MemoryCounter) {
	t.Helper()
	mc := NewMemoryCounter()
	c := NewCoalescer(CoalescerOptions{
		Counter: mc,
		Logger:  quietLogger(),
	})
	return c, mc
}

// TestCoalescer_SingleFlight_ManyConcurrentSameKey is the headline test.
// 100 goroutines call Get on the same key while a slow generate is in
// flight. The contract: generate runs exactly once, all 100 receive
// the same bytes, exactly one is a leader (shared=false), and the
// other 99 are followers (shared=true). The leader counter increments
// once; the coalesce counter increments 99 times.
func TestCoalescer_SingleFlight_ManyConcurrentSameKey(t *testing.T) {
	c, mc := newTestCoalescer(t)

	const N = 100
	const key = "media/abc/w800h600.webp"
	want := []byte("rendered-bytes-payload")

	// gateOpen is unsignaled at start; generate blocks on it so all 100
	// goroutines have time to pile up on the same key. Once we have
	// confidence they're all in flight (waitForInflight below), we close
	// the gate and let generate complete.
	gateOpen := make(chan struct{})
	var generateCount atomic.Int64

	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(N)

	type result struct {
		bytes  []byte
		shared bool
		err    error
	}
	results := make([]result, N)

	for i := 0; i < N; i++ {
		i := i
		go func() {
			defer wg.Done()
			<-start // synchronize the burst
			b, shared, err := c.Get(context.Background(), key, func() ([]byte, error) {
				generateCount.Add(1)
				<-gateOpen
				return want, nil
			})
			results[i] = result{bytes: b, shared: shared, err: err}
		}()
	}

	close(start)

	// Wait for at least N callers to be either inside generate or
	// blocked on Wait. We can't directly observe singleflight's
	// internal waiter count, but we can poll Stats().InFlight to
	// confirm the leader has entered generate; from there, the
	// scheduler has fair odds the other 99 piled on, and a short
	// grace period (5ms) shores up the rest. The test must finish
	// even if it doesn't strictly observe 100-way pile-up, but the
	// invariant we care about (generate called exactly once) holds
	// regardless.
	waitForInflight(t, c, 1, time.Second)
	time.Sleep(5 * time.Millisecond)

	close(gateOpen)
	wg.Wait()

	if got := generateCount.Load(); got != 1 {
		t.Fatalf("generate ran %d times, want exactly 1", got)
	}

	leaders, followers := 0, 0
	for i, r := range results {
		if r.err != nil {
			t.Errorf("caller %d: unexpected err %v", i, r.err)
		}
		if !bytes.Equal(r.bytes, want) {
			t.Errorf("caller %d: bytes mismatch", i)
		}
		if r.shared {
			followers++
		} else {
			leaders++
		}
	}
	if leaders != 1 {
		t.Errorf("leaders = %d, want 1", leaders)
	}
	if followers != N-1 {
		t.Errorf("followers = %d, want %d", followers, N-1)
	}

	// Counter assertions.
	if got := mc.Get(MetricGenerateTotal); got != 1 {
		t.Errorf("%s = %d, want 1", MetricGenerateTotal, got)
	}
	if got := mc.Get(MetricCoalesceTotal); got != int64(N-1) {
		t.Errorf("%s = %d, want %d", MetricCoalesceTotal, got, N-1)
	}

	// Stats counters.
	s := c.Stats()
	if s.TotalGenerated != 1 {
		t.Errorf("Stats.TotalGenerated = %d, want 1", s.TotalGenerated)
	}
	if s.TotalCoalesced != int64(N-1) {
		t.Errorf("Stats.TotalCoalesced = %d, want %d", s.TotalCoalesced, N-1)
	}
	if s.InFlight != 0 {
		t.Errorf("Stats.InFlight = %d, want 0 after wg.Wait", s.InFlight)
	}
}

// TestCoalescer_DistinctKeysGenerateIndependently asserts that two
// different keys do not coalesce — each is its own singleflight entry
// and runs its own generate.
func TestCoalescer_DistinctKeysGenerateIndependently(t *testing.T) {
	c, mc := newTestCoalescer(t)
	var aCount, bCount atomic.Int64

	bA, sharedA, errA := c.Get(context.Background(), "key-a", func() ([]byte, error) {
		aCount.Add(1)
		return []byte("A"), nil
	})
	if errA != nil {
		t.Fatalf("Get(A): %v", errA)
	}
	if string(bA) != "A" || sharedA {
		t.Fatalf("Get(A) = (%q, %v), want (%q, false)", bA, sharedA, "A")
	}

	bB, sharedB, errB := c.Get(context.Background(), "key-b", func() ([]byte, error) {
		bCount.Add(1)
		return []byte("B"), nil
	})
	if errB != nil {
		t.Fatalf("Get(B): %v", errB)
	}
	if string(bB) != "B" || sharedB {
		t.Fatalf("Get(B) = (%q, %v), want (%q, false)", bB, sharedB, "B")
	}

	if aCount.Load() != 1 {
		t.Errorf("generate(A) ran %d times, want 1", aCount.Load())
	}
	if bCount.Load() != 1 {
		t.Errorf("generate(B) ran %d times, want 1", bCount.Load())
	}
	if got := mc.Get(MetricGenerateTotal); got != 2 {
		t.Errorf("%s = %d, want 2", MetricGenerateTotal, got)
	}
	if got := mc.Get(MetricCoalesceTotal); got != 0 {
		t.Errorf("%s = %d, want 0", MetricCoalesceTotal, got)
	}
}

// TestCoalescer_GenerateError_AllWaitersReceiveIt asserts that when
// generate returns an error, every concurrent caller observes the
// same error.
func TestCoalescer_GenerateError_AllWaitersReceiveIt(t *testing.T) {
	c, mc := newTestCoalescer(t)
	want := errors.New("libvips: decode failed")

	const N = 20
	gate := make(chan struct{})
	var genCount atomic.Int64

	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(N)
	errs := make([]error, N)
	shareds := make([]bool, N)

	for i := 0; i < N; i++ {
		i := i
		go func() {
			defer wg.Done()
			<-start
			_, shared, err := c.Get(context.Background(), "broken", func() ([]byte, error) {
				genCount.Add(1)
				<-gate
				return nil, want
			})
			errs[i] = err
			shareds[i] = shared
		}()
	}
	close(start)
	waitForInflight(t, c, 1, time.Second)
	time.Sleep(5 * time.Millisecond)
	close(gate)
	wg.Wait()

	if genCount.Load() != 1 {
		t.Fatalf("generate ran %d times, want 1", genCount.Load())
	}
	for i, err := range errs {
		if !errors.Is(err, want) {
			t.Errorf("caller %d: err = %v, want errors.Is(%v) = true", i, err, want)
		}
	}
	// Leader vs follower bookkeeping is unaffected by error: exactly
	// one caller should still be the leader.
	leaders, followers := 0, 0
	for _, s := range shareds {
		if s {
			followers++
		} else {
			leaders++
		}
	}
	if leaders != 1 || followers != N-1 {
		t.Errorf("leader/follower split = (%d, %d), want (1, %d)", leaders, followers, N-1)
	}
	if got := mc.Get(MetricGenerateTotal); got != 1 {
		t.Errorf("%s = %d, want 1", MetricGenerateTotal, got)
	}
	if got := mc.Get(MetricCoalesceTotal); got != int64(N-1) {
		t.Errorf("%s = %d, want %d", MetricCoalesceTotal, got, N-1)
	}
}

// TestCoalescer_RetriesAfterGenerateError exercises the singleflight
// contract that failed generations are NOT cached — a fresh Get after
// the failure starts a brand-new generation.
func TestCoalescer_RetriesAfterGenerateError(t *testing.T) {
	c, _ := newTestCoalescer(t)
	var count atomic.Int64
	gen := func() ([]byte, error) {
		n := count.Add(1)
		if n == 1 {
			return nil, errors.New("transient")
		}
		return []byte("ok"), nil
	}
	if _, _, err := c.Get(context.Background(), "k", gen); err == nil {
		t.Fatal("first call should have errored")
	}
	b, shared, err := c.Get(context.Background(), "k", gen)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if shared {
		t.Errorf("second call shared = true, want false (fresh leader after prior failure)")
	}
	if string(b) != "ok" {
		t.Errorf("second call bytes = %q, want %q", b, "ok")
	}
	if count.Load() != 2 {
		t.Errorf("generate ran %d times, want 2", count.Load())
	}
}

// TestCoalescer_CtxCancellation_OneCallerDoesNotAffectSiblings asserts
// that when one caller's ctx is canceled, the in-flight generation
// continues to completion and other callers receive the result.
func TestCoalescer_CtxCancellation_OneCallerDoesNotAffectSiblings(t *testing.T) {
	c, _ := newTestCoalescer(t)
	want := []byte("payload-survives-sibling-cancel")
	gate := make(chan struct{})
	var genCount atomic.Int64

	// Survivor uses background ctx and runs first; we expect it to be
	// the leader and to receive the result.
	type result struct {
		bytes  []byte
		shared bool
		err    error
	}
	survivor := make(chan result, 1)
	go func() {
		b, shared, err := c.Get(context.Background(), "x", func() ([]byte, error) {
			genCount.Add(1)
			<-gate
			return want, nil
		})
		survivor <- result{b, shared, err}
	}()

	// Wait until generate is in flight so we know the survivor is the
	// leader and any other caller will pile on as a follower.
	waitForInflight(t, c, 1, time.Second)

	// Quitter joins as a follower then cancels its own ctx.
	quitterCtx, cancel := context.WithCancel(context.Background())
	quitterErr := make(chan error, 1)
	go func() {
		_, _, err := c.Get(quitterCtx, "x", func() ([]byte, error) {
			t.Errorf("quitter's generate should never be invoked")
			return nil, nil
		})
		quitterErr <- err
	}()

	// Brief pause to let quitter enter the wait.
	time.Sleep(20 * time.Millisecond)
	cancel()

	// Quitter should return promptly with a ctx error.
	select {
	case err := <-quitterErr:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("quitter err = %v, want errors.Is(ctx.Canceled)", err)
		}
	case <-time.After(time.Second):
		t.Fatal("quitter did not return after ctx cancel")
	}

	// Survivor is still blocked because generate's gate is closed.
	// Open it; survivor should receive the bytes.
	close(gate)
	select {
	case r := <-survivor:
		if r.err != nil {
			t.Fatalf("survivor err = %v, want nil", r.err)
		}
		if !bytes.Equal(r.bytes, want) {
			t.Errorf("survivor bytes = %q, want %q", r.bytes, want)
		}
		if r.shared {
			t.Errorf("survivor shared = true, want false (it was the leader)")
		}
	case <-time.After(time.Second):
		t.Fatal("survivor did not receive result")
	}

	if genCount.Load() != 1 {
		t.Errorf("generate ran %d times, want 1", genCount.Load())
	}
}

// TestCoalescer_FirstCallerSharedFalse_SecondCallerSharedTrue is the
// most direct assertion of the shared flag's leader/follower mapping.
func TestCoalescer_FirstCallerSharedFalse_SecondCallerSharedTrue(t *testing.T) {
	c, _ := newTestCoalescer(t)
	gate := make(chan struct{})
	want := []byte("data")

	type result struct {
		bytes  []byte
		shared bool
	}
	first := make(chan result, 1)
	second := make(chan result, 1)

	go func() {
		b, sh, _ := c.Get(context.Background(), "k", func() ([]byte, error) {
			<-gate
			return want, nil
		})
		first <- result{b, sh}
	}()
	waitForInflight(t, c, 1, time.Second)

	go func() {
		b, sh, _ := c.Get(context.Background(), "k", func() ([]byte, error) {
			t.Errorf("second caller's generate must not run")
			return nil, nil
		})
		second <- result{b, sh}
	}()
	// Give the second caller a moment to enter the wait.
	time.Sleep(10 * time.Millisecond)
	close(gate)

	r1 := <-first
	r2 := <-second

	if r1.shared {
		t.Errorf("first.shared = true, want false")
	}
	if !r2.shared {
		t.Errorf("second.shared = false, want true")
	}
	if !bytes.Equal(r1.bytes, want) || !bytes.Equal(r2.bytes, want) {
		t.Errorf("byte payload mismatch: first=%q second=%q want=%q",
			r1.bytes, r2.bytes, want)
	}
}

// TestCoalescer_PanicInCallerBeforeGet covers the contract that a nil
// generate yields a clear error rather than a panic later.
func TestCoalescer_NilGenerate(t *testing.T) {
	c, _ := newTestCoalescer(t)
	_, _, err := c.Get(context.Background(), "k", nil)
	if err == nil {
		t.Fatal("Get with nil generate should error")
	}
	if !strings.Contains(err.Error(), "generate must not be nil") {
		t.Errorf("err = %v, want generate-must-not-be-nil", err)
	}
}

// TestCoalescer_AlreadyCanceledCtx exercises the early-return path
// when ctx is canceled before any work begins.
func TestCoalescer_AlreadyCanceledCtx(t *testing.T) {
	c, _ := newTestCoalescer(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := c.Get(ctx, "k", func() ([]byte, error) {
		t.Error("generate must not be invoked when ctx is already canceled")
		return nil, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want errors.Is(context.Canceled)", err)
	}
}

// TestCoalescer_KeyExtractor_CollapsesEquivalentKeys verifies that an
// extractor mapping two surface keys to the same canonical form makes
// the Coalescer treat them as one in-flight entry.
func TestCoalescer_KeyExtractor_CollapsesEquivalentKeys(t *testing.T) {
	mc := NewMemoryCounter()
	c := NewCoalescer(CoalescerOptions{
		Counter:      mc,
		Logger:       quietLogger(),
		KeyExtractor: SortedQueryKey(),
	})

	gate := make(chan struct{})
	var count atomic.Int64
	want := []byte("variant")

	type result struct {
		bytes  []byte
		shared bool
	}
	res := make(chan result, 2)
	keys := []string{
		"media/x?w=800&h=600&fit=cover",
		"media/x?fit=cover&h=600&w=800", // same spec, different order
	}
	for _, k := range keys {
		k := k
		go func() {
			b, sh, _ := c.Get(context.Background(), k, func() ([]byte, error) {
				count.Add(1)
				<-gate
				return want, nil
			})
			res <- result{b, sh}
		}()
	}

	waitForInflight(t, c, 1, time.Second)
	time.Sleep(10 * time.Millisecond)
	close(gate)

	r1 := <-res
	r2 := <-res

	if count.Load() != 1 {
		t.Errorf("generate ran %d times, want 1 (keys should collapse)", count.Load())
	}
	if !bytes.Equal(r1.bytes, want) || !bytes.Equal(r2.bytes, want) {
		t.Errorf("byte payload mismatch")
	}
	// One leader, one follower.
	if r1.shared == r2.shared {
		t.Errorf("expected one shared=true and one shared=false, got (%v, %v)",
			r1.shared, r2.shared)
	}
}

// TestCoalescer_NoExtractor_DoesNotCollapse confirms the inverse: with
// no extractor, two surface-equivalent keys do NOT collapse.
func TestCoalescer_NoExtractor_DoesNotCollapse(t *testing.T) {
	c, _ := newTestCoalescer(t)
	var count atomic.Int64
	gen := func() ([]byte, error) {
		count.Add(1)
		return []byte("v"), nil
	}
	c.Get(context.Background(), "k?a=1&b=2", gen)
	c.Get(context.Background(), "k?b=2&a=1", gen)
	if count.Load() != 2 {
		t.Errorf("generate ran %d times, want 2 (no canonicalization)", count.Load())
	}
}

// TestCoalescer_Stats_ReflectsCumulativeAndInFlight asserts the Stats
// snapshot tracks all three fields correctly over a small scenario.
func TestCoalescer_Stats_ReflectsCumulativeAndInFlight(t *testing.T) {
	c, _ := newTestCoalescer(t)
	// Initially zero.
	s := c.Stats()
	if s != (Stats{}) {
		t.Fatalf("initial Stats = %+v, want zero", s)
	}

	// One solo Get → 1 generate, 0 coalesces, 0 inflight at end.
	if _, _, err := c.Get(context.Background(), "k1", func() ([]byte, error) {
		return []byte("a"), nil
	}); err != nil {
		t.Fatal(err)
	}
	s = c.Stats()
	if s.TotalGenerated != 1 || s.TotalCoalesced != 0 || s.InFlight != 0 {
		t.Errorf("after solo Get: Stats = %+v, want {1,0,0}", s)
	}

	// Concurrent Gets that pile up: observe InFlight > 0 mid-flight.
	gate := make(chan struct{})
	const N = 5
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			c.Get(context.Background(), "k2", func() ([]byte, error) {
				<-gate
				return []byte("b"), nil
			})
		}()
	}
	waitForInflight(t, c, 1, time.Second)
	mid := c.Stats()
	if mid.InFlight != 1 {
		t.Errorf("mid-flight Stats.InFlight = %d, want 1", mid.InFlight)
	}
	close(gate)
	wg.Wait()

	final := c.Stats()
	if final.InFlight != 0 {
		t.Errorf("final Stats.InFlight = %d, want 0", final.InFlight)
	}
	if final.TotalGenerated != 2 {
		t.Errorf("final Stats.TotalGenerated = %d, want 2", final.TotalGenerated)
	}
	if final.TotalCoalesced != int64(N-1) {
		t.Errorf("final Stats.TotalCoalesced = %d, want %d", final.TotalCoalesced, N-1)
	}
}

// TestCoalescer_SequentialSameKeyAreEachLeaders verifies that after a
// generate completes, the singleflight entry is released — a subsequent
// Get on the same key starts fresh as a new leader.
func TestCoalescer_SequentialSameKeyAreEachLeaders(t *testing.T) {
	c, _ := newTestCoalescer(t)
	for i := 0; i < 3; i++ {
		_, shared, err := c.Get(context.Background(), "same", func() ([]byte, error) {
			return []byte(fmt.Sprintf("v%d", i)), nil
		})
		if err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		if shared {
			t.Errorf("iter %d: shared = true, want false (sequential)", i)
		}
	}
	if c.Stats().TotalGenerated != 3 {
		t.Errorf("TotalGenerated = %d, want 3", c.Stats().TotalGenerated)
	}
	if c.Stats().TotalCoalesced != 0 {
		t.Errorf("TotalCoalesced = %d, want 0", c.Stats().TotalCoalesced)
	}
}

// TestCoalescer_NilOptions_UsesDefaults exercises the nil-option
// fallbacks (Counter, Logger). It also asserts that a nil Counter
// does not panic on the hot path.
func TestCoalescer_NilOptions_UsesDefaults(t *testing.T) {
	c := NewCoalescer(CoalescerOptions{}) // all nil
	b, shared, err := c.Get(context.Background(), "k", func() ([]byte, error) {
		return []byte("ok"), nil
	})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if shared {
		t.Errorf("shared = true, want false")
	}
	if string(b) != "ok" {
		t.Errorf("bytes = %q, want %q", b, "ok")
	}
}

// waitForInflight polls c.Stats().InFlight until it reaches at least
// want or the timeout expires. Used to synchronize tests around the
// "leader is in generate" milestone without sleeping arbitrarily.
func waitForInflight(t *testing.T, c *Coalescer, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if c.Stats().InFlight >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("inflight did not reach %d within %v (current=%d)",
		want, timeout, c.Stats().InFlight)
}
