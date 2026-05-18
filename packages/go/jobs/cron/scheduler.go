package cron

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"sync"
	"time"

	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"

	"github.com/Singleton-Solution/GoNext/packages/go/jobs/taskspec"
)

// DefaultLeaseKey is the Redis key used by the cron lease when the
// Config.LeaseKey is left empty. Production may override via env or
// config to namespace per environment (e.g. "gonext:staging:cron:leader"),
// but every worker replica in a given environment MUST use the same
// value or the lease guarantees collapse.
const DefaultLeaseKey = "gonext:cron:leader"

// DefaultLeaseTTL is the default Redis TTL for the cron lease.
// 15 seconds matches docs/09-deployment-ops.md §16 and the issue #88
// acceptance criteria; it's a balance between "small enough that the
// missed-tick window after a leader death is acceptable" and "large
// enough that a transient network blip doesn't unseat the leader".
const DefaultLeaseTTL = 15 * time.Second

// DefaultTickInterval is the default polling cadence for the leader's
// fire loop. Standard cron resolution is one minute, so a per-second
// tick would be overkill for production schedules but is helpful for
// tests that exercise sub-second cadences via the @every <duration>
// shorthand. The scheduler uses min(TickInterval, TTL/3) for renewals,
// so the lease renewal cadence is decoupled from the fire cadence.
const DefaultTickInterval = 1 * time.Second

// Config bundles the dependencies and tuning knobs the Scheduler
// needs. We package them into a struct rather than a long parameter
// list so future fields (Prometheus metrics, NowFunc for tests) can
// be added without breaking the call site.
type Config struct {
	// Redis is the live go-redis client the lease writes to. Required.
	// The scheduler holds a reference for the lifetime of Run; the
	// caller owns the client's Close.
	Redis *redis.Client

	// AsynqClient is the asynq client used to enqueue the fire of
	// each due CronSpec. Required. The scheduler does not Close it.
	AsynqClient *asynq.Client

	// TaskRegistry is the taskspec registry consulted when firing a
	// CronSpec. The scheduler calls taskspec.Enqueue with this
	// registry and the CronSpec's TaskName. Required.
	TaskRegistry *taskspec.Registry

	// CronRegistry is the cron registry that holds the schedule
	// definitions. The scheduler reads this every tick when it is
	// leader. Required.
	CronRegistry *Registry

	// LeaseKey is the Redis key the lease writes to. Empty means
	// DefaultLeaseKey. All replicas in the same environment must
	// agree on this value.
	LeaseKey string

	// LeaseTTL is the Redis TTL applied to the lease key. Zero means
	// DefaultLeaseTTL. Smaller values mean faster recovery from a
	// leader death; larger values tolerate longer network blips.
	LeaseTTL time.Duration

	// Owner is the compare-and-swap identity this replica claims when
	// it holds the lease. Typically a hostname-plus-PID or a K8s pod
	// UID. Required (the lease itself rejects empty owners).
	Owner string

	// TickInterval bounds how often the leader checks for due
	// schedules and how often non-leaders retry Acquire. Zero means
	// DefaultTickInterval (1s). The leader's renew cadence is
	// derived from LeaseTTL/3 independently.
	TickInterval time.Duration

	// Logger is the structured logger used for lifecycle and warning
	// lines. Nil falls back to slog.Default. The scheduler does NOT
	// log every fire — production cron tables are small and the
	// asynq side already counts enqueues.
	Logger *slog.Logger

	// NowFunc is the time source used for due-time comparisons. Nil
	// uses time.Now. Tests pin this to a deterministic clock to
	// avoid sleeping for a full minute to exercise a cron tick.
	NowFunc func() time.Time

	// Rand is the source used to jitter the idle-follower poll. Nil
	// uses a fresh time-seeded source. Tests may pin it for
	// determinism.
	Rand *rand.Rand
}

func (c Config) validate() error {
	if c.Redis == nil {
		return errors.New("cron: Config.Redis is required")
	}
	if c.AsynqClient == nil {
		return errors.New("cron: Config.AsynqClient is required")
	}
	if c.TaskRegistry == nil {
		return errors.New("cron: Config.TaskRegistry is required")
	}
	if c.CronRegistry == nil {
		return errors.New("cron: Config.CronRegistry is required")
	}
	if c.Owner == "" {
		return errors.New("cron: Config.Owner is required")
	}
	return nil
}

// Scheduler is the leader-elected cron run-loop. One Scheduler runs
// per replica; multiple replicas race for the same LeaseKey, and
// whichever wins fires the registered CronSpecs until it loses or
// releases the lease.
//
// Construct via NewScheduler. Start the loop with Run (blocks until
// ctx is canceled). Read live state via IsLeader and LastFiredAt —
// these are intended for /metrics and admin diagnostics, not for
// production-critical control flow.
type Scheduler struct {
	cfg   Config
	lease *Lease
	log   *slog.Logger

	// isLeader is the live leadership flag. Read by IsLeader and
	// written by the run loop. Atomic on the value would be enough,
	// but a small mutex lets us read leader + lastFiredAt + fires
	// together without tearing.
	mu           sync.RWMutex
	isLeader     bool
	lastFiredAt  map[string]time.Time
	fireCounters map[string]uint64
}

// NewScheduler validates cfg, constructs the underlying Lease, and
// returns a ready-but-not-started Scheduler. Errors:
//
//   - "Config.Redis is required" / "Config.AsynqClient is required" /
//     "Config.TaskRegistry is required" / "Config.CronRegistry is
//     required" / "Config.Owner is required" for missing fields.
//   - the wrapped NewLease error for invalid LeaseKey/Owner/TTL.
//
// Defaults applied here: LeaseKey, LeaseTTL, TickInterval, Logger,
// NowFunc, Rand. The returned Scheduler holds the resolved values;
// the caller's cfg is otherwise untouched.
func NewScheduler(cfg Config) (*Scheduler, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if cfg.LeaseKey == "" {
		cfg.LeaseKey = DefaultLeaseKey
	}
	if cfg.LeaseTTL <= 0 {
		cfg.LeaseTTL = DefaultLeaseTTL
	}
	if cfg.TickInterval <= 0 {
		cfg.TickInterval = DefaultTickInterval
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.NowFunc == nil {
		cfg.NowFunc = time.Now
	}
	if cfg.Rand == nil {
		cfg.Rand = rand.New(rand.NewSource(time.Now().UnixNano()))
	}

	lease, err := NewLease(cfg.Redis, cfg.LeaseKey, cfg.Owner, cfg.LeaseTTL)
	if err != nil {
		return nil, fmt.Errorf("cron: lease: %w", err)
	}

	return &Scheduler{
		cfg:          cfg,
		lease:        lease,
		log:          cfg.Logger.With(slog.String("component", "cron")),
		lastFiredAt:  map[string]time.Time{},
		fireCounters: map[string]uint64{},
	}, nil
}

// Run blocks until ctx is canceled. Loop:
//
//  1. Try Acquire on the lease.
//  2. If we just won leadership, transition into the fire loop:
//     every TickInterval, check the registry for any schedule whose
//     next-fire time has elapsed, enqueue it, and update next-fire.
//     Every LeaseTTL/3, Renew the lease. If a renewal fails, log,
//     drop leadership, and fall back to the idle poll.
//  3. If we're not leader, sleep for idleSleep(TTL, rand) and try
//     again.
//
// On ctx.Done the loop exits, ensuring Release is called if we still
// hold the lease. Release errors are logged but not returned —
// shutdown is best-effort.
//
// Run returns the first non-recoverable error it encounters (e.g.
// Redis is permanently unreachable). Lease losses and Acquire
// failures are recoverable and don't return.
func (s *Scheduler) Run(ctx context.Context) error {
	defer s.releaseQuietly(context.Background())

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		acquired, err := s.lease.Acquire(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			s.log.WarnContext(ctx, "cron: lease acquire failed",
				slog.String("err", err.Error()))
			// Back off and retry; treat a transport blip the same
			// as a contented lease.
			if !s.sleep(ctx, idleSleep(s.cfg.LeaseTTL, s.cfg.Rand)) {
				return nil
			}
			continue
		}

		if acquired {
			s.setLeader(true)
			s.log.InfoContext(ctx, "cron: lease acquired",
				slog.String("key", s.cfg.LeaseKey),
				slog.String("owner", s.cfg.Owner),
				slog.Duration("ttl", s.cfg.LeaseTTL))
			s.runAsLeader(ctx)
			s.setLeader(false)
			s.log.InfoContext(ctx, "cron: leadership released",
				slog.String("key", s.cfg.LeaseKey),
				slog.String("owner", s.cfg.Owner))
			continue
		}

		// Not leader. Idle-poll until next attempt.
		if !s.sleep(ctx, idleSleep(s.cfg.LeaseTTL, s.cfg.Rand)) {
			return nil
		}
	}
}

// runAsLeader is the inner loop entered after a successful Acquire.
// Returns when ctx is canceled OR when the lease is lost (either via
// a renew failure or via an explicit external takeover).
//
// We initialize next-fire times to the first cron.Schedule.Next from
// "now"; the first fire happens at the FIRST scheduled time AFTER
// our acquire, never immediately on acquisition. This matches the
// usual cron mental model where "every 5 minutes" means "fire at the
// next 0/5/10/... minute boundary", not "fire now plus every 5
// minutes from then on".
func (s *Scheduler) runAsLeader(ctx context.Context) {
	// Track per-name next-fire times so we don't double-fire within
	// a single tick window after a slow handler.
	nowFn := s.cfg.NowFunc
	next := map[string]time.Time{}
	for _, sch := range s.cfg.CronRegistry.snapshot(nowFn()) {
		next[sch.spec.Name] = sch.next
	}

	renewEvery := s.cfg.LeaseTTL / 3
	if renewEvery <= 0 {
		renewEvery = s.cfg.LeaseTTL
	}
	if renewEvery <= 0 {
		renewEvery = time.Second
	}

	tickEvery := s.cfg.TickInterval
	if tickEvery <= 0 {
		tickEvery = DefaultTickInterval
	}

	ticker := time.NewTicker(tickEvery)
	defer ticker.Stop()
	renewTicker := time.NewTicker(renewEvery)
	defer renewTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-renewTicker.C:
			if err := s.lease.Renew(ctx); err != nil {
				if errors.Is(err, ErrNotLeader) {
					s.log.WarnContext(ctx, "cron: lease lost on renew",
						slog.String("key", s.cfg.LeaseKey),
						slog.String("owner", s.cfg.Owner))
					return
				}
				if ctx.Err() != nil {
					return
				}
				s.log.WarnContext(ctx, "cron: lease renew failed",
					slog.String("err", err.Error()))
				// Don't abandon leadership on a single transport
				// error; the next renew tick may recover.
			}
		case <-ticker.C:
			now := nowFn()
			snap := s.cfg.CronRegistry.snapshot(now)
			for _, sch := range snap {
				// Seed unknown entries (registered after acquire)
				// with their first-fire time so we don't immediately
				// fire something the operator just added.
				due, ok := next[sch.spec.Name]
				if !ok {
					next[sch.spec.Name] = sch.next
					continue
				}
				if !now.Before(due) {
					s.fire(ctx, sch.spec, now)
					// Advance to the next boundary STRICTLY after
					// `now` so the same tick can't fire the same
					// schedule twice in a row.
					next[sch.spec.Name] = sch.cron.Next(now)
				}
			}
		}
	}
}

// fire enqueues one CronSpec via taskspec.Enqueue and updates the
// observability state. Errors are logged but not propagated — a
// missing TaskName or a transient Redis error must not knock the
// scheduler out of its loop.
func (s *Scheduler) fire(ctx context.Context, spec CronSpec, now time.Time) {
	if !s.cfg.TaskRegistry.Has(spec.TaskName) {
		s.log.WarnContext(ctx, "cron: skipping fire, task name not in registry",
			slog.String("schedule", spec.Name),
			slog.String("task", spec.TaskName))
		return
	}
	_, err := taskspec.Enqueue(ctx, s.cfg.AsynqClient, s.cfg.TaskRegistry, spec.TaskName, spec.Payload)
	if err != nil {
		s.log.WarnContext(ctx, "cron: fire failed",
			slog.String("schedule", spec.Name),
			slog.String("task", spec.TaskName),
			slog.String("err", err.Error()))
		return
	}
	s.mu.Lock()
	s.lastFiredAt[spec.Name] = now
	s.fireCounters[spec.Name]++
	s.mu.Unlock()
}

// IsLeader reports whether this Scheduler currently holds the
// lease. Intended for /readyz integrations and for the
// cron_lease_holder Prometheus gauge — production control flow must
// NOT branch on this value (the lease state may flip between the
// read and the action). Safe for concurrent calls.
func (s *Scheduler) IsLeader() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.isLeader
}

// LastFiredAt returns the most recent fire time for the named
// schedule, plus a bool indicating whether the schedule has ever
// fired on this Scheduler instance. Useful for tests and for
// per-schedule "last run" admin display.
func (s *Scheduler) LastFiredAt(name string) (time.Time, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.lastFiredAt[name]
	return t, ok
}

// FireCount returns the total number of times this Scheduler has
// fired the named schedule since startup. The counter resets on
// process restart; durable counts live in the asynq stats. Useful
// for the race test that verifies a multi-replica setup didn't
// double-fire.
func (s *Scheduler) FireCount(name string) uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.fireCounters[name]
}

// setLeader is the internal write side of the isLeader field.
func (s *Scheduler) setLeader(v bool) {
	s.mu.Lock()
	s.isLeader = v
	s.mu.Unlock()
}

// releaseQuietly attempts to release the lease and swallows
// ErrNotLeader (we never had it) and any transport error (we are on
// the shutdown path and the surrounding orchestrator already has a
// budget). Other code paths use the explicit Release for the error
// signal.
func (s *Scheduler) releaseQuietly(ctx context.Context) {
	if err := s.lease.Release(ctx); err != nil && !errors.Is(err, ErrNotLeader) {
		s.log.WarnContext(ctx, "cron: lease release failed",
			slog.String("err", err.Error()))
	}
}

// sleep blocks for d or returns false on ctx cancel. Centralized so
// the loop doesn't sprinkle the same select pattern in three places.
func (s *Scheduler) sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
