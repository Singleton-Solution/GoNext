package taskspec

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// noopHandler is a zero-effect handler reused across tests where we
// only care about the registry shape, not the consumer side.
func noopHandler(_ context.Context, _ []byte) error { return nil }

// TestRegister_RoundTrip is the happy-path smoke test: register a
// spec, fetch it back, observe the same scalar fields. The Handler
// pointer is intentionally not compared (function values aren't ==
// in Go); the contract is "the spec we put in is the spec we get
// back", which the scalar comparison plus a handler-presence check
// captures.
func TestRegister_RoundTrip(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	spec := TaskSpec{
		Name:     "demo.task",
		Queue:    "default",
		MaxRetry: 3,
		Timeout:  5 * time.Second,
		Handler:  noopHandler,
	}
	if err := r.Register(spec); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, ok := r.Get("demo.task")
	if !ok {
		t.Fatal("Get: expected spec present after Register")
	}
	if got.Name != spec.Name || got.Queue != spec.Queue ||
		got.MaxRetry != spec.MaxRetry || got.Timeout != spec.Timeout {
		t.Errorf("Get: scalar fields differ: got %+v, want %+v", got, spec)
	}
	if got.Handler == nil {
		t.Error("Get: Handler dropped")
	}
}

// TestRegister_EmptyNameRejected pins the ErrEmptyName contract: an
// empty Name would route into asynq's default and produce un-handleable
// tasks, so we reject at register time.
func TestRegister_EmptyNameRejected(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	err := r.Register(TaskSpec{Name: ""})
	if !errors.Is(err, ErrEmptyName) {
		t.Fatalf("Register(empty): got %v, want ErrEmptyName", err)
	}
}

// TestRegister_DuplicateErrors covers the first-writer-wins contract.
// The second Register call must return ErrAlreadyRegistered, and the
// originally-registered spec must survive intact.
func TestRegister_DuplicateErrors(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	first := TaskSpec{Name: "dup", Queue: "first"}
	second := TaskSpec{Name: "dup", Queue: "second"}

	if err := r.Register(first); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	err := r.Register(second)
	if !errors.Is(err, ErrAlreadyRegistered) {
		t.Fatalf("second Register: got %v, want ErrAlreadyRegistered", err)
	}

	got, ok := r.Get("dup")
	if !ok {
		t.Fatal("Get: expected spec to still be present")
	}
	if got.Queue != "first" {
		t.Errorf("Get: expected first writer to win, got Queue=%q", got.Queue)
	}
}

// TestGet_MissingReturnsFalse exercises the negative branch of Get;
// the bool is the only honest answer when the name is unknown.
func TestGet_MissingReturnsFalse(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	_, ok := r.Get("missing")
	if ok {
		t.Fatal("Get(missing): expected ok=false")
	}
}

// TestNames_SortedDeterministic verifies the documented contract that
// Names returns names sorted lexicographically. Dispatch and admin UI
// consumers rely on this ordering.
func TestNames_SortedDeterministic(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	for _, n := range []string{"c.task", "a.task", "b.task"} {
		if err := r.Register(TaskSpec{Name: n}); err != nil {
			t.Fatalf("Register(%q): %v", n, err)
		}
	}
	got := r.Names()
	want := []string{"a.task", "b.task", "c.task"}
	if len(got) != len(want) {
		t.Fatalf("Names: got %d, want %d (%v)", len(got), len(want), got)
	}
	for i, n := range got {
		if n != want[i] {
			t.Errorf("Names[%d] = %q, want %q", i, n, want[i])
		}
	}
}

// TestNames_ReturnsCopy verifies that mutating the returned slice does
// not corrupt the registry. Dispatch may filter the result in-place.
func TestNames_ReturnsCopy(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	_ = r.Register(TaskSpec{Name: "a.task"})
	out := r.Names()
	out[0] = "mutated"
	again := r.Names()
	if again[0] != "a.task" {
		t.Errorf("registry mutated via Names() result: %v", again)
	}
}

// TestHas mirrors Get's negative branch through the convenience wrapper.
func TestHas(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	_ = r.Register(TaskSpec{Name: "present"})
	if !r.Has("present") {
		t.Error("Has(present): got false, want true")
	}
	if r.Has("absent") {
		t.Error("Has(absent): got true, want false")
	}
}

// TestConcurrentRegisterAndNames is the race-detector workout: many
// Register goroutines (each writing a distinct name) alongside many
// Names readers. With sync.RWMutex correctly applied, `go test -race`
// reports no races, and every Name we registered is observable once
// the writers finish.
func TestConcurrentRegisterAndNames(t *testing.T) {
	t.Parallel()
	r := NewRegistry()

	const writers = 32
	const readers = 16

	var wg sync.WaitGroup
	wg.Add(writers + readers)

	for i := 0; i < writers; i++ {
		name := string(rune('A' + i))
		go func() {
			defer wg.Done()
			if err := r.Register(TaskSpec{Name: name}); err != nil {
				// Each writer uses a unique name; only an unexpected
				// error would surface here. ErrAlreadyRegistered would
				// indicate a regression in the uniqueness assumption.
				t.Errorf("Register(%q): %v", name, err)
			}
		}()
	}

	for i := 0; i < readers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = r.Names()
				_, _ = r.Get("anything")
			}
		}()
	}
	wg.Wait()

	for i := 0; i < writers; i++ {
		name := string(rune('A' + i))
		if !r.Has(name) {
			t.Errorf("after concurrent Register, missing name %q", name)
		}
	}
}

// TestConcurrentDuplicateRegister exercises the trickier race: many
// goroutines all try to Register the same Name. Exactly one must
// succeed; every other must observe ErrAlreadyRegistered.
func TestConcurrentDuplicateRegister(t *testing.T) {
	t.Parallel()
	r := NewRegistry()

	const N = 64
	var (
		wg       sync.WaitGroup
		successC int
		errC     int
		mu       sync.Mutex
	)
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			err := r.Register(TaskSpec{Name: "contended"})
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				successC++
				return
			}
			if errors.Is(err, ErrAlreadyRegistered) {
				errC++
				return
			}
			t.Errorf("unexpected error: %v", err)
		}()
	}
	wg.Wait()

	if successC != 1 {
		t.Errorf("expected exactly 1 success, got %d", successC)
	}
	if errC != N-1 {
		t.Errorf("expected %d duplicates, got %d", N-1, errC)
	}
}

// TestDefault_Singleton asserts that Default() returns the same
// process-wide *Registry on every call. The contract matters because
// production wiring relies on init-time Register into Default() being
// visible to later Get() / Dispatch().
func TestDefault_Singleton(t *testing.T) {
	t.Parallel()
	if Default() != Default() {
		t.Error("Default(): returned different instances on repeated call")
	}
}

// TestResetDefaultForTest is a meta-test for the test helper: a fresh
// Default() must not carry over scratch entries from before the reset.
func TestResetDefaultForTest(t *testing.T) {
	// Mutates the singleton — do not run in parallel.
	before := Default()
	if err := before.Register(TaskSpec{Name: "scratch.reset.task"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if !before.Has("scratch.reset.task") {
		t.Fatal("precondition: scratch task should be present")
	}
	resetDefaultForTest()
	if Default() == before {
		t.Error("reset: Default() returned the old instance")
	}
	if Default().Has("scratch.reset.task") {
		t.Error("reset: scratch task leaked into fresh registry")
	}
}
