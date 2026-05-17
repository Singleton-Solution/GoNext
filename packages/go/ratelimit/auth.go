package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// LoginAttemptLimiter combines two rate-limit buckets (per-IP and
// per-email) with an account-lockout counter to mitigate brute-force
// password attacks. The design follows docs/06-auth-permissions.md §12.
//
// Defaults match the spec there:
//   - Per IP: 20 attempts / 5 minutes (rolling token bucket)
//   - Per email: 5 attempts / 15 minutes (rolling)
//   - Lockout: 10 consecutive failures → lock 30 minutes, auto-unlock
//
// Lockout state is intentionally NOT surfaced through CheckPreCheck —
// it's a separate query (IsLocked) the caller invokes only AFTER a
// successful password match. This avoids the "you locked them" oracle
// that lets an attacker confirm a target email is registered without
// guessing the password (see §12.2).
type LoginAttemptLimiter struct {
	ipLimiter    Limiter
	emailLimiter Limiter

	// lockoutThreshold is the consecutive-failure count that triggers
	// an account lock. Default 10.
	lockoutThreshold int

	// lockoutWindow is how long an account stays locked once tripped.
	// Default 30 minutes.
	lockoutWindow time.Duration

	// failureStore tracks per-email failure state. In v1 we use an in-
	// memory store; the interface exists so plugins / multi-instance
	// deployments can swap in a Redis-backed implementation.
	failureStore FailureStore

	now func() time.Time
}

// LoginAttemptOptions configures a LoginAttemptLimiter. Fields are all
// optional; zero values get sensible defaults from the spec.
type LoginAttemptOptions struct {
	// IPLimiter throttles attempts by client IP. Required.
	IPLimiter Limiter
	// EmailLimiter throttles attempts by email address (after lookup;
	// see LoginAttemptLimiter docstring for the existing-email caveat).
	// Required.
	EmailLimiter Limiter

	// LockoutThreshold defaults to 10.
	LockoutThreshold int
	// LockoutWindow defaults to 30 minutes.
	LockoutWindow time.Duration

	// FailureStore defaults to NewMemoryFailureStore() if nil.
	FailureStore FailureStore
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

	return &LoginAttemptLimiter{
		ipLimiter:        opts.IPLimiter,
		emailLimiter:     opts.EmailLimiter,
		lockoutThreshold: threshold,
		lockoutWindow:    window,
		failureStore:     store,
		now:              time.Now,
	}, nil
}

// PreCheck consults both rate buckets before a credential check. If
// either bucket is exhausted, the request should be refused with a 429
// response carrying RetryAfter. PreCheck deliberately does NOT consult
// the lockout state — see the type docstring for the oracle reasoning.
//
// Callers should pass a non-empty email after normalizing it
// (lower-cased, trimmed). The empty-email case is allowed (the email
// bucket is skipped) so the limiter still works on flows that don't
// require an email field, e.g. pre-typing pre-flight.
func (l *LoginAttemptLimiter) PreCheck(ctx context.Context, ip, email string) (PreCheckResult, error) {
	res := PreCheckResult{Allowed: true}

	allowed, retryAfter, err := l.ipLimiter.Allow(ctx, ip)
	if err != nil {
		return res, fmt.Errorf("ratelimit.LoginAttemptLimiter.PreCheck: ip bucket: %w", err)
	}
	if !allowed {
		res.Allowed = false
		res.Reason = ReasonIPRateLimit
		res.RetryAfter = retryAfter
		return res, nil
	}

	if email != "" {
		allowed, retryAfter, err = l.emailLimiter.Allow(ctx, normalizeEmail(email))
		if err != nil {
			return res, fmt.Errorf("ratelimit.LoginAttemptLimiter.PreCheck: email bucket: %w", err)
		}
		if !allowed {
			res.Allowed = false
			res.Reason = ReasonEmailRateLimit
			res.RetryAfter = retryAfter
			return res, nil
		}
	}
	return res, nil
}

// RecordFailure increments the consecutive-failure counter for email.
// If the counter crosses the configured threshold, the account is
// locked for the configured window. Callers should invoke this after
// every failed credential check (wrong password, unknown user).
//
// Returns true if this failure triggered a new lockout (the caller may
// want to fire an audit event auth.login.locked); returns false if the
// account was already locked or the threshold wasn't reached.
func (l *LoginAttemptLimiter) RecordFailure(ctx context.Context, email string) (locked bool, err error) {
	if email == "" {
		return false, nil
	}
	key := normalizeEmail(email)
	state, err := l.failureStore.Increment(ctx, key)
	if err != nil {
		return false, fmt.Errorf("ratelimit.LoginAttemptLimiter.RecordFailure: %w", err)
	}
	now := l.now()
	if state.LockedUntil.After(now) {
		// Already locked; no new lock event to report.
		return false, nil
	}
	if state.FailureCount >= l.lockoutThreshold {
		until := now.Add(l.lockoutWindow)
		if err := l.failureStore.Lock(ctx, key, until); err != nil {
			return false, fmt.Errorf("ratelimit.LoginAttemptLimiter.RecordFailure: lock: %w", err)
		}
		return true, nil
	}
	return false, nil
}

// RecordSuccess clears the failure counter and lockout state for email.
// Called after a successful credential check.
func (l *LoginAttemptLimiter) RecordSuccess(ctx context.Context, email string) error {
	if email == "" {
		return nil
	}
	if err := l.failureStore.Clear(ctx, normalizeEmail(email)); err != nil {
		return fmt.Errorf("ratelimit.LoginAttemptLimiter.RecordSuccess: %w", err)
	}
	return nil
}

// IsLocked reports whether email is currently in lockout, and if so,
// when the lock expires. Callers invoke this AFTER confirming the
// password is correct — only then is it safe to reveal lockout status
// (per the oracle-avoidance design in §12.2).
//
// Returns (false, zero) for an unknown email or for an account whose
// lock has expired naturally.
func (l *LoginAttemptLimiter) IsLocked(ctx context.Context, email string) (locked bool, until time.Time, err error) {
	if email == "" {
		return false, time.Time{}, nil
	}
	state, err := l.failureStore.Get(ctx, normalizeEmail(email))
	if err != nil {
		return false, time.Time{}, fmt.Errorf("ratelimit.LoginAttemptLimiter.IsLocked: %w", err)
	}
	now := l.now()
	if state.LockedUntil.After(now) {
		return true, state.LockedUntil, nil
	}
	return false, time.Time{}, nil
}

// PreCheckResult is the verdict returned by LoginAttemptLimiter.PreCheck.
//
// When Allowed is false the caller should respond with 429 and a
// Retry-After header set to RetryAfter. Reason is provided for audit
// logging (auth.ratelimit.exceeded) and metrics.
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

// FailureStore tracks per-account failed-login counters and lockout
// expirations. It's separate from Limiter because the semantics are
// different (set/reset, not bucket math) and because operators may
// want to swap in a persistent backend (Postgres column) so lockouts
// survive restarts.
type FailureStore interface {
	// Increment adds one to the failure counter for key and returns
	// the post-increment state.
	Increment(ctx context.Context, key string) (FailureState, error)
	// Get returns the current state without mutating it.
	Get(ctx context.Context, key string) (FailureState, error)
	// Lock sets the LockedUntil timestamp for key.
	Lock(ctx context.Context, key string, until time.Time) error
	// Clear removes the state for key (counter and lock).
	Clear(ctx context.Context, key string) error
}

// FailureState is the per-account counter snapshot returned by a
// FailureStore.
type FailureState struct {
	FailureCount int
	LockedUntil  time.Time
}

// memoryFailureStore is the default in-process FailureStore. Sufficient
// for dev and single-instance deploys; production multi-instance should
// substitute a Redis or Postgres implementation that persists state
// across the fleet (see docs/06-auth-permissions.md §12 — the column
// users.failed_login_count, locked_until is the persistent option).
type memoryFailureStore struct {
	mu   sync.Mutex
	rows map[string]FailureState
}

// NewMemoryFailureStore returns a FailureStore that keeps state in
// process memory. Restarts clear all counters and unlock all accounts.
func NewMemoryFailureStore() FailureStore {
	return &memoryFailureStore{rows: make(map[string]FailureState)}
}

func (s *memoryFailureStore) Increment(_ context.Context, key string) (FailureState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.rows[key]
	state.FailureCount++
	s.rows[key] = state
	return state, nil
}

func (s *memoryFailureStore) Get(_ context.Context, key string) (FailureState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rows[key], nil
}

func (s *memoryFailureStore) Lock(_ context.Context, key string, until time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.rows[key]
	state.LockedUntil = until
	s.rows[key] = state
	return nil
}

func (s *memoryFailureStore) Clear(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.rows, key)
	return nil
}
