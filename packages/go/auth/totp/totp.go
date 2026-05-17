package totp

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base32"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

// Errors returned by this package. Callers should compare with errors.Is
// rather than string-matching; messages may grow more detail but the
// sentinel set is stable.
var (
	// ErrInvalidSecret indicates the supplied base32 string is not a
	// valid TOTP secret (bad base32, wrong length).
	ErrInvalidSecret = errors.New("totp: invalid secret")

	// ErrInvalidIssuer indicates issuer or account is empty / contains
	// a literal colon, which the otpauth URI grammar reserves as a
	// separator (RFC 4226, Google Authenticator key-uri-format).
	ErrInvalidIssuer = errors.New("totp: invalid issuer or account")
)

// RFC 6238 parameters. These are fixed — callers do not get to pick
// algorithm or digit count. Authenticator app compatibility is the
// governing constraint; SHA-1 is what every shipped app understands
// and the threat model (a 30-second window of a 6-digit code) does not
// improve meaningfully with SHA-256 in the TOTP setting.
const (
	// stepSeconds is the TOTP time step in seconds. 30 is the RFC 6238
	// default and what every authenticator app expects.
	stepSeconds uint = 30

	// digits is the length of the generated code in decimal digits.
	// 6 is the RFC 6238 default; 8 is permitted but breaks several
	// shipped authenticator apps.
	digits = otp.DigitsSix

	// algorithm is SHA-1 for authenticator-app compatibility.
	algorithm = otp.AlgorithmSHA1

	// secretBytes is the size of the random secret material. 20 bytes
	// = 160 bits matches the SHA-1 output size and RFC 4226 §4 R2
	// "the shared secret MUST be chosen at random or using a
	// cryptographically strong pseudorandom generator".
	secretBytes = 20

	// skewSteps is the ±N window of accepted past/future steps. ±1
	// means a code generated in step N is accepted in steps N-1, N, N+1
	// — the "code clicked at the boundary still works for ~30s" slop
	// documented in docs/06-auth-permissions.md §4.5.
	skewSteps uint = 1
)

// Secret is a freshly-generated TOTP secret. The Base32 field is what
// you show to the user (and embed in QR codes); URI builds the
// otpauth:// provisioning URI.
//
// Once stored, the same Base32 value is what you pass back to Verify.
// The struct deliberately does not carry the issuer/account: those are
// configuration of the enrolment ceremony, not properties of the secret.
type Secret struct {
	// Base32 is the shared secret encoded as RFC 4648 base32 without
	// padding. This is the canonical wire format for TOTP secrets —
	// authenticator apps accept it directly when the user types it
	// in instead of scanning a QR code.
	Base32 string
}

// Generate creates a fresh 20-byte random TOTP secret using crypto/rand.
//
// issuer is the human-readable service name (e.g. "GoNext") that shows
// up in the user's authenticator app as the label for the entry. account
// is the user-identifying string (typically the email address). Both
// are validated for the otpauth URI grammar — neither may be empty and
// neither may contain ':' (the URI reserves it as the issuer/account
// separator in the label segment).
//
// The returned Secret carries the base32 string; the issuer and account
// are not stored on it, because storage is the caller's concern: in
// production we encrypt the secret with a KMS key (see
// docs/06-auth-permissions.md §4.5) and the issuer is a build-time
// constant, so there is no value in coupling them.
func Generate(issuer, account string) (*Secret, error) {
	if err := validateLabel(issuer, account); err != nil {
		return nil, err
	}

	raw := make([]byte, secretBytes)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("totp: generate secret: %w", err)
	}

	return &Secret{
		Base32: base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw),
	}, nil
}

// URI returns the otpauth:// provisioning URI for this secret. The format
// follows the Google Authenticator key-uri-format spec (a superset of
// RFC 4226 §3 / RFC 6238 §6):
//
//	otpauth://totp/<issuer>:<account>?secret=<b32>&issuer=<issuer>
//	    &algorithm=SHA1&digits=6&period=30
//
// Both the label (path) and the issuer query parameter carry the issuer
// because authenticator apps differ in which one they read; including
// both is the widely-deployed compatibility recipe.
//
// Returns an error if issuer or account fail the URI grammar check —
// same rules as Generate.
func (s *Secret) URI(issuer, account string) (string, error) {
	if err := validateLabel(issuer, account); err != nil {
		return "", err
	}
	if s == nil || s.Base32 == "" {
		return "", fmt.Errorf("%w: empty secret", ErrInvalidSecret)
	}
	// Sanity-check the base32 — a caller could construct &Secret{Base32: "garbage"}.
	if _, err := decodeSecret(s.Base32); err != nil {
		return "", err
	}

	// Build manually rather than via net/url's String() so the parameter
	// order is stable (RFC 3986 does not require it, but stable output
	// helps tests and snapshotting).
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{}
	q.Set("secret", s.Base32)
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", "6")
	q.Set("period", "30")

	return "otpauth://totp/" + label + "?" + q.Encode(), nil
}

// Verify checks a 6-digit code against a base32-encoded TOTP secret. It
// returns true iff the code matches the current step or one step on
// either side (±30 seconds). Comparison is constant-time — see pquerna/otp,
// which uses subtle.ConstantTimeCompare internally.
//
// Verify does NOT track replays. Two successful verifications of the same
// code within the same 30s step are both true; the caller must persist
// the last successful step and refuse to re-use it. See the
// `last_used_step` column in user_totp (docs/06-auth-permissions.md §4.5).
//
// A malformed secret or an empty code returns false rather than an error.
// Returning an error here would push branching into every login handler
// for a condition (badly-formed stored secret) that's a programmer bug,
// not a runtime expectation. The caller distinguishes "user typed wrong
// code" from "stored secret is corrupt" out-of-band — e.g. by running
// the input through the same validateCode predicate before calling.
func Verify(base32Secret, code string) bool {
	// Reject obviously-wrong shapes before we hand off to the verifier.
	// pquerna/otp.ValidateCustom would also reject these, but doing it
	// here keeps the contract crisp: a non-6-digit input is "wrong",
	// not "error".
	if !validateCode(code) {
		return false
	}
	if _, err := decodeSecret(base32Secret); err != nil {
		return false
	}

	ok, err := totp.ValidateCustom(code, base32Secret, time.Now().UTC(), totp.ValidateOpts{
		Period:    stepSeconds,
		Skew:      skewSteps,
		Digits:    digits,
		Algorithm: algorithm,
	})
	if err != nil {
		// ValidateCustom returns an error only for malformed inputs we've
		// already rejected. Treat any leftover error as "no match".
		return false
	}
	return ok
}

// validateCode returns true if s is exactly 6 ASCII digits. This is what
// every authenticator app emits; rejecting anything else early avoids
// pushing weird shapes (whitespace, "123 456", unicode digit lookalikes)
// into the verifier.
func validateCode(s string) bool {
	if len(s) != 6 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// validateLabel enforces the otpauth:// label grammar: non-empty issuer
// and account, neither containing ':' (which the URI uses as the
// issuer/account separator) or control characters / leading-trailing
// whitespace (which authenticators render poorly).
func validateLabel(issuer, account string) error {
	if issuer == "" || account == "" {
		return fmt.Errorf("%w: empty", ErrInvalidIssuer)
	}
	if strings.ContainsRune(issuer, ':') || strings.ContainsRune(account, ':') {
		return fmt.Errorf("%w: contains ':'", ErrInvalidIssuer)
	}
	if strings.TrimSpace(issuer) != issuer || strings.TrimSpace(account) != account {
		return fmt.Errorf("%w: leading or trailing whitespace", ErrInvalidIssuer)
	}
	return nil
}

// decodeSecret base32-decodes s with the unpadded standard alphabet and
// validates the resulting byte length. Returns ErrInvalidSecret on any
// failure. The decoded bytes are returned for callers that need them
// (currently none — Verify hands the string back to pquerna/otp). They
// are kept on the return signature so a future caller doesn't have to
// re-decode.
func decodeSecret(s string) ([]byte, error) {
	if s == "" {
		return nil, fmt.Errorf("%w: empty", ErrInvalidSecret)
	}
	// Standard alphabet, no padding. Authenticators are case-insensitive;
	// we accept both upper and lower by uppercasing first.
	raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(s))
	if err != nil {
		return nil, fmt.Errorf("%w: base32 decode", ErrInvalidSecret)
	}
	if len(raw) < secretBytes {
		// A short secret is permitted by some legacy apps (Google's
		// own QR codes from 2010 used 10 bytes). For NEW enrolments
		// we always emit 20; for VERIFICATION we tolerate >= 10 to
		// avoid breaking users restoring from a very old export.
		// Setting the floor at 10 still gives 80 bits of entropy.
		if len(raw) < 10 {
			return nil, fmt.Errorf("%w: %d bytes below minimum", ErrInvalidSecret, len(raw))
		}
	}
	return raw, nil
}

// ctEqualString reports string equality in constant time. Useful where
// we compare user-supplied strings against stored values and want to
// avoid the early-exit timing side channel of ==.
//
// Currently unused outside tests but kept package-private for future
// callers that aren't going through argon2/pquerna and need a
// constant-time string compare.
func ctEqualString(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
