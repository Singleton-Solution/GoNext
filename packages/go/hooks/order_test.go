package hooks

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// recorder is a small helper for the order tests: it appends a tag to a
// shared slice every time its handler runs. Each test creates one, hands
// out tagged handlers, then asserts on the recorded order. Using a
// recorder rather than per-test closures keeps the assertion site close
// to the want-list.
type recorder struct {
	mu    sync.Mutex
	order []string
}

func (r *recorder) handler(tag string) ActionHandler {
	return func(ctx context.Context, args ...any) error {
		r.mu.Lock()
		r.order = append(r.order, tag)
		r.mu.Unlock()
		return nil
	}
}

func (r *recorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}

// joined renders the recorded order as a comma-separated string so
// `t.Errorf("got %s want %s", ...)` produces readable diffs.
func (r *recorder) joined() string {
	return strings.Join(r.snapshot(), ",")
}

// ----------------------------------------------------------------------
// No constraints: behaves exactly like pre-#265 priority+regOrder sort.
// ----------------------------------------------------------------------

func TestOrder_NoConstraints_PriorityThenRegistrationOrder(t *testing.T) {
	bus, _ := newTestBus(t)
	r := &recorder{}

	// Mix of priorities, deliberately registered out of priority order.
	if _, err := bus.RegisterActionWithOptions("h", RegisterOptions{Priority: 50, Source: "p50-first"}, r.handler("p50-first")); err != nil {
		t.Fatal(err)
	}
	if _, err := bus.RegisterActionWithOptions("h", RegisterOptions{Priority: 10, Source: "p10"}, r.handler("p10")); err != nil {
		t.Fatal(err)
	}
	if _, err := bus.RegisterActionWithOptions("h", RegisterOptions{Priority: 50, Source: "p50-second"}, r.handler("p50-second")); err != nil {
		t.Fatal(err)
	}
	if _, err := bus.RegisterActionWithOptions("h", RegisterOptions{Priority: 20, Source: "p20"}, r.handler("p20")); err != nil {
		t.Fatal(err)
	}

	if err := bus.Do(context.Background(), "h"); err != nil {
		t.Fatalf("Do: %v", err)
	}
	got := r.joined()
	want := "p10,p20,p50-first,p50-second"
	if got != want {
		t.Errorf("order: got %q want %q", got, want)
	}
}

// ----------------------------------------------------------------------
// Simple `after`: A.after(B) → B runs before A regardless of priority.
// ----------------------------------------------------------------------

func TestOrder_After_SwapsTwoSubscribers(t *testing.T) {
	bus, _ := newTestBus(t)
	r := &recorder{}

	// A is registered first AND has lower priority — under pure priority
	// it would run first. The `after: [B]` constraint flips that.
	if _, err := bus.RegisterActionWithOptions("h",
		RegisterOptions{Source: "A", Priority: 10, After: []string{"B"}},
		r.handler("A")); err != nil {
		t.Fatal(err)
	}
	if _, err := bus.RegisterActionWithOptions("h",
		RegisterOptions{Source: "B", Priority: 20},
		r.handler("B")); err != nil {
		t.Fatal(err)
	}

	if err := bus.Do(context.Background(), "h"); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if got := r.joined(); got != "B,A" {
		t.Errorf("order: got %q want B,A", got)
	}
}

// TestOrder_Before mirrors the After case in the symmetric form: A
// declares "before: [B]" and we expect A,B regardless of priority.
func TestOrder_Before_SwapsTwoSubscribers(t *testing.T) {
	bus, _ := newTestBus(t)
	r := &recorder{}

	if _, err := bus.RegisterActionWithOptions("h",
		RegisterOptions{Source: "B", Priority: 10},
		r.handler("B")); err != nil {
		t.Fatal(err)
	}
	if _, err := bus.RegisterActionWithOptions("h",
		RegisterOptions{Source: "A", Priority: 20, Before: []string{"B"}},
		r.handler("A")); err != nil {
		t.Fatal(err)
	}

	if err := bus.Do(context.Background(), "h"); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if got := r.joined(); got != "A,B" {
		t.Errorf("order: got %q want A,B", got)
	}
}

// ----------------------------------------------------------------------
// Diamond: A → {B, C} → D. Both topological linearizations are valid:
// the test asserts the partial-order constraints (A first, D last, B/C
// in either middle slot) rather than a single concrete sequence, because
// pinning a specific tie-break would over-specify the contract.
// ----------------------------------------------------------------------

func TestOrder_Diamond_RespectsPartialOrder(t *testing.T) {
	bus, _ := newTestBus(t)
	r := &recorder{}

	// A runs first; B and C both run after A; D runs after both B and C.
	if _, err := bus.RegisterActionWithOptions("h",
		RegisterOptions{Source: "B", After: []string{"A"}},
		r.handler("B")); err != nil {
		t.Fatal(err)
	}
	if _, err := bus.RegisterActionWithOptions("h",
		RegisterOptions{Source: "D", After: []string{"B", "C"}},
		r.handler("D")); err != nil {
		t.Fatal(err)
	}
	if _, err := bus.RegisterActionWithOptions("h",
		RegisterOptions{Source: "C", After: []string{"A"}},
		r.handler("C")); err != nil {
		t.Fatal(err)
	}
	if _, err := bus.RegisterActionWithOptions("h",
		RegisterOptions{Source: "A"},
		r.handler("A")); err != nil {
		t.Fatal(err)
	}

	if err := bus.Do(context.Background(), "h"); err != nil {
		t.Fatalf("Do: %v", err)
	}
	got := r.snapshot()
	if len(got) != 4 {
		t.Fatalf("expected 4 entries, got %v", got)
	}
	idx := func(tag string) int {
		for i, s := range got {
			if s == tag {
				return i
			}
		}
		return -1
	}
	a, b, c, d := idx("A"), idx("B"), idx("C"), idx("D")
	if a == -1 || b == -1 || c == -1 || d == -1 {
		t.Fatalf("missing subscriber: %v", got)
	}
	if a > b || a > c {
		t.Errorf("A must precede B and C, got %v", got)
	}
	if b > d || c > d {
		t.Errorf("D must come after B and C, got %v", got)
	}
}

// ----------------------------------------------------------------------
// Cycle: A.after(B) + B.after(A) → ErrCycle at the second registration.
// ----------------------------------------------------------------------

func TestOrder_Cycle_RejectedAtRegistration(t *testing.T) {
	bus, _ := newTestBus(t)

	noop := func(ctx context.Context, args ...any) error { return nil }

	// First subscriber registers fine on its own — there is nothing to
	// cycle against yet.
	off1, err := bus.RegisterActionWithOptions("c",
		RegisterOptions{Source: "A", After: []string{"B"}}, noop)
	if err != nil {
		t.Fatalf("first reg: %v", err)
	}
	t.Cleanup(off1)

	// Second subscriber closes the loop. Should be rejected.
	off2, err := bus.RegisterActionWithOptions("c",
		RegisterOptions{Source: "B", After: []string{"A"}}, noop)
	if err == nil {
		t.Fatal("expected ErrCycle on closing the loop")
	}
	if !errors.Is(err, ErrCycle) {
		t.Errorf("error: got %v, want errors.Is(_, ErrCycle)", err)
	}
	// The returned unsubscribe must be a non-nil no-op so a deferred
	// caller doesn't blow up.
	if off2 == nil {
		t.Error("rejected registration returned a nil unsubscribe")
	}
	off2() // must not panic

	// And the existing A registration should still be live — a rejected
	// registration must not corrupt the chain.
	r := &recorder{}
	off, err := bus.RegisterActionWithOptions("c",
		RegisterOptions{Source: "tracer"}, r.handler("tracer"))
	if err != nil {
		t.Fatal(err)
	}
	defer off()
	if err := bus.Do(context.Background(), "c"); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if r.joined() != "tracer" {
		t.Errorf("post-cycle Do: got %q want tracer", r.joined())
	}
}

// TestOrder_LongerCycle exercises a 3-node cycle (A → B → C → A) so the
// detection isn't tied to the 2-node special case.
func TestOrder_LongerCycle_Rejected(t *testing.T) {
	bus, _ := newTestBus(t)
	noop := func(ctx context.Context, args ...any) error { return nil }

	_, err := bus.RegisterActionWithOptions("c3",
		RegisterOptions{Source: "A", Before: []string{"B"}}, noop)
	if err != nil {
		t.Fatal(err)
	}
	_, err = bus.RegisterActionWithOptions("c3",
		RegisterOptions{Source: "B", Before: []string{"C"}}, noop)
	if err != nil {
		t.Fatal(err)
	}
	_, err = bus.RegisterActionWithOptions("c3",
		RegisterOptions{Source: "C", Before: []string{"A"}}, noop)
	if !errors.Is(err, ErrCycle) {
		t.Errorf("expected ErrCycle on 3-node cycle, got %v", err)
	}
}

// ----------------------------------------------------------------------
// Unknown constraint target: subscriber registers, constraint drops,
// warning is logged.
// ----------------------------------------------------------------------

func TestOrder_UnknownConstraintTarget_DropsAndWarns(t *testing.T) {
	bus, buf := newTestBus(t)
	r := &recorder{}

	off, err := bus.RegisterActionWithOptions("h",
		RegisterOptions{Source: "A", After: []string{"ghost"}}, r.handler("A"))
	if err != nil {
		t.Fatalf("reg: %v", err)
	}
	defer off()

	if err := bus.Do(context.Background(), "h"); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if r.joined() != "A" {
		t.Errorf("subscriber should still run: got %q", r.joined())
	}
	// Expect a warning naming the missing target. The log line shape is
	// "hook ordering constraint references unknown source" → keep this
	// substring stable.
	if !strings.Contains(buf.String(), "unknown source") {
		t.Errorf("expected warning about unknown source, got: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "ghost") {
		t.Errorf("warning should name the missing target, got: %q", buf.String())
	}
}

// ----------------------------------------------------------------------
// Self-constraint: subscriber X with `after: [X]` or `before: [X]` is
// rejected at registration time as ErrSelfConstraint.
// ----------------------------------------------------------------------

func TestOrder_SelfConstraint_Rejected(t *testing.T) {
	bus, _ := newTestBus(t)
	noop := func(ctx context.Context, args ...any) error { return nil }

	_, err := bus.RegisterActionWithOptions("h",
		RegisterOptions{Source: "X", After: []string{"X"}}, noop)
	if !errors.Is(err, ErrSelfConstraint) {
		t.Errorf("after-self: got %v want ErrSelfConstraint", err)
	}
	_, err = bus.RegisterActionWithOptions("h",
		RegisterOptions{Source: "X", Before: []string{"X"}}, noop)
	if !errors.Is(err, ErrSelfConstraint) {
		t.Errorf("before-self: got %v want ErrSelfConstraint", err)
	}
}

// ----------------------------------------------------------------------
// Priority tie-break in the absence of constraints: equal priority +
// no before/after → registration order is preserved.
// ----------------------------------------------------------------------

func TestOrder_PriorityTieBreak_RegistrationOrder(t *testing.T) {
	bus, _ := newTestBus(t)
	r := &recorder{}

	for _, tag := range []string{"first", "second", "third", "fourth"} {
		tag := tag
		if _, err := bus.RegisterActionWithOptions("h",
			RegisterOptions{Source: tag, Priority: 10}, r.handler(tag)); err != nil {
			t.Fatal(err)
		}
	}
	if err := bus.Do(context.Background(), "h"); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if got, want := r.joined(), "first,second,third,fourth"; got != want {
		t.Errorf("order: got %q want %q", got, want)
	}
}

// ----------------------------------------------------------------------
// Concurrent Subscribe + Apply: ordering math is race-clean.
// ----------------------------------------------------------------------

func TestOrder_ConcurrentRegisterAndFire(t *testing.T) {
	bus, _ := newTestBus(t)

	// Seed a stable chain of constrained subscribers so every dispatch
	// runs through the topo path, not just the no-constraints fast path.
	if _, err := bus.RegisterActionWithOptions("c",
		RegisterOptions{Source: "seed-A"},
		func(ctx context.Context, args ...any) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := bus.RegisterActionWithOptions("c",
		RegisterOptions{Source: "seed-B", After: []string{"seed-A"}},
		func(ctx context.Context, args ...any) error { return nil }); err != nil {
		t.Fatal(err)
	}

	var fired atomic.Int64
	if _, err := bus.RegisterActionWithOptions("c",
		RegisterOptions{Source: "counter", After: []string{"seed-B"}},
		func(ctx context.Context, args ...any) error {
			fired.Add(1)
			return nil
		}); err != nil {
		t.Fatal(err)
	}

	const (
		registrars = 8
		firers     = 8
		ticks      = 200
	)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < registrars; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				// Each registrar churns a unique Source so the topo
				// graph keeps changing shape; the constraint references
				// "seed-A" which always exists, so no cycle is possible.
				src := "tmp"
				off, err := bus.RegisterActionWithOptions("c",
					RegisterOptions{Source: src, After: []string{"seed-A"}},
					func(ctx context.Context, args ...any) error { return nil })
				if err != nil {
					t.Errorf("registrar %d: %v", i, err)
					return
				}
				off()
			}
		}(i)
	}
	for i := 0; i < firers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < ticks; j++ {
				_ = bus.Do(context.Background(), "c")
			}
		}()
	}

	// Wait for the firers to finish their fixed work; the race detector
	// is the actual assertion here.
	doneFirers := make(chan struct{})
	go func() {
		// rejoining the firers via a sub-WG would be cleaner, but a
		// simple "fired ≥ expected" poll keeps the test small.
		for fired.Load() < int64(firers*ticks) {
		}
		close(doneFirers)
	}()
	<-doneFirers
	close(stop)
	wg.Wait()

	if fired.Load() < int64(firers*ticks) {
		t.Errorf("fired count: got %d want >= %d", fired.Load(), firers*ticks)
	}
}

// ----------------------------------------------------------------------
// Direct topoSort unit tests — exercise the algorithm without going
// through the Bus layer, which makes failures easier to localize.
// ----------------------------------------------------------------------

func TestTopoSort_Empty(t *testing.T) {
	got, err := topoSort(nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len: got %d want 0", len(got))
	}
}

func TestTopoSort_PassThroughWhenNoConstraints(t *testing.T) {
	subs := []registration{
		{token: 1, source: "a"},
		{token: 2, source: "b"},
		{token: 3, source: "c"},
	}
	got, err := topoSort(subs)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	want := []string{"a", "b", "c"}
	for i, r := range got {
		if r.source != want[i] {
			t.Errorf("pos %d: got %q want %q", i, r.source, want[i])
		}
	}
}

func TestTopoSort_AfterRespected(t *testing.T) {
	subs := []registration{
		{token: 1, source: "X", after: []string{"Y"}},
		{token: 2, source: "Y"},
	}
	got, err := topoSort(subs)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got[0].source != "Y" || got[1].source != "X" {
		t.Errorf("order: got %v want [Y X]", []string{got[0].source, got[1].source})
	}
}

func TestTopoSort_Cycle(t *testing.T) {
	subs := []registration{
		{token: 1, source: "A", after: []string{"B"}},
		{token: 2, source: "B", after: []string{"A"}},
	}
	_, err := topoSort(subs)
	if !errors.Is(err, ErrCycle) {
		t.Fatalf("err: got %v want errors.Is(_, ErrCycle)", err)
	}
	// The error message should name both participants for debuggability.
	msg := err.Error()
	if !strings.Contains(msg, "A") || !strings.Contains(msg, "B") {
		t.Errorf("cycle error should mention both participants, got: %q", msg)
	}
}

func TestTopoSort_DeterministicTieBreak(t *testing.T) {
	// Two unconstrained subscribers with the same priority — the input
	// order (priority + regOrder) is what topoSort should preserve.
	// Repeat several times to be a little more confident it's not flaky.
	for i := 0; i < 20; i++ {
		subs := []registration{
			{token: 1, source: "first", priority: 10, regOrder: 1},
			{token: 2, source: "second", priority: 10, regOrder: 2},
			{token: 3, source: "third", priority: 10, regOrder: 3},
		}
		got, err := topoSort(subs)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if got[0].source != "first" || got[1].source != "second" || got[2].source != "third" {
			t.Errorf("run %d: order not deterministic: %v %v %v",
				i, got[0].source, got[1].source, got[2].source)
		}
	}
}

func TestValidateSelfConstraints(t *testing.T) {
	if err := validateSelfConstraints(registration{source: "x", after: []string{"x"}}); !errors.Is(err, ErrSelfConstraint) {
		t.Errorf("after-self: got %v", err)
	}
	if err := validateSelfConstraints(registration{source: "x", before: []string{"x"}}); !errors.Is(err, ErrSelfConstraint) {
		t.Errorf("before-self: got %v", err)
	}
	if err := validateSelfConstraints(registration{source: "x", after: []string{"y"}}); err != nil {
		t.Errorf("legit constraint: %v", err)
	}
	if err := validateSelfConstraints(registration{source: "", after: []string{""}}); err != nil {
		// Empty Source can't self-constrain (the lookup table doesn't
		// key on empty strings). We don't reject this case.
		t.Errorf("empty source: %v", err)
	}
}

// ----------------------------------------------------------------------
// Filter variant: confirm RegisterFilterWithOptions wires into the topo
// path too. We only need a small smoke test — the order logic is shared.
// ----------------------------------------------------------------------

func TestOrder_FilterWithOptions(t *testing.T) {
	bus, _ := newTestBus(t)

	// Filter B appends "-B" after A appends "-A". Constraint forces A first.
	if _, err := bus.RegisterFilterWithOptions("f",
		RegisterOptions{Source: "B", Priority: 10, After: []string{"A"}},
		func(ctx context.Context, v any, args ...any) (any, error) {
			return v.(string) + "-B", nil
		}); err != nil {
		t.Fatal(err)
	}
	if _, err := bus.RegisterFilterWithOptions("f",
		RegisterOptions{Source: "A", Priority: 20},
		func(ctx context.Context, v any, args ...any) (any, error) {
			return v.(string) + "-A", nil
		}); err != nil {
		t.Fatal(err)
	}

	got, err := bus.ApplyFilters(context.Background(), "f", "start")
	if err != nil {
		t.Fatalf("ApplyFilters: %v", err)
	}
	if got != "start-A-B" {
		t.Errorf("value: got %q want start-A-B", got)
	}
}

// ----------------------------------------------------------------------
// Async variant smoke test: verify the *WithOptions surface for async
// actions is wired (and that constraints affect the dispatch order even
// though async handlers race in goroutines — they're still START-ordered
// per the snapshot the chain produces).
// ----------------------------------------------------------------------

func TestOrder_AsyncWithOptions(t *testing.T) {
	bus, _ := newTestBus(t)
	var started atomic.Int32
	// Just confirm registration succeeds and dispatch fires; race
	// detector covers the rest.
	off, err := bus.RegisterAsyncWithOptions("a",
		RegisterOptions{Source: "x", Priority: 10},
		func(ctx context.Context, args ...any) error {
			started.Add(1)
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	defer off()
	if err := bus.Do(context.Background(), "a"); err != nil {
		t.Fatalf("Do: %v", err)
	}
	bus.Wait()
	if started.Load() != 1 {
		t.Errorf("async handler runs: got %d want 1", started.Load())
	}
}

// ----------------------------------------------------------------------
// Cache invalidation: after Unsubscribe, the topo order is refreshed so
// a stale subscriber doesn't keep firing.
// ----------------------------------------------------------------------

func TestOrder_UnsubscribeRefreshesOrder(t *testing.T) {
	bus, _ := newTestBus(t)
	r := &recorder{}

	if _, err := bus.RegisterActionWithOptions("h",
		RegisterOptions{Source: "A"}, r.handler("A")); err != nil {
		t.Fatal(err)
	}
	offB, err := bus.RegisterActionWithOptions("h",
		RegisterOptions{Source: "B", After: []string{"A"}}, r.handler("B"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bus.RegisterActionWithOptions("h",
		RegisterOptions{Source: "C", After: []string{"B"}}, r.handler("C")); err != nil {
		t.Fatal(err)
	}

	if err := bus.Do(context.Background(), "h"); err != nil {
		t.Fatalf("Do 1: %v", err)
	}
	if got := r.joined(); got != "A,B,C" {
		t.Errorf("Do 1 order: got %q want A,B,C", got)
	}

	// Drop B; A and C should still fire (C's `after: [B]` constraint
	// becomes dangling and gets silently dropped at the next order).
	offB()
	r.mu.Lock()
	r.order = nil
	r.mu.Unlock()
	if err := bus.Do(context.Background(), "h"); err != nil {
		t.Fatalf("Do 2: %v", err)
	}
	got := r.joined()
	if got != "A,C" {
		t.Errorf("Do 2 order: got %q want A,C", got)
	}
}
