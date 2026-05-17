package totp

import (
	"encoding/base32"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	pquerna "github.com/pquerna/otp/totp"
)

func TestGenerate_ProducesParseableBase32(t *testing.T) {
	s, err := Generate("GoNext", "alice@example.com")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if s.Base32 == "" {
		t.Fatalf("Generate: empty Base32")
	}
	// Round-trip through the same encoding used by Generate.
	dec := base32.StdEncoding.WithPadding(base32.NoPadding)
	raw, err := dec.DecodeString(s.Base32)
	if err != nil {
		t.Fatalf("base32 decode of Generate output failed: %v", err)
	}
	if len(raw) != secretBytes {
		t.Fatalf("decoded secret length = %d, want %d", len(raw), secretBytes)
	}
}

func TestGenerate_TwoCallsDiffer(t *testing.T) {
	// Smoke test for the rand source. If a regression ever stubbed
	// crypto/rand we'd silently issue identical secrets to every user.
	a, err := Generate("GoNext", "u")
	if err != nil {
		t.Fatalf("Generate a: %v", err)
	}
	b, err := Generate("GoNext", "u")
	if err != nil {
		t.Fatalf("Generate b: %v", err)
	}
	if a.Base32 == b.Base32 {
		t.Fatalf("two consecutive Generate calls produced identical secrets")
	}
}

func TestGenerate_InvalidLabel(t *testing.T) {
	cases := []struct {
		name, issuer, account string
	}{
		{"empty-issuer", "", "alice"},
		{"empty-account", "GoNext", ""},
		{"both-empty", "", ""},
		{"issuer-has-colon", "Go:Next", "alice"},
		{"account-has-colon", "GoNext", "alice:bob"},
		{"issuer-trailing-space", "GoNext ", "alice"},
		{"account-leading-space", "GoNext", " alice"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Generate(c.issuer, c.account)
			if !errors.Is(err, ErrInvalidIssuer) {
				t.Fatalf("Generate: want ErrInvalidIssuer, got %v", err)
			}
		})
	}
}

func TestVerify_CorrectCode(t *testing.T) {
	s, err := Generate("GoNext", "alice")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// Generate a code for "now" via the same library Verify uses.
	code, err := pquerna.GenerateCode(s.Base32, time.Now().UTC())
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}
	if !Verify(s.Base32, code) {
		t.Fatalf("Verify returned false for fresh code %q", code)
	}
}

func TestVerify_WrongCode(t *testing.T) {
	s, err := Generate("GoNext", "alice")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// Wrong codes — different families to exercise no-match cases.
	cases := []string{
		"000000",
		"999999",
		"123456",
		"111111",
	}
	// Filter out the actual code (just in case the current step happens
	// to land on one of these — astronomically unlikely but cheap to guard).
	current, err := pquerna.GenerateCode(s.Base32, time.Now().UTC())
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}
	for _, c := range cases {
		if c == current {
			continue
		}
		t.Run(c, func(t *testing.T) {
			if Verify(s.Base32, c) {
				t.Fatalf("Verify returned true for wrong code %q (current=%q)", c, current)
			}
		})
	}
}

func TestVerify_OneStepOldStillTrue(t *testing.T) {
	// Codes generated for step N-1 must still verify in step N.
	// docs/06-auth-permissions.md §4.5: "±1 step accepted".
	s, err := Generate("GoNext", "alice")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	oldT := time.Now().UTC().Add(-time.Duration(stepSeconds) * time.Second)
	code, err := pquerna.GenerateCode(s.Base32, oldT)
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}
	if !Verify(s.Base32, code) {
		t.Fatalf("Verify rejected one-step-old code %q (oldT=%s)", code, oldT.Format(time.RFC3339))
	}
}

func TestVerify_OneStepFutureStillTrue(t *testing.T) {
	// The ±1 window is symmetric: a code generated for step N+1 (e.g.
	// because the user's phone clock is 30s fast) must also verify.
	s, err := Generate("GoNext", "alice")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	futureT := time.Now().UTC().Add(time.Duration(stepSeconds) * time.Second)
	code, err := pquerna.GenerateCode(s.Base32, futureT)
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}
	if !Verify(s.Base32, code) {
		t.Fatalf("Verify rejected one-step-future code %q", code)
	}
}

func TestVerify_TwoStepOldFalse(t *testing.T) {
	// A code two steps in the past must NOT verify — that's outside the
	// ±1 window. This locks the skew boundary so a future change can't
	// accidentally widen it.
	s, err := Generate("GoNext", "alice")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	oldT := time.Now().UTC().Add(-2 * time.Duration(stepSeconds) * time.Second)
	code, err := pquerna.GenerateCode(s.Base32, oldT)
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}
	// Edge case: if the current step's code happens to equal the
	// two-step-old code by chance (6-digit collision ~ 1 in a million),
	// skip the assertion rather than flake.
	current, _ := pquerna.GenerateCode(s.Base32, time.Now().UTC())
	if code == current {
		t.Skip("rare 6-digit collision between step N and step N-2; re-run")
	}
	if Verify(s.Base32, code) {
		t.Fatalf("Verify accepted two-step-old code %q (outside ±1 window)", code)
	}
}

func TestVerify_TwoStepFutureFalse(t *testing.T) {
	s, err := Generate("GoNext", "alice")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	futureT := time.Now().UTC().Add(2 * time.Duration(stepSeconds) * time.Second)
	code, err := pquerna.GenerateCode(s.Base32, futureT)
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}
	current, _ := pquerna.GenerateCode(s.Base32, time.Now().UTC())
	if code == current {
		t.Skip("rare 6-digit collision; re-run")
	}
	if Verify(s.Base32, code) {
		t.Fatalf("Verify accepted two-step-future code %q", code)
	}
}

func TestVerify_MalformedInputs(t *testing.T) {
	s, err := Generate("GoNext", "alice")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	cases := []struct {
		name, secret, code string
	}{
		{"empty-code", s.Base32, ""},
		{"short-code", s.Base32, "12345"},
		{"long-code", s.Base32, "1234567"},
		{"non-digit-code", s.Base32, "12345a"},
		{"unicode-code", s.Base32, "１２３４５６"}, // full-width digits
		{"empty-secret", "", "123456"},
		{"bad-b32-secret", "!!!", "123456"},
		{"too-short-secret-bytes", base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("ab")), "123456"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if Verify(c.secret, c.code) {
				t.Fatalf("Verify returned true for malformed input")
			}
		})
	}
}

func TestVerify_CaseInsensitiveSecret(t *testing.T) {
	// Authenticator apps emit base32 in uppercase; users sometimes type
	// it in lowercase. Both must verify against the same secret.
	s, err := Generate("GoNext", "alice")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	code, err := pquerna.GenerateCode(s.Base32, time.Now().UTC())
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}
	if !Verify(strings.ToLower(s.Base32), code) {
		t.Fatalf("Verify rejected lowercase variant of secret")
	}
}

func TestURI_FollowsOtpauthFormat(t *testing.T) {
	s, err := Generate("GoNext", "alice@example.com")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	uri, err := s.URI("GoNext", "alice@example.com")
	if err != nil {
		t.Fatalf("URI: %v", err)
	}
	if !strings.HasPrefix(uri, "otpauth://totp/") {
		t.Fatalf("URI does not start with otpauth://totp/: %q", uri)
	}
	parsed, err := url.Parse(uri)
	if err != nil {
		t.Fatalf("url.Parse(uri): %v", err)
	}
	if parsed.Scheme != "otpauth" {
		t.Fatalf("scheme = %q, want otpauth", parsed.Scheme)
	}
	if parsed.Host != "totp" {
		t.Fatalf("host = %q, want totp", parsed.Host)
	}
	// Label is /<issuer>:<account>
	wantLabel := "/GoNext:alice@example.com"
	if parsed.Path != wantLabel {
		t.Fatalf("path = %q, want %q", parsed.Path, wantLabel)
	}
	q := parsed.Query()
	if q.Get("secret") != s.Base32 {
		t.Fatalf("query secret = %q, want %q", q.Get("secret"), s.Base32)
	}
	if q.Get("issuer") != "GoNext" {
		t.Fatalf("query issuer = %q, want GoNext", q.Get("issuer"))
	}
	if q.Get("algorithm") != "SHA1" {
		t.Fatalf("query algorithm = %q, want SHA1", q.Get("algorithm"))
	}
	if q.Get("digits") != "6" {
		t.Fatalf("query digits = %q, want 6", q.Get("digits"))
	}
	if q.Get("period") != "30" {
		t.Fatalf("query period = %q, want 30", q.Get("period"))
	}
}

func TestURI_RejectsInvalidLabel(t *testing.T) {
	s, err := Generate("GoNext", "alice")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	cases := []struct {
		name, issuer, account string
	}{
		{"empty-issuer", "", "alice"},
		{"empty-account", "GoNext", ""},
		{"issuer-colon", "Go:Next", "alice"},
		{"account-colon", "GoNext", "a:b"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := s.URI(c.issuer, c.account)
			if !errors.Is(err, ErrInvalidIssuer) {
				t.Fatalf("URI: want ErrInvalidIssuer, got %v", err)
			}
		})
	}
}

func TestURI_RejectsCorruptSecret(t *testing.T) {
	bad := &Secret{Base32: "!!!not-base32!!!"}
	_, err := bad.URI("GoNext", "alice")
	if !errors.Is(err, ErrInvalidSecret) {
		t.Fatalf("URI(corrupt): want ErrInvalidSecret, got %v", err)
	}
	// Nil secret
	var nilSecret *Secret
	_, err = nilSecret.URI("GoNext", "alice")
	if !errors.Is(err, ErrInvalidSecret) {
		t.Fatalf("URI(nil): want ErrInvalidSecret, got %v", err)
	}
	// Empty Base32 field
	empty := &Secret{Base32: ""}
	_, err = empty.URI("GoNext", "alice")
	if !errors.Is(err, ErrInvalidSecret) {
		t.Fatalf("URI(empty): want ErrInvalidSecret, got %v", err)
	}
}

func TestVerify_NeverEchoesSecret(t *testing.T) {
	// Verify returns bool, not error — but if any future refactor adds
	// an error path, lock down that the secret never appears in it.
	// Today this test mainly exercises Generate/URI error messages.
	s, err := Generate("GoNext", "alice")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	_, err = s.URI("Go:Next", "alice")
	if err == nil {
		t.Fatalf("URI: want error for invalid issuer")
	}
	if strings.Contains(err.Error(), s.Base32) {
		t.Errorf("URI error message echoed secret: %q", err.Error())
	}
}

func TestValidateCode(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"123456", true},
		{"000000", true},
		{"999999", true},
		{"", false},
		{"12345", false},
		{"1234567", false},
		{"abcdef", false},
		{"12345a", false},
		{"12 456", false},
		{"１２３４５６", false},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			if got := validateCode(c.in); got != c.want {
				t.Errorf("validateCode(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestCTEqualString(t *testing.T) {
	// Sanity check on the helper. Constant-time-ness is a property we
	// can't easily measure; correctness we can.
	if !ctEqualString("abc", "abc") {
		t.Errorf("ctEqualString(abc, abc) = false")
	}
	if ctEqualString("abc", "abd") {
		t.Errorf("ctEqualString(abc, abd) = true")
	}
	if ctEqualString("abc", "ab") {
		t.Errorf("ctEqualString(abc, ab) = true (different lengths)")
	}
	if !ctEqualString("", "") {
		t.Errorf("ctEqualString(empty, empty) = false")
	}
}
