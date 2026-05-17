package password

import (
	"errors"
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
		{"bad-hash-b64", "$argon2id$v=19$m=8,t=1,p=1$c2FsdA$!!!", ErrMalformedHash},
		{"empty-salt", "$argon2id$v=19$m=8,t=1,p=1$$aGFzaA", ErrMalformedHash},
		{"empty-hash", "$argon2id$v=19$m=8,t=1,p=1$c2FsdA$", ErrMalformedHash},
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
