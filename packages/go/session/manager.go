package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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
	return &Manager{
		rdb: rdb,
		log: logger,
		now: time.Now,
	}, nil
}

// NewWithClient is the test/DI seam: it wraps an already-constructed
// [redis.Client] (or anything pretending to be one). The constructor
// does NOT ping; the caller has already proven the client is live.
func NewWithClient(rdb *redis.Client, logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	return &Manager{
		rdb: rdb,
		log: logger,
		now: time.Now,
	}
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
		UserID:     userID,
		CreatedAt:  now,
		LastSeenAt: now,
		Data:       data,
	}
	if sess.Data == nil {
		sess.Data = map[string]any{}
	}
	blob, err := json.Marshal(sess)
	if err != nil {
		return "", fmt.Errorf("session: marshal: %w", err)
	}

	// MULTI/EXEC via a pipeline so the SET, SADD, and EXPIRE are one
	// round-trip and apply atomically. If the SADD fails after the SET,
	// the orphaned session blob will expire on its own; we don't roll
	// it back manually.
	pipe := m.rdb.TxPipeline()
	pipe.Set(ctx, sessionKey(token), blob, idleTTL)
	pipe.SAdd(ctx, userSessionsKey(userID), token)
	// The user-sessions set should outlive any single session in it,
	// so we expire it at the absolute TTL of this session. A later
	// Create extends it again.
	pipe.Expire(ctx, userSessionsKey(userID), ttl)
	if _, err := pipe.Exec(ctx); err != nil {
		return "", fmt.Errorf("session: persist: %w", err)
	}
	return token, nil
}

// Get loads the session for token. On every successful load the
// session's idle TTL is refreshed AND LastSeenAt is rewritten — two
// writes wrapped in a single pipeline. If the token is missing or
// expired, [ErrNotFound] is returned and the caller should clear the
// cookie.
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

	// Refresh LastSeenAt + idle TTL. We tolerate the refresh failing
	// (network blip): we already have a valid session to return. The
	// next Get will fix it up.
	sess.LastSeenAt = m.now().UTC()
	if blob, mErr := json.Marshal(sess); mErr == nil {
		pipe := m.rdb.TxPipeline()
		pipe.Set(ctx, sessionKey(token), blob, idleTTL)
		// If the user-sessions set's TTL has drifted shorter than the
		// session's, bump it back to at least the idle window. This
		// keeps List/DeleteAllForUser working until the absolute TTL.
		pipe.Expire(ctx, userSessionsKey(sess.UserID), idleTTL)
		if _, eErr := pipe.Exec(ctx); eErr != nil {
			m.log.WarnContext(ctx, "session: refresh failed",
				slog.String("err", eErr.Error()))
		}
	}
	return sess, nil
}

// Delete revokes a single session. It is idempotent: deleting a
// non-existent or already-expired token is not an error.
func (m *Manager) Delete(ctx context.Context, token string) error {
	if !validToken(token) {
		return ErrInvalidToken
	}
	// Best-effort: fetch the userID so we can prune the user-sessions
	// set. If the session is already gone we still try the SREM with
	// the empty user-sessions key, which is a no-op.
	raw, _ := m.rdb.Get(ctx, sessionKey(token)).Bytes()
	pipe := m.rdb.TxPipeline()
	pipe.Del(ctx, sessionKey(token))
	if len(raw) > 0 {
		var sess Session
		if err := json.Unmarshal(raw, &sess); err == nil && sess.UserID != "" {
			pipe.SRem(ctx, userSessionsKey(sess.UserID), token)
		}
	}
	if _, err := pipe.Exec(ctx); err != nil {
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
