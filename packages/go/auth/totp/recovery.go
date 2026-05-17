package totp

import (
	"crypto/rand"
	"errors"
	"fmt"
	"strings"

	"github.com/Singleton-Solution/GoNext/packages/go/auth/password"
)

// Errors specific to recovery codes.
var (
	// ErrInvalidRecoveryCount indicates n was outside the supported
	// [1, 100] range. Ten is the convention (see
	// docs/06-auth-permissions.md §4.5); the upper bound is a
	// defensive cap to keep argon2 cost predictable.
	ErrInvalidRecoveryCount = errors.New("totp: invalid recovery code count")

	// ErrInvalidRecoveryCode indicates the supplied plaintext doesn't
	// match the recovery-code shape (xxxx-xxxx, lowercase alphanumeric
	// from the reduced alphabet). Returned only from canonicaliseRecoveryCode;
	// VerifyRecoveryCode swallows it and returns (-1, false) so callers
	// don't have to branch.
	ErrInvalidRecoveryCode = errors.New("totp: invalid recovery code")
)

// Recovery-code shape constants.
const (
	// recoveryGroupLen is the number of characters per group (so
	// total length is 2*recoveryGroupLen + 1 for the separator).
	recoveryGroupLen = 4

	// recoveryTotalLen is the visible length of a recovery code,
	// including the hyphen.
	recoveryTotalLen = 2*recoveryGroupLen + 1 // "xxxx-xxxx" = 9

	// recoveryMaxN caps the number of codes a single call may produce.
	// Far above the 10 we issue in practice, but keeps argon2 cost
	// bounded if an absent-minded caller asks for a million.
	recoveryMaxN = 100
)

// recoveryAlphabet is the reduced alphabet used for recovery codes.
// Excludes visually-ambiguous glyphs: 0/O/o, 1/l/I/i, plus uppercase
// (codes are emitted lowercase). What's left: 2-9 and the consonants
// b,c,d,f,g,h,j,k,m,n,p,q,r,s,t,v,w,x,y,z plus a (which is unambiguous).
// 30 distinct symbols × 8 positions = ~39 bits of entropy per code.
// That's well above what argon2id+random-storage needs against online
// attack, and a bit below pure 10-char random base32 — the readability
// gain is worth the trade.
const recoveryAlphabet = "abcdefghjkmnpqrstvwxyz23456789"

// RecoveryCodes generates n random recovery codes and their argon2id
// hashes. The plaintext slice is what you show to the user ONE TIME
// at enrolment; the hashes are what you store.
//
// Hashes use packages/go/auth/password.Hash with an empty pepper — the
// pepper-via-HMAC pre-hash that protects user passwords from offline
// crack with a leaked database is appropriate when the cleartext is
// a low-entropy human secret. Recovery codes ARE high-entropy random
// strings; the argon2id work factor alone is sufficient. (We do not
// have an established per-user pepper rotation story for these.)
//
// Errors: only from crypto/rand or from password.Hash (which propagates
// crypto/rand). The codes never appear in any returned error.
func RecoveryCodes(n int) (plaintext []string, hashes [][]byte, err error) {
	if n <= 0 || n > recoveryMaxN {
		return nil, nil, fmt.Errorf("%w: %d (want 1..%d)", ErrInvalidRecoveryCount, n, recoveryMaxN)
	}

	plain := make([]string, 0, n)
	seen := make(map[string]struct{}, n)

	// Generate distinct codes. With ~39 bits of entropy per code and
	// n <= 100 the birthday probability of a collision is negligible
	// (~10^-9), but we de-dup anyway: returning two identical codes
	// would silently consume the same recovery slot twice, which is
	// the kind of bug that's painful to debug from the outside.
	for len(plain) < n {
		code, gerr := generateRecoveryCode()
		if gerr != nil {
			return nil, nil, gerr
		}
		if _, dup := seen[code]; dup {
			continue
		}
		seen[code] = struct{}{}
		plain = append(plain, code)
	}

	hashed := make([][]byte, 0, n)
	for _, code := range plain {
		// Empty pepper: see the function doc above.
		h, herr := password.Hash(code, nil)
		if herr != nil {
			return nil, nil, fmt.Errorf("totp: hash recovery code: %w", herr)
		}
		hashed = append(hashed, []byte(h))
	}

	return plain, hashed, nil
}

// VerifyRecoveryCode checks plaintextCode against each entry in
// storedHashes and returns the index of the first match (-1 if none).
//
// The check is constant-time per candidate via the underlying argon2id
// verify; we cannot avoid early-exit at the slice level without doing
// fixed-cost-N work for every call, which would make a 10-code list
// take 10x the time of a successful match. The realistic threat we
// guard against is offline crack of a leaked hashes table — for that,
// the argon2 cost per candidate is the relevant defence, and that
// defence is identical whether we iterate or fan out.
//
// Non-canonical input (uppercase, missing hyphen, surrounding
// whitespace) is normalised before comparison: the user has just typed
// this off a printed sheet under pressure, and we don't want a case-shift
// to lock them out. Anything that doesn't normalise to a valid code
// shape returns (-1, false) — same as a hash miss.
func VerifyRecoveryCode(plaintextCode string, storedHashes [][]byte) (matched int, ok bool) {
	code, err := canonicaliseRecoveryCode(plaintextCode)
	if err != nil {
		return -1, false
	}

	for i, h := range storedHashes {
		if len(h) == 0 {
			// Empty entry = consumed slot. Skip without calling Verify
			// (which would return an error on a malformed PHC string
			// and we'd silently move on anyway).
			continue
		}
		matched, _, verr := password.Verify(code, string(h), nil)
		if verr != nil {
			// A malformed stored hash is a programmer/storage bug —
			// don't fail the whole verification on it, just skip.
			// The realistic effect is "this slot acts as already-consumed"
			// which is the safe direction.
			continue
		}
		if matched {
			return i, true
		}
	}
	return -1, false
}

// generateRecoveryCode emits one code in the canonical xxxx-xxxx shape.
// We sample uniformly from recoveryAlphabet using crypto/rand; the
// rejection-sampling pattern is required to avoid the modulo bias that
// `b % len(alphabet)` would introduce when len(alphabet) is not a power
// of two (here it's 30 — a power of two would be ideal, but the
// readability-driven alphabet has the size it has).
func generateRecoveryCode() (string, error) {
	const alphaLen = len(recoveryAlphabet)
	// Largest multiple of alphaLen that fits in a byte. Bytes >= this
	// threshold are rejected and re-rolled to keep the distribution
	// uniform. With alphaLen=30, threshold = 240 = 30*8, so we reject
	// ~6% of draws — fine.
	const threshold = 256 - (256 % alphaLen)

	var out strings.Builder
	out.Grow(recoveryTotalLen)

	// 8 characters split as 4-4. The hyphen sits in the middle.
	for pos := 0; pos < 2*recoveryGroupLen; pos++ {
		if pos == recoveryGroupLen {
			out.WriteByte('-')
		}
		var b [1]byte
		for {
			if _, err := rand.Read(b[:]); err != nil {
				return "", fmt.Errorf("totp: read random for recovery code: %w", err)
			}
			if int(b[0]) < threshold {
				out.WriteByte(recoveryAlphabet[int(b[0])%alphaLen])
				break
			}
			// rejected — re-roll
		}
	}
	return out.String(), nil
}

// canonicaliseRecoveryCode normalises user input to the canonical
// "xxxx-xxxx" lowercase form before hash comparison. We:
//
//   - lowercase
//   - strip ASCII whitespace (helps with paste artefacts and the
//     occasional "1234 5678" finger-mistake)
//   - require exactly one '-' splitting two 4-char groups (or accept
//     a no-hyphen form by re-inserting one if the input is exactly 8
//     characters of alphabet — common when copy-pasting from
//     password managers that strip punctuation)
//   - validate the alphabet
//
// Returns ErrInvalidRecoveryCode if the input cannot be normalised.
func canonicaliseRecoveryCode(s string) (string, error) {
	// Strip whitespace.
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			continue
		}
		b.WriteRune(r)
	}
	t := strings.ToLower(b.String())

	switch len(t) {
	case recoveryTotalLen: // "xxxx-xxxx"
		if t[recoveryGroupLen] != '-' {
			return "", fmt.Errorf("%w: missing hyphen at pos %d", ErrInvalidRecoveryCode, recoveryGroupLen)
		}
	case 2 * recoveryGroupLen: // "xxxxxxxx" — re-insert hyphen
		t = t[:recoveryGroupLen] + "-" + t[recoveryGroupLen:]
	default:
		return "", fmt.Errorf("%w: length %d", ErrInvalidRecoveryCode, len(t))
	}

	// Alphabet check (skip the hyphen).
	for i := 0; i < len(t); i++ {
		if i == recoveryGroupLen {
			continue
		}
		if !strings.ContainsRune(recoveryAlphabet, rune(t[i])) {
			return "", fmt.Errorf("%w: char at pos %d not in alphabet", ErrInvalidRecoveryCode, i)
		}
	}
	return t, nil
}
