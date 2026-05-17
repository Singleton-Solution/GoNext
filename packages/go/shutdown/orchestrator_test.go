package shutdown

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// quietLogger returns a slog.Logger that discards everything. Used by
// tests that don't care about log output (which is most of them — drain
// behavior is asserted via call-order recording, not log scraping).
func quietLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// recorder captures the order closers fire in. A single recorder
// instance threaded through every closer in a test gives us a totally
// ordered trace of "which name fired when", which is the whole property
// we're testing in the LIFO + best-effort cases.
type recorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *recorder) record(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, name)
}

func (r *recorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.calls))
	copy(out, r.calls)
	return out
}

// closerThatRecords builds a Closer that appends name to r and returns
// err. The Closer respects ctx by returning ctx.Err() if it expired
// before name was recorded — that lets us test "did the cancellation
// reach the closer".
func closerThatRecords(r *recorder, name string, err error) Closer {
	return func(ctx context.Context) error {
		if ctx.Err() != nil {
			r.record(name + ".ctxcanceled")
			return ctx.Err()
		}
		r.record(name)
		return err
	}
}

func TestNew_RequiresLogger(t *testing.T) {
	_, err := New(Options{})
	if err == nil || !strings.Contains(err.Error(), "Log") {
		t.Fatalf("want error mentioning Log, got %v", err)
	}
}

func TestNew_DefaultsBudget(t *testing.T) {
	o, err := New(Options{Log: quietLogger()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if o.budget != defaultBudget {
		t.Errorf("budget: got %v, want %v", o.budget, defaultBudget)
	}
}

func TestRegister_RejectsNilCloser(t *testing.T) {
	o, _ := New(Options{Log: quietLogger()})
	if err := o.Register("nil", nil); err == nil {
		t.Fatal("want error for nil closer, got nil")
	}
}

// TestDrain_LIFOOrder asserts the core ordering guarantee: register
// (A, B, C) → drain calls C, B, A. This mirrors `defer` semantics and
// is the property main.go relies on for "stop accepting, then drain,
// then close pools".
func TestDrain_LIFOOrder(t *testing.T) {
	o, _ := New(Options{Log: quietLogger(), Budget: time.Second})
	r := &recorder{}
	for _, name := range []string{"first", "second", "third"} {
		if err := o.Register(name, closerThatRecords(r, name, nil)); err != nil {
			t.Fatalf("Register(%q): %v", name, err)
		}
	}

	if err := o.Drain(context.Background()); err != nil {
		t.Fatalf("Drain: %v", err)
	}

	got := r.snapshot()
	want := []string{"third", "second", "first"}
	if !equalSlices(got, want) {
		t.Errorf("call order: got %v, want %v", got, want)
	}
}

// TestDrain_ContinuesAfterError is the best-effort guarantee: an
// errored closer must not abort the drain. Worker -> audit -> db all
// need to run even if Worker.Stop() errors.
func TestDrain_ContinuesAfterError(t *testing.T) {
	o, _ := New(Options{Log: quietLogger(), Budget: time.Second})
	r := &recorder{}
	boom := errors.New("boom")
	_ = o.Register("a", closerThatRecords(r, "a", nil))
	_ = o.Register("b", closerThatRecords(r, "b", boom))
	_ = o.Register("c", closerThatRecords(r, "c", nil))

	err := o.Drain(context.Background())
	if err == nil || !errors.Is(err, boom) {
		t.Fatalf("want wrapped boom, got %v", err)
	}

	got := r.snapshot()
	want := []string{"c", "b", "a"}
	if !equalSlices(got, want) {
		t.Errorf("call order: got %v, want %v — every closer must run", got, want)
	}
}

// TestDrain_FirstErrorWins documents the error semantics: when several
// closers fail, the FIRST chronological failure is the head of the
// errors.Join chain, so logs say "what went wrong first" instead of
// "what went wrong last".
func TestDrain_FirstErrorWins(t *testing.T) {
	o, _ := New(Options{Log: quietLogger(), Budget: time.Second})
	first := errors.New("first")
	second := errors.New("second")
	// Registered in LIFO order: c drains first, then b, then a.
	_ = o.Register("a", func(context.Context) error { return nil })
	_ = o.Register("b", func(context.Context) error { return second })
	_ = o.Register("c", func(context.Context) error { return first })

	err := o.Drain(context.Background())
	if !errors.Is(err, first) || !errors.Is(err, second) {
		t.Fatalf("want both errors joined, got %v", err)
	}
	// The aggregate must include both, but the wrapping order should
	// surface `first` ahead of `second` in the message.
	if iFirst, iSecond := strings.Index(err.Error(), "first"), strings.Index(err.Error(), "second"); iFirst < 0 || iSecond < 0 || iFirst > iSecond {
		t.Errorf("first error should appear before second in joined message: %q", err.Error())
	}
}

// TestDrain_ContextCancelMidDrain verifies the short-budget fallback:
// when ctx fires mid-drain, remaining closers still run but with a
// minimum slice of time so they can flush. This is the "honor
// terminationGracePeriod even when caller gave up" guarantee.
func TestDrain_ContextCancelMidDrain(t *testing.T) {
	o, _ := New(Options{Log: quietLogger(), Budget: 5 * time.Second})
	r := &recorder{}
	// Closer "slow" cancels the parent ctx as soon as it runs (it
	// drains last → fires first). Subsequent closers (b, a) must
	// still run despite the canceled ctx.
	ctx, cancel := context.WithCancel(context.Background())
	_ = o.Register("a", closerThatRecords(r, "a", nil))
	_ = o.Register("b", closerThatRecords(r, "b", nil))
	_ = o.Register("slow", func(ctx context.Context) error {
		r.record("slow")
		cancel()
		return nil
	})

	err := o.Drain(ctx)
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}

	got := r.snapshot()
	// Order: slow → b → a; the "*.ctxcanceled" suffix from the helper
	// would only appear if the closer actually observed a canceled
	// ctx. We expect plain names because detachIfCanceled hands them
	// a fresh ctx.
	want := []string{"slow", "b", "a"}
	if !equalSlices(got, want) {
		t.Errorf("call order with mid-drain cancel: got %v, want %v", got, want)
	}
}

// TestRegister_RejectedDuringDrain is the race guarantee: once Drain
// starts, no new registrations are accepted. This prevents the order
// from being silently changed in flight.
func TestRegister_RejectedDuringDrain(t *testing.T) {
	o, _ := New(Options{Log: quietLogger(), Budget: time.Second})

	registerErr := make(chan error, 1)
	released := make(chan struct{})

	// First closer blocks until released; while it blocks, Drain is
	// in StateDraining and Register MUST return ErrDraining.
	_ = o.Register("blocker", func(ctx context.Context) error {
		err := o.Register("late", func(context.Context) error { return nil })
		registerErr <- err
		<-released
		return nil
	})

	drainDone := make(chan error, 1)
	go func() { drainDone <- o.Drain(context.Background()) }()

	select {
	case err := <-registerErr:
		if !errors.Is(err, ErrDraining) {
			t.Errorf("Register during drain: got %v, want ErrDraining", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Register call did not return within 1s")
	}
	close(released)
	if err := <-drainDone; err != nil {
		t.Errorf("Drain: %v", err)
	}

	// Registering after drain finishes is also rejected.
	if err := o.Register("post", func(context.Context) error { return nil }); !errors.Is(err, ErrDraining) {
		t.Errorf("Register post-drain: got %v, want ErrDraining", err)
	}
}

// TestDrain_Idempotent ensures repeated Drain calls do not re-run
// closers. The first error is cached and returned to every caller; if
// Wait() and a manual Drain() race, we don't double-Close anything.
func TestDrain_Idempotent(t *testing.T) {
	o, _ := New(Options{Log: quietLogger(), Budget: time.Second})
	var calls atomic.Int32
	_ = o.Register("once", func(context.Context) error {
		calls.Add(1)
		return errors.New("once")
	})

	err1 := o.Drain(context.Background())
	err2 := o.Drain(context.Background())
	if err1 == nil || err2 == nil {
		t.Fatalf("want errors, got %v / %v", err1, err2)
	}
	if err1.Error() != err2.Error() {
		t.Errorf("errors differ across Drain calls: %q vs %q", err1, err2)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("closer fired %d times, want 1 (idempotent Drain)", got)
	}
}

// TestDrain_PerStepBudget verifies that a slow closer cannot eat the
// whole drain budget — the orchestrator splits the budget across
// remaining steps so a single hanging Close gets its slice and then
// gives up.
func TestDrain_PerStepBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("timing-sensitive")
	}
	o, _ := New(Options{Log: quietLogger(), Budget: 600 * time.Millisecond})
	r := &recorder{}

	// 3 closers, 600ms budget → ~200ms per step. The slow one (drains
	// first because LIFO) blocks for 5s; we expect its ctx to fire
	// around the 200ms mark and the remaining two to still run within
	// the budget.
	_ = o.Register("a", closerThatRecords(r, "a", nil))
	_ = o.Register("b", closerThatRecords(r, "b", nil))
	_ = o.Register("slow", func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			r.record("slow.timeout")
			return ctx.Err()
		case <-time.After(5 * time.Second):
			r.record("slow.completed")
			return nil
		}
	})

	start := time.Now()
	_ = o.Drain(context.Background())
	elapsed := time.Since(start)

	// Total drain must stay under 1.5s (600ms budget + slop); without
	// per-step budgeting, the slow closer would eat all 5s.
	if elapsed > 1500*time.Millisecond {
		t.Errorf("drain took %v, want < 1.5s (per-step budget was not enforced)", elapsed)
	}
	got := r.snapshot()
	// Verify the trailing closers ran despite the slow one timing out.
	if len(got) < 3 {
		t.Fatalf("only %d closers ran: %v", len(got), got)
	}
	if got[0] != "slow.timeout" {
		t.Errorf("first event should be slow.timeout, got %q", got[0])
	}
	if got[1] != "b" || got[2] != "a" {
		t.Errorf("trailing closers should still run, got %v", got)
	}
}

// TestDrain_PanicInCloserRecovers asserts the panic guard — a
// nil-deref Close (e.g. half-initialized resource) must not abort the
// rest of the drain. Otherwise one buggy plugin could orphan the DB
// pool.
func TestDrain_PanicInCloserRecovers(t *testing.T) {
	o, _ := New(Options{Log: quietLogger(), Budget: time.Second})
	r := &recorder{}
	_ = o.Register("safe", closerThatRecords(r, "safe", nil))
	_ = o.Register("panicker", func(context.Context) error {
		var p *int
		_ = *p // intentional nil deref
		return nil
	})

	err := o.Drain(context.Background())
	if err == nil || !strings.Contains(err.Error(), "panic") {
		t.Fatalf("want panic-wrapped error, got %v", err)
	}
	if got := r.snapshot(); !equalSlices(got, []string{"safe"}) {
		t.Errorf("safe closer should still run after panic, got %v", got)
	}
}

// TestCloserFromIO bridges io.Closer (Redis, etc.) to the Closer shape.
// We assert the wrapper actually invokes Close and that the returned
// error is propagated.
func TestCloserFromIO(t *testing.T) {
	want := errors.New("io boom")
	fc := &fakeIOCloser{err: want}
	c := CloserFromIO(fc)
	if err := c(context.Background()); err != want {
		t.Errorf("CloserFromIO err: got %v, want %v", err, want)
	}
	if !fc.closed {
		t.Error("CloserFromIO did not invoke Close")
	}
}

// TestCloserFromFunc bridges a no-arg cleanup (pgxpool.Pool.Close) to
// the Closer shape. Always returns nil because the wrapped function
// has no error channel.
func TestCloserFromFunc(t *testing.T) {
	called := false
	c := CloserFromFunc(func() { called = true })
	if err := c(context.Background()); err != nil {
		t.Errorf("CloserFromFunc err: %v", err)
	}
	if !called {
		t.Error("CloserFromFunc did not invoke f")
	}
}

// TestWait_DrainsOnSignal is the integration smoke test required by
// the issue: start a server (httptest), register its shutdown plus a
// couple of fake resources, send SIGTERM from a goroutine, and verify
// Drain returns and every registered closer fired.
func TestWait_DrainsOnSignal(t *testing.T) {
	// Use SIGUSR1 so we don't bother the parent test runner with
	// SIGTERM. The semantics are identical — signal.NotifyContext
	// fires once any subscribed signal arrives.
	o, _ := New(Options{
		Log:     quietLogger(),
		Budget:  2 * time.Second,
		Signals: []os.Signal{syscall.SIGUSR1},
	})

	r := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	// Shut down the httptest.Server through the orchestrator so the
	// LIFO chain includes a real http.Server.
	_ = o.Register("http.server", func(context.Context) error {
		r.record("http.server")
		srv.Close()
		return nil
	})
	_ = o.Register("audit.emitter", closerThatRecords(r, "audit.emitter", nil))
	_ = o.Register("metrics.flusher", closerThatRecords(r, "metrics.flusher", nil))
	_ = o.Register("redis.client", closerThatRecords(r, "redis.client", nil))
	_ = o.Register("db.pool", closerThatRecords(r, "db.pool", nil))

	// Sanity: make sure the server is actually accepting before we
	// signal — otherwise we'd be testing nothing.
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("pre-drain GET: %v", err)
	}
	resp.Body.Close()

	waitErr := make(chan error, 1)
	go func() { waitErr <- o.Wait(context.Background()) }()

	// Send SIGUSR1 to ourselves; the orchestrator's signal handler
	// fires, drain begins, all closers run in LIFO order.
	go func() {
		// Tiny pause so the signal handler is definitely installed.
		time.Sleep(50 * time.Millisecond)
		_ = syscall.Kill(syscall.Getpid(), syscall.SIGUSR1)
	}()

	select {
	case err := <-waitErr:
		if err != nil {
			t.Fatalf("Wait returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Wait did not return within 3s of signal")
	}

	got := r.snapshot()
	want := []string{"db.pool", "redis.client", "metrics.flusher", "audit.emitter", "http.server"}
	if !equalSlices(got, want) {
		t.Errorf("LIFO drain order: got %v, want %v", got, want)
	}
}

// TestWait_DrainsOnCtxCancel covers the non-signal path: caller cancels
// the parent ctx and Wait still drains. This is the "test harness" or
// "embedded in a larger runtime" use case.
func TestWait_DrainsOnCtxCancel(t *testing.T) {
	o, _ := New(Options{
		Log:    quietLogger(),
		Budget: time.Second,
	})
	r := &recorder{}
	_ = o.Register("a", closerThatRecords(r, "a", nil))
	_ = o.Register("b", closerThatRecords(r, "b", nil))

	ctx, cancel := context.WithCancel(context.Background())
	waitErr := make(chan error, 1)
	go func() { waitErr <- o.Wait(ctx) }()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-waitErr:
		if err != nil {
			t.Fatalf("Wait err: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not return on ctx cancel")
	}

	if got, want := r.snapshot(), []string{"b", "a"}; !equalSlices(got, want) {
		t.Errorf("ctx-cancel drain: got %v, want %v", got, want)
	}
}

// fakeIOCloser implements io.Closer for the CloserFromIO test.
type fakeIOCloser struct {
	closed bool
	err    error
}

func (f *fakeIOCloser) Close() error {
	f.closed = true
	return f.err
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Ensure the package compiles against the Closer signature we promise.
var _ Closer = func(context.Context) error { return nil }
var _ = fmt.Sprint // keep fmt imported for future debug helpers
