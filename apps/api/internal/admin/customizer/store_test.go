package customizer

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// TestMemoryStore_ActiveTheme covers the slug accessor + ErrNoActiveTheme.
func TestMemoryStore_ActiveTheme(t *testing.T) {
	s := NewMemoryStore("gn-hello")
	got, err := s.ActiveThemeSlug(context.Background())
	if err != nil {
		t.Fatalf("ActiveThemeSlug: %v", err)
	}
	if got != "gn-hello" {
		t.Fatalf("slug = %q; want gn-hello", got)
	}

	empty := NewMemoryStore("")
	if _, err := empty.ActiveThemeSlug(context.Background()); !errors.Is(err, ErrNoActiveTheme) {
		t.Fatalf("expected ErrNoActiveTheme; got %v", err)
	}
}

// TestMemoryStore_RoundTrip stores, reads back, and deletes.
func TestMemoryStore_RoundTrip(t *testing.T) {
	s := NewMemoryStore("gn-hello")
	ctx := context.Background()

	if v, _ := s.ReadOverrides(ctx, "gn-hello"); v != nil {
		t.Fatalf("fresh store returned non-nil overrides: %s", string(v))
	}

	payload := json.RawMessage(`{"settings":{"layout":{"contentSize":"800px"}}}`)
	if err := s.WriteOverrides(ctx, "gn-hello", payload); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := s.ReadOverrides(ctx, "gn-hello")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("Read = %s; want %s", string(got), string(payload))
	}

	if err := s.DeleteOverrides(ctx, "gn-hello"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if v, _ := s.ReadOverrides(ctx, "gn-hello"); v != nil {
		t.Fatalf("delete left a row: %s", string(v))
	}
}

// TestMemoryStore_WriteEmpty Forwards an empty payload to delete so a
// no-op upsert doesn't masquerade as an active customization.
func TestMemoryStore_WriteEmpty(t *testing.T) {
	s := NewMemoryStore("gn-hello")
	ctx := context.Background()
	_ = s.WriteOverrides(ctx, "gn-hello", json.RawMessage(`{"settings":{"layout":{"contentSize":"800px"}}}`))
	if err := s.WriteOverrides(ctx, "gn-hello", nil); err != nil {
		t.Fatalf("Write(empty): %v", err)
	}
	if v, _ := s.ReadOverrides(ctx, "gn-hello"); v != nil {
		t.Fatalf("expected empty write to delete; got %s", string(v))
	}
}

// TestOverridesKey covers the slug -> options key convention so the
// admin UI and audit log share the same string format.
func TestOverridesKey(t *testing.T) {
	if got := OverridesKey("gn-hello"); got != "theme_mods.gn-hello" {
		t.Fatalf("OverridesKey = %q; want theme_mods.gn-hello", got)
	}
}
