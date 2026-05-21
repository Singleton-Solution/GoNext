package setup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

// MemoryUserCreator is an in-memory UserCreator used by the test suite
// and by the smoke-test wiring in dev mode. It is NOT a production
// adapter — a real install touches the Postgres `users` and
// `user_passwords` tables transactionally; the production adapter lives
// next to the rest of the user persistence (TODO once that package
// lands).
//
// Concurrency: the underlying map is guarded by a mutex; concurrent
// callers can safely Create different users.
type MemoryUserCreator struct {
	mu    sync.Mutex
	users map[string]UserCreateInput // user_id -> input
}

// NewMemoryUserCreator returns an empty creator.
func NewMemoryUserCreator() *MemoryUserCreator {
	return &MemoryUserCreator{users: map[string]UserCreateInput{}}
}

// Create stores in under a freshly minted UUID-like string and returns
// the id. If a row with the same lower-case email already exists we
// return ErrEmailExists so callers can render the right copy.
func (m *MemoryUserCreator) Create(_ context.Context, in UserCreateInput) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, existing := range m.users {
		if existing.Email == in.Email {
			return "", ErrEmailExists
		}
	}

	id, err := newID()
	if err != nil {
		return "", err
	}
	m.users[id] = in
	return id, nil
}

// Len returns the number of stored users. Tests use it to assert the
// "second install rejected" path didn't leave a partial row behind.
func (m *MemoryUserCreator) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.users)
}

// ErrEmailExists is returned by MemoryUserCreator.Create when the
// supplied email is already present. Production adapters surface
// equivalent semantics via the citext UNIQUE constraint.
var ErrEmailExists = errors.New("setup: email already exists")

// MemoryOptionStore is the in-memory companion. Has reports key
// presence; Write stores the value as-is. A nil-value write is treated
// the same as a write of any concrete type — only "has the key ever
// been written?" matters to the install lock.
type MemoryOptionStore struct {
	mu     sync.RWMutex
	values map[string]any
}

// NewMemoryOptionStore returns an empty store.
func NewMemoryOptionStore() *MemoryOptionStore {
	return &MemoryOptionStore{values: map[string]any{}}
}

// Has reports whether key has ever been written.
func (m *MemoryOptionStore) Has(_ context.Context, key string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.values[key]
	return ok, nil
}

// Write persists value under key. Idempotent.
func (m *MemoryOptionStore) Write(_ context.Context, key string, value any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.values[key] = value
	return nil
}

// Get exposes the stored value for assertion. Returns (value, true) if
// present, (nil, false) otherwise.
func (m *MemoryOptionStore) Get(key string) (any, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.values[key]
	return v, ok
}

// MemorySession is the in-memory SessionCreator used by tests. It mints
// random hex strings rather than going through the real session.Manager
// — that lets the test suite run without a Redis container while still
// asserting the cookie is set with a non-empty value.
type MemorySession struct {
	mu    sync.Mutex
	rows  map[string]string // token -> userID
}

// NewMemorySession returns an empty creator.
func NewMemorySession() *MemorySession {
	return &MemorySession{rows: map[string]string{}}
}

// Create implements SessionCreator with a random hex token. The
// ttl / idleTTL arguments are ignored — the fake doesn't expire
// anything; tests that care about expiry pin Now via Deps.
func (s *MemorySession) Create(_ context.Context, userID string, _ map[string]any, _, _ time.Duration) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tok, err := newID()
	if err != nil {
		return "", err
	}
	s.rows[tok] = userID
	return tok, nil
}

// Token returns the stored userID for a token, or "" if unknown. Tests
// use it to assert the cookie value round-trips a real session.
func (s *MemorySession) Token(token string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rows[token]
}

// newID produces a 16-byte hex string. It's used both for synthetic
// user IDs and for session tokens — neither is a security boundary at
// this tier (the real persistence layer mints UUID v7, the real session
// manager uses base64url 32-byte tokens), but the value must be
// non-empty and globally unique within a single test run.
func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
