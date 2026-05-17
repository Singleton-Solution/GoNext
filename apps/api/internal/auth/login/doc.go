// Package login implements the HTTP login flow for the GoNext API.
//
// The flow is intentionally split into three layers so each can be
// reasoned about in isolation and exercised in tests without spinning
// up the whole server:
//
//   - [Handler]  — POST /api/v1/auth/login. Decodes the JSON request,
//     drives the [Service], and writes responses + cookies.
//   - [Service]  — the pure business logic: rate-limit, user lookup,
//     password verify, 2FA gate, session creation, audit emission.
//     Has no dependency on net/http; takes context.Context and
//     primitive arguments only.
//   - [Deps]     — the wiring struct callers populate at boot. The
//     server's main.go builds it once and passes the resulting
//     Deps to [Mount].
//
// # Security properties (issue #124 AC)
//
// The handler enforces the following non-negotiables:
//
//  1. Constant-time over both code paths: an unknown email and a known
//     email with a wrong password take the same wall-clock time, to
//     within ~50ms. We achieve this by running argon2 against a
//     compile-time dummy hash whenever the user lookup misses. The
//     dummy hash is constructed once at package init using a discarded
//     secret; verification against it always returns "wrong password",
//     and the cost of the verify call dominates the IO of the user
//     lookup itself, closing the timing oracle.
//
//  2. Per-IP + per-email-existence-gated rate limiting via
//     packages/go/ratelimit.LoginAttemptLimiter. The EmailExists
//     argument is set to the result of the user lookup; this is the
//     load-bearing flag that prevents the per-email bucket from
//     becoming an enumeration oracle (issue #195).
//
//  3. Wrong-password and unknown-email responses are 401 with the same
//     {"error":"invalid_credentials"} body. We never return 404, 403,
//     or 500 for an authentication miss — those would either leak
//     enumeration data or hide a real failure behind a misleading code.
//
//  4. Lockout (after N consecutive failures) returns 423 Locked with a
//     Retry-After header. The lockout check is performed AFTER the
//     password is confirmed correct, per the docs/06-auth-permissions.md
//     §12.2 oracle-avoidance rule. A wrong password on a locked
//     account therefore looks identical to a wrong password on an
//     unlocked one.
//
//  5. 2FA is enforced when the user has it enabled. If the password
//     matches and 2FA is required, the response is a 200 carrying an
//     intermediate token and {"requires":["totp"]}; the session is
//     NOT created yet. The client posts the same body shape back with
//     totp_code (or recovery_code) plus the intermediate token to
//     finalize. The intermediate token lives in Redis with a short
//     TTL (5 minutes) so a partial login cannot stall indefinitely.
//
//  6. Every transition emits an audit event:
//     auth.login.attempt, auth.login.success, auth.login.failed,
//     auth.login.locked. The audit row carries IP, user-agent, and
//     (for known emails) the user ID; the cleartext password and
//     intermediate token are never logged.
//
// # Storage notes
//
// The user_totp table doesn't exist yet (its migration ships in a
// follow-up PR). The Service detects a missing table and treats every
// account as "2FA disabled" — the 2FA branch is dormant code paths
// guarded by [Deps.TOTPLookup] being either nil or returning
// ErrTOTPNotEnabled. Tests cover both wired and unwired states.
//
// # Cookie
//
// Session cookies are set via packages/go/session.SetCookie with the
// defaults that package enforces: HttpOnly, Secure (unless Deps.Insecure
// flips it for dev), SameSite=Lax, Path=/. The cookie name and Max-Age
// derive from Deps.SessionAbsoluteTTL.
//
// # File ownership
//
// Per the issue spec, this package owns only apps/api/internal/auth/login/.
// We expose [Mount] so the server's main.go can wire the route without
// this package reaching into cmd/.
package login
