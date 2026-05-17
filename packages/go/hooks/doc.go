// Package hooks is the GoNext in-process hook bus — WordPress-style actions
// and filters, but expressed as Go functions registered against a Bus.
//
// Two kinds of hooks live here:
//
//   - Actions are fire-and-forget side effects. Many handlers may listen for
//     the same action name (e.g. "post.published"); Do runs them in priority
//     order and aggregates any errors. A non-blocking variant, RegisterAsync,
//     lets a handler run in its own goroutine — the caller of Do does not
//     wait, and a handler error is logged via slog rather than returned.
//
//   - Filters transform a value through a chain. Each registered filter for
//     a name (e.g. "the_content") receives the previous return value and
//     returns the next one. ApplyFilters orchestrates the chain and returns
//     the final value plus any error. A filter handler may return
//     ErrShortCircuit to stop the chain early with the current value.
//
// Hook names are free-form strings. The platform uses dotted, namespaced
// identifiers — core hooks read "core.post.published" / "core.filter.the_content",
// plugins use "plg.{slug}.something_happened" — but the bus itself imposes
// no syntax rule. See docs/02-plugin-system.md §5 for the naming convention.
//
// Priority ordering: handlers are sorted by Priority ascending (lower = earlier).
// Ties preserve registration order, matching WordPress semantics so that
// authors porting plugins get the same behavior they expect.
//
// Panic recovery: a panicking handler is caught, logged at ERROR via slog
// (which is the only way to surface a problem from an Async handler), and
// treated as a returning error. For Do/ApplyFilters this means:
//
//   - Action chain continues past a panic; the error is aggregated.
//   - Filter chain stops at a panic; the last good value is returned with
//     the wrapped panic as the error.
//
// Reentrance: a handler may register or unregister hooks while running.
// Newly registered handlers do not participate in the in-flight dispatch
// — they take effect on the next Do/ApplyFilters call. Unregistering a
// handler that is mid-execution is safe: the running invocation completes,
// but later dispatches skip the unregistered slot.
//
// Concurrency: reads (Do/ApplyFilters) are lock-free. The handler table is
// a sync.Map of name -> *atomic.Value holding the priority-sorted slice;
// mutations copy-on-write so readers never observe a partially-mutated chain.
// Register and the returned unsubscribe function are safe to call from any
// goroutine.
//
// Metrics: a MetricsSink interface (no-op by default) lets packages/go/metrics
// plug in counter and histogram callbacks without taking a dependency the
// other way. Wire one in with Bus.WithMetrics; see Sink for the contract.
//
// Typical use:
//
//	bus := hooks.NewBus()
//
//	// A core subsystem registers an action listener for post publication.
//	off := bus.RegisterAction("core.post.published", 10,
//	    func(ctx context.Context, args ...any) error {
//	        post := args[0].(*Post)
//	        return search.Index(ctx, post)
//	    })
//	defer off()
//
//	// A filter that runs ahead of any plugins (low priority).
//	bus.RegisterFilter("core.filter.the_content", 5,
//	    func(ctx context.Context, value any, args ...any) (any, error) {
//	        return strings.TrimSpace(value.(string)), nil
//	    })
//
//	// At the right moment, fire and apply.
//	_ = bus.Do(ctx, "core.post.published", post)
//	rendered, _ := bus.ApplyFilters(ctx, "core.filter.the_content", body)
//
// See docs/02-plugin-system.md §5 for the design rationale and the
// WordPress-compat naming convention.
package hooks
