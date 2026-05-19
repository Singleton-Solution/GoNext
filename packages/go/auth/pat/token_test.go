package pat

import (
	"strings"
	"testing"
	"time"
)

// TestNew_TokenShape asserts the canonical shape every issued plaintext
// MUST satisfy: namespace prefix, exact total length, base62 tail. Any
// future change to the alphabet or width is a wire-format break and
// must update this test deliberately.
func TestNew_TokenShape(t *testing.T) {
	t.Parallel()
	plaintext, row, hash, err := New("user:1", "ci-token", []string{"read"}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !strings.HasPrefix(plaintext, "gnp_") {
		t.Fatalf("token missing gnp_ namespace: %q", plaintext)
	}
	if got, want := len(plaintext), MinTokenLen; got != want {
		t.Fatalf("token length: got %d want %d", got, want)
	}
	tail := plaintext[len("gnp_"):]
	for i, c := range []byte(tail) {
		switch {
		case c >= '0' && c <= '9':
		case c >= 'A' && c <= 'Z':
		case c >= 'a' && c <= 'z':
		default:
			t.Fatalf("non-base62 byte at index %d: %q", i, c)
		}
	}
	if got, want := row.Prefix, tail[:PrefixLen]; got != want {
		t.Fatalf("prefix: got %q want %q", got, want)
	}
	if len(hash) == 0 {
		t.Fatal("hash must be non-empty")
	}
}

// TestNew_RejectsEmptyInputs guards against silently issuing
// unattributable tokens.
func TestNew_RejectsEmptyInputs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, userID, label string
	}{
		{"no-user", "", "label"},
		{"no-label", "user:1", "   "},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if _, _, _, err := New(c.userID, c.label, nil, nil); err == nil {
				t.Fatalf("expected error")
			}
		})
	}
}

// TestNew_Uniqueness — two consecutive issues must not collide. A
// collision would mean broken entropy.
func TestNew_Uniqueness(t *testing.T) {
	t.Parallel()
	const N = 64
	seen := make(map[string]struct{}, N)
	for i := 0; i < N; i++ {
		p, _, _, err := New("user:1", "x", nil, nil)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if _, dup := seen[p]; dup {
			t.Fatalf("duplicate token: %q", p)
		}
		seen[p] = struct{}{}
	}
}

// TestHashVerifyRoundtrip asserts that VerifyHash returns true for the
// original plaintext and false for any tampered candidate.
func TestHashVerifyRoundtrip(t *testing.T) {
	t.Parallel()
	plaintext, _, hash, err := New("user:1", "x", nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !VerifyHash(hash, plaintext) {
		t.Fatal("VerifyHash must return true for the original plaintext")
	}
	if VerifyHash(hash, plaintext+"a") {
		t.Fatal("VerifyHash returned true for tampered plaintext")
	}
	// Flip a byte in the stored hash; must reject.
	tamp := make([]byte, len(hash))
	copy(tamp, hash)
	tamp[0] ^= 0xFF
	if VerifyHash(tamp, plaintext) {
		t.Fatal("VerifyHash returned true after stored hash was tampered")
	}
}

// TestVerifyHash_RejectsBadLength guards against the case where a
// migration accidentally truncates the hash column.
func TestVerifyHash_RejectsBadLength(t *testing.T) {
	t.Parallel()
	if VerifyHash([]byte("short"), "gnp_abc") {
		t.Fatal("VerifyHash must reject short stored bytes")
	}
	if VerifyHash(nil, "gnp_abc") {
		t.Fatal("VerifyHash must reject nil stored bytes")
	}
}

// TestValidShape covers the shape gate the middleware uses to skip the
// argon2 cost on obvious garbage.
func TestValidShape(t *testing.T) {
	t.Parallel()
	plaintext, _, _, err := New("user:1", "x", nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cases := []struct {
		name   string
		token  string
		wantOK bool
	}{
		{"valid", plaintext, true},
		{"empty", "", false},
		{"no-namespace", strings.TrimPrefix(plaintext, "gnp_"), false},
		{"wrong-namespace", "abc_" + strings.TrimPrefix(plaintext, "gnp_"), false},
		{"too-short", "gnp_short", false},
		{"non-base62", "gnp_" + strings.Repeat("!", 32), false},
		{"trailing-space", plaintext + " ", false},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := ValidShape(c.token); got != c.wantOK {
				t.Fatalf("ValidShape(%q) = %v want %v", c.token, got, c.wantOK)
			}
		})
	}
}

// TestParseBearer covers the Authorization-header trimming used by the
// middleware. Case-insensitive scheme match is mandatory (RFC 7235).
func TestParseBearer(t *testing.T) {
	t.Parallel()
	cases := []struct {
		header, want string
		ok           bool
	}{
		{"Bearer gnp_abc", "gnp_abc", true},
		{"bearer gnp_abc", "gnp_abc", true},
		{"BEARER gnp_abc", "gnp_abc", true},
		{"", "", false},
		{"Bearer", "", false},
		{"Basic abc", "", false},
		{"Bearer ", "", false},
	}
	for _, c := range cases {
		c := c
		t.Run(c.header, func(t *testing.T) {
			t.Parallel()
			got, ok := ParseBearer(c.header)
			if got != c.want || ok != c.ok {
				t.Fatalf("ParseBearer(%q) = (%q,%v) want (%q,%v)",
					c.header, got, ok, c.want, c.ok)
			}
		})
	}
}

// TestPrefixOf returns the 8-char display prefix and "" for malformed
// inputs.
func TestPrefixOf(t *testing.T) {
	t.Parallel()
	plaintext, row, _, err := New("user:1", "x", nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got, want := PrefixOf(plaintext), row.Prefix; got != want {
		t.Fatalf("PrefixOf: got %q want %q", got, want)
	}
	if got := PrefixOf("invalid"); got != "" {
		t.Fatalf("PrefixOf(invalid) = %q want empty", got)
	}
}

// TestRowFieldsPopulated checks that New stamps every field needed for
// the INSERT.
func TestRowFieldsPopulated(t *testing.T) {
	t.Parallel()
	expiry := time.Now().Add(24 * time.Hour).UTC()
	_, row, _, err := New("user:1", "ci ", []string{"posts.read", "posts.write"}, &expiry)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if row.UserID != "user:1" {
		t.Fatalf("UserID: %q", row.UserID)
	}
	if row.Name != "ci" {
		t.Fatalf("Name should be trimmed: %q", row.Name)
	}
	if got, want := len(row.Scopes), 2; got != want {
		t.Fatalf("Scopes len: %d want %d", got, want)
	}
	if row.ExpiresAt == nil || !row.ExpiresAt.Equal(expiry) {
		t.Fatalf("ExpiresAt: %v want %v", row.ExpiresAt, expiry)
	}
	if row.CreatedAt.IsZero() {
		t.Fatal("CreatedAt must be set")
	}
}
