// Package totp implements RFC 6238 time-based one-time passwords for use
// as a second authentication factor, plus the companion recovery-code
// scheme described in docs/06-auth-permissions.md §4.5.
//
// # TOTP
//
// The parameters are fixed at the RFC 6238 defaults that every authenticator
// app (Google Authenticator, 1Password, Authy, Bitwarden, ...) understands:
//
//   - SHA-1 (RFC 4226 §5 — required by the broadest set of authenticators)
//   - 30-second step
//   - 6 decimal digits
//   - 20-byte (160-bit) random secret encoded as base32 without padding
//
// Generate returns a Secret value carrying the base32 string and a method
// for emitting the otpauth:// provisioning URI (RFC 4226 / Google
// Authenticator key-uri format). The URI is what callers feed into a
// QR-code renderer — we deliberately do not pull a QR library into this
// package; rendering happens in the UI layer so dimensions, colours, and
// error-correction level stay in the design system rather than in the
// crypto package.
//
// Verify takes a base32 secret and a 6-digit code and returns ok via a
// constant-time comparison with a ±1 step skew window. The ±1 window
// covers the "code accepted at the boundary still works for ~30s after"
// case — a code generated in step N is accepted in steps N-1, N, and N+1.
// Replay protection (storing the last-used step per user) is the caller's
// responsibility, not this package's.
//
// # Recovery codes
//
// RecoveryCodes generates N random 10-character codes in the human-friendly
// shape "4f7g-h2k9" (two groups of four lowercase alphanumerics joined by
// a hyphen). The character set excludes visually-ambiguous glyphs (0/O,
// 1/l/I) so codes survive being read off a print-out.
//
// The function returns BOTH the plaintext codes (for one-time display to
// the user immediately after enrolment) AND argon2id hashes (for storage).
// Hashes reuse the auth/password scheme — same encoding, same cost params,
// same pepper-free interface — so consumers can verify a presented code
// with VerifyRecoveryCode and then mark the matching hash as consumed.
//
// VerifyRecoveryCode iterates the full hash list in constant time per
// candidate (subtle.ConstantTimeCompare under the hood via argon2) and
// returns the matched index so the caller can persist consumption. A
// match short-circuits no further than the argon2 verify cost; we cannot
// avoid that without leaking ordering information.
//
// # Redaction
//
// Secrets, recovery codes, TOTP codes, and hashes never appear in any
// error returned from this package. Error messages describe failure
// shapes (parse error, length mismatch) without echoing input — same
// discipline as packages/go/auth/password and packages/go/secrets.
//
// See docs/06-auth-permissions.md §4.5, RFC 6238, RFC 4226.
package totp
