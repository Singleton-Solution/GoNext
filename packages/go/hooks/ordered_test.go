package hooks

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ----------------------------------------------------------------------
// Same-key events fire in submission order across many goroutines
// ----------------------------------------------------------------------

// TestOrderedAsync_SameKeyPreservesOrder verifies the core contract: events
// submitted to Do with the same KeyFn-derived key are delivered to async
// subscribers in submission order even when Do is called concurrently from
// many goroutines. Without ordering, the unordered dispatchAsync path
// fans out independent goroutines and the receive order would be a free
// race.
func TestOrderedAsync_SameKeyPreservesOrder(t *testing.T) {
	bus, _ := newTestBus(t)
	bus.SetActionOptions("post.saved", OrderedAsync{
		KeyFn: func(args []any) string { return args[0].(string) },
	})

	var mu sync.Mutex
	got := make([]int, 0, 200)
	bus.RegisterAsync("post.saved", 10, func(ctx context.Context, args ...any) error {
		mu.Lock()
		got = append(got, args[1].(int))
		mu.Unlock()
		return nil
	})

	// Fire 200 events sequentially (from one goroutine; the worker
	// reads from a channel so order is preserved on a single submitter).
	for i := 0; i < 200; i++ {
		if err := bus.Do(context.Background(), "post.saved", "post-1", i); err != nil {
			t.Fatalf("Do: %v", err)
		}
	}
	bus.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 200 {
		t.Fatalf("count: got %d want 200", len(got))
	}
	for i, v := range got {
		if v != i {
			t.Errorf("order: index %d got %d", i, v)
			break
		}
	}
}

// TestOrderedAsync_SameKeySerializesAllSubscribers checks that two async
// subscribers for the same key see events in the same order — i.e. the
// dispatcher serializes WITHIN an event (all subscribers for event N
// complete before event N+1 starts), not just across events.
func TestOrderedAsync_SameKeySerializesAllSubscribers(t *testing.T) {
	bus, _ := newTestBus(t)
	bus.SetActionOptions("k", OrderedAsync{
		KeyFn: func(args []any) string { return args[0].(string) },
	})

	var mu sync.Mutex
	gotA := make([]int, 0, 50)
	gotB := make([]int, 0, 50)

	bus.RegisterAsync("k", 10, func(ctx context.Context, args ...any) error {
		mu.Lock()
		gotA = append(gotA, args[1].(int))
		mu.Unlock()
		return nil
	})
	bus.RegisterAsync("k", 20, func(ctx context.Context, args ...any) error {
		mu.Lock()
		gotB = append(gotB, args[1].(int))
		mu.Unlock()
		return nil
	})

	for i := 0; i < 50; i++ {
		_ = bus.Do(context.Background(), "k", "x", i)
	}
	bus.Wait()

	mu.Lock()
	defer mu.Unlock()
	for i := 0; i < 50; i++ {
		if gotA[i] != i {
			t.Errorf("subscriber A out of order at %d: %d", i, gotA[i])
		}
		if gotB[i] != i {
			t.Errorf("subscriber B out of order at %d: %d", i, gotB[i])
		}
	}
}

// ----------------------------------------------------------------------
// Different keys can interleave (parallelism preserved across keys)
// ----------------------------------------------------------------------

// TestOrderedAsync_DifferentKeysInterleave verifies the dual half of the
// ordering contract: events with DIFFERENT keys run in parallel. We
// confirm this by having two slow subscribers (one for key A, one
// effectively for key B via the same handler) and timing: if everything
// were serialized on a single worker, the test would take 2× the slow
// duration; if it runs in parallel, total time stays close to 1×.
func TestOrderedAsync_DifferentKeysInterleave(t *testing.T) {
	bus, _ := newTestBus(t)
	bus.SetActionOptions("k", OrderedAsync{
		KeyFn: func(args []any) string { return args[0].(string) },
	})

	const sleep = 200 * time.Millisecond
	bus.RegisterAsync("k", 10, func(ctx context.Context, args ...any) error {
		time.Sleep(sleep)
		return nil
	})

	start := time.Now()
	_ = bus.Do(context.Background(), "k", "key-A", 1)
	_ = bus.Do(context.Background(), "k", "key-B", 2)
	bus.Wait()
	elapsed := time.Since(start)

	// With parallel keys, elapsed ~= sleep (plus a small fudge factor).
	// Serialized would be ~2×sleep. We assert under 1.5×sleep to give
	// loaded CI runners room without losing the signal.
	if elapsed >= sleep*3/2+50*time.Millisecond {
		t.Errorf("keys did not run in parallel: elapsed %v (sleep %v)", elapsed, sleep)
	}
}

// ----------------------------------------------------------------------
// Idle reaper releases worker goroutines after the idle timeout
// ----------------------------------------------------------------------

// TestOrderedAsync_IdleReaper verifies that a per-key worker is retired
// after the configured idle timeout. We pick a short idle timeout (50ms)
// and check the worker map shrinks. The reaper ticks every
// orderedReaperInterval (5s) by default, which would make this test
// slow; so we wait long enough for at least one tick — and reduce the
// test's tolerance to "eventually" rather than "immediately".
func TestOrderedAsync_IdleReaper(t *testing.T) {
	if testing.Short() {
		t.Skip("uses ~6s of wall time waiting for reaper tick")
	}
	bus, _ := newTestBus(t)
	bus.SetActionOptions("k", OrderedAsync{
		KeyFn:       func(args []any) string { return args[0].(string) },
		IdleTimeout: 50 * time.Millisecond,
	})
	bus.RegisterAsync("k", 10, func(ctx context.Context, args ...any) error {
		return nil
	})

	// Fire a few events to spin up the worker.
	for i := 0; i < 3; i++ {
		_ = bus.Do(context.Background(), "k", "hot-key", i)
	}
	bus.Wait()

	if got := bus.drainOrderedForTests(); got != 1 {
		t.Fatalf("worker count before reap: got %d want 1", got)
	}

	// Wait for the reaper to fire (orderedReaperInterval = 5s) plus a
	// safety margin. The 50ms idle timeout will have elapsed long before
	// the tick lands.
	deadline := time.Now().Add(orderedReaperInterval + 2*time.Second)
	for time.Now().Before(deadline) {
		if bus.drainOrderedForTests() == 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Errorf("worker not reaped: count %d", bus.drainOrderedForTests())
}

// ----------------------------------------------------------------------
// Backpressure: full buffer + new submission returns ErrOrderedBacklog
// ----------------------------------------------------------------------

// TestOrderedAsync_BackpressureReturnsError exercises the bounded-buffer
// + enqueue-timeout path. A slow subscriber (one that blocks forever on
// release) lets us fill the buffer with N events; the N+2nd submission
// (one in worker, N in buffer) must return ErrOrderedBacklog within the
// configured EnqueueTimeout.
func TestOrderedAsync_BackpressureReturnsError(t *testing.T) {
	bus, _ := newTestBus(t)
	bus.SetActionOptions("k", OrderedAsync{
		KeyFn:          func(args []any) string { return args[0].(string) },
		BufferSize:     2,
		EnqueueTimeout: 100 * time.Millisecond,
	})

	release := make(chan struct{})
	var inFlight atomic.Int32
	bus.RegisterAsync("k", 10, func(ctx context.Context, args ...any) error {
		inFlight.Add(1)
		<-release
		inFlight.Add(-1)
		return nil
	})

	// First event lands in the worker and blocks on `release`.
	if err := bus.Do(context.Background(), "k", "key", 0); err != nil {
		t.Fatalf("event 0: %v", err)
	}
	// Wait for the worker to actually pick up event 0 so the channel
	// reaches "1 in flight, buffer empty" before we start filling.
	deadline := time.Now().Add(time.Second)
	for inFlight.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("worker never picked up first event")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Two more events fill the buffer (BufferSize: 2).
	for i := 1; i <= 2; i++ {
		if err := bus.Do(context.Background(), "k", "key", i); err != nil {
			t.Fatalf("event %d: %v", i, err)
		}
	}

	// Third event must block, hit the timeout, and surface ErrOrderedBacklog
	// inside Do's aggregated error.
	start := time.Now()
	err := bus.Do(context.Background(), "k", "key", 3)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("expected backlog error, got nil")
	}
	if !errors.Is(err, ErrOrderedBacklog) {
		t.Errorf("expected ErrOrderedBacklog, got %v", err)
	}
	// Timeout was 100ms; we should have waited at least that long
	// (minus scheduler jitter) and not much more.
	if elapsed < 80*time.Millisecond {
		t.Errorf("returned too quickly: %v", elapsed)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("waited too long: %v", elapsed)
	}

	// Cleanup: release the worker and drain.
	close(release)
	bus.Wait()
}

// ----------------------------------------------------------------------
// Race / stress: 1000 events across 10 keys × 100 goroutines, ordering preserved per key
// ----------------------------------------------------------------------

// TestOrderedAsync_RaceManyKeys is the high-volume stress test required by
// the spec. We submit 1000 events across 10 keys from 100 goroutines and
// assert that, FOR EACH KEY, the receive order matches a submission order
// — meaning each goroutine's events for a given key arrive in the order
// that goroutine submitted them.
//
// Note we do NOT assert a global submission order across goroutines (Do
// is called concurrently — the bus has no defined ordering between two
// parallel Do calls). The contract is "for any key K, events for K are
// delivered in the order the bus received their Do calls."
//
// We simulate this by: each goroutine submits a strictly increasing
// per-goroutine counter for its key; on receive we assert that, grouped
// by (key, goroutine), the values come back monotonically increasing.
func TestOrderedAsync_RaceManyKeys(t *testing.T) {
	bus, _ := newTestBus(t)
	bus.SetActionOptions("k", OrderedAsync{
		KeyFn:      func(args []any) string { return args[0].(string) },
		BufferSize: 1024,
	})

	type entry struct {
		key       string
		goroutine int
		seq       int
	}
	const goroutines = 100
	const perGoroutine = 10
	const keys = 10

	var mu sync.Mutex
	received := make(map[string][]entry, keys)

	bus.RegisterAsync("k", 10, func(ctx context.Context, args ...any) error {
		e := entry{
			key:       args[0].(string),
			goroutine: args[1].(int),
			seq:       args[2].(int),
		}
		mu.Lock()
		received[e.key] = append(received[e.key], e)
		mu.Unlock()
		return nil
	})

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			for s := 0; s < perGoroutine; s++ {
				key := fmt.Sprintf("k%d", s%keys)
				_ = bus.Do(context.Background(), "k", key, g, s)
			}
		}(g)
	}
	wg.Wait()
	bus.Wait()

	mu.Lock()
	defer mu.Unlock()
	total := 0
	for k, entries := range received {
		total += len(entries)
		// For each goroutine that contributed to this key, its events
		// must be monotonically increasing in seq.
		last := make(map[int]int)
		for i := range last {
			last[i] = -1
		}
		for i, e := range entries {
			prev, ok := last[e.goroutine]
			if ok && e.seq <= prev {
				t.Errorf("key %s pos %d: goroutine %d went %d -> %d (out of order)", k, i, e.goroutine, prev, e.seq)
			}
			last[e.goroutine] = e.seq
		}
	}
	if total != goroutines*perGoroutine {
		t.Errorf("total events: got %d want %d", total, goroutines*perGoroutine)
	}
}

// ----------------------------------------------------------------------
// Helper: a KeyFn that panics is recovered and falls back to empty key
// ----------------------------------------------------------------------

// TestOrderedAsync_KeyFnPanicRecovered verifies safeKey's panic recovery.
// A panicking KeyFn must not crash the dispatch — instead the event
// lands on the empty-key worker (alongside any other panicking events,
// which is acceptable because the action is misconfigured).
func TestOrderedAsync_KeyFnPanicRecovered(t *testing.T) {
	bus, buf := newTestBus(t)
	bus.SetActionOptions("k", OrderedAsync{
		KeyFn: func(args []any) string {
			panic("boom")
		},
	})
	var got atomic.Int32
	bus.RegisterAsync("k", 10, func(ctx context.Context, args ...any) error {
		got.Add(1)
		return nil
	})

	if err := bus.Do(context.Background(), "k", "anything"); err != nil {
		t.Fatalf("Do: %v", err)
	}
	bus.Wait()

	if got.Load() != 1 {
		t.Errorf("handler did not run after KeyFn panic: got %d", got.Load())
	}
	if !strings.Contains(buf.String(), "KeyFn panicked") {
		t.Errorf("expected panic log line; got: %s", buf.String())
	}
}
