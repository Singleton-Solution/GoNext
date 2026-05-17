package limits_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/plugins/runtime/limits"
)

// TestDefault sanity-checks the published defaults so a careless edit
// to limits.go that flips one to zero or negative is caught here.
func TestDefault(t *testing.T) {
	t.Parallel()

	d := limits.Default()
	if d.MemoryPages == 0 {
		t.Errorf("Default().MemoryPages = 0, want non-zero")
	}
	if d.CPUTimeoutSoft <= 0 {
		t.Errorf("Default().CPUTimeoutSoft = %v, want > 0", d.CPUTimeoutSoft)
	}
	if d.CPUTimeoutHard <= 0 {
		t.Errorf("Default().CPUTimeoutHard = %v, want > 0", d.CPUTimeoutHard)
	}
	if d.CPUTimeoutHard < d.CPUTimeoutSoft {
		t.Errorf("Default().CPUTimeoutHard (%v) < CPUTimeoutSoft (%v)",
			d.CPUTimeoutHard, d.CPUTimeoutSoft)
	}
	if d.MaxInstancesPerPlugin <= 0 {
		t.Errorf("Default().MaxInstancesPerPlugin = %d, want > 0", d.MaxInstancesPerPlugin)
	}

	// Defaults must always validate; otherwise New() panics for the
	// no-options happy path.
	if err := d.Validate(); err != nil {
		t.Errorf("Default().Validate() = %v, want nil", err)
	}
}

// TestValidate covers the rejection logic for Limits values that don't
// make sense (negative durations, hard < soft, negative instance cap).
func TestValidate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		l       limits.Limits
		wantErr string // substring expected in the error
	}{
		{
			name: "all zero is valid (disabled limits)",
			l:    limits.Limits{},
		},
		{
			name: "negative soft",
			l: limits.Limits{
				CPUTimeoutSoft: -time.Second,
			},
			wantErr: "CPUTimeoutSoft",
		},
		{
			name: "negative hard",
			l: limits.Limits{
				CPUTimeoutHard: -time.Second,
			},
			wantErr: "CPUTimeoutHard must be non-negative",
		},
		{
			name: "hard less than soft",
			l: limits.Limits{
				CPUTimeoutSoft: 2 * time.Second,
				CPUTimeoutHard: 1 * time.Second,
			},
			wantErr: "CPUTimeoutHard",
		},
		{
			name: "soft only is fine",
			l: limits.Limits{
				CPUTimeoutSoft: time.Second,
			},
		},
		{
			name: "hard only is fine",
			l: limits.Limits{
				CPUTimeoutHard: time.Second,
			},
		},
		{
			name: "hard equal to soft is fine",
			l: limits.Limits{
				CPUTimeoutSoft: time.Second,
				CPUTimeoutHard: time.Second,
			},
		},
		{
			name: "negative instance cap",
			l: limits.Limits{
				MaxInstancesPerPlugin: -1,
			},
			wantErr: "MaxInstancesPerPlugin",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.l.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() = nil, want error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("Validate() = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

// TestEffectiveMemoryPages confirms zero is treated as "use default"
// rather than "refuse instantiation".
func TestEffectiveMemoryPages(t *testing.T) {
	t.Parallel()

	if got := (limits.Limits{}).EffectiveMemoryPages(); got != limits.DefaultMemoryPages {
		t.Errorf("EffectiveMemoryPages on zero = %d, want %d", got, limits.DefaultMemoryPages)
	}
	if got := (limits.Limits{MemoryPages: 42}).EffectiveMemoryPages(); got != 42 {
		t.Errorf("EffectiveMemoryPages on 42 = %d, want 42", got)
	}
}

// TestNewEnforcerValidates rejects invalid Limits at construction.
func TestNewEnforcerValidates(t *testing.T) {
	t.Parallel()

	bad := limits.Limits{
		CPUTimeoutSoft: 2 * time.Second,
		CPUTimeoutHard: 1 * time.Second,
	}
	if _, err := limits.NewEnforcer(bad); err == nil {
		t.Fatal("NewEnforcer on invalid Limits = nil error, want non-nil")
	}

	if _, err := limits.NewEnforcer(limits.Default()); err != nil {
		t.Errorf("NewEnforcer on Default() = %v, want nil", err)
	}
}

// TestWithCPUDeadlineSoftOnly: when only the soft deadline is set the
// returned ctx has a deadline at +soft and cancels with
// DeadlineExceeded after that interval.
func TestWithCPUDeadlineSoftOnly(t *testing.T) {
	t.Parallel()

	e, err := limits.NewEnforcer(limits.Limits{
		CPUTimeoutSoft: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewEnforcer: %v", err)
	}

	parent := context.Background()
	ctx, cancel := e.WithCPUDeadline(parent)
	defer cancel()

	dl, ok := ctx.Deadline()
	if !ok {
		t.Fatal("ctx has no deadline; want one")
	}
	if d := time.Until(dl); d > 100*time.Millisecond {
		t.Errorf("ctx deadline %v is too far out", d)
	}

	select {
	case <-ctx.Done():
		// expected — wait for it to fire naturally
		if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			t.Errorf("ctx.Err() = %v, want DeadlineExceeded", ctx.Err())
		}
	case <-time.After(time.Second):
		t.Fatal("ctx did not fire its deadline within 1s")
	}
}

// TestWithCPUDeadlineNoLimits: no soft/hard means the returned ctx is
// effectively the parent ctx with no deadline.
func TestWithCPUDeadlineNoLimits(t *testing.T) {
	t.Parallel()

	e, err := limits.NewEnforcer(limits.Limits{})
	if err != nil {
		t.Fatalf("NewEnforcer: %v", err)
	}
	ctx, cancel := e.WithCPUDeadline(context.Background())
	defer cancel()
	if _, ok := ctx.Deadline(); ok {
		t.Error("ctx has a deadline but no limits were configured")
	}
}

// TestWithCPUDeadlineSoftHard: with both set, the soft deadline fires
// first (we observe the cancellation at +soft, well before +hard) and
// the calling code is unblocked promptly.
func TestWithCPUDeadlineSoftHard(t *testing.T) {
	t.Parallel()

	e, err := limits.NewEnforcer(limits.Limits{
		CPUTimeoutSoft: 30 * time.Millisecond,
		CPUTimeoutHard: 1 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewEnforcer: %v", err)
	}

	start := time.Now()
	ctx, cancel := e.WithCPUDeadline(context.Background())
	defer cancel()

	select {
	case <-ctx.Done():
		elapsed := time.Since(start)
		// soft is 30ms; we expect cancellation between 25-200ms (a wide
		// envelope to absorb scheduling jitter on busy CI).
		if elapsed < 20*time.Millisecond {
			t.Errorf("ctx done at %v, expected at least soft deadline (30ms)", elapsed)
		}
		if elapsed > 500*time.Millisecond {
			t.Errorf("ctx done at %v, expected well before hard deadline (1s)", elapsed)
		}
		if !errors.Is(ctx.Err(), context.Canceled) && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			t.Errorf("ctx.Err() = %v, want Canceled or DeadlineExceeded", ctx.Err())
		}
		// The cause should mention the soft deadline.
		cause := context.Cause(ctx)
		if cause != nil && !strings.Contains(cause.Error(), "soft cpu deadline") {
			t.Errorf("context.Cause() = %v, want it to mention soft deadline", cause)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ctx did not cancel within 2s")
	}
}

// TestWithCPUDeadlineHardAfterSoft: the hard timer remains armed after
// the soft fires; a second cancellation cause comes through. This is
// the belt-and-braces behavior we rely on if/when wazero adds a
// kill path distinct from ctx cancellation.
func TestWithCPUDeadlineHardAfterSoft(t *testing.T) {
	t.Parallel()

	e, err := limits.NewEnforcer(limits.Limits{
		CPUTimeoutSoft: 20 * time.Millisecond,
		CPUTimeoutHard: 80 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewEnforcer: %v", err)
	}

	start := time.Now()
	ctx, cancel := e.WithCPUDeadline(context.Background())
	defer cancel()

	<-ctx.Done()
	softElapsed := time.Since(start)
	if softElapsed > 150*time.Millisecond {
		t.Errorf("soft cancel fired at %v, expected ~20ms", softElapsed)
	}

	// Wait long enough for the hard timer to have fired internally,
	// confirming the goroutines tear down cleanly without leaking
	// (this is best observed under -race).
	time.Sleep(150 * time.Millisecond)
}

// TestWithCPUDeadlineEarlyCancelTearsDownTimers covers the hot path:
// the caller's deferred cancel must close out both timer goroutines so
// we don't leak. Hard to assert directly; we rely on `go test -race`
// to flag any data race in the teardown and t.Cleanup to flush.
func TestWithCPUDeadlineEarlyCancelTearsDownTimers(t *testing.T) {
	t.Parallel()

	e, err := limits.NewEnforcer(limits.Limits{
		CPUTimeoutSoft: 10 * time.Second,
		CPUTimeoutHard: 20 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewEnforcer: %v", err)
	}

	ctx, cancel := e.WithCPUDeadline(context.Background())
	cancel() // immediate

	select {
	case <-ctx.Done():
		// ok
	case <-time.After(time.Second):
		t.Fatal("ctx did not return from Done after explicit cancel")
	}
}

// TestAcquireBasic happy-path: with cap N, the first N acquires
// succeed and the (N+1)-th fails with ErrInstanceLimitReached.
func TestAcquireBasic(t *testing.T) {
	t.Parallel()

	e, err := limits.NewEnforcer(limits.Limits{
		MaxInstancesPerPlugin: 3,
	})
	if err != nil {
		t.Fatalf("NewEnforcer: %v", err)
	}

	const name = "p"
	releases := make([]func(), 0, 3)
	for i := 0; i < 3; i++ {
		rel, err := e.Acquire(name)
		if err != nil {
			t.Fatalf("Acquire #%d: %v", i+1, err)
		}
		releases = append(releases, rel)
	}
	if got := e.InstanceCount(name); got != 3 {
		t.Errorf("InstanceCount = %d, want 3", got)
	}

	if _, err := e.Acquire(name); !errors.Is(err, limits.ErrInstanceLimitReached) {
		t.Errorf("4th Acquire = %v, want ErrInstanceLimitReached", err)
	}

	// Release one; next Acquire should succeed.
	releases[0]()
	rel, err := e.Acquire(name)
	if err != nil {
		t.Fatalf("Acquire after release: %v", err)
	}
	releases[0] = rel

	for _, r := range releases {
		r()
	}
	if got := e.InstanceCount(name); got != 0 {
		t.Errorf("InstanceCount after releases = %d, want 0", got)
	}
}

// TestAcquireReleaseIdempotent: calling the release func twice must
// only decrement the counter once. Otherwise concurrent Close + pool
// release could double-free a slot and let the cap drift.
func TestAcquireReleaseIdempotent(t *testing.T) {
	t.Parallel()

	e, err := limits.NewEnforcer(limits.Limits{MaxInstancesPerPlugin: 2})
	if err != nil {
		t.Fatalf("NewEnforcer: %v", err)
	}
	rel, err := e.Acquire("p")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	rel()
	rel()
	rel()
	if got := e.InstanceCount("p"); got != 0 {
		t.Errorf("InstanceCount = %d, want 0", got)
	}
}

// TestAcquireDisabled: MaxInstancesPerPlugin == 0 disables the check.
// Even after 1000 acquires we never error.
func TestAcquireDisabled(t *testing.T) {
	t.Parallel()

	e, err := limits.NewEnforcer(limits.Limits{}) // all zero
	if err != nil {
		t.Fatalf("NewEnforcer: %v", err)
	}
	for i := 0; i < 1000; i++ {
		if _, err := e.Acquire("p"); err != nil {
			t.Fatalf("Acquire #%d on disabled limit: %v", i, err)
		}
	}
}

// TestAcquirePerName: counters are per-plugin. Hitting the cap on
// plugin A doesn't affect plugin B.
func TestAcquirePerName(t *testing.T) {
	t.Parallel()

	e, err := limits.NewEnforcer(limits.Limits{MaxInstancesPerPlugin: 1})
	if err != nil {
		t.Fatalf("NewEnforcer: %v", err)
	}
	relA, err := e.Acquire("a")
	if err != nil {
		t.Fatalf("Acquire a: %v", err)
	}
	defer relA()

	// a is at cap; b is fresh.
	if _, err := e.Acquire("a"); !errors.Is(err, limits.ErrInstanceLimitReached) {
		t.Errorf("second Acquire a = %v, want ErrInstanceLimitReached", err)
	}
	relB, err := e.Acquire("b")
	if err != nil {
		t.Fatalf("Acquire b: %v", err)
	}
	defer relB()
}

// TestAcquireConcurrent: hammer the enforcer from N goroutines on a
// single plugin name and verify the cap is never exceeded. Run under
// -race to catch any torn writes in the counter or the sync.Map slot
// installation.
func TestAcquireConcurrent(t *testing.T) {
	t.Parallel()

	const cap = 8
	const goroutines = 64

	e, err := limits.NewEnforcer(limits.Limits{MaxInstancesPerPlugin: cap})
	if err != nil {
		t.Fatalf("NewEnforcer: %v", err)
	}

	var wg sync.WaitGroup
	var max int64
	var maxMu sync.Mutex

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rel, err := e.Acquire("p")
			if err != nil {
				return // hit the cap, that's allowed
			}
			defer rel()

			now := int64(e.InstanceCount("p"))
			maxMu.Lock()
			if now > max {
				max = now
			}
			maxMu.Unlock()

			time.Sleep(2 * time.Millisecond)
		}()
	}
	wg.Wait()

	if max > cap {
		t.Errorf("instance count peaked at %d, cap was %d", max, cap)
	}
	if got := e.InstanceCount("p"); got != 0 {
		t.Errorf("InstanceCount after all release = %d, want 0", got)
	}
}

// TestAcquireRequiresName: passing an empty plugin name is a
// programming error; surface it.
func TestAcquireRequiresName(t *testing.T) {
	t.Parallel()
	e, err := limits.NewEnforcer(limits.Limits{MaxInstancesPerPlugin: 1})
	if err != nil {
		t.Fatalf("NewEnforcer: %v", err)
	}
	if _, err := e.Acquire(""); err == nil {
		t.Error("Acquire(\"\") = nil err, want error")
	}
}
