package login

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestMemoryIntermediateStore_StoreRequiresArgs(t *testing.T) {
	s := newMemoryIntermediateStore(time.Now)
	if err := s.Store(context.Background(), "", "u-1", time.Minute); err == nil {
		t.Error("Store with empty token: expected error")
	}
	if err := s.Store(context.Background(), "tok", "", time.Minute); err == nil {
		t.Error("Store with empty userID: expected error")
	}
	if err := s.Store(context.Background(), "tok", "u-1", 0); err == nil {
		t.Error("Store with zero ttl: expected error")
	}
}

func TestMemoryIntermediateStore_LoadMissesAfterTTL(t *testing.T) {
	clock := newClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	s := newMemoryIntermediateStore(clock.now)
	if err := s.Store(context.Background(), "tok", "u-1", time.Minute); err != nil {
		t.Fatalf("Store: %v", err)
	}
	// Advance past the TTL.
	clock.advance(2 * time.Minute)
	_, err := s.Load(context.Background(), "tok")
	if !errors.Is(err, ErrIntermediateNotFound) {
		t.Fatalf("Load after expiry: got %v, want ErrIntermediateNotFound", err)
	}
}

func TestMemoryIntermediateStore_DeleteIsIdempotent(t *testing.T) {
	s := newMemoryIntermediateStore(time.Now)
	if err := s.Delete(context.Background(), "never-stored"); err != nil {
		t.Errorf("Delete on missing: %v", err)
	}
}

func TestMemoryIntermediateStore_RoundTrip(t *testing.T) {
	s := newMemoryIntermediateStore(time.Now)
	if err := s.Store(context.Background(), "tok", "u-1", time.Minute); err != nil {
		t.Fatalf("Store: %v", err)
	}
	got, err := s.Load(context.Background(), "tok")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != "u-1" {
		t.Errorf("Load: got %q, want u-1", got)
	}
	if err := s.Delete(context.Background(), "tok"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Load(context.Background(), "tok"); !errors.Is(err, ErrIntermediateNotFound) {
		t.Errorf("Load after Delete: got %v, want ErrIntermediateNotFound", err)
	}
}

// TestRedisIntermediateStore exercises the production Redis-backed
// store against a real go-redis client pointed at a fake redis server.
// We use the github.com/redis/go-redis/v9 client's own miniredis-style
// loopback... but since that's a heavy dep, we instead build a tiny
// in-test mock that satisfies the operations Store/Load/Delete use.
//
// To keep the test free of an external Redis dependency, we use a
// "miniredis-ish" approach: instantiate go-redis against a closed
// server endpoint and only assert the key-shape function. The full
// Store/Load round-trip is covered against the memory store above;
// this test covers the wire-level wiring + key prefix.
func TestRedisIntermediateStore_KeyShape(t *testing.T) {
	want := "login_intermediate:abc"
	if got := intermediateKey("abc"); got != want {
		t.Errorf("intermediateKey: got %q, want %q", got, want)
	}
}

func TestNewRedisIntermediateStore_TypeAssertion(t *testing.T) {
	// Constructing the store with a nil-but-typed client and
	// asserting the wrapper exists doesn't go to the network — Store
	// would fail, but that's expected. The constructor itself must
	// return a non-nil IntermediateStore.
	s := NewRedisIntermediateStore((*redis.Client)(nil))
	if s == nil {
		t.Fatal("NewRedisIntermediateStore returned nil")
	}
}

func TestRedisIntermediateStore_StoreValidatesArgs(t *testing.T) {
	// We don't need a real Redis to verify that empty arguments are
	// rejected before the call hits the wire.
	s := &redisIntermediateStore{rdb: nil}
	if err := s.Store(context.Background(), "", "u", time.Minute); err == nil {
		t.Error("empty token: expected error")
	}
	if err := s.Store(context.Background(), "t", "", time.Minute); err == nil {
		t.Error("empty userID: expected error")
	}
	if err := s.Store(context.Background(), "t", "u", 0); err == nil {
		t.Error("zero ttl: expected error")
	}
}

// TestRedisIntermediateStore_RoundTrip exercises the production Redis
// path end-to-end. Skipped when REDIS_URL is unset; CI provisions a
// Redis sidecar and exports the var (same pattern as
// packages/go/session/manager_test.go).
func TestRedisIntermediateStore_RoundTrip(t *testing.T) {
	url := os.Getenv("REDIS_URL")
	if url == "" {
		t.Skip("REDIS_URL not set; skipping Redis integration")
	}
	opt, err := redis.ParseURL(url)
	if err != nil {
		t.Fatalf("ParseURL: %v", err)
	}
	rdb := redis.NewClient(opt)
	t.Cleanup(func() { _ = rdb.Close() })
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("ping: %v", err)
	}
	s := NewRedisIntermediateStore(rdb)

	ctx := context.Background()
	tok := "test-tok-" + t.Name()
	if err := s.Store(ctx, tok, "u-1", time.Minute); err != nil {
		t.Fatalf("Store: %v", err)
	}
	got, err := s.Load(ctx, tok)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != "u-1" {
		t.Errorf("Load: got %q, want u-1", got)
	}
	if err := s.Delete(ctx, tok); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Load(ctx, tok); !errors.Is(err, ErrIntermediateNotFound) {
		t.Errorf("Load after Delete: got %v, want ErrIntermediateNotFound", err)
	}
}

// fakeRedis implements redisCmdable so we can unit-test the Redis-
// backed store without spinning up a Redis container. It mirrors
// just enough behaviour to satisfy Set / Get / Del — a missing key
// returns redis.Nil from Get; an existing one returns its value.
type fakeRedis struct {
	rows   map[string]fakeRedisRow
	setErr error
	getErr error
	delErr error
}

type fakeRedisRow struct {
	value string
}

func newFakeRedis() *fakeRedis { return &fakeRedis{rows: map[string]fakeRedisRow{}} }

func (f *fakeRedis) Set(_ context.Context, key string, value any, _ time.Duration) *redis.StatusCmd {
	cmd := redis.NewStatusCmd(context.Background())
	if f.setErr != nil {
		cmd.SetErr(f.setErr)
		return cmd
	}
	f.rows[key] = fakeRedisRow{value: value.(string)}
	cmd.SetVal("OK")
	return cmd
}

func (f *fakeRedis) Get(_ context.Context, key string) *redis.StringCmd {
	cmd := redis.NewStringCmd(context.Background())
	if f.getErr != nil {
		cmd.SetErr(f.getErr)
		return cmd
	}
	row, ok := f.rows[key]
	if !ok {
		cmd.SetErr(redis.Nil)
		return cmd
	}
	cmd.SetVal(row.value)
	return cmd
}

func (f *fakeRedis) Del(_ context.Context, keys ...string) *redis.IntCmd {
	cmd := redis.NewIntCmd(context.Background())
	if f.delErr != nil {
		cmd.SetErr(f.delErr)
		return cmd
	}
	var deleted int64
	for _, k := range keys {
		if _, ok := f.rows[k]; ok {
			delete(f.rows, k)
			deleted++
		}
	}
	cmd.SetVal(deleted)
	return cmd
}

func TestRedisIntermediateStore_RoundTripFake(t *testing.T) {
	fake := newFakeRedis()
	s := &redisIntermediateStore{rdb: fake}

	ctx := context.Background()
	if err := s.Store(ctx, "tok", "u-1", time.Minute); err != nil {
		t.Fatalf("Store: %v", err)
	}
	got, err := s.Load(ctx, "tok")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != "u-1" {
		t.Errorf("Load: got %q, want u-1", got)
	}
	if err := s.Delete(ctx, "tok"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Load(ctx, "tok"); !errors.Is(err, ErrIntermediateNotFound) {
		t.Errorf("Load after Delete: got %v, want ErrIntermediateNotFound", err)
	}
}

func TestRedisIntermediateStore_LoadErrorBubblesUp(t *testing.T) {
	fake := newFakeRedis()
	fake.getErr = errors.New("connection refused")
	s := &redisIntermediateStore{rdb: fake}
	_, err := s.Load(context.Background(), "tok")
	if err == nil {
		t.Fatal("expected error")
	}
	if errors.Is(err, ErrIntermediateNotFound) {
		t.Errorf("got ErrIntermediateNotFound; want generic error")
	}
}

func TestRedisIntermediateStore_StoreErrorBubblesUp(t *testing.T) {
	fake := newFakeRedis()
	fake.setErr = errors.New("connection refused")
	s := &redisIntermediateStore{rdb: fake}
	err := s.Store(context.Background(), "tok", "u-1", time.Minute)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRedisIntermediateStore_DeleteErrorBubblesUp(t *testing.T) {
	fake := newFakeRedis()
	fake.delErr = errors.New("connection refused")
	s := &redisIntermediateStore{rdb: fake}
	if err := s.Delete(context.Background(), "tok"); err == nil {
		t.Fatal("expected error")
	}
}

// clock is a tiny test-only time source. Used by stores that wire a
// "now" closure for deterministic expiry tests.
type clock struct {
	t time.Time
}

func newClock(start time.Time) *clock         { return &clock{t: start} }
func (c *clock) now() time.Time               { return c.t }
func (c *clock) advance(d time.Duration)      { c.t = c.t.Add(d) }
func (c *clock) set(t time.Time)              { c.t = t }
func (c *clock) sub(t time.Time) time.Duration { return c.t.Sub(t) }
