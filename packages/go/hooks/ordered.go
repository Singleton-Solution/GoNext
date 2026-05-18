package hooks

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// ErrOrderedBacklog is returned (and logged) when an ordered-async dispatch
// cannot be enqueued because the per-key buffer is full and the configured
// enqueue timeout elapses before a slot frees up.
//
// Strict-ordered actions trade throughput for ordering — a slow subscriber
// for key K stalls every event for K. Backpressure is the explicit signal
// that "K's queue is overflowing"; the caller's response is up to them
// (drop, log, alert, escalate). We surface a typed sentinel so callers can
// errors.Is against it without string matching.
var ErrOrderedBacklog = errors.New("hooks: ordered-async backlog full")

// OrderedAsync is a per-action option that turns RegisterAsync subscribers
// for the named action into a strict-ordered fan-out: events with the same
// KeyFn-derived key are processed by a single per-key worker goroutine in
// submission order, while events with different keys can be processed in
// parallel.
//
// The motivating case is `posts.saved` for a specific post ID: an "updated"
// event must reach every async subscriber before the "deleted" event for
// the same post does, even though async dispatch normally fans out via
// independent goroutines (and ordering between them is unspecified).
//
// Why a per-action option and not a per-handler one: ordering is a property
// of the EVENT STREAM, not of any individual subscriber. If three plugins
// subscribe to `posts.saved` and one wants ordering, all three need it —
// otherwise an unordered subscriber observes "deleted" before "updated"
// and the invariant the ordered subscribers rely on breaks. We attach the
// flag to the action name so every async subscriber for that name shares
// the ordering guarantee.
//
// Trade-offs:
//
//   - Throughput: same-key events serialize. A pathological key (every
//     event has the same key) collapses to a single goroutine.
//   - Memory: the dispatcher keeps a goroutine per active key. Idle workers
//     are reaped after IdleTimeout (default 30s) so a transient burst
//     doesn't permanently inflate goroutine count.
//   - Backpressure: when a key's bounded buffer is full, the enqueue blocks
//     up to EnqueueTimeout. Past the timeout, ErrOrderedBacklog is logged
//     (the caller of Do has already moved on; we have no return channel).
type OrderedAsync struct {
	// KeyFn extracts an ordering key from the payload passed to Do. The
	// returned string is the per-key serializer's identity; the empty
	// string is a valid key and is treated like any other (all events
	// whose KeyFn returns "" serialize against each other).
	//
	// KeyFn is called once per Do, from the goroutine that called Do, so
	// it MUST be cheap and side-effect free. A panic in KeyFn is recovered
	// and logged; the event falls through to non-ordered async dispatch
	// so it is not silently dropped.
	//
	// The payload passed to KeyFn is the full args slice handed to Do —
	// the caller picks whichever element carries the ordering key.
	KeyFn func(payload []any) string

	// BufferSize bounds the per-key channel. Zero means use the default
	// (orderedDefaultBuffer). Set to a larger value when bursts are
	// expected; set lower to fail-fast on backpressure.
	BufferSize int

	// EnqueueTimeout caps how long an enqueue will block waiting for a
	// slot. Zero means use the default (orderedDefaultEnqueueTimeout). On
	// timeout the event is dropped and ErrOrderedBacklog is logged.
	EnqueueTimeout time.Duration

	// IdleTimeout caps how long a per-key worker may sit idle before the
	// reaper releases its goroutine. Zero means use the default
	// (orderedDefaultIdleTimeout). Reaping is best-effort — a worker may
	// linger past IdleTimeout if the reaper hasn't ticked recently.
	IdleTimeout time.Duration
}

// Defaults chosen so a misconfigured OrderedAsync still works:
//
//   - BufferSize 64: enough for typical event bursts (posts.saved for a
//     hot post in a write loop) without unbounded memory.
//   - EnqueueTimeout 5s: long enough that a brief stall is absorbed,
//     short enough that a wedged subscriber surfaces as backpressure
//     rather than blocking the producer forever.
//   - IdleTimeout 30s: matches the WordPress-compat heartbeat — after a
//     half-minute of silence the key is "cold" and its goroutine should
//     not be holding a stack.
const (
	orderedDefaultBuffer         = 64
	orderedDefaultEnqueueTimeout = 5 * time.Second
	orderedDefaultIdleTimeout    = 30 * time.Second

	// orderedReaperInterval is how often the dispatcher scans for idle
	// keys to retire. The reaper trades a small periodic wakeup for
	// bounded memory growth; tuning is not exposed because the value is
	// not load-bearing for correctness.
	orderedReaperInterval = 5 * time.Second
)

// SetActionOptions attaches options to an action name. Currently the only
// supported option is OrderedAsync; future options (rate limiting, etc.)
// can be wired through the same call without breaking the existing one.
//
// Passing a zero OrderedAsync (KeyFn == nil) removes the ordering and
// reverts the action to unordered async dispatch. Existing in-flight
// ordered events are NOT cancelled — they drain on their respective
// per-key workers, and only subsequent Do calls see the new configuration.
//
// Safe for concurrent use; the swap is atomic relative to other reads.
// In practice this is called at startup (plugin registration time) so
// contention is not a concern.
func (b *Bus) SetActionOptions(name string, opts OrderedAsync) {
	b.actionOpts.Store(name, opts)
}

// orderedDispatcher returns the lazy-initialized per-Bus ordered dispatcher.
// Initialization is sync.Once-style: the first ordered Do triggers it; the
// reaper goroutine is parented on a context the dispatcher owns.
func (b *Bus) orderedDispatcher() *orderedDispatcher {
	if d := b.ordered.Load(); d != nil {
		return d
	}
	d := newOrderedDispatcher(b)
	if !b.ordered.CompareAndSwap(nil, d) {
		// Another goroutine raced us; release our shadow and use theirs.
		d.stop()
		return b.ordered.Load()
	}
	return d
}

// orderedDispatcher serializes events with the same key through a per-key
// goroutine + bounded channel. Different keys run in parallel; idle keys
// are reaped after a timeout.
//
// The map is guarded by mu rather than sync.Map because the reaper needs
// to enumerate-and-mutate atomically (snapshot, decide who's idle, delete
// in one critical section). A sync.Map would let the reaper observe an
// entry mid-mutation; the keyed-mutex cost is negligible compared to the
// goroutine cost of even a single per-key worker.
type orderedDispatcher struct {
	bus *Bus

	mu      sync.Mutex
	workers map[string]*orderedWorker

	stopCh chan struct{}
	stopWG sync.WaitGroup
}

// orderedWorker owns the channel and goroutine for a single ordering key.
// lastActive is updated every enqueue and consume so the reaper can tell
// "idle since when". closed flags a worker whose goroutine has been told
// to exit; an enqueue racing with reap re-creates a fresh worker rather
// than reviving a closed one (simpler than coordinating a wake signal).
type orderedWorker struct {
	key        string
	ch         chan orderedJob
	stop       chan struct{} // closed by reaper to signal goroutine exit
	lastActive atomic.Int64  // unix nano
	closed     atomic.Bool

	bufferSize     int
	enqueueTimeout time.Duration
	idleTimeout    time.Duration
}

// orderedJob is the unit of work carried over a worker's channel. We pin
// the registration snapshot taken at enqueue time so that a subsequent
// unregister doesn't change which subscribers receive the event (matches
// the "newly registered handlers do not join in-flight dispatch" rule
// from the bus docstring).
type orderedJob struct {
	ctx     context.Context
	name    string
	regs    chain
	args    []any
	enqueue time.Time
}

func newOrderedDispatcher(b *Bus) *orderedDispatcher {
	d := &orderedDispatcher{
		bus:     b,
		workers: make(map[string]*orderedWorker),
		stopCh:  make(chan struct{}),
	}
	d.stopWG.Add(1)
	go d.reaper()
	return d
}

// stop shuts down the reaper goroutine. It is exposed for the rare
// dispatcher-was-raced path in orderedDispatcher() — actual Bus shutdown
// is handled via Wait, which drains worker goroutines individually.
func (d *orderedDispatcher) stop() {
	close(d.stopCh)
	d.stopWG.Wait()
}

// reaper periodically retires workers that have been idle longer than
// their configured IdleTimeout. The reaper is conservative — it only
// retires a worker whose channel is empty AND whose lastActive is past
// the cutoff. This prevents a tight race where the reaper observes an
// idle worker that was about to receive a new job (the producer sees
// closed==true on its next enqueue and creates a fresh worker).
func (d *orderedDispatcher) reaper() {
	defer d.stopWG.Done()
	t := time.NewTicker(orderedReaperInterval)
	defer t.Stop()
	for {
		select {
		case <-d.stopCh:
			return
		case now := <-t.C:
			d.reapOnce(now)
		}
	}
}

func (d *orderedDispatcher) reapOnce(now time.Time) {
	var toClose []*orderedWorker

	d.mu.Lock()
	for k, w := range d.workers {
		if len(w.ch) > 0 {
			continue
		}
		idle := time.Duration(now.UnixNano() - w.lastActive.Load())
		if idle < w.idleTimeout {
			continue
		}
		// Tombstone the worker before signalling stop so a producer
		// racing us creates a new worker instead of trying to send on
		// a worker whose goroutine is about to exit.
		if !w.closed.CompareAndSwap(false, true) {
			continue
		}
		delete(d.workers, k)
		toClose = append(toClose, w)
	}
	d.mu.Unlock()

	for _, w := range toClose {
		close(w.stop)
	}
}

// dispatch enqueues a job for the given key. Returns ErrOrderedBacklog if
// the per-key buffer is full and the enqueue timeout elapses; nil otherwise.
// A returned error means the event was NOT delivered to any subscriber.
func (d *orderedDispatcher) dispatch(
	ctx context.Context,
	name string,
	key string,
	opts OrderedAsync,
	regs chain,
	args []any,
) error {
	job := orderedJob{
		ctx:     ctx,
		name:    name,
		regs:    regs,
		args:    args,
		enqueue: time.Now(),
	}

	bufSize := opts.BufferSize
	if bufSize <= 0 {
		bufSize = orderedDefaultBuffer
	}
	enqTimeout := opts.EnqueueTimeout
	if enqTimeout <= 0 {
		enqTimeout = orderedDefaultEnqueueTimeout
	}
	idleTimeout := opts.IdleTimeout
	if idleTimeout <= 0 {
		idleTimeout = orderedDefaultIdleTimeout
	}

	// Increment the WaitGroup BEFORE we attempt to enqueue so a racing
	// Bus.Wait() doesn't return between "producer decides to send" and
	// "consumer decremented." If the enqueue ultimately fails (backlog
	// timeout or ctx cancelled) we balance the increment in the failure
	// branches below.
	d.bus.asyncWG.Add(1)

	w := d.workerFor(key, bufSize, enqTimeout, idleTimeout)

	// Fast path: room in the buffer right now.
	select {
	case w.ch <- job:
		w.lastActive.Store(time.Now().UnixNano())
		return nil
	default:
	}

	// Slow path: wait up to enqueueTimeout for a slot. If the worker is
	// retired via stop while we wait, its drain loop will pull our job
	// off the buffer on its way out, so the asyncWG.Add stays balanced.
	timer := time.NewTimer(enqTimeout)
	defer timer.Stop()
	select {
	case w.ch <- job:
		w.lastActive.Store(time.Now().UnixNano())
		return nil
	case <-timer.C:
		d.bus.log().ErrorContext(ctx, "hook ordered async backlog full",
			slog.String("hook", name),
			slog.String("key", key),
			slog.Duration("waited", enqTimeout),
		)
		d.bus.asyncWG.Done()
		return ErrOrderedBacklog
	case <-ctx.Done():
		d.bus.asyncWG.Done()
		return ctx.Err()
	}
}

// workerFor returns the worker for `key`, creating one on first use.
// If the current worker has been tombstoned by the reaper (closed flag),
// it is replaced. The double-check pattern under the lock ensures only
// one fresh worker is created per key even under contention.
func (d *orderedDispatcher) workerFor(key string, bufSize int, enqTimeout, idleTimeout time.Duration) *orderedWorker {
	d.mu.Lock()
	defer d.mu.Unlock()
	if w, ok := d.workers[key]; ok && !w.closed.Load() {
		return w
	}
	w := &orderedWorker{
		key:            key,
		ch:             make(chan orderedJob, bufSize),
		stop:           make(chan struct{}),
		bufferSize:     bufSize,
		enqueueTimeout: enqTimeout,
		idleTimeout:    idleTimeout,
	}
	w.lastActive.Store(time.Now().UnixNano())
	d.workers[key] = w
	// Note: we do NOT add the worker goroutine itself to asyncWG. Workers
	// are long-lived (they sleep waiting for the next event), so tracking
	// them in asyncWG would mean Wait() never returns until the reaper
	// retires them. Instead, asyncWG is incremented on each enqueued JOB
	// and decremented when that job's subscribers finish — see dispatch
	// and runWorker. The reaper closes the channel, ending the goroutine
	// independently of in-flight work.
	go d.runWorker(w)
	return w
}

// runWorker is the per-key goroutine. It pulls jobs off the channel and
// invokes every subscriber synchronously, in priority order, blocking the
// next job until the current one's subscribers have all returned. This is
// the actual ordering guarantee — events for the same key are processed
// one at a time, with all subscribers for event N completing before
// event N+1 starts.
//
// Exit is signalled by closing `stop`. Before exiting the worker drains
// any remaining queued jobs so an in-flight enqueue doesn't lose its
// asyncWG.Add via the never-running consumer.
func (d *orderedDispatcher) runWorker(w *orderedWorker) {
	for {
		select {
		case job := <-w.ch:
			d.runJob(w, job)
			w.lastActive.Store(time.Now().UnixNano())
			// asyncWG counts JOBS not workers; decrement once a job's
			// subscribers have all returned so tests waiting via
			// Bus.Wait see the queue drain without blocking on the
			// worker goroutine.
			d.bus.asyncWG.Done()
		case <-w.stop:
			// Drain any remaining jobs before exiting so the asyncWG
			// stays balanced with the producer-side Add.
			for {
				select {
				case job := <-w.ch:
					d.runJob(w, job)
					d.bus.asyncWG.Done()
				default:
					return
				}
			}
		}
	}
}

func (d *orderedDispatcher) runJob(w *orderedWorker, job orderedJob) {
	for i, reg := range job.regs {
		if !reg.active.Load() {
			continue
		}
		if !reg.async {
			// Should not happen — ordered dispatch is gated on async
			// subscribers — but defend against future code that snapshots
			// the full chain and trusts the ordered path to skip sync.
			continue
		}
		err := d.bus.invokeActionRaw(job.ctx, job.name, i, reg, job.args, true)
		if err != nil {
			d.bus.log().ErrorContext(job.ctx, "ordered async hook handler failed",
				slog.String("hook", job.name),
				slog.String("key", w.key),
				slog.Uint64("token", reg.token),
				slog.Any("err", err),
			)
		}
	}
}

// drainOrderedForTests is a test-only helper exposing the worker count.
// Lives in this file (not _test.go) so the production package compiles
// without it conditional on a test build tag; it's unexported so callers
// outside the package can't reach it.
func (b *Bus) drainOrderedForTests() int {
	d := b.ordered.Load()
	if d == nil {
		return 0
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.workers)
}
