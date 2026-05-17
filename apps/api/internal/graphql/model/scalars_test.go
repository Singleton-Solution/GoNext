package model_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/graphql/model"
)

// TestDateTimeRoundtrip: marshal then unmarshal yields the same UTC
// instant.
func TestDateTimeRoundtrip(t *testing.T) {
	t.Parallel()
	in := model.NewDateTime(time.Date(2026, 1, 2, 3, 4, 5, 678_000_000, time.UTC))
	var buf bytes.Buffer
	in.MarshalGQL(&buf)
	wire := buf.String()
	// MarshalGQL writes JSON-quoted RFC3339Nano. Strip the quotes
	// to feed UnmarshalGQL, which takes a Go string (the value the
	// JSON decoder hands gqlgen).
	if len(wire) < 2 || wire[0] != '"' || wire[len(wire)-1] != '"' {
		t.Fatalf("wire not quoted: %q", wire)
	}
	raw := wire[1 : len(wire)-1]
	var out model.DateTime
	if err := out.UnmarshalGQL(raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !out.Time.Equal(in.Time) {
		t.Errorf("roundtrip mismatch: in=%v out=%v", in.Time, out.Time)
	}
}

// TestDateTimeUnmarshalRejectsNonString: passing a non-string value
// returns an error rather than silently zeroing the field.
func TestDateTimeUnmarshalRejectsNonString(t *testing.T) {
	t.Parallel()
	var d model.DateTime
	if err := d.UnmarshalGQL(42); err == nil {
		t.Error("expected error for int input")
	}
	if err := d.UnmarshalGQL(""); err == nil {
		t.Error("expected error for empty string")
	}
	if err := d.UnmarshalGQL("not a date"); err == nil {
		t.Error("expected error for garbage input")
	}
}

// TestCursorRoundtrip: EncodeCursor / DecodeCursor are inverses, and
// the empty cursor decodes to the empty string (the "no cursor" form).
func TestCursorRoundtrip(t *testing.T) {
	t.Parallel()
	cases := []string{"", "foo", "abc:123", "a|b|c"}
	for _, raw := range cases {
		enc := model.EncodeCursor(raw)
		dec, err := model.DecodeCursor(enc)
		if err != nil {
			t.Errorf("%q: decode: %v", raw, err)
			continue
		}
		if dec != raw {
			t.Errorf("%q: roundtrip mismatch, got %q", raw, dec)
		}
	}
}

// TestCursorDecodeMalformed: a garbage cursor returns an error.
func TestCursorDecodeMalformed(t *testing.T) {
	t.Parallel()
	if _, err := model.DecodeCursor(model.Cursor("not-base64!")); err == nil {
		t.Error("expected error for malformed cursor")
	}
}
