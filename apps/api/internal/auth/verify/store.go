package verify

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// keyPrefix is the namespace under which we park verification tokens
// in Redis. The full key shape is "email_verify:{sha256_hex(token)}".
const keyPrefix = "email_verify:"

// DefaultTTL is the lifetime of a single verification token. 24 hours
// matches the OWASP recommendation for "email confirmation" links —
// long enough that users can act on the email at their leisure
// (next-day reply still works), short enough that a leaked archived
// email isn't a perpetual takeover primitive.
const DefaultTTL = 24 * time.Hour

// ErrTokenNotFound is returned by [TokenStore.Lookup] when the token
// is unknown or has expired. Callers should treat the two cases
// identically — both are "the user does not currently hold a valid
// verification claim".
var ErrTokenNotFound = errors.New("verify: token not found")

// TokenStore is the slice of behavior the HTTP handlers need from the
// underlying Redis client. Defining it as an interface lets unit
// tests pass a fake without standing up a real Redis.
type TokenStore interface {
	// Save associates tokenHash with userID, expiring after ttl.
	// Implementations MUST set both the value and the TTL atomically
	// (a single SET with PX/EX) so a process crash between the SET
	// and an EXPIRE cannot leak a never-expiring row.
	Save(ctx context.Context, tokenHash, userID string, ttl time.Duration) error

	// Lookup returns the userID associated with tokenHash, or
	// [ErrTokenNotFound] when the key is gone. Lookup does NOT delete
	// the key — the caller calls [Consume] after a successful UPDATE
	// so the audit emission and the deletion are in the same path.
	Lookup(ctx context.Context, tokenHash string) (string, error)

	// Consume deletes the token in a single round-trip. Idempotent —
	// deleting an unknown or already-expired token is not an error.
	Consume(ctx context.Context, tokenHash string) error
}

// RedisTokenStore is the production [TokenStore] backed by a
// go-redis client. The struct holds nothing per-request, so one
// instance per process is the expected pattern.
type RedisTokenStore struct {
	rdb *redis.Client
}

// NewRedisTokenStore wraps rdb. Returns an error if rdb is nil so
// boot fails fast on a wiring mistake.
func NewRedisTokenStore(rdb *redis.Client) (*RedisTokenStore, error) {
	if rdb == nil {
		return nil, errors.New("verify: redis client is nil")
	}
	return &RedisTokenStore{rdb: rdb}, nil
}

// Save persists the (tokenHash, userID) pair with ttl. The value is
// the raw user_id string — the audit/users tables drive everything
// else, so storing nothing else minimizes the surface for a Redis
// peek-and-replay.
func (s *RedisTokenStore) Save(ctx context.Context, tokenHash, userID string, ttl time.Duration) error {
	if tokenHash == "" {
		return errors.New("verify: tokenHash is required")
	}
	if userID == "" {
		return errors.New("verify: userID is required")
	}
	if ttl <= 0 {
		return errors.New("verify: ttl must be positive")
	}
	if err := s.rdb.Set(ctx, keyPrefix+tokenHash, userID, ttl).Err(); err != nil {
		return fmt.Errorf("verify: save: %w", err)
	}
	return nil
}

// Lookup returns the userID for tokenHash. ErrTokenNotFound is
// returned when the key is absent OR expired.
func (s *RedisTokenStore) Lookup(ctx context.Context, tokenHash string) (string, error) {
	if tokenHash == "" {
		return "", ErrTokenNotFound
	}
	val, err := s.rdb.Get(ctx, keyPrefix+tokenHash).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return "", ErrTokenNotFound
		}
		return "", fmt.Errorf("verify: lookup: %w", err)
	}
	return val, nil
}

// Consume deletes the token. Idempotent.
func (s *RedisTokenStore) Consume(ctx context.Context, tokenHash string) error {
	if tokenHash == "" {
		return nil
	}
	if err := s.rdb.Del(ctx, keyPrefix+tokenHash).Err(); err != nil {
		return fmt.Errorf("verify: consume: %w", err)
	}
	return nil
}
