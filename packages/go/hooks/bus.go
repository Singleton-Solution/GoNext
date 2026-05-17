package hooks

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Bus is the hook dispatcher.
//
// One Bus instance is expected per process — wire it into your DI graph and
// pass it to anything that needs to register handlers or fire hooks. Two
// buses in the same process won't share handlers, which is the intended
// isolation boundary (tests use it freely; production uses one).
//
// The zero value of Bus is NOT ready for use — call NewBus. Construction
// allocates the internal map and seeds the no-op Sink.
//
// Concurrency model:
//
//   - Reads (Do, ApplyFilters) take no lock. Each hook name maps to an
//     *atomic.Value holding a *chain (a priority-sorted []registration).
//     Readers Load the chain pointer once and iterate the snapshot.
//
//   - Writes (Register*, the returned unsubscribe) take a per-name mutex
//     (via the sync.Map of *chainSlot) and Store a new chain pointer.
//     The old chain remains valid for any reader that observed it.
//
//   - This gives lock-free fan-out at the price of an allocation per
//     register. Plugins register at startup, not per request, so the
//     allocation is amortized.
type Bus struct {
	// actions and filters store per-name *chainSlot. We use sync.Map for
	// the outer map because name lookup is read-heavy (every Do call
	// reads; only Register writes), and sync.Map is the right shape for
	// "many readers, occasional writers, key set grows over time".
	actions sync.Map // map[string]*chainSlot
	filters sync.Map // map[string]*chainSlot

	// regSeq generates the monotonic registration order used to break
	// priority ties (stable sort semantics matching WordPress).
	regSeq atomic.Uint64

	// logger is the slog logger used for panic-in-handler reports and
	// async-handler errors. Defaults to slog.Default(); replace via
	// WithLogger for test isolation.
	logger atomic.Pointer[slog.Logger]

	// metrics is the metrics sink. Defaults to noopSink; replace via
	// WithMetrics. atomic.Pointer[Sink] lets the concrete implementation
	// type vary between calls without tripping atomic.Value's
	// "inconsistently typed value" check.
	metrics atomic.Pointer[Sink]

	// asyncWG tracks in-flight async goroutines so tests (and graceful
	// shutdown, eventually) can wait for them to finish. Internal use
	// only — not exported because the contract is "fire and forget".
	asyncWG sync.WaitGroup
}

// chainSlot holds a single hook's handler chain plus the mutex that
// serializes mutations. The chain pointer is updated copy-on-write so
// readers never need to lock.
//
// We keep one chainSlot per hook name, not per Bus, so concurrent
// registrations on different hooks don't contend with each other.
type chainSlot struct {
	mu    sync.Mutex
	chain atomic.Pointer[chain]
}

// chain is an immutable, priority-sorted list of registrations. A
// registration whose token has been revoked has its handler set to nil
// — the dispatch loop skips nil entries rather than rebuilding the slice
// on every unsubscribe (the slice is small; copy-on-write would be more
// expensive than a nil check).
type chain []registration

// registration is one row in a chain. Token is the unsubscribe handle's
// identity (just a monotonic uint64); the unsubscribe closure flips
// active to false via atomic.Bool so concurrent readers see the change
// without locking.
//
// Async only applies to actions; it is ignored for filters (where the
// chain has to run synchronously for the value to flow).
type registration struct {
	token    uint64
	priority int
	regOrder uint64
	async    bool
	active   *atomic.Bool
	action   ActionHandler
	filter   FilterHandler
}

// NewBus returns a ready-to-use Bus.
//
// The returned bus uses slog.Default() and a no-op metrics sink; replace
// either via WithLogger / WithMetrics. Calling NewBus is cheap — tests
// freely create a per-test bus.
func NewBus() *Bus {
	b := &Bus{}
	var s Sink = noopSink{}
	b.metrics.Store(&s)
	return b
}

// WithLogger replaces the slog.Logger the bus uses for panic and async
// error reports. Returns b for chaining.
//
// Pass nil to revert to slog.Default(). Safe for concurrent use; the swap
// is atomic so an in-flight dispatch sees either the old or the new
// logger but never a torn read.
func (b *Bus) WithLogger(l *slog.Logger) *Bus {
	b.logger.Store(l)
	return b
}

// WithMetrics replaces the metrics Sink. Returns b for chaining.
//
// Pass nil to revert to the no-op sink. Safe for concurrent use.
func (b *Bus) WithMetrics(s Sink) *Bus {
	if s == nil {
		s = noopSink{}
	}
	b.metrics.Store(&s)
	return b
}

// log returns the configured slog.Logger, falling back to slog.Default
// when nothing has been wired in via WithLogger.
func (b *Bus) log() *slog.Logger {
	if l := b.logger.Load(); l != nil {
		return l
	}
	return slog.Default()
}

// sink returns the configured Sink (never nil — the no-op sink is the
// default).
func (b *Bus) sink() Sink {
	if p := b.metrics.Load(); p != nil {
		return *p
	}
	return noopSink{}
}

// slot returns the *chainSlot for (name, kind), creating it on first use.
// kind selects which sync.Map (actions vs filters) we look in; the two
// kinds share the same name namespace by design — a plugin might fire an
// action and a filter with the same dotted name (e.g. "core.user.created"
// the action, plus "core.filter.user.created" the filter), but if it
// reuses the exact same name for both kinds that's allowed and they live
// in separate slots.
func (b *Bus) slot(name string, kind callKind) *chainSlot {
	m := &b.actions
	if kind == kindFilterCall {
		m = &b.filters
	}
	if v, ok := m.Load(name); ok {
		return v.(*chainSlot)
	}
	// LoadOrStore is the race-safe form: if two goroutines first-register
	// the same name simultaneously, only one *chainSlot is created.
	slot := &chainSlot{}
	slot.chain.Store(&chain{})
	actual, _ := m.LoadOrStore(name, slot)
	return actual.(*chainSlot)
}

// callKind distinguishes action calls from filter calls in internal
// helpers. Exported users see RegisterAction / RegisterFilter / Do /
// ApplyFilters and don't need this distinction.
type callKind uint8

const (
	kindActionCall callKind = iota
	kindFilterCall
)

// RegisterAction registers an action listener and returns an unsubscribe
// function. Calling the returned function more than once is a no-op.
//
// priority orders the dispatch: lower priority runs first. Registrations
// with equal priority run in registration order (stable sort), matching
// WordPress and freeing plugin authors from priority-juggling for ties.
//
// The handler runs synchronously inside Do — to opt into async dispatch,
// use RegisterAsync instead.
//
// Registering while a Do is in flight is safe: the new handler does not
// join the in-flight dispatch; it participates in the next Do call.
func (b *Bus) RegisterAction(name string, priority int, handler ActionHandler) func() {
	return b.register(name, priority, kindActionCall, false, handler, nil)
}

// RegisterAsync registers an action listener that runs in its own
// goroutine. Do returns without waiting for async handlers to finish; an
// error returned by an async handler is logged via slog at ERROR level
// rather than included in Do's aggregated error.
//
// Use this for side-effects that should not block the caller — sending an
// email, indexing a document, posting to a webhook. Synchronous handlers
// are preferred for anything whose failure should fail the request.
func (b *Bus) RegisterAsync(name string, priority int, handler ActionHandler) func() {
	return b.register(name, priority, kindActionCall, true, handler, nil)
}

// RegisterFilter registers a filter handler and returns an unsubscribe
// function. Calling the returned function more than once is a no-op.
//
// Priority semantics match RegisterAction: lower runs first, ties keep
// registration order.
//
// A filter handler MUST return promptly; the chain blocks the caller of
// ApplyFilters. There is no async filter mode because the chain has to
// thread a value through synchronously.
func (b *Bus) RegisterFilter(name string, priority int, handler FilterHandler) func() {
	return b.register(name, priority, kindFilterCall, false, nil, handler)
}

// register is the shared implementation for the three exported registrars.
// It allocates a registration, inserts it into the per-name slot's chain
// under the slot's mutex, then publishes the new chain via atomic store.
func (b *Bus) register(
	name string,
	priority int,
	kind callKind,
	async bool,
	action ActionHandler,
	filter FilterHandler,
) func() {
	token := b.regSeq.Add(1)
	active := &atomic.Bool{}
	active.Store(true)
	reg := registration{
		token:    token,
		priority: priority,
		regOrder: token, // token doubles as regOrder (monotonic)
		async:    async,
		active:   active,
		action:   action,
		filter:   filter,
	}

	slot := b.slot(name, kind)
	slot.mu.Lock()
	old := slot.chain.Load()
	next := make(chain, 0, len(*old)+1)
	// Drop any tombstoned (inactive) entries while we're rebuilding. This
	// keeps the chain from growing unboundedly when a process registers
	// and unregisters a lot — for instance a long-running test suite.
	for _, r := range *old {
		if r.active.Load() {
			next = append(next, r)
		}
	}
	next = append(next, reg)
	sort.SliceStable(next, func(i, j int) bool {
		if next[i].priority != next[j].priority {
			return next[i].priority < next[j].priority
		}
		return next[i].regOrder < next[j].regOrder
	})
	slot.chain.Store(&next)
	slot.mu.Unlock()

	// The unsubscribe closure flips the active flag, then rebuilds the
	// chain to prune the tombstone. Flipping the flag first means
	// in-flight readers stop calling this handler immediately, even
	// before the chain rebuild publishes. sync.Once guarantees idempotence
	// on repeated unsubscribe calls.
	var once sync.Once
	return func() {
		once.Do(func() {
			active.Store(false)
			slot.mu.Lock()
			cur := slot.chain.Load()
			pruned := make(chain, 0, len(*cur))
			for _, r := range *cur {
				if r.token == token {
					continue
				}
				if !r.active.Load() {
					continue
				}
				pruned = append(pruned, r)
			}
			slot.chain.Store(&pruned)
			slot.mu.Unlock()
		})
	}
}

// Do fires the action `name`, running every registered handler in priority
// order and aggregating their errors. Returns nil when every handler
// returned nil (or there were no handlers).
//
// Async handlers (registered via RegisterAsync) are launched in a
// goroutine; Do does NOT wait for them. Their errors are logged via slog
// at ERROR level — there is no other surface for those errors because the
// caller has already moved on by the time the handler finishes.
//
// Panic recovery: if a synchronous handler panics, the recovered value is
// logged and converted into a *panicError that joins the aggregated error.
// The action chain continues past the panicking handler — losing one
// listener should not silently drop the others. (Filter chains have
// stricter semantics; see ApplyFilters.)
//
// Reentrance: a handler is free to call Bus methods, including Register*
// and the unsubscribe closures, while running. Newly registered handlers
// do not join the in-flight dispatch.
func (b *Bus) Do(ctx context.Context, name string, args ...any) error {
	start := time.Now()
	sink := b.sink()
	// Always count the dispatch — including the zero-handler case, which
	// is useful for ops to confirm a hook is being fired.
	sink.Counter(metricDispatchTotal, map[string]string{
		labelKind: kindAction,
		labelHook: name,
	})
	defer func() {
		sink.Histogram(metricDispatchDuration, time.Since(start).Seconds(),
			map[string]string{labelKind: kindAction, labelHook: name})
	}()

	slot, ok := b.actions.Load(name)
	if !ok {
		return nil
	}
	snapshot := slot.(*chainSlot).chain.Load()
	if len(*snapshot) == 0 {
		return nil
	}

	var errs []error
	for i, reg := range *snapshot {
		if !reg.active.Load() {
			continue
		}
		if reg.async {
			b.dispatchAsync(ctx, name, reg, args)
			continue
		}
		err := b.invokeAction(ctx, name, i, reg, args)
		if err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}

// ApplyFilters runs the filter chain for `name`, threading `value` through
// each handler in priority order. Returns the final value and any error.
//
// Short-circuit: a handler may return ErrShortCircuit to stop the chain
// with the value it returned. ApplyFilters returns that value and a nil
// error — the short circuit is a success outcome, not a failure. Use
// ApplyFiltersWith if you need to distinguish "ran to completion" from
// "stopped early."
//
// Errors: a handler that returns any non-nil error other than
// ErrShortCircuit stops the chain. The returned value is the value the
// PREVIOUS handler produced (i.e. the last accepted value), not the value
// the failing handler produced. The error is returned as-is.
//
// Panic recovery: a panicking filter is logged and STOPS the chain. The
// last accepted value is returned along with a *panicError. The reason
// for stop-on-panic in filters (vs continue-on-panic for actions) is that
// a filter chain depends on each handler's output as the next handler's
// input; once a handler crashes we have no good value to continue with.
func (b *Bus) ApplyFilters(ctx context.Context, name string, value any, args ...any) (any, error) {
	start := time.Now()
	sink := b.sink()
	sink.Counter(metricDispatchTotal, map[string]string{
		labelKind: kindFilter,
		labelHook: name,
	})
	defer func() {
		sink.Histogram(metricDispatchDuration, time.Since(start).Seconds(),
			map[string]string{labelKind: kindFilter, labelHook: name})
	}()

	slot, ok := b.filters.Load(name)
	if !ok {
		return value, nil
	}
	snapshot := slot.(*chainSlot).chain.Load()
	if len(*snapshot) == 0 {
		return value, nil
	}

	current := value
	for i, reg := range *snapshot {
		if !reg.active.Load() {
			continue
		}
		next, err := b.invokeFilter(ctx, name, i, reg, current, args)
		if err != nil {
			if errors.Is(err, ErrShortCircuit) {
				sink.Counter(metricShortCircuit, map[string]string{labelHook: name})
				return next, nil
			}
			// Non-short-circuit error: return the LAST ACCEPTED value
			// (current), not the half-baked value from this handler.
			return current, err
		}
		current = next
	}
	return current, nil
}

// dispatchAsync launches the handler in its own goroutine. The goroutine
// is tracked in asyncWG so tests (via Wait) can synchronize without
// time.Sleep. Errors are logged; nothing is returned to Do's caller.
func (b *Bus) dispatchAsync(ctx context.Context, name string, reg registration, args []any) {
	b.asyncWG.Add(1)
	sink := b.sink()
	sink.Counter(metricDispatchTotal, map[string]string{
		labelKind:  kindAction,
		labelHook:  name,
		labelAsync: "true",
	})
	go func() {
		defer b.asyncWG.Done()
		err := b.invokeActionRaw(ctx, name, -1, reg, args, true)
		if err != nil {
			b.log().ErrorContext(ctx, "async hook handler failed",
				slog.String("hook", name),
				slog.Uint64("token", reg.token),
				slog.Any("err", err),
			)
		}
	}()
}

// invokeAction runs one synchronous action handler with panic recovery.
// Errors (including the recovered panic) are returned to Do for aggregation.
func (b *Bus) invokeAction(ctx context.Context, name string, idx int, reg registration, args []any) error {
	return b.invokeActionRaw(ctx, name, idx, reg, args, false)
}

// invokeActionRaw is the shared implementation for sync and async action
// invocation. The async flag affects only the metric label.
func (b *Bus) invokeActionRaw(ctx context.Context, name string, idx int, reg registration, args []any, async bool) (err error) {
	start := time.Now()
	sink := b.sink()
	labels := map[string]string{labelKind: kindAction, labelHook: name}
	defer func() {
		sink.Histogram(metricHandlerDuration, time.Since(start).Seconds(), labels)
	}()

	defer func() {
		if r := recover(); r != nil {
			pe := &panicError{hook: name, handler: idx, value: r}
			sink.Counter(metricHandlerPanic, labels)
			b.log().ErrorContext(ctx, "hook handler panicked",
				slog.String("hook", name),
				slog.String("kind", kindAction),
				slog.Bool("async", async),
				slog.Any("recovered", r),
			)
			err = pe
		}
	}()

	if err = reg.action(ctx, args...); err != nil {
		sink.Counter(metricHandlerError, labels)
	}
	return err
}

// invokeFilter runs one filter handler with panic recovery. Returns the
// produced value and any error (including a *panicError on recovery).
func (b *Bus) invokeFilter(ctx context.Context, name string, idx int, reg registration, value any, args []any) (result any, err error) {
	start := time.Now()
	sink := b.sink()
	labels := map[string]string{labelKind: kindFilter, labelHook: name}
	defer func() {
		sink.Histogram(metricHandlerDuration, time.Since(start).Seconds(), labels)
	}()

	defer func() {
		if r := recover(); r != nil {
			pe := &panicError{hook: name, handler: idx, value: r}
			sink.Counter(metricHandlerPanic, labels)
			b.log().ErrorContext(ctx, "hook handler panicked",
				slog.String("hook", name),
				slog.String("kind", kindFilter),
				slog.Any("recovered", r),
			)
			// On panic, return the input value (unchanged) so the
			// caller has the last-known-good value to surface.
			result = value
			err = pe
		}
	}()

	result, err = reg.filter(ctx, value, args...)
	if err != nil && !errors.Is(err, ErrShortCircuit) {
		sink.Counter(metricHandlerError, labels)
	}
	return result, err
}

// Wait blocks until all async handlers spawned by Do have returned. It is
// intended for tests and graceful shutdown — production code firing Do
// does not call Wait.
//
// Wait does not prevent new async dispatches; it returns when the current
// in-flight set drains, and a Do that races with Wait may launch more
// goroutines that the next Wait will cover. The recommended pattern in
// tests is "stop firing, then Wait" so the synchronization is well-defined.
func (b *Bus) Wait() {
	b.asyncWG.Wait()
}
