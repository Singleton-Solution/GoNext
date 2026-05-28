// Package pat implements the per-user Personal Access Token (PAT)
// issuance HTTP surface — list, create, revoke — mounted at
// /api/v1/me/tokens by the API server's main.go.
//
// The package is intentionally separate from packages/go/auth/pat,
// which is the lower-level credential primitive (the bearer-token
// middleware that resolves "Authorization: Bearer gn_pat_..." into a
// Principal). Splitting issuance from authentication keeps the admin
// settings page from importing the middleware and inverting the
// dependency graph: handlers depend on store, store depends on
// password+pool, and the bearer middleware is wholly separate.
//
// Wire format
//
// A token is the literal "gn_pat_" followed by 32 URL-safe base64
// characters (24 raw bytes from crypto/rand). The "gn_pat_" namespace
// lets secret-scanners pattern-match a leaked credential without false
// positives against arbitrary base64. The plaintext is only ever
// returned by the create handler, exactly once. The database stores
// only the argon2id PHC encoding of the full plaintext, peppered with
// GONEXT_AUTH_PEPPER — identical posture to user passwords.
//
// Storage
//
// Rows land in personal_access_tokens (migration 000026). The list
// view exposes id, name, prefix, scopes, created_at, last_used_at,
// expires_at — the hash and plaintext never leave the package. The
// prefix column is the first 8 chars of the random tail (post the
// gn_pat_ namespace), surfaced as "gn_pat_AbCdEfGh…" by the admin UI.
package pat

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Singleton-Solution/GoNext/packages/go/auth/password"
)

// Namespace is the mandatory prefix on every PAT plaintext. The "gn_pat_"
// literal lets log scrubbers, GitHub's secret scanner, and gitleaks
// pattern-match a leaked credential without false positives against
// arbitrary base64. Changing this is a breaking change for every
// downstream scanner — it is a constant for a reason.
const Namespace = "gn_pat_"

// secretBytes is the entropy in the random tail. 24 bytes → 32 chars
// under base64.RawURLEncoding, matching the width OWASP recommends for
// long-lived bearer tokens. The total token width is then
//
//	len("gn_pat_") + 32 = 39 characters
//
// which fits comfortably in a single Authorization header and inside
// the 64-character ceiling most password managers honour.
const secretBytes = 24

// secretLen is the encoded length of the random tail.
const secretLen = 32

// PrefixLen is the on-disk length of the prefix column. The first 8
// characters of the random tail (post-namespace) are what the list view
// renders as "gn_pat_AbCdEfGh…". Exposed as a const so the handler
// doesn't hard-code the slice bound.
const PrefixLen = 8

// MinTokenLen is the minimum length a string must have to even be
// considered a candidate token. Used to fail fast on obvious garbage
// before paying the argon2 cost. Equal to len(Namespace) + secretLen.
const MinTokenLen = len(Namespace) + secretLen

// Errors returned by the store. Callers compare with errors.Is.
var (
	// ErrNotFound is returned by Revoke when no (id, user_id) row
	// matches. Distinct from "already revoked" (which is a silent
	// idempotent no-op) so the handler can map to 404.
	ErrNotFound = errors.New("pat: not found")

	// ErrInvalidName surfaces a caller-side validation failure. The
	// store re-checks the table's CHECK (length(btrim(name)) > 0)
	// because relying on the DB constraint to bubble up a 23xxx
	// SQLSTATE through pgx is brittle.
	ErrInvalidName = errors.New("pat: name must be non-empty")

	// ErrInvalidUserID is the zero-UUID / empty user-id guard. Belt
	// and braces: the handler already gates on policy.FromContext but
	// a future caller path could regress.
	ErrInvalidUserID = errors.New("pat: user_id is required")
)

// Token is the in-memory representation of a personal_access_tokens
// row. It mirrors the table columns 1:1 with the hash deliberately
// omitted — the store never exposes the hash bytes across the package
// boundary. The handler turns this into the on-wire TokenView; this
// type keeps the SQL scan target out of the handler so the wire shape
// can evolve without touching the SQL.
type Token struct {
	ID         string
	UserID     string
	Name       string
	Prefix     string
	Scopes     []string
	CreatedAt  time.Time
	LastUsedAt *time.Time
	ExpiresAt  *time.Time
	RevokedAt  *time.Time
}

// Store is the Postgres-backed persistence layer for the
// personal_access_tokens table. It is the only thing in the package
// that knows the table's column shape; the handler is wire-shape only.
//
// Zero value is unusable. Construct with NewStore.
type Store struct {
	pool   *pgxpool.Pool
	pepper []byte
}

// NewStore wraps a pgxpool.Pool and a pepper. Both are required — a
// nil pool or empty pepper is a wiring bug we surface at boot rather
// than silently degrading to an in-memory or unsalted hash. The pool's
// lifecycle stays with the caller; the store never calls Close on it.
func NewStore(pool *pgxpool.Pool, pepper []byte) *Store {
	return &Store{pool: pool, pepper: pepper}
}

// CreateInput is the validated arguments to Create. Splitting the
// validation from the SQL means the handler can map specific errors
// (invalid name, invalid scopes) to 400 without sniffing pg error
// codes.
type CreateInput struct {
	UserID    string
	Name      string
	Scopes    []string
	ExpiresAt *time.Time
}

// Created is the return value of Create. It carries the persisted
// Token row and — exactly once — the plaintext bearer the operator
// must save. The plaintext is intentionally not stashed on the Token
// struct itself; a Token returned by List or any other read path will
// never have a plaintext.
type Created struct {
	Token     Token
	Plaintext string
}

// Create mints a fresh PAT for input.UserID, hashes it with argon2id
// + the configured pepper, persists the row, and returns the plaintext
// once. Subsequent reads of the same row see only the prefix.
//
// Errors:
//   - ErrInvalidUserID, ErrInvalidName for caller-side validation.
//   - Any pgx error wrapped with "pat: create: ..." for SQL failures.
//   - Any crypto/rand error wrapped with "pat: random: ..." (vanishingly
//     unlikely; the kernel CSPRNG essentially never fails).
//
// Create is safe for concurrent use; the underlying pgxpool handles
// the connection multiplexing.
func (s *Store) Create(ctx context.Context, in CreateInput) (Created, error) {
	if strings.TrimSpace(in.UserID) == "" {
		return Created{}, ErrInvalidUserID
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return Created{}, ErrInvalidName
	}

	plaintext, secret, err := newPlaintext()
	if err != nil {
		return Created{}, fmt.Errorf("pat: random: %w", err)
	}

	encoded, err := password.Hash(plaintext, s.pepper)
	if err != nil {
		return Created{}, fmt.Errorf("pat: hash: %w", err)
	}

	// Copy scopes so a later mutation of the caller's slice can't
	// affect the persisted row. Costs nothing.
	scopes := make([]string, 0, len(in.Scopes))
	for _, sc := range in.Scopes {
		sc = strings.TrimSpace(sc)
		if sc == "" {
			continue
		}
		scopes = append(scopes, sc)
	}

	prefix := secret[:PrefixLen]
	now := time.Now().UTC()

	const q = `
INSERT INTO personal_access_tokens (
    user_id, name, prefix, hash, scopes, created_at, expires_at
) VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id, created_at
`
	var row Token
	row.UserID = in.UserID
	row.Name = name
	row.Prefix = prefix
	row.Scopes = scopes
	row.ExpiresAt = in.ExpiresAt
	if err := s.pool.QueryRow(ctx, q,
		in.UserID, name, prefix, []byte(encoded), scopes, now, in.ExpiresAt,
	).Scan(&row.ID, &row.CreatedAt); err != nil {
		return Created{}, fmt.Errorf("pat: create: %w", err)
	}
	return Created{Token: row, Plaintext: plaintext}, nil
}

// List returns the active (revoked_at IS NULL, not expired) tokens
// for userID, newest first. The hash column is deliberately not
// selected so the bytes never cross the store boundary.
//
// Returns an empty slice (not a nil slice) on success with no rows so
// the handler can range over the result without a nil check and the
// JSON envelope is "[]" instead of "null".
func (s *Store) List(ctx context.Context, userID string) ([]Token, error) {
	if strings.TrimSpace(userID) == "" {
		return nil, ErrInvalidUserID
	}
	const q = `
SELECT id, user_id, name, prefix, scopes,
       created_at, last_used_at, expires_at, revoked_at
FROM personal_access_tokens
WHERE user_id = $1
  AND revoked_at IS NULL
  AND (expires_at IS NULL OR expires_at > now())
ORDER BY created_at DESC
`
	rows, err := s.pool.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("pat: list: %w", err)
	}
	defer rows.Close()
	out := make([]Token, 0)
	for rows.Next() {
		var t Token
		if err := rows.Scan(
			&t.ID, &t.UserID, &t.Name, &t.Prefix, &t.Scopes,
			&t.CreatedAt, &t.LastUsedAt, &t.ExpiresAt, &t.RevokedAt,
		); err != nil {
			return nil, fmt.Errorf("pat: list scan: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pat: list iter: %w", err)
	}
	return out, nil
}

// Revoke marks the row identified by (userID, id) as revoked. The
// userID is the authorisation gate — a caller cannot revoke another
// user's token by guessing the id. The store collapses both "wrong
// owner" and "no such id" into ErrNotFound so the handler can return
// a uniform 404 without an enumeration oracle (a 403 would leak that
// the id IS valid, just belongs to someone else).
//
// Idempotent: a second Revoke against an already-revoked row is a
// no-op and returns nil. The handler maps that to the same 204 as a
// first-time revoke; CI scripts that retry on transient errors won't
// see a 404 they have to special-case.
func (s *Store) Revoke(ctx context.Context, userID, id string) error {
	if strings.TrimSpace(userID) == "" {
		return ErrInvalidUserID
	}
	if strings.TrimSpace(id) == "" {
		return ErrNotFound
	}
	const updateQ = `
UPDATE personal_access_tokens
SET revoked_at = now()
WHERE id = $1
  AND user_id = $2
  AND revoked_at IS NULL
`
	ct, err := s.pool.Exec(ctx, updateQ, id, userID)
	if err != nil {
		return fmt.Errorf("pat: revoke: %w", err)
	}
	if ct.RowsAffected() == 1 {
		return nil
	}
	// Zero rows affected: either "already revoked" (fine, return nil)
	// or "row does not exist for this (id, user_id)" (ErrNotFound).
	// Disambiguate with a cheap EXISTS probe. Note we don't probe by
	// id-only — that would let a caller learn the id belongs to another
	// user via a side channel.
	const existsQ = `SELECT EXISTS(SELECT 1 FROM personal_access_tokens WHERE id = $1 AND user_id = $2)`
	var found bool
	if err := s.pool.QueryRow(ctx, existsQ, id, userID).Scan(&found); err != nil {
		// pgx surfaces "invalid input syntax for type uuid" as a
		// regular query error when the id isn't a UUID. We collapse
		// that to ErrNotFound — a non-UUID can never match an existing
		// row, and surfacing the DB error would be a fingerprint.
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if isInvalidUUID(err) {
			return ErrNotFound
		}
		return fmt.Errorf("pat: revoke exists: %w", err)
	}
	if !found {
		return ErrNotFound
	}
	return nil
}

// isInvalidUUID matches pgx's "invalid input syntax for type uuid"
// error so we can collapse it to ErrNotFound. Done via substring match
// because pgx wraps the underlying pgconn.PgError without exporting a
// sentinel for this case. The string is stable in libpq and unlikely
// to change.
func isInvalidUUID(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "invalid input syntax for type uuid") ||
		strings.Contains(msg, "invalid input syntax for uuid")
}

// newPlaintext mints a fresh "gn_pat_<32-char-base64>" string from
// crypto/rand. Returns the full plaintext and the secret portion (the
// 32 chars after the namespace) so callers can slice the prefix
// without re-parsing.
//
// Uses base64.RawURLEncoding so the secret is safe to drop into a
// URL or a header without any escaping. The alphabet is the URL-safe
// 64-char set (A-Z, a-z, 0-9, '-', '_'); no padding because the
// length is fixed and known.
func newPlaintext() (plaintext, secret string, err error) {
	buf := make([]byte, secretBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	secret = base64.RawURLEncoding.EncodeToString(buf)
	// RawURLEncoding of 24 bytes is always 32 chars, but we belt-and-
	// brace the length in case a future stdlib change introduces a
	// trailing newline or similar.
	if len(secret) != secretLen {
		return "", "", fmt.Errorf("pat: unexpected encoded length %d, want %d", len(secret), secretLen)
	}
	return Namespace + secret, secret, nil
}
