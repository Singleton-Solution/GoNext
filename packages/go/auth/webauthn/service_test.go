package webauthn

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestMemoryStore_InsertList exercises the most common round-trip:
// insert two records, list them back, confirm ordering.
func TestMemoryStore_InsertList(t *testing.T) {
	s := NewMemoryStore()
	uid := uuid.New()
	first := Record{
		UserID:       uid,
		CredentialID: []byte("cred-1"),
		PublicKey:    []byte("pk-1"),
		CreatedAt:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	second := Record{
		UserID:       uid,
		CredentialID: []byte("cred-2"),
		PublicKey:    []byte("pk-2"),
		CreatedAt:    time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
	}
	if _, err := s.Insert(context.Background(), first); err != nil {
		t.Fatalf("insert first: %v", err)
	}
	if _, err := s.Insert(context.Background(), second); err != nil {
		t.Fatalf("insert second: %v", err)
	}

	got, err := s.ListForUser(context.Background(), uid)
	if err != nil {
		t.Fatalf("ListForUser: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 records; got %d", len(got))
	}
	if !got[0].CreatedAt.Before(got[1].CreatedAt) {
		t.Fatalf("expected ascending-by-CreatedAt order; got %v then %v",
			got[0].CreatedAt, got[1].CreatedAt)
	}
}

// TestMemoryStore_GetByCredentialID_NotFound asserts the sentinel
// error wiring — callers branch on ErrNotFound to produce the 401
// without leaking "credential not found" in the response body.
func TestMemoryStore_GetByCredentialID_NotFound(t *testing.T) {
	s := NewMemoryStore()
	_, err := s.GetByCredentialID(context.Background(), []byte("missing"))
	if err == nil {
		t.Fatal("expected ErrNotFound; got nil")
	}
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound; got %v", err)
	}
}

// TestMemoryStore_UpdateSignCount confirms the sign-count round-trip
// — read after write, the new value sticks.
func TestMemoryStore_UpdateSignCount(t *testing.T) {
	s := NewMemoryStore()
	uid := uuid.New()
	rec, err := s.Insert(context.Background(), Record{
		UserID: uid, CredentialID: []byte("c"), PublicKey: []byte("p"), SignCount: 0,
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	now := time.Now().UTC()
	if err := s.UpdateSignCount(context.Background(), rec.CredentialID, 42, now); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := s.GetByCredentialID(context.Background(), rec.CredentialID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.SignCount != 42 {
		t.Fatalf("expected sign count 42; got %d", got.SignCount)
	}
	if got.LastUsedAt == nil || !got.LastUsedAt.Equal(now) {
		t.Fatalf("expected LastUsedAt == %v; got %v", now, got.LastUsedAt)
	}
}

// TestNewService_RejectsMissingStore + TestNewService_RejectsMissingResolver
// guard the boot-time contract — both deps are required because the
// HTTP handlers would dereference nil otherwise.
func TestNewService_RejectsMissingStore(t *testing.T) {
	_, err := NewService(Config{RPID: "x", RPDisplayName: "x", RPOrigins: []string{"https://x"}}, nil,
		func(_ context.Context, _ uuid.UUID) (User, error) { return User{}, nil })
	if err == nil {
		t.Fatal("expected error for nil store; got nil")
	}
}

func TestNewService_RejectsMissingResolver(t *testing.T) {
	_, err := NewService(Config{RPID: "x", RPDisplayName: "x", RPOrigins: []string{"https://x"}},
		NewMemoryStore(), nil)
	if err == nil {
		t.Fatal("expected error for nil resolver; got nil")
	}
}

// TestService_BeginRegistration_StubResolver exercises the happy path
// without ever needing a real browser/authenticator. We don't have a
// way to fabricate a valid attestation response (that's what a real
// authenticator does), but we CAN confirm:
//
//   - the library returns a credential-creation payload,
//   - the session blob round-trips through Marshal/Unmarshal,
//   - the User resolver is called with the supplied id.
//
// The FinishRegistration path is exercised end-to-end by the HTTP
// handler integration test in apps/api/internal/auth/webauthn.
func TestService_BeginRegistration_StubResolver(t *testing.T) {
	uid := uuid.New()
	resolveCalls := 0
	svc, err := NewService(Config{
		RPID:          "localhost",
		RPDisplayName: "GoNext Test",
		RPOrigins:     []string{"https://localhost"},
	}, NewMemoryStore(),
		func(_ context.Context, gotUID uuid.UUID) (User, error) {
			resolveCalls++
			if gotUID != uid {
				t.Errorf("expected user id %v; got %v", uid, gotUID)
			}
			return User{
				ID:       uid,
				Username: "alice@example.com",
			}, nil
		})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	creation, session, err := svc.BeginRegistration(context.Background(), uid)
	if err != nil {
		t.Fatalf("BeginRegistration: %v", err)
	}
	if creation == nil {
		t.Fatal("expected non-nil creation payload")
	}
	if session == nil {
		t.Fatal("expected non-nil session data")
	}
	if resolveCalls != 1 {
		t.Fatalf("expected 1 resolver call; got %d", resolveCalls)
	}

	// Round-trip the session blob — the HTTP handler will marshal
	// it into the session cookie payload, so the test confirms the
	// shape is stable.
	b, err := MarshalSession(session)
	if err != nil {
		t.Fatalf("MarshalSession: %v", err)
	}
	got, err := UnmarshalSession(b)
	if err != nil {
		t.Fatalf("UnmarshalSession: %v", err)
	}
	if string(got.Challenge) != string(session.Challenge) {
		t.Fatalf("challenge round-trip lost data; in=%q out=%q",
			session.Challenge, got.Challenge)
	}
}
