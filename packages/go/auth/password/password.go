package password

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Errors returned by this package. Callers should compare with errors.Is
// rather than string-matching — error messages may grow more detail over
// time but the sentinel set is stable.
var (
	// ErrMalformedHash indicates the encoded string is not a valid PHC
	// argon2id record. The wrapped error gives a short reason
	// (segment count, base64 decode failure, malformed parameter list)
	// without echoing user input.
	ErrMalformedHash = errors.New("password: malformed encoded hash")

	// ErrUnsupportedAlgorithm indicates the encoded string is a PHC
	// record for an algorithm this package does not implement. Only
	// "argon2id" is supported; argon2i and argon2d are rejected.
	ErrUnsupportedAlgorithm = errors.New("password: unsupported algorithm")

	// ErrUnsupportedVersion indicates the PHC record uses an argon2
	// version we don't speak. We accept v=19 (0x13) only; that's what
	// golang.org/x/crypto/argon2 produces and what RFC 9106 standardises.
	ErrUnsupportedVersion = errors.New("password: unsupported argon2 version")
)

// argon2Version is the on-the-wire version number from RFC 9106 (0x13).
// golang.org/x/crypto/argon2 exposes this as argon2.Version.
const argon2Version = argon2.Version

// Hash hashes password with argon2id using DefaultParams and the given
// pepper. The pepper is mixed in via HMAC-SHA256(pepper, password) so it
// is bound to the input — knowledge of the database alone is not enough
// to attempt an offline crack.
//
// pepper may be empty; if so the HMAC step still runs (with an empty key)
// but the package offers no protection against database-only theft. In
// production GONEXT_AUTH_PEPPER is required (see packages/go/config).
//
// The returned string is the PHC encoding:
//
//	$argon2id$v=19$m=65536,t=3,p=2$<salt>$<hash>
//
// salt and hash are raw (unpadded) standard base64.
//
// Errors: only from crypto/rand. The password itself can never cause an
// error here, and is never echoed in returned messages.
func Hash(password string, pepper []byte) (string, error) {
	return hashWithParams(password, pepper, DefaultParams)
}

// hashWithParams is Hash with the cost knobs exposed. Kept package-private
// because callers should not be picking cost ad-hoc — the only legitimate
// use is from tests that want a cheap-to-run set, and from internal
// re-hashing.
func hashWithParams(password string, pepper []byte, p Params) (string, error) {
	salt := make([]byte, p.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("password: generate salt: %w", err)
	}

	prehash := prehashPepper(pepper, password)
	key := argon2.IDKey(prehash, salt, p.Iterations, p.Memory, p.Parallelism, p.KeyLen)

	return encode(p, salt, key), nil
}

// Verify checks password against an encoded PHC argon2id record produced
// by Hash. It returns:
//
//   - ok: true iff the password matches.
//   - needsRehash: true iff the encoded hash uses parameters weaker than
//     the current DefaultParams (only meaningful when ok is true).
//   - err: non-nil on malformed input. A wrong password is not an error;
//     it returns (false, false, nil).
//
// Verify uses subtle.ConstantTimeCompare for the hash comparison. The
// password and pepper are never echoed in returned errors.
func Verify(password, encoded string, pepper []byte) (ok bool, needsRehash bool, err error) {
	p, salt, want, err := decode(encoded)
	if err != nil {
		return false, false, err
	}

	prehash := prehashPepper(pepper, password)
	got := argon2.IDKey(prehash, salt, p.Iterations, p.Memory, p.Parallelism, p.KeyLen)

	if subtle.ConstantTimeCompare(got, want) != 1 {
		return false, false, nil
	}
	return true, p.weakerThan(DefaultParams), nil
}

// prehashPepper computes HMAC-SHA256(pepper, password). Doing the HMAC
// before argon2 means the pepper is bound to the password input: an
// attacker with the database but not the pepper can't even start an
// offline guess.
//
// Using SHA-256 (32-byte output) means the actual password length is
// hidden from argon2 — argon2 always sees 32 bytes regardless of whether
// the user typed "hi" or a 200-character passphrase. This also caps the
// memory argon2 needs to read from the password, which closes a subtle
// timing channel.
func prehashPepper(pepper []byte, password string) []byte {
	h := hmac.New(sha256.New, pepper)
	// hash.Hash.Write never returns an error; ignoring it is idiomatic.
	_, _ = h.Write([]byte(password))
	return h.Sum(nil)
}

// encode produces the PHC string for argon2id. Salt and hash use raw
// (unpadded) standard base64, matching the reference C library and
// libsodium.
func encode(p Params, salt, key []byte) string {
	b64 := base64.RawStdEncoding
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2Version,
		p.Memory, p.Iterations, p.Parallelism,
		b64.EncodeToString(salt),
		b64.EncodeToString(key),
	)
}

// Floors used by decode to reject PHC strings that would otherwise reach
// argon2.IDKey with out-of-range arguments. argon2.IDKey panics on
// parallelism==0, iterations==0, and very low memory; we reject these in
// decode so callers always get a clean error rather than a crash.
//
// These are validation floors, NOT recommended values. Production hashing
// uses DefaultParams which is well above all of these.
const (
	minDecodeMemory      uint32 = 8       // KiB; argon2 requires >= 8*p, we treat 8 as the floor
	minDecodeIterations  uint32 = 1       // argon2.IDKey panics on 0
	minDecodeParallelism uint8  = 1       // argon2.IDKey panics on 0; uint8 already caps at 255
	minDecodeSaltLen     int    = 8       // RFC 9106 §3.1 minimum salt
	minDecodeKeyLen      int    = 4       // argon2 minimum tag length
	maxDecodeMemory      uint32 = 1 << 22 // 4 GiB ceiling: prevents an attacker-supplied m=4294967295 OOMing the verifier
)

// decode parses an argon2id PHC string. Returns the params, salt, key
// (the expected hash), or an error sentinel wrapped with a short reason.
//
// The format is strict: exactly six segments split on '$' (the first is
// empty because the string starts with '$'), algorithm "argon2id",
// version 19, three named params in m,t,p order, and two base64 blobs.
// Anything else is ErrMalformedHash. argon2i / argon2d are rejected with
// ErrUnsupportedAlgorithm.
//
// Parameter values are also validated against floors that match what
// argon2.IDKey will accept without panicking — see the constants above.
// This is a hard requirement for an auth primitive: a crafted row in
// users.password_hash (via SQLi, leaked credential, or a bad migration)
// must NEVER crash the verifier and break login for everyone.
func decode(encoded string) (Params, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" {
		return Params{}, nil, nil, fmt.Errorf("%w: want 5 segments", ErrMalformedHash)
	}

	switch parts[1] {
	case "argon2id":
		// ok
	case "argon2i", "argon2d":
		return Params{}, nil, nil, fmt.Errorf("%w: %s", ErrUnsupportedAlgorithm, parts[1])
	default:
		return Params{}, nil, nil, fmt.Errorf("%w: %q", ErrUnsupportedAlgorithm, parts[1])
	}

	// Strict parse of the version segment. fmt.Sscanf with "v=%d" will
	// silently accept trailing garbage like "v=19trailing" (it parses 19
	// and discards the rest). We use scanExact to require that the entire
	// segment was consumed.
	var version int
	if err := scanExact(parts[2], "v=%d", &version); err != nil {
		return Params{}, nil, nil, fmt.Errorf("%w: version segment: %w", ErrMalformedHash, err)
	}
	// A negative version is malformed (RFC 9106 versions are unsigned),
	// not "unsupported" — surface it as ErrMalformedHash for clarity.
	if version < 0 {
		return Params{}, nil, nil, fmt.Errorf("%w: negative version v=%d", ErrMalformedHash, version)
	}
	if version != argon2Version {
		return Params{}, nil, nil, fmt.Errorf("%w: v=%d", ErrUnsupportedVersion, version)
	}

	var p Params
	var mem, iter uint32
	var par uint8
	if err := scanExact(parts[3], "m=%d,t=%d,p=%d", &mem, &iter, &par); err != nil {
		return Params{}, nil, nil, fmt.Errorf("%w: parameter segment: %w", ErrMalformedHash, err)
	}
	// Validate parameter ranges BEFORE returning success. argon2.IDKey
	// panics on iterations==0, parallelism==0, or memory below 8*p, and
	// can OOM on absurdly large memory. We must reject all of these here.
	//
	// Note: par is uint8, so the [1,255] check reduces to par != 0. uint8
	// already enforces the upper bound at parse time. We still spell out
	// the intent for the reader and for future-proofing if the type ever
	// widens.
	if mem < minDecodeMemory {
		return Params{}, nil, nil, fmt.Errorf("%w: memory %d KiB below floor %d", ErrMalformedHash, mem, minDecodeMemory)
	}
	if mem > maxDecodeMemory {
		return Params{}, nil, nil, fmt.Errorf("%w: memory %d KiB above ceiling %d", ErrMalformedHash, mem, maxDecodeMemory)
	}
	if iter < minDecodeIterations {
		return Params{}, nil, nil, fmt.Errorf("%w: iterations %d below floor %d", ErrMalformedHash, iter, minDecodeIterations)
	}
	if par < minDecodeParallelism {
		return Params{}, nil, nil, fmt.Errorf("%w: parallelism %d below floor %d", ErrMalformedHash, par, minDecodeParallelism)
	}
	p.Memory, p.Iterations, p.Parallelism = mem, iter, par

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return Params{}, nil, nil, fmt.Errorf("%w: salt base64: %w", ErrMalformedHash, err)
	}
	if len(salt) < minDecodeSaltLen {
		return Params{}, nil, nil, fmt.Errorf("%w: salt %d bytes below floor %d", ErrMalformedHash, len(salt), minDecodeSaltLen)
	}
	p.SaltLen = uint32(len(salt))

	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return Params{}, nil, nil, fmt.Errorf("%w: hash base64: %w", ErrMalformedHash, err)
	}
	if len(key) < minDecodeKeyLen {
		return Params{}, nil, nil, fmt.Errorf("%w: hash %d bytes below floor %d", ErrMalformedHash, len(key), minDecodeKeyLen)
	}
	p.KeyLen = uint32(len(key))

	return p, salt, key, nil
}

// scanExact is fmt.Sscanf with the additional constraint that the entire
// source string must be consumed by the format directives. The stdlib
// Sscanf silently tolerates trailing characters (e.g. "v=19trailing"
// parses 19 and discards the rest). For PHC parsing we want strictness:
// any unexpected suffix is ErrMalformedHash.
//
// We implement this by appending "%s" to the format and rejecting any
// non-empty tail. We use a single tail variable so the caller's arg list
// stays clean.
func scanExact(src, format string, args ...any) error {
	var tail string
	scanArgs := make([]any, 0, len(args)+1)
	scanArgs = append(scanArgs, args...)
	scanArgs = append(scanArgs, &tail)
	// Sscanf returns the count of successfully-scanned items. We don't
	// rely on the count; we rely on (a) Sscanf returning no error and
	// (b) tail being empty (or whitespace-only — Sscanf treats %s as
	// "next whitespace-delimited token", so if there's no trailing
	// non-space, %s simply fails to scan and we get io.EOF, which we
	// treat as "fully consumed").
	_, err := fmt.Sscanf(src, format+"%s", scanArgs...)
	if err != nil {
		// io.EOF from %s means the format directives consumed everything
		// before we got to %s — that's the success case for us.
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	// %s matched something — trailing garbage.
	return fmt.Errorf("unexpected trailing input %q", tail)
}
