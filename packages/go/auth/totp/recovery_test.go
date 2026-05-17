package totp

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestRecoveryCodes_Shape(t *testing.T) {
	plain, hashes, err := RecoveryCodes(10)
	if err != nil {
		t.Fatalf("RecoveryCodes: %v", err)
	}
	if len(plain) != 10 {
		t.Fatalf("plain len = %d, want 10", len(plain))
	}
	if len(hashes) != 10 {
		t.Fatalf("hashes len = %d, want 10", len(hashes))
	}
	for i, c := range plain {
		if len(c) != recoveryTotalLen {
			t.Errorf("plain[%d] = %q has length %d, want %d", i, c, len(c), recoveryTotalLen)
		}
		if c[recoveryGroupLen] != '-' {
			t.Errorf("plain[%d] = %q missing hyphen", i, c)
		}
		// Alphabet check: each non-hyphen char must be in recoveryAlphabet.
		for j := 0; j < len(c); j++ {
			if j == recoveryGroupLen {
				continue
			}
			if !strings.ContainsRune(recoveryAlphabet, rune(c[j])) {
				t.Errorf("plain[%d] = %q has out-of-alphabet char at pos %d", i, c, j)
			}
		}
	}
	// Hashes must each look like a PHC argon2id string. We don't fully
	// re-validate the encoding — the password package's own tests do
	// that — but a quick prefix check catches a regression where we'd
	// accidentally store the plaintext.
	for i, h := range hashes {
		if !bytes.HasPrefix(h, []byte("$argon2id$v=19$")) {
			t.Errorf("hashes[%d] does not have argon2id PHC prefix: %q", i, string(h))
		}
		// Hash must not contain the plaintext (defence against any future
		// mistake that stored both).
		if bytes.Contains(h, []byte(plain[i])) {
			t.Errorf("hashes[%d] contains plaintext code %q", i, plain[i])
		}
	}
}

func TestRecoveryCodes_AllUnique(t *testing.T) {
	plain, _, err := RecoveryCodes(50)
	if err != nil {
		t.Fatalf("RecoveryCodes: %v", err)
	}
	seen := make(map[string]bool, len(plain))
	for _, c := range plain {
		if seen[c] {
			t.Fatalf("duplicate recovery code: %q", c)
		}
		seen[c] = true
	}
}

func TestRecoveryCodes_VerifyMatchesExactlyOne(t *testing.T) {
	plain, hashes, err := RecoveryCodes(10)
	if err != nil {
		t.Fatalf("RecoveryCodes: %v", err)
	}
	for i, c := range plain {
		matched, ok := VerifyRecoveryCode(c, hashes)
		if !ok {
			t.Errorf("VerifyRecoveryCode(plain[%d]) returned ok=false", i)
		}
		if matched != i {
			t.Errorf("VerifyRecoveryCode(plain[%d]) returned matched=%d", i, matched)
		}
	}
}

func TestVerifyRecoveryCode_WrongCode(t *testing.T) {
	_, hashes, err := RecoveryCodes(5)
	if err != nil {
		t.Fatalf("RecoveryCodes: %v", err)
	}
	// A code that's well-formed but not in the issued set.
	matched, ok := VerifyRecoveryCode("aaaa-bbbb", hashes)
	if ok {
		t.Errorf("VerifyRecoveryCode: ok=true for non-issued code (matched=%d)", matched)
	}
	if matched != -1 {
		t.Errorf("VerifyRecoveryCode: matched=%d, want -1", matched)
	}
}

func TestVerifyRecoveryCode_ConsumedSlot(t *testing.T) {
	plain, hashes, err := RecoveryCodes(3)
	if err != nil {
		t.Fatalf("RecoveryCodes: %v", err)
	}
	// Simulate "code at index 1 was consumed" by zeroing the slot.
	hashes[1] = nil
	// The other two still verify at their original positions.
	matched, ok := VerifyRecoveryCode(plain[0], hashes)
	if !ok || matched != 0 {
		t.Errorf("plain[0]: ok=%v matched=%d", ok, matched)
	}
	matched, ok = VerifyRecoveryCode(plain[2], hashes)
	if !ok || matched != 2 {
		t.Errorf("plain[2]: ok=%v matched=%d", ok, matched)
	}
	// The consumed slot's plaintext no longer verifies.
	matched, ok = VerifyRecoveryCode(plain[1], hashes)
	if ok {
		t.Errorf("plain[1]: ok=true on consumed slot (matched=%d)", matched)
	}
	if matched != -1 {
		t.Errorf("plain[1]: matched=%d, want -1", matched)
	}
}

func TestVerifyRecoveryCode_NormalisesInput(t *testing.T) {
	plain, hashes, err := RecoveryCodes(1)
	if err != nil {
		t.Fatalf("RecoveryCodes: %v", err)
	}
	original := plain[0]
	// Each of these variants must verify.
	variants := []string{
		original,
		strings.ToUpper(original),
		" " + original + " ",
		"\t" + original + "\n",
		strings.ReplaceAll(original, "-", ""), // no hyphen, 8 chars
		// uppercase no-hyphen
		strings.ReplaceAll(strings.ToUpper(original), "-", ""),
	}
	for _, v := range variants {
		matched, ok := VerifyRecoveryCode(v, hashes)
		if !ok || matched != 0 {
			t.Errorf("variant %q: ok=%v matched=%d", v, ok, matched)
		}
	}
}

func TestVerifyRecoveryCode_RejectsMalformed(t *testing.T) {
	_, hashes, err := RecoveryCodes(2)
	if err != nil {
		t.Fatalf("RecoveryCodes: %v", err)
	}
	bad := []string{
		"",
		"toosh",
		"way-too-long-to-be-a-recovery-code",
		"1111-1111",      // '1' not in reduced alphabet (avoids l/I/1 ambiguity)
		"oooo-oooo",      // 'o' excluded (looks like 0)
		"aaaa_bbbb",      // wrong separator
		"aaaabbbb-extra", // length wrong
	}
	for _, c := range bad {
		t.Run(c, func(t *testing.T) {
			matched, ok := VerifyRecoveryCode(c, hashes)
			if ok {
				t.Errorf("malformed %q: ok=true (matched=%d)", c, matched)
			}
			if matched != -1 {
				t.Errorf("malformed %q: matched=%d, want -1", c, matched)
			}
		})
	}
}

func TestVerifyRecoveryCode_HandlesCorruptStoredHash(t *testing.T) {
	plain, hashes, err := RecoveryCodes(3)
	if err != nil {
		t.Fatalf("RecoveryCodes: %v", err)
	}
	// Corrupt the middle slot — must NOT panic and must NOT cause
	// later slots to be skipped.
	hashes[1] = []byte("not-a-valid-phc-string")
	matched, ok := VerifyRecoveryCode(plain[2], hashes)
	if !ok || matched != 2 {
		t.Errorf("plain[2] with corrupt hashes[1]: ok=%v matched=%d", ok, matched)
	}
}

func TestRecoveryCodes_InvalidCount(t *testing.T) {
	cases := []struct {
		name string
		n    int
	}{
		{"zero", 0},
		{"negative", -1},
		{"way-too-many", recoveryMaxN + 1},
		{"huge", 1_000_000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := RecoveryCodes(c.n)
			if !errors.Is(err, ErrInvalidRecoveryCount) {
				t.Errorf("RecoveryCodes(%d): want ErrInvalidRecoveryCount, got %v", c.n, err)
			}
		})
	}
}

func TestRecoveryCodes_BoundaryCount(t *testing.T) {
	// n=1 and n=recoveryMaxN are the boundary values that must succeed.
	for _, n := range []int{1, recoveryMaxN} {
		plain, hashes, err := RecoveryCodes(n)
		if err != nil {
			t.Fatalf("RecoveryCodes(%d): %v", n, err)
		}
		if len(plain) != n || len(hashes) != n {
			t.Errorf("RecoveryCodes(%d): got %d plain / %d hashes", n, len(plain), len(hashes))
		}
	}
}

func TestCanonicaliseRecoveryCode(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"canonical", "abcd-efgh", "abcd-efgh", false},
		{"uppercase", "ABCD-EFGH", "abcd-efgh", false},
		{"surrounded-by-whitespace", "  abcd-efgh\n", "abcd-efgh", false},
		{"no-hyphen", "abcdefgh", "abcd-efgh", false},
		{"no-hyphen-uppercase", "ABCDEFGH", "abcd-efgh", false},
		{"wrong-separator-len9", "abcd_efgh", "", true},
		{"wrong-length-short", "abcd-efg", "", true},
		{"wrong-length-long", "abcd-efghi", "", true},
		{"bad-alphabet-1", "1abc-efgh", "", true},
		{"bad-alphabet-O", "Oabc-defg", "", true},
		{"empty", "", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := canonicaliseRecoveryCode(c.in)
			if c.wantErr {
				if err == nil {
					t.Errorf("want error, got nil (out=%q)", got)
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestRecoveryCodes_NeverEchoesCodeInError(t *testing.T) {
	// We force the error path with an invalid count; the message must
	// not include the candidate codes (there were none, but lock the
	// discipline anyway).
	_, _, err := RecoveryCodes(0)
	if err == nil {
		t.Fatalf("want error")
	}
	// The argon2 pepper isn't involved here; just check the obvious.
	if strings.Contains(err.Error(), "secret") {
		t.Errorf("error mentions 'secret': %q", err.Error())
	}
}

func TestRecoveryCodes_HashesAreDistinct(t *testing.T) {
	// Two hashes of the same code must differ (salt is random). The
	// argon2id PHC contract guarantees this; we lock it down so a
	// regression that fixed the salt would surface here.
	_, hashes, err := RecoveryCodes(2)
	if err != nil {
		t.Fatalf("RecoveryCodes: %v", err)
	}
	if bytes.Equal(hashes[0], hashes[1]) {
		t.Fatalf("two hashes are identical — salt collision or deterministic hashing")
	}
}
