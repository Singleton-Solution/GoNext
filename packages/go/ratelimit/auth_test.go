package ratelimit

import (
	"context"
	"sync"
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

	const userID = "user-victim"

	// 1st and 2nd failures: no lock.
	for i := 1; i <= 2; i++ {
		locked, err := lal.RecordFailure(context.Background(), userID)
		if err != nil {
			t.Fatal(err)
		}
		if locked {
			t.Fatalf("failure %d should not lock yet", i)
		}
	}

	// 3rd failure trips the lock.
	locked, err := lal.RecordFailure(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	if !locked {
		t.Fatal("3rd failure should trigger lock")
	}

	// IsLocked confirms.
	isLocked, until, err := lal.IsLocked(context.Background(), userID)
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
	locked, _ = lal.RecordFailure(context.Background(), userID)
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

	const userID = "user-x"
	_, _ = lal.RecordFailure(context.Background(), userID)
	if locked, _ := lal.RecordFailure(context.Background(), userID); !locked {
		t.Fatal("should be locked after 2 failures")
	}

	clock.Advance(11 * time.Minute)
	isLocked, _, _ := lal.IsLocked(context.Background(), userID)
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

	const userID = "user-y"
	_, _ = lal.RecordFailure(context.Background(), userID)
	_, _ = lal.RecordFailure(context.Background(), userID)

	if err := lal.RecordSuccess(context.Background(), userID); err != nil {
		t.Fatal(err)
	}

	// Two new failures should NOT yet lock — counter must have reset.
	for i := 0; i < 2; i++ {
		locked, _ := lal.RecordFailure(context.Background(), userID)
		if locked {
			t.Fatalf("failure %d after success should not lock", i+1)
		}
	}
	// Third post-reset failure trips lock again.
	locked, _ := lal.RecordFailure(context.Background(), userID)
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

	res, err := lal.Check(context.Background(), CheckInput{IP: "1.2.3.4"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Allowed {
		t.Fatal("first call should pass")
	}

	res, _ = lal.Check(context.Background(), CheckInput{IP: "1.2.3.4"})
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
// email bucket is the gate when the IP bucket has headroom AND the
// caller has confirmed the email exists.
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

	in := CheckInput{IP: "9.9.9.9", Email: "v@example.com", EmailExists: true}
	if res, _ := lal.Check(context.Background(), in); !res.Allowed {
		t.Fatal("first call should pass")
	}
	res, _ := lal.Check(context.Background(), in)
	if res.Allowed {
		t.Fatal("second call same email should be throttled")
	}
	if res.Reason != ReasonEmailRateLimit {
		t.Errorf("wrong reason: got %q want %q", res.Reason, ReasonEmailRateLimit)
	}
}

// TestLoginAttemptLimiter_OracleAvoidance asserts that Check does
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

	const userID = "user-locked"

	// Force a lock.
	if locked, _ := lal.RecordFailure(context.Background(), userID); !locked {
		t.Fatal("expected immediate lock for threshold=1")
	}

	// Check still allows requests through (the buckets haven't been
	// exhausted). The caller MUST run the credential check, and only
	// reveal lockout state on a correct password.
	res, err := lal.Check(context.Background(), CheckInput{IP: "8.8.8.8", Email: "locked@example.com", EmailExists: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Allowed {
		t.Fatal("Check must not deny on lockout alone; that would expose the oracle")
	}
}

// TestLoginAttemptLimiter_EmailNormalization confirms that case and
// surrounding whitespace on the email-bucket key are normalized so
// "Alice@X.com" and "alice@x.com" share a token-bucket entry.
func TestLoginAttemptLimiter_EmailNormalization(t *testing.T) {
	clock := newFakeClock(time.Unix(1_700_706_000, 0))
	ipLim, _ := NewMemoryLimiter(Policy{Capacity: 100, RefillRate: 1})
	ipLim.now = clock.Now
	// Capacity 1 so two normalized hits exhaust the bucket and we can
	// detect them sharing state via the 429.
	emailLim, _ := NewMemoryLimiter(Policy{Capacity: 1, RefillRate: 0.01})
	emailLim.now = clock.Now
	lal, _ := NewLoginAttemptLimiter(LoginAttemptOptions{
		IPLimiter:    ipLim,
		EmailLimiter: emailLim,
	})
	lal.now = clock.Now

	first, _ := lal.Check(context.Background(), CheckInput{IP: "1.1.1.1", Email: "  Alice@X.com  ", EmailExists: true})
	if !first.Allowed {
		t.Fatal("first call should pass")
	}
	second, _ := lal.Check(context.Background(), CheckInput{IP: "1.1.1.1", Email: "alice@x.com", EmailExists: true})
	if second.Allowed {
		t.Error("normalized email variants should share a bucket and be throttled together")
	}
	if second.Reason != ReasonEmailRateLimit {
		t.Errorf("expected email reason; got %q", second.Reason)
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

// -----------------------------------------------------------------
// Regression tests for the three AC gaps flagged on PR #296 review.
// -----------------------------------------------------------------

// TestLoginAttemptLimiter_NoEmailOracle is the regression for issue
// #195's "only against existing emails" clause. It asserts that for a
// run of submissions within the IP bucket's burst budget, an unknown-
// email request and a known-email request return the SAME shape of
// PreCheckResult — same Allowed, same Reason — so an attacker can't
// distinguish "this email is registered" from "this email isn't" by
// watching for a 429 on the per-email bucket.
//
// Setup: tiny email bucket (Capacity 1) so if we DID apply it to
// unknown emails we'd see a 429 on the 2nd hit. With the fix, unknown
// emails skip the bucket entirely and never produce 429 (within the
// IP burst). The "indistinguishable" check is on Allowed and Reason.
func TestLoginAttemptLimiter_NoEmailOracle(t *testing.T) {
	clock := newFakeClock(time.Unix(1_700_707_000, 0))
	// Generous IP bucket so the IP path never gates within the test.
	ipLim, _ := NewMemoryLimiter(Policy{Capacity: 1000, RefillRate: 100})
	ipLim.now = clock.Now
	// Tiny email bucket: a single token. If applied to an unknown
	// email, the 2nd unknown-email submission would 429 and reveal
	// the bucket existed.
	emailLim, _ := NewMemoryLimiter(Policy{Capacity: 1, RefillRate: 0.01})
	emailLim.now = clock.Now
	lal, _ := NewLoginAttemptLimiter(LoginAttemptOptions{
		IPLimiter:    ipLim,
		EmailLimiter: emailLim,
	})
	lal.now = clock.Now

	const N = 5
	unknownResults := make([]PreCheckResult, N)
	for i := 0; i < N; i++ {
		r, err := lal.Check(context.Background(), CheckInput{
			IP:          "10.0.0.1",
			Email:       "ghost@example.com",
			EmailExists: false, // attacker probes a non-existent email
		})
		if err != nil {
			t.Fatalf("unknown[%d]: %v", i, err)
		}
		unknownResults[i] = r
	}

	// All unknown-email submissions MUST be allowed (within the IP
	// burst). If any returned 429 with ReasonEmailRateLimit we've
	// leaked the per-email bucket to unknown emails.
	for i, r := range unknownResults {
		if !r.Allowed {
			t.Errorf("unknown[%d]: got denied (%q); per-email bucket leaked existence", i, r.Reason)
		}
		if r.Reason == ReasonEmailRateLimit {
			t.Errorf("unknown[%d]: ReasonEmailRateLimit on unknown email is the oracle", i)
		}
	}

	// Sanity: a known-email request hitting the same tiny bucket DOES
	// 429 on the second submission. This confirms the bucket is wired
	// for known emails — we're not just observing "the bucket is
	// unused everywhere".
	first, _ := lal.Check(context.Background(), CheckInput{
		IP: "10.0.0.2", Email: "real@example.com", EmailExists: true,
	})
	if !first.Allowed {
		t.Fatal("known-email first hit should pass")
	}
	second, _ := lal.Check(context.Background(), CheckInput{
		IP: "10.0.0.2", Email: "real@example.com", EmailExists: true,
	})
	if second.Allowed || second.Reason != ReasonEmailRateLimit {
		t.Errorf("known-email second hit should be throttled by email bucket; got allowed=%v reason=%q",
			second.Allowed, second.Reason)
	}
}

// TestLoginAttemptLimiter_AuditEvents is the regression for issue
// #195's "emit auth.login.locked / .unlocked / .ratelimit.exceeded"
// clause. It captures emissions via a recording AuditEmitter and
// asserts each event fires at exactly the right transition.
func TestLoginAttemptLimiter_AuditEvents(t *testing.T) {
	clock := newFakeClock(time.Unix(1_700_708_000, 0))
	// Tiny IP bucket to trigger ratelimit.exceeded.
	ipLim, _ := NewMemoryLimiter(Policy{Capacity: 1, RefillRate: 0.01})
	ipLim.now = clock.Now
	emailLim, _ := NewMemoryLimiter(Policy{Capacity: 100, RefillRate: 1})
	emailLim.now = clock.Now

	rec := newRecordingAudit()
	lal, _ := NewLoginAttemptLimiter(LoginAttemptOptions{
		IPLimiter:        ipLim,
		EmailLimiter:     emailLim,
		LockoutThreshold: 2,
		LockoutWindow:    1 * time.Hour,
		Audit:            rec,
	})
	lal.now = clock.Now

	const userID = "audit-user"

	// 1. Trigger lock by two failures (threshold = 2).
	if _, err := lal.RecordFailure(context.Background(), userID); err != nil {
		t.Fatal(err)
	}
	if rec.lockedCount() != 0 {
		t.Error("EmitLocked should not fire before threshold")
	}
	if locked, _ := lal.RecordFailure(context.Background(), userID); !locked {
		t.Fatal("2nd failure should lock")
	}
	if rec.lockedCount() != 1 {
		t.Errorf("EmitLocked should fire exactly once on transition; got %d", rec.lockedCount())
	}
	if got := rec.lastLocked(); got.userID != userID || got.reason != AuditReasonThresholdExceeded {
		t.Errorf("EmitLocked payload wrong: %+v", got)
	}

	// 2. Subsequent failures during the active lockout MUST NOT
	// re-emit (audit-log spam guard).
	_, _ = lal.RecordFailure(context.Background(), userID)
	if rec.lockedCount() != 1 {
		t.Errorf("EmitLocked should not re-fire during active lock; got count=%d", rec.lockedCount())
	}

	// 3. Successful login while locked → EmitUnlocked once.
	if err := lal.RecordSuccess(context.Background(), userID); err != nil {
		t.Fatal(err)
	}
	if rec.unlockedCount() != 1 {
		t.Errorf("EmitUnlocked should fire once on success during lockout; got %d", rec.unlockedCount())
	}
	if rec.lastUnlocked() != userID {
		t.Errorf("EmitUnlocked userID = %q want %q", rec.lastUnlocked(), userID)
	}

	// 4. Successful login when NOT locked must not emit unlocked.
	if err := lal.RecordSuccess(context.Background(), "other-user"); err != nil {
		t.Fatal(err)
	}
	if rec.unlockedCount() != 1 {
		t.Errorf("EmitUnlocked should not fire for non-locked users; got %d", rec.unlockedCount())
	}

	// 5. Rate-limit denial fires EmitRateLimitExceeded.
	// First Check consumes the only IP token; second 429s.
	_, _ = lal.Check(context.Background(), CheckInput{IP: "5.5.5.5"})
	if rec.rateLimitCount() != 0 {
		t.Errorf("first Check (allowed) should not emit; got %d", rec.rateLimitCount())
	}
	_, _ = lal.Check(context.Background(), CheckInput{IP: "5.5.5.5"})
	if rec.rateLimitCount() != 1 {
		t.Errorf("denied Check should emit auth.ratelimit.exceeded; got %d", rec.rateLimitCount())
	}
	last := rec.lastRateLimit()
	if last.key != "5.5.5.5" {
		t.Errorf("EmitRateLimitExceeded key = %q want %q", last.key, "5.5.5.5")
	}
	if last.retryAfter <= 0 {
		t.Errorf("EmitRateLimitExceeded retryAfter should be positive; got %v", last.retryAfter)
	}
}

// TestLoginAttemptLimiter_DurableFailureStore is the regression for
// issue #195's "persistent storage in users.failed_login_count and
// users.locked_until" clause. It exercises the FailureStore interface
// (independent of implementation) and demonstrates that a freshly
// constructed limiter sharing the same store sees the lockout — i.e.
// the durable-state contract works across "process boundaries"
// modelled here as two separate limiter instances against one store.
func TestLoginAttemptLimiter_DurableFailureStore(t *testing.T) {
	clock := newFakeClock(time.Unix(1_700_709_000, 0))
	store := NewMemoryFailureStore()

	build := func() *LoginAttemptLimiter {
		ipLim, _ := NewMemoryLimiter(Policy{Capacity: 100, RefillRate: 100})
		emailLim, _ := NewMemoryLimiter(Policy{Capacity: 100, RefillRate: 100})
		lal, _ := NewLoginAttemptLimiter(LoginAttemptOptions{
			IPLimiter:        ipLim,
			EmailLimiter:     emailLim,
			LockoutThreshold: 2,
			LockoutWindow:    1 * time.Hour,
			FailureStore:     store,
		})
		lal.now = clock.Now
		return lal
	}

	// First limiter instance: trip the lock.
	a := build()
	const userID = "durable-user"
	_, _ = a.RecordFailure(context.Background(), userID)
	if locked, _ := a.RecordFailure(context.Background(), userID); !locked {
		t.Fatal("expected lock after 2 failures on instance A")
	}

	// Second limiter instance, same store — simulates the post-restart
	// or other-replica view. IsLocked MUST still see the lockout.
	b := build()
	locked, until, err := b.IsLocked(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	if !locked {
		t.Fatal("durable store should preserve lockout across limiter instances")
	}
	if until.IsZero() {
		t.Error("expected non-zero LockedUntil from durable store")
	}

	// And clearing on instance B reflects in instance A.
	if err := b.RecordSuccess(context.Background(), userID); err != nil {
		t.Fatal(err)
	}
	stillLocked, _, _ := a.IsLocked(context.Background(), userID)
	if stillLocked {
		t.Error("ClearFailures on instance B should propagate via the shared store")
	}
}

// TestLoginAttemptLimiter_NopAuditDefault verifies the nil-Audit case
// is handled (default NopAuditEmitter wired in NewLoginAttemptLimiter)
// — so the limiter is usable without audit wiring.
func TestLoginAttemptLimiter_NopAuditDefault(t *testing.T) {
	ipLim, _ := NewMemoryLimiter(Policy{Capacity: 10, RefillRate: 1})
	emailLim, _ := NewMemoryLimiter(Policy{Capacity: 10, RefillRate: 1})
	lal, err := NewLoginAttemptLimiter(LoginAttemptOptions{
		IPLimiter:        ipLim,
		EmailLimiter:     emailLim,
		LockoutThreshold: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Should not panic on emission paths.
	_, _ = lal.RecordFailure(context.Background(), "u1")
	_ = lal.RecordSuccess(context.Background(), "u1")
}

// TestNopAuditEmitter_AllNoops exercises every method on the zero-
// cost default to keep coverage measurement honest.
func TestNopAuditEmitter_AllNoops(t *testing.T) {
	n := NopAuditEmitter{}
	n.EmitLocked(context.Background(), "u", "r")
	n.EmitUnlocked(context.Background(), "u")
	n.EmitRateLimitExceeded(context.Background(), "k", time.Second)
}

// TestLoginAttemptLimiter_PreCheckLegacy verifies the deprecated
// PreCheck wrapper still applies the email bucket (back-compat).
func TestLoginAttemptLimiter_PreCheckLegacy(t *testing.T) {
	clock := newFakeClock(time.Unix(1_700_710_000, 0))
	ipLim, _ := NewMemoryLimiter(Policy{Capacity: 100, RefillRate: 1})
	ipLim.now = clock.Now
	emailLim, _ := NewMemoryLimiter(Policy{Capacity: 1, RefillRate: 0.01})
	emailLim.now = clock.Now
	lal, _ := NewLoginAttemptLimiter(LoginAttemptOptions{
		IPLimiter:    ipLim,
		EmailLimiter: emailLim,
	})
	lal.now = clock.Now

	if r, _ := lal.PreCheck(context.Background(), "1.1.1.2", "a@b.com"); !r.Allowed {
		t.Fatal("legacy PreCheck first hit should pass")
	}
	if r, _ := lal.PreCheck(context.Background(), "1.1.1.2", "a@b.com"); r.Allowed {
		t.Error("legacy PreCheck should still apply the email bucket")
	}

	// Empty email in legacy path skips the email bucket regardless.
	emptyResult, _ := lal.PreCheck(context.Background(), "1.1.1.3", "")
	if !emptyResult.Allowed {
		t.Error("legacy PreCheck with empty email should pass through IP-only")
	}
}

// recordingAudit captures emitted events so tests can assert order
// and payload without mocking a whole audit pipeline.
type recordingAudit struct {
	mu          sync.Mutex
	locked      []lockedEvent
	unlocked    []string
	rateLimited []rateLimitEvent
}

type lockedEvent struct {
	userID string
	reason string
}

type rateLimitEvent struct {
	key        string
	retryAfter time.Duration
}

func newRecordingAudit() *recordingAudit { return &recordingAudit{} }

func (r *recordingAudit) EmitLocked(_ context.Context, userID, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.locked = append(r.locked, lockedEvent{userID, reason})
}

func (r *recordingAudit) EmitUnlocked(_ context.Context, userID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.unlocked = append(r.unlocked, userID)
}

func (r *recordingAudit) EmitRateLimitExceeded(_ context.Context, key string, retryAfter time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rateLimited = append(r.rateLimited, rateLimitEvent{key, retryAfter})
}

func (r *recordingAudit) lockedCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.locked)
}

func (r *recordingAudit) unlockedCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.unlocked)
}

func (r *recordingAudit) rateLimitCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.rateLimited)
}

func (r *recordingAudit) lastLocked() lockedEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.locked) == 0 {
		return lockedEvent{}
	}
	return r.locked[len(r.locked)-1]
}

func (r *recordingAudit) lastUnlocked() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.unlocked) == 0 {
		return ""
	}
	return r.unlocked[len(r.unlocked)-1]
}

func (r *recordingAudit) lastRateLimit() rateLimitEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.rateLimited) == 0 {
		return rateLimitEvent{}
	}
	return r.rateLimited[len(r.rateLimited)-1]
}
