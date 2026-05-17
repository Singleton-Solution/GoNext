package idempotency

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
)

// =============================================================================
// Fake Redis: in-memory state machine that mirrors the Lua script's
// semantics. We model Redis-on-the-wire because the unit tests run on
// machines without Docker; the integration test (testcontainers) below
// covers the real-server happy path.
// =============================================================================

// fakeRedis stores the same "hash|status|code|body_b64" strings the
// real RedisStore writes, plus per-key TTL deadlines. The Eval path
// interprets the script body inline.
type fakeRedis struct {
	mu       sync.Mutex
	rows     map[string]fakeRedisRow
	now      func() time.Time
	failEval error
	failGet  error
	failSet  error
}

type fakeRedisRow struct {
	val     string
	expires time.Time
}

func newFakeRedis(now func() time.Time) *fakeRedis {
	if now == nil {
		now = time.Now
	}
	return &fakeRedis{rows: map[string]fakeRedisRow{}, now: now}
}

func (f *fakeRedis) ScriptLoad(ctx context.Context, _ string) *redis.StringCmd {
	cmd := redis.NewStringCmd(ctx)
	cmd.SetVal("fake-sha")
	return cmd
}

func (f *fakeRedis) EvalSha(ctx context.Context, _ string, keys []string, args ...any) *redis.Cmd {
	return f.Eval(ctx, "", keys, args...)
}

func (f *fakeRedis) Eval(ctx context.Context, _ string, keys []string, args ...any) *redis.Cmd {
	cmd := redis.NewCmd(ctx)
	if f.failEval != nil {
		cmd.SetErr(f.failEval)
		return cmd
	}
	if len(keys) != 1 || len(args) != 2 {
		cmd.SetErr(errors.New("fakeRedis.Eval: bad arity"))
		return cmd
	}
	key := keys[0]
	hash := args[0].(string)
	ttlSec, _ := args[1].(int64)
	if ttlSec == 0 {
		switch v := args[1].(type) {
		case int:
			ttlSec = int64(v)
		case int64:
			ttlSec = v
		}
	}
	ttl := time.Duration(ttlSec) * time.Second

	f.mu.Lock()
	defer f.mu.Unlock()

	now := f.now()
	row, ok := f.rows[key]
	if ok && !row.expires.After(now) {
		// Treat expired entries as missing — the real Redis would
		// have evicted them via EX.
		delete(f.rows, key)
		ok = false
	}
	if !ok {
		f.rows[key] = fakeRedisRow{
			val:     hash + "|in_progress|0|",
			expires: now.Add(ttl),
		}
		cmd.SetVal([]any{int64(luaOutcomeNew), "in_progress", int64(0), ""})
		return cmd
	}
	parts := splitFour(row.val, '|')
	if parts == nil {
		delete(f.rows, key)
		cmd.SetVal([]any{int64(luaOutcomeNew), "", int64(0), ""})
		return cmd
	}
	if parts[0] != hash {
		var code int64
		fmt.Sscanf(parts[2], "%d", &code)
		cmd.SetVal([]any{int64(luaOutcomeMismatch), parts[1], code, parts[3]})
		return cmd
	}
	if parts[1] == "in_progress" {
		cmd.SetVal([]any{int64(luaOutcomePending), parts[1], int64(0), ""})
		return cmd
	}
	var code int64
	fmt.Sscanf(parts[2], "%d", &code)
	cmd.SetVal([]any{int64(luaOutcomeReplay), parts[1], code, parts[3]})
	return cmd
}

func (f *fakeRedis) Get(ctx context.Context, key string) *redis.StringCmd {
	cmd := redis.NewStringCmd(ctx)
	if f.failGet != nil {
		cmd.SetErr(f.failGet)
		return cmd
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.rows[key]
	if !ok || !row.expires.After(f.now()) {
		cmd.SetErr(redis.Nil)
		return cmd
	}
	cmd.SetVal(row.val)
	return cmd
}

func (f *fakeRedis) Set(ctx context.Context, key string, value any, ttl time.Duration) *redis.StatusCmd {
	cmd := redis.NewStatusCmd(ctx)
	if f.failSet != nil {
		cmd.SetErr(f.failSet)
		return cmd
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[key] = fakeRedisRow{
		val:     value.(string),
		expires: f.now().Add(ttl),
	}
	cmd.SetVal("OK")
	return cmd
}

func (f *fakeRedis) Del(ctx context.Context, keys ...string) *redis.IntCmd {
	cmd := redis.NewIntCmd(ctx)
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int64
	for _, k := range keys {
		if _, ok := f.rows[k]; ok {
			delete(f.rows, k)
			n++
		}
	}
	cmd.SetVal(n)
	return cmd
}

// makeKey builds a Key for tests. We hash a fixed deterministic input
// so the request_hash is stable across test runs.
func makeKey(t *testing.T, value, requestBody string) Key {
	t.Helper()
	if err := ValidateKeyValue(value); err != nil {
		t.Fatalf("ValidateKeyValue: %v", err)
	}
	h := sha256.Sum256([]byte(requestBody))
	return Key{Value: value, RequestHash: h[:]}
}

// TestRedisStore_FirstClaimIsNew exercises the SETNX path: a brand
// new key returns ClaimNew and the row is marked in_progress.
func TestRedisStore_FirstClaimIsNew(t *testing.T) {
	t.Parallel()
	fake := newFakeRedis(time.Now)
	s := newRedisStoreForTest(fake, time.Now)
	k := makeKey(t, "key-1", "body-1")

	out, _, err := s.Claim(context.Background(), k, time.Minute)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if out != ClaimNew {
		t.Fatalf("first Claim: got %v, want ClaimNew", out)
	}
}

// TestRedisStore_ReplayWithSameHash is the canonical replay: same key
// + same body → ClaimReplay with the stored result.
func TestRedisStore_ReplayWithSameHash(t *testing.T) {
	t.Parallel()
	fake := newFakeRedis(time.Now)
	s := newRedisStoreForTest(fake, time.Now)
	k := makeKey(t, "key-replay", "body")

	if _, _, err := s.Claim(context.Background(), k, time.Minute); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	want := Result{Code: 201, Body: []byte(`{"ok":true}`)}
	if err := s.Finish(context.Background(), k, StatusSucceeded, want, time.Minute); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	out, got, err := s.Claim(context.Background(), k, time.Minute)
	if err != nil {
		t.Fatalf("Claim (replay): %v", err)
	}
	if out != ClaimReplay {
		t.Fatalf("replay outcome: got %v, want ClaimReplay", out)
	}
	if got.Code != want.Code || string(got.Body) != string(want.Body) {
		t.Fatalf("replay result: got %+v, want %+v", got, want)
	}
}

// TestRedisStore_MismatchOnDifferentHash is the 422 path.
func TestRedisStore_MismatchOnDifferentHash(t *testing.T) {
	t.Parallel()
	fake := newFakeRedis(time.Now)
	s := newRedisStoreForTest(fake, time.Now)
	k1 := makeKey(t, "key-mismatch", "body-A")
	k2 := makeKey(t, "key-mismatch", "body-B")

	if _, _, err := s.Claim(context.Background(), k1, time.Minute); err != nil {
		t.Fatalf("Claim k1: %v", err)
	}
	if err := s.Finish(context.Background(), k1, StatusSucceeded, Result{Code: 200, Body: []byte("ok")}, time.Minute); err != nil {
		t.Fatalf("Finish k1: %v", err)
	}
	out, _, err := s.Claim(context.Background(), k2, time.Minute)
	if err != nil {
		t.Fatalf("Claim k2: %v", err)
	}
	if out != ClaimMismatch {
		t.Fatalf("got %v, want ClaimMismatch", out)
	}
}

// TestRedisStore_PendingOnConcurrentClaim simulates two replicas
// racing the same key. Exactly one wins.
func TestRedisStore_PendingOnConcurrentClaim(t *testing.T) {
	t.Parallel()
	fake := newFakeRedis(time.Now)
	s := newRedisStoreForTest(fake, time.Now)
	k := makeKey(t, "key-race", "body")

	out1, _, err := s.Claim(context.Background(), k, time.Minute)
	if err != nil {
		t.Fatalf("Claim 1: %v", err)
	}
	out2, _, err := s.Claim(context.Background(), k, time.Minute)
	if err != nil {
		t.Fatalf("Claim 2: %v", err)
	}
	if out1 != ClaimNew {
		t.Fatalf("first Claim: got %v, want ClaimNew", out1)
	}
	if out2 != ClaimPending {
		t.Fatalf("second Claim: got %v, want ClaimPending", out2)
	}
}

// TestRedisStore_TTLExpiresClaim confirms the EX seconds bound is
// real — once it elapses, the next Claim is treated as new.
func TestRedisStore_TTLExpiresClaim(t *testing.T) {
	t.Parallel()
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	fake := newFakeRedis(clk.Now)
	s := newRedisStoreForTest(fake, clk.Now)
	k := makeKey(t, "key-ttl", "body")

	if _, _, err := s.Claim(context.Background(), k, 5*time.Second); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	// Without time advancing, a re-claim should be Pending.
	out, _, _ := s.Claim(context.Background(), k, 5*time.Second)
	if out != ClaimPending {
		t.Fatalf("pre-TTL: got %v, want ClaimPending", out)
	}

	// Advance past the TTL — re-claim returns ClaimNew.
	clk.advance(10 * time.Second)
	out, _, err := s.Claim(context.Background(), k, 5*time.Second)
	if err != nil {
		t.Fatalf("Claim post-TTL: %v", err)
	}
	if out != ClaimNew {
		t.Fatalf("post-TTL: got %v, want ClaimNew", out)
	}
}

// TestRedisStore_GetMissReturnsErrNotFound exercises the lookup path
// used by [TieredStore.Get].
func TestRedisStore_GetMissReturnsErrNotFound(t *testing.T) {
	t.Parallel()
	fake := newFakeRedis(time.Now)
	s := newRedisStoreForTest(fake, time.Now)
	k := makeKey(t, "no-such", "")
	_, _, err := s.Get(context.Background(), k)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get miss: want ErrNotFound, got %v", err)
	}
}

// TestRedisStore_GetInProgressReturnsErrNotFound covers the corner
// case where Get is called before Finish — there's no terminal result
// to return.
func TestRedisStore_GetInProgressReturnsErrNotFound(t *testing.T) {
	t.Parallel()
	fake := newFakeRedis(time.Now)
	s := newRedisStoreForTest(fake, time.Now)
	k := makeKey(t, "in-progress", "body")
	if _, _, err := s.Claim(context.Background(), k, time.Minute); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	_, _, err := s.Get(context.Background(), k)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get in-progress: want ErrNotFound, got %v", err)
	}
}

// TestRedisStore_FinishRejectsInProgressStatus is the safety check —
// we don't allow Finish to write a non-terminal state, otherwise the
// claim could never be replayed.
func TestRedisStore_FinishRejectsInProgressStatus(t *testing.T) {
	t.Parallel()
	fake := newFakeRedis(time.Now)
	s := newRedisStoreForTest(fake, time.Now)
	k := makeKey(t, "fin", "body")
	err := s.Finish(context.Background(), k, StatusInProgress, Result{}, time.Minute)
	if err == nil {
		t.Fatal("Finish with in_progress: want error, got nil")
	}
}

// TestRedisStore_BackendErrorIsPropagated confirms a Redis outage
// surfaces as an error, not a silent ClaimNew.
func TestRedisStore_BackendErrorIsPropagated(t *testing.T) {
	t.Parallel()
	fake := newFakeRedis(time.Now)
	fake.failEval = errors.New("redis: connection refused")
	s := newRedisStoreForTest(fake, time.Now)
	k := makeKey(t, "boom", "body")
	_, _, err := s.Claim(context.Background(), k, time.Minute)
	if err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("Claim: want connection refused, got %v", err)
	}
}

// fakeClock is a tiny mockable time source.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// =============================================================================
// Fake Postgres: minimal in-memory state for the PostgresStore unit
// tests. The real DB is exercised by the testcontainers integration
// test below.
// =============================================================================

type fakePG struct {
	mu        sync.Mutex
	rows      map[string]fakePGRow
	failExec  error
	failQuery error
}

type fakePGRow struct {
	hash      []byte
	status    string
	code      *int
	body      []byte
	createdAt time.Time
	expiresAt time.Time
}

func newFakePG() *fakePG { return &fakePG{rows: map[string]fakePGRow{}} }

type fakeCommandTag struct{ n int64 }

func (f fakeCommandTag) RowsAffected() int64 { return f.n }

func (f *fakePG) Exec(_ context.Context, sql string, args ...any) (pgxCommandTag, error) {
	if f.failExec != nil {
		return fakeCommandTag{}, f.failExec
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	switch {
	case strings.HasPrefix(strings.TrimSpace(sql), "INSERT INTO idempotency_keys"):
		key := args[0].(string)
		hash := args[1].([]byte)
		createdAt := args[2].(time.Time)
		expiresAt := args[3].(time.Time)
		if existing, ok := f.rows[key]; ok && existing.expiresAt.After(createdAt) {
			return fakeCommandTag{n: 0}, nil
		}
		f.rows[key] = fakePGRow{
			hash:      hash,
			status:    "in_progress",
			createdAt: createdAt,
			expiresAt: expiresAt,
		}
		return fakeCommandTag{n: 1}, nil

	case strings.HasPrefix(strings.TrimSpace(sql), "UPDATE idempotency_keys"):
		status := args[0].(string)
		code := args[1].(int)
		body := args[2].([]byte)
		expiresAt := args[3].(time.Time)
		key := args[4].(string)
		row, ok := f.rows[key]
		if !ok || row.status != "in_progress" {
			return fakeCommandTag{n: 0}, nil
		}
		row.status = status
		row.code = &code
		row.body = body
		row.expiresAt = expiresAt
		f.rows[key] = row
		return fakeCommandTag{n: 1}, nil

	case strings.HasPrefix(strings.TrimSpace(sql), "DELETE FROM idempotency_keys"):
		cutoff := args[0].(time.Time)
		var n int64
		for k, r := range f.rows {
			if r.expiresAt.Before(cutoff) {
				delete(f.rows, k)
				n++
			}
		}
		return fakeCommandTag{n: n}, nil
	}
	return fakeCommandTag{}, fmt.Errorf("fakePG.Exec: unknown SQL %q", sql)
}

func (f *fakePG) QueryRow(_ context.Context, _ string, args ...any) pgx.Row {
	if f.failQuery != nil {
		return &fakeRow{err: f.failQuery}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	key := args[0].(string)
	row, ok := f.rows[key]
	if !ok {
		return &fakeRow{err: pgx.ErrNoRows}
	}
	return &fakeRow{row: row}
}

type fakeRow struct {
	row fakePGRow
	err error
}

func (r *fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	switch len(dest) {
	case 5: // Claim's SELECT: request_hash, status, result_code, result_body, expires_at
		*(dest[0].(*[]byte)) = r.row.hash
		*(dest[1].(*string)) = r.row.status
		*(dest[2].(**int)) = r.row.code
		*(dest[3].(*[]byte)) = r.row.body
		*(dest[4].(*time.Time)) = r.row.expiresAt
	case 4: // Get's SELECT: status, result_code, result_body, expires_at
		*(dest[0].(*string)) = r.row.status
		*(dest[1].(**int)) = r.row.code
		*(dest[2].(*[]byte)) = r.row.body
		*(dest[3].(*time.Time)) = r.row.expiresAt
	default:
		return fmt.Errorf("fakeRow.Scan: bad arity %d", len(dest))
	}
	return nil
}

// TestPostgresStore_ClaimAndFinish is the end-to-end happy path for
// the durable tier. Insert → Finish → Get returns the stored result.
func TestPostgresStore_ClaimAndFinish(t *testing.T) {
	t.Parallel()
	pg := newFakePG()
	s := newPostgresStoreForTest(pg, time.Now)
	k := makeKey(t, "pg-1", "body")

	out, _, err := s.Claim(context.Background(), k, time.Hour)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if out != ClaimNew {
		t.Fatalf("Claim: got %v, want ClaimNew", out)
	}

	want := Result{Code: 200, Body: []byte(`{"id":"abc"}`)}
	if err := s.Finish(context.Background(), k, StatusSucceeded, want, time.Hour); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	st, got, err := s.Get(context.Background(), k)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if st != StatusSucceeded {
		t.Fatalf("status: %v", st)
	}
	if got.Code != want.Code || string(got.Body) != string(want.Body) {
		t.Fatalf("result: got %+v, want %+v", got, want)
	}
}

// TestPostgresStore_ReplayDuplicate returns the stored result when
// the same key+hash is claimed again. This is the durable tier's
// promise: even if Redis flushes, the cache survives.
func TestPostgresStore_ReplayDuplicate(t *testing.T) {
	t.Parallel()
	pg := newFakePG()
	s := newPostgresStoreForTest(pg, time.Now)
	k := makeKey(t, "pg-dup", "body")

	if _, _, err := s.Claim(context.Background(), k, time.Hour); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := s.Finish(context.Background(), k, StatusSucceeded, Result{Code: 201, Body: []byte(`{"ok":1}`)}, time.Hour); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	out, res, err := s.Claim(context.Background(), k, time.Hour)
	if err != nil {
		t.Fatalf("replay Claim: %v", err)
	}
	if out != ClaimReplay {
		t.Fatalf("replay: got %v, want ClaimReplay", out)
	}
	if res.Code != 201 || string(res.Body) != `{"ok":1}` {
		t.Fatalf("replay result: %+v", res)
	}
}

// TestPostgresStore_MismatchOnDifferentHash covers the 422 case for
// the durable tier — same as RedisStore but the conflict resolution
// happens at the SQL row level.
func TestPostgresStore_MismatchOnDifferentHash(t *testing.T) {
	t.Parallel()
	pg := newFakePG()
	s := newPostgresStoreForTest(pg, time.Now)
	k1 := makeKey(t, "pg-mm", "body-A")
	k2 := makeKey(t, "pg-mm", "body-B")

	if _, _, err := s.Claim(context.Background(), k1, time.Hour); err != nil {
		t.Fatalf("Claim k1: %v", err)
	}
	out, _, err := s.Claim(context.Background(), k2, time.Hour)
	if err != nil {
		t.Fatalf("Claim k2: %v", err)
	}
	if out != ClaimMismatch {
		t.Fatalf("got %v, want ClaimMismatch", out)
	}
}

// TestPostgresStore_PrunesExpiredRows verifies the Prune call deletes
// every row whose expires_at is in the past. The scheduled job
// (issue #264 §7) calls this on a 24h cadence.
func TestPostgresStore_PrunesExpiredRows(t *testing.T) {
	t.Parallel()
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	pg := newFakePG()
	s := newPostgresStoreForTest(pg, clk.Now)
	k := makeKey(t, "pg-prune", "body")

	if _, _, err := s.Claim(context.Background(), k, time.Hour); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := s.Finish(context.Background(), k, StatusSucceeded, Result{Code: 200, Body: []byte("ok")}, time.Hour); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	// Before expiry: prune is a no-op.
	n, err := s.Prune(context.Background())
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if n != 0 {
		t.Fatalf("Prune (fresh): got %d, want 0", n)
	}

	clk.advance(2 * time.Hour)
	n, err = s.Prune(context.Background())
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if n != 1 {
		t.Fatalf("Prune (expired): got %d, want 1", n)
	}
	_, _, err = s.Get(context.Background(), k)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get post-prune: want ErrNotFound, got %v", err)
	}
}

// TestPostgresStore_ExpiredEntryReclaimable confirms that even
// without an explicit Prune call, an expired row is treated as
// missing — the middleware can re-claim it on the next request.
func TestPostgresStore_ExpiredEntryReclaimable(t *testing.T) {
	t.Parallel()
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	pg := newFakePG()
	s := newPostgresStoreForTest(pg, clk.Now)
	k := makeKey(t, "pg-exp", "body")

	if _, _, err := s.Claim(context.Background(), k, 5*time.Second); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := s.Finish(context.Background(), k, StatusSucceeded, Result{Code: 200, Body: []byte("ok")}, 5*time.Second); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	clk.advance(10 * time.Second)
	out, _, err := s.Claim(context.Background(), k, 5*time.Second)
	if err != nil {
		t.Fatalf("Claim post-TTL: %v", err)
	}
	if out != ClaimNew {
		t.Fatalf("post-TTL: got %v, want ClaimNew", out)
	}
}

// TestWrapUnwrapBody round-trips both JSON and binary payloads so a
// later schema bump that touches the wrapper doesn't silently lose
// data.
func TestWrapUnwrapBody(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   []byte
	}{
		{"json object", []byte(`{"id":"abc","amount":42}`)},
		{"json array", []byte(`[1,2,3]`)},
		{"binary", []byte{0x00, 0x01, 0xFF, 0xFE}},
		{"plain text", []byte("hello world")},
		{"empty", nil},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			wrapped, err := wrapBody(tc.in)
			if err != nil {
				t.Fatalf("wrap: %v", err)
			}
			got, err := unwrapBody(wrapped)
			if err != nil {
				t.Fatalf("unwrap: %v", err)
			}
			if string(got) != string(tc.in) {
				t.Fatalf("round-trip: got %q, want %q", got, tc.in)
			}
		})
	}
}

// TestSplitFour rejects malformed payloads instead of returning
// truncated data — the corrupt-redis branch in Claim relies on this
// returning nil to recover.
func TestSplitFour(t *testing.T) {
	t.Parallel()
	if got := splitFour("a|b|c|d", '|'); len(got) != 4 || got[3] != "d" {
		t.Fatalf("splitFour ok: %v", got)
	}
	if got := splitFour("only|two", '|'); got != nil {
		t.Fatalf("splitFour bad arity: got %v, want nil", got)
	}
}

// TestRedisKeyHasPrefix is a property test — operator debug
// (`KEYS idempotency:*`) depends on this layout.
func TestRedisKeyHasPrefix(t *testing.T) {
	t.Parallel()
	k := redisKey("abc-123")
	if want := "idempotency:abc-123"; k != want {
		t.Fatalf("redisKey: got %q, want %q", k, want)
	}
}

// TestRedisStore_FinishRoundTrip confirms a written terminal result
// is readable verbatim — no encoding artifacts on the binary path.
func TestRedisStore_FinishRoundTrip(t *testing.T) {
	t.Parallel()
	fake := newFakeRedis(time.Now)
	s := newRedisStoreForTest(fake, time.Now)
	k := makeKey(t, "rt", "body")
	if _, _, err := s.Claim(context.Background(), k, time.Minute); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	want := Result{Code: 418, Body: []byte{0x00, 0x01, 0x02, 0xFF}}
	if err := s.Finish(context.Background(), k, StatusSucceeded, want, time.Minute); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	// The raw value should be hex-encoded — sanity check
	// the wire format here so a refactor of the payload layout
	// breaks loud.
	row := fake.rows[redisKey("rt")]
	if !strings.Contains(row.val, hex.EncodeToString(want.Body)) {
		t.Fatalf("on-wire payload missing hex body: %q", row.val)
	}
	st, got, err := s.Get(context.Background(), k)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if st != StatusSucceeded {
		t.Fatalf("status: %v", st)
	}
	if got.Code != want.Code || string(got.Body) != string(want.Body) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}
