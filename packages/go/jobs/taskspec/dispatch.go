package taskspec

import (
	"context"

	"github.com/hibiken/asynq"
)

// Dispatch walks every spec in registry and registers its Handler onto
// mux under the spec's Name. Specs with a nil Handler are skipped (the
// producer-only case) — Dispatch does not panic, so a missing handler
// surfaces as "task not handled" at the consumer rather than a boot-
// time crash, which is easier to triage when the handler lives in a
// package that hasn't been imported yet.
//
// The wired adapter unwraps the *asynq.Task into the (ctx, []byte)
// signature TaskSpec.Handler exposes. This keeps the asynq dependency
// from leaking into every package that declares a spec — handlers stay
// agnostic, and the adapter lives in one place.
//
// Dispatch is idempotent at the asynq layer for a given mux: registering
// the same pattern twice on an *asynq.ServeMux panics inside asynq
// itself. Callers that build a mux from multiple registries should
// arrange the merge upstream rather than calling Dispatch twice with
// overlapping names.
//
// Returns the slice of names actually wired (in the registry's sorted
// order, with nil-Handler specs filtered out). Useful for diagnostics
// and for tests that want to assert "every spec I expect is on the mux".
//
// Safe for concurrent reads of the registry; not safe for concurrent
// mutation of mux (that's asynq.ServeMux's contract, not ours).
func Dispatch(mux *asynq.ServeMux, registry *Registry) []string {
	if mux == nil || registry == nil {
		return nil
	}
	names := registry.Names()
	wired := make([]string, 0, len(names))
	for _, name := range names {
		spec, ok := registry.Get(name)
		if !ok {
			// Race with concurrent Register/delete that we don't have —
			// the registry has no delete API. Defensive nonetheless;
			// the cost is one Get, the benefit is that a future delete
			// can't crash the dispatch.
			continue
		}
		if spec.Handler == nil {
			continue
		}
		mux.Handle(spec.Name, adapt(spec))
		wired = append(wired, spec.Name)
	}
	return wired
}

// adapt wraps a TaskSpec.Handler in the asynq.Handler interface. We
// pull the payload bytes off the *asynq.Task once and pass them to the
// declared handler — every spec gets the same adapter so the dependency
// boundary stays sharp.
//
// Note we close over spec.Handler rather than the whole spec: the
// closure is allocated once per Dispatch call (at mux-wiring time, not
// per-task), so capturing only the function pointer keeps the per-task
// path small and avoids holding a reference to the larger TaskSpec
// value for the lifetime of the worker.
func adapt(spec TaskSpec) asynq.Handler {
	h := spec.Handler
	return asynq.HandlerFunc(func(ctx context.Context, t *asynq.Task) error {
		return h(ctx, t.Payload())
	})
}
