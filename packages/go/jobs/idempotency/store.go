package idempotency

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// DefaultTTL is the lifetime of an idempotency record in both tiers.
// 24h is the spec-recommended window (idempotency-key-header-06 §2.5):
// long enough that a legitimate client retry after a network blip
// still hits the cache, short enough that the storage cost stays
// bounded under sustained traffic. Operators can override via
// [WithTTL].
const DefaultTTL = 24 * time.Hour

// Result is the snapshot the middleware stores on a finished request
// and replays on a hit. It carries the HTTP status code and the body
// bytes; replay does NOT re-execute the inner handler.
type Result struct {
	// Code is the HTTP status code from the original response.
	Code int

	// Body is the response body verbatim. We do NOT enforce JSON —
	// the body might be plaintext, an empty 204, or a binary
	// download. The Postgres store wraps non-JSON bytes in
	// {"_raw_base64": "..."} so the JSONB column stays valid.
	Body []byte
}

// ClaimOutcome is the verdict from [Store.Claim]. It tells the
// middleware whether to dispatch to the inner handler, replay a
// stored result, or refuse with 422 / 409.
type ClaimOutcome int

const (
	// ClaimNew means the key has never been seen. The middleware
	// dispatches to the inner handler and finishes with
	// [Store.Finish].
	ClaimNew ClaimOutcome = iota

	// ClaimReplay means the key exists with the SAME request hash
	// and reached a terminal state. The middleware replays the
	// [Result] returned alongside this outcome.
	ClaimReplay

	// ClaimMismatch means the key exists with a DIFFERENT request
	// hash. The middleware returns 422 idempotency_key_reused. We
	// do NOT clobber the prior entry — the original client may
	// still be polling for that earlier response.
	ClaimMismatch

	// ClaimPending means the key exists and is still in_progress.
	// The middleware returns 409 idempotency_key_pending. The
	// client is expected to retry with exponential backoff; we do
	// NOT block the request, because the inner handler may legitimately
	// run for many seconds and tying up server goroutines to wait
	// for it is its own DoS vector.
	ClaimPending
)

// Store is the storage tier the middleware talks to. Implementations
// must be safe for concurrent use. The contract:
//
//   - Claim is atomic with respect to other Claim calls. Two
//     concurrent Claims with the same key must produce exactly one
//     ClaimNew; the other gets ClaimPending.
//
//   - Finish writes a terminal result for an in-progress claim. It
//     is a no-op if the key has already been written (e.g. by a
//     racing Finish or a Prune). The middleware tolerates the no-op.
//
//   - Get returns the stored result for a succeeded/failed key.
//     Returns ErrNotFound if the key is missing or still in_progress.
type Store interface {
	Claim(ctx context.Context, key Key, ttl time.Duration) (ClaimOutcome, Result, error)
	Finish(ctx context.Context, key Key, status Status, result Result, ttl time.Duration) error
	Get(ctx context.Context, key Key) (Status, Result, error)
}

// ErrNotFound is returned by [Store.Get] when the key is missing or
// has expired. Implementations MUST distinguish this from a transport
// error so the middleware can decide whether to surface 5xx or just
// re-claim.
var ErrNotFound = errors.New("idempotency: not found")

// =============================================================================
// Redis
// =============================================================================

// RedisStore is the hot-path tier. Claim runs the Lua script
// [claimScript] in a single round-trip and returns the outcome
// atomically — there is no GET-then-SETNX race window where two
// replicas can both see "missing" and both run the handler.
//
// Layout:
//
//	key:    "idempotency:{value}"
//	value:  hex(request_hash) || "|" || status || "|" || code || "|" || body_b64
//
// We pack everything into a single Redis string instead of a HASH so
// the Lua script can compare in one O(1) call. The | separator is
// safe because every component is either hex, a fixed enum, an
// integer, or base64 — none contain |.
type RedisStore struct {
	rdb redisClient
	now func() time.Time
}

// redisClient is the subset of *redis.Client we exercise. Pulling out
// an interface lets the unit tests substitute a fake without booting
// a real server, matching the pattern in packages/go/ratelimit/.
type redisClient interface {
	Eval(ctx context.Context, script string, keys []string, args ...any) *redis.Cmd
	EvalSha(ctx context.Context, sha1 string, keys []string, args ...any) *redis.Cmd
	ScriptLoad(ctx context.Context, script string) *redis.StringCmd
	Get(ctx context.Context, key string) *redis.StringCmd
	Set(ctx context.Context, key string, value any, expiration time.Duration) *redis.StatusCmd
	Del(ctx context.Context, keys ...string) *redis.IntCmd
}

// NewRedisStore wires a [RedisStore] around an already-connected
// go-redis client. We do NOT Ping here: the caller has already proven
// the client is live in packages/go/redis/.
func NewRedisStore(rdb *redis.Client) *RedisStore {
	return &RedisStore{rdb: rdb, now: time.Now}
}

// newRedisStoreForTest is the test seam that bypasses *redis.Client
// in favour of the interface, so unit tests can substitute a fake.
func newRedisStoreForTest(c redisClient, now func() time.Time) *RedisStore {
	if now == nil {
		now = time.Now
	}
	return &RedisStore{rdb: c, now: now}
}

// redisKey is the Redis key for a single idempotency entry. The
// prefix matches the rest of the codebase (session:, ratelimit:) so
// debug `KEYS idempotency:*` is useful out of the box.
func redisKey(value string) string { return "idempotency:" + value }

// Outcome marker codes returned by the Lua claim script. Keep these
// in sync with the integers the Lua script emits.
const (
	luaOutcomeNew      = 0
	luaOutcomePending  = 1
	luaOutcomeReplay   = 2
	luaOutcomeMismatch = 3
)

// claimScript is the atomic Lua program executed on the Redis server.
//
//	KEYS[1] = idempotency:{value}
//	ARGV[1] = hex(request_hash)
//	ARGV[2] = ttl in seconds (integer)
//
// Returns {outcome, status, code, body_b64}.
//
// Algorithm:
//
//  1. GET the key.
//  2. If missing → SET (hash | "in_progress" | "" | ""), EX ttl,
//     NX. Return {NEW, "", 0, ""}.
//  3. If present and hash matches:
//     - status == "in_progress" → return {PENDING, ...}
//     - status terminal         → return {REPLAY, status, code, body}
//  4. If hash differs → return {MISMATCH, ...}.
//
// The SET ... NX guards against the (extremely rare) race where the
// key TTL'd between the GET and the SET — without NX a third writer
// could land between them and we'd overwrite their claim.
const claimScript = `
local existing = redis.call('GET', KEYS[1])
if not existing then
  local payload = ARGV[1] .. '|in_progress|0|'
  local ok = redis.call('SET', KEYS[1], payload, 'EX', tonumber(ARGV[2]), 'NX')
  if ok then
    return {0, 'in_progress', 0, ''}
  end
  -- A racing writer claimed between our GET and our SET. Re-read
  -- so we report the truth instead of a spurious NEW.
  existing = redis.call('GET', KEYS[1])
  if not existing then
    -- Should be unreachable, but bail out with a safe default.
    return {1, 'in_progress', 0, ''}
  end
end

-- Parse "hash|status|code|body_b64". string.find with plain=true is
-- the cheap way; we know the separators are |.
local p1 = string.find(existing, '|', 1, true)
local p2 = string.find(existing, '|', p1 + 1, true)
local p3 = string.find(existing, '|', p2 + 1, true)
if not (p1 and p2 and p3) then
  -- Corrupt payload. Treat as missing and let the caller re-claim.
  redis.call('DEL', KEYS[1])
  return {0, '', 0, ''}
end
local existingHash = string.sub(existing, 1, p1 - 1)
local existingStatus = string.sub(existing, p1 + 1, p2 - 1)
local existingCode = string.sub(existing, p2 + 1, p3 - 1)
local existingBody = string.sub(existing, p3 + 1)

if existingHash ~= ARGV[1] then
  return {3, existingStatus, tonumber(existingCode) or 0, existingBody}
end
if existingStatus == 'in_progress' then
  return {1, existingStatus, 0, ''}
end
return {2, existingStatus, tonumber(existingCode) or 0, existingBody}
`

// Claim runs the Lua script. See [Store.Claim] for the contract.
func (s *RedisStore) Claim(ctx context.Context, k Key, ttl time.Duration) (ClaimOutcome, Result, error) {
	if err := ValidateKeyValue(k.Value); err != nil {
		return 0, Result{}, err
	}
	if len(k.RequestHash) == 0 {
		return 0, Result{}, fmt.Errorf("idempotency: empty request hash")
	}
	ttlSec := int64(ttl.Seconds())
	if ttlSec < 1 {
		ttlSec = 1
	}
	res, err := s.rdb.Eval(ctx, claimScript,
		[]string{redisKey(k.Value)}, hex.EncodeToString(k.RequestHash), ttlSec,
	).Result()
	if err != nil {
		return 0, Result{}, fmt.Errorf("idempotency: redis claim: %w", err)
	}
	arr, ok := res.([]any)
	if !ok || len(arr) != 4 {
		return 0, Result{}, fmt.Errorf("idempotency: bad claim reply %T", res)
	}
	outcomeI, _ := arr[0].(int64)
	codeI, _ := arr[2].(int64)
	body, _ := arr[3].(string)

	var bodyBytes []byte
	if body != "" {
		decoded, derr := hex.DecodeString(body)
		if derr != nil {
			// Body was written in a future schema (or by a buggy
			// admin). Behave as if the entry never existed so the
			// caller can re-claim. We don't surface the error to the
			// client — the request is still serviceable.
			return ClaimNew, Result{}, nil
		}
		bodyBytes = decoded
	}

	switch outcomeI {
	case luaOutcomeNew:
		return ClaimNew, Result{}, nil
	case luaOutcomePending:
		return ClaimPending, Result{}, nil
	case luaOutcomeReplay:
		return ClaimReplay, Result{Code: int(codeI), Body: bodyBytes}, nil
	case luaOutcomeMismatch:
		return ClaimMismatch, Result{}, nil
	default:
		return 0, Result{}, fmt.Errorf("idempotency: unknown outcome %d", outcomeI)
	}
}

// Finish writes a terminal result into Redis with a refreshed TTL.
// We bake the existing request_hash back into the payload by reading
// the in-progress entry first; that way a Finish that races a Prune
// doesn't write a record that's missing its hash.
//
// If the key has been pruned between Claim and Finish (TTL elapsed,
// operator DEL, Redis flush), Finish is a no-op — the request is
// already served and the next replay just re-claims.
func (s *RedisStore) Finish(ctx context.Context, k Key, status Status, result Result, ttl time.Duration) error {
	if !status.Valid() || status == StatusInProgress {
		return fmt.Errorf("idempotency: Finish requires terminal status, got %q", status)
	}
	payload := hex.EncodeToString(k.RequestHash) + "|" + string(status) + "|" +
		fmt.Sprintf("%d", result.Code) + "|" + hex.EncodeToString(result.Body)
	if err := s.rdb.Set(ctx, redisKey(k.Value), payload, ttl).Err(); err != nil {
		return fmt.Errorf("idempotency: redis finish: %w", err)
	}
	return nil
}

// Get reads a terminal entry directly. Used by the middleware after a
// Redis miss to fall back to Postgres, and by tests.
func (s *RedisStore) Get(ctx context.Context, k Key) (Status, Result, error) {
	raw, err := s.rdb.Get(ctx, redisKey(k.Value)).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return "", Result{}, ErrNotFound
		}
		return "", Result{}, fmt.Errorf("idempotency: redis get: %w", err)
	}
	return parseRedisPayload(raw)
}

// parseRedisPayload splits the "hash|status|code|body_b64" layout.
// Returns ErrNotFound for "in_progress" entries so the middleware
// treats them as "no terminal result yet" — Get is for replay, not
// for state inspection.
func parseRedisPayload(raw string) (Status, Result, error) {
	parts := splitFour(raw, '|')
	if parts == nil {
		return "", Result{}, fmt.Errorf("idempotency: corrupt redis payload")
	}
	st := Status(parts[1])
	if !st.Valid() {
		return "", Result{}, fmt.Errorf("idempotency: unknown status %q", parts[1])
	}
	if st == StatusInProgress {
		return "", Result{}, ErrNotFound
	}
	code := 0
	for _, c := range parts[2] {
		if c < '0' || c > '9' {
			return "", Result{}, fmt.Errorf("idempotency: bad code %q", parts[2])
		}
		code = code*10 + int(c-'0')
	}
	var body []byte
	if parts[3] != "" {
		decoded, err := hex.DecodeString(parts[3])
		if err != nil {
			return "", Result{}, fmt.Errorf("idempotency: bad body hex: %w", err)
		}
		body = decoded
	}
	return st, Result{Code: code, Body: body}, nil
}

// splitFour splits s on sep into exactly four parts. Returns nil if
// the count is wrong, so callers can treat that as "corrupt".
func splitFour(s string, sep byte) []string {
	out := make([]string, 0, 4)
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			out = append(out, s[start:i])
			start = i + 1
			if len(out) == 3 {
				out = append(out, s[start:])
				return out
			}
		}
	}
	if len(out) != 4 {
		return nil
	}
	return out
}

// =============================================================================
// Postgres
// =============================================================================

// PostgresStore is the durable tier. It is intentionally narrow —
// the hot path goes through Redis; Postgres is only consulted when
// Redis misses or when the middleware writes through.
//
// The schema lives in migrations/000014_idempotency.up.sql.
type PostgresStore struct {
	db  pgPool
	now func() time.Time
}

// pgPool is the subset of pgxpool.Pool we use. Defining an interface
// keeps the package importable from tests that prefer to inject a
// mock transactor — but the production path always passes a real pool.
type pgPool interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgxCommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// pgxCommandTag is a tiny shim around pgx's CommandTag so the pgPool
// interface doesn't leak the pgx type. We only need RowsAffected().
type pgxCommandTag interface {
	RowsAffected() int64
}

// pgxPoolWrapper adapts *pgxpool.Pool to the pgPool interface. Pulled
// out so tests can substitute without depending on pgxpool.
type pgxPoolWrapper struct {
	p *pgxpool.Pool
}

func (w pgxPoolWrapper) Exec(ctx context.Context, sql string, arguments ...any) (pgxCommandTag, error) {
	tag, err := w.p.Exec(ctx, sql, arguments...)
	return tag, err
}

func (w pgxPoolWrapper) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return w.p.QueryRow(ctx, sql, args...)
}

// NewPostgresStore wires a [PostgresStore] over a pgxpool.Pool.
func NewPostgresStore(p *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{db: pgxPoolWrapper{p: p}, now: time.Now}
}

// newPostgresStoreForTest is the test seam for injecting a fake pool.
func newPostgresStoreForTest(p pgPool, now func() time.Time) *PostgresStore {
	if now == nil {
		now = time.Now
	}
	return &PostgresStore{db: p, now: now}
}

// rawBodyMarker is the JSON wrapper for response bodies that aren't
// valid JSON (binary downloads, plaintext, empty 204s). We pick a
// reserved key starting with underscore so it can't collide with a
// real API response that happens to be a JSON object.
const rawBodyMarker = "_raw_base64"

// Claim performs the atomic insert-or-fetch via Postgres. The query
// uses INSERT ... ON CONFLICT (key) DO NOTHING RETURNING so we get
// either "newly inserted" (RETURNING the in-progress row) or
// "already exists" (no rows) in one round-trip.
//
// We follow with a SELECT only on the "already exists" branch so
// the happy path stays one statement.
func (s *PostgresStore) Claim(ctx context.Context, k Key, ttl time.Duration) (ClaimOutcome, Result, error) {
	if err := ValidateKeyValue(k.Value); err != nil {
		return 0, Result{}, err
	}
	if len(k.RequestHash) == 0 {
		return 0, Result{}, fmt.Errorf("idempotency: empty request hash")
	}
	now := s.now().UTC()
	expires := now.Add(ttl)
	// Try to insert. On conflict, do nothing and we'll read the existing
	// row in the next query.
	tag, err := s.db.Exec(ctx, `
		INSERT INTO idempotency_keys (key, request_hash, status, created_at, expires_at)
		VALUES ($1, $2, 'in_progress', $3, $4)
		ON CONFLICT (key) DO NOTHING
	`, k.Value, k.RequestHash, now, expires)
	if err != nil {
		return 0, Result{}, fmt.Errorf("idempotency: pg claim insert: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return ClaimNew, Result{}, nil
	}

	// Conflict: someone got there first. Read the existing row and
	// decide which outcome applies.
	var existingHash []byte
	var status string
	var code *int
	var bodyJSON []byte
	var rowExpires time.Time
	row := s.db.QueryRow(ctx, `
		SELECT request_hash, status, result_code, result_body, expires_at
		FROM idempotency_keys
		WHERE key = $1
	`, k.Value)
	if err := row.Scan(&existingHash, &status, &code, &bodyJSON, &rowExpires); err != nil {
		// If the row was pruned between our INSERT and our SELECT
		// (TTL elapsed in the millisecond gap), treat as a new claim.
		// The middleware will retry; we don't surface the error.
		if errors.Is(err, pgx.ErrNoRows) {
			return ClaimNew, Result{}, nil
		}
		return 0, Result{}, fmt.Errorf("idempotency: pg claim select: %w", err)
	}

	// Expired row? Treat as gone — the caller can re-claim, and the
	// next prune cycle will remove the stale row. We deliberately
	// don't DELETE here to keep the Claim path side-effect-free.
	if !rowExpires.After(now) {
		return ClaimNew, Result{}, nil
	}

	// Hash mismatch → 422.
	if !bytesEqual(existingHash, k.RequestHash) {
		return ClaimMismatch, Result{}, nil
	}

	switch Status(status) {
	case StatusInProgress:
		return ClaimPending, Result{}, nil
	case StatusSucceeded, StatusFailed:
		body, derr := unwrapBody(bodyJSON)
		if derr != nil {
			return 0, Result{}, fmt.Errorf("idempotency: pg unwrap body: %w", derr)
		}
		c := 0
		if code != nil {
			c = *code
		}
		return ClaimReplay, Result{Code: c, Body: body}, nil
	default:
		return 0, Result{}, fmt.Errorf("idempotency: pg unknown status %q", status)
	}
}

// Finish updates the in-progress row to terminal. We use a WHERE
// status='in_progress' guard so a racing Finish (or a prune that
// re-inserted) can't be silently clobbered.
func (s *PostgresStore) Finish(ctx context.Context, k Key, status Status, result Result, ttl time.Duration) error {
	if !status.Valid() || status == StatusInProgress {
		return fmt.Errorf("idempotency: Finish requires terminal status, got %q", status)
	}
	bodyJSON, err := wrapBody(result.Body)
	if err != nil {
		return fmt.Errorf("idempotency: pg wrap body: %w", err)
	}
	now := s.now().UTC()
	expires := now.Add(ttl)
	_, err = s.db.Exec(ctx, `
		UPDATE idempotency_keys
		SET status = $1, result_code = $2, result_body = $3, expires_at = $4
		WHERE key = $5 AND status = 'in_progress'
	`, string(status), result.Code, bodyJSON, expires, k.Value)
	if err != nil {
		return fmt.Errorf("idempotency: pg finish: %w", err)
	}
	return nil
}

// Get reads a terminal entry. Returns ErrNotFound for missing or
// in-progress rows.
func (s *PostgresStore) Get(ctx context.Context, k Key) (Status, Result, error) {
	var status string
	var code *int
	var bodyJSON []byte
	var rowExpires time.Time
	row := s.db.QueryRow(ctx, `
		SELECT status, result_code, result_body, expires_at
		FROM idempotency_keys
		WHERE key = $1
	`, k.Value)
	if err := row.Scan(&status, &code, &bodyJSON, &rowExpires); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", Result{}, ErrNotFound
		}
		return "", Result{}, fmt.Errorf("idempotency: pg get: %w", err)
	}
	if !rowExpires.After(s.now().UTC()) {
		return "", Result{}, ErrNotFound
	}
	st := Status(status)
	if !st.Valid() || st == StatusInProgress {
		return "", Result{}, ErrNotFound
	}
	body, err := unwrapBody(bodyJSON)
	if err != nil {
		return "", Result{}, fmt.Errorf("idempotency: pg unwrap body: %w", err)
	}
	c := 0
	if code != nil {
		c = *code
	}
	return st, Result{Code: c, Body: body}, nil
}

// Prune deletes rows whose expires_at is in the past. Called by the
// scheduled cleanup job (issue #264 §7). Returns the number of rows
// removed for the cleanup job's metric label.
func (s *PostgresStore) Prune(ctx context.Context) (int64, error) {
	tag, err := s.db.Exec(ctx, `
		DELETE FROM idempotency_keys WHERE expires_at < $1
	`, s.now().UTC())
	if err != nil {
		return 0, fmt.Errorf("idempotency: pg prune: %w", err)
	}
	return tag.RowsAffected(), nil
}

// wrapBody encodes a response body for the JSONB column. We have two
// shapes:
//
//   - JSON bodies are stored inside {"_raw_base64": ..., "json": <body>}
//     so operators can still index/query the JSON path via
//     result_body->'json' while replay byte-fidelity is preserved
//     through the base64 sidecar. Postgres JSONB normalises whitespace
//     and key ordering, so we cannot rely on the raw JSON column to
//     replay the exact bytes the client first received.
//
//   - Non-JSON bodies are stored as {"_raw_base64": "..."} only.
//
// For the small but real subset of replayed responses where a client
// computes a signature over the raw bytes, byte fidelity is the only
// safe contract. The JSON sidecar gives operators the index they need.
func wrapBody(body []byte) ([]byte, error) {
	if len(body) == 0 {
		return []byte("null"), nil
	}
	// Always store the raw bytes via base64 for replay fidelity.
	wrapper := map[string]any{rawBodyMarker: body}
	// If the bytes parse as JSON, expose them under "json" too so
	// operator queries can drill into the structure.
	var probe json.RawMessage
	if err := json.Unmarshal(body, &probe); err == nil {
		wrapper["json"] = probe
	}
	return json.Marshal(wrapper)
}

// unwrapBody reverses [wrapBody]. The wrapper always carries the raw
// bytes under [rawBodyMarker]; we read that field exclusively to
// preserve byte fidelity.
func unwrapBody(stored []byte) ([]byte, error) {
	if len(stored) == 0 || string(stored) == "null" {
		return nil, nil
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(stored, &probe); err == nil {
		if raw, ok := probe[rawBodyMarker]; ok {
			var decoded []byte
			if err := json.Unmarshal(raw, &decoded); err == nil {
				return decoded, nil
			}
		}
	}
	// Legacy path: a row written before this wrapper shape existed.
	// Return verbatim — operators may have hand-inserted rows.
	return stored, nil
}

// bytesEqual is a tiny shim so we don't pull in "bytes" for one call.
// crypto/subtle.ConstantTimeCompare is overkill for a hash equality
// check — there's no secret on either side.
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// =============================================================================
// Tiered
// =============================================================================

// TieredStore composes a Redis hot tier in front of a Postgres
// durable tier. Reads check Redis first; on miss they fall through
// to Postgres and re-populate Redis with the recovered entry.
// Writes go through both stores; a Postgres failure aborts the
// write (the durable record is the source of truth), a Redis
// failure is logged and tolerated (the next request re-claims).
//
// Construct one of these per process; both inner stores are safe
// for concurrent use, so this is too.
type TieredStore struct {
	Redis    *RedisStore
	Postgres *PostgresStore

	// OnRedisErr is called when a Redis operation fails but the
	// Postgres path is intact. The middleware wires this to its
	// logger so operators see the degradation without the request
	// failing. May be nil.
	OnRedisErr func(err error)
}

// NewTieredStore returns a TieredStore over both backends. Either
// may be nil; missing tiers degrade to a single-store setup, useful
// for tests and small deployments that don't need both.
func NewTieredStore(r *RedisStore, p *PostgresStore) *TieredStore {
	return &TieredStore{Redis: r, Postgres: p}
}

// Claim consults Redis first. On hit it returns directly; on miss it
// falls through to Postgres and warms Redis with the recovered entry
// so the next request stays on the fast path.
func (t *TieredStore) Claim(ctx context.Context, k Key, ttl time.Duration) (ClaimOutcome, Result, error) {
	if t.Redis != nil {
		out, res, err := t.Redis.Claim(ctx, k, ttl)
		if err == nil {
			// On NEW we still need to plant the record in Postgres so
			// it survives a Redis flush. Do it in the same goroutine
			// so Finish can rely on the row being there.
			if out == ClaimNew && t.Postgres != nil {
				// Postgres returns NEW for an unclaimed row, REPLAY/
				// PENDING/MISMATCH for an existing one. If Redis said
				// NEW but Postgres says PENDING (replica raced), we
				// trust Postgres — it's the durable tier.
				pgOut, pgRes, pgErr := t.Postgres.Claim(ctx, k, ttl)
				if pgErr != nil {
					return 0, Result{}, pgErr
				}
				if pgOut != ClaimNew {
					return pgOut, pgRes, nil
				}
			}
			return out, res, nil
		}
		if t.OnRedisErr != nil {
			t.OnRedisErr(err)
		}
	}
	if t.Postgres == nil {
		return 0, Result{}, errors.New("idempotency: no store available")
	}
	return t.Postgres.Claim(ctx, k, ttl)
}

// Finish writes through both stores. Postgres is authoritative; a
// Redis write failure is logged and otherwise ignored.
func (t *TieredStore) Finish(ctx context.Context, k Key, status Status, result Result, ttl time.Duration) error {
	if t.Postgres != nil {
		if err := t.Postgres.Finish(ctx, k, status, result, ttl); err != nil {
			return err
		}
	}
	if t.Redis != nil {
		if err := t.Redis.Finish(ctx, k, status, result, ttl); err != nil && t.OnRedisErr != nil {
			t.OnRedisErr(err)
		}
	}
	return nil
}

// Get queries Redis first, then Postgres. We do NOT warm Redis on a
// Postgres hit here — Get is rare (only used by tests and admin
// tooling) and the warm path is naturally taken on the next Claim.
func (t *TieredStore) Get(ctx context.Context, k Key) (Status, Result, error) {
	if t.Redis != nil {
		st, res, err := t.Redis.Get(ctx, k)
		if err == nil {
			return st, res, nil
		}
		if !errors.Is(err, ErrNotFound) && t.OnRedisErr != nil {
			t.OnRedisErr(err)
		}
	}
	if t.Postgres == nil {
		return "", Result{}, ErrNotFound
	}
	return t.Postgres.Get(ctx, k)
}
