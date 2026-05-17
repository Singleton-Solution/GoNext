package pool

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/plugins/runtime"
)

// Sentinel errors. errors.Is matches; users do not need to unwrap.
var (
	// ErrPoolClosed is returned by Checkout (and surfaces from Return
	// for an instance that was checked out before Close) when the
	// pool has shut down.
	ErrPoolClosed = errors.New("pool: pool is closed")

	// ErrCheckoutTimeout is returned by Checkout when the supplied
	// ctx is cancelled or its deadline expires while waiting for an
	// idle instance. The pool's bookkeeping is unchanged on this
	// path — no leak, no phantom InUse increment.
	ErrCheckoutTimeout = errors.New("pool: checkout timed out")

	// ErrInvalidConfig is returned by NewPool when the supplied
	// Config is malformed (Min > Max, no Runtime, empty WasmBytes,
	// etc).
	ErrInvalidConfig = errors.New("pool: invalid config")
)

// Default constants. NewPool fills these in when the corresponding
// Config field is left at its zero value, so callers can spell out
// only the knobs that matter for their workload.
const (
	defaultMinInstances       = 1
	defaultMaxInstances       = 8
	defaultMaxIdleTime        = 5 * time.Minute
	defaultMaxUsesPerInstance = 0 // 0 == unlimited
	defaultReapInterval       = 30 * time.Second
)

// Config configures a Pool. NewPool validates the config and applies
// defaults; callers can pass a zero Config and get a 1-to-8 pool
// with a 5-minute idle cap and no use ceiling.
type Config struct {
	// Runtime is the wazero runtime that compiles WasmBytes for this
	// pool. Required.
	Runtime *runtime.Runtime

	// WasmBytes is the compiled plugin's module. Each instance the
	// pool creates calls Runtime.LoadModule with these bytes under
	// a fabricated unique name. Required.
	WasmBytes []byte

	// PluginName is the human-readable label for the plugin. The
	// pool combines it with a monotonic sequence to produce the
	// unique name passed to LoadModule. Optional; if empty,
	// "plugin" is used.
	PluginName string

	// MinInstances is the minimum number of instances the pool
	// keeps alive. Pool.Start pre-warms this many; the reaper
	// refills if eviction takes the live count below it. Default 1.
	MinInstances int

	// MaxInstances is the upper bound on live instances. Checkouts
	// that arrive while every instance is in use block until either
	// (a) someone Returns or (b) the caller's ctx expires. Default
	// 8.
	MaxInstances int

	// MaxIdleTime is the soft eviction threshold for the reaper. An
	// instance that has sat in the idle slice for longer than
	// MaxIdleTime is closed on the next reap tick, unless closing
	// would take the pool below MinInstances. Default 5 minutes.
	// Zero disables idle eviction.
	MaxIdleTime time.Duration

	// MaxUsesPerInstance, if positive, recycles an instance after
	// it has been checked out this many times. Useful when the
	// guest accumulates state (heap fragmentation, leaks) that the
	// host can't otherwise reset. Default 0 (unlimited).
	MaxUsesPerInstance int64

	// ReapInterval is how often the reaper goroutine wakes to
	// check for idle eviction. Default 30 seconds. Set to a very
	// small value in tests to make idle eviction observable
	// quickly.
	ReapInterval time.Duration

	// Metrics, if non-nil, receives counter/gauge updates. A nil
	// Metrics turns into a no-op pool — useful in tests that don't
	// care about observability.
	Metrics *Metrics

	// now is the time source for the reaper / lastUsed stamping.
	// Hidden from the public API; tests use it to make idle
	// eviction deterministic. The pool defaults to time.Now.
	now func() time.Time
}

// Pool holds a fixed-bounded set of pre-instantiated wazero modules.
// See package doc for the rationale.
//
// The zero Pool is unusable; construct via NewPool.
type Pool struct {
	cfg Config

	// mu guards every mutable field below. The pool deliberately
	// uses one mutex rather than a finer-grained scheme because:
	//   - Checkout/Return need atomicity across the idle slice AND
	//     the InUse gauge AND the cond.Signal wake-up.
	//   - Even at 100 goroutines × 1000 checkouts (the race-test
	//     case), contention on a single mutex is well under the
	//     guest call latency.
	mu sync.Mutex

	// cond.L == &mu. Checkout waits on cond when idle is empty and
	// live == MaxInstances. Return / reaper Signal cond after
	// modifying the idle slice.
	cond *sync.Cond

	// idle is the LIFO stack of available instances. We pull from
	// the back (most recently used) so a steady-state workload
	// reuses a hot working set rather than rotating the entire
	// pool — kinder to wazero's internal caches.
	idle []*instance

	// live is the total instance count (idle + checked out). The
	// pool refuses to create new instances when live ==
	// MaxInstances; the reaper refills up to MinInstances.
	live int

	// nextID is the monotonic counter that produces unique module
	// names ("<plugin>#<n>"). Wraps would require many billion
	// recycles per pool; safe in practice.
	nextID atomic.Int64

	// closed flips to true on Close. Checkout/Return/reaper all
	// check it.
	closed atomic.Bool

	// outstanding is the count of checked-out leases. Pool.Close
	// waits on outstandingDone until this reaches zero.
	outstanding atomic.Int64
	doneMu      sync.Mutex
	doneCh      chan struct{}

	// reaperStop signals the reaper goroutine to exit. Closed by
	// Close. Buffered/unbuffered does not matter: Close is the
	// only sender.
	reaperStop chan struct{}
	reaperDone chan struct{}
}

// NewPool validates cfg, applies defaults, and starts the reaper
// goroutine. It does NOT pre-instantiate MinInstances modules; call
// Pool.Start for that. (Lazy pre-warming makes NewPool cheap and
// keeps the construction phase synchronous-failure-free, leaving the
// LoadModule errors to surface from the explicit Start.)
func NewPool(cfg Config) (*Pool, error) {
	if cfg.Runtime == nil {
		return nil, fmt.Errorf("%w: Runtime is required", ErrInvalidConfig)
	}
	if len(cfg.WasmBytes) == 0 {
		return nil, fmt.Errorf("%w: WasmBytes is required", ErrInvalidConfig)
	}
	if cfg.PluginName == "" {
		cfg.PluginName = "plugin"
	}
	if cfg.MinInstances < 0 {
		return nil, fmt.Errorf("%w: MinInstances must be >= 0", ErrInvalidConfig)
	}
	if cfg.MaxInstances <= 0 {
		cfg.MaxInstances = defaultMaxInstances
	}
	if cfg.MinInstances == 0 {
		cfg.MinInstances = defaultMinInstances
	}
	if cfg.MinInstances > cfg.MaxInstances {
		return nil, fmt.Errorf("%w: MinInstances (%d) > MaxInstances (%d)",
			ErrInvalidConfig, cfg.MinInstances, cfg.MaxInstances)
	}
	if cfg.MaxIdleTime == 0 {
		cfg.MaxIdleTime = defaultMaxIdleTime
	}
	if cfg.MaxUsesPerInstance < 0 {
		cfg.MaxUsesPerInstance = defaultMaxUsesPerInstance
	}
	if cfg.ReapInterval == 0 {
		cfg.ReapInterval = defaultReapInterval
	}
	if cfg.now == nil {
		cfg.now = time.Now
	}

	p := &Pool{
		cfg:        cfg,
		idle:       make([]*instance, 0, cfg.MaxInstances),
		reaperStop: make(chan struct{}),
		reaperDone: make(chan struct{}),
		doneCh:     make(chan struct{}),
	}
	p.cond = sync.NewCond(&p.mu)

	go p.reaperLoop()

	return p, nil
}

// Start pre-instantiates MinInstances modules so the first
// MinInstances checkouts skip the LoadModule cost. Idempotent:
// repeat calls do nothing.
//
// If any instance creation fails, every instance that succeeded so
// far is closed and the error is returned. The pool itself remains
// usable; lazy creation in Checkout still works on subsequent calls.
func (p *Pool) Start(ctx context.Context) error {
	if p.closed.Load() {
		return ErrPoolClosed
	}

	p.mu.Lock()
	needed := p.cfg.MinInstances - p.live
	p.mu.Unlock()
	if needed <= 0 {
		return nil
	}

	created := make([]*instance, 0, needed)
	for i := 0; i < needed; i++ {
		inst, err := p.createInstance(ctx)
		if err != nil {
			// Roll back: close everything we just made.
			for _, c := range created {
				_ = c.close(context.Background())
			}
			p.mu.Lock()
			p.live -= len(created)
			p.mu.Unlock()
			p.observePoolSize()
			return fmt.Errorf("pool: Start: create instance %d: %w", i, err)
		}
		created = append(created, inst)
	}

	p.mu.Lock()
	for _, inst := range created {
		inst.markReturned(p.cfg.now())
		p.idle = append(p.idle, inst)
	}
	p.live += len(created)
	p.mu.Unlock()
	p.observePoolSize()
	return nil
}

// Checkout returns a Lease wrapping an idle instance. If every
// instance is in use and live < MaxInstances, Checkout creates a new
// one. If live == MaxInstances, Checkout blocks until Return is
// called or ctx is cancelled.
//
// Errors:
//
//   - ErrPoolClosed if the pool has been closed.
//   - ErrCheckoutTimeout if ctx is cancelled or its deadline expires.
//     The error wraps ctx.Err() so errors.Is(err, context.Canceled)
//     or context.DeadlineExceeded works.
//   - Any LoadModule error if the pool had to lazily create a new
//     instance and wazero rejected the bytes (rare — the bytes were
//     presumably validated when the pool was constructed).
//
// On success the caller MUST eventually call Lease.Return. The
// pool's accounting (InUse gauge, idle slice, MaxInstances cap) all
// assume every successful Checkout is paired with one Return.
func (p *Pool) Checkout(ctx context.Context) (*Lease, error) {
	if p.closed.Load() {
		p.observeCheckoutError()
		return nil, ErrPoolClosed
	}

	startWait := p.cfg.now()

	// Wait phase: pop from idle or create if there's headroom. If
	// neither is possible (live == max, idle empty), block on cond
	// until Return / Close wakes us. A goroutine separate from the
	// pool watches ctx and broadcasts on cancellation so the
	// blocked Checkout doesn't get stranded.
	p.mu.Lock()

	// ctxStop terminates the watcher goroutine on the happy path
	// (we got an instance before ctx fired). Without it we'd leak
	// one goroutine per Checkout.
	ctxStop := make(chan struct{})
	if ctx.Done() != nil {
		go func() {
			select {
			case <-ctx.Done():
				p.mu.Lock()
				p.cond.Broadcast()
				p.mu.Unlock()
			case <-ctxStop:
			}
		}()
	}

	for {
		if p.closed.Load() {
			p.mu.Unlock()
			close(ctxStop)
			p.observeCheckoutError()
			return nil, ErrPoolClosed
		}
		// Fast path: idle stack non-empty.
		if n := len(p.idle); n > 0 {
			inst := p.idle[n-1]
			p.idle = p.idle[:n-1]
			// markCheckedOut bumps the use counter; the cap (if
			// any) is checked on Return so this counter is the
			// monotonic source of truth for "how many times has
			// this instance been used".
			inst.markCheckedOut()
			p.outstanding.Add(1)
			p.mu.Unlock()
			close(ctxStop)
			p.observeCheckoutOk(startWait)
			return &Lease{pool: p, inst: inst}, nil
		}
		// Lazy create if there's headroom.
		if p.live < p.cfg.MaxInstances {
			// Reserve the slot before releasing the lock — two
			// concurrent Checkouts must not both decide they can
			// create up to MaxInstances+1.
			p.live++
			p.mu.Unlock()
			inst, err := p.createInstance(ctx)
			if err != nil {
				p.mu.Lock()
				p.live--
				p.mu.Unlock()
				close(ctxStop)
				p.observeCheckoutError()
				return nil, fmt.Errorf("pool: Checkout: create: %w", err)
			}
			inst.markCheckedOut()
			p.outstanding.Add(1)
			p.observePoolSize()
			close(ctxStop)
			p.observeCheckoutOk(startWait)
			return &Lease{pool: p, inst: inst}, nil
		}
		// Block on cond. Check ctx first so a cancelled ctx
		// short-circuits even when the pool happens to have idle
		// capacity briefly.
		if err := ctx.Err(); err != nil {
			p.mu.Unlock()
			close(ctxStop)
			p.observeCheckoutError()
			return nil, fmt.Errorf("%w: %w", ErrCheckoutTimeout, err)
		}
		p.cond.Wait()
	}
}

// returnLease is the back half of Checkout. Called from Lease.Return
// after the Lease's CAS guard has fired exactly once. The pool
// decides whether to recycle the instance (trapped or hit
// MaxUsesPerInstance) or return it to idle.
func (p *Pool) returnLease(l *Lease) error {
	inst := l.inst
	p.outstanding.Add(-1)

	// Trap dominates everything else: a poisoned instance must not
	// re-enter rotation. Use cap exhaustion is the second reason.
	// Pool closure is third — Close itself drains everything; this
	// branch handles the "returned during Close()" race.
	trapped := inst.isTrapped()
	atUseCap := p.cfg.MaxUsesPerInstance > 0 && inst.useCount.Load() >= p.cfg.MaxUsesPerInstance
	poolClosed := p.closed.Load()

	if trapped || atUseCap || poolClosed {
		reason := RecycleReasonClose
		switch {
		case trapped:
			reason = RecycleReasonTrap
		case atUseCap:
			reason = RecycleReasonMaxUses
		}
		err := inst.close(context.Background())

		p.mu.Lock()
		p.live--
		// Wake a waiter — the pool now has headroom to lazily
		// create. Broadcast (not Signal) so blocked Checkouts that
		// missed the headroom check race-window also retry.
		p.cond.Broadcast()
		p.mu.Unlock()

		p.observeRecycle(reason)
		p.observePoolSize()
		p.observeInUseDec()
		p.checkDrainComplete()
		return err
	}

	// Healthy return: stack the instance back, stamp lastUsed,
	// wake one waiter.
	p.mu.Lock()
	inst.markReturned(p.cfg.now())
	p.idle = append(p.idle, inst)
	p.cond.Signal()
	p.mu.Unlock()

	p.observeInUseDec()
	p.checkDrainComplete()
	return nil
}

// Close shuts the pool down. It:
//
//  1. Flips the closed flag so further Checkouts fail-fast.
//  2. Stops the reaper.
//  3. Waits for outstanding leases to be Returned (up to ctx).
//  4. Closes every idle instance.
//
// If ctx expires before outstanding leases are all Returned, Close
// returns ctx.Err(); the pool is still closed but the unreturned
// leases will be discarded once their owners eventually Return them
// (the instances will be closed on that path).
//
// Idempotent — second Close returns nil immediately.
func (p *Pool) Close(ctx context.Context) error {
	if !p.closed.CompareAndSwap(false, true) {
		return nil
	}

	// Tell the reaper to exit and wait for it. We do this before
	// the lease drain so the reaper isn't poking at the idle slice
	// while Close tears it down.
	close(p.reaperStop)
	<-p.reaperDone

	// Wake every Checkout currently blocked on cond. They re-check
	// closed and return ErrPoolClosed.
	p.mu.Lock()
	p.cond.Broadcast()
	p.mu.Unlock()

	// Wait for outstanding == 0 OR ctx cancellation.
	if p.outstanding.Load() > 0 {
		p.checkDrainComplete()
		select {
		case <-p.doneCh:
		case <-ctx.Done():
			// Don't proactively close anything: the outstanding
			// leases will close their own instances via Return.
			return fmt.Errorf("pool: Close: %w", ctx.Err())
		}
	}

	// Now drain idle. Use background ctx so the caller's already-
	// cancelled ctx (if any) doesn't truncate the wazero close.
	p.mu.Lock()
	idle := p.idle
	p.idle = nil
	p.live = 0
	p.mu.Unlock()

	var firstErr error
	for _, inst := range idle {
		if err := inst.close(context.Background()); err != nil && firstErr == nil {
			firstErr = err
		}
		p.observeRecycle(RecycleReasonClose)
	}
	p.observePoolSize()
	return firstErr
}

// IsClosed reports whether Close has been called.
func (p *Pool) IsClosed() bool { return p.closed.Load() }

// Stats is a point-in-time snapshot of the pool. Cheap; tests use
// it to assert invariants.
type Stats struct {
	Live        int
	Idle        int
	InUse       int64
	Outstanding int64
}

// Stats returns the current Stats.
func (p *Pool) Stats() Stats {
	p.mu.Lock()
	defer p.mu.Unlock()
	return Stats{
		Live:        p.live,
		Idle:        len(p.idle),
		InUse:       p.outstanding.Load(),
		Outstanding: p.outstanding.Load(),
	}
}

// createInstance is the LoadModule wrapper that fabricates a unique
// name. Used by Start, Checkout (lazy create), and the reaper
// (refill).
func (p *Pool) createInstance(ctx context.Context) (*instance, error) {
	id := p.nextID.Add(1)
	name := fmt.Sprintf("%s#%d", p.cfg.PluginName, id)
	mod, err := p.cfg.Runtime.LoadModule(ctx, name, p.cfg.WasmBytes)
	if err != nil {
		return nil, err
	}
	return &instance{
		module:     mod,
		uniqueName: name,
	}, nil
}

// observeCheckoutOk records a successful checkout in metrics.
func (p *Pool) observeCheckoutOk(start time.Time) {
	if p.cfg.Metrics == nil {
		return
	}
	wait := p.cfg.now().Sub(start).Seconds()
	if wait < 0 {
		wait = 0
	}
	p.cfg.Metrics.CheckoutTotal.Inc()
	p.cfg.Metrics.CheckoutWaitSeconds.Observe(wait)
	p.cfg.Metrics.InUse.Inc()
}

// observeCheckoutError records a failed checkout.
func (p *Pool) observeCheckoutError() {
	if p.cfg.Metrics == nil {
		return
	}
	p.cfg.Metrics.CheckoutErrors.Inc()
}

// observeRecycle records an instance disposal.
func (p *Pool) observeRecycle(reason string) {
	if p.cfg.Metrics == nil {
		return
	}
	p.cfg.Metrics.RecycleTotal.WithLabelValues(reason).Inc()
}

// observePoolSize syncs the PoolSize gauge with the current live
// count. Called after every transition that changes live.
func (p *Pool) observePoolSize() {
	if p.cfg.Metrics == nil {
		return
	}
	p.mu.Lock()
	live := p.live
	p.mu.Unlock()
	p.cfg.Metrics.PoolSize.Set(float64(live))
}

// observeInUseDec is the InUse decrement on healthy Return. Recycle
// paths also decrement InUse, but indirectly via the pool transition
// (recycle is "in use → closed", not "in use → idle"); we decrement
// there too to keep the gauge balanced.
func (p *Pool) observeInUseDec() {
	if p.cfg.Metrics == nil {
		return
	}
	p.cfg.Metrics.InUse.Dec()
}

// checkDrainComplete fires the doneCh when outstanding hits zero
// after a Return during Close. Safe to call any time; only signals
// when the pool is closed AND outstanding is zero.
func (p *Pool) checkDrainComplete() {
	if !p.closed.Load() {
		return
	}
	if p.outstanding.Load() != 0 {
		return
	}
	p.doneMu.Lock()
	select {
	case <-p.doneCh:
		// already closed
	default:
		close(p.doneCh)
	}
	p.doneMu.Unlock()
}
