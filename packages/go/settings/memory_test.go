package settings

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"testing"

	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// testRegistry returns a Registry pre-loaded with a small but diverse
// set of settings used across store tests. Keeps each test compact.
func testRegistry(t *testing.T) *Registry {
	t.Helper()
	reg := NewRegistry()

	settings := []Setting{
		{
			Key:                "core.site.name",
			Description:        "Site name",
			Type:               SettingTypeString,
			Schema:             json.RawMessage(`{"type":"string","minLength":1,"maxLength":80}`),
			Default:            "My GoNext Site",
			Autoload:           true,
			Group:              GroupGeneral,
			RequiresCapability: policy.CapManageOptions,
		},
		{
			Key:                "core.posts.per_page",
			Description:        "How many posts per page",
			Type:               SettingTypeInt,
			Schema:             json.RawMessage(`{"type":"integer","minimum":1,"maximum":100}`),
			Default:            float64(10), // JSON numbers decode to float64 by default
			Autoload:           false,
			Group:              GroupReading,
			RequiresCapability: policy.CapManageOptions,
		},
		{
			Key:                "core.comments.enabled",
			Description:        "Whether comments are enabled site-wide",
			Type:               SettingTypeBool,
			Schema:             json.RawMessage(`{"type":"boolean"}`),
			Default:            true,
			Autoload:           true,
			Group:              GroupDiscussion,
			RequiresCapability: policy.CapManageOptions,
		},
		{
			Key:                "core.site.default_role",
			Description:        "Default role for new users",
			Type:               SettingTypeEnum,
			Schema:             json.RawMessage(`{"type":"string","enum":["subscriber","contributor","author"]}`),
			Default:            "subscriber",
			Autoload:           false,
			Group:              GroupGeneral,
			RequiresCapability: policy.CapManageOptions,
		},
	}
	for _, s := range settings {
		if err := reg.Register(s); err != nil {
			t.Fatalf("seed Register(%q): %v", s.Key, err)
		}
	}
	return reg
}

func TestMemoryStore_ReadReturnsDefaultWhenNotWritten(t *testing.T) {
	reg := testRegistry(t)
	store := NewMemoryStore(reg)

	v, err := store.Read(context.Background(), "core.site.name")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if v != "My GoNext Site" {
		t.Errorf("Read default: got %v want %q", v, "My GoNext Site")
	}
}

func TestMemoryStore_WriteThenRead(t *testing.T) {
	reg := testRegistry(t)
	store := NewMemoryStore(reg)
	ctx := context.Background()

	if err := store.Write(ctx, "core.site.name", "Custom Site"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	v, err := store.Read(ctx, "core.site.name")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if v != "Custom Site" {
		t.Errorf("Read: got %v want %q", v, "Custom Site")
	}
}

func TestMemoryStore_ReadUnknownKey(t *testing.T) {
	reg := testRegistry(t)
	store := NewMemoryStore(reg)

	_, err := store.Read(context.Background(), "no.such.key")
	if !errors.Is(err, ErrUnknownKey) {
		t.Errorf("expected ErrUnknownKey, got %v", err)
	}
}

func TestMemoryStore_WriteUnknownKey(t *testing.T) {
	reg := testRegistry(t)
	store := NewMemoryStore(reg)

	err := store.Write(context.Background(), "no.such.key", "anything")
	if !errors.Is(err, ErrUnknownKey) {
		t.Errorf("expected ErrUnknownKey, got %v", err)
	}
}

func TestMemoryStore_BulkReadAppliesDefaults(t *testing.T) {
	reg := testRegistry(t)
	store := NewMemoryStore(reg)
	ctx := context.Background()

	// Write one of the three keys to verify mixed (stored + default) behavior.
	if err := store.Write(ctx, "core.site.name", "Stored"); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := store.BulkRead(ctx, []string{
		"core.site.name", "core.comments.enabled", "no.such.key",
	})
	if err != nil {
		t.Fatalf("BulkRead: %v", err)
	}

	if got["core.site.name"] != "Stored" {
		t.Errorf("site.name: got %v want %q", got["core.site.name"], "Stored")
	}
	if got["core.comments.enabled"] != true {
		t.Errorf("comments.enabled default: got %v want true", got["core.comments.enabled"])
	}
	if _, present := got["no.such.key"]; present {
		t.Errorf("unknown key should be skipped, got %v", got["no.such.key"])
	}
	if len(got) != 2 {
		t.Errorf("expected 2 results, got %d: %+v", len(got), got)
	}
}

func TestMemoryStore_LoadAutoloadOnlyAutoloadKeys(t *testing.T) {
	reg := testRegistry(t)
	store := NewMemoryStore(reg)
	ctx := context.Background()

	got, err := store.LoadAutoload(ctx)
	if err != nil {
		t.Fatalf("LoadAutoload: %v", err)
	}

	// Expected: only Autoload=true keys (site.name, comments.enabled).
	wantKeys := []string{"core.site.name", "core.comments.enabled"}
	gotKeys := make([]string, 0, len(got))
	for k := range got {
		gotKeys = append(gotKeys, k)
	}
	sort.Strings(wantKeys)
	sort.Strings(gotKeys)
	if !reflect.DeepEqual(wantKeys, gotKeys) {
		t.Errorf("LoadAutoload keys: got %v want %v", gotKeys, wantKeys)
	}

	// Defaults must be applied for autoload keys not yet written.
	if got["core.site.name"] != "My GoNext Site" {
		t.Errorf("site.name default: got %v", got["core.site.name"])
	}
	if got["core.comments.enabled"] != true {
		t.Errorf("comments.enabled default: got %v", got["core.comments.enabled"])
	}
}

func TestMemoryStore_LoadAutoloadReflectsWrites(t *testing.T) {
	reg := testRegistry(t)
	store := NewMemoryStore(reg)
	ctx := context.Background()

	if err := store.Write(ctx, "core.site.name", "Overridden"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := store.Write(ctx, "core.posts.per_page", float64(25)); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := store.LoadAutoload(ctx)
	if err != nil {
		t.Fatalf("LoadAutoload: %v", err)
	}
	if got["core.site.name"] != "Overridden" {
		t.Errorf("autoload reflects write: got %v want %q", got["core.site.name"], "Overridden")
	}
	// posts.per_page is autoload=false; must not appear.
	if _, ok := got["core.posts.per_page"]; ok {
		t.Errorf("autoload=false key leaked into LoadAutoload: %v", got)
	}
}

func TestMemoryStore_BulkReadCachesNothing(t *testing.T) {
	// MemoryStore has no L1 cache to worry about — its values map IS
	// the storage — but a regression that accidentally returned stale
	// values would still be caught here.
	reg := testRegistry(t)
	store := NewMemoryStore(reg)
	ctx := context.Background()

	first, _ := store.BulkRead(ctx, []string{"core.site.name"})
	if first["core.site.name"] != "My GoNext Site" {
		t.Errorf("first read: got %v", first["core.site.name"])
	}

	if err := store.Write(ctx, "core.site.name", "Changed"); err != nil {
		t.Fatalf("Write: %v", err)
	}

	second, _ := store.BulkRead(ctx, []string{"core.site.name"})
	if second["core.site.name"] != "Changed" {
		t.Errorf("post-write read: got %v want %q", second["core.site.name"], "Changed")
	}
}
