package session

import (
	"encoding/base64"
	"testing"
)

func TestGenerateToken_LengthAndUniqueness(t *testing.T) {
	wantLen := base64.RawURLEncoding.EncodedLen(tokenBytes)
	seen := make(map[string]struct{}, 64)
	for i := 0; i < 64; i++ {
		tok, err := generateToken()
		if err != nil {
			t.Fatalf("generateToken: %v", err)
		}
		if got := len(tok); got != wantLen {
			t.Fatalf("token length: got %d want %d (token=%q)", got, wantLen, tok)
		}
		if _, dup := seen[tok]; dup {
			t.Fatalf("duplicate token after %d iterations: %q", i, tok)
		}
		seen[tok] = struct{}{}
	}
}

func TestGenerateToken_IsBase64URL(t *testing.T) {
	tok, err := generateToken()
	if err != nil {
		t.Fatalf("generateToken: %v", err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil {
		t.Fatalf("decode: %v (token=%q)", err, tok)
	}
	if len(raw) != tokenBytes {
		t.Fatalf("decoded length: got %d want %d", len(raw), tokenBytes)
	}
	// base64url uses '-' and '_' (no '+' or '/'), and is unpadded here.
	for _, r := range tok {
		switch {
		case r >= 'A' && r <= 'Z',
			r >= 'a' && r <= 'z',
			r >= '0' && r <= '9',
			r == '-', r == '_':
			// ok
		default:
			t.Fatalf("non-base64url byte %q in token %q", r, tok)
		}
	}
}

func TestValidToken(t *testing.T) {
	good, err := generateToken()
	if err != nil {
		t.Fatalf("generateToken: %v", err)
	}
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"good", good, true},
		{"empty", "", false},
		{"too short", "abc", false},
		{"too long", good + "x", false},
		{"std base64 chars", "++++++++++++++++++++++++++++++++++++++++++", false},
		{"padding char", "==========================================", false},
		{"path traversal", "../../etc/passwd", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := validToken(tc.in); got != tc.want {
				t.Fatalf("validToken(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
