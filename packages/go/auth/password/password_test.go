package password

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// testParams is a cheap argon2id cost profile for tests. The production
// DefaultParams allocate 64 MiB and take ~hundreds of ms per call; that
// would make `go test -race -count=1` painful. The crypto being exercised
// is identical — only the cost knobs change.
var testParams = Params{
	Memory:      8 * 1024, // 8 MiB
	Iterations:  1,
	Parallelism: 1,
	SaltLen:     16,
	KeyLen:      32,
}

func TestHashVerify_Roundtrip(t *testing.T) {
	pepper := []byte("test-pepper-not-used-in-prod-but-fine-for-tests")
	cases := []struct {
		name     string
		password string
	}{
		{"typical", "correct horse battery staple"},
		{"unicode", "пароль-密码-🔑"},
		{"long", strings.Repeat("a", 1024)},
		{"empty", ""},
		{"single-byte", "x"},
		{"with-nulls", "abc\x00def\x00ghi"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Use testParams (cheap) for hashing; the roundtrip property
			// we're asserting is "what was hashed verifies", independent
			// of cost. needsRehash is exercised in its own test.
			encoded, err := hashWithParams(c.password, pepper, testParams)
			if err != nil {
				t.Fatalf("hash: %v", err)
			}
			if !strings.HasPrefix(encoded, "$argon2id$v=19$") {
				t.Fatalf("encoded prefix wrong: %q", encoded)
			}
			ok, _, err := Verify(c.password, encoded, pepper)
			if err != nil {
				t.Fatalf("verify: %v", err)
			}
			if !ok {
				t.Fatalf("verify returned ok=false for matching password")
			}
		})
	}
}

func TestVerify_WrongPassword(t *testing.T) {
	pepper := []byte("pepper")
	encoded, err := hashWithParams("right-password", pepper, testParams)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	cases := []string{
		"wrong-password",
		"right-passwor", // off by one
		"right-passwordX",
		"",
		"RIGHT-PASSWORD", // case
	}
	for _, p := range cases {
		t.Run(p, func(t *testing.T) {
			ok, needsRehash, err := Verify(p, encoded, pepper)
			if err != nil {
				t.Fatalf("verify: %v", err)
			}
			if ok {
				t.Fatalf("verify returned ok=true for wrong password %q", p)
			}
			if needsRehash {
				t.Fatalf("verify returned needsRehash=true on mismatch (should be false)")
			}
		})
	}
}

func TestVerify_PepperMismatch(t *testing.T) {
	encoded, err := hashWithParams("password", []byte("pepper-A"), testParams)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	// Same password, different pepper => must not verify.
	ok, _, err := Verify("password", encoded, []byte("pepper-B"))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if ok {
		t.Fatalf("verify returned ok=true with mismatched pepper")
	}
	// Same password, empty pepper => also must not verify.
	ok, _, err = Verify("password", encoded, nil)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if ok {
		t.Fatalf("verify returned ok=true with nil pepper vs non-empty pepper")
	}
}

func TestVerify_MalformedEncoded(t *testing.T) {
	cases := []struct {
		name    string
		encoded string
		want    error
	}{
		{"empty", "", ErrMalformedHash},
		{"random", "not a hash at all", ErrMalformedHash},
		{"too-few-segments", "$argon2id$v=19$m=8,t=1,p=1$salt", ErrMalformedHash},
		{"too-many-segments", "$argon2id$v=19$m=8,t=1,p=1$salt$hash$extra", ErrMalformedHash},
		{"missing-leading-dollar", "argon2id$v=19$m=8,t=1,p=1$c2FsdA$aGFzaA", ErrMalformedHash},
		{"unsupported-algo-i", "$argon2i$v=19$m=8,t=1,p=1$c2FsdA$aGFzaA", ErrUnsupportedAlgorithm},
		{"unsupported-algo-d", "$argon2d$v=19$m=8,t=1,p=1$c2FsdA$aGFzaA", ErrUnsupportedAlgorithm},
		{"unsupported-algo-bcrypt", "$2a$v=19$m=8,t=1,p=1$c2FsdA$aGFzaA", ErrUnsupportedAlgorithm},
		{"bad-version-prefix", "$argon2id$vv=19$m=8,t=1,p=1$c2FsdA$aGFzaA", ErrMalformedHash},
		{"unsupported-version", "$argon2id$v=16$m=8,t=1,p=1$c2FsdA$aGFzaA", ErrUnsupportedVersion},
		{"bad-params", "$argon2id$v=19$mem=8,t=1,p=1$c2FsdA$aGFzaA", ErrMalformedHash},
		{"bad-salt-b64", "$argon2id$v=19$m=8,t=1,p=1$!!!$aGFzaA", ErrMalformedHash},
		// salt is 8 bytes ("saltsalt") so we exercise the hash-b64 path
		// rather than the salt-floor branch.
		{"bad-hash-b64", "$argon2id$v=19$m=8,t=1,p=1$c2FsdHNhbHQ$!!!", ErrMalformedHash},
		{"empty-salt", "$argon2id$v=19$m=8,t=1,p=1$$aGFzaA", ErrMalformedHash},
		{"empty-hash", "$argon2id$v=19$m=8,t=1,p=1$c2FsdHNhbHQ$", ErrMalformedHash},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ok, needsRehash, err := Verify("anything", c.encoded, []byte("pepper"))
			if err == nil {
				t.Fatalf("verify: want error, got nil (ok=%v, needsRehash=%v)", ok, needsRehash)
			}
			if !errors.Is(err, c.want) {
				t.Fatalf("verify: got error %v, want errors.Is(%v)", err, c.want)
			}
			if ok || needsRehash {
				t.Fatalf("verify: on malformed input want ok=false needsRehash=false, got ok=%v needsRehash=%v", ok, needsRehash)
			}
		})
	}
}

func TestVerify_NeverEchoesSecret(t *testing.T) {
	// Whatever error path triggers, the password and the pepper must not
	// appear verbatim in the returned message.
	const password = "super-secret-pAssw0rd!"
	pepper := []byte("very-secret-pepper-bytes-here")

	// Malformed hash: error is returned.
	_, _, err := Verify(password, "garbage", pepper)
	if err == nil {
		t.Fatalf("expected error")
	}
	msg := err.Error()
	if strings.Contains(msg, password) {
		t.Errorf("error message contains password: %q", msg)
	}
	if strings.Contains(msg, string(pepper)) {
		t.Errorf("error message contains pepper: %q", msg)
	}
}

func TestVerify_NeedsRehash_OlderParams(t *testing.T) {
	// Produce a hash with parameters that are weaker than the current
	// DefaultParams on every dimension we check (memory, iterations).
	weaker := Params{
		Memory:      4 * 1024, // 4 MiB (< 64 MiB default)
		Iterations:  1,        // < 3 default
		Parallelism: 1,
		SaltLen:     16,
		KeyLen:      32,
	}
	pepper := []byte("pepper")
	encoded, err := hashWithParams("password", pepper, weaker)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	ok, needsRehash, err := Verify("password", encoded, pepper)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !ok {
		t.Fatalf("verify returned ok=false for correct password")
	}
	if !needsRehash {
		t.Fatalf("verify did not flag needsRehash for weaker params")
	}

	// Hash with current defaults should NOT need rehash.
	encodedDefault, err := Hash("password", pepper)
	if err != nil {
		t.Fatalf("hash default: %v", err)
	}
	ok, needsRehash, err = Verify("password", encodedDefault, pepper)
	if err != nil {
		t.Fatalf("verify default: %v", err)
	}
	if !ok {
		t.Fatalf("verify(default) returned ok=false")
	}
	if needsRehash {
		t.Fatalf("verify(default) returned needsRehash=true for fresh hash")
	}
}

func TestHash_DefaultParamsHaveRFC9106Defaults(t *testing.T) {
	if DefaultParams.Memory != 64*1024 {
		t.Errorf("DefaultParams.Memory = %d, want %d", DefaultParams.Memory, 64*1024)
	}
	if DefaultParams.Iterations != 3 {
		t.Errorf("DefaultParams.Iterations = %d, want 3", DefaultParams.Iterations)
	}
	if DefaultParams.Parallelism != 2 {
		t.Errorf("DefaultParams.Parallelism = %d, want 2", DefaultParams.Parallelism)
	}
	if DefaultParams.SaltLen != 16 {
		t.Errorf("DefaultParams.SaltLen = %d, want 16", DefaultParams.SaltLen)
	}
	if DefaultParams.KeyLen != 32 {
		t.Errorf("DefaultParams.KeyLen = %d, want 32", DefaultParams.KeyLen)
	}
}

func TestHash_SaltUniqueness(t *testing.T) {
	// Two hashes of the same password with the same pepper must differ
	// because the salt is random. This is a smoke test for the rand
	// source — if a future change ever broke the salt generator we'd
	// silently produce identical hashes.
	pepper := []byte("pepper")
	a, err := hashWithParams("same-password", pepper, testParams)
	if err != nil {
		t.Fatalf("hash a: %v", err)
	}
	b, err := hashWithParams("same-password", pepper, testParams)
	if err != nil {
		t.Fatalf("hash b: %v", err)
	}
	if a == b {
		t.Fatalf("two hashes of the same password collided — salt not random")
	}
}

func TestParams_WeakerThan(t *testing.T) {
	base := Params{Memory: 64 * 1024, Iterations: 3, Parallelism: 2, SaltLen: 16, KeyLen: 32}
	cases := []struct {
		name   string
		p      Params
		weaker bool
	}{
		{"equal", base, false},
		{"more-memory", Params{Memory: 128 * 1024, Iterations: 3, Parallelism: 2, SaltLen: 16, KeyLen: 32}, false},
		{"less-memory", Params{Memory: 32 * 1024, Iterations: 3, Parallelism: 2, SaltLen: 16, KeyLen: 32}, true},
		{"less-iter", Params{Memory: 64 * 1024, Iterations: 1, Parallelism: 2, SaltLen: 16, KeyLen: 32}, true},
		{"less-keylen", Params{Memory: 64 * 1024, Iterations: 3, Parallelism: 2, SaltLen: 16, KeyLen: 16}, true},
		{"less-saltlen", Params{Memory: 64 * 1024, Iterations: 3, Parallelism: 2, SaltLen: 8, KeyLen: 32}, true},
		// Parallelism is hardware-affinity, not security — must NOT flag rehash.
		{"less-parallelism", Params{Memory: 64 * 1024, Iterations: 3, Parallelism: 1, SaltLen: 16, KeyLen: 32}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.p.weakerThan(base); got != c.weaker {
				t.Errorf("weakerThan: got %v, want %v", got, c.weaker)
			}
		})
	}
}

// TestVerify_AdversarialPHC locks the contract that decode() rejects
// malformed PHC strings BEFORE forwarding to argon2.IDKey. Without these
// guards, an attacker who can write a row into users.password_hash (via
// SQLi, leaked credential, or a malicious migration) could crash login
// for everyone with a string like "$argon2id$v=19$m=8,t=1,p=0$...".
//
// Every row in this table represents a regression that would re-introduce
// a panic or a "looks-malformed-but-parses-cleanly" bug. The whole table
// runs inside a panic-recover wrapper (TestVerify_NoPanicEvenOnAdversarial
// below) so that even if a future change does panic, the test fails
// loudly with the offending input rather than aborting the suite.
func TestVerify_AdversarialPHC(t *testing.T) {
	// Valid base64 sample salt (8 bytes "saltsalt") and hash (4 bytes "hash"),
	// used as filler. These are deliberately at the floor; tests that need a
	// stronger filler set their own.
	const (
		validSaltB64 = "c2FsdHNhbHQ" // base64 of "saltsalt" (8 bytes)
		validHashB64 = "aGFzaA"      // base64 of "hash" (4 bytes)
		shortSaltB64 = "c2FsdA"      // base64 of "salt" (4 bytes — below 8-byte floor)
		shortHashB64 = "YQ"          // base64 of "a" (1 byte — below 4-byte floor)
	)

	cases := []struct {
		name    string
		encoded string
		want    error
	}{
		// p=0 — would panic ("argon2: parallelism degree too low") if forwarded.
		{
			name:    "parallelism-zero-panics-without-guard",
			encoded: "$argon2id$v=19$m=64,t=1,p=0$" + validSaltB64 + "$" + validHashB64,
			want:    ErrMalformedHash,
		},
		// t=0 — would panic ("argon2: number of rounds too small").
		{
			name:    "iterations-zero-panics-without-guard",
			encoded: "$argon2id$v=19$m=64,t=0,p=2$" + validSaltB64 + "$" + validHashB64,
			want:    ErrMalformedHash,
		},
		// m below floor (8 KiB). argon2.IDKey requires m >= 8*p; we reject below 8 unconditionally.
		{
			name:    "memory-below-floor",
			encoded: "$argon2id$v=19$m=4,t=3,p=2$" + validSaltB64 + "$" + validHashB64,
			want:    ErrMalformedHash,
		},
		// m=0 — argon2 happens to handle this without panicking today, but the
		// contract is still "reject malformed input". Lock it down.
		{
			name:    "memory-zero",
			encoded: "$argon2id$v=19$m=0,t=3,p=2$" + validSaltB64 + "$" + validHashB64,
			want:    ErrMalformedHash,
		},
		// m above ceiling — would OOM the verifier. uint32 max is the worst case.
		{
			name:    "memory-above-ceiling",
			encoded: "$argon2id$v=19$m=4294967295,t=3,p=2$" + validSaltB64 + "$" + validHashB64,
			want:    ErrMalformedHash,
		},
		// Trailing garbage in version segment — fmt.Sscanf silently accepted
		// this before scanExact() was added.
		{
			name:    "version-trailing-garbage",
			encoded: "$argon2id$v=19trailing$m=64,t=1,p=2$" + validSaltB64 + "$" + validHashB64,
			want:    ErrMalformedHash,
		},
		// Trailing garbage in params segment — same Sscanf footgun.
		{
			name:    "params-trailing-garbage",
			encoded: "$argon2id$v=19$m=64,t=1,p=2,extra=junk$" + validSaltB64 + "$" + validHashB64,
			want:    ErrMalformedHash,
		},
		// Negative version is malformed, not "unsupported" (cosmetic fix
		// noted by the reviewer).
		{
			name:    "version-negative-is-malformed-not-unsupported",
			encoded: "$argon2id$v=-1$m=64,t=1,p=2$" + validSaltB64 + "$" + validHashB64,
			want:    ErrMalformedHash,
		},
		// Salt below 8-byte floor — argon2 produces a result here but the
		// salt offers negligible collision resistance. Reject as malformed.
		{
			name:    "salt-below-floor",
			encoded: "$argon2id$v=19$m=64,t=1,p=2$" + shortSaltB64 + "$" + validHashB64,
			want:    ErrMalformedHash,
		},
		// Hash output below 4-byte floor.
		{
			name:    "hash-below-floor",
			encoded: "$argon2id$v=19$m=64,t=1,p=2$" + validSaltB64 + "$" + shortHashB64,
			want:    ErrMalformedHash,
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			// Run under a panic-recover so a regression that re-introduces
			// the panic fails THIS test with a useful message, rather than
			// crashing the whole `go test` binary.
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("verify panicked on adversarial PHC %q: %v", c.encoded, r)
				}
			}()

			ok, needsRehash, err := Verify("anything", c.encoded, []byte("pepper"))
			if err == nil {
				t.Fatalf("verify: want error, got nil (ok=%v, needsRehash=%v) for %q", ok, needsRehash, c.encoded)
			}
			if !errors.Is(err, c.want) {
				t.Fatalf("verify: got error %v, want errors.Is(%v) for %q", err, c.want, c.encoded)
			}
			if ok || needsRehash {
				t.Fatalf("verify: on adversarial input want ok=false needsRehash=false, got ok=%v needsRehash=%v", ok, needsRehash)
			}
		})
	}
}

// TestVerify_NoPanic_AdversarialFuzz is a small belt-and-braces fuzz of
// the PHC shape: it generates a handful of weird-but-syntactically-plausible
// PHC strings and asserts that NONE of them panic. The exact error returned
// doesn't matter; what matters is "no panic, returns cleanly".
//
// This complements TestVerify_AdversarialPHC by catching shape-level
// regressions (segment count, separator choice) that the table-driven test
// doesn't enumerate.
func TestVerify_NoPanic_AdversarialFuzz(t *testing.T) {
	inputs := []string{
		// Empty and short.
		"",
		"$",
		"$$$$$",
		"$$$$$$",
		// Wrong algo with degenerate params.
		"$argon2x$v=19$m=0,t=0,p=0$$",
		// Boundary values around the panic conditions.
		"$argon2id$v=19$m=1,t=1,p=1$c2FsdHNhbHQ$aGFzaA", // mem below floor
		"$argon2id$v=19$m=7,t=1,p=1$c2FsdHNhbHQ$aGFzaA", // mem one below floor
		"$argon2id$v=19$m=8,t=1,p=2$c2FsdHNhbHQ$aGFzaA", // mem at floor but well below 8*p
		// Garbage in every numeric position.
		"$argon2id$vNN$m=NN,t=NN,p=NN$c2FsdHNhbHQ$aGFzaA",
		"$argon2id$v=$m=,t=,p=$c2FsdHNhbHQ$aGFzaA",
		// Negative numbers in params (would underflow uint32/uint8).
		"$argon2id$v=19$m=-1,t=-1,p=-1$c2FsdHNhbHQ$aGFzaA",
		// Very large numbers (uint32 overflow territory).
		"$argon2id$v=99999999999999999999$m=64,t=1,p=2$c2FsdHNhbHQ$aGFzaA",
		// Non-ASCII version.
		"$argon2id$v=１９$m=64,t=1,p=2$c2FsdHNhbHQ$aGFzaA",
	}

	for i, in := range inputs {
		in := in
		t.Run(fmt.Sprintf("input-%d", i), func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Verify panicked on %q: %v", in, r)
				}
			}()
			// Result doesn't matter — only the absence of a panic does.
			ok, needsRehash, err := Verify("anything", in, []byte("pepper"))
			// Make some weak assertions just to use the values and to lock
			// down "weird inputs never report a successful match".
			if ok {
				t.Errorf("Verify returned ok=true for adversarial %q (err=%v, rehash=%v)", in, err, needsRehash)
			}
		})
	}

	// Sanity check: there IS a valid PHC string that round-trips. Without
	// this the test above could be passing because EVERY input falls into
	// the recover path. (It doesn't — the previous assertions enforce
	// ok=false, not panic — but defense in depth.)
	encoded, err := hashWithParams("p", []byte("pepper"), testParams)
	if err != nil {
		t.Fatalf("hashWithParams: %v", err)
	}
	ok, _, err := Verify("p", encoded, []byte("pepper"))
	if err != nil || !ok {
		t.Fatalf("control case: Verify on valid PHC returned ok=%v err=%v", ok, err)
	}
}

func TestVerify_ConstantTimeCompare_NoEarlyExit(t *testing.T) {
	// We can't directly measure constant-time-ness in a unit test (timing
	// is non-deterministic on a busy CI box). What we CAN do is verify
	// that the comparison path is reached for a hash that decodes
	// correctly but doesn't match — i.e. no parse error short-circuits
	// the wrong-password case. Combined with code inspection
	// (subtle.ConstantTimeCompare in the source), this guards against
	// regressions like swapping in bytes.Equal.
	pepper := []byte("pepper")
	encoded, err := hashWithParams("a", pepper, testParams)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	ok, _, err := Verify("b", encoded, pepper)
	if err != nil {
		t.Fatalf("verify: %v (expected nil for wrong-password)", err)
	}
	if ok {
		t.Fatalf("verify: ok=true for wrong password")
	}
}
