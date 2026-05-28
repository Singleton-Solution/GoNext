package router

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestEncodeCursor_RoundTrip(t *testing.T) {
	t.Parallel()

	cases := []string{
		"",
		"019a1bcd-3a4f-7c7e-89c5-2a8b3c4d5e6f",
		"a",
		"plain text",
		"abc/+=?$%",
	}
	for _, raw := range cases {
		encoded := EncodeCursor(raw)
		decoded, err := ParseCursor(encoded)
		if err != nil {
			t.Fatalf("raw=%q: ParseCursor(%q): %v", raw, encoded, err)
		}
		if decoded != raw {
			t.Errorf("raw=%q: round-trip = %q", raw, decoded)
		}
	}
}

func TestEncodeCursor_Empty(t *testing.T) {
	t.Parallel()

	if got := EncodeCursor(""); got != "" {
		t.Errorf("EncodeCursor(\"\") = %q, want empty", got)
	}
}

func TestParseCursor_Empty(t *testing.T) {
	t.Parallel()

	v, err := ParseCursor("")
	if err != nil {
		t.Errorf("ParseCursor(\"\") err = %v", err)
	}
	if v != "" {
		t.Errorf("ParseCursor(\"\") = %q, want empty", v)
	}
}

func TestParseCursor_Invalid(t *testing.T) {
	t.Parallel()

	_, err := ParseCursor("!!!not-valid-base64!!!")
	if !errors.Is(err, ErrInvalidCursor) {
		t.Errorf("err = %v, want ErrInvalidCursor", err)
	}
}

func TestEncodeCursor_NoPadding(t *testing.T) {
	t.Parallel()

	// The on-wire form is unpadded to keep cursors URL-friendly without
	// percent-encoded equals signs.
	encoded := EncodeCursor("abc")
	for _, c := range encoded {
		if c == '=' {
			t.Errorf("cursor %q contains padding char =", encoded)
		}
	}
}

func TestPage_MarshalJSON_NilDataBecomesEmptyArray(t *testing.T) {
	t.Parallel()

	p := Page[string]{Data: nil, Pagination: PageInfo{}}
	out, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"data":[]`) {
		t.Errorf("expected data:[], got: %s", string(out))
	}
	if strings.Contains(string(out), `"data":null`) {
		t.Errorf("expected no null, got: %s", string(out))
	}
}

func TestPage_MarshalJSON_NonNilDataPreserved(t *testing.T) {
	t.Parallel()

	p := Page[string]{Data: []string{"a", "b"}, Pagination: PageInfo{}}
	out, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"data":["a","b"]`) {
		t.Errorf("expected data:[a,b], got: %s", string(out))
	}
}
