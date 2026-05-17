package verify

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRedisTokenStore_RequiresClient(t *testing.T) {
	if _, err := NewRedisTokenStore(nil); err == nil {
		t.Fatal("expected error for nil client")
	}
}

func TestMemTokenStore_ContractRoundTrip(t *testing.T) {
	// Sanity check for the in-memory store the unit tests use.
	s := newMemTokenStore(time.Now)
	ctx := context.Background()

	if err := s.Save(ctx, "h1", "u1", time.Hour); err != nil {
		t.Fatalf("Save: %v", err)
	}
	uid, err := s.Lookup(ctx, "h1")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if uid != "u1" {
		t.Errorf("uid: got %q want u1", uid)
	}
	if err := s.Consume(ctx, "h1"); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if _, err := s.Lookup(ctx, "h1"); !errors.Is(err, ErrTokenNotFound) {
		t.Errorf("after Consume: got %v want ErrTokenNotFound", err)
	}
}

func TestMemTokenStore_Expiry(t *testing.T) {
	clk := newClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	s := newMemTokenStore(clk.Now)
	ctx := context.Background()
	if err := s.Save(ctx, "h", "u", time.Hour); err != nil {
		t.Fatalf("Save: %v", err)
	}
	clk.Advance(2 * time.Hour)
	if _, err := s.Lookup(ctx, "h"); !errors.Is(err, ErrTokenNotFound) {
		t.Errorf("expired token did not return ErrTokenNotFound: %v", err)
	}
}
