package pat

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
)

// Namespace is the mandatory prefix on every PAT plaintext. The leading
// "gnp_" lets secret-scanners (GitHub's, Gitleaks', custom log scrubbers)
// pattern-match a leaked credential without false positives against
// arbitrary base62. Changing this prefix is a breaking change for every
// downstream scanner — it is a constant for a reason.
const Namespace = "gnp_"

// suffixBytes is the entropy in the random tail of the token. 24 bytes
// → 32 base62 characters, matching the width recommended by OWASP for
// long-lived bearer tokens. The total token width is then
//
//	len("gnp_") + 32 = 36 characters
//
// which fits comfortably in a single `Authorization: Bearer ...` header
// and inside the 64-character ceiling most password managers honour.
const suffixBytes = 24

// suffixLen is the encoded length of the random tail. base62 doesn't
// have a built-in encoding in the stdlib; we use base64.RawURLEncoding
// over the same entropy and then strip the non-base62 characters,
// rerolling until the result is the exact width. Because the alphabet
// reduction is a ~95% acceptance rate, the loop terminates on the first
// iteration in practice; the cap is paranoia.
const suffixLen = 32

// PrefixLen is the on-disk length of the prefix column. The first 8
// characters of the random tail are what the list view renders as
// "gnp_AbCdEfGh…". Exposed as a const so the storage layer doesn't
// hard-code the slice bound.
const PrefixLen = 8

// MinTokenLen is the minimum length a string must have to even be
// considered a candidate token. Used by the middleware to fail fast on
// obvious garbage before paying the argon2 cost.
//
//	len("gnp_") + suffixLen = 4 + 32 = 36
const MinTokenLen = len(Namespace) + suffixLen

// base62Alphabet is the canonical 62-character alphabet (digits,
// uppercase, lowercase). The order is deliberately A-Z before a-z to
// match RFC 4648's base32hex convention for visual symmetry.
const base62Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// ErrInvalid is returned by token-parsing helpers when the input does
// not look like a PAT. It is intentionally a sentinel so the middleware
// can branch with errors.Is and return 401 without leaking the reason.
var ErrInvalid = errors.New("pat: invalid token")

// ErrExpired is returned by Lookup when the stored row is past its
// expires_at. Surfaced as 401 to the client; the middleware logs
// "token expired" for the audit trail.
var ErrExpired = errors.New("pat: token expired")

// ErrRevoked is returned by Lookup when the stored row has a non-NULL
// revoked_at. Distinct from ErrInvalid so the audit log can show
// "revoked-token replay" — a stronger signal than "garbage input".
var ErrRevoked = errors.New("pat: token revoked")

// PAT is the canonical in-memory representation of a stored Personal
// Access Token. It mirrors the personal_access_tokens table 1:1 minus
// the hash (kept inside the store, never on the Principal).
//
// The zero value is not a valid PAT; use Store.Lookup to obtain one.
type PAT struct {
	// ID is the row's UUID v7. Stable across token rotations (which
	// don't exist for PATs, but the column is the audit-log handle).
	ID string

	// UserID is the owner. The middleware uses this to load the user's
	// effective capabilities and intersect with Scopes.
	UserID string

	// Name is the operator-chosen label, surfaced in the "my tokens"
	// list. Trimmed-non-empty at the table CHECK.
	Name string

	// Prefix is the first 8 chars of the random tail, for display only.
	// Never used for authentication.
	Prefix string

	// Scopes is the explicit capability slug list the token may exercise.
	// Intersected with the user's effective caps at request time.
	Scopes []string

	// CreatedAt is when the row was inserted.
	CreatedAt time.Time

	// LastUsedAt is refreshed on every successful authentication, with
	// a 60s write-amplification throttle. Nil = never used.
	LastUsedAt *time.Time

	// ExpiresAt is the optional absolute expiry. Nil = never expires.
	ExpiresAt *time.Time

	// RevokedAt is the manual-revoke marker. Non-nil = treated as 401.
	RevokedAt *time.Time
}

// argon2Params is the cost knob set for PAT hashing. We deliberately
// pick the same memory / time / parallelism as packages/go/auth/password
// (m=65536, t=3, p=2) so the two surfaces share an attack-cost profile
// — an adversary who can crack one can crack the other, which means
// hardening the password parameters automatically hardens PATs too.
//
// The salt is fixed at 16 bytes per OWASP guidance; the output key is
// 32 bytes to match SHA-256 width for downstream byte-slice handling.
type argon2Params struct {
	memory      uint32
	iterations  uint32
	parallelism uint8
	saltLen     uint32
	keyLen      uint32
}

// productionArgon2Params are the runtime-grade cost knobs documented in
// docs/06-auth-permissions.md §2.2. Tests swap these for testArgon2Params
// via UseTestParams; production code never calls UseTestParams.
var productionArgon2Params = argon2Params{
	memory:      64 * 1024,
	iterations:  3,
	parallelism: 2,
	saltLen:     16,
	keyLen:      32,
}

// testArgon2Params are a deliberately cheap variant used only by the
// package's own tests. The combinatorics of N parallel Issue+Lookup
// calls would otherwise blow the CI test budget (each argon2id verify
// is ~30ms at production cost). Tests still exercise the same hashing
// code path; only the cost knobs change. See UseTestParams.
var testArgon2Params = argon2Params{
	memory:      8,
	iterations:  1,
	parallelism: 1,
	saltLen:     16,
	keyLen:      32,
}

// defaultArgon2Params is the live cost set used by Hash and VerifyHash.
// Mutable only via UseTestParams (which is package-private and only
// called from _test.go files via TestMain).
var defaultArgon2Params = productionArgon2Params

// useTestParams swaps in the cheaper test cost set. Returns a restore
// function the caller MUST defer. Package-private; tests only.
func useTestParams() func() {
	defaultArgon2Params = testArgon2Params
	return func() { defaultArgon2Params = productionArgon2Params }
}

// New mints a fresh PAT plaintext for the given owner with the given
// name, scopes, and optional expiry. It returns:
//
//   - plaintext: the full "gnp_..." string. Show to the operator ONCE
//     and then forget it. The caller hands this to Store.Issue.
//   - row: the PAT row to persist. The Hash field on row is the bytes
//     to put in the personal_access_tokens.hash column; everything
//     else maps to its same-named column.
//   - err: only from crypto/rand. A non-nil err means no plaintext
//     was issued and the caller must not retry with the partial row.
//
// New does not touch the database. The caller is responsible for the
// INSERT; this split lets the test pack the row into a fixture without
// running migrations.
func New(userID, name string, scopes []string, expiresAt *time.Time) (plaintext string, row PAT, hash []byte, err error) {
	if userID == "" {
		return "", PAT{}, nil, fmt.Errorf("pat: userID is required")
	}
	if strings.TrimSpace(name) == "" {
		return "", PAT{}, nil, fmt.Errorf("pat: name must be non-empty")
	}

	suffix, err := randomBase62(suffixLen)
	if err != nil {
		return "", PAT{}, nil, fmt.Errorf("pat: random suffix: %w", err)
	}

	plaintext = Namespace + suffix
	hash, err = Hash(plaintext)
	if err != nil {
		return "", PAT{}, nil, fmt.Errorf("pat: hash: %w", err)
	}

	// Copy scopes so the caller can mutate their slice without
	// surprising the persisted row. Defensive — costs nothing.
	sc := make([]string, len(scopes))
	copy(sc, scopes)

	row = PAT{
		UserID:    userID,
		Name:      strings.TrimSpace(name),
		Prefix:    suffix[:PrefixLen],
		Scopes:    sc,
		CreatedAt: time.Now().UTC(),
		ExpiresAt: expiresAt,
	}
	return plaintext, row, hash, nil
}

// Hash derives the argon2id storage hash for a plaintext token.
//
// The output is a self-contained PHC-style byte payload:
//
//	salt(16) || key(32) = 48 bytes
//
// We do not emit the textual PHC envelope ($argon2id$v=19$m=...$) the
// password package uses because the parameters are fixed (the password
// package supports parameter migration, PATs don't — we'll bump on a
// breaking change instead of doing it row-by-row). The fixed layout
// halves the on-disk footprint and lets the UNIQUE index on
// personal_access_tokens.hash do collision detection without parsing.
//
// Hash never returns the empty slice; an empty plaintext is a bug the
// caller should detect upstream, but we still compute against it so a
// constant-time comparison at Lookup doesn't reveal the difference.
func Hash(plaintext string) ([]byte, error) {
	salt := make([]byte, defaultArgon2Params.saltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("pat: read salt: %w", err)
	}
	key := argon2.IDKey(
		[]byte(plaintext),
		salt,
		defaultArgon2Params.iterations,
		defaultArgon2Params.memory,
		defaultArgon2Params.parallelism,
		defaultArgon2Params.keyLen,
	)
	out := make([]byte, 0, len(salt)+len(key))
	out = append(out, salt...)
	out = append(out, key...)
	return out, nil
}

// VerifyHash returns true iff the candidate plaintext hashes (under the
// stored salt) to the same key as the stored hash bytes. The comparison
// is constant-time so a timing-side-channel can't differentiate "wrong
// salt position" from "right token, wrong tail".
//
// A stored slice of an unexpected length returns false (and never
// false-positives) — the salt+key layout is a fixed 48 bytes; anything
// else is corruption or a forged probe.
func VerifyHash(stored []byte, candidate string) bool {
	saltLen := int(defaultArgon2Params.saltLen)
	keyLen := int(defaultArgon2Params.keyLen)
	if len(stored) != saltLen+keyLen {
		return false
	}
	salt := stored[:saltLen]
	want := stored[saltLen:]
	got := argon2.IDKey(
		[]byte(candidate),
		salt,
		defaultArgon2Params.iterations,
		defaultArgon2Params.memory,
		defaultArgon2Params.parallelism,
		defaultArgon2Params.keyLen,
	)
	return subtle.ConstantTimeCompare(want, got) == 1
}

// ParseBearer trims the "Bearer " prefix from an Authorization header
// value and returns the candidate token. It does NOT validate the
// shape beyond the prefix and the minimum length — the cheap shape
// check is in ValidShape, called next in the middleware.
//
// An empty header or one that doesn't start with "Bearer " returns
// ("", false) — the middleware then falls through to other auth
// schemes (cookies) without raising.
func ParseBearer(header string) (string, bool) {
	const prefix = "Bearer "
	if len(header) <= len(prefix) {
		return "", false
	}
	if !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}
	return header[len(prefix):], true
}

// ValidShape reports whether s looks like a PAT (right namespace,
// right minimum length, base62 tail). It does NOT prove the token is
// live — only that calling Lookup against it is worthwhile. The
// middleware uses this gate so an "Authorization: Bearer hello" header
// doesn't pay the argon2 cost.
func ValidShape(s string) bool {
	if len(s) < MinTokenLen {
		return false
	}
	if !strings.HasPrefix(s, Namespace) {
		return false
	}
	tail := s[len(Namespace):]
	if len(tail) != suffixLen {
		return false
	}
	for i := 0; i < len(tail); i++ {
		c := tail[i]
		switch {
		case c >= '0' && c <= '9':
		case c >= 'A' && c <= 'Z':
		case c >= 'a' && c <= 'z':
		default:
			return false
		}
	}
	return true
}

// PrefixOf extracts the 8-char display prefix from a plaintext token.
// Returns "" if the token shape is wrong; callers should have already
// gated with ValidShape but we don't trust that across package
// boundaries.
func PrefixOf(s string) string {
	if !ValidShape(s) {
		return ""
	}
	return s[len(Namespace) : len(Namespace)+PrefixLen]
}

// randomBase62 returns n base62 characters drawn from a CSPRNG. The
// implementation reads ceil(n*log2(62)/8) random bytes, rejects samples
// outside the 62-multiple window, and loops until n acceptance.
// Rejection sampling is the only bias-free way to map a power-of-two
// random byte to a non-power-of-two alphabet.
//
// An adversary cannot influence the loop count because the random
// source is crypto/rand. The expected wall time is constant for the
// suffix lengths we care about (32 chars takes ~24-32 bytes of entropy).
func randomBase62(n int) (string, error) {
	// Read in chunks slightly larger than strictly needed so the loop
	// almost never has to refill. The 95% acceptance rate means a chunk
	// of n bytes yields roughly 0.95n base62 characters; we round up.
	out := make([]byte, 0, n)
	buf := make([]byte, n+8)
	for len(out) < n {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		for _, b := range buf {
			// 62 * 4 = 248. Any byte >= 248 is biased; reject it.
			// (4 = floor(256/62); the largest unbiased multiple is 248.)
			if b >= 248 {
				continue
			}
			out = append(out, base62Alphabet[b%62])
			if len(out) == n {
				break
			}
		}
	}
	return string(out), nil
}

// EncodeHashForDebug returns a base64 representation of a hash. Used
// only by tests and the diagnostics endpoint; the runtime path never
// surfaces the hash bytes outside the store.
func EncodeHashForDebug(b []byte) string {
	return base64.RawStdEncoding.EncodeToString(b)
}
