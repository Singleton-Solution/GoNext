package hooks

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// SchemaEnforcer is the minimal surface the bus requires from a payload
// validator. We declare it as an interface here (rather than importing
// packages/go/hooks/schemas directly) so the hooks package stays free of
// a hard dependency on jsonschema — packages that fire hooks without
// validation pay no compile-time cost for the feature.
//
// The interface mirrors *schemas.Enforcer's Validate method. The bus
// calls Validate once per Apply/Do invocation; the implementation is
// expected to be cheap (compiled-schema reuse, no per-call allocation
// beyond the JSON round-trip).
type SchemaEnforcer interface {
	Validate(hookName string, payload any) error
}

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

	// actionOpts stores per-action-name configuration set via
	// SetActionOptions. Currently the only entry is OrderedAsync (see
	// ordered.go). sync.Map matches actions/filters above: read-heavy,
	// rare writes at startup, key set grows over time.
	actionOpts sync.Map // map[string]OrderedAsync

	// ordered is the lazy-initialized dispatcher for strict-ordered
	// async actions. The first ordered Do creates it; subsequent calls
	// reuse the same instance. atomic.Pointer is the right shape for
	// "create-once, read-many" without a sync.Once because the dispatcher
	// owns a background reaper that needs to live for the bus's lifetime.
	ordered atomic.Pointer[orderedDispatcher]

	// schemas, when non-nil, is invoked from Do and ApplyFilters to
	// validate the payload BEFORE any handler runs. A validation
	// failure short-circuits the dispatch with the validator's error
	// and bumps the schema_rejected counter on the metrics sink.
	//
	// Stored as atomic.Pointer so WithSchemas is hot-swappable without
	// torn reads on concurrent in-flight Apply/Do calls — the same
	// pattern used for logger and metrics.
	schemas atomic.Pointer[SchemaEnforcer]

	// batchAdapters indexes registrations that opted into the batch
	// filter path via RegisterBatchFilter. The map is keyed by hook
	// name then by the registration's token so invokeBatch can spot a
	// batch-aware handler in the middle of a chain that also contains
	// legacy per-item filters.
	//
	// The mutex is taken on the hot path of ApplyBatch (one RLock per
	// handler invocation), but only by hooks that have *any* batch
	// registration — the nil-map fast path inside batchAdapterFor
	// short-circuits without touching the lock.
	batchAdaptersMu sync.RWMutex
	batchAdapters   map[string]map[uint64]*batchFilterAdapter
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
//
// source/before/after are the optional declarative-ordering fields that
// supplement the numeric priority. source names this subscriber so other
// subscribers can refer to it; before/after name the subscribers this one
// must run before/after. An empty source is fine (the subscriber simply
// can't be the target of anyone else's constraint); an empty before/after
// is the common case (priority-only ordering, identical to pre-#265
// behavior).
type registration struct {
	token    uint64
	priority int
	regOrder uint64
	async    bool
	active   *atomic.Bool
	action   ActionHandler
	filter   FilterHandler
	source   string
	before   []string
	after    []string
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

// WithSchemas attaches a [SchemaEnforcer] to the bus. When set, every
// [Bus.Do] and [Bus.ApplyFilters] call validates its payload against
// the enforcer BEFORE running any handler. A validation failure
// short-circuits dispatch with the validator's error, leaving no
// handler observed and bumping the schema_rejected counter on the
// metrics sink.
//
// The payload presented to the validator is shaped to match what plugin
// authors see:
//
//   - For Do (actions), the validator receives args (the variadic slice
//     as a single []any value) — handlers receive that slice spread, so
//     the schema describes the slice itself. An action with zero args
//     should declare `{"type": "array", "maxItems": 0}`.
//
//   - For ApplyFilters (filters), the validator receives `value` — the
//     running value being threaded through the chain. Filter args... is
//     NOT validated here because filters operate on `value`; args... is
//     auxiliary context that varies per call site.
//
// Pass nil to remove a previously installed enforcer. The swap is
// atomic — an in-flight Do/Apply sees either the old or the new
// enforcer but never a torn read.
//
// Safe for concurrent use; returns b for chaining.
func (b *Bus) WithSchemas(enf SchemaEnforcer) *Bus {
	if enf == nil {
		b.schemas.Store(nil)
		return b
	}
	b.schemas.Store(&enf)
	return b
}

// schemaEnforcer returns the configured [SchemaEnforcer] or nil. Hot
// path — kept tiny so the no-validator case is one atomic load and a
// nil check.
func (b *Bus) schemaEnforcer() SchemaEnforcer {
	p := b.schemas.Load()
	if p == nil {
		return nil
	}
	return *p
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

// RegisterOptions carries the declarative-ordering metadata a subscriber
// can supply alongside (or in place of) a numeric priority. See issue
// #265 — numeric priority alone forces plugin authors to play games with
// magic numbers when they want to express "I must run after plugin X";
// before/after lets them say so directly and have the scheduler resolve
// the partial order via a topological sort.
//
// Source is this subscriber's identifier, the name other subscribers
// reference in their Before/After lists. Convention is "plugin-slug" or
// "plugin-slug/hook-tag" — anything stable, unique enough to be a
// useful constraint target, and meaningful in error messages.
//
// Before and After are the constraint lists. "Before: [\"foo\"]" means
// "this subscriber must run before every subscriber whose Source is
// \"foo\"". "After: [\"bar\"]" is the symmetric form. Constraints that
// reference a Source no subscriber currently has are silently dropped
// at sort time (with a slog warning) — this matches the WordPress
// expectation that an "after some-other-plugin" registration is best-
// effort when that plugin isn't installed.
//
// Priority is the numeric fallback that breaks ties the constraint graph
// doesn't pin. Zero is the documented default; lower runs first; ties
// preserve registration order, identical to the pre-#265 contract.
type RegisterOptions struct {
	Source   string
	Before   []string
	After    []string
	Priority int
}

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
	off, _ := b.register(name, kindActionCall, false, handler, nil, RegisterOptions{Priority: priority})
	return off
}

// RegisterActionWithOptions registers an action listener with the full
// declarative-ordering metadata of RegisterOptions, returning an
// unsubscribe function and any registration error.
//
// Errors:
//
//   - ErrSelfConstraint: this subscriber's Source appears in its own
//     Before or After list. Always a typo; the registration is rejected
//     and the returned unsubscribe is a no-op.
//   - ErrCycle: adding this subscriber would create a cycle in the
//     constraint graph for the hook. The registration is rejected, the
//     existing chain is unchanged, and the returned unsubscribe is a
//     no-op.
//
// On any other error (none currently) the registration is also rejected.
// On success, the returned unsubscribe is the same idempotent closure
// RegisterAction returns.
func (b *Bus) RegisterActionWithOptions(name string, opts RegisterOptions, handler ActionHandler) (func(), error) {
	return b.register(name, kindActionCall, false, handler, nil, opts)
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
	off, _ := b.register(name, kindActionCall, true, handler, nil, RegisterOptions{Priority: priority})
	return off
}

// RegisterAsyncWithOptions is the declarative-ordering variant of
// RegisterAsync. See RegisterActionWithOptions for the error cases —
// they are identical here.
func (b *Bus) RegisterAsyncWithOptions(name string, opts RegisterOptions, handler ActionHandler) (func(), error) {
	return b.register(name, kindActionCall, true, handler, nil, opts)
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
	off, _ := b.register(name, kindFilterCall, false, nil, handler, RegisterOptions{Priority: priority})
	return off
}

// RegisterFilterWithOptions is the declarative-ordering variant of
// RegisterFilter. See RegisterActionWithOptions for the error cases —
// they are identical here.
func (b *Bus) RegisterFilterWithOptions(name string, opts RegisterOptions, handler FilterHandler) (func(), error) {
	return b.register(name, kindFilterCall, false, nil, handler, opts)
}

// register is the shared implementation for the three exported registrars.
// It allocates a registration, inserts it into the per-name slot's chain
// under the slot's mutex, runs the priority sort + topological sort to
// resolve before/after constraints, then publishes the new chain via
// atomic store.
//
// Returning (func, error) rather than panicking on a malformed
// registration is a deliberate API choice for the *WithOptions surface:
// a cycle is almost always a misconfiguration the operator wants to see
// at startup as a typed error, not a runtime panic that takes down the
// process. The error is also surfaced through the legacy non-options
// RegisterAction/RegisterFilter callers — but those can't pass
// constraints, so they will never trigger a non-nil error and the
// generated unsubscribe closure is always returned.
func (b *Bus) register(
	name string,
	kind callKind,
	async bool,
	action ActionHandler,
	filter FilterHandler,
	opts RegisterOptions,
) (func(), error) {
	token := b.regSeq.Add(1)
	active := &atomic.Bool{}
	active.Store(true)
	reg := registration{
		token:    token,
		priority: opts.Priority,
		regOrder: token, // token doubles as regOrder (monotonic)
		async:    async,
		active:   active,
		action:   action,
		filter:   filter,
		source:   opts.Source,
		before:   opts.Before,
		after:    opts.After,
	}

	// Self-constraint check happens before we touch the slot — it does
	// not depend on the rest of the chain and rejecting early keeps the
	// hot path branch-free.
	if err := validateSelfConstraints(reg); err != nil {
		return noopUnsub, err
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
	// Pre-sort by (priority, regOrder). This stable order is both the
	// fallback for subscribers without constraints and the deterministic
	// tie-break topoSort uses when multiple roots have in-degree zero.
	sort.SliceStable(next, func(i, j int) bool {
		if next[i].priority != next[j].priority {
			return next[i].priority < next[j].priority
		}
		return next[i].regOrder < next[j].regOrder
	})
	// Topological pass resolves before/after constraints. The fast path
	// (no constraints anywhere in the chain) is detected up front so we
	// can skip the O(n^2) Kahn loop for the common case — most chains
	// will be priority-only.
	ordered, err := orderChain(next)
	if err != nil {
		// Cycle (or any future ordering error) → reject this
		// registration, leave the existing chain untouched. The
		// unsubscribe we hand back is a no-op so callers who ignore the
		// error don't accidentally unregister an unrelated handler.
		slot.mu.Unlock()
		b.log().Warn("hook registration rejected: ordering constraints invalid",
			slog.String("hook", name),
			slog.String("source", opts.Source),
			slog.Any("err", err),
		)
		return noopUnsub, err
	}
	// Warn (once, at registration time) about constraints that point at
	// Sources nobody registered. The constraint still drops silently at
	// sort time — this is just the operator-visible signal so a typo or
	// a missing-plugin scenario doesn't go unnoticed.
	b.warnUnknownConstraints(name, reg, next)
	slot.chain.Store(&ordered)
	slot.mu.Unlock()

	// The unsubscribe closure flips the active flag, then rebuilds the
	// chain to prune the tombstone. Flipping the flag first means
	// in-flight readers stop calling this handler immediately, even
	// before the chain rebuild publishes. sync.Once guarantees idempotence
	// on repeated unsubscribe calls.
	//
	// Removing a node cannot introduce a cycle, but it does invalidate
	// the cached topological order in one specific way: a Before/After
	// constraint pointing at the removed Source becomes "dangling" and
	// the remaining order may now violate some other implicit constraint
	// the removed node was sitting between. We re-run orderChain on the
	// pruned slice to keep the cache consistent — the resort is cheap
	// (chain sizes are small) and avoids subtle bugs where a removed
	// listener leaves the rest of the chain in a stale order.
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
			// Re-order after removal. A successful prior order means the
			// graph has no cycles; removing a node cannot introduce one,
			// so the only error case is theoretical and we fall back to
			// the pre-topo order if it somehow fires.
			ordered, err := orderChain(pruned)
			if err == nil {
				pruned = ordered
			}
			slot.chain.Store(&pruned)
			slot.mu.Unlock()
		})
	}, nil
}

// orderChain runs the topological sort over a priority-pre-sorted chain.
// The fast path — no subscriber in the chain has any before/after
// constraint — skips the Kahn allocation altogether and returns the
// input unchanged. The slow path delegates to topoSort.
//
// This split lives here (not in order.go) so the fast-path branch can
// stay tight: a chain with zero constraints walks the slice once and
// returns. order.go owns the actual algorithm.
func orderChain(c chain) (chain, error) {
	hasConstraints := false
	for i := range c {
		if len(c[i].before) > 0 || len(c[i].after) > 0 {
			hasConstraints = true
			break
		}
	}
	if !hasConstraints {
		// No constraints: the priority+regOrder sort already done by
		// the caller is the final order. Hand back the slice as-is.
		return c, nil
	}
	out, err := topoSort([]registration(c))
	if err != nil {
		return nil, err
	}
	return chain(out), nil
}

// noopUnsub is the unsubscribe handle returned alongside a non-nil
// registration error. Calling it is a no-op — there is nothing to
// unregister because the registration was rejected. We give callers a
// non-nil closure so the common pattern of `defer off()` doesn't panic
// when they ignore the error.
func noopUnsub() {}

// warnUnknownConstraints logs a warning for each Before/After target in
// reg that doesn't match any Source currently in chain. Called inside
// the slot's mutex so the snapshot is consistent.
//
// We log rather than fail because "after X" referring to an absent X is
// a perfectly reasonable best-effort declaration — if X gets installed
// later the constraint resolves itself. But the operator deserves a
// heads-up at startup so a missing-plugin issue doesn't masquerade as
// "ordering quietly does the wrong thing."
func (b *Bus) warnUnknownConstraints(hook string, reg registration, c chain) {
	if len(reg.before) == 0 && len(reg.after) == 0 {
		return
	}
	known := make(map[string]struct{}, len(c))
	for i := range c {
		if c[i].source != "" {
			known[c[i].source] = struct{}{}
		}
	}
	check := func(targets []string, kind string) {
		for _, t := range targets {
			if _, ok := known[t]; ok {
				continue
			}
			b.log().Warn("hook ordering constraint references unknown source",
				slog.String("hook", hook),
				slog.String("source", reg.source),
				slog.String("constraint", kind),
				slog.String("target", t),
			)
		}
	}
	check(reg.before, "before")
	check(reg.after, "after")
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

	// Validate before fan-out. Actions receive the variadic args slice
	// as their payload — so we present args to the validator as a
	// single []any. A misbehaving plugin firing the wrong shape is
	// rejected before any listener observes the value, which is the
	// whole point of issue #259's "validate on both sides" contract.
	//
	// Note on the empty-args case: when a caller invokes Do(ctx, name)
	// with no varargs, Go materialises args as a nil slice. Marshalled
	// to JSON that becomes "null", which fails type=array schemas
	// (zero-args actions like init/wp_head). We normalise to a
	// non-nil zero-length slice so the validator sees an empty array,
	// which is the shape callers actually mean.
	if enf := b.schemaEnforcer(); enf != nil {
		payload := args
		if payload == nil {
			payload = []any{}
		}
		if err := enf.Validate(name, []any(payload)); err != nil {
			sink.Counter(metricSchemaRejected, map[string]string{
				labelKind: kindAction,
				labelHook: name,
			})
			return err
		}
	}

	slot, ok := b.actions.Load(name)
	if !ok {
		return nil
	}
	snapshot := slot.(*chainSlot).chain.Load()
	if len(*snapshot) == 0 {
		return nil
	}

	// If this action is configured for strict-ordered async dispatch and
	// has at least one async subscriber, bundle all async subscribers into
	// a single per-key job so they fire as a unit in submission order
	// across same-key events. Sync subscribers still run inline below.
	var orderedOpts OrderedAsync
	hasOrdered := false
	if v, ok := b.actionOpts.Load(name); ok {
		if oa, ok2 := v.(OrderedAsync); ok2 && oa.KeyFn != nil {
			orderedOpts = oa
			hasOrdered = true
		}
	}

	var errs []error
	var asyncRegs chain
	for i, reg := range *snapshot {
		if !reg.active.Load() {
			continue
		}
		if reg.async {
			if hasOrdered {
				asyncRegs = append(asyncRegs, reg)
			} else {
				b.dispatchAsync(ctx, name, reg, args)
			}
			continue
		}
		err := b.invokeAction(ctx, name, i, reg, args)
		if err != nil {
			errs = append(errs, err)
		}
	}

	if hasOrdered && len(asyncRegs) > 0 {
		key := b.safeKey(ctx, name, orderedOpts, args)
		d := b.orderedDispatcher()
		if err := d.dispatch(ctx, name, key, orderedOpts, asyncRegs, args); err != nil {
			// Backlog / context errors are surfaced to the caller via
			// errs so Do's contract — "errors from this dispatch" —
			// covers ordered-async drops too. We chose to include this
			// in the aggregated error (rather than silently log) because
			// a backlog drop means the event did not reach any
			// subscriber, which is materially different from an async
			// handler returning an error.
			errs = append(errs, err)
		}
	}

	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}

// safeKey calls the OrderedAsync KeyFn with panic recovery. A panicking
// KeyFn falls back to the empty key — every event lands on the same worker
// — which preserves "at most one panic per misconfigured action" rather
// than failing every Do.
func (b *Bus) safeKey(ctx context.Context, name string, opts OrderedAsync, args []any) (key string) {
	defer func() {
		if r := recover(); r != nil {
			b.log().ErrorContext(ctx, "OrderedAsync KeyFn panicked; using empty key",
				slog.String("hook", name),
				slog.Any("recovered", r),
			)
			key = ""
		}
	}()
	return opts.KeyFn(args)
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

	// Validate the running value BEFORE entering the chain. Filters
	// thread `value` through each handler; the schema describes that
	// value, not the args... auxiliary slice (which varies per call
	// site and rarely has a stable shape).
	//
	// On rejection we return the input value unchanged plus the
	// validator error — matching the "last accepted value" convention
	// used elsewhere in ApplyFilters for non-short-circuit errors.
	if enf := b.schemaEnforcer(); enf != nil {
		if err := enf.Validate(name, value); err != nil {
			sink.Counter(metricSchemaRejected, map[string]string{
				labelKind: kindFilter,
				labelHook: name,
			})
			return value, err
		}
	}

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

// BatchFilterHandler is the signature opted-into by plugins that prefer
// to receive a whole []any slice in one call rather than be invoked N
// times by ApplyFilters. It mirrors FilterHandler but pluralises the
// `value` parameter to a slice — the handler returns the transformed
// slice (same length, mapped element-by-element) or any error.
//
// The slice length is preserved on the returned value: ApplyBatch
// validates this contract and falls back to the input slice if a
// misbehaving handler resizes it (we log loudly so the bug surfaces
// without dropping items from the chain).
//
// Plugins opt in via the manifest flag `flags.apply_filters_batch`
// (parsed by packages/go/plugins/manifest). The hook bus carries no
// manifest awareness itself — it just exposes RegisterBatchFilter for
// the manifest-driven wiring layer to call.
type BatchFilterHandler func(ctx context.Context, items []any, args ...any) ([]any, error)
// ApplyBatch is the hot-path equivalent of ApplyFilters for callers
// that want to thread a whole slice through a filter chain in a single
// dispatch. It is the issue #263 optimisation: instead of N separate
// ApplyFilters invocations for N items (each paying the per-call
// validate + metrics + per-handler overhead), ApplyBatch dispatches
// once with the whole slice.
//
// Two flavours of handler participate in the chain:
//
//   - Batch-aware handlers (registered via RegisterBatchFilter)
//     receive items as []any and return the transformed []any. Plugins
//     opt in via the manifest flag `flags.apply_filters_batch`; the
//     wiring layer that reads the manifest is what calls
//     RegisterBatchFilter on this bus.
//
//   - Regular filter handlers (registered via RegisterFilter) are
//     called once per item — the bus loops the legacy handler over the
//     slice. This keeps backward compatibility: existing handlers
//     continue to work unchanged inside a batched chain.
//
// Order of dispatch is the same priority-sorted chain ApplyFilters
// uses; the batch and non-batch handlers interleave by priority. A
// batch-aware handler that mutates the slice's length is rejected
// (the input slice is preserved and a warning logged) so downstream
// handlers always see a slice whose i-th item is the transformed
// version of the original i-th item.
//
// Short-circuit / error semantics mirror ApplyFilters: returning
// ErrShortCircuit from any handler stops the chain successfully with
// the value-so-far; any other error stops the chain with the
// last-accepted slice.
func (b *Bus) ApplyBatch(ctx context.Context, name string, items []any, args ...any) ([]any, error) {
	start := time.Now()
	sink := b.sink()
	sink.Counter(metricDispatchTotal, map[string]string{
		labelKind: kindFilter,
		labelHook: name,
		labelBatch: "true",
	})
	defer func() {
		sink.Histogram(metricDispatchDuration, time.Since(start).Seconds(),
			map[string]string{labelKind: kindFilter, labelHook: name, labelBatch: "true"})
	}()

	// Validate every item once before entering the chain. A single bad
	// item rejects the whole batch — that matches the contract of
	// ApplyFilters (one bad value, one returned error) extended to the
	// pluralised case.
	if enf := b.schemaEnforcer(); enf != nil {
		for i, v := range items {
			if err := enf.Validate(name, v); err != nil {
				sink.Counter(metricSchemaRejected, map[string]string{
					labelKind:  kindFilter,
					labelHook:  name,
					labelBatch: "true",
				})
				return items, fmt.Errorf("hooks: ApplyBatch %q: item %d: %w", name, i, err)
			}
		}
	}

	slot, ok := b.filters.Load(name)
	if !ok {
		return items, nil
	}
	snapshot := slot.(*chainSlot).chain.Load()
	if len(*snapshot) == 0 {
		return items, nil
	}

	// Defensive copy: handlers operate on the slice the bus owns, and we
	// hand the final value back to the caller. A handler mutating in place
	// is fine; what we want to avoid is the *caller* seeing a half-mutated
	// slice if the chain errors midway. The copy is one allocation per
	// ApplyBatch — cheap relative to the N-call alternative this method
	// exists to avoid.
	current := make([]any, len(items))
	copy(current, items)

	for i, reg := range *snapshot {
		if !reg.active.Load() {
			continue
		}
		next, err := b.invokeBatch(ctx, name, i, reg, current, args)
		if err != nil {
			if errors.Is(err, ErrShortCircuit) {
				sink.Counter(metricShortCircuit, map[string]string{labelHook: name})
				return next, nil
			}
			return current, err
		}
		if len(next) != len(current) {
			// Length-mismatch is a contract violation. Log and keep the
			// previous slice — silently dropping or padding items would
			// confuse downstream handlers and the caller.
			b.log().Error("hooks: batch filter returned mismatched slice length; ignoring",
				slog.String("hook", name),
				slog.Int("expected", len(current)),
				slog.Int("got", len(next)),
				slog.Uint64("token", reg.token),
			)
			continue
		}
		current = next
	}
	return current, nil
}

// RegisterBatchFilter registers a batch-aware filter handler. The
// manifest layer calls this when a plugin sets `flags.apply_filters_batch`
// in its manifest; everyday code paths should keep using RegisterFilter.
//
// The returned unsubscribe closure behaves like the one RegisterFilter
// returns: idempotent, safe to call from concurrent goroutines.
func (b *Bus) RegisterBatchFilter(name string, priority int, handler BatchFilterHandler) func() {
	if handler == nil {
		return noopUnsub
	}
	// Wrap the batch handler as a regular FilterHandler so the registration
	// machinery and chain semantics need no changes. invokeBatch knows
	// how to spot the wrapper and call the batch path; legacy ApplyFilters
	// callers see the handler as a per-item filter that applies the batch
	// over a singleton slice.
	wrapper := &batchFilterAdapter{handler: handler}
	filterFn := func(ctx context.Context, value any, args ...any) (any, error) {
		out, err := handler(ctx, []any{value}, args...)
		if err != nil {
			return value, err
		}
		if len(out) != 1 {
			return value, fmt.Errorf("hooks: batch filter %q returned %d items for singleton input", name, len(out))
		}
		return out[0], nil
	}
	off, _ := b.register(name, kindFilterCall, false, nil, filterFn, RegisterOptions{Priority: priority})
	// Tag the latest registration so invokeBatch can route the slice
	// straight to the batch entry-point. The lookup is done by token
	// inside invokeBatch.
	b.batchAdaptersMu.Lock()
	if b.batchAdapters == nil {
		b.batchAdapters = make(map[string]map[uint64]*batchFilterAdapter)
	}
	if b.batchAdapters[name] == nil {
		b.batchAdapters[name] = make(map[uint64]*batchFilterAdapter)
	}
	// The token assigned to this registration is the last one issued. We
	// snapshot regSeq AFTER register returns: register's call to b.regSeq.Add
	// reserved exactly one slot, so the current value of regSeq is this
	// handler's token.
	b.batchAdapters[name][b.regSeq.Load()] = wrapper
	b.batchAdaptersMu.Unlock()

	originalOff := off
	return func() {
		originalOff()
		b.batchAdaptersMu.Lock()
		delete(b.batchAdapters[name], b.regSeq.Load())
		b.batchAdaptersMu.Unlock()
	}
}

// batchFilterAdapter pairs a RegisterBatchFilter call with its
// original handler so invokeBatch can route the slice directly into
// the BatchFilterHandler without re-routing through the per-item
// wrapper.
type batchFilterAdapter struct {
	handler BatchFilterHandler
}

// invokeBatch runs one handler against the running slice. If the
// handler was registered via RegisterBatchFilter it dispatches the
// batch path in one call; otherwise it loops the legacy FilterHandler
// over each item.
func (b *Bus) invokeBatch(ctx context.Context, name string, idx int, reg registration, items []any, args []any) (result []any, err error) {
	start := time.Now()
	sink := b.sink()
	labels := map[string]string{labelKind: kindFilter, labelHook: name, labelBatch: "true"}
	defer func() {
		sink.Histogram(metricHandlerDuration, time.Since(start).Seconds(), labels)
	}()

	defer func() {
		if r := recover(); r != nil {
			pe := &panicError{hook: name, handler: idx, value: r}
			sink.Counter(metricHandlerPanic, labels)
			b.log().ErrorContext(ctx, "hook batch handler panicked",
				slog.String("hook", name),
				slog.String("kind", kindFilter),
				slog.Any("recovered", r),
			)
			result = items
			err = pe
		}
	}()

	// Batch-aware path: route the whole slice to the BatchFilterHandler
	// in one call.
	if adapter := b.batchAdapterFor(name, reg.token); adapter != nil {
		out, hErr := adapter.handler(ctx, items, args...)
		if hErr != nil && !errors.Is(hErr, ErrShortCircuit) {
			sink.Counter(metricHandlerError, labels)
		}
		return out, hErr
	}

	// Legacy path: loop the per-item filter over the slice. Yes, this
	// is N calls — but the caller already paid the cost of ApplyBatch's
	// per-call setup once for the whole batch (validation, metrics
	// timing, snapshot loading), so the per-item overhead is just the
	// handler invocation itself.
	out := make([]any, len(items))
	for i, v := range items {
		next, hErr := reg.filter(ctx, v, args...)
		if hErr != nil {
			if errors.Is(hErr, ErrShortCircuit) {
				// Short-circuit at item i applies to the whole batch: the
				// item the short-circuit returned replaces the i-th slot;
				// the rest of the items pass through unchanged (caller
				// sees the latest accepted values).
				out[i] = next
				for j := i + 1; j < len(items); j++ {
					out[j] = items[j]
				}
				return out, ErrShortCircuit
			}
			sink.Counter(metricHandlerError, labels)
			return items, hErr
		}
		out[i] = next
	}
	return out, nil
}

// batchAdapterFor returns the registered adapter for (name, token) or
// nil if the registration was not made via RegisterBatchFilter. The
// fast path (no batch-aware handler ever registered) avoids the lock
// entirely.
func (b *Bus) batchAdapterFor(name string, token uint64) *batchFilterAdapter {
	b.batchAdaptersMu.RLock()
	defer b.batchAdaptersMu.RUnlock()
	if b.batchAdapters == nil {
		return nil
	}
	m := b.batchAdapters[name]
	if m == nil {
		return nil
	}
	return m[token]
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
