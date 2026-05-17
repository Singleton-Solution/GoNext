package hooks

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newTestBus returns a Bus whose slog output is captured into the returned
// buffer. Tests that need to assert on a log line (panic recovery, async
// error reporting) use the buffer; everything else just needs the bus and
// can ignore the buffer.
func newTestBus(t *testing.T) (*Bus, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	l := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return NewBus().WithLogger(l), &buf
}

// ----------------------------------------------------------------------
// Action: register + fire + handler runs
// ----------------------------------------------------------------------

func TestBus_Do_RunsRegisteredHandler(t *testing.T) {
	bus, _ := newTestBus(t)

	var got atomic.Int32
	bus.RegisterAction("post.published", 10, func(ctx context.Context, args ...any) error {
		got.Add(1)
		if len(args) != 1 || args[0].(string) != "hello" {
			t.Errorf("args: got %v want [hello]", args)
		}
		return nil
	})

	if err := bus.Do(context.Background(), "post.published", "hello"); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if got.Load() != 1 {
		t.Errorf("handler runs: got %d want 1", got.Load())
	}
}

func TestBus_Do_NoHandlersIsNotAnError(t *testing.T) {
	bus, _ := newTestBus(t)
	if err := bus.Do(context.Background(), "nothing.listens"); err != nil {
		t.Errorf("Do on empty: %v", err)
	}
}

// ----------------------------------------------------------------------
// Multiple actions in priority order (ties keep registration order)
// ----------------------------------------------------------------------

func TestBus_Do_PriorityOrdering(t *testing.T) {
	bus, _ := newTestBus(t)

	var order []string
	var mu sync.Mutex
	record := func(tag string) ActionHandler {
		return func(ctx context.Context, args ...any) error {
			mu.Lock()
			order = append(order, tag)
			mu.Unlock()
			return nil
		}
	}

	bus.RegisterAction("x", 50, record("p50-first"))
	bus.RegisterAction("x", 10, record("p10"))
	bus.RegisterAction("x", 50, record("p50-second"))
	bus.RegisterAction("x", 20, record("p20"))
	bus.RegisterAction("x", 10, record("p10-second"))

	if err := bus.Do(context.Background(), "x"); err != nil {
		t.Fatalf("Do: %v", err)
	}
	want := []string{"p10", "p10-second", "p20", "p50-first", "p50-second"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Errorf("order: got %v want %v", order, want)
	}
}

// ----------------------------------------------------------------------
// Filter: chain transforms value correctly
// ----------------------------------------------------------------------

func TestBus_ApplyFilters_ChainTransforms(t *testing.T) {
	bus, _ := newTestBus(t)

	bus.RegisterFilter("doc", 10, func(ctx context.Context, v any, args ...any) (any, error) {
		return v.(string) + "-a", nil
	})
	bus.RegisterFilter("doc", 20, func(ctx context.Context, v any, args ...any) (any, error) {
		return v.(string) + "-b", nil
	})
	bus.RegisterFilter("doc", 30, func(ctx context.Context, v any, args ...any) (any, error) {
		return v.(string) + "-c", nil
	})

	got, err := bus.ApplyFilters(context.Background(), "doc", "start")
	if err != nil {
		t.Fatalf("ApplyFilters: %v", err)
	}
	if got != "start-a-b-c" {
		t.Errorf("value: got %q want %q", got, "start-a-b-c")
	}
}

func TestBus_ApplyFilters_NoHandlersReturnsValueAsIs(t *testing.T) {
	bus, _ := newTestBus(t)
	got, err := bus.ApplyFilters(context.Background(), "nope", 42)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != 42 {
		t.Errorf("value: got %v want 42", got)
	}
}

func TestBus_ApplyFilters_ReceivesArgs(t *testing.T) {
	bus, _ := newTestBus(t)

	bus.RegisterFilter("x", 10, func(ctx context.Context, v any, args ...any) (any, error) {
		if len(args) != 2 {
			t.Errorf("args len: got %d want 2", len(args))
		}
		return fmt.Sprintf("%s/%v/%v", v, args[0], args[1]), nil
	})
	got, err := bus.ApplyFilters(context.Background(), "x", "base", "extra1", 7)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "base/extra1/7" {
		t.Errorf("value: got %q", got)
	}
}

// ----------------------------------------------------------------------
// Filter short-circuit
// ----------------------------------------------------------------------

func TestBus_ApplyFilters_ShortCircuit(t *testing.T) {
	bus, _ := newTestBus(t)

	bus.RegisterFilter("c", 10, func(ctx context.Context, v any, args ...any) (any, error) {
		return v.(int) + 1, nil
	})
	bus.RegisterFilter("c", 20, func(ctx context.Context, v any, args ...any) (any, error) {
		return 999, ErrShortCircuit
	})
	bus.RegisterFilter("c", 30, func(ctx context.Context, v any, args ...any) (any, error) {
		t.Error("priority-30 handler should not run after short-circuit")
		return v, nil
	})

	got, err := bus.ApplyFilters(context.Background(), "c", 0)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != 999 {
		t.Errorf("value: got %v want 999", got)
	}
}

func TestBus_ApplyFilters_ShortCircuitViaWrappedErr(t *testing.T) {
	bus, _ := newTestBus(t)

	bus.RegisterFilter("c", 10, func(ctx context.Context, v any, args ...any) (any, error) {
		return "stopped", fmt.Errorf("decided early: %w", ErrShortCircuit)
	})
	bus.RegisterFilter("c", 20, func(ctx context.Context, v any, args ...any) (any, error) {
		t.Error("second handler should not run")
		return v, nil
	})

	got, err := bus.ApplyFilters(context.Background(), "c", "x")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "stopped" {
		t.Errorf("value: got %v", got)
	}
}

// ----------------------------------------------------------------------
// Filter error (non-short-circuit) stops chain and surfaces error
// ----------------------------------------------------------------------

func TestBus_ApplyFilters_ErrorStopsChain(t *testing.T) {
	bus, _ := newTestBus(t)

	bus.RegisterFilter("e", 10, func(ctx context.Context, v any, args ...any) (any, error) {
		return v.(int) + 1, nil
	})
	bus.RegisterFilter("e", 20, func(ctx context.Context, v any, args ...any) (any, error) {
		return v.(int) + 100, errors.New("boom")
	})
	bus.RegisterFilter("e", 30, func(ctx context.Context, v any, args ...any) (any, error) {
		t.Error("post-error handler should not run")
		return v, nil
	})

	got, err := bus.ApplyFilters(context.Background(), "e", 0)
	if err == nil || err.Error() != "boom" {
		t.Fatalf("err: got %v want boom", err)
	}
	// Last accepted value (from p10) flows back to the caller, not the
	// p20 handler's half-baked value.
	if got != 1 {
		t.Errorf("value: got %v want 1 (last accepted)", got)
	}
}

// ----------------------------------------------------------------------
// Action with multiple handlers, one errors: aggregated error
// ----------------------------------------------------------------------

func TestBus_Do_AggregatesErrors(t *testing.T) {
	bus, _ := newTestBus(t)

	var seen atomic.Int32
	bus.RegisterAction("multi", 10, func(ctx context.Context, args ...any) error {
		seen.Add(1)
		return errors.New("err-a")
	})
	bus.RegisterAction("multi", 20, func(ctx context.Context, args ...any) error {
		seen.Add(1)
		return nil // ok
	})
	bus.RegisterAction("multi", 30, func(ctx context.Context, args ...any) error {
		seen.Add(1)
		return errors.New("err-c")
	})

	err := bus.Do(context.Background(), "multi")
	if err == nil {
		t.Fatal("expected aggregated error")
	}
	if seen.Load() != 3 {
		t.Errorf("all handlers should run despite errors: got %d want 3", seen.Load())
	}
	msg := err.Error()
	if !strings.Contains(msg, "err-a") || !strings.Contains(msg, "err-c") {
		t.Errorf("aggregated msg %q missing constituents", msg)
	}
}

// ----------------------------------------------------------------------
// Async action: returns immediately, handler runs in goroutine
// ----------------------------------------------------------------------

func TestBus_RegisterAsync_DoesNotBlock(t *testing.T) {
	bus, _ := newTestBus(t)

	release := make(chan struct{})
	done := make(chan struct{})
	bus.RegisterAsync("slow", 10, func(ctx context.Context, args ...any) error {
		<-release
		close(done)
		return nil
	})

	start := time.Now()
	if err := bus.Do(context.Background(), "slow"); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Errorf("Do blocked %v — async should return immediately", elapsed)
	}

	// Now release the handler and wait for completion.
	close(release)
	bus.Wait()
	select {
	case <-done:
	default:
		t.Error("async handler did not complete")
	}
}

func TestBus_RegisterAsync_ErrorIsLoggedNotReturned(t *testing.T) {
	bus, buf := newTestBus(t)

	bus.RegisterAsync("logfail", 10, func(ctx context.Context, args ...any) error {
		return errors.New("async-boom")
	})

	if err := bus.Do(context.Background(), "logfail"); err != nil {
		t.Errorf("Do should not surface async error: got %v", err)
	}
	bus.Wait()
	if !strings.Contains(buf.String(), "async-boom") {
		t.Errorf("expected async error in log, got: %q", buf.String())
	}
}

// ----------------------------------------------------------------------
// Panic in handler: logged, doesn't crash the bus
// ----------------------------------------------------------------------

func TestBus_Do_PanicIsRecoveredChainContinues(t *testing.T) {
	bus, buf := newTestBus(t)

	var ranAfter atomic.Bool
	bus.RegisterAction("p", 10, func(ctx context.Context, args ...any) error {
		panic("bang")
	})
	bus.RegisterAction("p", 20, func(ctx context.Context, args ...any) error {
		ranAfter.Store(true)
		return nil
	})

	err := bus.Do(context.Background(), "p")
	if err == nil {
		t.Fatal("expected aggregated *panicError")
	}
	var pe *panicError
	if !errors.As(err, &pe) {
		t.Errorf("expected *panicError in chain, got %T: %v", err, err)
	}
	if !ranAfter.Load() {
		t.Error("chain should continue past a panic in an action")
	}
	if !strings.Contains(buf.String(), "panicked") {
		t.Errorf("expected panic log line, got: %q", buf.String())
	}
}

func TestBus_ApplyFilters_PanicStopsChain(t *testing.T) {
	bus, buf := newTestBus(t)

	bus.RegisterFilter("p", 10, func(ctx context.Context, v any, args ...any) (any, error) {
		return v.(int) + 1, nil
	})
	bus.RegisterFilter("p", 20, func(ctx context.Context, v any, args ...any) (any, error) {
		panic(errors.New("kaboom"))
	})
	bus.RegisterFilter("p", 30, func(ctx context.Context, v any, args ...any) (any, error) {
		t.Error("filter chain should stop on panic")
		return v, nil
	})

	got, err := bus.ApplyFilters(context.Background(), "p", 0)
	if err == nil {
		t.Fatal("expected *panicError")
	}
	var pe *panicError
	if !errors.As(err, &pe) {
		t.Errorf("expected *panicError, got %T", err)
	}
	// The original error inside the panic should be reachable via Unwrap.
	if !errors.Is(err, pe) || pe.Unwrap() == nil {
		t.Errorf("panicError.Unwrap chain broken: %v", err)
	}
	// Value returned should be the last accepted value (p10's output: 1),
	// NOT the half-baked value from p20.
	if got != 1 {
		t.Errorf("value on panic: got %v want 1 (last accepted)", got)
	}
	if !strings.Contains(buf.String(), "panicked") {
		t.Errorf("expected log, got: %q", buf.String())
	}
}

func TestBus_RegisterAsync_PanicIsLogged(t *testing.T) {
	bus, buf := newTestBus(t)

	bus.RegisterAsync("apanic", 10, func(ctx context.Context, args ...any) error {
		panic("async-bang")
	})
	if err := bus.Do(context.Background(), "apanic"); err != nil {
		t.Errorf("Do should not surface async panic: got %v", err)
	}
	bus.Wait()
	logged := buf.String()
	if !strings.Contains(logged, "panicked") {
		t.Errorf("expected panic log, got: %q", logged)
	}
}

// ----------------------------------------------------------------------
// Unsubscribe: handler stops being called
// ----------------------------------------------------------------------

func TestBus_RegisterAction_UnsubscribeStopsHandler(t *testing.T) {
	bus, _ := newTestBus(t)

	var hits atomic.Int32
	off := bus.RegisterAction("u", 10, func(ctx context.Context, args ...any) error {
		hits.Add(1)
		return nil
	})

	if err := bus.Do(context.Background(), "u"); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("first dispatch: got %d want 1", hits.Load())
	}

	off()
	off() // idempotent: second call must be a no-op

	if err := bus.Do(context.Background(), "u"); err != nil {
		t.Fatalf("Do post-unsub: %v", err)
	}
	if hits.Load() != 1 {
		t.Errorf("post-unsubscribe: got %d want 1", hits.Load())
	}
}

func TestBus_RegisterFilter_UnsubscribeStopsHandler(t *testing.T) {
	bus, _ := newTestBus(t)

	off := bus.RegisterFilter("uf", 10, func(ctx context.Context, v any, args ...any) (any, error) {
		return "should-not-see", nil
	})
	off()

	got, err := bus.ApplyFilters(context.Background(), "uf", "initial")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "initial" {
		t.Errorf("filter ran after unsubscribe: got %q", got)
	}
}

func TestBus_Unsubscribe_DuringActiveDispatch(t *testing.T) {
	// A handler unsubscribes itself mid-dispatch; later handlers still run.
	bus, _ := newTestBus(t)

	var off func()
	var ranSecond atomic.Bool
	off = bus.RegisterAction("self", 10, func(ctx context.Context, args ...any) error {
		off() // self-unsubscribe
		return nil
	})
	bus.RegisterAction("self", 20, func(ctx context.Context, args ...any) error {
		ranSecond.Store(true)
		return nil
	})

	if err := bus.Do(context.Background(), "self"); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if !ranSecond.Load() {
		t.Error("second handler should still run after self-unsubscribe")
	}
	// Next dispatch: first handler should be gone.
	ranSecond.Store(false)
	if err := bus.Do(context.Background(), "self"); err != nil {
		t.Fatalf("Do 2: %v", err)
	}
	if !ranSecond.Load() {
		t.Error("second handler should still run on next dispatch")
	}
}

// ----------------------------------------------------------------------
// Reentrance: a handler can register new hooks while running
// ----------------------------------------------------------------------

func TestBus_Register_FromInsideHandler(t *testing.T) {
	bus, _ := newTestBus(t)

	var inflightCount atomic.Int32
	var nextDispatchCount atomic.Int32

	bus.RegisterAction("rein", 10, func(ctx context.Context, args ...any) error {
		// Register another handler from inside the running dispatch.
		bus.RegisterAction("rein", 20, func(ctx context.Context, args ...any) error {
			nextDispatchCount.Add(1)
			return nil
		})
		inflightCount.Add(1)
		return nil
	})

	if err := bus.Do(context.Background(), "rein"); err != nil {
		t.Fatalf("Do 1: %v", err)
	}
	if inflightCount.Load() != 1 {
		t.Errorf("outer handler ran: got %d want 1", inflightCount.Load())
	}
	if nextDispatchCount.Load() != 0 {
		t.Errorf("newly registered handler should NOT run in the same dispatch: got %d want 0",
			nextDispatchCount.Load())
	}

	if err := bus.Do(context.Background(), "rein"); err != nil {
		t.Fatalf("Do 2: %v", err)
	}
	if nextDispatchCount.Load() == 0 {
		t.Error("newly registered handler should run on the next dispatch")
	}
}

// ----------------------------------------------------------------------
// Concurrent register + fire: race detector clean
// ----------------------------------------------------------------------

func TestBus_ConcurrentRegisterAndFire(t *testing.T) {
	bus, _ := newTestBus(t)

	const (
		registrars = 8
		firers     = 8
		ticks      = 200
	)

	var fired atomic.Int64
	stop := make(chan struct{})

	// Pre-register one handler so the slot exists from the start.
	bus.RegisterAction("c", 10, func(ctx context.Context, args ...any) error {
		fired.Add(1)
		return nil
	})

	var wg sync.WaitGroup
	// Concurrent registrars: each registers and unregisters in a loop.
	for i := 0; i < registrars; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				off := bus.RegisterAction("c", 20, func(ctx context.Context, args ...any) error {
					return nil
				})
				off()
			}
		}()
	}
	// Concurrent firers.
	for i := 0; i < firers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < ticks; j++ {
				_ = bus.Do(context.Background(), "c")
			}
		}()
	}

	// Let the firers finish.
	for i := 0; i < firers*ticks/100; i++ {
		time.Sleep(time.Millisecond)
	}
	// Give explicit time for the firers to complete their ticks loop.
	// Cap test wall time at a few hundred ms; race detector is the actual signal.
	deadline := time.Now().Add(2 * time.Second)
	for fired.Load() < int64(firers*ticks) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	close(stop)
	wg.Wait()

	if fired.Load() < int64(firers*ticks) {
		t.Errorf("fired count: got %d want >= %d", fired.Load(), firers*ticks)
	}
}

func TestBus_ConcurrentFilterChain(t *testing.T) {
	// Fire ApplyFilters from many goroutines against a chain that is also
	// being mutated. The race detector is the real assertion; the value
	// check confirms there are no torn snapshots.
	bus, _ := newTestBus(t)

	for i := 0; i < 5; i++ {
		i := i
		bus.RegisterFilter("p", 10*i, func(ctx context.Context, v any, args ...any) (any, error) {
			return v.(int) + i, nil
		})
	}

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				got, err := bus.ApplyFilters(context.Background(), "p", 0)
				if err != nil {
					t.Errorf("err: %v", err)
					return
				}
				// 0+1+2+3+4 = 10
				if got.(int) != 10 {
					t.Errorf("value: got %v want 10", got)
					return
				}
			}
		}()
	}
	// In parallel: register and immediately unregister extra no-op
	// filters at varying priorities. These all return the value
	// unchanged, so the sum should remain 10.
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				off := bus.RegisterFilter("p", 5, func(ctx context.Context, v any, args ...any) (any, error) {
					return v, nil
				})
				off()
			}
		}()
	}
	wg.Wait()
}

// ----------------------------------------------------------------------
// Context propagation
// ----------------------------------------------------------------------

func TestBus_ContextPropagation(t *testing.T) {
	bus, _ := newTestBus(t)

	type ctxKey struct{}
	var got atomic.Value
	bus.RegisterAction("ctx", 10, func(ctx context.Context, args ...any) error {
		got.Store(ctx.Value(ctxKey{}))
		return nil
	})

	ctx := context.WithValue(context.Background(), ctxKey{}, "marker")
	if err := bus.Do(ctx, "ctx"); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if got.Load() != "marker" {
		t.Errorf("ctx value: got %v want marker", got.Load())
	}
}

func TestBus_FilterContextPropagation(t *testing.T) {
	bus, _ := newTestBus(t)

	type ctxKey struct{}
	bus.RegisterFilter("fctx", 10, func(ctx context.Context, v any, args ...any) (any, error) {
		return ctx.Value(ctxKey{}), nil
	})

	ctx := context.WithValue(context.Background(), ctxKey{}, "marker")
	got, err := bus.ApplyFilters(ctx, "fctx", nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "marker" {
		t.Errorf("got %v", got)
	}
}

// ----------------------------------------------------------------------
// MetricsSink integration
// ----------------------------------------------------------------------

type capturingSink struct {
	mu         sync.Mutex
	counters   []sinkCounter
	histograms []sinkHistogram
}
type sinkCounter struct {
	name   string
	labels map[string]string
}
type sinkHistogram struct {
	name   string
	value  float64
	labels map[string]string
}

func (c *capturingSink) Counter(name string, labels map[string]string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.counters = append(c.counters, sinkCounter{name, labels})
}
func (c *capturingSink) Histogram(name string, value float64, labels map[string]string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.histograms = append(c.histograms, sinkHistogram{name, value, labels})
}
func (c *capturingSink) hasCounter(name string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, c := range c.counters {
		if c.name == name {
			return true
		}
	}
	return false
}

func TestBus_MetricsSink_ReceivesCallbacks(t *testing.T) {
	bus, _ := newTestBus(t)
	sink := &capturingSink{}
	bus.WithMetrics(sink)

	bus.RegisterAction("a", 10, func(ctx context.Context, args ...any) error { return nil })
	bus.RegisterAction("a", 20, func(ctx context.Context, args ...any) error { return errors.New("e") })
	_ = bus.Do(context.Background(), "a")

	if !sink.hasCounter(metricDispatchTotal) {
		t.Error("dispatch counter not emitted")
	}
	if !sink.hasCounter(metricHandlerError) {
		t.Error("handler error counter not emitted")
	}

	bus.RegisterFilter("f", 10, func(ctx context.Context, v any, args ...any) (any, error) {
		return v, ErrShortCircuit
	})
	_, _ = bus.ApplyFilters(context.Background(), "f", "x")
	if !sink.hasCounter(metricShortCircuit) {
		t.Error("short-circuit counter not emitted")
	}
}

func TestBus_MetricsSink_PanicCounter(t *testing.T) {
	bus, _ := newTestBus(t)
	sink := &capturingSink{}
	bus.WithMetrics(sink)

	bus.RegisterAction("ap", 10, func(ctx context.Context, args ...any) error { panic("boom") })
	_ = bus.Do(context.Background(), "ap")

	if !sink.hasCounter(metricHandlerPanic) {
		t.Error("panic counter not emitted")
	}
}

func TestBus_WithMetrics_NilRevertsToNoop(t *testing.T) {
	// Smoke test: passing nil to WithMetrics must not crash subsequent dispatches.
	bus, _ := newTestBus(t)
	bus.WithMetrics(nil)
	bus.RegisterAction("z", 10, func(ctx context.Context, args ...any) error { return nil })
	if err := bus.Do(context.Background(), "z"); err != nil {
		t.Errorf("Do with nil sink: %v", err)
	}
}

func TestBus_WithLogger_Nil(t *testing.T) {
	// Smoke test: explicit nil logger should fall back to slog.Default.
	bus := NewBus()
	bus.WithLogger(nil)
	bus.RegisterAction("z", 10, func(ctx context.Context, args ...any) error { return nil })
	if err := bus.Do(context.Background(), "z"); err != nil {
		t.Errorf("Do with nil logger: %v", err)
	}
}

// ----------------------------------------------------------------------
// panicError surface
// ----------------------------------------------------------------------

func TestPanicError_ErrorAndUnwrap(t *testing.T) {
	inner := errors.New("inner")
	pe := &panicError{hook: "h", handler: 3, value: inner}
	if !strings.Contains(pe.Error(), "h") || !strings.Contains(pe.Error(), "3") {
		t.Errorf("Error(): %q", pe.Error())
	}
	if pe.Unwrap() != inner {
		t.Errorf("Unwrap: got %v want %v", pe.Unwrap(), inner)
	}

	// Non-error panic value: Unwrap returns nil so errors.As/Is still work.
	pe2 := &panicError{hook: "x", handler: 0, value: "raw-string"}
	if pe2.Unwrap() != nil {
		t.Errorf("Unwrap on non-error: got %v want nil", pe2.Unwrap())
	}
}

// ----------------------------------------------------------------------
// Unsubscribe is safe across goroutines (race detector clean)
// ----------------------------------------------------------------------

func TestBus_Unsubscribe_ConcurrentWithDispatch(t *testing.T) {
	bus, _ := newTestBus(t)

	const N = 50
	offs := make([]func(), N)
	for i := 0; i < N; i++ {
		i := i
		offs[i] = bus.RegisterAction("c", i, func(ctx context.Context, args ...any) error {
			return nil
		})
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			_ = bus.Do(context.Background(), "c")
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < N; i++ {
			offs[i]()
		}
	}()
	wg.Wait()

	// All unsubscribed → dispatch should be a no-op (no panic, no error).
	if err := bus.Do(context.Background(), "c"); err != nil {
		t.Errorf("post-cleanup Do: %v", err)
	}
}

// ----------------------------------------------------------------------
// ErrShortCircuit returns the handler's value, not the previous value
// ----------------------------------------------------------------------

func TestBus_ApplyFilters_ShortCircuitUsesHandlerValue(t *testing.T) {
	bus, _ := newTestBus(t)
	bus.RegisterFilter("sc", 10, func(ctx context.Context, v any, args ...any) (any, error) {
		return "handler-output", ErrShortCircuit
	})
	got, err := bus.ApplyFilters(context.Background(), "sc", "initial")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "handler-output" {
		t.Errorf("short-circuit value: got %q want handler-output", got)
	}
}

// ----------------------------------------------------------------------
// noopSink: verify the default sink is wired and harmless
// ----------------------------------------------------------------------

func TestBus_DefaultSink_IsNoOp(t *testing.T) {
	bus := NewBus()
	// Trigger every code path that calls into the sink.
	bus.RegisterAction("n", 10, func(ctx context.Context, args ...any) error { return errors.New("e") })
	bus.RegisterAction("n", 20, func(ctx context.Context, args ...any) error { panic("p") })
	_ = bus.Do(context.Background(), "n")

	bus.RegisterFilter("nf", 10, func(ctx context.Context, v any, args ...any) (any, error) {
		return v, ErrShortCircuit
	})
	_, _ = bus.ApplyFilters(context.Background(), "nf", "x")

	// Reaching here without a crash is the assertion.
}

// TestNoopSink_DirectMethods exercises the noop methods through the Sink
// interface to guarantee they are covered (the compiler may inline the
// empty bodies away at hot call sites, which can leave them showing 0%
// in the cover tool even when functionally hit).
func TestNoopSink_DirectMethods(t *testing.T) {
	var s Sink = noopSink{}
	s.Counter("anything", map[string]string{"k": "v"})
	s.Histogram("anything", 1.0, map[string]string{"k": "v"})
}

// TestBus_Sink_FallbackWhenUnset directly invokes the unexported sink()
// accessor on a Bus that has never been initialized via NewBus, to cover
// the defensive nil-pointer branch.
func TestBus_Sink_FallbackWhenUnset(t *testing.T) {
	var b Bus
	if b.sink() == nil {
		t.Error("sink() on uninitialized Bus should never return nil")
	}
}
