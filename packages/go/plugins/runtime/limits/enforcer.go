package limits

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// ErrInstanceLimitReached is returned by Enforcer.Acquire when the
// per-plugin instance-count cap is at the configured maximum.
//
// Callers (typically the pool from #9) can match this with errors.Is
// and either back off, wait for an existing instance to be released,
// or surface a "plugin temporarily unavailable" error upstream.
var ErrInstanceLimitReached = errors.New("limits: per-plugin instance limit reached")

// Enforcer applies a Limits envelope to live calls.
//
// One Enforcer per Runtime is the intended pattern — it holds the
// per-plugin instance counters and serves WithCPUDeadline wrappers
// against a single shared Limits.
//
// Enforcer is goroutine-safe.
type Enforcer struct {
	// Limits is the policy this enforcer applies. Mutating fields
	// after the enforcer is in use is undefined — copy, mutate, then
	// install a fresh Enforcer via runtime.WithLimits.
	Limits Limits

	// instancesMu guards instances. The per-key counter itself is
	// stored as an *int64 inside a sync.Map so check-and-bump can be
	// atomic without holding the outer lock; the lock only protects
	// the *creation* of a new key, which is rare (once per plugin
	// over the lifetime of the runtime).
	instancesMu sync.Mutex
	instances   sync.Map // map[string]*int64
}

// NewEnforcer builds an Enforcer for the given Limits. The Limits are
// validated; an invalid input yields a nil enforcer and a non-nil
// error. Callers that want to skip validation (e.g., for a test
// fixture deliberately exercising odd combinations) can construct the
// struct literal directly.
func NewEnforcer(l Limits) (*Enforcer, error) {
	if err := l.Validate(); err != nil {
		return nil, err
	}
	return &Enforcer{Limits: l}, nil
}

// WithCPUDeadline wraps ctx with the configured CPU-time deadlines and
// returns the wrapped context plus a cancel func.
//
// Behavior:
//
//   - If CPUTimeoutSoft is set and CPUTimeoutHard is zero (or equal),
//     the returned ctx carries a single deadline at CPUTimeoutSoft.
//     Callers cancel by invoking the returned cancel func or letting
//     the deadline fire.
//   - If both are set and CPUTimeoutHard > CPUTimeoutSoft, the soft
//     deadline cancels the ctx first; if the call still hasn't
//     returned by CPUTimeoutHard, a *forced* cancel fires. The hard
//     cancel is implemented by parking a goroutine that closes the
//     hard timer; the returned cancel func tears it down idempotently
//     if the call finishes first.
//   - If neither is set, the returned ctx is the input ctx with a
//     no-op cancel. The caller still gets a CancelFunc so the call
//     site can stay uniform.
//
// The wazero runtime is constructed with WithCloseOnContextDone(true),
// so cancellation propagates into the running guest as a trap.
// WithCPUDeadline is the entry point that arms that trap.
func (e *Enforcer) WithCPUDeadline(parent context.Context) (context.Context, context.CancelFunc) {
	soft := e.Limits.CPUTimeoutSoft
	hard := e.Limits.CPUTimeoutHard

	switch {
	case soft <= 0 && hard <= 0:
		// No CPU policy. Return the parent ctx and a no-op cancel so
		// the caller's `defer cancel()` is always safe.
		return parent, func() {}

	case soft > 0 && (hard <= 0 || hard == soft):
		// Single deadline at soft. The hard path is degenerate.
		return context.WithTimeout(parent, soft)

	case soft <= 0 && hard > 0:
		// Only the hard deadline is set. Treat it as the sole
		// deadline — the soft/hard distinction collapses.
		return context.WithTimeout(parent, hard)

	default:
		// soft and hard both set, hard > soft (Validate enforces).
		// We layer a hard cancel on top of the soft deadline.
		return e.withSoftHard(parent, soft, hard)
	}
}

// withSoftHard installs a two-stage deadline.
//
// Implementation note: context.WithTimeout already supports a single
// deadline. We can't simply use both because the *latest* deadline
// would govern (context picks the earliest). Instead we:
//
//  1. Wrap parent with a manually-cancellable ctx.
//  2. Arm two timers — soft fires at +soft, hard at +hard.
//  3. The soft timer cancels the ctx with a sentinel cause
//     (DeadlineExceeded). wazero observes it and the guest sees a
//     trap. A well-behaved guest unwinds and the call returns
//     normally.
//  4. The hard timer fires only if the call is still running at
//     +hard — meaning the soft cancel did not unblock the call. It
//     re-cancels (no-op on already-cancelled ctx) and the caller's
//     deferred cancel teardown reaps the timers.
//
// In practice the second cancel is belt-and-braces: ctx is already
// cancelled by the soft timer, so wazero's guest interruption fires
// from the soft signal. The hard timer exists for the future where
// wazero adds a kill path (e.g. interpreter-mode instruction-budget
// interruption) distinct from ctx cancellation.
func (e *Enforcer) withSoftHard(parent context.Context, soft, hard time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancelCause(parent)

	// done is closed when the caller invokes the returned CancelFunc
	// (or when ctx.Done fires for any other reason). The timer
	// goroutines watch done so they tear themselves down promptly
	// when the call finishes early.
	var teardownOnce sync.Once
	done := make(chan struct{})

	softTimer := time.NewTimer(soft)
	hardTimer := time.NewTimer(hard)

	// Soft goroutine: cancel ctx with DeadlineExceeded after the soft
	// budget. The guest sees the ctx trap and ideally unwinds.
	go func() {
		select {
		case <-softTimer.C:
			cancel(fmt.Errorf("%w: soft cpu deadline (%v)", context.DeadlineExceeded, soft))
		case <-done:
		}
	}()

	// Hard goroutine: belt-and-braces second cancel if the guest is
	// still running at +hard. Same ctx, same cause type so callers
	// using errors.Is(err, context.DeadlineExceeded) still match.
	go func() {
		select {
		case <-hardTimer.C:
			cancel(fmt.Errorf("%w: hard cpu deadline (%v)", context.DeadlineExceeded, hard))
		case <-done:
		}
	}()

	cancelFn := func() {
		teardownOnce.Do(func() {
			close(done)
			softTimer.Stop()
			hardTimer.Stop()
			cancel(nil)
		})
	}
	return ctx, cancelFn
}

// Acquire reserves an instance slot for plugin `name`. It returns an
// in-use error when the cap is hit and a release func otherwise.
//
// MaxInstancesPerPlugin == 0 in the Limits disables the check; Acquire
// always succeeds and the release func is still safe to call.
//
// The release func is idempotent: repeat calls only decrement once.
//
// Acquire is the seam the pool (#9) plugs into. It does NOT block — a
// pool that wants to wait for a slot does so externally. The contract
// is "is there room right now? yes/no."
func (e *Enforcer) Acquire(name string) (release func(), err error) {
	if e.Limits.MaxInstancesPerPlugin <= 0 {
		// Limit disabled — always succeed with a no-op release.
		return func() {}, nil
	}
	if name == "" {
		return nil, errors.New("limits: Acquire: plugin name is required")
	}

	counter := e.counterFor(name)
	cap := int64(e.Limits.MaxInstancesPerPlugin)

	// Optimistic add-and-check: bump, look, undo if over.
	// Cheaper than holding a mutex on the hot path and the worst case
	// is a momentary overshoot we immediately reverse.
	new := atomic.AddInt64(counter, 1)
	if new > cap {
		atomic.AddInt64(counter, -1)
		return nil, fmt.Errorf("%w: plugin=%q cap=%d", ErrInstanceLimitReached, name, cap)
	}

	var released atomic.Bool
	return func() {
		if released.CompareAndSwap(false, true) {
			atomic.AddInt64(counter, -1)
		}
	}, nil
}

// InstanceCount returns the current number of live instances for
// plugin `name`. Exposed for diagnostics, admin endpoints, and tests.
func (e *Enforcer) InstanceCount(name string) int {
	v, ok := e.instances.Load(name)
	if !ok {
		return 0
	}
	return int(atomic.LoadInt64(v.(*int64)))
}

// counterFor returns the *int64 counter for plugin `name`, creating
// it on first use under a mutex so two concurrent first-callers can't
// race to install separate counters.
func (e *Enforcer) counterFor(name string) *int64 {
	if v, ok := e.instances.Load(name); ok {
		return v.(*int64)
	}
	e.instancesMu.Lock()
	defer e.instancesMu.Unlock()
	if v, ok := e.instances.Load(name); ok {
		return v.(*int64)
	}
	var c int64
	e.instances.Store(name, &c)
	return &c
}
