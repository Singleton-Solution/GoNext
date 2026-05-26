package password

import (
	"crypto/md5" //nolint:gosec // phpass is intentionally MD5-based; we verify legacy WP hashes
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"
)

// Errors specific to the phpass verifier.
var (
	// ErrNotPhpass indicates the encoded string does not look like a
	// phpass record. Callers use errors.Is to dispatch to the
	// argon2id verifier when this hits.
	ErrNotPhpass = errors.New("password: not a phpass hash")

	// ErrPhpassMalformed indicates the prefix matched but the
	// remainder of the record is not a valid phpass encoding.
	ErrPhpassMalformed = errors.New("password: malformed phpass hash")
)

// phpassItoa64 is the alphabet phpass uses for its base64-ish output.
// It is NOT standard base64 — order and characters differ. Defined
// once at package scope so both encode and decode share a single
// source of truth.
//
// Reference: wp-includes/class-phpass.php in WordPress core, where
// the alphabet has been stable since 2008.
const phpassItoa64 = "./0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// phpassMinSettingLen is the minimum length of a phpass setting
// string (the part before the hash output): "$P$" + cost + 8-char
// salt = 12 characters.
const phpassMinSettingLen = 12

// phpassHashLen is the full encoded length: 12-char setting +
// 22-char hash output = 34 characters.
const phpassHashLen = 34

// PhpassPrefixes is the set of recognised phpass identifier prefixes.
// WordPress uses "$P$"; the Drupal fork also emits "$H$". We accept
// both because operators migrating from a multi-CMS install ought to
// be able to bulk-import either form.
var PhpassPrefixes = []string{"$P$", "$H$"}

// IsPhpass reports whether encoded looks like a phpass hash. This is
// a cheap prefix check used before the full Verify path runs; the
// dispatcher in apps/api/auth uses it to decide which verifier owns
// a given user_passwords row.
//
// A true return does not guarantee VerifyPhpass will succeed —
// malformed phpass strings will still parse-fail. False means
// "definitely not phpass; pass to argon2id".
func IsPhpass(encoded string) bool {
	if len(encoded) < phpassMinSettingLen {
		return false
	}
	for _, p := range PhpassPrefixes {
		if strings.HasPrefix(encoded, p) {
			return true
		}
	}
	return false
}

// VerifyPhpass checks password against a WordPress phpass hash.
//
// This is a READ-ONLY verifier: GoNext never writes a phpass hash.
// The expected lifecycle is:
//
//  1. WordPress migration imports legacy users with their phpass
//     hashes in users.legacy_phpass_hash.
//  2. On first login, the auth path calls VerifyPhpass.
//  3. On success, the caller computes Hash(password) and stores the
//     argon2id PHC into user_passwords; legacy_phpass_hash is
//     cleared in the same transaction.
//
// After step 3 the user is indistinguishable from a natively-created
// account and the next login goes through the normal argon2id path.
//
// VerifyPhpass returns:
//   - ok: true iff the password matches.
//   - err: non-nil when encoded is not a valid phpass string. A
//     password mismatch is not an error; it returns (false, nil).
//
// Constant-time comparison via subtle.ConstantTimeCompare. The
// password is never echoed in returned errors.
func VerifyPhpass(password, encoded string) (ok bool, err error) {
	if !IsPhpass(encoded) {
		return false, ErrNotPhpass
	}
	if len(encoded) != phpassHashLen {
		return false, fmt.Errorf("%w: length %d (want %d)", ErrPhpassMalformed, len(encoded), phpassHashLen)
	}
	// Setting layout: $P$<cost><8-byte-salt><22-byte-hash>
	// Index:           0123  4   5..........12 13..........34
	costChar := encoded[3]
	cost := strings.IndexByte(phpassItoa64, costChar)
	if cost < 7 || cost > 30 {
		// phpass uses cost values 7..30 (the encoded form is the
		// log2 of the iteration count). Outside that band the
		// reference implementation refuses to verify; we follow.
		return false, fmt.Errorf("%w: invalid cost char %q", ErrPhpassMalformed, costChar)
	}
	iterations := uint64(1) << uint(cost) //nolint:gosec // cost is constrained to 7..30

	salt := encoded[4:12]
	want := encoded[12:]

	got := phpassCrypt([]byte(password), []byte(salt), iterations)
	// Constant-time compare on the 22-byte encoded tail.
	if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
		return false, nil
	}
	return true, nil
}

// phpassCrypt is the phpass key derivation: MD5 stretched
// `iterations` times. The first round mixes salt+password; every
// subsequent round mixes the previous hash + password.
//
// This is intentionally weak by 2026 standards — that's the whole
// reason we re-hash to argon2id on first login. The CPU cost is
// bounded because cost is capped at 30 in VerifyPhpass.
func phpassCrypt(password, salt []byte, iterations uint64) string {
	h := md5.Sum(append(salt, password...)) //nolint:gosec // intentional: matches WordPress phpass
	for i := uint64(0); i < iterations; i++ {
		h = md5.Sum(append(h[:], password...)) //nolint:gosec // intentional
	}
	return phpassEncode64(h[:], 16)
}

// phpassEncode64 implements the phpass-specific base64 variant.
// It takes 16 input bytes and returns 22 output characters using
// the phpassItoa64 alphabet, packing 3 input bytes into 4 output
// chars (with a 1-byte+2-char tail).
//
// The bit ordering differs from RFC 4648 base64. This is the
// reference algorithm from wp-includes/class-phpass.php
// transcribed to Go.
func phpassEncode64(input []byte, count int) string {
	var out strings.Builder
	out.Grow((count*4 + 2) / 3)
	i := 0
	for i < count {
		val := uint32(input[i])
		i++
		out.WriteByte(phpassItoa64[val&0x3f])
		if i < count {
			val |= uint32(input[i]) << 8
		}
		out.WriteByte(phpassItoa64[(val>>6)&0x3f])
		if i >= count {
			break
		}
		i++
		if i < count {
			val |= uint32(input[i]) << 16
		}
		out.WriteByte(phpassItoa64[(val>>12)&0x3f])
		if i >= count {
			break
		}
		i++
		out.WriteByte(phpassItoa64[(val>>18)&0x3f])
	}
	return out.String()
}
