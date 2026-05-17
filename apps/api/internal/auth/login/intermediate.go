package login

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// intermediateTokenBytes is the size of the random material backing a
// partial-login token. 32 bytes → 256 bits of entropy → 43 chars of
// base64url. Same shape as session tokens, so the wire format is
// trivially distinguishable from neither but also doesn't add a
// second token grammar to learn.
const intermediateTokenBytes = 32

// generateIntermediateToken returns a fresh base64url-encoded 32-byte
// random token. Errors only on crypto/rand failure.
func generateIntermediateToken() (string, error) {
	var b [intermediateTokenBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("login: generate intermediate token: %w", err)
	}
	// base64url without padding — same alphabet as the session token.
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// ErrIntermediateNotFound is the canonical "miss" returned by every
// IntermediateStore.Load implementation. The service translates it to
// ErrIntermediateExpired before reaching the handler so callers see
// one error from the public API.
var ErrIntermediateNotFound = errors.New("login: intermediate token not found")

// memoryIntermediateStore keeps partial-login tokens in process memory.
// It is intended for tests and single-instance dev — restarts wipe the
// state and replicas don't share it. Production wiring should use the
// Redis-backed implementation.
//
// Concurrency: safe for concurrent Store / Load / Delete via the inner
// mutex. Expired entries are pruned lazily on the next Load that
// stumbles across them; we don't run a background sweeper because the
// total number of partial logins in flight is small (one per
// in-progress 2FA exchange per user) and a stray expired entry costs
// less than a goroutine.
type memoryIntermediateStore struct {
	mu    sync.Mutex
	rows  map[string]memIntermediateRow
	clock func() time.Time
}

type memIntermediateRow struct {
	userID    string
	expiresAt time.Time
}

// NewMemoryIntermediateStore returns an empty in-memory intermediate
// token store. Exposed so tests in this package and (potentially)
// downstream consumers can construct one without reflection.
func NewMemoryIntermediateStore() IntermediateStore {
	return newMemoryIntermediateStore(time.Now)
}

func newMemoryIntermediateStore(now func() time.Time) *memoryIntermediateStore {
	return &memoryIntermediateStore{
		rows:  map[string]memIntermediateRow{},
		clock: now,
	}
}

func (s *memoryIntermediateStore) Store(_ context.Context, token, userID string, ttl time.Duration) error {
	if token == "" || userID == "" {
		return errors.New("login: memoryIntermediateStore.Store: token and userID required")
	}
	if ttl <= 0 {
		return errors.New("login: memoryIntermediateStore.Store: ttl must be > 0")
	}
	s.mu.Lock()
	s.rows[token] = memIntermediateRow{userID: userID, expiresAt: s.clock().Add(ttl)}
	s.mu.Unlock()
	return nil
}

func (s *memoryIntermediateStore) Load(_ context.Context, token string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[token]
	if !ok {
		return "", ErrIntermediateNotFound
	}
	if !s.clock().Before(row.expiresAt) {
		delete(s.rows, token)
		return "", ErrIntermediateNotFound
	}
	return row.userID, nil
}

func (s *memoryIntermediateStore) Delete(_ context.Context, token string) error {
	s.mu.Lock()
	delete(s.rows, token)
	s.mu.Unlock()
	return nil
}

// redisCmdable is the narrow subset of *redis.Client the Redis-backed
// store consumes. Defining it as an interface lets us drop in a fake
// for unit tests without spinning up a Redis container — *redis.Client
// and miniredis-style fakes both satisfy this shape.
type redisCmdable interface {
	Set(ctx context.Context, key string, value any, expiration time.Duration) *redis.StatusCmd
	Get(ctx context.Context, key string) *redis.StringCmd
	Del(ctx context.Context, keys ...string) *redis.IntCmd
}

// redisIntermediateStore persists partial-login tokens in Redis. Keys
// are prefixed with "login_intermediate:" so they don't collide with
// session keys ("session:") or rate-limit buckets. The TTL on the
// Redis key is the source of truth for expiry — no in-Go clock.
type redisIntermediateStore struct {
	rdb redisCmdable
}

// NewRedisIntermediateStore wraps a redis.Client with the
// IntermediateStore interface. The client is shared with the session
// manager and rate limiter; we don't open a new connection.
func NewRedisIntermediateStore(rdb *redis.Client) IntermediateStore {
	return &redisIntermediateStore{rdb: rdb}
}

// intermediateKey is the Redis key shape. The format mirrors
// session:{token} for consistency with the session package.
func intermediateKey(token string) string {
	return "login_intermediate:" + token
}

func (s *redisIntermediateStore) Store(ctx context.Context, token, userID string, ttl time.Duration) error {
	if token == "" || userID == "" {
		return errors.New("login: redisIntermediateStore.Store: token and userID required")
	}
	if ttl <= 0 {
		return errors.New("login: redisIntermediateStore.Store: ttl must be > 0")
	}
	if err := s.rdb.Set(ctx, intermediateKey(token), userID, ttl).Err(); err != nil {
		return fmt.Errorf("login: persist intermediate: %w", err)
	}
	return nil
}

func (s *redisIntermediateStore) Load(ctx context.Context, token string) (string, error) {
	v, err := s.rdb.Get(ctx, intermediateKey(token)).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return "", ErrIntermediateNotFound
		}
		return "", fmt.Errorf("login: load intermediate: %w", err)
	}
	return v, nil
}

func (s *redisIntermediateStore) Delete(ctx context.Context, token string) error {
	if err := s.rdb.Del(ctx, intermediateKey(token)).Err(); err != nil {
		return fmt.Errorf("login: delete intermediate: %w", err)
	}
	return nil
}
