package setup

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// InstallationOptionKey is the options-table key that signals
// "the install wizard has been completed". Its presence (regardless of
// value type) is what locks the wizard; the value carries the RFC3339
// timestamp for audit purposes.
const InstallationOptionKey = "core.site.installation_completed_at"

// SiteNameOptionKey + SiteURLOptionKey are the keys the install handler
// writes alongside the lock so the freshly-seeded site has the operator's
// chosen values rather than the placeholders from migration 000008.
const (
	SiteNameOptionKey = "core.site.name"
	SiteURLOptionKey  = "core.site.url"
)

// MinPasswordLength is the floor enforced on the install endpoint. The
// wizard's strength meter is a UX layer on top of this — the server is
// the gate. 12 characters matches NIST SP 800-63B's modern guidance for
// human-typed passwords protected by a slow KDF (argon2id, here).
//
// Keep this value low enough that a passphrase like "correct horse"
// passes (correct-horse-battery-staple is the canonical example), but
// high enough that an attacker can't precompute the candidate set with
// a CPU rig.
const MinPasswordLength = 12

// DefaultRateLimit defines the per-IP install-attempt budget. Tuned to
// "5 attempts / hour" per the issue spec; an operator who legitimately
// needs to retry a typo'd email a sixth time within the same hour can
// wait or restart the API binary.
var DefaultRateLimit = RateLimitPolicy{
	Capacity:   5,
	RefillRate: 5.0 / 3600.0, // 5 tokens regenerate over an hour
}

// RateLimitPolicy is the small subset of ratelimit.Policy this package
// consumes. Extracted as a struct so tests can override without dragging
// the full ratelimit package into the import graph.
type RateLimitPolicy struct {
	// Capacity is the maximum burst size (initial token count).
	Capacity int

	// RefillRate is the per-second refill rate. For "N attempts per
	// hour" use N/3600.
	RefillRate float64
}

// UserCreator persists the bootstrap admin row + password hash in one
// atomic write. Implementations typically wrap a pgx Tx so the users +
// user_passwords rows are committed together.
//
// The Role field on UserCreateInput is informational for now (v1 stores
// it in users.meta under "role" because no roles table exists yet — see
// docs/06-auth-permissions.md §6.1). When the roles migration lands it
// becomes a real FK write inside the same transaction.
type UserCreator interface {
	Create(ctx context.Context, in UserCreateInput) (userID string, err error)
}

// UserCreateInput is the contract the UserCreator implements against.
// Email is required and must already be normalized (lower-cased,
// trimmed); the handler does that before calling.
type UserCreateInput struct {
	Email        string
	Handle       string
	DisplayName  string
	PasswordHash string
	Role         string
}

// OptionStore is the narrow subset of settings.Store the install path
// needs. We intentionally keep it small so the rest of the settings
// package's registry / autoload machinery isn't a dependency of the
// setup boot path.
//
// Has returns true iff a value has been previously written. The install
// lock check uses it; the rest of the handler uses Write to persist the
// site name / URL / install marker.
type OptionStore interface {
	// Has reports whether the key has ever been written through this
	// store. The install handler uses it to determine whether
	// installation has already happened.
	Has(ctx context.Context, key string) (bool, error)

	// Write persists value under key. The implementation is expected to
	// be idempotent for the install path — the handler always writes
	// fresh values, never appends.
	Write(ctx context.Context, key string, value any) error
}

// SessionCreator mints the session cookie returned at the end of a
// successful install. Matches the same shape login uses so the
// installer logs in directly without a separate POST /auth/login.
type SessionCreator interface {
	Create(ctx context.Context, userID string, data map[string]any, ttl, idleTTL time.Duration) (string, error)
}

// Limiter is the per-IP rate-limit gateway. Returns (allowed, retryAfter,
// err). On err, the handler fails CLOSED — a misbehaving Redis must
// not turn the install endpoint into a free brute-force oracle.
type Limiter interface {
	Allow(ctx context.Context, key string) (allowed bool, retryAfter time.Duration, err error)
}

// PasswordHasher computes the argon2id PHC string the user_passwords
// row stores. Production wires packages/go/auth/password.Hash; tests
// substitute a deterministic stub so a unit test doesn't pay 200ms of
// argon2 latency on every call.
type PasswordHasher func(plaintext string, pepper []byte) (string, error)

// Deps is the wiring bundle assembled at server boot. Every required
// field is checked at Mount time so a misconfigured deployment fails to
// start rather than surfacing a confusing 500 on the first install
// attempt.
type Deps struct {
	// Users persists the bootstrap admin. Required.
	Users UserCreator

	// Options reads / writes the install marker + site name / URL.
	// Required.
	Options OptionStore

	// Sessions mints the install-completion cookie. Required.
	Sessions SessionCreator

	// Hash computes the password PHC string. Required.
	Hash PasswordHasher

	// Pepper is forwarded to Hash. Empty in dev; required in production
	// (the config layer enforces non-empty; this package does not).
	Pepper []byte

	// Limiter is the per-IP brute-force gate. Required.
	Limiter Limiter

	// SessionAbsoluteTTL is the cookie's Max-Age and the session's
	// absolute deadline. Required (> 0).
	SessionAbsoluteTTL time.Duration

	// SessionIdleTTL is the session's rolling idle window. Required
	// (> 0, ≤ SessionAbsoluteTTL).
	SessionIdleTTL time.Duration

	// CookieName / CookieDomain mirror the login package's knobs so
	// the install cookie is indistinguishable from a login one.
	CookieName   string
	CookieDomain string

	// Insecure drops Secure from the install cookie so plain-HTTP dev
	// servers work. Production must leave it false.
	Insecure bool

	// Now is the time source. Defaults to time.Now. Tests pin it to a
	// fixed instant so the audit timestamp on the install marker is
	// deterministic.
	Now func() time.Time

	// Log is the structured logger. Defaults to slog.Default.
	Log *slog.Logger
}

// validate returns an error if Deps is incomplete. The handler calls it
// at Mount time so wiring bugs crash the server at boot, not at request
// time.
func (d *Deps) validate() error {
	if d.Users == nil {
		return errors.New("setup.Deps: Users is required")
	}
	if d.Options == nil {
		return errors.New("setup.Deps: Options is required")
	}
	if d.Sessions == nil {
		return errors.New("setup.Deps: Sessions is required")
	}
	if d.Hash == nil {
		return errors.New("setup.Deps: Hash is required")
	}
	if d.Limiter == nil {
		return errors.New("setup.Deps: Limiter is required")
	}
	if d.SessionAbsoluteTTL <= 0 {
		return errors.New("setup.Deps: SessionAbsoluteTTL must be > 0")
	}
	if d.SessionIdleTTL <= 0 || d.SessionIdleTTL > d.SessionAbsoluteTTL {
		return errors.New("setup.Deps: SessionIdleTTL must be in (0, SessionAbsoluteTTL]")
	}
	return nil
}

// defaults fills the nil-tolerable fields after validate. Keeps the
// Mount-time wiring loud about required things and quiet about
// conveniences.
func (d *Deps) defaults() {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
}
