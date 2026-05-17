package idempotency

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestValidateKeyValue_AcceptsTypicalUUID is the happy-path smoke
// test. A UUID v4 is the canonical idempotency key shape.
func TestValidateKeyValue_AcceptsTypicalUUID(t *testing.T) {
	t.Parallel()
	if err := ValidateKeyValue("550e8400-e29b-41d4-a716-446655440000"); err != nil {
		t.Fatalf("ValidateKeyValue: %v", err)
	}
}

// TestValidateKeyValue_Rejects sweeps the malformed cases. We
// table-drive them so a future contributor adding a new rejection
// (NUL byte, BOM, etc.) only adds one row.
func TestValidateKeyValue_Rejects(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		v    string
	}{
		{"empty", ""},
		{"too long", strings.Repeat("a", MaxKeyLength+1)},
		{"leading whitespace", " abc"},
		{"trailing whitespace", "abc "},
		{"newline", "ab\nc"},
		{"tab", "ab\tc"},
		{"DEL byte", "ab\x7Fc"},
		{"NUL byte", "ab\x00c"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateKeyValue(tc.v)
			if !errors.Is(err, ErrInvalidKey) {
				t.Fatalf("ValidateKeyValue(%q): want ErrInvalidKey, got %v", tc.v, err)
			}
		})
	}
}

// TestHashRequest_Deterministic confirms equivalent requests produce
// equal hashes. This is the property the middleware relies on to
// detect replays — if it isn't deterministic the cache is useless.
func TestHashRequest_Deterministic(t *testing.T) {
	t.Parallel()
	a := HashRequest("POST", "/api/payments?dry_run=true", []byte(`{"amount":42}`))
	b := HashRequest("POST", "/api/payments?dry_run=true", []byte(`{"amount":42}`))
	if !bytes.Equal(a, b) {
		t.Fatalf("HashRequest: not deterministic")
	}
	if len(a) != sha256.Size {
		t.Fatalf("HashRequest: length %d, want %d", len(a), sha256.Size)
	}
}

// TestHashRequest_DifferentMethodPathBody verifies the inverse — any
// component change shifts the hash. We don't try to fingerprint the
// full mathematical avalanche, just that the obvious axes all matter.
func TestHashRequest_DifferentMethodPathBody(t *testing.T) {
	t.Parallel()
	base := HashRequest("POST", "/a", []byte("x"))
	for _, tc := range []struct {
		name string
		hash []byte
	}{
		{"different method", HashRequest("PUT", "/a", []byte("x"))},
		{"different path", HashRequest("POST", "/b", []byte("x"))},
		{"different body", HashRequest("POST", "/a", []byte("y"))},
		{"different query", HashRequest("POST", "/a?q=1", []byte("x"))},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if bytes.Equal(base, tc.hash) {
				t.Fatalf("expected different hash for %s", tc.name)
			}
		})
	}
}

// TestNewKeyFromRequest_NoHeader returns ok=false with no error. The
// middleware uses this to short-circuit the pipeline for non-opt-in
// requests.
func TestNewKeyFromRequest_NoHeader(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest("POST", "/payments", strings.NewReader("{}"))
	k, ok, err := NewKeyFromRequest(r, DefaultMaxBodySize)
	if err != nil {
		t.Fatalf("NewKeyFromRequest: %v", err)
	}
	if ok {
		t.Fatalf("ok = true, want false")
	}
	if k.Value != "" || len(k.RequestHash) != 0 {
		t.Fatalf("Key not zero: %+v", k)
	}
}

// TestNewKeyFromRequest_ReplacesBody is the load-bearing test for
// the middleware: NewKeyFromRequest reads the body to hash it, but
// the downstream handler must still see the same bytes. Without the
// swap, every protected route gets an empty body.
func TestNewKeyFromRequest_ReplacesBody(t *testing.T) {
	t.Parallel()
	const payload = `{"amount":42,"currency":"USD"}`
	r := httptest.NewRequest("POST", "/payments", strings.NewReader(payload))
	r.Header.Set(HeaderName, "abc-123-def-456")

	k, ok, err := NewKeyFromRequest(r, DefaultMaxBodySize)
	if err != nil {
		t.Fatalf("NewKeyFromRequest: %v", err)
	}
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if k.Value != "abc-123-def-456" {
		t.Fatalf("Value: %q", k.Value)
	}
	if len(k.RequestHash) != sha256.Size {
		t.Fatalf("hash length %d", len(k.RequestHash))
	}

	read, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("ReadAll(r.Body): %v", err)
	}
	if string(read) != payload {
		t.Fatalf("body after replace = %q, want %q", read, payload)
	}
}

// TestNewKeyFromRequest_RejectsOversizedBody confirms the 413 path
// triggers before we hash a multi-gigabyte upload into memory.
func TestNewKeyFromRequest_RejectsOversizedBody(t *testing.T) {
	t.Parallel()
	body := strings.Repeat("x", 256)
	r := httptest.NewRequest("POST", "/payments", strings.NewReader(body))
	r.Header.Set(HeaderName, "abc-123")

	_, _, err := NewKeyFromRequest(r, 100)
	if !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("NewKeyFromRequest: want ErrBodyTooLarge, got %v", err)
	}
}

// TestNewKeyFromRequest_RejectsInvalidHeader exercises the
// validation branch that comes BEFORE the body read — a malformed
// header must not consume the body.
func TestNewKeyFromRequest_RejectsInvalidHeader(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest("POST", "/payments", strings.NewReader("{}"))
	r.Header.Set(HeaderName, "")

	// An empty header is treated as "absent" because Header.Get
	// returns "" for both. The interesting case is a literally
	// invalid header.
	r.Header.Set(HeaderName, "ab\nc")

	_, _, err := NewKeyFromRequest(r, DefaultMaxBodySize)
	if !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("NewKeyFromRequest: want ErrInvalidKey, got %v", err)
	}
}

// TestStatus_Valid sanity-checks the enum membership.
func TestStatus_Valid(t *testing.T) {
	t.Parallel()
	for _, st := range []Status{StatusInProgress, StatusSucceeded, StatusFailed} {
		if !st.Valid() {
			t.Errorf("%q: Valid = false", st)
		}
	}
	if Status("bogus").Valid() {
		t.Error("bogus: Valid = true")
	}
}
