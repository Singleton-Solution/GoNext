package capabilities

import (
	"errors"
	"sync"
	"testing"
)

// TestRegister_RoundTrip is the happy-path smoke test: register a def,
// fetch it back, observe the same fields. Anchors the basic contract
// before the more interesting concurrency tests run.
func TestRegister_RoundTrip(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	def := CapabilityDef{
		ID:          "test.read",
		Description: "Read test things.",
		Resource:    "test",
		Action:      "read",
	}
	if err := r.Register(def); err != nil {
		t.Fatalf("Register: unexpected error: %v", err)
	}
	got, ok := r.Get("test.read")
	if !ok {
		t.Fatal("Get: expected def to be present after Register")
	}
	if got != def {
		t.Errorf("Get: got %+v, want %+v", got, def)
	}
}

// TestRegister_EmptyIDRejected pins the ErrEmptyID contract. Empty
// IDs would make every Allowed() check return false in a way that's
// nearly impossible to trace, so we reject them at register time.
func TestRegister_EmptyIDRejected(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	err := r.Register(CapabilityDef{ID: ""})
	if !errors.Is(err, ErrEmptyID) {
		t.Fatalf("Register(empty ID): got %v, want ErrEmptyID", err)
	}
}

// TestRegister_DuplicateErrors covers the "first writer wins" contract.
// A duplicate registration returns ErrAlreadyRegistered and the
// original def remains in the registry — the second call does NOT
// overwrite.
func TestRegister_DuplicateErrors(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	first := CapabilityDef{ID: "dup", Description: "first"}
	second := CapabilityDef{ID: "dup", Description: "second"}

	if err := r.Register(first); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	err := r.Register(second)
	if !errors.Is(err, ErrAlreadyRegistered) {
		t.Fatalf("second Register: got %v, want ErrAlreadyRegistered", err)
	}

	got, ok := r.Get("dup")
	if !ok {
		t.Fatal("Get: expected def to still be present")
	}
	if got.Description != "first" {
		t.Errorf("Get: expected first def to win, got Description=%q", got.Description)
	}
}

// TestGet_MissingReturnsFalse exercises the negative branch of Get.
// The zero-value CapabilityDef is fine; the bool is the source of
// truth.
func TestGet_MissingReturnsFalse(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	_, ok := r.Get("does.not.exist")
	if ok {
		t.Fatal("Get(nonexistent): expected ok=false")
	}
}

// TestList_SortedDeterministic verifies the documented contract that
// List returns defs sorted by ID. A consumer (admin UI, audit metadata)
// relying on stable order shouldn't have to sort again.
func TestList_SortedDeterministic(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	ids := []string{"c", "a", "b"}
	for _, id := range ids {
		if err := r.Register(CapabilityDef{ID: id}); err != nil {
			t.Fatalf("Register(%q): %v", id, err)
		}
	}
	got := r.List()
	if len(got) != 3 {
		t.Fatalf("List: got %d defs, want 3", len(got))
	}
	want := []string{"a", "b", "c"}
	for i, def := range got {
		if def.ID != want[i] {
			t.Errorf("List[%d].ID = %q, want %q", i, def.ID, want[i])
		}
	}
}

// TestList_ReturnsCopy verifies that mutating the returned slice does
// not affect the registry. Important because admin code may sort or
// filter the result in-place.
func TestList_ReturnsCopy(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	_ = r.Register(CapabilityDef{ID: "a"})
	out := r.List()
	out[0].ID = "mutated"
	got, _ := r.Get("a")
	if got.ID != "a" {
		t.Errorf("registry mutated via List() result: got %q, want %q", got.ID, "a")
	}
}

// TestDefault_PreSeeded confirms that the package-level Default()
// returns a registry with every documented built-in present. The list
// is the wire-format contract for plugin manifests — accidentally
// dropping one would silently break plugins in the field.
func TestDefault_PreSeeded(t *testing.T) {
	t.Parallel()
	want := []string{
		"posts.read",
		"posts.write",
		"users.read",
		"email.send",
		"http.fetch",
		"kv.read",
		"kv.write",
		"hooks.subscribe",
		"jobs.enqueue",
	}
	r := Default()
	for _, id := range want {
		if !r.Has(id) {
			t.Errorf("Default registry missing built-in cap %q", id)
		}
	}
}

// TestDefault_SensitiveFlagsSet ensures the Sensitive bit is wired up
// correctly for the caps that need it (outbound effects). A regression
// here would silently downgrade the install-confirmation UX.
func TestDefault_SensitiveFlagsSet(t *testing.T) {
	t.Parallel()
	r := Default()
	for _, id := range []string{"email.send", "http.fetch"} {
		def, ok := r.Get(id)
		if !ok {
			t.Fatalf("Default registry missing %q", id)
		}
		if !def.Sensitive {
			t.Errorf("%s: expected Sensitive=true, got false", id)
		}
	}
	// Spot check a non-sensitive cap to guard against the easy bug
	// where every cap gets flagged.
	postsRead, _ := r.Get("posts.read")
	if postsRead.Sensitive {
		t.Error("posts.read: expected Sensitive=false")
	}
}

// TestConcurrentRegisterAndList is the race-detector workout. We run
// many Register goroutines (each writing a distinct ID) alongside many
// List/Get readers. With sync.RWMutex correctly applied, `go test
// -race` reports no races and every ID we registered is observable
// once the writers finish.
func TestConcurrentRegisterAndList(t *testing.T) {
	t.Parallel()
	r := NewRegistry()

	const writers = 32
	const readers = 16

	var wg sync.WaitGroup
	wg.Add(writers + readers)

	// Writers: each registers one unique ID. No retries — we want any
	// race to manifest as a missing entry or a panic.
	for i := 0; i < writers; i++ {
		id := string(rune('A' + i))
		go func() {
			defer wg.Done()
			if err := r.Register(CapabilityDef{ID: id}); err != nil {
				// First-writer-wins: only ErrAlreadyRegistered is
				// acceptable here, and only if two writers somehow
				// hit the same ID — which can't happen with our
				// unique IDs, but assert it explicitly so a flaky
				// regression is caught.
				if !errors.Is(err, ErrAlreadyRegistered) {
					t.Errorf("Register(%q): unexpected %v", id, err)
				}
			}
		}()
	}

	// Readers: hammer List and Get from many goroutines. We don't
	// assert on the contents (the writers may not have finished) —
	// we're entirely after race-detector signal.
	for i := 0; i < readers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = r.List()
				_, _ = r.Get("anything")
			}
		}()
	}

	wg.Wait()

	// After all writers finish, every unique ID must be present.
	for i := 0; i < writers; i++ {
		id := string(rune('A' + i))
		if !r.Has(id) {
			t.Errorf("after concurrent Register, missing id %q", id)
		}
	}
}

// TestConcurrentDuplicateRegister exercises the trickier race: many
// goroutines all try to Register the same ID simultaneously. Exactly
// one must succeed; every other must observe ErrAlreadyRegistered.
// Anchors the first-writer-wins contract under contention.
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
			err := r.Register(CapabilityDef{ID: "contended"})
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
		t.Errorf("expected exactly 1 successful Register, got %d", successC)
	}
	if errC != N-1 {
		t.Errorf("expected %d ErrAlreadyRegistered, got %d", N-1, errC)
	}
}

// TestResetDefaultForTest is meta — it exercises the test helper that
// the rest of the suite depends on. Without this the helper could
// silently rot.
func TestResetDefaultForTest(t *testing.T) {
	// Don't run in parallel: this mutates a process-wide singleton.
	original := Default()
	_ = original.Register(CapabilityDef{ID: "test.scratch.cap"})
	if !original.Has("test.scratch.cap") {
		t.Fatal("precondition: scratch cap should be registered")
	}
	resetDefaultForTest()
	if Default() == original {
		t.Error("resetDefaultForTest: Default() still returns the old registry")
	}
	if Default().Has("test.scratch.cap") {
		t.Error("resetDefaultForTest: scratch cap leaked into fresh registry")
	}
	// Built-ins must come back.
	if !Default().Has("posts.read") {
		t.Error("resetDefaultForTest: fresh registry missing built-ins")
	}
}
