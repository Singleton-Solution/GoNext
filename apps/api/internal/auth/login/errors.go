package login

import "errors"

// Domain errors returned by Service. Callers compare with errors.Is.
// The handler maps these to HTTP responses.
var (
	// ErrInvalidCredentials is returned when the email is unknown or
	// the password is wrong. Deliberately ambiguous — the handler
	// renders the same 401 in either case to avoid enumeration.
	ErrInvalidCredentials = errors.New("login: invalid credentials")

	// ErrLocked is returned when the account is in lockout. The
	// service surfaces it ONLY after a successful password match,
	// per the §12.2 oracle-avoidance rule.
	ErrLocked = errors.New("login: account locked")

	// ErrRateLimited is returned when the per-IP or per-email bucket
	// has been exhausted. The handler renders 429 with Retry-After.
	ErrRateLimited = errors.New("login: rate limited")

	// ErrTOTPRequired is returned when the password is correct, 2FA is
	// enabled on the account, and the request did not carry a
	// totp_code / recovery_code (or carried an invalid one). The
	// handler renders 200 with an intermediate token and
	// {"requires":["totp"]}.
	ErrTOTPRequired = errors.New("login: TOTP required")

	// ErrTOTPInvalid is returned when a TOTP / recovery code was
	// provided but didn't match. Distinct from ErrTOTPRequired so the
	// handler can emit a different audit event and render 401.
	ErrTOTPInvalid = errors.New("login: TOTP invalid")

	// ErrIntermediateExpired is returned by FinalizeTOTP when the
	// intermediate token from the first call has expired or never
	// existed. The handler renders 401.
	ErrIntermediateExpired = errors.New("login: intermediate token expired")
)
