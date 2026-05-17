package taskspec

import (
	"context"
	"errors"
	"testing"

	"github.com/hibiken/asynq"
)

// TestDispatch_WiresHandlers walks the happy path: every spec in the
// registry whose Handler is non-nil ends up on the mux under the
// declared Name. We assert on the mux by manufacturing a Task with the
// declared name and verifying ProcessTask routes to our handler.
//
// ProcessTask is the cheapest end-to-end probe asynq exposes: it
// performs the same name-to-handler lookup the real worker does, so a
// successful round-trip proves the mux contains the right pattern.
func TestDispatch_WiresHandlers(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()

	calls := map[string][]byte{}
	makeHandler := func(name string) func(context.Context, []byte) error {
		return func(_ context.Context, p []byte) error {
			calls[name] = p
			return nil
		}
	}
	specs := []TaskSpec{
		{Name: "alpha.task", Queue: "default", Handler: makeHandler("alpha.task")},
		{Name: "beta.task", Queue: "low", Handler: makeHandler("beta.task")},
	}
	for _, s := range specs {
		if err := reg.Register(s); err != nil {
			t.Fatalf("Register(%q): %v", s.Name, err)
		}
	}

	mux := asynq.NewServeMux()
	wired := Dispatch(mux, reg)
	if len(wired) != 2 {
		t.Fatalf("Dispatch wired %d, want 2 (%v)", len(wired), wired)
	}
	// Sorted order — same contract as Names().
	if wired[0] != "alpha.task" || wired[1] != "beta.task" {
		t.Errorf("Dispatch order: %v", wired)
	}

	for _, name := range wired {
		payload := []byte(`{"hello":"` + name + `"}`)
		task := asynq.NewTask(name, payload)
		if err := mux.ProcessTask(context.Background(), task); err != nil {
			t.Errorf("ProcessTask(%q): %v", name, err)
		}
		got, ok := calls[name]
		if !ok {
			t.Errorf("handler for %q not invoked", name)
			continue
		}
		if string(got) != string(payload) {
			t.Errorf("handler for %q got %q, want %q", name, got, payload)
		}
	}
}

// TestDispatch_SkipsNilHandler covers the producer-only case: a spec
// with no Handler is fine for Enqueue (the consumer lives elsewhere)
// but Dispatch must NOT panic on it. The skipped name does not
// appear in the wired list.
func TestDispatch_SkipsNilHandler(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	_ = reg.Register(TaskSpec{Name: "producer.only", Handler: nil})
	_ = reg.Register(TaskSpec{Name: "full.spec", Handler: noopHandler})

	mux := asynq.NewServeMux()
	wired := Dispatch(mux, reg)
	if len(wired) != 1 || wired[0] != "full.spec" {
		t.Fatalf("Dispatch: got %v, want [full.spec]", wired)
	}

	// The producer-only task must not match the mux either —
	// asynq.ServeMux returns a non-nil error when no handler is
	// registered for a typename.
	task := asynq.NewTask("producer.only", nil)
	err := mux.ProcessTask(context.Background(), task)
	if err == nil {
		t.Error("expected producer.only to have no handler on mux")
	}
}

// TestDispatch_NilArgs documents the defensive contract: nil mux or
// nil registry returns nil rather than panicking. The cost of
// defending is one branch; the benefit is that a wiring bug surfaces
// as "no tasks wired" rather than a panic at boot.
func TestDispatch_NilArgs(t *testing.T) {
	t.Parallel()
	if got := Dispatch(nil, NewRegistry()); got != nil {
		t.Errorf("Dispatch(nil mux): got %v, want nil", got)
	}
	if got := Dispatch(asynq.NewServeMux(), nil); got != nil {
		t.Errorf("Dispatch(nil registry): got %v, want nil", got)
	}
}

// TestDispatch_HandlerErrorPropagates pins the contract that the
// adapter does not swallow handler errors — the worker side needs
// the error to drive asynq's retry path.
func TestDispatch_HandlerErrorPropagates(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	sentinel := errors.New("boom")
	_ = reg.Register(TaskSpec{
		Name:    "fail.task",
		Handler: func(_ context.Context, _ []byte) error { return sentinel },
	})

	mux := asynq.NewServeMux()
	Dispatch(mux, reg)

	err := mux.ProcessTask(context.Background(), asynq.NewTask("fail.task", nil))
	if !errors.Is(err, sentinel) {
		t.Errorf("got %v, want sentinel boom", err)
	}
}
