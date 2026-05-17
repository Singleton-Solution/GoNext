package revisions

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// canonicalize re-marshals JSON so byte-level comparisons aren't fooled
// by formatting differences. ApplyDelta returns minified output, the
// inputs in these tests are also minified, but anyone editing the
// test fixtures shouldn't have to count whitespace.
func canonicalize(t *testing.T, raw json.RawMessage) []byte {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("canonicalize: %v\nraw=%s", err, raw)
	}
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("canonicalize remarshal: %v", err)
	}
	return out
}

func TestComputeDelta_RoundTrip(t *testing.T) {
	cases := []struct {
		name string
		a, b json.RawMessage
	}{
		{
			name: "scalar field flip",
			a:    json.RawMessage(`{"title":"hello","body":"x"}`),
			b:    json.RawMessage(`{"title":"world","body":"x"}`),
		},
		{
			name: "array element insert",
			a:    json.RawMessage(`{"blocks":[{"id":"a"},{"id":"c"}]}`),
			b:    json.RawMessage(`{"blocks":[{"id":"a"},{"id":"b"},{"id":"c"}]}`),
		},
		{
			name: "array element remove",
			a:    json.RawMessage(`{"blocks":[{"id":"a"},{"id":"b"},{"id":"c"}]}`),
			b:    json.RawMessage(`{"blocks":[{"id":"a"},{"id":"c"}]}`),
		},
		{
			name: "deep nested edit",
			a:    json.RawMessage(`{"root":{"children":[{"k":1},{"k":2}]}}`),
			b:    json.RawMessage(`{"root":{"children":[{"k":1},{"k":99}]}}`),
		},
		{
			name: "no change emits empty patch",
			a:    json.RawMessage(`{"x":1,"y":[1,2,3]}`),
			b:    json.RawMessage(`{"x":1,"y":[1,2,3]}`),
		},
		{
			name: "empty object to populated",
			a:    json.RawMessage(`{}`),
			b:    json.RawMessage(`{"k":"v"}`),
		},
		{
			name: "json array root",
			a:    json.RawMessage(`[1,2,3]`),
			b:    json.RawMessage(`[1,2,3,4]`),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			delta, err := ComputeDelta(tc.a, tc.b)
			if err != nil {
				t.Fatalf("ComputeDelta: %v", err)
			}
			applied, err := ApplyDelta(tc.a, delta)
			if err != nil {
				t.Fatalf("ApplyDelta: %v", err)
			}
			if !bytes.Equal(canonicalize(t, applied), canonicalize(t, tc.b)) {
				t.Errorf("round-trip mismatch:\n got=%s\nwant=%s", applied, tc.b)
			}
		})
	}
}

func TestComputeDelta_RejectsInvalidJSON(t *testing.T) {
	cases := []struct {
		name string
		a, b json.RawMessage
	}{
		{"a not json", json.RawMessage(`{x}`), json.RawMessage(`{}`)},
		{"b not json", json.RawMessage(`{}`), json.RawMessage(`{x}`)},
		{"both empty", json.RawMessage{}, json.RawMessage{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ComputeDelta(tc.a, tc.b)
			if !errors.Is(err, ErrInvalidDelta) {
				t.Errorf("expected ErrInvalidDelta, got %v", err)
			}
		})
	}
}

func TestApplyDelta_RejectsInvalidInputs(t *testing.T) {
	t.Run("base not json", func(t *testing.T) {
		_, err := ApplyDelta(json.RawMessage(`{x}`), json.RawMessage(`[]`))
		if !errors.Is(err, ErrInvalidDelta) {
			t.Errorf("expected ErrInvalidDelta, got %v", err)
		}
	})
	t.Run("malformed patch", func(t *testing.T) {
		_, err := ApplyDelta(json.RawMessage(`{}`), json.RawMessage(`not-a-patch`))
		if !errors.Is(err, ErrInvalidDelta) {
			t.Errorf("expected ErrInvalidDelta, got %v", err)
		}
	})
	t.Run("patch references missing path", func(t *testing.T) {
		// "remove" on a non-existent path should fail.
		_, err := ApplyDelta(
			json.RawMessage(`{"a":1}`),
			json.RawMessage(`[{"op":"remove","path":"/nope"}]`),
		)
		if !errors.Is(err, ErrInvalidDelta) {
			t.Errorf("expected ErrInvalidDelta, got %v", err)
		}
	})
}

func TestApplyDelta_EmptyDeltaIsNoOp(t *testing.T) {
	// A nil / empty delta is treated as no-op so callers that route
	// Materialize through ApplyDelta on a snapshot don't have to
	// branch on emptiness.
	base := json.RawMessage(`{"k":"v"}`)
	out, err := ApplyDelta(base, nil)
	if err != nil {
		t.Fatalf("nil delta: %v", err)
	}
	if !bytes.Equal(canonicalize(t, out), canonicalize(t, base)) {
		t.Errorf("nil delta mutated base: got=%s want=%s", out, base)
	}

	out, err = ApplyDelta(base, json.RawMessage(`[]`))
	if err != nil {
		t.Fatalf("empty array delta: %v", err)
	}
	if !bytes.Equal(canonicalize(t, out), canonicalize(t, base)) {
		t.Errorf("empty delta mutated base: got=%s want=%s", out, base)
	}
}

func TestComputeDelta_NoChangeEmitsEmptyArray(t *testing.T) {
	// The persisted JSONB column must never be NULL when the row
	// claims to be a delta. ComputeDelta returns "[]" for the no-op
	// case rather than nil to make that invariant hold.
	a := json.RawMessage(`{"k":"v"}`)
	delta, err := ComputeDelta(a, a)
	if err != nil {
		t.Fatalf("ComputeDelta: %v", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(string(delta)), "[") {
		t.Errorf("expected empty JSON array, got %s", delta)
	}
	if len(delta) == 0 {
		t.Error("delta should not be empty bytes — must be at least []")
	}
}
