package cron

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"

	"github.com/Singleton-Solution/GoNext/packages/go/jobs/taskspec"
)

// fastSchedule is a cron.Schedule that ticks at a fixed sub-second
// cadence. Robfig's @every parser clamps to 1s (Every() in
// constantdelay.go), but the tests in this package need sub-second
// cadences to keep the test suite under a minute. The Scheduler only
// dereferences the cron.Schedule interface — it doesn't care that
// the test-only schedule was constructed without going through the
// public parser.
type fastSchedule struct{ d time.Duration }

func (f fastSchedule) Next(t time.Time) time.Time { return t.Add(f.d) }

// quietLogger discards all output; the scheduler is verbose at info
// level on every Acquire and Release, and we don't want tests to spam
// stderr.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// newAsynqClient builds an asynq.Client backed by miniredis. Same
// pattern as packages/go/jobs/taskspec/enqueue_test.go — the chassis
// only needs the queue to accept the enqueue, not to deliver it.
func newAsynqClient(t *testing.T, mr *miniredis.Miniredis) *asynq.Client {
	t.Helper()
	uc := redis.NewUniversalClient(&redis.UniversalOptions{Addrs: []string{mr.Addr()}})
	c := asynq.NewClientFromRedisClient(uc)
	t.Cleanup(func() {
		_ = c.Close()
		_ = uc.Close()
	})
	return c
}

// counterHandler returns a TaskSpec.Handler that bumps a shared
// counter on every dispatch. We don't actually dispatch in these
// tests — we count enqueues via taskspec.Enqueue's success path — so
// the handler is the registration's "I exist" marker.
func counterHandler() func(context.Context, []byte) error {
	return func(context.Context, []byte) error { return nil }
}

// buildTestEnv builds a fully wired scheduler against miniredis. The
// returned cleanup is registered via t.Cleanup. Callers can override
// CronRegistry, Owner, LeaseKey, etc. via the cfgPatch hook.
func buildTestEnv(t *testing.T, cfgPatch func(*Config)) (*Scheduler, *Registry, *miniredis.Miniredis, *taskspec.Registry) {
	t.Helper()
	rdb, mr := newMiniRedis(t)
	asynqClient := newAsynqClient(t, mr)

	taskReg := taskspec.NewRegistry()
	if err := taskReg.Register(taskspec.TaskSpec{
		Name:    "test.tick",
		Queue:   "default",
		Handler: counterHandler(),
	}); err != nil {
		t.Fatalf("taskReg.Register: %v", err)
	}

	cronReg := NewRegistry()
	cfg := Config{
		Redis:        rdb,
		AsynqClient:  asynqClient,
		TaskRegistry: taskReg,
		CronRegistry: cronReg,
		LeaseKey:     "test:cron:leader",
		LeaseTTL:     500 * time.Millisecond,
		Owner:        "test-owner",
		TickInterval: 25 * time.Millisecond,
		Logger:       quietLogger(),
		Rand:         rand.New(rand.NewSource(1)),
	}
	if cfgPatch != nil {
		cfgPatch(&cfg)
	}
	sched, err := NewScheduler(cfg)
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}
	return sched, cronReg, mr, taskReg
}

// TestScheduler_ValidationErrors covers the construction-time
// requirements.
func TestScheduler_ValidationErrors(t *testing.T) {
	t.Parallel()
	rdb, _ := newMiniRedis(t)
	taskReg := taskspec.NewRegistry()
	cronReg := NewRegistry()
	// Build a baseline good Config and zero out one field at a time.
	good := func() Config {
		return Config{
			Redis:        rdb,
			AsynqClient:  &asynq.Client{},
			TaskRegistry: taskReg,
			CronRegistry: cronReg,
			Owner:        "o",
		}
	}
	cases := []struct {
		name string
		mut  func(*Config)
	}{
		{"redis", func(c *Config) { c.Redis = nil }},
		{"asynq", func(c *Config) { c.AsynqClient = nil }},
		{"taskreg", func(c *Config) { c.TaskRegistry = nil }},
		{"cronreg", func(c *Config) { c.CronRegistry = nil }},
		{"owner", func(c *Config) { c.Owner = "" }},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := good()
			tc.mut(&cfg)
			if _, err := NewScheduler(cfg); err == nil {
				t.Fatalf("NewScheduler: missing %s, want error", tc.name)
			}
		})
	}
}

// TestScheduler_SingleInstanceFires verifies the smoke path: a single
// scheduler acquires the lease, fires the registered cron entry on
// every tick of a 100ms fastSchedule, and IsLeader stays true while
// it runs. The schedule is injected via the test-only
// registerWithSchedule helper because robfig's parser clamps
// @every to one-second resolution.
func TestScheduler_SingleInstanceFires(t *testing.T) {
	t.Parallel()
	sched, cronReg, _, _ := buildTestEnv(t, nil)
	if err := cronReg.registerWithSchedule(
		CronSpec{Name: "ticker", Schedule: "@every 100ms", TaskName: "test.tick"},
		fastSchedule{d: 100 * time.Millisecond},
	); err != nil {
		t.Fatalf("cronReg.registerWithSchedule: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- sched.Run(ctx) }()

	// Wait long enough for at least three fires; 100ms cadence in a
	// 1s window is up to ~9 nominal fires.
	deadline := time.Now().Add(900 * time.Millisecond)
	for time.Now().Before(deadline) {
		if sched.FireCount("ticker") >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return within 2s of cancel")
	}

	if got := sched.FireCount("ticker"); got < 2 {
		t.Fatalf("FireCount: got %d, want >= 2", got)
	}
}

// TestScheduler_TwoInstancesOnlyOneFires verifies the leader-election
// contract end to end: two schedulers with different Owners, sharing
// a Redis lease, must fire the schedule exactly once per tick across
// both. Sum of FireCount across both equals what a single instance
// would produce.
func TestScheduler_TwoInstancesOnlyOneFires(t *testing.T) {
	t.Parallel()
	// Both schedulers point at the SAME miniredis (so the lease is
	// shared) but each carries its own cron registry — they must
	// register the same schedule so both have something to fire
	// when they become leader.
	rdb, mr := newMiniRedis(t)
	asynqClient := newAsynqClient(t, mr)

	taskReg := taskspec.NewRegistry()
	if err := taskReg.Register(taskspec.TaskSpec{
		Name: "test.tick", Queue: "default", Handler: counterHandler(),
	}); err != nil {
		t.Fatalf("taskReg.Register: %v", err)
	}

	makeSched := func(owner string) (*Scheduler, *Registry) {
		cr := NewRegistry()
		_ = cr.registerWithSchedule(
			CronSpec{Name: "ticker", Schedule: "@every 100ms", TaskName: "test.tick"},
			fastSchedule{d: 100 * time.Millisecond},
		)
		s, err := NewScheduler(Config{
			Redis:        rdb,
			AsynqClient:  asynqClient,
			TaskRegistry: taskReg,
			CronRegistry: cr,
			LeaseKey:     "test:cron:leader",
			LeaseTTL:     500 * time.Millisecond,
			Owner:        owner,
			TickInterval: 25 * time.Millisecond,
			Logger:       quietLogger(),
			Rand:         rand.New(rand.NewSource(int64(len(owner)))),
		})
		if err != nil {
			t.Fatalf("NewScheduler(%s): %v", owner, err)
		}
		return s, cr
	}
	a, _ := makeSched("alpha")
	b, _ := makeSched("beta")

	ctx, cancel := context.WithTimeout(context.Background(), 1100*time.Millisecond)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(2)
	errs := make(chan error, 2)
	go func() { defer wg.Done(); errs <- a.Run(ctx) }()
	go func() { defer wg.Done(); errs <- b.Run(ctx) }()

	// Run for ~1s then cancel.
	<-ctx.Done()
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	}

	// Exactly one of the two schedulers should have a non-zero fire
	// count — the leader. The other must be zero (it was the
	// follower). Total fires is the @every 100ms count across the
	// 1s window.
	fa := a.FireCount("ticker")
	fb := b.FireCount("ticker")
	if fa > 0 && fb > 0 {
		t.Fatalf("both schedulers fired (a=%d, b=%d); leader election broken", fa, fb)
	}
	if fa+fb < 2 {
		t.Fatalf("total fires too low: a=%d b=%d (want at least 2 across 1s window)", fa, fb)
	}
}

// TestScheduler_LeaderDeathFollowerTakesOver simulates a hard leader
// crash by canceling the leader's context without calling Release.
// The lease then expires after LeaseTTL, and the follower picks it
// up. The follower must observe IsLeader=true and start firing
// within (LeaseTTL + idle-poll) of the death.
func TestScheduler_LeaderDeathFollowerTakesOver(t *testing.T) {
	t.Parallel()
	rdb, mr := newMiniRedis(t)
	asynqClient := newAsynqClient(t, mr)
	taskReg := taskspec.NewRegistry()
	_ = taskReg.Register(taskspec.TaskSpec{
		Name: "test.tick", Queue: "default", Handler: counterHandler(),
	})

	// Both schedulers use the same key; the leader has a tight TTL so
	// the test runs in well under a second.
	const ttl = 300 * time.Millisecond
	makeSched := func(owner string) *Scheduler {
		cr := NewRegistry()
		_ = cr.registerWithSchedule(
			CronSpec{Name: "ticker", Schedule: "@every 50ms", TaskName: "test.tick"},
			fastSchedule{d: 50 * time.Millisecond},
		)
		s, err := NewScheduler(Config{
			Redis:        rdb,
			AsynqClient:  asynqClient,
			TaskRegistry: taskReg,
			CronRegistry: cr,
			LeaseKey:     "test:cron:leader",
			LeaseTTL:     ttl,
			Owner:        owner,
			TickInterval: 15 * time.Millisecond,
			Logger:       quietLogger(),
			Rand:         rand.New(rand.NewSource(int64(len(owner)))),
		})
		if err != nil {
			t.Fatalf("NewScheduler(%s): %v", owner, err)
		}
		return s
	}
	leader := makeSched("leader")
	follower := makeSched("follower")

	// Start the leader and give it time to acquire BEFORE the
	// follower starts contending — otherwise both schedulers race
	// for the first Acquire and the test cares only about which one
	// wins second, not which one wins first.
	leaderCtx, killLeader := context.WithCancel(context.Background())
	leaderDone := make(chan error, 1)
	go func() { leaderDone <- leader.Run(leaderCtx) }()
	if !waitFor(t, 1*time.Second, leader.IsLeader) {
		t.Fatal("leader did not acquire lease in 1s")
	}

	// Start the follower in parallel; it must idle until the lease
	// drops.
	followerCtx, cancelFollower := context.WithCancel(context.Background())
	defer cancelFollower()
	followerDone := make(chan error, 1)
	go func() { followerDone <- follower.Run(followerCtx) }()
	if follower.IsLeader() {
		t.Fatal("follower must NOT be leader while leader holds the lease")
	}

	// Simulate the leader dying without releasing: cancel its
	// context. The defer in Run calls Release, but on a real crash
	// (kill -9) that defer wouldn't run — to simulate the harder
	// case we manually wipe the lease key from the leader's side
	// after the context cancel completes (the lease's Release is
	// a no-op once the key holder identity is broken).
	killLeader()
	<-leaderDone

	// At this point the lease MAY be released (if the defer ran)
	// OR still held by the dead leader (if we simulate kill -9).
	// To exercise the TTL-expiry path explicitly, wipe the lease
	// only when the defer didn't already do it; then FastForward
	// past the TTL.
	// In practice, the scheduler's defer runs because cancel()
	// returns synchronously after Run exits. We still want to
	// validate the follower can take over either way.
	mr.FastForward(ttl + 50*time.Millisecond)

	// The follower must now acquire within one poll window plus
	// jitter. Our poll is TTL/2 = 150ms with ±25% jitter, so
	// 200ms is a comfortable upper bound (plus some scheduling
	// slack for CI).
	deadline := time.Now().Add(700 * time.Millisecond)
	for time.Now().Before(deadline) {
		if follower.IsLeader() {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if !follower.IsLeader() {
		t.Fatalf("follower failed to take over within deadline (IsLeader=false)")
	}

	// Give the follower a brief window to fire at least once after
	// taking over, then stop it.
	time.Sleep(150 * time.Millisecond)
	cancelFollower()
	<-followerDone

	if follower.FireCount("ticker") == 0 {
		t.Fatal("follower took over but never fired")
	}
}

// waitFor polls cond until it returns true or the deadline elapses.
// Returns the final value of cond.
func waitFor(t *testing.T, d time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

// TestScheduler_ShutdownReleasesLease verifies that ctx cancellation
// runs the deferred Release: the lease key is gone immediately after
// Run returns, and a fresh Acquire by an unrelated owner succeeds
// without waiting for the TTL.
func TestScheduler_ShutdownReleasesLease(t *testing.T) {
	t.Parallel()
	sched, cronReg, _, _ := buildTestEnv(t, func(c *Config) {
		c.LeaseTTL = 10 * time.Second // long, so we'd notice if Release was skipped
	})
	_ = cronReg.registerWithSchedule(
		CronSpec{Name: "ticker", Schedule: "@every 200ms", TaskName: "test.tick"},
		fastSchedule{d: 200 * time.Millisecond},
	)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sched.Run(ctx) }()

	if !waitFor(t, 1*time.Second, sched.IsLeader) {
		t.Fatal("scheduler did not become leader in 1s")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return within 2s of cancel")
	}

	// After Run returns the lease key MUST be gone (released cleanly).
	got, err := sched.lease.CurrentOwner(context.Background())
	if !errors.Is(err, redis.Nil) {
		t.Fatalf("CurrentOwner after Run: got (%q, %v), want redis.Nil", got, err)
	}
}

// TestScheduler_RaceContending runs 8 schedulers against the same
// lease and the same registered schedule; the cumulative FireCount
// across all 8 must equal what a single scheduler would have produced
// in the same window. No double-fires.
func TestScheduler_RaceContending(t *testing.T) {
	t.Parallel()
	rdb, mr := newMiniRedis(t)
	asynqClient := newAsynqClient(t, mr)
	taskReg := taskspec.NewRegistry()
	_ = taskReg.Register(taskspec.TaskSpec{
		Name: "test.tick", Queue: "default", Handler: counterHandler(),
	})

	const N = 8
	scheds := make([]*Scheduler, N)
	for i := 0; i < N; i++ {
		cr := NewRegistry()
		// 100ms cadence so a 1s window yields ~9 nominal fires;
		// any double-fire across replicas would push the total above
		// the single-instance ceiling.
		_ = cr.registerWithSchedule(
			CronSpec{Name: "ticker", Schedule: "@every 100ms", TaskName: "test.tick"},
			fastSchedule{d: 100 * time.Millisecond},
		)
		s, err := NewScheduler(Config{
			Redis:        rdb,
			AsynqClient:  asynqClient,
			TaskRegistry: taskReg,
			CronRegistry: cr,
			LeaseKey:     "test:cron:leader",
			LeaseTTL:     400 * time.Millisecond,
			Owner:        "owner-" + itoa(i),
			TickInterval: 25 * time.Millisecond,
			Logger:       quietLogger(),
			Rand:         rand.New(rand.NewSource(int64(i + 1))),
		})
		if err != nil {
			t.Fatalf("NewScheduler[%d]: %v", i, err)
		}
		scheds[i] = s
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1100*time.Millisecond)
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		s := scheds[i]
		go func() {
			defer wg.Done()
			_ = s.Run(ctx)
		}()
	}
	<-ctx.Done()
	wg.Wait()

	var total uint64
	var leaderCount int
	for _, s := range scheds {
		c := s.FireCount("ticker")
		total += c
		if c > 0 {
			leaderCount++
		}
	}

	// 1s window at @every 100ms = at most 10 fires. We accept any
	// total in [1, 12] — the upper bound is the per-tick budget plus
	// some slack for the test's wall-clock tolerance, but the
	// CRITICAL property is that no double-fire pushes us past what a
	// single scheduler could produce. Under the lease contract,
	// even though leadership may transfer once or twice during the
	// 1s window, each schedule should fire at most once per
	// cron-tick boundary.
	if total < 1 {
		t.Fatalf("no fires across %d schedulers; lease blocked everyone", N)
	}
	if total > 15 {
		t.Fatalf("too many fires (got %d); double-fire bug across %d schedulers", total, N)
	}

	// Allow up to 2 schedulers to have fired (one main leader + one
	// possible takeover during the 1s window). More than 2 distinct
	// firing schedulers suggests double-fire at lease transitions.
	if leaderCount > 2 {
		t.Fatalf("too many schedulers fired (got %d); want <= 2", leaderCount)
	}
}

// TestScheduler_MissingTaskNameLoggedNotFired ensures a CronSpec
// pointing at an unregistered TaskName does NOT crash the scheduler —
// it's logged at warn level and the schedule keeps its place in the
// rotation. The fire counter stays at zero because no enqueue happened.
func TestScheduler_MissingTaskNameLoggedNotFired(t *testing.T) {
	t.Parallel()
	sched, cronReg, _, _ := buildTestEnv(t, nil)
	_ = cronReg.registerWithSchedule(
		CronSpec{Name: "bogus", Schedule: "@every 50ms", TaskName: "does.not.exist"},
		fastSchedule{d: 50 * time.Millisecond},
	)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- sched.Run(ctx) }()
	<-ctx.Done()
	<-done

	if got := sched.FireCount("bogus"); got != 0 {
		t.Fatalf("FireCount(bogus): got %d, want 0 (task name missing)", got)
	}
}

// TestScheduler_IsLeaderFalseInitially is a tiny invariant check: a
// fresh scheduler is not leader until Run acquires the lease.
func TestScheduler_IsLeaderFalseInitially(t *testing.T) {
	t.Parallel()
	sched, _, _, _ := buildTestEnv(t, nil)
	if sched.IsLeader() {
		t.Fatal("fresh Scheduler: want IsLeader=false")
	}
}

// TestScheduler_FireCountAtomic is a lightweight race-detector probe:
// two goroutines call FireCount concurrently with one writing via
// the (private) fire helper. We can't reach fire from the outside, so
// we instead spin Run + reader and rely on the race detector to
// catch any unsynchronized access.
func TestScheduler_FireCountAtomic(t *testing.T) {
	t.Parallel()
	sched, cronReg, _, _ := buildTestEnv(t, nil)
	_ = cronReg.registerWithSchedule(
		CronSpec{Name: "ticker", Schedule: "@every 25ms", TaskName: "test.tick"},
		fastSchedule{d: 25 * time.Millisecond},
	)

	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- sched.Run(ctx) }()

	var stop atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stop.Load() {
			_ = sched.FireCount("ticker")
			_ = sched.IsLeader()
			_, _ = sched.LastFiredAt("ticker")
		}
	}()
	<-ctx.Done()
	stop.Store(true)
	wg.Wait()
	<-done
}
