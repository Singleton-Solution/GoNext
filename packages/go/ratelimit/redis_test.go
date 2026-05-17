package ratelimit

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// fakeRedis implements the redisClient interface in process so we can
// unit-test the RedisLimiter contract without booting a real Redis. It
// simulates the same algorithm the Lua script performs server-side.
type fakeRedis struct {
	rows map[string]fakeRow

	// failEval and failDel let tests force backend errors.
	failEval error
	failDel  error
}

type fakeRow struct {
	tokens float64
	lastMS int64
}

func newFakeRedis() *fakeRedis { return &fakeRedis{rows: make(map[string]fakeRow)} }

func (f *fakeRedis) Eval(ctx context.Context, _ string, keys []string, args ...any) *redis.Cmd {
	cmd := redis.NewCmd(ctx)
	if f.failEval != nil {
		cmd.SetErr(f.failEval)
		return cmd
	}
	if len(keys) != 1 || len(args) != 4 {
		cmd.SetErr(errors.New("fakeRedis.Eval: bad arity"))
		return cmd
	}

	key := keys[0]
	capacity := toInt(args[0])
	rate := toFloat(args[1])
	nowMS := toInt64(args[2])
	// TTL unused in fake — we never expire.

	row, ok := f.rows[key]
	if !ok {
		row = fakeRow{tokens: float64(capacity), lastMS: nowMS}
	}
	elapsed := nowMS - row.lastMS
	if elapsed < 0 {
		elapsed = 0
	}
	row.tokens += (float64(elapsed) / 1000.0) * rate
	if row.tokens > float64(capacity) {
		row.tokens = float64(capacity)
	}

	var allowed, retryMS int64
	if row.tokens >= 1 {
		row.tokens -= 1
		allowed = 1
	} else {
		missing := 1.0 - row.tokens
		retryMS = int64((missing / rate) * 1000.0)
		if retryMS < 1 {
			retryMS = 1
		}
	}
	row.lastMS = nowMS
	f.rows[key] = row

	cmd.SetVal([]any{allowed, retryMS})
	return cmd
}

func (f *fakeRedis) Del(ctx context.Context, keys ...string) *redis.IntCmd {
	cmd := redis.NewIntCmd(ctx)
	if f.failDel != nil {
		cmd.SetErr(f.failDel)
		return cmd
	}
	deleted := int64(0)
	for _, k := range keys {
		if _, ok := f.rows[k]; ok {
			delete(f.rows, k)
			deleted++
		}
	}
	cmd.SetVal(deleted)
	return cmd
}

func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}

func toInt64(v any) int64 {
	switch n := v.(type) {
	case int:
		return int64(n)
	case int64:
		return n
	case float64:
		return int64(n)
	}
	return 0
}

func toFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	}
	return 0
}

// TestRedis_FakeBurst exercises the same burst/deny cycle as the
// memory limiter, using the in-process fake. This proves the RedisLimiter
// glue (reply parsing, error wrapping) is correct without external deps.
func TestRedis_FakeBurst(t *testing.T) {
	clock := newFakeClock(time.Unix(1_700_600_000, 0))
	fake := newFakeRedis()
	l, err := newRedisLimiterWithClient(fake, Policy{Capacity: 3, RefillRate: 1, Prefix: "test"})
	if err != nil {
		t.Fatal(err)
	}
	l.now = clock.Now

	for i := 0; i < 3; i++ {
		ok, _, err := l.Allow(context.Background(), "k")
		if err != nil || !ok {
			t.Fatalf("Allow %d: ok=%v err=%v", i, ok, err)
		}
	}
	ok, retry, err := l.Allow(context.Background(), "k")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected denial")
	}
	if retry <= 0 {
		t.Errorf("retryAfter should be > 0, got %v", retry)
	}

	clock.Advance(2 * time.Second)
	if ok, _, _ := l.Allow(context.Background(), "k"); !ok {
		t.Fatal("expected allowed after 2s refill")
	}
}

// TestRedis_ErrorPropagation verifies backend errors surface to the
// caller with %w wrapping (so middleware can fail open after logging).
func TestRedis_ErrorPropagation(t *testing.T) {
	sentinel := errors.New("connection refused")
	fake := &fakeRedis{rows: map[string]fakeRow{}, failEval: sentinel}
	l, err := newRedisLimiterWithClient(fake, Policy{Capacity: 1, RefillRate: 1, Prefix: "test"})
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = l.Allow(context.Background(), "k")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("error chain missing sentinel: %v", err)
	}
}

// TestRedis_ResetDeletes proves Reset issues DEL and clears the bucket.
func TestRedis_ResetDeletes(t *testing.T) {
	fake := newFakeRedis()
	l, _ := newRedisLimiterWithClient(fake, Policy{Capacity: 1, RefillRate: 0.01, Prefix: "p"})

	// Touch then exhaust.
	_, _, _ = l.Allow(context.Background(), "k")
	if ok, _, _ := l.Allow(context.Background(), "k"); ok {
		t.Fatal("expected denial after burst")
	}

	if err := l.Reset(context.Background(), "k"); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if _, ok := fake.rows["p:k"]; ok {
		t.Error("Reset should have deleted the row")
	}
}

// TestRedis_ConstructorValidation covers nil-client and bad-policy paths.
func TestRedis_ConstructorValidation(t *testing.T) {
	if _, err := NewRedisLimiter(nil, Policy{Capacity: 1, RefillRate: 1, Prefix: "p"}); err == nil {
		t.Error("expected error for nil client")
	}

	rdb := &redis.Client{}
	if _, err := NewRedisLimiter(rdb, Policy{Capacity: 0, RefillRate: 1, Prefix: "p"}); err == nil {
		t.Error("expected error for bad capacity")
	}
	if _, err := NewRedisLimiter(rdb, Policy{Capacity: 1, RefillRate: 1, Prefix: ""}); err == nil {
		t.Error("expected error for missing prefix")
	}
}

// TestRedis_Integration runs against a real Redis when REDIS_URL is set.
// Skipped silently otherwise so unit-only test runs stay green.
func TestRedis_Integration(t *testing.T) {
	url := os.Getenv("REDIS_URL")
	if url == "" {
		t.Skip("REDIS_URL not set; skipping Redis integration test")
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatalf("REDIS_URL: %v", err)
	}
	client := redis.NewClient(opts)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("Redis unreachable: %v", err)
	}

	prefix := "ratelimit-it"
	defer func() {
		// Best-effort cleanup of test keys.
		c, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		iter := client.Scan(c, 0, prefix+":*", 100).Iterator()
		var keys []string
		for iter.Next(c) {
			keys = append(keys, iter.Val())
		}
		if len(keys) > 0 {
			client.Del(c, keys...)
		}
	}()

	l, err := NewRedisLimiter(client, Policy{Capacity: 3, RefillRate: 5, Prefix: prefix})
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		ok, _, err := l.Allow(context.Background(), "burst")
		if err != nil {
			t.Fatalf("Allow %d: %v", i, err)
		}
		if !ok {
			t.Fatalf("Allow %d denied", i)
		}
	}

	ok, retry, err := l.Allow(context.Background(), "burst")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("4th call should be denied")
	}
	if retry <= 0 || retry > time.Second {
		t.Errorf("retryAfter outside expected band: %v", retry)
	}

	// Wait for refill.
	time.Sleep(retry + 20*time.Millisecond)
	if ok, _, err := l.Allow(context.Background(), "burst"); err != nil || !ok {
		t.Fatalf("post-refill: ok=%v err=%v", ok, err)
	}
}
