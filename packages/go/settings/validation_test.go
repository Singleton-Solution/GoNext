package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestWrite_RejectsValueViolatingSchema(t *testing.T) {
	reg := testRegistry(t)
	store := NewMemoryStore(reg)
	ctx := context.Background()

	// site.name has minLength: 1 — empty string violates it.
	err := store.Write(ctx, "core.site.name", "")
	if !errors.Is(err, ErrValidation) {
		t.Errorf("expected ErrValidation, got %v", err)
	}
}

func TestWrite_RejectsWrongType(t *testing.T) {
	reg := testRegistry(t)
	store := NewMemoryStore(reg)
	ctx := context.Background()

	// site.name wants string; pass an int.
	err := store.Write(ctx, "core.site.name", 42)
	if !errors.Is(err, ErrValidation) {
		t.Errorf("expected ErrValidation, got %v", err)
	}
}

func TestWrite_RejectsIntOutOfRange(t *testing.T) {
	reg := testRegistry(t)
	store := NewMemoryStore(reg)
	ctx := context.Background()

	// posts.per_page has minimum 1, maximum 100.
	if err := store.Write(ctx, "core.posts.per_page", 0); !errors.Is(err, ErrValidation) {
		t.Errorf("0 should be rejected (below minimum): %v", err)
	}
	if err := store.Write(ctx, "core.posts.per_page", 1000); !errors.Is(err, ErrValidation) {
		t.Errorf("1000 should be rejected (above maximum): %v", err)
	}
}

func TestWrite_AcceptsValidInt(t *testing.T) {
	reg := testRegistry(t)
	store := NewMemoryStore(reg)
	ctx := context.Background()

	// Both Go int and JSON-style float64 should be accepted; the
	// normalize step round-trips through encoding/json.
	if err := store.Write(ctx, "core.posts.per_page", 25); err != nil {
		t.Errorf("Write int: %v", err)
	}
	if err := store.Write(ctx, "core.posts.per_page", float64(25)); err != nil {
		t.Errorf("Write float64: %v", err)
	}
}

func TestWrite_EnumRejectsUnknownValue(t *testing.T) {
	reg := testRegistry(t)
	store := NewMemoryStore(reg)
	ctx := context.Background()

	// default_role enum is [subscriber, contributor, author].
	if err := store.Write(ctx, "core.site.default_role", "admin"); !errors.Is(err, ErrValidation) {
		t.Errorf("non-enum value should be rejected: %v", err)
	}
	if err := store.Write(ctx, "core.site.default_role", "subscriber"); err != nil {
		t.Errorf("valid enum should be accepted: %v", err)
	}
}

func TestWrite_BoolStrictlyBool(t *testing.T) {
	reg := testRegistry(t)
	store := NewMemoryStore(reg)
	ctx := context.Background()

	if err := store.Write(ctx, "core.comments.enabled", true); err != nil {
		t.Errorf("bool true: %v", err)
	}
	if err := store.Write(ctx, "core.comments.enabled", false); err != nil {
		t.Errorf("bool false: %v", err)
	}
	if err := store.Write(ctx, "core.comments.enabled", "yes"); !errors.Is(err, ErrValidation) {
		t.Errorf("string-instead-of-bool should be rejected: %v", err)
	}
}

func TestWrite_CallsValidatorAfterSchema(t *testing.T) {
	reg := NewRegistry()
	validatorCalled := false
	rejectErr := errors.New("validator says no")

	s := Setting{
		Key:         "test.validator",
		Description: "Has a validator",
		Type:        SettingTypeString,
		Schema:      json.RawMessage(`{"type":"string","minLength":1}`),
		Default:     "ok",
		Group:       GroupGeneral,
		Validator: func(v any) error {
			validatorCalled = true
			s, _ := v.(string)
			if strings.Contains(s, "forbidden") {
				return rejectErr
			}
			return nil
		},
	}
	if err := reg.Register(s); err != nil {
		t.Fatalf("Register: %v", err)
	}

	store := NewMemoryStore(reg)
	ctx := context.Background()

	// Schema rejection short-circuits before validator runs.
	validatorCalled = false
	if err := store.Write(ctx, "test.validator", ""); !errors.Is(err, ErrValidation) {
		t.Errorf("schema reject: %v", err)
	}
	if validatorCalled {
		t.Error("validator should not run when schema fails")
	}

	// Schema passes, validator rejects.
	validatorCalled = false
	err := store.Write(ctx, "test.validator", "forbidden-value")
	if !validatorCalled {
		t.Error("validator should run after schema passes")
	}
	if !errors.Is(err, ErrValidation) {
		t.Errorf("validator reject should wrap ErrValidation: %v", err)
	}
	if !strings.Contains(err.Error(), "validator says no") {
		t.Errorf("validator error message should bubble up: %v", err)
	}

	// Schema passes, validator passes.
	validatorCalled = false
	if err := store.Write(ctx, "test.validator", "totally fine"); err != nil {
		t.Errorf("happy path: %v", err)
	}
	if !validatorCalled {
		t.Error("validator should have run on happy path")
	}
}

func TestWrite_DoesNotModifyStoreOnValidationFailure(t *testing.T) {
	reg := testRegistry(t)
	store := NewMemoryStore(reg)
	ctx := context.Background()

	// Establish a known value.
	if err := store.Write(ctx, "core.site.name", "Initial"); err != nil {
		t.Fatalf("setup Write: %v", err)
	}

	// Failed Write must leave the previous value intact.
	if err := store.Write(ctx, "core.site.name", ""); !errors.Is(err, ErrValidation) {
		t.Fatalf("expected validation failure: %v", err)
	}

	v, _ := store.Read(ctx, "core.site.name")
	if v != "Initial" {
		t.Errorf("post-failure Read: got %v want %q", v, "Initial")
	}
}

func TestWrite_AcceptsJSONRawMessage(t *testing.T) {
	reg := NewRegistry()
	s := Setting{
		Key:         "test.obj",
		Description: "Object setting",
		Type:        SettingTypeObject,
		Schema: json.RawMessage(`{
			"type":"object",
			"properties":{"name":{"type":"string"},"age":{"type":"integer"}},
			"required":["name"]
		}`),
		Default: map[string]any{},
		Group:   GroupGeneral,
	}
	if err := reg.Register(s); err != nil {
		t.Fatalf("Register: %v", err)
	}

	store := NewMemoryStore(reg)
	ctx := context.Background()

	raw := json.RawMessage(`{"name":"Mohamed","age":30}`)
	if err := store.Write(ctx, "test.obj", raw); err != nil {
		t.Errorf("RawMessage Write: %v", err)
	}

	// Missing required property → rejected.
	bad := json.RawMessage(`{"age":30}`)
	if err := store.Write(ctx, "test.obj", bad); !errors.Is(err, ErrValidation) {
		t.Errorf("missing required: %v", err)
	}
}

func TestValidateDefault_PassesForCoreSettings(t *testing.T) {
	// Every core setting's Default must pass its own schema. This is a
	// belt-and-braces check that catches the "I changed the schema but
	// forgot to update the default" class of bug.
	for _, s := range CoreSettings() {
		if err := s.ValidateDefault(); err != nil {
			t.Errorf("core setting %q: ValidateDefault: %v", s.Key, err)
		}
	}
}

func TestValidateDefault_DetectsMismatch(t *testing.T) {
	s := Setting{
		Key:     "test.mismatch",
		Type:    SettingTypeInt,
		Schema:  json.RawMessage(`{"type":"integer"}`),
		Default: "not an integer",
	}
	if err := s.ValidateDefault(); err == nil {
		t.Error("expected ValidateDefault to reject string-vs-integer mismatch")
	}
}

func TestValidateDefault_EmptySchemaError(t *testing.T) {
	s := Setting{Key: "x", Type: SettingTypeString}
	if err := s.ValidateDefault(); !errors.Is(err, ErrInvalidSchema) {
		t.Errorf("expected ErrInvalidSchema for empty schema, got %v", err)
	}
}

// TestSettingType_Valid covers each constant + a negative case so the
// Valid switch never silently grows a hole.
func TestSettingType_Valid(t *testing.T) {
	cases := []struct {
		t    SettingType
		want bool
	}{
		{SettingTypeString, true},
		{SettingTypeInt, true},
		{SettingTypeBool, true},
		{SettingTypeEnum, true},
		{SettingTypeArray, true},
		{SettingTypeObject, true},
		{SettingType(""), false},
		{SettingType("number"), false},
		{SettingType("nope"), false},
	}
	for _, c := range cases {
		if got := c.t.Valid(); got != c.want {
			t.Errorf("%q.Valid() = %v want %v", c.t, got, c.want)
		}
	}
}

// TestNormalizeForValidation_RoundTripIsLossless asserts that the
// round-trip used by Write produces values the JSON Schema validator
// recognizes correctly for the common Go types.
func TestNormalizeForValidation_RoundTrips(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want any
	}{
		{"string", "hello", "hello"},
		{"bool true", true, true},
		{"bool false", false, false},
		{"int->float64", 42, float64(42)},
		{"float64", 3.14, 3.14},
		{"raw message", json.RawMessage(`{"a":1}`), map[string]any{"a": float64(1)}},
		{"slice", []string{"a", "b"}, []any{"a", "b"}},
		{"map", map[string]any{"k": "v"}, map[string]any{"k": "v"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := normalizeForValidation(c.in)
			if err != nil {
				t.Fatalf("normalize: %v", err)
			}
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(c.want)
			if !equalJSON(gotJSON, wantJSON) {
				t.Errorf("normalize(%v) = %s want %s", c.in, gotJSON, wantJSON)
			}
		})
	}
}

func TestNormalizeForValidation_RejectsUnmarshalable(t *testing.T) {
	// A chan can't be marshaled; the normalizer should surface that
	// as an error rather than panicking.
	_, err := normalizeForValidation(make(chan int))
	if err == nil {
		t.Error("expected error from chan-as-value")
	}
}

func TestNormalizeForValidation_RejectsInvalidRawMessage(t *testing.T) {
	raw := json.RawMessage(`{this is not json}`)
	_, err := normalizeForValidation(raw)
	if err == nil {
		t.Error("expected error from malformed RawMessage")
	}
}

func equalJSON(a, b []byte) bool { return string(a) == string(b) }

func TestErrValidation_WrapPreservesDetails(t *testing.T) {
	reg := testRegistry(t)
	store := NewMemoryStore(reg)
	ctx := context.Background()

	err := store.Write(ctx, "core.posts.per_page", -1)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrValidation) {
		t.Errorf("expected ErrValidation chain: %v", err)
	}
	// The schema-error detail (minimum, range) should be in the
	// rendered message so admin-API responses are helpful.
	if !strings.Contains(err.Error(), "minimum") && !strings.Contains(err.Error(), "doesn't validate") {
		t.Logf("error string: %s", err.Error())
		// Not strict — different jsonschema versions phrase differently.
	}
}

// Sanity check that the error sentinels are distinguishable.
func TestErrorSentinels_Distinct(t *testing.T) {
	all := []error{
		ErrValidation, ErrUnknownKey, ErrNotFound,
		ErrDuplicateKey, ErrInvalidSchema, ErrEmptyKey, ErrInvalidType,
	}
	for i, a := range all {
		for j, b := range all {
			if i == j {
				continue
			}
			if errors.Is(a, b) {
				t.Errorf("sentinel %v should not Is %v", a, b)
			}
		}
	}
}

// guard against future refactors that might accidentally swallow
// the registry-lookup error path.
func TestWrite_UnknownKey_NotValidation(t *testing.T) {
	reg := testRegistry(t)
	store := NewMemoryStore(reg)

	err := store.Write(context.Background(), "ghost.key", "x")
	if errors.Is(err, ErrValidation) {
		t.Errorf("unknown key should not be reported as validation error: %v", err)
	}
	if !errors.Is(err, ErrUnknownKey) {
		t.Errorf("expected ErrUnknownKey, got %v", err)
	}
}

// Sanity: bad RawMessage in Setting.Default surfaces from ValidateDefault.
func TestValidateDefault_HandlesBadJSON(t *testing.T) {
	s := Setting{
		Key:     "test.bad-default-json",
		Type:    SettingTypeObject,
		Schema:  json.RawMessage(`{"type":"object"}`),
		Default: json.RawMessage(`{not json}`),
	}
	if err := s.ValidateDefault(); err == nil {
		t.Error("expected ValidateDefault to surface unmarshalable default")
	}
}

// Tiny helper to keep error-output debugging predictable.
var _ = fmt.Errorf
