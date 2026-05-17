package ratelimit

import (
	"context"
	"time"
)

// AuditEmitter is the contract LoginAttemptLimiter uses to surface
// auth-relevant state transitions to an audit log. Issue #195 names
// three events explicitly:
//
//   - auth.login.locked: emitted when a failed login crosses the
//     consecutive-failure threshold and the account becomes locked.
//   - auth.login.unlocked: emitted when a successful login (or a
//     manual reset) clears an active lockout.
//   - auth.ratelimit.exceeded: emitted when a rate-limit bucket denies
//     a request. Includes the bucket key (IP or normalized email) and
//     the Retry-After hint so the audit row carries enough context to
//     identify abuse patterns.
//
// This package ships a no-op default (NopAuditEmitter) so wiring is
// optional in tests and dev. Production callers should plug in the
// packages/go/audit package's emitter — that is the intended consumer
// of these events. The integration is one line in the application's
// bootstrap (see the wiring example in this package's doc.go).
//
// Implementations MUST be non-blocking: LoginAttemptLimiter is on the
// hot path of every login request. If your audit pipeline writes
// synchronously (e.g. to Postgres), wrap it in a buffered channel
// before passing it here.
type AuditEmitter interface {
	// EmitLocked fires once per lockout transition (not once per
	// failure during a lockout — duplicate-event suppression is the
	// limiter's responsibility, not the emitter's). reason is a short
	// machine-readable label such as "threshold_exceeded".
	EmitLocked(ctx context.Context, userID, reason string)

	// EmitUnlocked fires when an active lockout is cleared, either by
	// a successful login or by an admin reset.
	EmitUnlocked(ctx context.Context, userID string)

	// EmitRateLimitExceeded fires when PreCheck (or its newer sibling
	// Check) denies a request because a token bucket was empty. key is
	// the bucket key (the IP address for the IP bucket, the normalized
	// email for the email bucket). retryAfter is the hint returned to
	// the caller.
	EmitRateLimitExceeded(ctx context.Context, key string, retryAfter time.Duration)
}

// NopAuditEmitter is the zero-cost default: every method is a no-op.
// Use this in tests, or in dev when the audit pipeline isn't wired
// yet.
type NopAuditEmitter struct{}

// EmitLocked is a no-op.
func (NopAuditEmitter) EmitLocked(context.Context, string, string) {}

// EmitUnlocked is a no-op.
func (NopAuditEmitter) EmitUnlocked(context.Context, string) {}

// EmitRateLimitExceeded is a no-op.
func (NopAuditEmitter) EmitRateLimitExceeded(context.Context, string, time.Duration) {}

// Standardized event-name constants. Callers building their own
// AuditEmitter implementation should use these when constructing
// audit rows so the event taxonomy matches issue #195.
const (
	AuditEventLoginLocked        = "auth.login.locked"
	AuditEventLoginUnlocked      = "auth.login.unlocked"
	AuditEventRateLimitExceeded  = "auth.ratelimit.exceeded"
	AuditReasonThresholdExceeded = "threshold_exceeded"
)
