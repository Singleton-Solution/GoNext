package pat

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestMemoryStore_IssueAndLookup_Roundtrip is the happy-path baseline:
// issue a token, look it up by the original plaintext, get the row.
func TestMemoryStore_IssueAndLookup_Roundtrip(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	ctx := context.Background()

	plaintext, row, hash, err := New("user:1", "ci", []string{"read"}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	stored, err := s.Issue(ctx, row, hash)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if stored.ID == "" {
		t.Fatal("Issue must populate ID")
	}

	got, err := s.Lookup(ctx, plaintext)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if got.ID != stored.ID {
		t.Fatalf("Lookup ID: %q want %q", got.ID, stored.ID)
	}
	if got.UserID != "user:1" {
		t.Fatalf("Lookup UserID: %q", got.UserID)
	}
}

// TestMemoryStore_Lookup_InvalidShape returns ErrInvalid before paying
// the argon2 cost.
func TestMemoryStore_Lookup_InvalidShape(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	if _, err := s.Lookup(context.Background(), "garbage"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid, got %v", err)
	}
}

// TestMemoryStore_Lookup_NotFound on a well-shaped but unknown token.
func TestMemoryStore_Lookup_NotFound(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	// Generate a plaintext but don't issue it.
	plaintext, _, _, err := New("user:1", "x", nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := s.Lookup(context.Background(), plaintext); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

// TestMemoryStore_Lookup_Expired returns ErrExpired when expires_at is
// in the past.
func TestMemoryStore_Lookup_Expired(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	ctx := context.Background()

	past := time.Now().Add(-1 * time.Hour).UTC()
	plaintext, row, hash, err := New("user:1", "x", nil, &past)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := s.Issue(ctx, row, hash); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, err := s.Lookup(ctx, plaintext); !errors.Is(err, ErrExpired) {
		t.Fatalf("expected ErrExpired, got %v", err)
	}
}

// TestMemoryStore_Lookup_Revoked returns ErrRevoked after Revoke.
func TestMemoryStore_Lookup_Revoked(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	ctx := context.Background()

	plaintext, row, hash, err := New("user:1", "x", nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	stored, err := s.Issue(ctx, row, hash)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if err := s.Revoke(ctx, "user:1", stored.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, err := s.Lookup(ctx, plaintext); !errors.Is(err, ErrRevoked) {
		t.Fatalf("expected ErrRevoked, got %v", err)
	}
}

// TestMemoryStore_Lookup_ConstantTimeNoEarlyExit — issue two tokens
// in distinct order and verify both lookups succeed regardless of
// which row was inserted first. The implementation's loop must not
// stop on the first match.
func TestMemoryStore_Lookup_ManyCandidates(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	ctx := context.Background()

	const N = 16
	plaintexts := make([]string, 0, N)
	for i := 0; i < N; i++ {
		p, row, hash, err := New("user:1", "x", nil, nil)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if _, err := s.Issue(ctx, row, hash); err != nil {
			t.Fatalf("Issue: %v", err)
		}
		plaintexts = append(plaintexts, p)
	}
	// Every issued plaintext must look up successfully.
	for i, p := range plaintexts {
		got, err := s.Lookup(ctx, p)
		if err != nil {
			t.Fatalf("Lookup(%d): %v", i, err)
		}
		if got.UserID != "user:1" {
			t.Fatalf("Lookup(%d) UserID: %q", i, got.UserID)
		}
	}
}

// TestMemoryStore_List returns active tokens for a user; hidden after
// revoke; never includes other users' tokens.
func TestMemoryStore_List(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	ctx := context.Background()

	issue := func(uid string) PAT {
		_, row, hash, err := New(uid, "x", nil, nil)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		r, err := s.Issue(ctx, row, hash)
		if err != nil {
			t.Fatalf("Issue: %v", err)
		}
		return r
	}
	a1 := issue("user:1")
	_ = issue("user:1")
	_ = issue("user:2")

	list, err := s.List(ctx, "user:1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got, want := len(list), 2; got != want {
		t.Fatalf("List len: %d want %d", got, want)
	}
	// Revoke one and re-list.
	if err := s.Revoke(ctx, "user:1", a1.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	list, err = s.List(ctx, "user:1")
	if err != nil {
		t.Fatalf("List 2: %v", err)
	}
	if got, want := len(list), 1; got != want {
		t.Fatalf("List len after revoke: %d want %d", got, want)
	}
}

// TestMemoryStore_Revoke_Idempotent — revoking twice returns nil the
// second time.
func TestMemoryStore_Revoke_Idempotent(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	ctx := context.Background()
	_, row, hash, err := New("user:1", "x", nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	stored, err := s.Issue(ctx, row, hash)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if err := s.Revoke(ctx, "user:1", stored.ID); err != nil {
		t.Fatalf("Revoke 1: %v", err)
	}
	if err := s.Revoke(ctx, "user:1", stored.ID); err != nil {
		t.Fatalf("Revoke 2 (idempotent): %v", err)
	}
}

// TestMemoryStore_Revoke_WrongOwner — a user can't revoke another
// user's token via id alone.
func TestMemoryStore_Revoke_WrongOwner(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	ctx := context.Background()
	_, row, hash, err := New("user:1", "x", nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	stored, err := s.Issue(ctx, row, hash)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if err := s.Revoke(ctx, "user:2", stored.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Revoke wrong owner: got %v want ErrNotFound", err)
	}
}

// TestMemoryStore_Revoke_Unknown — an unknown id is ErrNotFound.
func TestMemoryStore_Revoke_Unknown(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	err := s.Revoke(context.Background(), "user:1", "00000000-0000-7000-8000-000000000999")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v want ErrNotFound", err)
	}
}

// TestMemoryStore_TouchUsed updates last_used_at.
func TestMemoryStore_TouchUsed(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	ctx := context.Background()
	_, row, hash, err := New("user:1", "x", nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	stored, err := s.Issue(ctx, row, hash)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	now := time.Now().UTC()
	if err := s.TouchUsed(ctx, stored.ID, now); err != nil {
		t.Fatalf("TouchUsed: %v", err)
	}
	list, err := s.List(ctx, "user:1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].LastUsedAt == nil || !list[0].LastUsedAt.Equal(now) {
		t.Fatalf("LastUsedAt not updated: %+v", list)
	}
}
