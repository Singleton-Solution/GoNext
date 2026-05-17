package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// LoginAttemptLimiter combines two rate-limit buckets (per-IP and
// per-email) with an account-lockout counter to mitigate brute-force
// password attacks. The design follows docs/06-auth-permissions.md §12
// and the AC in issue #195.
//
// Defaults match the spec there:
//   - Per IP: 20 attempts / 5 minutes (rolling token bucket).
//   - Per email: 5 attempts / 15 minutes — applied ONLY against known
//     emails to avoid the enumeration oracle (see CheckInput.EmailExists).
//   - Lockout: 10 consecutive failures → lock 30 minutes, auto-unlock.
//
// Two oracles are explicitly defended against:
//
//  1. Lockout-status oracle. Lockout state is NOT consulted by Check —
//     it is a separate query (IsLocked) the caller invokes only AFTER
//     a successful password match. This avoids letting an attacker
//     confirm a target email is registered by triggering then probing
//     for the lock.
//
//  2. Email-existence oracle. The per-email bucket is applied ONLY if
//     the caller has confirmed the email exists (CheckInput.EmailExists
//     = true). For unknown emails the per-email bucket is skipped —
//     otherwise an attacker watching for a 429 (vs an unlimited 401)
//     gains a registration probe at zero cost (see issue #195 AC).
//
// CONTRACT: callers MUST look up the email in their user store first,
// and pass the result as EmailExists. Passing EmailExists=true for an
// unknown email re-opens the oracle.
type LoginAttemptLimiter struct {
	ipLimiter    Limiter
	emailLimiter Limiter

	lockoutThreshold int
	lockoutWindow    time.Duration

	failureStore FailureStore
	audit        AuditEmitter

	now func() time.Time
}

// LoginAttemptOptions configures a LoginAttemptLimiter. Fields are all
// optional EXCEPT IPLimiter and EmailLimiter; zero values get sensible
// defaults from the spec.
type LoginAttemptOptions struct {
	// IPLimiter throttles attempts by client IP. Required.
	IPLimiter Limiter
	// EmailLimiter throttles attempts by email address — but ONLY when
	// the caller passes EmailExists=true to Check. Required.
	EmailLimiter Limiter

	// LockoutThreshold defaults to 10.
	LockoutThreshold int
	// LockoutWindow defaults to 30 minutes.
	LockoutWindow time.Duration

	// FailureStore defaults to NewMemoryFailureStore() if nil. In
	// production prefer a durable implementation (PostgresFailureStore)
	// so lockouts survive restarts and are shared across replicas.
	FailureStore FailureStore

	// Audit defaults to NopAuditEmitter{} if nil. Production callers
	// should wire packages/go/audit's emitter here so the three
	// AC-required events land in the audit log.
	Audit AuditEmitter
}

// ErrMissingLimiter is returned when LoginAttemptLimiter construction
// is called without one of the required limiter dependencies.
var ErrMissingLimiter = errors.New("ratelimit: missing required limiter")

// NewLoginAttemptLimiter constructs a LoginAttemptLimiter from the
// supplied options. Returns an error if required fields are missing.
func NewLoginAttemptLimiter(opts LoginAttemptOptions) (*LoginAttemptLimiter, error) {
	if opts.IPLimiter == nil {
		return nil, fmt.Errorf("ratelimit.NewLoginAttemptLimiter: IPLimiter is nil: %w", ErrMissingLimiter)
	}
	if opts.EmailLimiter == nil {
		return nil, fmt.Errorf("ratelimit.NewLoginAttemptLimiter: EmailLimiter is nil: %w", ErrMissingLimiter)
	}

	threshold := opts.LockoutThreshold
	if threshold <= 0 {
		threshold = 10
	}
	window := opts.LockoutWindow
	if window <= 0 {
		window = 30 * time.Minute
	}
	store := opts.FailureStore
	if store == nil {
		store = NewMemoryFailureStore()
	}
	audit := opts.Audit
	if audit == nil {
		audit = NopAuditEmitter{}
	}

	return &LoginAttemptLimiter{
		ipLimiter:        opts.IPLimiter,
		emailLimiter:     opts.EmailLimiter,
		lockoutThreshold: threshold,
		lockoutWindow:    window,
		failureStore:     store,
		audit:            audit,
		now:              time.Now,
	}, nil
}

// CheckInput is the argument bundle for Check. It is a struct (rather
// than a positional argument list) because the EmailExists flag is
// load-bearing and we want call sites to spell it out explicitly: a
// caller who passes the wrong value here re-opens the enumeration
// oracle, so a named field is much harder to misuse than a positional
// bool.
type CheckInput struct {
	// IP is the client IP. Required (empty IP bypasses the IP bucket,
	// which should only happen in tests).
	IP string

	// Email is the (raw, un-normalized) email submitted by the user.
	// It is normalized internally before being used as a bucket key.
	// May be empty for flows that don't carry an email.
	Email string

	// EmailExists MUST be set to true only if the caller has confirmed
	// (via a DB lookup) that Email corresponds to a registered user.
	// When false, the per-email bucket is skipped — this is the oracle
	// defense for issue #195.
	EmailExists bool
}

// Check consults the rate-limit buckets before a credential check. If
// either bucket is exhausted, the request should be refused with a 429
// response carrying RetryAfter.
//
// The per-email bucket is consulted ONLY when in.EmailExists is true.
// For unknown emails the call returns identical "allowed" results to
// the unknown-IP case (within burst), closing the enumeration oracle
// flagged in issue #195's AC.
//
// Check deliberately does NOT consult the lockout state — see the
// type docstring's "Lockout-status oracle" reasoning.
func (l *LoginAttemptLimiter) Check(ctx context.Context, in CheckInput) (PreCheckResult, error) {
	res := PreCheckResult{Allowed: true}

	allowed, retryAfter, err := l.ipLimiter.Allow(ctx, in.IP)
	if err != nil {
		return res, fmt.Errorf("ratelimit.LoginAttemptLimiter.Check: ip bucket: %w", err)
	}
	if !allowed {
		res.Allowed = false
		res.Reason = ReasonIPRateLimit
		res.RetryAfter = retryAfter
		l.audit.EmitRateLimitExceeded(ctx, in.IP, retryAfter)
		return res, nil
	}

	// Per-email bucket is applied ONLY for known emails. Skipping it
	// for unknown emails is what closes the enumeration oracle: an
	// attacker who guesses a non-existent email gets the same response
	// shape (no per-email throttling) as an attacker who hasn't been
	// throttled, so they can't distinguish "this email is registered"
	// from "this email isn't" by watching for 429.
	if in.Email != "" && in.EmailExists {
		emailKey := normalizeEmail(in.Email)
		allowed, retryAfter, err = l.emailLimiter.Allow(ctx, emailKey)
		if err != nil {
			return res, fmt.Errorf("ratelimit.LoginAttemptLimiter.Check: email bucket: %w", err)
		}
		if !allowed {
			res.Allowed = false
			res.Reason = ReasonEmailRateLimit
			res.RetryAfter = retryAfter
			l.audit.EmitRateLimitExceeded(ctx, emailKey, retryAfter)
			return res, nil
		}
	}
	return res, nil
}

// PreCheck is the legacy 2-arg entry point retained for backward
// compatibility with the PR's earlier API. New code should call Check
// with an explicit EmailExists flag — PreCheck conservatively assumes
// EmailExists=true (the pre-fix behavior), which leaks the
// enumeration oracle. The bool return is intentionally absent from
// the signature so a grep for ".PreCheck(" reveals call sites that
// must migrate.
//
// Deprecated: use Check. PreCheck applies the per-email bucket
// unconditionally, which re-opens the email-existence oracle that
// issue #195 requires us to close.
func (l *LoginAttemptLimiter) PreCheck(ctx context.Context, ip, email string) (PreCheckResult, error) {
	return l.Check(ctx, CheckInput{IP: ip, Email: email, EmailExists: email != ""})
}

// RecordFailure increments the consecutive-failure counter for
// userID. If the counter crosses the configured threshold, the
// account is locked for the configured window and the
// auth.login.locked audit event fires.
//
// Callers should invoke this after every failed credential check
// where the email IS known (wrong password on a real account). For
// unknown-email submissions there is no userID to record against —
// the per-IP bucket is the only protection in that path, by design
// (an unknown-email counter is the enumeration oracle this whole
// refactor exists to close).
//
// Returns true if this failure triggered a new lockout (i.e. the
// transition into locked state); returns false if the account was
// already locked or the threshold wasn't reached. The transition
// edge is the right place for an audit event, which the limiter
// fires internally — callers don't need to re-emit.
func (l *LoginAttemptLimiter) RecordFailure(ctx context.Context, userID string) (locked bool, err error) {
	if userID == "" {
		return false, nil
	}
	count, lockedUntil, err := l.failureStore.IncrementFailure(ctx, userID)
	if err != nil {
		return false, fmt.Errorf("ratelimit.LoginAttemptLimiter.RecordFailure: %w", err)
	}
	now := l.now()
	if lockedUntil.After(now) {
		// Already locked; no new lock event to report. Suppressing the
		// duplicate event here is intentional — audit pipelines should
		// see one "locked" row per lockout, not one per failure during
		// the lockout.
		return false, nil
	}
	if count >= l.lockoutThreshold {
		until := now.Add(l.lockoutWindow)
		if err := l.failureStore.SetLockedUntil(ctx, userID, until); err != nil {
			return false, fmt.Errorf("ratelimit.LoginAttemptLimiter.RecordFailure: lock: %w", err)
		}
		l.audit.EmitLocked(ctx, userID, AuditReasonThresholdExceeded)
		return true, nil
	}
	return false, nil
}

// RecordSuccess clears the failure counter and lockout state for
// userID. Called after a successful credential check. If the user
// was actively locked, emits auth.login.unlocked.
func (l *LoginAttemptLimiter) RecordSuccess(ctx context.Context, userID string) error {
	if userID == "" {
		return nil
	}
	// Read first so we can fire the unlock event only on the active-
	// lockout transition. The read+clear is two statements, which is
	// fine: on the success path there is no concurrent failure on the
	// same userID (the credential check already succeeded once).
	_, lockedUntil, err := l.failureStore.GetFailures(ctx, userID)
	if err != nil {
		return fmt.Errorf("ratelimit.LoginAttemptLimiter.RecordSuccess: get: %w", err)
	}
	if err := l.failureStore.ClearFailures(ctx, userID); err != nil {
		return fmt.Errorf("ratelimit.LoginAttemptLimiter.RecordSuccess: clear: %w", err)
	}
	if lockedUntil.After(l.now()) {
		l.audit.EmitUnlocked(ctx, userID)
	}
	return nil
}

// IsLocked reports whether userID is currently in lockout, and if so,
// when the lock expires. Callers invoke this AFTER confirming the
// password is correct — only then is it safe to reveal lockout status
// (per the oracle-avoidance design in §12.2).
//
// Returns (false, zero) for an unknown userID or for an account whose
// lock has expired naturally.
func (l *LoginAttemptLimiter) IsLocked(ctx context.Context, userID string) (locked bool, until time.Time, err error) {
	if userID == "" {
		return false, time.Time{}, nil
	}
	_, lockedUntil, err := l.failureStore.GetFailures(ctx, userID)
	if err != nil {
		return false, time.Time{}, fmt.Errorf("ratelimit.LoginAttemptLimiter.IsLocked: %w", err)
	}
	if lockedUntil.After(l.now()) {
		return true, lockedUntil, nil
	}
	return false, time.Time{}, nil
}

// PreCheckResult is the verdict returned by LoginAttemptLimiter.Check.
//
// When Allowed is false the caller should respond with 429 and a
// Retry-After header set to RetryAfter. Reason is provided for audit
// logging (auth.ratelimit.exceeded) and metrics. Reason MUST NOT be
// echoed to the client — distinguishing "ip_rate_limit" from
// "email_rate_limit" in a response is itself a (weaker) oracle.
type PreCheckResult struct {
	Allowed    bool
	Reason     PreCheckReason
	RetryAfter time.Duration
}

// PreCheckReason identifies which bucket denied the request, for
// audit and metric labelling.
type PreCheckReason string

const (
	ReasonNone           PreCheckReason = ""
	ReasonIPRateLimit    PreCheckReason = "ip_rate_limit"
	ReasonEmailRateLimit PreCheckReason = "email_rate_limit"
)

// IPLimiter is a thin convenience wrapper around a Limiter for the
// common case of a per-IP general API rate limit. It exists so callers
// can keep a typed reference and intention-revealing constructor in
// their wiring code; functionally it forwards every call to the
// underlying Limiter.
type IPLimiter struct {
	Limiter
}

// NewMemoryIPLimiter returns an IPLimiter backed by a MemoryLimiter
// with the given policy. Convenience constructor for the dev/single-
// instance case.
func NewMemoryIPLimiter(p Policy) (*IPLimiter, error) {
	inner, err := NewMemoryLimiter(p)
	if err != nil {
		return nil, fmt.Errorf("ratelimit.NewMemoryIPLimiter: %w", err)
	}
	return &IPLimiter{Limiter: inner}, nil
}

// normalizeEmail is the canonical form used for bucket keys and
// failure-store entries. It lower-cases and trims surrounding white-
// space. It deliberately does NOT do "Gmail dots" normalization or
// plus-tag stripping; those are out of scope for rate limiting (the
// limiter should mirror the auth system's identifier, not invent a
// new one). Keep this in sync with packages/go/auth's email normalizer.
func normalizeEmail(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
