package magiclink

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DefaultTTL is the time-to-live of a single magic-link token. 15
// minutes is tighter than the password-reset TTL (1h) because the
// magic link itself IS the credential — once consumed it mints a real
// session, so the window of abuse must be short.
const DefaultTTL = 15 * time.Minute

// Errors returned by the [TokenStore] and [UserStore] implementations.
var (
	// ErrTokenNotFound is returned by [TokenStore.Consume] when the
	// supplied hash is absent, expired, or already redeemed. The
	// handler treats all three cases identically — the wire response
	// is "invalid_or_expired_token" in every case so an attacker
	// cannot tell which path was taken.
	ErrTokenNotFound = errors.New("magiclink: token not found")

	// ErrUserNotFound is returned by [UserStore] lookups when the
	// supplied identifier (email or user_id) resolves to no row.
	ErrUserNotFound = errors.New("magiclink: user not found")
)

// TokenStore is the persistence seam for magic-link tokens. The
// production implementation is [PgxTokenStore]; tests typically pass
// an in-memory fake.
type TokenStore interface {
	// Save persists tokenHash for userID with the supplied expiry.
	// Implementations write a single row to magic_link_tokens.
	Save(ctx context.Context, tokenHash, userID string, expiresAt time.Time) error

	// Consume looks up tokenHash and atomically marks it used. Returns
	// the user_id whose sign-in is being completed, or [ErrTokenNotFound]
	// when the token is unknown, expired, or already redeemed.
	//
	// The atomicity matters: two parallel verify calls with the same
	// token must result in exactly one success. The implementation
	// uses an UPDATE ... WHERE used_at IS NULL ... RETURNING dance to
	// get that guarantee in a single round-trip.
	Consume(ctx context.Context, tokenHash string, now time.Time) (string, error)
}

// UserStore is the persistence seam for the user-side operations the
// flow needs. The request handler resolves an email to a user_id;
// the verify handler resolves a user_id to an email (used in the audit
// trail, not the wire response).
type UserStore interface {
	// LookupIDByEmail returns the user_id whose email matches.
	// Case-insensitive (users.email is citext). Returns ErrUserNotFound
	// when no row matches or the user is soft-deleted.
	LookupIDByEmail(ctx context.Context, email string) (userID string, err error)
}

// SessionCreator is the narrow subset of [session.Manager] the verify
// handler uses. Stated as an interface so tests don't need to spin up
// a Redis-backed manager.
type SessionCreator interface {
	Create(ctx context.Context, userID string, data map[string]any, ttl, idleTTL time.Duration) (string, error)
}

// PgxTokenStore is the production [TokenStore] backed by pgxpool.
type PgxTokenStore struct {
	pool *pgxpool.Pool
}

// NewPgxTokenStore returns a TokenStore over pool. Returns an error if
// pool is nil so a wiring mistake fails fast at boot.
func NewPgxTokenStore(pool *pgxpool.Pool) (*PgxTokenStore, error) {
	if pool == nil {
		return nil, errors.New("magiclink: pgx pool is nil")
	}
	return &PgxTokenStore{pool: pool}, nil
}

// Save inserts a new row into magic_link_tokens. The user_id and
// expiresAt are written as-is; the caller is responsible for computing
// expiresAt from a deterministic clock so tests can pin the value.
func (s *PgxTokenStore) Save(ctx context.Context, tokenHash, userID string, expiresAt time.Time) error {
	if tokenHash == "" {
		return errors.New("magiclink: tokenHash is required")
	}
	if userID == "" {
		return errors.New("magiclink: userID is required")
	}
	if expiresAt.IsZero() {
		return errors.New("magiclink: expiresAt is required")
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO magic_link_tokens (user_id, token_hash, expires_at)
		 VALUES ($1::uuid, $2, $3)`,
		userID, tokenHash, expiresAt,
	)
	if err != nil {
		return fmt.Errorf("magiclink: insert token: %w", err)
	}
	return nil
}

// Consume marks the row identified by tokenHash as used and returns
// the user_id. The UPDATE ... RETURNING in a single statement
// guarantees at-most-once consumption even under concurrent verify
// calls — the WHERE clause filters on `used_at IS NULL AND expires_at
// > now` so the second caller falls through to "no rows".
func (s *PgxTokenStore) Consume(ctx context.Context, tokenHash string, now time.Time) (string, error) {
	if tokenHash == "" {
		return "", ErrTokenNotFound
	}
	var userID string
	err := s.pool.QueryRow(ctx,
		`UPDATE magic_link_tokens
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
		return "", fmt.Errorf("magiclink: consume token: %w", err)
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
		return nil, errors.New("magiclink: pgx pool is nil")
	}
	return &PgxUserStore{pool: pool}, nil
}

// LookupIDByEmail returns the user_id whose email matches. Case-
// insensitive (users.email is citext). The status filter excludes
// soft-deleted accounts — a magic link for a deleted user is
// meaningless, and looking up a deleted row would leak its existence
// to a crafted-input attacker if we ever surfaced the result.
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
		return "", fmt.Errorf("magiclink: lookup user: %w", err)
	}
	return userID, nil
}
