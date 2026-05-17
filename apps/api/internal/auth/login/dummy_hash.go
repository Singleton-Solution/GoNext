package login

import (
	"crypto/rand"
	"encoding/hex"
	"sync"

	"github.com/Singleton-Solution/GoNext/packages/go/auth/password"
)

// dummyHash is the pre-computed argon2id PHC string that the service
// hashes against when a user lookup misses. The point is constant
// time: the unknown-email code path must run the same expensive
// argon2 verify as the known-email-wrong-password path, otherwise an
// attacker can distinguish "user exists" from "user does not exist"
// by measuring wall-clock time.
//
// The hash is generated once on first use against a 32-byte random
// secret that is then discarded. Verification against this hash with
// any user-supplied password is overwhelmingly likely to return
// "no match" — the only way to "match" is to guess the discarded
// secret, which is cryptographic-randomness hard.
//
// We construct lazily (via sync.Once) rather than at init() so the
// startup cost of argon2 only lands when the first login fires; for
// a server that never receives a login request this saves ~50ms of
// boot time. The Once also gives us a free guard against a second
// goroutine racing in during the first call.
var (
	dummyHashOnce  sync.Once
	dummyHashValue string
	dummyHashErr   error
)

// getDummyHash returns the pre-computed dummy hash for the supplied
// pepper. We key the cache by pepper because the cache hit depends
// on the HMAC pre-hash being deterministic for a given (pepper,
// secret) pair; if the operator hot-rotates the pepper at runtime
// the dummy hash for the old pepper is invalidated.
//
// In practice the pepper is process-static (it's loaded from
// GONEXT_AUTH_PEPPER at boot) so the Once below seeds it on the
// first request and serves it for the lifetime of the process.
//
// Returns an error only if crypto/rand fails — same condition as the
// underlying password.Hash. A non-nil error from this function is
// catastrophic; the caller should bubble it as a 500.
func getDummyHash(pepper []byte) (string, error) {
	dummyHashOnce.Do(func() {
		// 32 bytes of crypto/rand → hex-encoded so it survives the
		// HMAC-SHA256 prehash without losing entropy. The exact form
		// doesn't matter; what matters is that no one — including us
		// — knows what plaintext hashes to dummyHashValue.
		var secret [32]byte
		if _, err := rand.Read(secret[:]); err != nil {
			dummyHashErr = err
			return
		}
		// hex.EncodeToString produces an ASCII string with 64 chars
		// for 32 bytes of input; safe to feed into password.Hash.
		h, err := password.Hash(hex.EncodeToString(secret[:]), pepper)
		if err != nil {
			dummyHashErr = err
			return
		}
		dummyHashValue = h
		// Zero the secret material before returning. Go can't guarantee
		// the compiler doesn't keep a copy in a register, but the
		// effort is cheap and the intent is clear.
		for i := range secret {
			secret[i] = 0
		}
	})
	if dummyHashErr != nil {
		return "", dummyHashErr
	}
	return dummyHashValue, nil
}
