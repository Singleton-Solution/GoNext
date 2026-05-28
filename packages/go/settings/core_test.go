package settings

import (
	"context"
	"strings"
	"testing"
)

// TestRegisterCore_RegistersAllKeys asserts every key returned by
// CoreSettings shows up in a freshly seeded registry. Guards against
// silently dropping a Setting from the slice during a refactor.
func TestRegisterCore_RegistersAllKeys(t *testing.T) {
	reg := NewRegistry()
	if err := RegisterCore(reg); err != nil {
		t.Fatalf("RegisterCore: %v", err)
	}

	for _, want := range CoreSettings() {
		got, err := reg.Get(want.Key)
		if err != nil {
			t.Errorf("Get(%q): %v", want.Key, err)
			continue
		}
		if got.Key != want.Key {
			t.Errorf("Get(%q): got key %q", want.Key, got.Key)
		}
	}
}

// TestRegisterCore_ReadingKeysPresent locks in the keys the admin
// Reading page renders against. Issue #525 — if any of these go
// missing, the form renders blank and saves 400.
func TestRegisterCore_ReadingKeysPresent(t *testing.T) {
	reg := NewRegistry()
	if err := RegisterCore(reg); err != nil {
		t.Fatalf("RegisterCore: %v", err)
	}

	wantKeys := []string{
		"core.reading.homepage_type",
		"core.reading.homepage_page_id",
		"core.reading.posts_per_page",
		"core.reading.show_summary",
		"core.reading.rss_items",
		"core.reading.rss_full_text",
	}
	for _, key := range wantKeys {
		s, err := reg.Get(key)
		if err != nil {
			t.Errorf("Get(%q): %v", key, err)
			continue
		}
		if s.Group != GroupReading {
			t.Errorf("%s: Group = %q, want %q", key, s.Group, GroupReading)
		}
		if !strings.HasPrefix(s.Key, "core.reading.") {
			t.Errorf("%s: key should be under core.reading.* namespace", key)
		}
	}
}

// TestRegisterCore_WritingKeysPresent locks in the keys the admin
// Writing page renders against. See the Reading equivalent.
func TestRegisterCore_WritingKeysPresent(t *testing.T) {
	reg := NewRegistry()
	if err := RegisterCore(reg); err != nil {
		t.Fatalf("RegisterCore: %v", err)
	}

	wantKeys := []string{
		"core.writing.default_category",
		"core.writing.default_format",
		"core.writing.default_editor",
		"core.writing.post_by_email_enabled",
		"core.writing.post_by_email_address",
	}
	for _, key := range wantKeys {
		s, err := reg.Get(key)
		if err != nil {
			t.Errorf("Get(%q): %v", key, err)
			continue
		}
		if s.Group != GroupWriting {
			t.Errorf("%s: Group = %q, want %q", key, s.Group, GroupWriting)
		}
		if !strings.HasPrefix(s.Key, "core.writing.") {
			t.Errorf("%s: key should be under core.writing.* namespace", key)
		}
	}
}

// TestRegisterCore_DefaultsReadable seeds the registry, wraps it in a
// memory store, and confirms every core key returns its declared default
// from Read (no Write needed). Equivalent to the
// TestRegisterPrivacy_AllRegister belt-and-braces check.
func TestRegisterCore_DefaultsReadable(t *testing.T) {
	reg := NewRegistry()
	if err := RegisterCore(reg); err != nil {
		t.Fatalf("RegisterCore: %v", err)
	}
	store := NewMemoryStore(reg)
	for _, s := range CoreSettings() {
		v, err := store.Read(context.Background(), s.Key)
		if err != nil {
			t.Errorf("Read %s: %v", s.Key, err)
			continue
		}
		if v == nil && s.Default != nil {
			t.Errorf("expected default for %s, got nil", s.Key)
		}
	}
}

// TestRegisterCore_ReadingPostsPerPageWriteRoundtrip confirms the
// posts_per_page schema accepts the documented range. The admin form
// renders a number input; we make sure a typical value round-trips
// through the registry without a validation error.
func TestRegisterCore_ReadingPostsPerPageWriteRoundtrip(t *testing.T) {
	reg := NewRegistry()
	if err := RegisterCore(reg); err != nil {
		t.Fatalf("RegisterCore: %v", err)
	}
	store := NewMemoryStore(reg)

	if err := store.Write(context.Background(), "core.reading.posts_per_page", float64(25)); err != nil {
		t.Fatalf("Write posts_per_page=25: %v", err)
	}
	got, err := store.Read(context.Background(), "core.reading.posts_per_page")
	if err != nil {
		t.Fatalf("Read posts_per_page: %v", err)
	}
	if got.(float64) != 25 {
		t.Errorf("posts_per_page roundtrip: got %v, want 25", got)
	}
}

// TestRegisterCore_WritingDefaultEditorEnum confirms the default_editor
// enum schema rejects an unknown value. Cheap insurance that the
// validation hook is actually wired.
func TestRegisterCore_WritingDefaultEditorEnum(t *testing.T) {
	reg := NewRegistry()
	if err := RegisterCore(reg); err != nil {
		t.Fatalf("RegisterCore: %v", err)
	}
	store := NewMemoryStore(reg)

	if err := store.Write(context.Background(), "core.writing.default_editor", "block"); err != nil {
		t.Fatalf("Write default_editor=block: %v", err)
	}
	if err := store.Write(context.Background(), "core.writing.default_editor", "vim"); err == nil {
		t.Errorf("Write default_editor=vim: want validation error, got nil")
	}
}
