package outbox

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Default knobs for the recovery sweep.
const (
	// DefaultRecoverInterval is how often the recovery sweep runs.
	// Quick enough that a stuck row never delays the system by more
	// than ~claim-lease + this; slow enough that the periodic UPDATE
	// is a non-event for the database under steady-state load.
	DefaultRecoverInterval = 30 * time.Second
)

// Recoverer periodically releases outbox rows whose claim lease has
// expired. The Poller stamps claimed_at when it takes a row; if the
// poller process dies (or hangs) before deleting or releasing the
// row, claimed_at stays in the past and no other poller will see it
// (because the poll-cycle query filters on `claimed_at IS NULL`).
//
// The recovery sweep clears claimed_at on rows whose lease has
// elapsed, making them eligible for the next poll cycle.
//
// Lease semantics: the Poller's claim stamp is "the time at which
// I'm taking this row". A row is considered stuck when
//
//	now - claimed_at > ClaimLeaseSec
//
// Crucially, the Poller in this package uses the *same column* to
// implement per-row backoff (releaseFailed sets claimed_at to a
// future-ish timestamp). That means the recovery threshold must be
// computed against the *future* timestamp on a backoff row, not the
// past one. We naturally get this behaviour because the threshold
// check is `now() > claimed_at + lease`: a future claimed_at simply
// pushes the recovery time further out, which is exactly what we
// want for a backed-off row.
type Recoverer struct {
	// Pool is the database handle. Required.
	Pool PoolQuerier

	// ClaimLeaseSec is the lease duration in seconds. Must match the
	// Poller's setting (otherwise the recoverer will either steal
	// rows the poller is legitimately still processing or wait
	// longer than necessary). Zero falls back to
	// DefaultClaimLeaseSec.
	ClaimLeaseSec int

	// Interval is the gap between sweeps. Zero falls back to
	// DefaultRecoverInterval.
	Interval time.Duration

	// Logger is used for sweep summaries. Nil falls back to
	// slog.Default.
	Logger *slog.Logger

	// NowFunc, if set, replaces time.Now. Tests pin it to fast-
	// forward through the lease window.
	NowFunc func() time.Time
}

// Run loops sweep → sleep until ctx is cancelled. Like Poller.Run, an
// error in one cycle does not terminate the loop — we log and try
// again on the next interval.
func (r *Recoverer) Run(ctx context.Context) error {
	if err := r.validate(); err != nil {
		return err
	}
	logger := r.logger()
	interval := r.Interval
	if interval <= 0 {
		interval = DefaultRecoverInterval
	}

	logger.Info("outbox recoverer started",
		slog.Int("claim_lease_sec", r.leaseSec()),
		slog.Duration("interval", interval),
	)

	t := time.NewTimer(interval)
	defer t.Stop()
	// First tick fires immediately.
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
	t.Reset(0)

	for {
		select {
		case <-ctx.Done():
			logger.Info("outbox recoverer stopping")
			return ctx.Err()
		case <-t.C:
		}

		n, err := r.SweepOnce(ctx)
		if err != nil {
			logger.Warn("outbox recoverer sweep failed",
				slog.String("err", err.Error()),
			)
		} else if n > 0 {
			logger.Info("outbox recoverer released stuck rows",
				slog.Int64("released", n),
			)
		}
		t.Reset(interval)
	}
}

// SweepOnce runs exactly one sweep cycle and returns the number of
// rows whose claim was released. Exposed for tests (so they can
// fast-forward the clock and assert directly) and for operators who
// want to integrate the sweep into their own scheduler.
func (r *Recoverer) SweepOnce(ctx context.Context) (released int64, err error) {
	if err := r.validate(); err != nil {
		return 0, err
	}

	now := r.now()
	lease := time.Duration(r.leaseSec()) * time.Second
	threshold := now.Add(-lease)

	const q = `
		UPDATE outbox
		   SET claimed_at = NULL,
		       claimed_by = NULL
		 WHERE claimed_at IS NOT NULL
		   AND claimed_at < $1
	`
	tag, err := r.Pool.Exec(ctx, q, threshold)
	if err != nil {
		return 0, fmt.Errorf("outbox recover sweep: %w", err)
	}
	return tag.RowsAffected(), nil
}

func (r *Recoverer) leaseSec() int {
	if r.ClaimLeaseSec <= 0 {
		return DefaultClaimLeaseSec
	}
	return r.ClaimLeaseSec
}

func (r *Recoverer) now() time.Time {
	if r.NowFunc != nil {
		return r.NowFunc()
	}
	return time.Now()
}

func (r *Recoverer) logger() *slog.Logger {
	if r.Logger != nil {
		return r.Logger
	}
	return slog.Default()
}

func (r *Recoverer) validate() error {
	if r.Pool == nil {
		return fmt.Errorf("outbox.Recoverer: Pool is required")
	}
	return nil
}
