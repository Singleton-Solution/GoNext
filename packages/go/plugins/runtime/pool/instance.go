package pool

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/plugins/runtime"
)

// instance is the pool's bookkeeping wrapper around a single
// runtime.Module. Each instance owns one wazero module, plus the
// timestamps and counters the pool needs to decide when to recycle
// it.
//
// Lifecycle:
//
//	(create) → idle → checked out → idle → ... → closed
//	                       │
//	                       └── (uses ≥ MaxUses) → closed
//	                       └── (trap) → closed on Return
//	                       └── (idle ≥ MaxIdleTime) → closed by reaper
//
// instance is not goroutine-safe by itself — the pool guards every
// transition with its own mutex except for the atomic useCount /
// trapped fields, which the lease may write without the pool lock.
type instance struct {
	// module is the underlying runtime.Module. After close() it is
	// kept set to nil so any stale pointer panics fast rather than
	// silently dispatching into a closed wazero handle.
	module *runtime.Module

	// uniqueName is the name the pool fabricated for the module
	// when it was loaded (e.g. "<pluginName>#7"). The pool tracks
	// this so it can pass the same suffix to LoadModule when
	// replacing the instance, without colliding with the previous
	// generation of the slot's module name (LoadModule rejects
	// duplicate names while the prior instance is still in the
	// runtime's active map).
	uniqueName string

	// lastUsed is the time at which the instance was last returned
	// to the idle slice. The reaper compares this against
	// time.Now() to decide whether to evict.
	//
	// Reads/writes happen exclusively under the pool's mutex, so
	// no atomic is needed; the field is plain.
	lastUsed time.Time

	// useCount is incremented on every successful Checkout. The
	// MaxUsesPerInstance comparison happens on Return; if the
	// instance has hit the cap, the pool closes it instead of
	// putting it back into idle.
	//
	// Atomic because tests may read it concurrently for assertions.
	useCount atomic.Int64

	// trapped is set by Lease.MarkUnusable. On Return, an instance
	// with trapped==true is closed (and a fresh one created if the
	// pool is below MinInstances on the next reap).
	trapped atomic.Bool

	// closed is set when close() runs. Subsequent close() calls
	// return immediately; the pool guarantees close() runs at most
	// once but the flag is cheap defensive coding for the reaper /
	// Close race.
	closed atomic.Bool

	// closeOnce serializes close so the underlying wazero
	// resources are released exactly once even if both the reaper
	// and Pool.Close race to retire the same instance.
	closeOnce sync.Once
}

// markCheckedOut bumps the use counter and returns the new value.
// Called by the pool under its mutex after pulling the instance out
// of the idle slice.
func (i *instance) markCheckedOut() int64 {
	return i.useCount.Add(1)
}

// markReturned stamps lastUsed and is called by the pool under its
// mutex when an instance is put back into the idle slice.
func (i *instance) markReturned(now time.Time) {
	i.lastUsed = now
}

// markTrapped is called from Lease.MarkUnusable. The flag is read on
// Return to decide whether the instance is salvageable.
func (i *instance) markTrapped() { i.trapped.Store(true) }

// isTrapped reports whether the instance has been marked unusable.
func (i *instance) isTrapped() bool { return i.trapped.Load() }

// close shuts down the underlying wazero module. Idempotent.
//
// The supplied ctx is forwarded to runtime.Module.Close. We use a
// fresh context.Background() in some pool paths (e.g. Pool.Close
// after the caller's ctx already expired) so we can still drain the
// wazero state; cancellation of the original ctx must not strand a
// wazero handle.
func (i *instance) close(ctx context.Context) error {
	var err error
	i.closeOnce.Do(func() {
		i.closed.Store(true)
		if i.module != nil {
			err = i.module.Close(ctx)
			i.module = nil
		}
	})
	return err
}

// isClosed reports whether close has run.
func (i *instance) isClosed() bool { return i.closed.Load() }

// Lease is the caller-facing handle for a checked-out instance. It
// is created by Pool.Checkout and must be released by exactly one
// call to Return (or, in failure paths, Close on the pool, which
// drains all outstanding leases).
//
// The zero Lease is invalid. Callers should treat a Lease as a
// scarce resource: defer l.Return() immediately after a successful
// checkout, the same way one defers Mutex.Unlock or rows.Close.
//
// Lease is goroutine-hostile by design — only the goroutine holding
// the Lease should call Module()/MarkUnusable()/Return(). Wazero
// modules serialize internally, so passing a Lease across goroutines
// is technically safe, but it is a smell: the pool's accounting
// (use counters, trapped flag) assumes the caller's lifecycle is
// linear.
type Lease struct {
	pool     *Pool
	inst     *instance
	returned atomic.Bool
}

// Module returns the underlying runtime.Module for the duration of
// the lease. The returned Module is owned by the pool — do NOT
// Close it; the pool decides whether to recycle on Return.
//
// Returns nil if Return has already been called on this Lease.
func (l *Lease) Module() *runtime.Module {
	if l.returned.Load() {
		return nil
	}
	return l.inst.module
}

// MarkUnusable flags the underlying instance for recycle on Return.
// Call this when the caller's plugin invocation trapped (panic, OOB
// memory, ctx-cancellation that surfaced as a wazero trap) — the
// guest's linear memory and globals are now in an indeterminate
// state and reusing the instance risks cascading failures.
//
// Idempotent. Safe to call after Return (it has no effect; the
// instance was already disposed under the trap branch or returned
// to idle).
func (l *Lease) MarkUnusable() {
	if l.inst != nil {
		l.inst.markTrapped()
	}
}

// Return releases the lease back to the pool. Required after every
// successful Checkout — pool.Close blocks indefinitely on leases
// that were never returned.
//
// Idempotent: a second Return is a no-op (returns nil) so callers
// can safely `defer l.Return()` AND explicitly `l.Return()` in
// happy-path code. The double-return guard is necessary because the
// pool's internal accounting (InUse gauge, idle slice) is not
// idempotent.
//
// On Return the pool decides whether to:
//
//   - close the instance (trapped, MaxUsesPerInstance exhausted, or
//     pool already Closed), or
//   - put it back into the idle slice and wake a waiter.
//
// The ctx is used only if the pool needs to close the instance and
// is forwarded to runtime.Module.Close. Callers that don't care can
// pass context.Background().
func (l *Lease) Return() error {
	if !l.returned.CompareAndSwap(false, true) {
		return nil
	}
	return l.pool.returnLease(l)
}
