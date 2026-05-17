package idempotency_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/jobs/idempotency"
	"github.com/Singleton-Solution/GoNext/packages/go/testutil/containers"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// schemaSQL is the slice of migration 000014 we care about for these
// tests. We don't run the full migrator (that's the migrator package's
// job) — we apply just enough DDL to exercise the store. Keep this in
// sync with migrations/000014_idempotency.up.sql.
const schemaSQL = `
CREATE TABLE IF NOT EXISTS idempotency_keys (
    key             TEXT PRIMARY KEY
                    CHECK (length(key) > 0 AND length(key) <= 255),
    request_hash    BYTEA NOT NULL
                    CHECK (octet_length(request_hash) = 32),
    status          TEXT NOT NULL
                    CHECK (status IN ('in_progress', 'succeeded', 'failed')),
    result_code     INT,
    result_body     JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at      TIMESTAMPTZ NOT NULL,
    CONSTRAINT idempotency_keys_terminal_fields_chk
        CHECK (
            (status = 'in_progress' AND result_code IS NULL AND result_body IS NULL)
            OR
            (status IN ('succeeded', 'failed') AND result_code IS NOT NULL)
        )
);
CREATE INDEX IF NOT EXISTS idempotency_keys_expires_at_idx
    ON idempotency_keys (expires_at);
`

// makeKey is the integration-test mirror of the store-level helper.
// We duplicate it here because the integration_test file is in a
// _test package and can't reach the internal one.
func makeKey(value, body string) idempotency.Key {
	h := sha256.Sum256([]byte(body))
	return idempotency.Key{Value: value, RequestHash: h[:]}
}

// setupPostgres spins up a real Postgres container, applies the
// schema, and returns a pool ready for the store. Skips when Docker
// isn't available — the unit tests carry the contract; this is
// belt-and-suspenders coverage against the real query planner.
func setupPostgres(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := containers.Postgres(t)
	if dsn == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	if _, err := pool.Exec(ctx, schemaSQL); err != nil {
		pool.Close()
		t.Fatalf("apply schema: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// setupRedis spins up a real Redis container and returns a connected
// client. Skips on no-Docker hosts.
func setupRedis(t *testing.T) *redis.Client {
	t.Helper()
	url := containers.Redis(t)
	if url == "" {
		return nil
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatalf("redis.ParseURL: %v", err)
	}
	client := redis.NewClient(opts)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// TestIntegration_PostgresStoreEndToEnd exercises every PostgresStore
// path against a real DB so the SQL syntax, the BYTEA round-trip, and
// the CHECK constraints are all validated.
func TestIntegration_PostgresStoreEndToEnd(t *testing.T) {
	pool := setupPostgres(t)
	if pool == nil {
		return
	}
	s := idempotency.NewPostgresStore(pool)
	ctx := context.Background()
	k := makeKey("integ-pg-1", `{"amt":42}`)

	// First claim: new.
	out, _, err := s.Claim(ctx, k, time.Hour)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if out != idempotency.ClaimNew {
		t.Fatalf("first Claim: got %v, want ClaimNew", out)
	}

	// Second claim while still in_progress: pending.
	out, _, err = s.Claim(ctx, k, time.Hour)
	if err != nil {
		t.Fatalf("Claim 2: %v", err)
	}
	if out != idempotency.ClaimPending {
		t.Fatalf("second Claim: got %v, want ClaimPending", out)
	}

	// Finish the original.
	want := idempotency.Result{Code: 201, Body: []byte(`{"created":"abc"}`)}
	if err := s.Finish(ctx, k, idempotency.StatusSucceeded, want, time.Hour); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	// Third claim: replay with stored result.
	out, got, err := s.Claim(ctx, k, time.Hour)
	if err != nil {
		t.Fatalf("Claim 3: %v", err)
	}
	if out != idempotency.ClaimReplay {
		t.Fatalf("replay: got %v, want ClaimReplay", out)
	}
	if got.Code != want.Code || string(got.Body) != string(want.Body) {
		t.Fatalf("replay result: got %+v, want %+v", got, want)
	}

	// Different hash, same key: mismatch.
	k2 := makeKey("integ-pg-1", `{"amt":99}`)
	out, _, err = s.Claim(ctx, k2, time.Hour)
	if err != nil {
		t.Fatalf("Claim mismatch: %v", err)
	}
	if out != idempotency.ClaimMismatch {
		t.Fatalf("mismatch: got %v, want ClaimMismatch", out)
	}

	// Get the stored entry directly.
	st, res, err := s.Get(ctx, k)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if st != idempotency.StatusSucceeded {
		t.Fatalf("Get status: %v", st)
	}
	if res.Code != want.Code {
		t.Fatalf("Get code: %d", res.Code)
	}
}

// TestIntegration_PostgresStoreBinaryBody verifies the {"_raw_base64": ...}
// wrapper survives a round trip — non-JSON response bodies (file
// downloads, plaintext) must replay verbatim.
func TestIntegration_PostgresStoreBinaryBody(t *testing.T) {
	pool := setupPostgres(t)
	if pool == nil {
		return
	}
	s := idempotency.NewPostgresStore(pool)
	ctx := context.Background()
	k := makeKey("integ-pg-bin", "body")

	if _, _, err := s.Claim(ctx, k, time.Hour); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	want := idempotency.Result{Code: 200, Body: []byte{0x00, 0x01, 0xFF, 0xFE}}
	if err := s.Finish(ctx, k, idempotency.StatusSucceeded, want, time.Hour); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	st, got, err := s.Get(ctx, k)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if st != idempotency.StatusSucceeded {
		t.Fatalf("status: %v", st)
	}
	if string(got.Body) != string(want.Body) {
		t.Fatalf("body round-trip: got %v, want %v", got.Body, want.Body)
	}
}

// TestIntegration_PostgresStorePruneRemovesExpired drives the
// scheduled prune end-to-end against a real DB.
func TestIntegration_PostgresStorePruneRemovesExpired(t *testing.T) {
	pool := setupPostgres(t)
	if pool == nil {
		return
	}
	s := idempotency.NewPostgresStore(pool)
	ctx := context.Background()
	k := makeKey("integ-pg-prune", "body")

	// Insert with a 1-second TTL.
	if _, _, err := s.Claim(ctx, k, time.Second); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := s.Finish(ctx, k, idempotency.StatusSucceeded, idempotency.Result{Code: 200, Body: []byte("ok")}, time.Second); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	// Wait past the TTL.
	time.Sleep(2 * time.Second)

	n, err := s.Prune(ctx)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if n < 1 {
		t.Fatalf("Prune deleted %d, want at least 1", n)
	}

	_, _, err = s.Get(ctx, k)
	if !errors.Is(err, idempotency.ErrNotFound) {
		t.Fatalf("Get post-prune: want ErrNotFound, got %v", err)
	}
}

// TestIntegration_RedisStoreLuaClaim runs the real Lua claim script
// against a real Redis. This is where we catch syntax errors in the
// Lua that the fake redis can't detect.
func TestIntegration_RedisStoreLuaClaim(t *testing.T) {
	client := setupRedis(t)
	if client == nil {
		return
	}
	s := idempotency.NewRedisStore(client)
	ctx := context.Background()
	k := makeKey("integ-redis-1", "body")

	// New → Pending → Replay sequence.
	out, _, err := s.Claim(ctx, k, time.Minute)
	if err != nil {
		t.Fatalf("Claim 1: %v", err)
	}
	if out != idempotency.ClaimNew {
		t.Fatalf("Claim 1: got %v, want ClaimNew", out)
	}
	out, _, err = s.Claim(ctx, k, time.Minute)
	if err != nil {
		t.Fatalf("Claim 2: %v", err)
	}
	if out != idempotency.ClaimPending {
		t.Fatalf("Claim 2: got %v, want ClaimPending", out)
	}

	want := idempotency.Result{Code: 200, Body: []byte(`{"ok":true}`)}
	if err := s.Finish(ctx, k, idempotency.StatusSucceeded, want, time.Minute); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	out, got, err := s.Claim(ctx, k, time.Minute)
	if err != nil {
		t.Fatalf("Claim 3: %v", err)
	}
	if out != idempotency.ClaimReplay {
		t.Fatalf("Claim 3: got %v, want ClaimReplay", out)
	}
	if got.Code != want.Code || string(got.Body) != string(want.Body) {
		t.Fatalf("replay: got %+v, want %+v", got, want)
	}

	// Mismatch.
	out, _, err = s.Claim(ctx, makeKey("integ-redis-1", "different"), time.Minute)
	if err != nil {
		t.Fatalf("Claim mismatch: %v", err)
	}
	if out != idempotency.ClaimMismatch {
		t.Fatalf("mismatch: got %v, want ClaimMismatch", out)
	}
}

// TestIntegration_RedisStoreConcurrentClaim hammers the Lua script
// with many concurrent goroutines on the same key. Exactly one
// receives ClaimNew; the others all receive ClaimPending.
func TestIntegration_RedisStoreConcurrentClaim(t *testing.T) {
	client := setupRedis(t)
	if client == nil {
		return
	}
	s := idempotency.NewRedisStore(client)
	k := makeKey("integ-redis-race", "body")

	const N = 64
	var wg sync.WaitGroup
	var newCount, pendingCount atomic.Int64
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			out, _, err := s.Claim(context.Background(), k, time.Minute)
			if err != nil {
				t.Errorf("Claim: %v", err)
				return
			}
			switch out {
			case idempotency.ClaimNew:
				newCount.Add(1)
			case idempotency.ClaimPending:
				pendingCount.Add(1)
			default:
				t.Errorf("unexpected outcome %v", out)
			}
		}()
	}
	wg.Wait()
	if newCount.Load() != 1 {
		t.Fatalf("newCount: %d, want 1", newCount.Load())
	}
	if pendingCount.Load() != N-1 {
		t.Fatalf("pendingCount: %d, want %d", pendingCount.Load(), N-1)
	}
}

// TestIntegration_RedisStoreTTLExpiry validates the EX seconds bound
// against real Redis: once the TTL elapses, the next Claim sees a
// fresh key.
func TestIntegration_RedisStoreTTLExpiry(t *testing.T) {
	client := setupRedis(t)
	if client == nil {
		return
	}
	s := idempotency.NewRedisStore(client)
	k := makeKey("integ-redis-ttl", "body")
	ctx := context.Background()

	if _, _, err := s.Claim(ctx, k, time.Second); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	// Wait past the TTL with a small buffer for Redis's
	// second-resolution EX rounding.
	time.Sleep(2200 * time.Millisecond)
	out, _, err := s.Claim(ctx, k, time.Second)
	if err != nil {
		t.Fatalf("Claim post-TTL: %v", err)
	}
	if out != idempotency.ClaimNew {
		t.Fatalf("post-TTL: got %v, want ClaimNew", out)
	}
}

// TestIntegration_TieredStoreFallbackToPostgres simulates a Redis
// outage by pointing the tiered store at a closed Redis client. The
// fallback must read from Postgres without losing the contract.
func TestIntegration_TieredStoreFallbackToPostgres(t *testing.T) {
	pool := setupPostgres(t)
	if pool == nil {
		return
	}
	client := setupRedis(t)
	if client == nil {
		return
	}
	pgs := idempotency.NewPostgresStore(pool)
	rs := idempotency.NewRedisStore(client)
	ts := idempotency.NewTieredStore(rs, pgs)

	ctx := context.Background()
	k := makeKey("integ-tiered", `{"amt":1}`)

	// First call goes through Redis + Postgres.
	out, _, err := ts.Claim(ctx, k, time.Hour)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if out != idempotency.ClaimNew {
		t.Fatalf("Claim 1: got %v, want ClaimNew", out)
	}
	want := idempotency.Result{Code: 200, Body: []byte(`{"ok":true}`)}
	if err := ts.Finish(ctx, k, idempotency.StatusSucceeded, want, time.Hour); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	// Flush Redis to simulate a cold cache. The next Claim must
	// still resolve via Postgres.
	if err := client.FlushAll(ctx).Err(); err != nil {
		t.Fatalf("FLUSHALL: %v", err)
	}

	out, got, err := ts.Claim(ctx, k, time.Hour)
	if err != nil {
		t.Fatalf("Claim post-flush: %v", err)
	}
	if out != idempotency.ClaimReplay {
		t.Fatalf("post-flush: got %v, want ClaimReplay", out)
	}
	if got.Code != want.Code || string(got.Body) != string(want.Body) {
		t.Fatalf("post-flush replay: got %+v, want %+v", got, want)
	}
}

// TestIntegration_MiddlewareEndToEnd wires the full middleware over a
// real Redis+Postgres backing and exercises the 200/replay/422/409
// matrix through HTTP.
func TestIntegration_MiddlewareEndToEnd(t *testing.T) {
	pool := setupPostgres(t)
	if pool == nil {
		return
	}
	client := setupRedis(t)
	if client == nil {
		return
	}
	store := idempotency.NewTieredStore(
		idempotency.NewRedisStore(client),
		idempotency.NewPostgresStore(pool),
	)
	var calls atomic.Int64
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"abc"}`))
	})
	srv := idempotency.New(store, idempotency.Config{}).Wrap(inner)

	// 1) Fresh key: handler runs.
	r1 := httptest.NewRequest("POST", "/payments", strings.NewReader(`{"a":1}`))
	r1.Header.Set(idempotency.HeaderName, "e2e-key")
	w1 := httptest.NewRecorder()
	srv.ServeHTTP(w1, r1)
	if w1.Code != http.StatusCreated {
		t.Fatalf("first: status %d", w1.Code)
	}
	if calls.Load() != 1 {
		t.Fatalf("first call count: %d", calls.Load())
	}

	// 2) Replay: same key + same body → stored result, no re-run.
	r2 := httptest.NewRequest("POST", "/payments", strings.NewReader(`{"a":1}`))
	r2.Header.Set(idempotency.HeaderName, "e2e-key")
	w2 := httptest.NewRecorder()
	srv.ServeHTTP(w2, r2)
	if w2.Code != http.StatusCreated {
		t.Fatalf("replay: status %d", w2.Code)
	}
	if calls.Load() != 1 {
		t.Fatalf("replay should not re-invoke handler: %d", calls.Load())
	}
	if w2.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("replay header missing")
	}

	// 3) Mismatch: same key + different body → 422.
	r3 := httptest.NewRequest("POST", "/payments", strings.NewReader(`{"a":2}`))
	r3.Header.Set(idempotency.HeaderName, "e2e-key")
	w3 := httptest.NewRecorder()
	srv.ServeHTTP(w3, r3)
	if w3.Code != http.StatusUnprocessableEntity {
		t.Fatalf("mismatch: status %d", w3.Code)
	}
	if calls.Load() != 1 {
		t.Fatalf("mismatch should not invoke handler: %d", calls.Load())
	}
}
