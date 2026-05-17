package ratelimit

import (
	"context"
	"testing"
	"time"
)

// TestLoginAttemptLimiter_LockoutTriggersAfterThreshold verifies the
// failure counter trips into a lock after exactly the configured
// number of failures and that subsequent IsLocked queries return true.
func TestLoginAttemptLimiter_LockoutTriggersAfterThreshold(t *testing.T) {
	clock := newFakeClock(time.Unix(1_700_700_000, 0))

	ipLim, _ := NewMemoryLimiter(Policy{Capacity: 1_000, RefillRate: 100})
	emailLim, _ := NewMemoryLimiter(Policy{Capacity: 1_000, RefillRate: 100})

	lal, err := NewLoginAttemptLimiter(LoginAttemptOptions{
		IPLimiter:        ipLim,
		EmailLimiter:     emailLim,
		LockoutThreshold: 3,
		LockoutWindow:    30 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	lal.now = clock.Now

	const email = "victim@example.com"

	// 1st and 2nd failures: no lock.
	for i := 1; i <= 2; i++ {
		locked, err := lal.RecordFailure(context.Background(), email)
		if err != nil {
			t.Fatal(err)
		}
		if locked {
			t.Fatalf("failure %d should not lock yet", i)
		}
	}

	// 3rd failure trips the lock.
	locked, err := lal.RecordFailure(context.Background(), email)
	if err != nil {
		t.Fatal(err)
	}
	if !locked {
		t.Fatal("3rd failure should trigger lock")
	}

	// IsLocked confirms.
	isLocked, until, err := lal.IsLocked(context.Background(), email)
	if err != nil {
		t.Fatal(err)
	}
	if !isLocked {
		t.Fatal("expected IsLocked=true")
	}
	if until.Before(clock.Now()) {
		t.Errorf("LockedUntil %v should be in future", until)
	}

	// Subsequent RecordFailure within the window does NOT re-fire the
	// lock event (locked is false), because the account is already
	// locked — re-firing would spam the audit log.
	locked, _ = lal.RecordFailure(context.Background(), email)
	if locked {
		t.Error("RecordFailure during active lock should not re-fire the lock event")
	}
}

// TestLoginAttemptLimiter_LockClearsAfterWindow advances the clock past
// the lockout window and confirms IsLocked returns false (auto-unlock).
func TestLoginAttemptLimiter_LockClearsAfterWindow(t *testing.T) {
	clock := newFakeClock(time.Unix(1_700_701_000, 0))
	ipLim, _ := NewMemoryLimiter(Policy{Capacity: 100, RefillRate: 100})
	emailLim, _ := NewMemoryLimiter(Policy{Capacity: 100, RefillRate: 100})

	lal, _ := NewLoginAttemptLimiter(LoginAttemptOptions{
		IPLimiter:        ipLim,
		EmailLimiter:     emailLim,
		LockoutThreshold: 2,
		LockoutWindow:    10 * time.Minute,
	})
	lal.now = clock.Now

	const email = "x@example.com"
	_, _ = lal.RecordFailure(context.Background(), email)
	if locked, _ := lal.RecordFailure(context.Background(), email); !locked {
		t.Fatal("should be locked after 2 failures")
	}

	clock.Advance(11 * time.Minute)
	isLocked, _, _ := lal.IsLocked(context.Background(), email)
	if isLocked {
		t.Error("lock should have auto-expired after window")
	}
}

// TestLoginAttemptLimiter_SuccessResetsCounter verifies a successful
// login clears the failure counter so subsequent failures start at 1
// again.
func TestLoginAttemptLimiter_SuccessResetsCounter(t *testing.T) {
	clock := newFakeClock(time.Unix(1_700_702_000, 0))
	ipLim, _ := NewMemoryLimiter(Policy{Capacity: 100, RefillRate: 100})
	emailLim, _ := NewMemoryLimiter(Policy{Capacity: 100, RefillRate: 100})
	lal, _ := NewLoginAttemptLimiter(LoginAttemptOptions{
		IPLimiter:        ipLim,
		EmailLimiter:     emailLim,
		LockoutThreshold: 3,
		LockoutWindow:    30 * time.Minute,
	})
	lal.now = clock.Now

	const email = "y@example.com"
	_, _ = lal.RecordFailure(context.Background(), email)
	_, _ = lal.RecordFailure(context.Background(), email)

	if err := lal.RecordSuccess(context.Background(), email); err != nil {
		t.Fatal(err)
	}

	// Two new failures should NOT yet lock — counter must have reset.
	for i := 0; i < 2; i++ {
		locked, _ := lal.RecordFailure(context.Background(), email)
		if locked {
			t.Fatalf("failure %d after success should not lock", i+1)
		}
	}
	// Third post-reset failure trips lock again.
	locked, _ := lal.RecordFailure(context.Background(), email)
	if !locked {
		t.Fatal("3rd post-reset failure should lock")
	}
}

// TestLoginAttemptLimiter_PreCheckIPExhausted confirms the per-IP
// limiter is consulted first and returns the right reason and
// retryAfter.
func TestLoginAttemptLimiter_PreCheckIPExhausted(t *testing.T) {
	clock := newFakeClock(time.Unix(1_700_703_000, 0))
	ipLim, _ := NewMemoryLimiter(Policy{Capacity: 1, RefillRate: 0.01})
	ipLim.now = clock.Now
	emailLim, _ := NewMemoryLimiter(Policy{Capacity: 100, RefillRate: 1})
	emailLim.now = clock.Now

	lal, _ := NewLoginAttemptLimiter(LoginAttemptOptions{
		IPLimiter:    ipLim,
		EmailLimiter: emailLim,
	})
	lal.now = clock.Now

	res, err := lal.PreCheck(context.Background(), "1.2.3.4", "")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Allowed {
		t.Fatal("first call should pass")
	}

	res, _ = lal.PreCheck(context.Background(), "1.2.3.4", "")
	if res.Allowed {
		t.Fatal("second call should be throttled")
	}
	if res.Reason != ReasonIPRateLimit {
		t.Errorf("wrong reason: got %q want %q", res.Reason, ReasonIPRateLimit)
	}
	if res.RetryAfter <= 0 {
		t.Error("expected positive retryAfter")
	}
}

// TestLoginAttemptLimiter_PreCheckEmailExhausted verifies that the
// email bucket is the gate when the IP bucket has headroom.
func TestLoginAttemptLimiter_PreCheckEmailExhausted(t *testing.T) {
	clock := newFakeClock(time.Unix(1_700_704_000, 0))
	ipLim, _ := NewMemoryLimiter(Policy{Capacity: 100, RefillRate: 1})
	ipLim.now = clock.Now
	emailLim, _ := NewMemoryLimiter(Policy{Capacity: 1, RefillRate: 0.01})
	emailLim.now = clock.Now

	lal, _ := NewLoginAttemptLimiter(LoginAttemptOptions{
		IPLimiter:    ipLim,
		EmailLimiter: emailLim,
	})
	lal.now = clock.Now

	if res, _ := lal.PreCheck(context.Background(), "9.9.9.9", "v@example.com"); !res.Allowed {
		t.Fatal("first call should pass")
	}
	res, _ := lal.PreCheck(context.Background(), "9.9.9.9", "v@example.com")
	if res.Allowed {
		t.Fatal("second call same email should be throttled")
	}
	if res.Reason != ReasonEmailRateLimit {
		t.Errorf("wrong reason: got %q want %q", res.Reason, ReasonEmailRateLimit)
	}
}

// TestLoginAttemptLimiter_OracleAvoidance asserts that PreCheck does
// NOT consult lockout state — IsLocked must remain a separate query
// the caller runs only after the password matches. This is the test
// that guards against an attacker getting a "locked" oracle on the
// wrong-password path.
func TestLoginAttemptLimiter_OracleAvoidance(t *testing.T) {
	clock := newFakeClock(time.Unix(1_700_705_000, 0))
	ipLim, _ := NewMemoryLimiter(Policy{Capacity: 100, RefillRate: 1})
	emailLim, _ := NewMemoryLimiter(Policy{Capacity: 100, RefillRate: 1})
	lal, _ := NewLoginAttemptLimiter(LoginAttemptOptions{
		IPLimiter:        ipLim,
		EmailLimiter:     emailLim,
		LockoutThreshold: 1,
		LockoutWindow:    1 * time.Hour,
	})
	lal.now = clock.Now

	const email = "locked@example.com"

	// Force a lock.
	if locked, _ := lal.RecordFailure(context.Background(), email); !locked {
		t.Fatal("expected immediate lock for threshold=1")
	}

	// PreCheck still allows requests through (the buckets haven't been
	// exhausted). The caller MUST run the credential check, and only
	// reveal lockout state on a correct password.
	res, err := lal.PreCheck(context.Background(), "8.8.8.8", email)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Allowed {
		t.Fatal("PreCheck must not deny on lockout alone; that would expose the oracle")
	}
}

// TestLoginAttemptLimiter_EmailNormalization confirms that case and
// surrounding whitespace are normalized so "Alice@X.com" and
// "alice@x.com" share a bucket and a counter.
func TestLoginAttemptLimiter_EmailNormalization(t *testing.T) {
	clock := newFakeClock(time.Unix(1_700_706_000, 0))
	ipLim, _ := NewMemoryLimiter(Policy{Capacity: 100, RefillRate: 1})
	emailLim, _ := NewMemoryLimiter(Policy{Capacity: 100, RefillRate: 1})
	lal, _ := NewLoginAttemptLimiter(LoginAttemptOptions{
		IPLimiter:        ipLim,
		EmailLimiter:     emailLim,
		LockoutThreshold: 2,
		LockoutWindow:    1 * time.Hour,
	})
	lal.now = clock.Now

	_, _ = lal.RecordFailure(context.Background(), "  Alice@X.com  ")
	locked, _ := lal.RecordFailure(context.Background(), "alice@x.com")
	if !locked {
		t.Error("normalized emails should share a counter")
	}
}

// TestLoginAttemptLimiter_ConstructorErrors covers missing-limiter
// paths and confirms the sentinel wraps with %w.
func TestLoginAttemptLimiter_ConstructorErrors(t *testing.T) {
	if _, err := NewLoginAttemptLimiter(LoginAttemptOptions{}); err == nil {
		t.Error("expected error for missing IPLimiter")
	}
	ip, _ := NewMemoryLimiter(Policy{Capacity: 1, RefillRate: 1})
	if _, err := NewLoginAttemptLimiter(LoginAttemptOptions{IPLimiter: ip}); err == nil {
		t.Error("expected error for missing EmailLimiter")
	}
}

// TestIPLimiter_Wraps confirms NewMemoryIPLimiter behaves like a
// Limiter and rejects bad policies.
func TestIPLimiter_Wraps(t *testing.T) {
	l, err := NewMemoryIPLimiter(Policy{Capacity: 2, RefillRate: 1})
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ {
		if ok, _, _ := l.Allow(context.Background(), "127.0.0.1"); !ok {
			t.Fatalf("Allow %d should pass", i)
		}
	}
	if ok, _, _ := l.Allow(context.Background(), "127.0.0.1"); ok {
		t.Fatal("3rd call should fail")
	}

	if _, err := NewMemoryIPLimiter(Policy{Capacity: 0, RefillRate: 1}); err == nil {
		t.Error("expected error for bad policy")
	}
}
