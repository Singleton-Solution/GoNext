package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisLimiter is a Redis-backed token-bucket limiter that runs its
// state machine atomically on the server via a Lua script. Use this in
// multi-instance production where counters must be shared across
// replicas.
//
// The bucket state is two HASH fields per key (tokens + last_tick),
// both stored under "{Prefix}:{key}". Atomicity is guaranteed by
// EVAL; the script returns the allow/retry decision so a single round-
// trip is enough per request.
//
// Why the token bucket and not a sliding-window INCR? A sliding window
// is fine for ceiling enforcement but doesn't model burst capacity well
// (the first request after a quiet minute carries the same cost as the
// 100th request in a busy minute). Token buckets model burst correctly
// and are the canonical answer for login rate limiting.
type RedisLimiter struct {
	rdb    redisClient
	policy Policy

	// now is the time source used both client-side (for retryAfter) and
	// passed to the Lua script as the wall clock. Tests inject a fixed
	// clock; production uses time.Now.
	now func() time.Time
}

// redisClient is the subset of go-redis we depend on. Defining an
// interface lets tests swap in a fake without spinning up a real Redis.
type redisClient interface {
	Eval(ctx context.Context, script string, keys []string, args ...any) *redis.Cmd
	Del(ctx context.Context, keys ...string) *redis.IntCmd
}

// tokenBucketScript is the Lua program executed atomically on the
// Redis server. ARGV layout:
//
//	ARGV[1] = capacity        (int)
//	ARGV[2] = refill_rate     (float, tokens/sec)
//	ARGV[3] = now_ms          (int64 milliseconds since epoch)
//	ARGV[4] = ttl_ms          (int, key TTL refresh in ms)
//
// Returns a two-element table: {allowed (1/0), retry_after_ms (int)}.
//
// Algorithm: read prior {tokens, last_ms}; if missing, start with a
// full bucket; refill by (now-last)*rate; cap at capacity; if tokens>=1
// decrement and allow; else compute the time until one token accrues
// and deny. PEXPIRE refreshes the TTL so idle buckets are reaped
// server-side.
const tokenBucketScript = `
local key = KEYS[1]
local capacity = tonumber(ARGV[1])
local rate = tonumber(ARGV[2])
local now_ms = tonumber(ARGV[3])
local ttl_ms = tonumber(ARGV[4])

local data = redis.call('HMGET', key, 'tokens', 'last_ms')
local tokens
local last_ms
if data[1] == false or data[1] == nil then
  tokens = capacity
  last_ms = now_ms
else
  tokens = tonumber(data[1])
  last_ms = tonumber(data[2])
end

local elapsed_ms = now_ms - last_ms
if elapsed_ms < 0 then elapsed_ms = 0 end
tokens = tokens + (elapsed_ms / 1000.0) * rate
if tokens > capacity then tokens = capacity end

local allowed = 0
local retry_after_ms = 0
if tokens >= 1 then
  tokens = tokens - 1
  allowed = 1
else
  local missing = 1.0 - tokens
  retry_after_ms = math.ceil((missing / rate) * 1000.0)
end

redis.call('HMSET', key, 'tokens', tokens, 'last_ms', now_ms)
redis.call('PEXPIRE', key, ttl_ms)

return {allowed, retry_after_ms}
`

// NewRedisLimiter constructs a RedisLimiter. The redis.Client must be
// connected; we don't ping here to avoid blocking boot if Redis is
// briefly unavailable (the limiter will surface backend errors per
// Allow call, where the middleware can decide to fail open).
func NewRedisLimiter(rdb *redis.Client, p Policy) (*RedisLimiter, error) {
	if rdb == nil {
		return nil, fmt.Errorf("ratelimit.NewRedisLimiter: client is nil")
	}
	if err := p.validate(); err != nil {
		return nil, fmt.Errorf("ratelimit.NewRedisLimiter: %w", err)
	}
	if p.Prefix == "" {
		return nil, fmt.Errorf("ratelimit.NewRedisLimiter: Prefix is required for Redis backend")
	}
	return &RedisLimiter{rdb: rdb, policy: p, now: time.Now}, nil
}

// newRedisLimiterWithClient is the internal constructor used by tests
// to inject a fake redisClient. It bypasses the *redis.Client type so
// we don't need a real Redis server in unit tests.
func newRedisLimiterWithClient(c redisClient, p Policy) (*RedisLimiter, error) {
	if c == nil {
		return nil, fmt.Errorf("ratelimit.newRedisLimiterWithClient: client is nil")
	}
	if err := p.validate(); err != nil {
		return nil, fmt.Errorf("ratelimit.newRedisLimiterWithClient: %w", err)
	}
	if p.Prefix == "" {
		return nil, fmt.Errorf("ratelimit.newRedisLimiterWithClient: Prefix is required")
	}
	return &RedisLimiter{rdb: c, policy: p, now: time.Now}, nil
}

// Allow consumes one token. See Limiter for the contract.
//
// The Redis round-trip uses ctx for cancellation; if Redis is
// unreachable, the error is returned and the caller decides whether
// to fail open or closed.
func (l *RedisLimiter) Allow(ctx context.Context, key string) (bool, time.Duration, error) {
	fullKey := l.policy.Prefix + ":" + key

	// TTL = at least 2x the time to fully refill from empty. An idle
	// bucket eventually expires, freeing the key; a busy bucket gets
	// its TTL refreshed on every Allow so it never expires under load.
	refillMS := int64(float64(l.policy.Capacity) / l.policy.RefillRate * 1000.0)
	ttlMS := refillMS * 2
	if ttlMS < 60_000 {
		ttlMS = 60_000 // floor: always keep at least 1 minute so debug `KEYS` is useful
	}

	nowMS := l.now().UnixMilli()

	res, err := l.rdb.Eval(
		ctx, tokenBucketScript, []string{fullKey},
		l.policy.Capacity, l.policy.RefillRate, nowMS, ttlMS,
	).Result()
	if err != nil {
		return false, 0, fmt.Errorf("ratelimit.RedisLimiter.Allow: %w", err)
	}

	arr, ok := res.([]any)
	if !ok || len(arr) != 2 {
		return false, 0, fmt.Errorf("ratelimit.RedisLimiter.Allow: unexpected reply shape %T", res)
	}

	allowed, ok := arr[0].(int64)
	if !ok {
		return false, 0, fmt.Errorf("ratelimit.RedisLimiter.Allow: allowed field is %T", arr[0])
	}
	retryMS, ok := arr[1].(int64)
	if !ok {
		return false, 0, fmt.Errorf("ratelimit.RedisLimiter.Allow: retry field is %T", arr[1])
	}

	if allowed == 1 {
		return true, 0, nil
	}
	return false, time.Duration(retryMS) * time.Millisecond, nil
}

// Reset clears the bucket for key. Used by LoginAttemptLimiter after a
// successful login.
func (l *RedisLimiter) Reset(ctx context.Context, key string) error {
	fullKey := l.policy.Prefix + ":" + key
	if err := l.rdb.Del(ctx, fullKey).Err(); err != nil {
		return fmt.Errorf("ratelimit.RedisLimiter.Reset: %w", err)
	}
	return nil
}
