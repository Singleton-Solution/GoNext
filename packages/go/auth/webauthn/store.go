package webauthn

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"time"

	gwa "github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
)

// ErrNotFound is the sentinel returned by Store.GetByCredentialID when
// the supplied credential id doesn't match any row. Callers map it to
// a 401 in the assertion handler; we never echo "credential not
// found" to the client because that's a probe vector.
var ErrNotFound = errors.New("webauthn: credential not found")

// Record is the persisted shape of a single WebAuthn credential —
// one row in the webauthn_credentials table from migration 000035.
//
// It mirrors the library's Credential plus the GoNext-specific
// columns (id, user_id, name, timestamps). Store implementations
// convert between Record and gwa.Credential at the boundary.
type Record struct {
	ID              uuid.UUID
	UserID          uuid.UUID
	CredentialID    []byte
	PublicKey       []byte
	SignCount       uint32
	AttestationType string
	Name            string
	CreatedAt       time.Time
	LastUsedAt      *time.Time
}

// ToCredential converts a Record into the library's Credential shape
// so the verification methods can consume it. The library is
// otherwise indifferent to our column layout.
func (r Record) ToCredential() gwa.Credential {
	return gwa.Credential{
		ID:              r.CredentialID,
		PublicKey:       r.PublicKey,
		AttestationType: r.AttestationType,
		Authenticator: gwa.Authenticator{
			SignCount: r.SignCount,
		},
	}
}

// Store is the persistence seam for webauthn credentials. Both the
// MemoryStore (tests) and the PgxStore (production) implement it.
type Store interface {
	// Insert adds a freshly-registered credential to the store.
	// Returns the persisted Record (with ID populated) on success.
	Insert(ctx context.Context, rec Record) (Record, error)

	// ListForUser returns every credential the user has, oldest
	// first. Used by the login path to populate the User's
	// Credentials slice and by the admin UI to render the list.
	ListForUser(ctx context.Context, userID uuid.UUID) ([]Record, error)

	// GetByCredentialID looks up a single row by the raw
	// credential id bytes. Returns ErrNotFound when the id isn't
	// known.
	GetByCredentialID(ctx context.Context, credentialID []byte) (Record, error)

	// UpdateSignCount + LastUsedAt is called by FinishLogin after
	// a successful assertion so the next assertion can reject a
	// downgrade.
	UpdateSignCount(ctx context.Context, credentialID []byte, signCount uint32, lastUsedAt time.Time) error

	// Delete removes a credential by id (the row's primary key,
	// not the credential_id bytes). The admin UI's "Remove" button
	// calls into this.
	Delete(ctx context.Context, id uuid.UUID) error
}

// MemoryStore is the in-memory implementation used by tests and the
// no-DB dev loop. Safe for concurrent use; uses a single mutex
// because the surface is small enough that lock contention would
// require a benchmark suite to justify anything fancier.
type MemoryStore struct {
	mu      sync.RWMutex
	records map[uuid.UUID]Record
}

// NewMemoryStore returns an empty in-memory Store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{records: map[uuid.UUID]Record{}}
}

// Insert satisfies Store by assigning a fresh UUID + CreatedAt and
// recording the credential under that id.
func (s *MemoryStore) Insert(_ context.Context, rec Record) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rec.ID == uuid.Nil {
		rec.ID = uuid.New()
	}
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now().UTC()
	}
	s.records[rec.ID] = rec
	return rec, nil
}

// ListForUser satisfies Store by filtering the map. The result is
// sorted by CreatedAt ascending so the admin UI shows the oldest
// passkey first (matches the doc's "earliest enrolment first" rule
// from the design).
func (s *MemoryStore) ListForUser(_ context.Context, userID uuid.UUID) ([]Record, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Record
	for _, r := range s.records {
		if r.UserID == userID {
			out = append(out, r)
		}
	}
	// Simple insertion sort — the list per user is short (a handful
	// of passkeys at most).
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].CreatedAt.After(out[j].CreatedAt); j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out, nil
}

// GetByCredentialID satisfies Store by linear-scanning the map. We
// keep the surface small; a future production implementation will
// index on credential_id (the SQL migration already does).
func (s *MemoryStore) GetByCredentialID(_ context.Context, credentialID []byte) (Record, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, r := range s.records {
		if bytes.Equal(r.CredentialID, credentialID) {
			return r, nil
		}
	}
	return Record{}, ErrNotFound
}

// UpdateSignCount satisfies Store. Returns ErrNotFound if the
// credential id isn't known — defensive, the caller won't actually
// hit this because FinishLogin only calls Update on a credential it
// just verified.
func (s *MemoryStore) UpdateSignCount(_ context.Context, credentialID []byte, signCount uint32, lastUsedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, r := range s.records {
		if bytes.Equal(r.CredentialID, credentialID) {
			r.SignCount = signCount
			r.LastUsedAt = &lastUsedAt
			s.records[id] = r
			return nil
		}
	}
	return ErrNotFound
}

// Delete satisfies Store. Idempotent — deleting an unknown id is a
// no-op (matches the convention used by the admin UI's batch delete
// flow, which doesn't want to fail on a row a peer already removed).
func (s *MemoryStore) Delete(_ context.Context, id uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.records, id)
	return nil
}
