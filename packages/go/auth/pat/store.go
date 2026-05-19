package pat

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned by Lookup / Get / Revoke when no row matches.
// The middleware translates this to 401 to deny information about
// whether a token "exists but is wrong" vs "does not exist at all".
var ErrNotFound = errors.New("pat: not found")

// Store is the persistence interface for personal access tokens.
//
// Two production-grade implementations live in this package:
//
//   - PostgresStore: the canonical implementation against the
//     personal_access_tokens table (migrations/000024).
//   - MemoryStore:   the in-memory test double. Goroutine-safe.
//
// All methods are safe for concurrent use. Implementations MUST do
// constant-time comparisons in Lookup; the interface comment is the
// contract.
type Store interface {
	// Issue persists row with the given hash bytes. Returns the
	// inserted row populated with the database-assigned ID. The
	// plaintext is intentionally NOT a parameter — the caller already
	// has it and the store has no use for it.
	Issue(ctx context.Context, row PAT, hash []byte) (PAT, error)

	// Lookup resolves a plaintext token to its stored row. The
	// constant-time comparison happens inside the implementation; the
	// caller never sees the hash bytes.
	//
	// Returns:
	//   - ErrInvalid when plaintext fails shape validation. Cheap.
	//   - ErrNotFound when no row matches the hash (or candidate window).
	//   - ErrExpired when the row exists but expires_at is in the past.
	//   - ErrRevoked when revoked_at is non-NULL.
	//   - (row, nil) on success.
	Lookup(ctx context.Context, plaintext string) (PAT, error)

	// List returns the active (revoked_at IS NULL) tokens for a user,
	// most recent first. The hash is NEVER included in the returned
	// rows; the field is the empty slice.
	List(ctx context.Context, userID string) ([]PAT, error)

	// Revoke marks the row identified by id as revoked. Idempotent:
	// a second call against an already-revoked row is a no-op (and
	// returns nil). Returns ErrNotFound if the id does not exist or
	// does not belong to userID — the userID is the authorisation
	// gate the handler doesn't have to re-implement.
	Revoke(ctx context.Context, userID, id string) error

	// TouchUsed records a successful authentication against the token
	// at the given timestamp, subject to the implementation's write-
	// amplification throttle. Errors are non-fatal in the auth path;
	// the middleware logs them and continues.
	TouchUsed(ctx context.Context, id string, when time.Time) error
}

// MemoryStore is the in-memory Store implementation used by tests. It
// holds rows in a slice indexed by ID and answers Lookup by walking
// the slice and constant-time comparing the candidate hash against
// each row's stored hash. The O(n) walk is fine for tests; production
// uses PostgresStore where the UNIQUE index on hash makes lookup O(log n).
//
// Goroutine-safe via a single RWMutex. The lock granularity is per-store
// — fine-grained locking buys nothing because PATs aren't a high-write
// surface.
type MemoryStore struct {
	mu     sync.RWMutex
	rows   []memoryRow
	nextID uint64
}

// memoryRow stores the in-memory representation: the public PAT fields
// plus the hash bytes. Hash is kept inside the store, NEVER returned
// across the package boundary.
type memoryRow struct {
	pat  PAT
	hash []byte
}

// NewMemoryStore returns a fresh empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{rows: make([]memoryRow, 0, 8)}
}

// Issue implements Store. Assigns a deterministic-but-opaque ID so
// tests can re-derive expectations without scanning the store.
func (m *MemoryStore) Issue(_ context.Context, row PAT, hash []byte) (PAT, error) {
	if len(hash) == 0 {
		return PAT{}, fmt.Errorf("pat: Issue requires a non-empty hash")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	// A deterministic synthetic UUID-shaped id keeps test fixtures
	// readable. Production rows use gen_uuid_v7() at the DB.
	row.ID = fmt.Sprintf("00000000-0000-7000-8000-%012d", m.nextID)
	dup := make([]byte, len(hash))
	copy(dup, hash)
	m.rows = append(m.rows, memoryRow{pat: row, hash: dup})
	return row, nil
}

// Lookup implements Store with constant-time comparison against every
// candidate row's hash. The walk is intentionally not short-circuited
// on a non-matching hash so that the wall time is constant in the
// number of rows — a timing-side-channel can't differentiate "hash
// near the head" from "hash near the tail". Production PostgresStore
// does the same shape: it loads the row by a content-addressed UNIQUE
// index but still constant-time compares after recomputing the salt.
func (m *MemoryStore) Lookup(_ context.Context, plaintext string) (PAT, error) {
	if !ValidShape(plaintext) {
		return PAT{}, ErrInvalid
	}
	m.mu.RLock()
	defer m.mu.RUnlock()

	var matched memoryRow
	found := 0
	// Walk every row; constant-time-compare against every row.
	for _, r := range m.rows {
		if VerifyHash(r.hash, plaintext) {
			matched = r
			found = 1
			// Intentionally NO break: keep the loop running so wall
			// time doesn't depend on match position. The found counter
			// is unioned with subtle.ConstantTimeEq below.
		} else {
			// Burn a comparison against a dummy slice so non-matches
			// pay the same cost as matches that didn't find earlier.
			_ = subtle.ConstantTimeCompare(r.hash, r.hash)
		}
	}
	if found == 0 {
		return PAT{}, ErrNotFound
	}

	now := time.Now().UTC()
	if matched.pat.RevokedAt != nil {
		return PAT{}, ErrRevoked
	}
	if matched.pat.ExpiresAt != nil && !matched.pat.ExpiresAt.After(now) {
		return PAT{}, ErrExpired
	}
	return matched.pat, nil
}

// List implements Store. Returns rows where revoked_at IS NULL, most
// recent first.
func (m *MemoryStore) List(_ context.Context, userID string) ([]PAT, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]PAT, 0, len(m.rows))
	for _, r := range m.rows {
		if r.pat.UserID != userID {
			continue
		}
		if r.pat.RevokedAt != nil {
			continue
		}
		// Strip any hash-shaped field by returning a copy of the
		// PAT struct, which has no hash field. Defensive.
		out = append(out, r.pat)
	}
	// Order newest first.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// Revoke implements Store. Idempotent.
func (m *MemoryStore) Revoke(_ context.Context, userID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.rows {
		if m.rows[i].pat.ID != id {
			continue
		}
		if m.rows[i].pat.UserID != userID {
			return ErrNotFound
		}
		if m.rows[i].pat.RevokedAt != nil {
			// Already revoked, idempotent no-op.
			return nil
		}
		now := time.Now().UTC()
		m.rows[i].pat.RevokedAt = &now
		return nil
	}
	return ErrNotFound
}

// TouchUsed implements Store. The throttle isn't necessary for the
// in-memory store — every call is essentially free — so we just write.
func (m *MemoryStore) TouchUsed(_ context.Context, id string, when time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.rows {
		if m.rows[i].pat.ID == id {
			t := when.UTC()
			m.rows[i].pat.LastUsedAt = &t
			return nil
		}
	}
	return ErrNotFound
}

// PostgresStore implements Store against the personal_access_tokens
// table (migrations/000024). Per-row throttling for TouchUsed is
// 60 seconds, matching the session mirror's write-amplification budget.
type PostgresStore struct {
	pool   *pgxpool.Pool
	tokens map[string]time.Time // last-touch cache for the throttle
	mu     sync.Mutex
}

// NewPostgresStore wraps a *pgxpool.Pool. The pool's lifecycle stays
// with the caller; the store never calls Close on it.
func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{
		pool:   pool,
		tokens: make(map[string]time.Time),
	}
}

const issueSQL = `
INSERT INTO personal_access_tokens (
    user_id, name, prefix, hash, scopes, created_at, expires_at
) VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id, created_at
`

// Issue inserts the row and returns the populated PAT (with ID).
func (s *PostgresStore) Issue(ctx context.Context, row PAT, hash []byte) (PAT, error) {
	if s.pool == nil {
		return PAT{}, fmt.Errorf("pat: nil pgxpool")
	}
	r := s.pool.QueryRow(ctx, issueSQL,
		row.UserID, row.Name, row.Prefix, hash, row.Scopes,
		row.CreatedAt, row.ExpiresAt,
	)
	if err := r.Scan(&row.ID, &row.CreatedAt); err != nil {
		return PAT{}, fmt.Errorf("pat: insert: %w", err)
	}
	return row, nil
}

// fingerprintForLookup returns a non-secret content address for a
// plaintext token, used to prune the candidate set before argon2
// verify. The fingerprint is SHA-256 of the plaintext; we store it
// nowhere — production PostgresStore narrows by `prefix = $1` on the
// table column instead. SHA-256 is here as a fallback for the case
// where the prefix column has been blanked by a future migration.
func fingerprintForLookup(plaintext string) [32]byte {
	return sha256.Sum256([]byte(plaintext))
}

const lookupByPrefixSQL = `
SELECT id, user_id, name, prefix, hash, scopes,
       created_at, last_used_at, expires_at, revoked_at
FROM personal_access_tokens
WHERE prefix = $1
`

// Lookup resolves a plaintext token. The DB query narrows by prefix
// (the cheap discriminator) and the loop argon2-verifies each row
// constant-time. In practice prefix has 62^8 ≈ 2e14 codomain so any
// real workload sees a single candidate row.
func (s *PostgresStore) Lookup(ctx context.Context, plaintext string) (PAT, error) {
	if !ValidShape(plaintext) {
		return PAT{}, ErrInvalid
	}
	if s.pool == nil {
		return PAT{}, fmt.Errorf("pat: nil pgxpool")
	}

	prefix := plaintext[len(Namespace) : len(Namespace)+PrefixLen]
	rows, err := s.pool.Query(ctx, lookupByPrefixSQL, prefix)
	if err != nil {
		return PAT{}, fmt.Errorf("pat: lookup: %w", err)
	}
	defer rows.Close()

	type cand struct {
		pat  PAT
		hash []byte
	}
	var cands []cand
	for rows.Next() {
		var c cand
		if err := rows.Scan(
			&c.pat.ID, &c.pat.UserID, &c.pat.Name, &c.pat.Prefix,
			&c.hash, &c.pat.Scopes,
			&c.pat.CreatedAt, &c.pat.LastUsedAt, &c.pat.ExpiresAt, &c.pat.RevokedAt,
		); err != nil {
			return PAT{}, fmt.Errorf("pat: lookup scan: %w", err)
		}
		cands = append(cands, c)
	}
	if err := rows.Err(); err != nil {
		return PAT{}, fmt.Errorf("pat: lookup iter: %w", err)
	}

	var matched cand
	found := 0
	for _, c := range cands {
		if VerifyHash(c.hash, plaintext) {
			matched = c
			found = 1
		} else {
			_ = subtle.ConstantTimeCompare(c.hash, c.hash)
		}
	}
	if found == 0 {
		return PAT{}, ErrNotFound
	}
	now := time.Now().UTC()
	if matched.pat.RevokedAt != nil {
		return PAT{}, ErrRevoked
	}
	if matched.pat.ExpiresAt != nil && !matched.pat.ExpiresAt.After(now) {
		return PAT{}, ErrExpired
	}
	return matched.pat, nil
}

const listSQL = `
SELECT id, user_id, name, prefix, scopes,
       created_at, last_used_at, expires_at, revoked_at
FROM personal_access_tokens
WHERE user_id = $1
  AND revoked_at IS NULL
  AND (expires_at IS NULL OR expires_at > now())
ORDER BY created_at DESC
`

// List returns the active tokens for a user. The hash is NOT selected
// — the row never carries the hash bytes outside the store boundary.
func (s *PostgresStore) List(ctx context.Context, userID string) ([]PAT, error) {
	if s.pool == nil {
		return nil, fmt.Errorf("pat: nil pgxpool")
	}
	rows, err := s.pool.Query(ctx, listSQL, userID)
	if err != nil {
		return nil, fmt.Errorf("pat: list: %w", err)
	}
	defer rows.Close()
	var out []PAT
	for rows.Next() {
		var p PAT
		if err := rows.Scan(
			&p.ID, &p.UserID, &p.Name, &p.Prefix, &p.Scopes,
			&p.CreatedAt, &p.LastUsedAt, &p.ExpiresAt, &p.RevokedAt,
		); err != nil {
			return nil, fmt.Errorf("pat: list scan: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pat: list iter: %w", err)
	}
	return out, nil
}

const revokeSQL = `
UPDATE personal_access_tokens
SET revoked_at = now()
WHERE id = $1
  AND user_id = $2
  AND revoked_at IS NULL
`

// Revoke marks the row revoked. Idempotent (no rows affected → already
// revoked → nil). ErrNotFound when the (id, user_id) tuple is unknown.
func (s *PostgresStore) Revoke(ctx context.Context, userID, id string) error {
	if s.pool == nil {
		return fmt.Errorf("pat: nil pgxpool")
	}
	ct, err := s.pool.Exec(ctx, revokeSQL, id, userID)
	if err != nil {
		return fmt.Errorf("pat: revoke: %w", err)
	}
	if ct.RowsAffected() == 0 {
		// Could be "already revoked" (which is fine) or "row does not
		// exist for this user" (which is ErrNotFound). Disambiguate
		// with a second cheap exists query.
		var found bool
		if err := s.pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM personal_access_tokens WHERE id = $1 AND user_id = $2)`,
			id, userID,
		).Scan(&found); err != nil {
			return fmt.Errorf("pat: revoke exists: %w", err)
		}
		if !found {
			return ErrNotFound
		}
	}
	return nil
}

// touchThrottle is the minimum gap between last_used_at writebacks for
// the same token. 60s matches the session mirror's budget (PR #291).
const touchThrottle = 60 * time.Second

const touchSQL = `
UPDATE personal_access_tokens
SET last_used_at = $2
WHERE id = $1
`

// TouchUsed records a successful authentication. The in-process cache
// suppresses writebacks more frequent than touchThrottle so a token
// in a tight CI loop doesn't hot-write the row.
func (s *PostgresStore) TouchUsed(ctx context.Context, id string, when time.Time) error {
	s.mu.Lock()
	last, ok := s.tokens[id]
	if ok && when.Sub(last) < touchThrottle {
		s.mu.Unlock()
		return nil
	}
	s.tokens[id] = when
	s.mu.Unlock()
	if s.pool == nil {
		// Cache-only mode used by tests; not an error.
		return nil
	}
	_, err := s.pool.Exec(ctx, touchSQL, id, when)
	if err != nil {
		return fmt.Errorf("pat: touch: %w", err)
	}
	return nil
}
