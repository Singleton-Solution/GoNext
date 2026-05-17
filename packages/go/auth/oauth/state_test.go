package oauth

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestState_EntropyAndEncoding(t *testing.T) {
	s, err := State()
	if err != nil {
		t.Fatalf("State: %v", err)
	}

	// base64url, unpadded — must round-trip via base64.RawURLEncoding.
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		t.Fatalf("State output %q not base64url: %v", s, err)
	}
	if len(raw) < 32 {
		t.Errorf("State entropy = %d bytes (%d bits), want ≥ 32 bytes (256 bits)", len(raw), len(raw)*8)
	}
	// URL-safe: must not contain '+' or '/' (those are stdlib base64,
	// not base64url) and must not contain '=' (we use Raw encoding).
	for _, ch := range []string{"+", "/", "="} {
		if strings.Contains(s, ch) {
			t.Errorf("State output contains URL-unsafe character %q: %q", ch, s)
		}
	}
}

func TestState_Uniqueness(t *testing.T) {
	// Generate a batch and make sure they're all distinct. With 256-bit
	// entropy a collision is statistically impossible; if we see one it
	// means rand.Read is broken.
	const n = 1024
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		s, err := State()
		if err != nil {
			t.Fatalf("State[%d]: %v", i, err)
		}
		if _, dup := seen[s]; dup {
			t.Fatalf("State collision at i=%d: %q", i, s)
		}
		seen[s] = struct{}{}
	}
}

func TestNonce_SameShapeAsState(t *testing.T) {
	// Nonce shares the entropy budget and encoding with State.
	n, err := Nonce()
	if err != nil {
		t.Fatalf("Nonce: %v", err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(n)
	if err != nil {
		t.Fatalf("Nonce output %q not base64url: %v", n, err)
	}
	if len(raw) < 32 {
		t.Errorf("Nonce entropy = %d bytes, want ≥ 32", len(raw))
	}
}
