package hooks

import (
	"context"
	"errors"
	"fmt"
)

// ErrShortCircuit is returned by a FilterHandler to stop a filter chain
// and return the current value to the caller of ApplyFilters.
//
// This is the explicit "early return" mechanism described in
// docs/02-plugin-system.md §5.5: rather than relying on a sentinel return
// value or a bespoke per-filter convention, a handler asks the bus to stop
// the chain by returning ErrShortCircuit (or any error wrapping it via
// errors.Is). ApplyFilters returns the value the handler returned alongside
// the short-circuit sentinel and a nil error — the chain stopping is the
// success outcome, not a failure.
//
// Example:
//
//	bus.RegisterFilter("rest.post.serialize", 50,
//	    func(ctx context.Context, value any, args ...any) (any, error) {
//	        post := value.(*Post)
//	        if post.Cached {
//	            return post.CachedJSON, hooks.ErrShortCircuit
//	        }
//	        return value, nil
//	    })
var ErrShortCircuit = errors.New("hooks: short-circuit filter chain")

// ActionHandler is the signature for action listeners.
//
// args is the call-site payload — the bus passes it through untouched.
// Returning an error contributes to the aggregated error from Do; an
// Async handler's error is logged via slog rather than returned to the
// originator (no one is waiting to receive it).
//
// Handlers must respect ctx — they SHOULD return promptly if ctx.Err()
// becomes non-nil. The bus itself does not cancel handlers; it relies on
// the handler honoring its context.
type ActionHandler func(ctx context.Context, args ...any) error

// FilterHandler is the signature for filter chain members.
//
// value is the running value being transformed; args are the unchanged
// per-call extras provided by the originator of ApplyFilters.
//
// Returning (newValue, nil) advances the chain. Returning a non-nil error
// — other than ErrShortCircuit — stops the chain and surfaces the error to
// the caller along with the last accepted value (NOT the value this handler
// produced). Returning ErrShortCircuit stops the chain successfully and the
// value this handler returned is what the caller receives.
type FilterHandler func(ctx context.Context, value any, args ...any) (any, error)

// panicError wraps a value recovered from a handler panic. It carries the
// recovered value through errors.Is/As without losing the original type
// (callers can errors.As to *panicError to recover the original).
//
// We surface this rather than swallowing because the alternative — a
// panicking handler silently dropping a hook — is the kind of bug that
// only shows up in production. The slog ERROR line is the primary signal;
// the typed error is for tests and aggregators that want machine-readable
// proof a panic happened.
type panicError struct {
	hook    string
	handler int // index in the priority-sorted chain
	value   any
}

func (p *panicError) Error() string {
	return fmt.Sprintf("hooks: handler for %q panicked (chain index %d): %v",
		p.hook, p.handler, p.value)
}

// Unwrap lets errors.Is/As reach into the recovered value if it is itself
// an error (the most common case for panic(err)).
func (p *panicError) Unwrap() error {
	if e, ok := p.value.(error); ok {
		return e
	}
	return nil
}
