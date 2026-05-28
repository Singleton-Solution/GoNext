package login

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/audit"
	"github.com/Singleton-Solution/GoNext/packages/go/ratelimit"
	"github.com/Singleton-Solution/GoNext/packages/go/session"
)

// UserRecord is the projection of the users + user_passwords rows
// required to verify a credential. Tests fake this struct directly so
// we don't need to spin up a real Postgres for unit coverage.
//
// Hash is the PHC argon2id string from user_passwords.password_hash.
// Empty Hash means "user found but has no password set" (a legitimate
// state for OAuth-only accounts), which the service treats as "wrong
// credentials" — we never accept an empty hash as a match.
type UserRecord struct {
	ID    string
	Email string
	Hash  string

	// Status is the lifecycle column from users.status. Active accounts
	// pass; suspended / deleted return ErrInvalidCredentials so the
	// admin-side decision isn't leaked to a stranger.
	Status string

	// Roles are the role slugs assigned to this user, projected from
	// users.meta.roles by the SQL lookup. The service stamps these
	// into the session's data map so RequireSession-derived principals
	// carry the role grant. Empty slice → anonymous-equivalent
	// authorization (login succeeds but every capability check fails).
	Roles []string
}

// TOTPRecord describes a user's TOTP enrolment. SecretBase32 is the
// base32-encoded RFC 4226 secret accepted by packages/go/auth/totp.
// RecoveryHashes are argon2id PHC strings (recovery code material is
// HMAC-bound to a per-deployment pepper just like passwords are).
//
// Enabled is true once enrolment has been confirmed; users mid-flow
// may have a secret persisted but not yet active — those count as
// "not enrolled" for login purposes.
type TOTPRecord struct {
	Enabled        bool
	SecretBase32   string
	RecoveryHashes [][]byte
}

// ErrUserNotFound is returned by UserLookup when no row matches the
// provided email. The Service treats it the same as a hash miss —
// see the constant-time guarantee in doc.go.
var ErrUserNotFound = errors.New("login: user not found")

// ErrTOTPNotEnabled is returned by TOTPLookup for accounts without a
// confirmed enrolment. Distinct from ErrUserNotFound because the
// caller may have already loaded the user; this lets a downstream
// implementation distinguish "lookup hit, 2FA disabled" from "lookup
// missed entirely".
var ErrTOTPNotEnabled = errors.New("login: TOTP not enabled")

// UserLookup is the seam between the login service and persistence.
// Implementations typically query a pgxpool but the interface is
// intentionally generic so a future cache layer (or test fake) can
// drop in without touching the service.
//
// The lookup MUST be case-insensitive on email — the users table uses
// citext, so the SQL implementation gets that for free; in-memory
// fakes need to lower-case both sides.
type UserLookup func(ctx context.Context, email string) (UserRecord, error)

// UserByIDLookup returns the user record for an already-authenticated
// userID. Used by the TOTP finalize path which only has the userID
// (recovered from the intermediate token), not an email. Implementations
// MUST be case-equivalent to UserLookup: same source of truth, same
// role projection.
type UserByIDLookup func(ctx context.Context, userID string) (UserRecord, error)

// TOTPLookup returns the TOTP enrolment for a user, or ErrTOTPNotEnabled.
// If the underlying user_totp table doesn't exist yet (the migration
// ships in a separate PR), implementations should return
// ErrTOTPNotEnabled rather than surfacing the table-not-found error;
// the service treats every miss as "2FA disabled" by design.
//
// Callers can also leave Deps.TOTPLookup nil entirely — the service
// treats nil as "no 2FA in this deployment".
type TOTPLookup func(ctx context.Context, userID string) (TOTPRecord, error)

// PasswordRehash is invoked by the service when password.Verify
// reports needsRehash=true. Implementations re-hash the (already
// validated) plaintext with the current cluster-wide parameters and
// write the new PHC string back to user_passwords. Errors are
// logged but do not fail the login — the user gets in, the rehash
// is best-effort.
//
// Leave nil to skip rehashing entirely (e.g. in tests).
type PasswordRehash func(ctx context.Context, userID, newHash string) error

// IntermediateStore persists the short-lived "password OK, awaiting
// 2FA" token between the first and second login calls. Implementations
// are typically backed by Redis; the in-memory implementation in this
// package is intended for tests.
//
// The token is opaque to the caller; only Store/Load/Delete are needed.
type IntermediateStore interface {
	Store(ctx context.Context, token string, userID string, ttl time.Duration) error
	Load(ctx context.Context, token string) (userID string, err error)
	Delete(ctx context.Context, token string) error
}

// SessionCreator is the narrow subset of *session.Manager the login
// service consumes. We extract it as an interface so unit tests can
// drop in a stub without spinning up a Redis container — the real
// session manager satisfies this interface by virtue of having the
// same method shape.
type SessionCreator interface {
	Create(ctx context.Context, userID string, data map[string]any, ttl, idleTTL time.Duration) (string, error)
}

// compile-time check that *session.Manager satisfies SessionCreator.
var _ SessionCreator = (*session.Manager)(nil)

// Deps is the wiring bundle assembled at server boot. Every field is
// load-bearing; nil values are checked at handler-construction time
// rather than per-request to fail loudly when wiring is wrong.
//
// Pepper is the GONEXT_AUTH_PEPPER value passed to password.Verify so
// the database alone is not enough to attempt an offline crack.
type Deps struct {
	// Lookup performs the email -> UserRecord query. Required.
	Lookup UserLookup

	// UserByID performs the userID -> UserRecord query. Used by the
	// TOTP finalize path so the post-2FA session carries the same
	// roles the no-2FA path would. May be nil — when nil, the
	// finalize path degrades to a session with no roles (same
	// behavior as before this field landed).
	UserByID UserByIDLookup

	// TOTPLookup performs the user -> TOTPRecord query. May be nil
	// when 2FA isn't wired in this deployment.
	TOTPLookup TOTPLookup

	// Rehash is the password-rotation callback. May be nil.
	Rehash PasswordRehash

	// Pepper is the HMAC pepper passed to password.Hash / Verify.
	// May be empty in dev; the service does not enforce a non-empty
	// pepper (that's the config layer's job).
	Pepper []byte

	// Sessions is the manager that mints session tokens. Required.
	// Production code wires *session.Manager here; tests can pass a
	// stub satisfying SessionCreator.
	Sessions SessionCreator

	// SessionAbsoluteTTL is the lifetime passed to Sessions.Create.
	// Required (> 0).
	SessionAbsoluteTTL time.Duration

	// SessionIdleTTL is the rolling idle window passed to Sessions.Create.
	// Required (> 0, ≤ SessionAbsoluteTTL).
	SessionIdleTTL time.Duration

	// Limiter is the rate-limit + lockout gateway. Required.
	Limiter *ratelimit.LoginAttemptLimiter

	// AuditEmitter is the audit log emitter. Required — every
	// transition fires an event. Wire packages/go/audit.NewEmitter
	// against your durable store in production; tests use a memory
	// emitter.
	AuditEmitter *audit.Emitter

	// Intermediate is the partial-login token store. Required when
	// TOTPLookup is non-nil; ignored otherwise.
	Intermediate IntermediateStore

	// IntermediateTTL is the lifetime of the partial-login token.
	// Defaults to 5 minutes if zero.
	IntermediateTTL time.Duration

	// Insecure, when true, drops the Secure attribute from the session
	// cookie so plain-HTTP dev servers work. Production must leave it
	// false.
	Insecure bool

	// CookieDomain overrides the Domain attribute on the session
	// cookie. Empty scopes the cookie to the exact serving host.
	CookieDomain string

	// CookieName overrides session.CookieName. Empty uses the package
	// default ("sid").
	CookieName string

	// Now is the time source. Defaults to time.Now. Tests pin this
	// for deterministic timestamps in audit events.
	Now func() time.Time

	// Log is the structured logger. Defaults to slog.Default.
	Log *slog.Logger
}

// validate returns an error if Deps is incomplete. The handler calls
// it at Mount time; a missing field is a wiring bug that should crash
// the server at boot, not produce a confusing 500 at request time.
func (d *Deps) validate() error {
	if d.Lookup == nil {
		return errors.New("login.Deps: Lookup is required")
	}
	if d.Sessions == nil {
		return errors.New("login.Deps: Sessions is required")
	}
	if d.SessionAbsoluteTTL <= 0 {
		return errors.New("login.Deps: SessionAbsoluteTTL must be > 0")
	}
	if d.SessionIdleTTL <= 0 || d.SessionIdleTTL > d.SessionAbsoluteTTL {
		return errors.New("login.Deps: SessionIdleTTL must be in (0, SessionAbsoluteTTL]")
	}
	if d.Limiter == nil {
		return errors.New("login.Deps: Limiter is required")
	}
	if d.AuditEmitter == nil {
		return errors.New("login.Deps: AuditEmitter is required")
	}
	if d.TOTPLookup != nil && d.Intermediate == nil {
		return errors.New("login.Deps: Intermediate is required when TOTPLookup is set")
	}
	return nil
}

// defaults fills in nil-tolerable fields. Call after validate.
func (d *Deps) defaults() {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	if d.IntermediateTTL <= 0 {
		d.IntermediateTTL = 5 * time.Minute
	}
}
