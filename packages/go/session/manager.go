package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/config"
	"github.com/redis/go-redis/v9"
)

// ErrNotFound is returned by [Manager.Get] when the token is unknown or
// has expired. Callers should treat it as the same outcome as "no
// cookie was sent" — clear the cookie and route the user to login.
var ErrNotFound = errors.New("session: not found")

// ErrInvalidToken is returned when a token does not look like one we
// could have issued (wrong length, not base64url). It is separated
// from [ErrNotFound] so callers can rate-limit on this signal.
var ErrInvalidToken = errors.New("session: invalid token")

// ErrDataTooLarge is returned by [Manager.Create] and [Manager.Regenerate]
// when the serialized session blob exceeds the configured [Manager.SetMaxDataSize]
// ceiling. The default ceiling is [DefaultMaxDataSize].
var ErrDataTooLarge = errors.New("session: data exceeds max size")

// DefaultMaxDataSize is the per-session blob ceiling enforced when the
// caller has not configured a custom limit via [Manager.SetMaxDataSize].
// 4 KiB is the same conservative bound most cookie stores adopt — it
// catches "someone put a megabyte of audit logs in the session" without
// getting in the way of normal use (factors, roles, preferences).
const DefaultMaxDataSize = 4 * 1024

// Session is the full session record returned from [Manager.Get]. The
// embedded Data map is whatever the caller passed at Create time, plus
// any future mutations.
type Session struct {
	// Token is the opaque session identifier. Callers should treat it
	// as a secret on par with the cookie value itself — never log it.
	Token string `json:"-"`

	// UserID is the principal this session authenticates. It is opaque
	// to this package; the auth layer chooses whether it's a numeric
	// ID, a UUID, or a stringified email.
	UserID string `json:"user_id"`

	// CreatedAt is when [Manager.Create] minted this session. It does
	// not change once set; it bounds the absolute lifetime of the
	// session against [Manager.Create]'s ttl argument.
	CreatedAt time.Time `json:"created_at"`

	// LastSeenAt is refreshed on every successful [Manager.Get]. It is
	// what the "Where you're logged in" admin page surfaces.
	LastSeenAt time.Time `json:"last_seen_at"`

	// AbsoluteExpiry is CreatedAt + the ttl argument passed to
	// [Manager.Create]. [Manager.Get] checks the current time against
	// this bound and returns [ErrNotFound] once it has passed,
	// independent of the rolling idle TTL. Persisting the bound in the
	// blob means the absolute deadline is enforced even when Redis's
	// own EXPIRE has been pushed forward by repeated Gets.
	AbsoluteExpiry time.Time `json:"absolute_expiry"`

	// Data is the arbitrary, JSON-serializable payload the caller
	// attached at Create time. It is round-tripped through encoding/json
	// so types degrade to their JSON equivalents (numbers → float64,
	// etc.) on the way out — that's a feature, not a bug, for an
	// opaque store.
	Data map[string]any `json:"data,omitempty"`
}

// SessionInfo is the lightweight projection returned from [Manager.List].
// It carries enough to render a "Where you're logged in" UI without
// shipping the entire Data payload back to the browser.
type SessionInfo struct {
	Token      string    `json:"token"`
	UserID     string    `json:"user_id"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
}

// Manager owns the Redis client and is safe for concurrent use by
// many goroutines. One [Manager] per process is the expected pattern.
type Manager struct {
	rdb *redis.Client
	log *slog.Logger
	now func() time.Time

	// maxDataSize is the per-session blob ceiling in bytes. Atomically
	// loaded/stored so [Manager.SetMaxDataSize] is safe to call on a
	// running manager (e.g. from a config-reload hook).
	maxDataSize atomic.Int64
}

// New opens a connection to Redis using cfg, pings it once, and
// returns a ready [Manager]. The constructor takes a [slog.Logger] so
// the manager can emit structured logs on background failures (e.g.
// JSON decode errors), but it never logs token values or session data.
//
// On failure the returned error wraps the underlying redis error with
// %w so callers can errors.Is/As as usual.
func New(ctx context.Context, cfg config.RedisConfig, logger *slog.Logger) (*Manager, error) {
	if cfg.URL == "" {
		return nil, errors.New("session: redis URL is required")
	}
	opt, err := redis.ParseURL(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("session: parse redis URL: %w", err)
	}
	if cfg.PoolSize > 0 {
		opt.PoolSize = cfg.PoolSize
	}
	if cfg.MinIdleConns > 0 {
		opt.MinIdleConns = cfg.MinIdleConns
	}
	if cfg.DialTimeout > 0 {
		opt.DialTimeout = cfg.DialTimeout
	}
	if cfg.ReadTimeout > 0 {
		opt.ReadTimeout = cfg.ReadTimeout
	}
	if cfg.WriteTimeout > 0 {
		opt.WriteTimeout = cfg.WriteTimeout
	}

	rdb := redis.NewClient(opt)
	if err := rdb.Ping(ctx).Err(); err != nil {
		// Don't leak a half-open client to callers.
		_ = rdb.Close()
		return nil, fmt.Errorf("session: ping redis: %w", err)
	}
	if logger == nil {
		logger = slog.Default()
	}
	m := &Manager{
		rdb: rdb,
		log: logger,
		now: time.Now,
	}
	m.maxDataSize.Store(DefaultMaxDataSize)
	return m, nil
}

// NewWithClient is the test/DI seam: it wraps an already-constructed
// [redis.Client] (or anything pretending to be one). The constructor
// does NOT ping; the caller has already proven the client is live.
func NewWithClient(rdb *redis.Client, logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	m := &Manager{
		rdb: rdb,
		log: logger,
		now: time.Now,
	}
	m.maxDataSize.Store(DefaultMaxDataSize)
	return m
}

// SetMaxDataSize overrides the per-session serialized-blob ceiling. A
// value <= 0 disables the cap. Concurrency-safe; call it at boot
// after [New] or from a config-reload hook.
func (m *Manager) SetMaxDataSize(n int) {
	m.maxDataSize.Store(int64(n))
}

// effectiveMaxDataSize returns the live ceiling, or -1 if the cap is
// disabled. We model "disabled" with the same sentinel rather than a
// branchy nil pointer.
func (m *Manager) effectiveMaxDataSize() int64 {
	v := m.maxDataSize.Load()
	if v <= 0 {
		return -1
	}
	return v
}

// Close releases the Redis connection pool. It is safe to call more
// than once.
func (m *Manager) Close() error {
	if m == nil || m.rdb == nil {
		return nil
	}
	return m.rdb.Close()
}

// sessionKey is the Redis key for a single session blob. We
// intentionally do NOT hash the token here — issue #131's scope spec
// keeps the layout simple ("session:{token}") and the token itself is
// already opaque, high-entropy, and never logged. The §5.1 doc-level
// SHA-256 wrapping is left for the auth-layer to apply at the
// boundary if/when needed.
func sessionKey(token string) string { return "session:" + token }

// userSessionsKey is the Redis SET that lets us enumerate or wipe all
// of a user's sessions without a SCAN.
func userSessionsKey(userID string) string { return "user_sessions:" + userID }

// Create mints a new session, persists it in Redis, and returns the
// opaque token. ttl is the absolute lifetime — once it elapses the
// session is gone regardless of activity. idleTTL is the rolling
// inactivity window: every successful [Manager.Get] refreshes the
// key's TTL to min(idleTTL, remaining absolute TTL).
//
// idleTTL must be positive and ≤ ttl. Passing data == nil is fine; an
// empty map is used.
func (m *Manager) Create(ctx context.Context, userID string, data map[string]any, ttl, idleTTL time.Duration) (string, error) {
	if userID == "" {
		return "", errors.New("session: userID is required")
	}
	if ttl <= 0 {
		return "", errors.New("session: ttl must be positive")
	}
	if idleTTL <= 0 || idleTTL > ttl {
		return "", errors.New("session: idleTTL must be positive and <= ttl")
	}

	token, err := generateToken()
	if err != nil {
		return "", err
	}
	now := m.now().UTC()
	sess := Session{
		UserID:         userID,
		CreatedAt:      now,
		LastSeenAt:     now,
		AbsoluteExpiry: now.Add(ttl),
		Data:           data,
	}
	if sess.Data == nil {
		sess.Data = map[string]any{}
	}
	blob, err := json.Marshal(sess)
	if err != nil {
		return "", fmt.Errorf("session: marshal: %w", err)
	}
	if cap := m.effectiveMaxDataSize(); cap >= 0 && int64(len(blob)) > cap {
		return "", fmt.Errorf("%w (%d > %d)", ErrDataTooLarge, len(blob), cap)
	}

	// The session blob's redis TTL is min(idleTTL, ttl) so the key
	// never lives past its absolute deadline even if no Get ever
	// fires to refresh it. (idleTTL <= ttl is already enforced above.)
	keyTTL := idleTTL
	if keyTTL > ttl {
		keyTTL = ttl
	}

	// MULTI/EXEC via a pipeline so the SET, SADD, and EXPIRE are one
	// round-trip and apply atomically. If the SADD fails after the SET,
	// the orphaned session blob will expire on its own; we don't roll
	// it back manually.
	pipe := m.rdb.TxPipeline()
	pipe.Set(ctx, sessionKey(token), blob, keyTTL)
	pipe.SAdd(ctx, userSessionsKey(userID), token)
	// The user-sessions set should outlive any single session in it,
	// so we bump its TTL up to this session's absolute ttl. We use
	// ExpireNX first to seed the TTL on a fresh set (Redis treats "no
	// TTL" as infinite, so a bare ExpireGT would refuse to set one),
	// then ExpireGT to extend only if our new ttl is longer than the
	// existing one. A second Create with a shorter ttl cannot demote
	// the existing window and orphan other live tokens from
	// List/DeleteAllForUser.
	pipe.ExpireNX(ctx, userSessionsKey(userID), ttl)
	pipe.ExpireGT(ctx, userSessionsKey(userID), ttl)
	if _, err := pipe.Exec(ctx); err != nil {
		return "", fmt.Errorf("session: persist: %w", err)
	}
	return token, nil
}

// refreshIfExistsScript writes the new session blob and resets its
// PEXPIRE only if the key still exists. This closes the
// GET-then-SET TOCTOU race in [Manager.Get]: if a Delete (or a
// DeleteAllForUser) lands between the initial read and the refresh,
// the script sees the key gone and the SET is a no-op — there is no
// way to resurrect a deleted session blob.
//
// KEYS[1]   session:{token}
// ARGV[1]   new blob
// ARGV[2]   PEXPIRE in milliseconds (string)
// Returns 1 if the refresh happened, 0 if the key was gone.
var refreshIfExistsScript = redis.NewScript(`
if redis.call("EXISTS", KEYS[1]) == 0 then
	return 0
end
redis.call("SET", KEYS[1], ARGV[1], "PX", tonumber(ARGV[2]))
return 1
`)

// Get loads the session for token. On every successful load the
// session's idle TTL is refreshed AND LastSeenAt is rewritten —
// atomically, via a Lua script that aborts if the key disappeared
// between the read and the write. If the token is missing, expired,
// or has crossed its absolute deadline, [ErrNotFound] is returned and
// the caller should clear the cookie.
//
// idleTTL is the same idle window passed at Create time, threaded
// through by the auth layer so the manager doesn't need to remember
// per-session policy.
func (m *Manager) Get(ctx context.Context, token string, idleTTL time.Duration) (Session, error) {
	if !validToken(token) {
		return Session{}, ErrInvalidToken
	}
	if idleTTL <= 0 {
		return Session{}, errors.New("session: idleTTL must be positive")
	}

	raw, err := m.rdb.Get(ctx, sessionKey(token)).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return Session{}, ErrNotFound
		}
		return Session{}, fmt.Errorf("session: get: %w", err)
	}
	// Guard against a runaway blob that snuck past an older, looser
	// cap. Reading a 50 MB session is itself an availability hazard.
	if cap := m.effectiveMaxDataSize(); cap >= 0 && int64(len(raw)) > cap {
		m.log.WarnContext(ctx, "session: blob exceeds max size, dropping",
			slog.Int("size", len(raw)), slog.Int64("cap", cap))
		_ = m.rdb.Del(ctx, sessionKey(token)).Err()
		return Session{}, ErrNotFound
	}
	var sess Session
	if err := json.Unmarshal(raw, &sess); err != nil {
		// A corrupt blob is unrecoverable. Drop it so the next request
		// from this cookie is cleanly ErrNotFound.
		m.log.WarnContext(ctx, "session: corrupt blob, dropping",
			slog.String("err", err.Error()))
		_ = m.rdb.Del(ctx, sessionKey(token)).Err()
		return Session{}, ErrNotFound
	}
	sess.Token = token

	// Enforce the absolute TTL bound. Even if the rolling idle window
	// has been pushed forward by repeated Gets, the session must die
	// at CreatedAt + ttl. We delete the session here so a subsequent
	// Get hits redis.Nil cleanly and so the user_sessions set doesn't
	// keep a dangling pointer past the bound.
	now := m.now().UTC()
	if !sess.AbsoluteExpiry.IsZero() && !now.Before(sess.AbsoluteExpiry) {
		if err := m.Delete(ctx, token); err != nil {
			m.log.WarnContext(ctx, "session: absolute-expiry cleanup failed",
				slog.String("err", err.Error()))
		}
		return Session{}, ErrNotFound
	}

	// Refresh LastSeenAt + idle TTL. The effective TTL for the session
	// key is min(idleTTL, time-until-absolute-expiry); past the
	// absolute bound the key MUST NOT live, even if idleTTL is
	// generous.
	sess.LastSeenAt = now
	keyTTL := idleTTL
	if !sess.AbsoluteExpiry.IsZero() {
		remaining := sess.AbsoluteExpiry.Sub(now)
		if remaining < keyTTL {
			keyTTL = remaining
		}
	}
	if keyTTL <= 0 {
		// Defensive: should be unreachable because we already checked
		// !now.Before(AbsoluteExpiry) above, but if the clock jitters
		// between checks we still want to return cleanly.
		_ = m.Delete(ctx, token)
		return Session{}, ErrNotFound
	}

	if blob, mErr := json.Marshal(sess); mErr == nil {
		// Atomic refresh: only writes if the key still exists, so a
		// concurrent Delete cannot be silently undone by this refresh.
		// We tolerate the refresh failing (network blip): we already
		// have a valid session to return. The next Get fixes it up.
		pxMs := keyTTL.Milliseconds()
		if pxMs < 1 {
			pxMs = 1
		}
		if _, sErr := refreshIfExistsScript.Run(ctx, m.rdb,
			[]string{sessionKey(token)}, blob, pxMs).Result(); sErr != nil && !errors.Is(sErr, redis.Nil) {
			m.log.WarnContext(ctx, "session: refresh failed",
				slog.String("err", sErr.Error()))
		}
		// Bump the user-sessions set's TTL to the remaining absolute
		// expiry of this session, but only if that's longer than the
		// existing window. ExpireGT alone is not enough if the set has
		// no TTL (Redis treats absent TTL as infinite, so GT refuses to
		// set one), so we pair it with ExpireNX as a seed. The result:
		// a short-idleTTL Get on a long-lived session can no longer
		// collapse the set's TTL and break List/DeleteAllForUser.
		// Separate from the Lua script: this key doesn't need the
		// resurrection guard (SREM/SADD elsewhere keep it honest).
		setTTL := keyTTL
		if !sess.AbsoluteExpiry.IsZero() {
			if r := sess.AbsoluteExpiry.Sub(now); r > setTTL {
				setTTL = r
			}
		}
		// EXPIRE is second-granularity in Redis; round up so we don't
		// trigger the go-redis "truncating to 1s" log line on
		// sub-second remainders.
		if setTTL < time.Second {
			setTTL = time.Second
		}
		setPipe := m.rdb.Pipeline()
		setPipe.ExpireNX(ctx, userSessionsKey(sess.UserID), setTTL)
		setPipe.ExpireGT(ctx, userSessionsKey(sess.UserID), setTTL)
		if _, eErr := setPipe.Exec(ctx); eErr != nil {
			m.log.WarnContext(ctx, "session: user_sessions ttl refresh failed",
				slog.String("err", eErr.Error()))
		}
	}
	return sess, nil
}

// regenerateScript atomically rotates a session token. It reads the
// existing blob at KEYS[1] (old session key), writes ARGV[2] (the new
// blob) at KEYS[2] (new session key) with PEXPIRE ARGV[3] ms, DELs
// the old key, and swaps SREM-old / SADD-new on KEYS[3] (the user's
// sessions set). ARGV[1] is the old token, ARGV[4] is the new token,
// ARGV[5] is the user-sessions set ExpireGT TTL in ms.
//
// Returning 0 from the script means "the old key was gone"; the Go
// wrapper translates that to ErrNotFound. Returning 1 means rotation
// succeeded.
var regenerateScript = redis.NewScript(`
local old = redis.call("GET", KEYS[1])
if not old then
	return 0
end
redis.call("SET", KEYS[2], ARGV[2], "PX", tonumber(ARGV[3]))
redis.call("DEL", KEYS[1])
redis.call("SREM", KEYS[3], ARGV[1])
redis.call("SADD", KEYS[3], ARGV[4])
-- Bump the user-sessions set's TTL: NX seeds it on a freshly-empty
-- set (otherwise EXPIRE GT refuses, treating "no TTL" as infinite),
-- and GT extends only when the new window is longer.
redis.call("EXPIRE", KEYS[3], tonumber(ARGV[5]), "NX")
redis.call("EXPIRE", KEYS[3], tonumber(ARGV[5]), "GT")
return 1
`)

// Regenerate rotates the opaque token while preserving the session's
// UserID, Data, and AbsoluteExpiry. Call it after any privilege
// escalation — successful login, MFA challenge passed, role promoted
// — to defeat session fixation: an attacker who pre-seeded the
// victim's cookie before login no longer holds a usable token.
//
// The rotation is atomic: the new blob is written, the old blob
// deleted, and the user-sessions set's membership swapped in a
// single Lua call. There is no window where both tokens are live or
// where the user-sessions set holds neither.
//
// idleTTL is the rolling idle window to apply to the new session;
// AbsoluteExpiry is preserved from the old session.
func (m *Manager) Regenerate(ctx context.Context, oldToken string, idleTTL time.Duration) (string, error) {
	if !validToken(oldToken) {
		return "", ErrInvalidToken
	}
	if idleTTL <= 0 {
		return "", errors.New("session: idleTTL must be positive")
	}

	raw, err := m.rdb.Get(ctx, sessionKey(oldToken)).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("session: regenerate get: %w", err)
	}
	var sess Session
	if err := json.Unmarshal(raw, &sess); err != nil {
		_ = m.rdb.Del(ctx, sessionKey(oldToken)).Err()
		return "", ErrNotFound
	}
	now := m.now().UTC()
	if !sess.AbsoluteExpiry.IsZero() && !now.Before(sess.AbsoluteExpiry) {
		_ = m.Delete(ctx, oldToken)
		return "", ErrNotFound
	}

	newToken, err := generateToken()
	if err != nil {
		return "", err
	}
	sess.LastSeenAt = now
	keyTTL := idleTTL
	if !sess.AbsoluteExpiry.IsZero() {
		remaining := sess.AbsoluteExpiry.Sub(now)
		if remaining < keyTTL {
			keyTTL = remaining
		}
	}
	if keyTTL <= 0 {
		_ = m.Delete(ctx, oldToken)
		return "", ErrNotFound
	}
	blob, err := json.Marshal(sess)
	if err != nil {
		return "", fmt.Errorf("session: marshal: %w", err)
	}
	if cap := m.effectiveMaxDataSize(); cap >= 0 && int64(len(blob)) > cap {
		return "", fmt.Errorf("%w (%d > %d)", ErrDataTooLarge, len(blob), cap)
	}

	pxMs := keyTTL.Milliseconds()
	if pxMs < 1 {
		pxMs = 1
	}
	// The user-sessions set's TTL should bracket the remaining
	// absolute lifetime of any session in it; pick the longer of the
	// new key's TTL and what's already there. EXPIRE GT keeps the max.
	setTTLSeconds := int64(keyTTL.Seconds())
	if setTTLSeconds < 1 {
		setTTLSeconds = 1
	}

	res, err := regenerateScript.Run(ctx, m.rdb,
		[]string{sessionKey(oldToken), sessionKey(newToken), userSessionsKey(sess.UserID)},
		oldToken, blob, pxMs, newToken, setTTLSeconds,
	).Int()
	if err != nil {
		return "", fmt.Errorf("session: regenerate: %w", err)
	}
	if res == 0 {
		return "", ErrNotFound
	}
	return newToken, nil
}

// deleteScript atomically deletes a session key and SREMs the token
// from the user-sessions set keyed by the userID embedded in the
// blob. Doing this in Lua means no race window where the blob is
// gone but the index still points at it, and no race where a
// concurrent Get refresh re-creates the blob between our GET and
// our DEL (the resurrection bug, fixed from the other direction).
//
// KEYS[1]   session:{token}
// KEYS[2]   user_sessions:{<resolved-from-blob>} — passed in by Go
// ARGV[1]   token
// We pass KEYS[2] from Go because Redis cluster mode forbids
// constructing keys inside a script.
//
// Returns 1 if the session existed and was deleted, 0 otherwise.
var deleteScript = redis.NewScript(`
if redis.call("EXISTS", KEYS[1]) == 0 then
	return 0
end
redis.call("DEL", KEYS[1])
if KEYS[2] ~= "" then
	redis.call("SREM", KEYS[2], ARGV[1])
end
return 1
`)

// Delete revokes a single session. It is idempotent: deleting a
// non-existent or already-expired token is not an error.
//
// Internally Delete uses a Lua script so the session-key DEL and the
// user-sessions SREM are guaranteed atomic relative to a concurrent
// Get refresh — the refresh's "only set if exists" guard cannot
// resurrect a session that was already deleted.
func (m *Manager) Delete(ctx context.Context, token string) error {
	if !validToken(token) {
		return ErrInvalidToken
	}
	// Resolve the userID so we can pass user_sessions:{uid} as a key
	// (Lua scripts can't compose keys from ARGV under cluster mode).
	// A missing blob is fine — we still run the script so an orphaned
	// set entry, if any, can be cleaned up by a follow-up SREM.
	var userKey string
	if raw, err := m.rdb.Get(ctx, sessionKey(token)).Bytes(); err == nil && len(raw) > 0 {
		var sess Session
		if jErr := json.Unmarshal(raw, &sess); jErr == nil && sess.UserID != "" {
			userKey = userSessionsKey(sess.UserID)
		}
	}
	if _, err := deleteScript.Run(ctx, m.rdb,
		[]string{sessionKey(token), userKey}, token,
	).Result(); err != nil && !errors.Is(err, redis.Nil) {
		return fmt.Errorf("session: delete: %w", err)
	}
	return nil
}

// DeleteAllForUser revokes every active session for userID. This is
// the "log me out everywhere" primitive — also the right thing to
// call after a password change or a role downgrade.
//
// Sessions whose absolute TTL already elapsed are silently skipped.
func (m *Manager) DeleteAllForUser(ctx context.Context, userID string) error {
	if userID == "" {
		return errors.New("session: userID is required")
	}
	tokens, err := m.rdb.SMembers(ctx, userSessionsKey(userID)).Result()
	if err != nil {
		return fmt.Errorf("session: list tokens: %w", err)
	}
	pipe := m.rdb.TxPipeline()
	for _, t := range tokens {
		pipe.Del(ctx, sessionKey(t))
	}
	pipe.Del(ctx, userSessionsKey(userID))
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("session: revoke all: %w", err)
	}
	return nil
}

// List returns a [SessionInfo] for each live session belonging to
// userID. The slice is unordered. Tokens whose session blobs have
// since expired are pruned from the user-sessions set as a side effect
// — this keeps the set from drifting unboundedly.
func (m *Manager) List(ctx context.Context, userID string) ([]SessionInfo, error) {
	if userID == "" {
		return nil, errors.New("session: userID is required")
	}
	tokens, err := m.rdb.SMembers(ctx, userSessionsKey(userID)).Result()
	if err != nil {
		return nil, fmt.Errorf("session: list tokens: %w", err)
	}
	if len(tokens) == 0 {
		return nil, nil
	}

	keys := make([]string, len(tokens))
	for i, t := range tokens {
		keys[i] = sessionKey(t)
	}
	vals, err := m.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("session: mget: %w", err)
	}

	out := make([]SessionInfo, 0, len(vals))
	var stale []string
	for i, v := range vals {
		if v == nil {
			stale = append(stale, tokens[i])
			continue
		}
		s, ok := v.(string)
		if !ok {
			stale = append(stale, tokens[i])
			continue
		}
		var sess Session
		if err := json.Unmarshal([]byte(s), &sess); err != nil {
			stale = append(stale, tokens[i])
			continue
		}
		out = append(out, SessionInfo{
			Token:      tokens[i],
			UserID:     sess.UserID,
			CreatedAt:  sess.CreatedAt,
			LastSeenAt: sess.LastSeenAt,
		})
	}
	if len(stale) > 0 {
		// Don't propagate cleanup errors — the caller asked for a list,
		// not a vacuum. Worst case the next call retries the SREM.
		if err := m.rdb.SRem(ctx, userSessionsKey(userID), toAny(stale)...).Err(); err != nil {
			m.log.WarnContext(ctx, "session: prune stale failed",
				slog.String("err", err.Error()))
		}
	}
	return out, nil
}

// toAny widens a []string to a []any so it can be passed to redis
// variadic interfaces like SRem(ctx, key, members...).
func toAny(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}
