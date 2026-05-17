package verify

import (
	"encoding/base64"
	"testing"
)

func TestGenerateToken_LengthAndShape(t *testing.T) {
	tok, err := generateToken()
	if err != nil {
		t.Fatalf("generateToken: %v", err)
	}
	if !validToken(tok) {
		t.Fatalf("generateToken returned invalid shape: %q", tok)
	}
	raw, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(raw) != tokenBytes {
		t.Errorf("decoded length: got %d want %d", len(raw), tokenBytes)
	}
}

func TestGenerateToken_Uniqueness(t *testing.T) {
	seen := map[string]struct{}{}
	for i := 0; i < 1000; i++ {
		tok, err := generateToken()
		if err != nil {
			t.Fatalf("generateToken: %v", err)
		}
		if _, dup := seen[tok]; dup {
			t.Fatalf("token collision at iteration %d: %s", i, tok)
		}
		seen[tok] = struct{}{}
	}
}

func TestHashToken_Stable(t *testing.T) {
	a := hashToken("hello")
	b := hashToken("hello")
	if a != b {
		t.Errorf("hashToken not stable: %q != %q", a, b)
	}
	if hashToken("hello") == hashToken("world") {
		t.Errorf("hashToken collision")
	}
}

func TestConstantTimeEqual(t *testing.T) {
	if !constantTimeEqual("abc", "abc") {
		t.Errorf("equal strings reported unequal")
	}
	if constantTimeEqual("abc", "abd") {
		t.Errorf("unequal strings reported equal")
	}
	if constantTimeEqual("abc", "abcd") {
		t.Errorf("different lengths reported equal")
	}
}

func TestValidToken(t *testing.T) {
	tok, _ := generateToken()
	if !validToken(tok) {
		t.Errorf("real token rejected")
	}
	if validToken("") {
		t.Errorf("empty accepted")
	}
	if validToken("too-short") {
		t.Errorf("short string accepted")
	}
	if validToken(tok + "garbage") {
		t.Errorf("garbage suffix accepted")
	}
}
