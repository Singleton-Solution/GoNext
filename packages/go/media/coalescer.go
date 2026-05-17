package media

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"
)

// Metric names emitted by the Coalescer through the injected Counter.
//
// They're const so adapters in other packages (e.g., a Prometheus
// CounterVec wired to these names) can reference the same identifiers.
const (
	// MetricCoalesceTotal is incremented once per follower — a caller
	// that attached to an in-flight generation started by another
	// caller and did NOT execute generate themselves.
	MetricCoalesceTotal = "media_variant_coalesce_total"

	// MetricGenerateTotal is incremented once per leader — a caller
	// whose generate function actually ran.
	MetricGenerateTotal = "media_variant_generate_total"
)

// CoalescerOptions configures a Coalescer. All fields are optional.
type CoalescerOptions struct {
	// Counter receives Inc(name) for each Get. If nil, a no-op counter
	// is used and metrics are silently dropped (Stats() still works).
	Counter Counter

	// Logger receives a debug line for each generate-vs-coalesce
	// decision. If nil, slog.Default() is used. Logging is at Debug
	// level so production callers see nothing by default.
	Logger *slog.Logger

	// KeyExtractor canonicalizes the raw key before singleflight
	// lookup. See SortedQueryKey for the standard query-string
	// canonicalizer. If nil, the raw key is used unchanged.
	KeyExtractor KeyExtractor
}

// Stats is a point-in-time snapshot of the Coalescer's counters.
//
// InFlight is the number of singleflight keys with at least one
// active waiter at the moment of the call.
//
// TotalCoalesced is the cumulative count of followers across the
// Coalescer's lifetime (callers that waited on someone else's
// generation).
//
// TotalGenerated is the cumulative count of leaders (callers whose
// generate function actually ran).
//
// Snapshots are taken without locking the singleflight group, so
// InFlight may race slightly with concurrent Gets in progress —
// it's a live gauge, not a transactional view. The two cumulative
// counters are accurate (atomic).
type Stats struct {
	InFlight       int
	TotalCoalesced int64
	TotalGenerated int64
}

// Coalescer wraps golang.org/x/sync/singleflight with metrics, logging,
// optional key canonicalization, and a Stats() snapshot.
//
// One Coalescer is intended per process per logical "generation pool"
// — typically one for media variants. The zero value is NOT usable;
// construct via NewCoalescer.
//
// Coalescer is safe for concurrent use.
type Coalescer struct {
	group singleflight.Group

	counter      Counter
	logger       *slog.Logger
	keyExtractor KeyExtractor

	// inFlight tracks the number of distinct keys with at least one
	// active waiter. Incremented when a leader's generate begins,
	// decremented when it returns. Read by Stats() for the gauge.
	//
	// We track this ourselves rather than reading singleflight.Group
	// internals because the upstream package deliberately keeps that
	// state private.
	inFlight atomic.Int64

	totalCoalesced atomic.Int64
	totalGenerated atomic.Int64
}

// NewCoalescer constructs a Coalescer with the given options. Returns
// a non-nil *Coalescer that is ready to use.
//
// Options.Counter, Options.Logger, and Options.KeyExtractor are all
// optional; any nil field gets a sensible default (nop counter,
// slog.Default, identity extractor).
func NewCoalescer(opts CoalescerOptions) *Coalescer {
	counter := opts.Counter
	if counter == nil {
		counter = nopCounter{}
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Coalescer{
		counter:      counter,
		logger:       logger,
		keyExtractor: opts.KeyExtractor,
	}
}

// Get fetches the variant identified by key, calling generate exactly
// once across all concurrent callers with the same canonical key.
//
// Behavior:
//
//   - The first caller to arrive on a given key becomes the LEADER. It
//     synchronously runs generate, records the result, increments
//     media_variant_generate_total, and returns (bytes, false, err).
//
//   - Subsequent callers that arrive while the leader's generate is
//     still in flight become FOLLOWERS. They block until the leader
//     finishes, then receive the leader's (bytes, err); they get
//     shared=true and an increment of media_variant_coalesce_total.
//     generate is NOT called for followers.
//
//   - If generate returns an error, ALL waiters (leader and followers)
//     receive the same error. The next Get for the same key after
//     resolution starts a fresh generation — singleflight does not
//     cache failed generations.
//
//   - ctx cancellation in one caller does NOT cancel siblings. Each
//     caller's Get respects only its own ctx, returning early with
//     ctx.Err() while leaving the in-flight generation running for
//     other waiters. This matches the upstream singleflight semantics
//     and is the right choice for the variant pipeline — a client
//     disconnecting should not kill the render that other clients
//     are waiting on.
//
// generate is invoked WITHOUT a context. The variant pipeline is
// disk/CPU-bound libvips work that doesn't take a context anyway, and
// passing one caller's context would create a "first ctx wins"
// surprise for followers. Implementations that need cancellation
// should use a Coalescer-scoped context closed at shutdown.
//
// The canonical key is what gets used internally; if Options.KeyExtractor
// is set, the raw key passed in is mapped through it first.
func (c *Coalescer) Get(ctx context.Context, key string, generate func() ([]byte, error)) ([]byte, bool, error) {
	if generate == nil {
		return nil, false, fmt.Errorf("media.Coalescer.Get: generate must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, false, fmt.Errorf("media.Coalescer.Get: %w", err)
	}

	canonical := key
	if c.keyExtractor != nil {
		canonical = c.keyExtractor(key)
	}

	// ran is set to true ONLY by the goroutine whose generate closure
	// actually executes. singleflight guarantees that the closure runs
	// exactly once across all concurrent Get callers on this key, so
	// exactly one Get's local pointer ends up pointing at this flag's
	// true-set; every other concurrent Get observes its own local
	// ran=false. After their channel receive, that lets each Get
	// decide leader vs follower without racing on any shared map.
	//
	// We pass a pointer down to the closure so the leader's
	// post-generate state is visible to its own caller frame.
	var ran atomic.Bool
	ranPtr := &ran

	resCh := c.group.DoChan(canonical, func() (any, error) {
		// This closure runs in a goroutine spawned by singleflight,
		// exactly once per in-flight key. Whichever Get call's
		// closure was selected by singleflight to run is THE leader;
		// its ran flag will be observed true on the leader's caller
		// frame, while every concurrent follower's local ran is
		// untouched (still false) — which is exactly the invariant
		// we need.
		ranPtr.Store(true)
		c.inFlight.Add(1)
		started := time.Now()
		b, err := generate()
		c.inFlight.Add(-1)
		c.logger.Debug("media.Coalescer: generate finished",
			slog.String("key", canonical),
			slog.Duration("elapsed", time.Since(started)),
			slog.Bool("ok", err == nil),
		)
		return b, err
	})

	// Wait for the leader's result OR for ctx cancellation. Per the
	// docstring contract, ctx cancellation in one caller does NOT
	// cancel siblings — we abandon our wait but leave the singleflight
	// entry intact for others.
	select {
	case <-ctx.Done():
		return nil, false, fmt.Errorf("media.Coalescer.Get: %w", ctx.Err())
	case res := <-resCh:
		var bytes []byte
		if res.Val != nil {
			bytes, _ = res.Val.([]byte)
		}
		// isLeader is true iff this Get's own closure was the one
		// singleflight picked to execute. ran is observed under a
		// happens-before with the channel receive, so the atomic
		// load here sees the store inside the closure.
		isLeader := ran.Load()
		if isLeader {
			c.totalGenerated.Add(1)
			c.counter.Inc(MetricGenerateTotal)
			if res.Err != nil {
				c.logger.Debug("media.Coalescer: leader returned error",
					slog.String("key", canonical),
					slog.String("err", res.Err.Error()),
				)
			}
			return bytes, false, res.Err
		}
		c.totalCoalesced.Add(1)
		c.counter.Inc(MetricCoalesceTotal)
		c.logger.Debug("media.Coalescer: follower received leader result",
			slog.String("key", canonical),
			slog.Bool("ok", res.Err == nil),
		)
		return bytes, true, res.Err
	}
}

// Stats returns a point-in-time snapshot of internal counters. See
// the Stats type for field semantics.
func (c *Coalescer) Stats() Stats {
	return Stats{
		InFlight:       int(c.inFlight.Load()),
		TotalCoalesced: c.totalCoalesced.Load(),
		TotalGenerated: c.totalGenerated.Load(),
	}
}
