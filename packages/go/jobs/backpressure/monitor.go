package backpressure

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hibiken/asynq"
)

// QueueInspector is the subset of *asynq.Inspector the Monitor needs.
// We define an interface so tests can supply a fake that returns
// controllable Pending values without standing up Redis. The production
// wiring passes *asynq.Inspector directly — it satisfies the interface
// because GetQueueInfo's signature matches.
type QueueInspector interface {
	GetQueueInfo(queue string) (*asynq.QueueInfo, error)
}

// Monitor polls a QueueInspector at a fixed interval and exposes the
// most recent Pending count per configured queue via Depth. Reads from
// Depth are lock-free (atomic load) so the Gate fast-path stays cheap
// even when /enqueue is hammered.
//
// One Monitor per asynq cluster suffices; multiple Gates and middleware
// instances can share it. The struct is safe to use after Run returns
// (Depth keeps returning the last observed value); it is also safe to
// construct without ever calling Run (Depth returns 0 — fail-open), so
// tests that only exercise the Gate need not spin up the goroutine.
type Monitor struct {
	// Inspector pulls QueueInfo from Redis. Required.
	Inspector QueueInspector

	// Thresholds is the set of queues to poll. Only queues named here
	// are sampled — saving an Inspector round-trip per unconfigured
	// queue. The Limit fields are ignored by the Monitor itself; they
	// belong to the Gate. We accept the same []Threshold slice to keep
	// the call site honest (one config block, one source of truth).
	Thresholds []Threshold

	// Interval is the poll cadence. Defaults to 1s if zero. The cost
	// of a poll is len(Thresholds) Redis round-trips; 1s × 7 queues is
	// well within budget for a small-instance Redis. Operators tune
	// this down if they want faster shed response.
	Interval time.Duration

	// Logger receives Warn lines when GetQueueInfo returns an error
	// (Redis unavailable, queue name typo). nil is allowed for tests.
	Logger *slog.Logger

	// depths is the latest observed Pending count per queue, written
	// atomically by the poll goroutine and read lock-free by Depth.
	// Keys are queue names; values point to int64 atomics so each
	// queue's slot is independently mutable. The map itself is built
	// once in init() and never reshaped; readers hold no lock.
	depths map[string]*atomic.Int64

	// initOnce guards depths initialization, called either by Run on
	// first invocation or by Depth's first read (whichever wins). The
	// double-checked init lets Depth be safe to call before Run starts.
	initOnce sync.Once
}

const defaultMonitorInterval = 1 * time.Second

// init lazily builds the depths map from Thresholds. Called from both
// Run (so the map exists before the first poll) and Depth (so a Gate
// constructed before Run.Start can still read zeroes safely). Idempotent.
func (m *Monitor) init() {
	m.initOnce.Do(func() {
		m.depths = make(map[string]*atomic.Int64, len(m.Thresholds))
		for _, t := range m.Thresholds {
			if _, ok := m.depths[t.Queue]; ok {
				continue
			}
			m.depths[t.Queue] = new(atomic.Int64)
		}
	})
}

// Run polls Inspector.GetQueueInfo for each configured queue at
// m.Interval and writes the Pending count into the per-queue atomic.
// Returns when ctx is canceled; never returns an error (per-queue
// inspector failures are logged and the previous depth is retained — a
// transient Redis blip should not cause the gate to flip).
//
// Run is a long-lived goroutine target; typical wiring is:
//
//	go monitor.Run(ctx)
//
// Calling Run more than once concurrently is allowed but produces
// duplicate polls; production wiring runs it exactly once per Monitor.
func (m *Monitor) Run(ctx context.Context) error {
	m.init()
	interval := m.Interval
	if interval <= 0 {
		interval = defaultMonitorInterval
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()

	// Sample once immediately so Depth has fresh data before the first
	// tick; without this the first Interval window would see zeroes,
	// which fails-open (no shedding) but masks a real backlog at boot.
	m.sample()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			m.sample()
		}
	}
}

// sample issues one GetQueueInfo round-trip per configured queue and
// stores Pending into the corresponding atomic. Errors are logged but
// don't propagate — the previous value is retained so a transient
// Inspector failure can't accidentally open the gate.
func (m *Monitor) sample() {
	if m.Inspector == nil {
		return
	}
	for _, t := range m.Thresholds {
		info, err := m.Inspector.GetQueueInfo(t.Queue)
		if err != nil {
			if m.Logger != nil {
				m.Logger.Warn("backpressure: GetQueueInfo failed; retaining previous depth",
					slog.String("queue", t.Queue),
					slog.String("err", err.Error()),
				)
			}
			continue
		}
		if slot, ok := m.depths[t.Queue]; ok {
			slot.Store(int64(info.Pending))
		}
	}
}

// Depth returns the most recently observed Pending count for queue.
// Unknown queues return 0 — the Gate treats 0 as "well below SoftLimit"
// so the effect is fail-open admission. Returning a sentinel like -1
// would force every caller to special-case the lookup, which we'd
// rather avoid; the operator-facing contract is "configure thresholds
// for the queues you care about".
//
// Safe for concurrent use; reads are atomic.
func (m *Monitor) Depth(queue string) int {
	m.init()
	slot, ok := m.depths[queue]
	if !ok {
		return 0
	}
	return int(slot.Load())
}

// setDepth writes the per-queue atomic directly. Exposed at package
// scope (lowercase) for the same-package _test files that drive Gate
// and Middleware behavior across depth transitions without needing a
// fake Inspector + goroutine. The queue must already exist in
// Thresholds; this method never mutates the depths map (only the
// atomics inside it) so it remains race-clean with concurrent Depth
// readers.
func (m *Monitor) setDepth(queue string, depth int) {
	m.init()
	if slot, ok := m.depths[queue]; ok {
		slot.Store(int64(depth))
	}
}
