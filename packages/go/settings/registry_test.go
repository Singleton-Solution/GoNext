package settings

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/Singleton-Solution/GoNext/packages/go/jsonschemautil"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// stringSetting is a tiny helper used across registry tests to keep
// each test case readable. The actual schema-validation behavior is
// exercised by store_test.go; here we only need a Setting that
// Register accepts.
func stringSetting(key string) Setting {
	return Setting{
		Key:                key,
		Description:        "Test setting " + key,
		Type:               SettingTypeString,
		Schema:             json.RawMessage(`{"type":"string"}`),
		Default:            "default-" + key,
		Group:              GroupGeneral,
		RequiresCapability: policy.CapManageOptions,
	}
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	reg := NewRegistry()
	s := stringSetting("test.foo")

	if err := reg.Register(s); err != nil {
		t.Fatalf("Register: %v", err)
	}

	got, err := reg.Get("test.foo")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Key != "test.foo" {
		t.Errorf("Key: got %q want %q", got.Key, "test.foo")
	}
	if got.Default != "default-test.foo" {
		t.Errorf("Default: got %v want %v", got.Default, "default-test.foo")
	}
}

func TestRegistry_GetUnknownReturnsNotFound(t *testing.T) {
	reg := NewRegistry()
	_, err := reg.Get("does.not.exist")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestRegistry_RegisterDuplicateReturnsError(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(stringSetting("dup.key")); err != nil {
		t.Fatalf("first Register: %v", err)
	}

	err := reg.Register(stringSetting("dup.key"))
	if !errors.Is(err, ErrDuplicateKey) {
		t.Errorf("expected ErrDuplicateKey, got %v", err)
	}
	if !strings.Contains(err.Error(), "dup.key") {
		t.Errorf("error should mention the key: %v", err)
	}
}

func TestRegistry_RegisterRejectsEmptyKey(t *testing.T) {
	reg := NewRegistry()
	s := stringSetting("")
	if err := reg.Register(s); !errors.Is(err, ErrEmptyKey) {
		t.Errorf("expected ErrEmptyKey, got %v", err)
	}
}

func TestRegistry_RegisterRejectsWhitespaceKey(t *testing.T) {
	reg := NewRegistry()
	s := stringSetting("   ")
	if err := reg.Register(s); !errors.Is(err, ErrEmptyKey) {
		t.Errorf("expected ErrEmptyKey, got %v", err)
	}
}

func TestRegistry_RegisterRejectsInvalidType(t *testing.T) {
	reg := NewRegistry()
	s := stringSetting("test.bad-type")
	s.Type = "weird"
	if err := reg.Register(s); !errors.Is(err, ErrInvalidType) {
		t.Errorf("expected ErrInvalidType, got %v", err)
	}
}

func TestRegistry_RegisterRejectsEmptySchema(t *testing.T) {
	reg := NewRegistry()
	s := stringSetting("test.no-schema")
	s.Schema = nil
	if err := reg.Register(s); !errors.Is(err, ErrInvalidSchema) {
		t.Errorf("expected ErrInvalidSchema, got %v", err)
	}
}

func TestRegistry_RegisterRejectsMalformedSchema(t *testing.T) {
	reg := NewRegistry()
	s := stringSetting("test.bad-schema")
	s.Schema = json.RawMessage(`{not-json`)
	if err := reg.Register(s); !errors.Is(err, ErrInvalidSchema) {
		t.Errorf("expected ErrInvalidSchema, got %v", err)
	}
}

// TestRegistry_RegisterAcceptsExplicit2020Dialect proves the dialect
// pin lets through schemas that explicitly declare the canonical URI,
// in addition to the existing "schema with no $schema field" case.
// This is the "valid 2020-12 schema" leg of issue #275's test plan.
func TestRegistry_RegisterAcceptsExplicit2020Dialect(t *testing.T) {
	reg := NewRegistry()
	s := stringSetting("test.with-dialect")
	s.Schema = json.RawMessage(`{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type": "string",
		"minLength": 1
	}`)
	if err := reg.Register(s); err != nil {
		t.Fatalf("Register: %v", err)
	}
}

// TestRegistry_RegisterRejectsDraft07Schema is the headline behavior:
// a setting whose Schema field declares draft-07 must be refused with
// a clear, sentinel-chained error. Plugin loaders surface this back to
// the operator as "your plugin's settings declared the wrong JSON
// Schema draft" — see docs/02-plugin-system.md §7.7.
func TestRegistry_RegisterRejectsDraft07Schema(t *testing.T) {
	reg := NewRegistry()
	s := stringSetting("test.draft07")
	s.Schema = json.RawMessage(`{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"type": "string"
	}`)
	err := reg.Register(s)
	if err == nil {
		t.Fatal("Register accepted a draft-07 schema; expected rejection")
	}
	if !errors.Is(err, ErrInvalidSchema) {
		t.Errorf("error should chain ErrInvalidSchema: %v", err)
	}
	if !errors.Is(err, jsonschemautil.ErrUnsupportedDialect) {
		t.Errorf("error should chain jsonschemautil.ErrUnsupportedDialect: %v", err)
	}
	// Operator-facing message: ensure the offending draft is named so
	// the plugin author can fix it without spelunking through code.
	if !strings.Contains(err.Error(), "draft-07") {
		t.Errorf("error message should mention the bad draft: %v", err)
	}
}

// TestRegistry_RegisterRejectsOtherDraftSchemas locks down the policy
// that ANY non-2020-12 dialect is rejected, not just draft-07.
func TestRegistry_RegisterRejectsOtherDraftSchemas(t *testing.T) {
	cases := []string{
		"http://json-schema.org/draft-04/schema#",
		"http://json-schema.org/draft-06/schema#",
		"https://json-schema.org/draft/2019-09/schema",
	}
	for _, dialect := range cases {
		reg := NewRegistry()
		s := stringSetting("test.draft")
		s.Schema = json.RawMessage(`{"$schema":"` + dialect + `","type":"string"}`)
		err := reg.Register(s)
		if !errors.Is(err, jsonschemautil.ErrUnsupportedDialect) {
			t.Errorf("dialect %q: expected ErrUnsupportedDialect, got %v", dialect, err)
		}
	}
}

func TestRegistry_List_Sorted(t *testing.T) {
	reg := NewRegistry()
	keys := []string{"z.last", "a.first", "m.middle", "b.second"}
	for _, k := range keys {
		if err := reg.Register(stringSetting(k)); err != nil {
			t.Fatalf("Register(%q): %v", k, err)
		}
	}

	got := reg.List()
	if len(got) != 4 {
		t.Fatalf("len: got %d want 4", len(got))
	}
	want := []string{"a.first", "b.second", "m.middle", "z.last"}
	for i, s := range got {
		if s.Key != want[i] {
			t.Errorf("at %d: got %q want %q", i, s.Key, want[i])
		}
	}
}

func TestRegistry_ListByGroup(t *testing.T) {
	reg := NewRegistry()

	general := stringSetting("general.one")
	general.Group = GroupGeneral
	reading := stringSetting("reading.one")
	reading.Group = GroupReading
	general2 := stringSetting("general.two")
	general2.Group = GroupGeneral

	for _, s := range []Setting{general, reading, general2} {
		if err := reg.Register(s); err != nil {
			t.Fatalf("Register(%q): %v", s.Key, err)
		}
	}

	got := reg.ListByGroup(GroupGeneral)
	if len(got) != 2 {
		t.Fatalf("GroupGeneral: got %d want 2", len(got))
	}
	if got[0].Key != "general.one" || got[1].Key != "general.two" {
		t.Errorf("GroupGeneral keys: got %q,%q", got[0].Key, got[1].Key)
	}

	got = reg.ListByGroup(GroupReading)
	if len(got) != 1 || got[0].Key != "reading.one" {
		t.Errorf("GroupReading: got %+v", got)
	}

	got = reg.ListByGroup("nonexistent")
	if len(got) != 0 {
		t.Errorf("nonexistent group: got %d want 0", len(got))
	}
}

func TestRegistry_ListReturnsCopy(t *testing.T) {
	// Mutating the returned slice must not affect the registry.
	reg := NewRegistry()
	if err := reg.Register(stringSetting("test.one")); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got := reg.List()
	got[0].Key = "mutated"

	// Re-list and check the original key survived.
	got2 := reg.List()
	if got2[0].Key != "test.one" {
		t.Errorf("List returned by reference: got %q after mutation", got2[0].Key)
	}
}

func TestRegistry_ConcurrentRegister(t *testing.T) {
	reg := NewRegistry()
	const n = 64
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			s := stringSetting("concurrent.key." + itoa(i))
			if err := reg.Register(s); err != nil {
				t.Errorf("Register(%d): %v", i, err)
			}
		}()
	}
	wg.Wait()

	if got := len(reg.List()); got != n {
		t.Errorf("post-concurrent List len: got %d want %d", got, n)
	}
}

func TestRegistry_MustRegisterPanicsOnDuplicate(t *testing.T) {
	reg := NewRegistry()
	reg.MustRegister(stringSetting("must.dup"))

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on MustRegister duplicate")
		} else if msg, _ := r.(string); !strings.Contains(msg, "must.dup") {
			t.Errorf("panic message should mention key, got %v", r)
		}
	}()
	reg.MustRegister(stringSetting("must.dup"))
}

func TestPackageGlobalRegistry_Roundtrip(t *testing.T) {
	t.Cleanup(resetGlobalRegistryForTest)
	resetGlobalRegistryForTest()

	if err := Register(stringSetting("pkg.foo")); err != nil {
		t.Fatalf("package Register: %v", err)
	}
	if _, err := Get("pkg.foo"); err != nil {
		t.Errorf("package Get: %v", err)
	}
	if got := List(); len(got) != 1 || got[0].Key != "pkg.foo" {
		t.Errorf("package List: got %+v", got)
	}
	if got := ListByGroup(GroupGeneral); len(got) != 1 {
		t.Errorf("package ListByGroup(general): got %d want 1", len(got))
	}
}

func TestPackageMustRegister_Panics(t *testing.T) {
	t.Cleanup(resetGlobalRegistryForTest)
	resetGlobalRegistryForTest()

	MustRegister(stringSetting("pkg.must"))
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on duplicate MustRegister")
		}
	}()
	MustRegister(stringSetting("pkg.must"))
}

// itoa is a tiny helper to avoid pulling strconv into the test file
// purely for label construction.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
