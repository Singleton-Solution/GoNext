package passwordreset

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DefaultTTL is the time-to-live of a single reset token. 1 hour
// matches OWASP's "Password Reset Cheat Sheet" upper bound: long
// enough that a user's email client can ferry the message through a
// spam filter; short enough that a stolen archived inbox isn't a
// perpetual takeover primitive.
const DefaultTTL = 1 * time.Hour

// Errors returned by the [TokenStore] implementations.
var (
	// ErrTokenNotFound is returned by [TokenStore.Consume] when the
	// supplied hash is absent, expired, or already redeemed. The
	// handler treats all three cases identically — the wire response
	// is "invalid_or_expired_token" in every case so an attacker
	// cannot tell which path was taken.
	ErrTokenNotFound = errors.New("passwordreset: token not found")

	// ErrUserNotFound is returned by [UserStore] lookups when the
	// supplied identifier (email or user_id) resolves to no row.
	ErrUserNotFound = errors.New("passwordreset: user not found")
)

// TokenStore is the persistence seam for password reset tokens. The
// production implementation is [PgxTokenStore]; tests typically pass an
// in-memory fake.
type TokenStore interface {
	// Save persists tokenHash for userID with the supplied expiry.
	// Implementations write a single row to password_reset_tokens.
	Save(ctx context.Context, tokenHash, userID string, expiresAt time.Time) error

	// Consume looks up tokenHash and atomically marks it used. Returns
	// the user_id whose reset is being completed, or [ErrTokenNotFound]
	// when the token is unknown, expired, or already redeemed.
	//
	// The atomicity matters: two parallel confirm calls with the same
	// token must result in exactly one success. The implementation
	// uses an UPDATE ... WHERE used_at IS NULL ... RETURNING dance to
	// get that guarantee in a single round-trip.
	Consume(ctx context.Context, tokenHash string, now time.Time) (string, error)
}

// UserStore is the persistence seam for the user-side operations the
// flow needs:
//
//   - LookupIDByEmail resolves the email a request body carries into
//     a user_id (the token is stored against the user_id, not the
//     email, so renames don't break in-flight resets).
//   - UpdatePassword rewrites user_passwords.password_hash for the
//     supplied user.
type UserStore interface {
	LookupIDByEmail(ctx context.Context, email string) (userID string, err error)
	UpdatePassword(ctx context.Context, userID, newHash string) error
}

// SessionRevoker is the narrow subset of [session.Manager] the confirm
// handler uses. Stated as an interface so tests don't need to spin up
// a Redis-backed manager.
type SessionRevoker interface {
	DeleteAllForUser(ctx context.Context, userID string) error
}

// PgxTokenStore is the production [TokenStore] backed by pgxpool.
type PgxTokenStore struct {
	pool *pgxpool.Pool
}

// NewPgxTokenStore returns a TokenStore over pool. Returns an error if
// pool is nil so a wiring mistake fails fast at boot.
func NewPgxTokenStore(pool *pgxpool.Pool) (*PgxTokenStore, error) {
	if pool == nil {
		return nil, errors.New("passwordreset: pgx pool is nil")
	}
	return &PgxTokenStore{pool: pool}, nil
}

// Save inserts a new row into password_reset_tokens. The user_id and
// expiresAt are written as-is; the caller is responsible for computing
// expiresAt from a deterministic clock so tests can pin the value.
func (s *PgxTokenStore) Save(ctx context.Context, tokenHash, userID string, expiresAt time.Time) error {
	if tokenHash == "" {
		return errors.New("passwordreset: tokenHash is required")
	}
	if userID == "" {
		return errors.New("passwordreset: userID is required")
	}
	if expiresAt.IsZero() {
		return errors.New("passwordreset: expiresAt is required")
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO password_reset_tokens (user_id, token_hash, expires_at)
		 VALUES ($1::uuid, $2, $3)`,
		userID, tokenHash, expiresAt,
	)
	if err != nil {
		return fmt.Errorf("passwordreset: insert token: %w", err)
	}
	return nil
}

// Consume marks the row identified by tokenHash as used and returns
// the user_id. The UPDATE ... RETURNING in a single statement
// guarantees at-most-once consumption even under concurrent confirm
// calls — the WHERE clause filters on `used_at IS NULL AND expires_at
// > now` so the second caller falls through to "no rows".
func (s *PgxTokenStore) Consume(ctx context.Context, tokenHash string, now time.Time) (string, error) {
	if tokenHash == "" {
		return "", ErrTokenNotFound
	}
	var userID string
	err := s.pool.QueryRow(ctx,
		`UPDATE password_reset_tokens
		 SET used_at = $2
		 WHERE token_hash = $1
		   AND used_at IS NULL
		   AND expires_at > $2
		 RETURNING user_id::text`,
		tokenHash, now,
	).Scan(&userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrTokenNotFound
		}
		return "", fmt.Errorf("passwordreset: consume token: %w", err)
	}
	return userID, nil
}

// PgxUserStore is the production [UserStore] backed by pgxpool.
type PgxUserStore struct {
	pool *pgxpool.Pool
}

// NewPgxUserStore returns a UserStore over pool. Returns an error if
// pool is nil.
func NewPgxUserStore(pool *pgxpool.Pool) (*PgxUserStore, error) {
	if pool == nil {
		return nil, errors.New("passwordreset: pgx pool is nil")
	}
	return &PgxUserStore{pool: pool}, nil
}

// LookupIDByEmail returns the user_id whose email matches. Case-
// insensitive (users.email is citext). The status filter excludes
// soft-deleted accounts — a reset for a deleted user is meaningless,
// and looking up a deleted row would leak its existence to a
// crafted-input attacker if we ever surfaced the result.
func (s *PgxUserStore) LookupIDByEmail(ctx context.Context, email string) (string, error) {
	email = strings.TrimSpace(email)
	if email == "" {
		return "", ErrUserNotFound
	}
	var userID string
	err := s.pool.QueryRow(ctx,
		`SELECT id::text FROM users
		 WHERE email = $1::citext AND status <> 'deleted'
		 LIMIT 1`,
		email,
	).Scan(&userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrUserNotFound
		}
		return "", fmt.Errorf("passwordreset: lookup user: %w", err)
	}
	return userID, nil
}

// UpdatePassword rewrites user_passwords.password_hash for userID.
// last_changed_at is bumped to now() in the same statement so the
// audit timeline reflects the rotation. params_version is left alone
// — the caller hands us a hash already produced under the current
// default params.
//
// We use UPSERT semantics (INSERT ... ON CONFLICT) so an OAuth-only
// user who completes a password reset gets a brand-new user_passwords
// row rather than failing the UPDATE. The flow does not currently
// allow self-service password creation, but the ON CONFLICT keeps the
// reset surface usable for that future variant without a second
// migration.
func (s *PgxUserStore) UpdatePassword(ctx context.Context, userID, newHash string) error {
	if userID == "" {
		return ErrUserNotFound
	}
	if newHash == "" {
		return errors.New("passwordreset: newHash is required")
	}
	tag, err := s.pool.Exec(ctx,
		`INSERT INTO user_passwords (user_id, password_hash, last_changed_at)
		 VALUES ($1::uuid, $2, now())
		 ON CONFLICT (user_id) DO UPDATE
		 SET password_hash = EXCLUDED.password_hash,
		     last_changed_at = now()`,
		userID, newHash,
	)
	if err != nil {
		return fmt.Errorf("passwordreset: update password: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return nil
}
