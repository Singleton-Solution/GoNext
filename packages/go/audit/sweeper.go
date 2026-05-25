package audit

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// Sweeper runs a periodic retention prune against a PostgresStore.
//
// It exists so main.go can register one closer with the shutdown
// orchestrator that flushes the audit log AND tears down the
// background goroutine. The cron-cadenced pattern is intentionally
// simple — a time.Ticker plus a stop channel, mirroring
// redirects.Engine — rather than going through the full
// packages/go/jobs/cron registry. The audit prune has no payload, no
// distributed-lease requirement, and runs once per binary; the
// heavyweight scheduler is the wrong shape here.
//
// Lifecycle:
//
//   1. NewSweeper(store, retention).
//   2. (*Sweeper).Start launches the loop. Idempotent.
//   3. (*Sweeper).Stop drains the loop and runs ONE final Sweep so
//      we don't leak a partial day of retention on a deploy.
//
// A nil Sweeper.store causes Start to return immediately — operators
// who flip off Postgres audit (running the in-memory MemoryStore
// instead) get a free no-op without conditional wiring on the call
// site.
type Sweeper struct {
	store *PostgresStore

	// retention is the maximum age of an 'info'-severity audit row
	// before it becomes eligible for deletion. The default in main.go
	// is 90 days; operator overrides land via GONEXT_AUDIT_RETENTION
	// (a follow-up will promote it to config.Config when the wider
	// retention surface graduates).
	retention time.Duration

	// interval is the wall-clock cadence between sweeps. 24h is the
	// default — once a day matches the documented audit-prune SLO
	// and keeps the WHERE-occurred_at index scan small enough to
	// finish well inside the maintenance window.
	interval time.Duration

	log *slog.Logger

	startOnce sync.Once
	stop      chan struct{}
	done      chan struct{}
}

// SweeperOptions tunes the cadence and logger. Zero values fall back
// to the documented defaults; pass &SweeperOptions{} (or just nil)
// for production wiring.
type SweeperOptions struct {
	// Interval is the wall-clock cadence between sweeps. Defaults to
	// 24 hours. Values < 1 minute are clamped to 1 minute so a typo'd
	// sub-second cadence can't hammer the DELETE path.
	Interval time.Duration

	// Log is the slog logger. Defaults to slog.Default(). Sweep
	// outcomes (deleted-row count, errors) log at INFO; the
	// "I started" line logs at INFO too. No DEBUG output by default.
	Log *slog.Logger
}

// NewSweeper constructs a Sweeper. retention controls the max age of
// 'info'-severity audit rows; 'warning'/'critical' rows are retained
// indefinitely (docs/06-auth-permissions.md §13.2). Use 90 * 24h for
// the documented default.
//
// store may be nil — Start becomes a no-op in that case so the wiring
// on the main.go side stays unconditional. retention <= 0 is also a
// no-op (delegated to PostgresStore.Sweep, which guards the same
// invariant in case a caller bypasses Sweeper entirely).
func NewSweeper(store *PostgresStore, retention time.Duration, opts *SweeperOptions) *Sweeper {
	interval := 24 * time.Hour
	var log *slog.Logger
	if opts != nil {
		if opts.Interval > 0 {
			interval = opts.Interval
		}
		log = opts.Log
	}
	if interval < time.Minute {
		// Floor: sub-minute sweeps don't reflect any real operational
		// need and would burn a DELETE every cycle for at most a
		// handful of rows.
		interval = time.Minute
	}
	if log == nil {
		log = slog.Default()
	}
	return &Sweeper{
		store:     store,
		retention: retention,
		interval:  interval,
		log:       log,
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}
}

// Start launches the background sweep loop. Idempotent — only the
// first call wins; subsequent calls return immediately. Safe to call
// from any goroutine, including the orchestrator-wired init path.
//
// If the underlying store is nil, Start logs a single info line and
// closes the done channel so Stop can short-circuit.
func (s *Sweeper) Start() {
	s.startOnce.Do(func() {
		if s.store == nil {
			s.log.Info("audit.sweeper: store is nil; skipping background sweep")
			close(s.done)
			return
		}
		go s.loop()
	})
}

// Stop signals the loop to exit, performs ONE final sweep so we don't
// leak a partial day of retention across a deploy, and returns when
// the goroutine has terminated. Honors ctx for the final sweep — a
// canceled ctx returns ctx.Err() without retrying.
//
// Calling Stop on an unstarted Sweeper (or one constructed with a nil
// store) is a no-op and returns nil.
func (s *Sweeper) Stop(ctx context.Context) error {
	// Guard against double-stop. Closing a closed channel panics; we
	// recover so the second Stop is observably a no-op.
	defer func() { _ = recover() }()
	close(s.stop)
	select {
	case <-s.done:
		// Drained cleanly. Run one final sweep on the way out so a
		// long deploy doesn't skip a day of pruning.
		if s.store == nil {
			return nil
		}
		n, err := s.store.Sweep(ctx, s.retention)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			s.log.Error("audit.sweeper: final sweep failed", slog.Any("err", err))
			return err
		}
		s.log.Info("audit.sweeper: final sweep", slog.Int64("deleted", n))
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Sweeper) loop() {
	defer close(s.done)
	t := time.NewTicker(s.interval)
	defer t.Stop()
	s.log.Info("audit.sweeper: started",
		slog.Duration("interval", s.interval),
		slog.Duration("retention", s.retention),
	)
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			n, err := s.store.Sweep(ctx, s.retention)
			cancel()
			if err != nil {
				s.log.Error("audit.sweeper: sweep failed", slog.Any("err", err))
				continue
			}
			if n > 0 {
				s.log.Info("audit.sweeper: swept", slog.Int64("deleted", n))
			}
		}
	}
}
