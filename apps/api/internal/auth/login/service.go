package login

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/audit"
	"github.com/Singleton-Solution/GoNext/packages/go/auth/password"
	"github.com/Singleton-Solution/GoNext/packages/go/auth/totp"
	"github.com/Singleton-Solution/GoNext/packages/go/ratelimit"
)

// Input is the request body decoded by the handler. It is exported so
// the handler and service can share the shape and so tests can drive
// the service directly without round-tripping JSON.
//
// Email and Password are required for the first call. TOTPCode and
// RecoveryCode are mutually exclusive (recovery is the user's escape
// hatch when they don't have their device); both are optional on the
// first call and used on the second call together with
// IntermediateToken.
type Input struct {
	Email             string
	Password          string
	TOTPCode          string
	RecoveryCode      string
	IntermediateToken string

	// IP + UserAgent are passed in by the handler so the service can
	// route them into rate-limit checks and audit emission without
	// peeking at *http.Request.
	IP        string
	UserAgent string
}

// Result is what a successful Authenticate returns. Token and
// ExpiresAt are zero in the "TOTP required" branch; the handler reads
// IntermediateToken instead and skips the cookie set.
//
// When 2FA is not required (or has already been satisfied), Token
// holds the session token and ExpiresAt is its absolute deadline.
type Result struct {
	// UserID identifies the authenticated user.
	UserID string

	// Token is the session token. Empty when RequiresTOTP is true.
	Token string

	// ExpiresAt is the session's absolute expiry. Zero when
	// RequiresTOTP is true.
	ExpiresAt time.Time

	// RequiresTOTP is true when the password was correct but 2FA is
	// enabled and the client must call again with TOTPCode +
	// IntermediateToken.
	RequiresTOTP bool

	// IntermediateToken is non-empty iff RequiresTOTP is true. The
	// client echoes it on the second call.
	IntermediateToken string
}

// Service encapsulates the authentication logic. It is constructed
// once at server boot and is safe for concurrent use; every public
// method takes a context so cancellation is honored.
type Service struct {
	deps Deps
}

// NewService validates Deps and returns a ready Service.
func NewService(d Deps) (*Service, error) {
	if err := d.validate(); err != nil {
		return nil, err
	}
	d.defaults()
	return &Service{deps: d}, nil
}

// Authenticate runs the login flow against in. It returns one of:
//
//   - (Result{...session...}, nil) — success, session created.
//   - (Result{RequiresTOTP: true, IntermediateToken: ...}, nil)
//     — password OK, 2FA required.
//   - (Result{}, ErrInvalidCredentials) — wrong email or wrong password.
//   - (Result{}, ErrLocked) — correct password against a locked account.
//     The error wraps a time.Duration via [LockedUntil] so the handler
//     can populate Retry-After.
//   - (Result{}, ErrRateLimited) — bucket exhausted.
//   - (Result{}, ErrTOTPInvalid) — second call with a wrong code.
//   - (Result{}, ErrIntermediateExpired) — second call after the token
//     has expired.
//
// Internal errors (DB blip, Redis blip) are wrapped with %w and
// surface as 500s in the handler. The handler is responsible for
// translating these into HTTP, and for setting the session cookie on
// the success branch.
//
// The constant-time guarantee is the key correctness property:
// regardless of whether the email maps to a real user, this function
// runs argon2 against a hash and emits one audit event. The
// wall-clock duration of a wrong-email call and a wrong-password
// call are within argon2's noise floor of each other (≪50ms).
func (s *Service) Authenticate(ctx context.Context, in Input) (Result, error) {
	in.Email = strings.TrimSpace(in.Email)

	// Special case: second call carrying an intermediate token. We
	// don't hit the rate-limit / user-lookup paths because the first
	// call already paid that cost, and the intermediate token itself
	// has a 5-minute TTL. The email field is optional on this branch
	// — clients commonly omit it once the password step is past.
	if in.IntermediateToken != "" {
		return s.finalizeTOTP(ctx, in)
	}

	if in.Email == "" {
		// Without an email we have nothing to compute against. Still
		// run the dummy hash to keep the timing profile, but skip
		// the user lookup that would otherwise short-circuit.
		_ = s.verifyDummy(in.Password)
		s.emitAttempt(ctx, in, "")
		s.emitFailed(ctx, in, "", "missing_email")
		return Result{}, ErrInvalidCredentials
	}

	// First-call rate-limit + dispatch. We need to know whether the
	// email exists BEFORE we call Check, because the per-email bucket
	// is gated on that flag (issue #195). The user lookup itself is
	// timing-leaky relative to the password verify but the verify
	// step that follows dominates the wall-clock — we still run it
	// on the unknown path against the dummy hash.
	user, lookupErr := s.lookup(ctx, in.Email)
	emailExists := lookupErr == nil

	s.emitAttempt(ctx, in, user.ID)

	pre, err := s.deps.Limiter.Check(ctx, ratelimit.CheckInput{
		IP:          in.IP,
		Email:       in.Email,
		EmailExists: emailExists,
	})
	if err != nil {
		// Fail closed on a rate-limit backend outage: a misbehaving
		// Redis must not make us a free brute-force oracle. The audit
		// row still fires so operators see the storm.
		s.deps.Log.WarnContext(ctx, "login: rate-limit check failed",
			slog.String("err", err.Error()))
		s.emitFailed(ctx, in, user.ID, "ratelimit_error")
		return Result{}, fmt.Errorf("login: rate limit: %w", err)
	}
	if !pre.Allowed {
		s.emitFailed(ctx, in, user.ID, "rate_limited")
		return Result{}, withRetryAfter(ErrRateLimited, pre.RetryAfter)
	}

	// Password verify. On the unknown-email branch we still run the
	// argon2 verify (against the dummy hash) so the wall clock of
	// the two paths agrees.
	if !emailExists || user.Status != "active" {
		_ = s.verifyDummy(in.Password)
		s.emitFailed(ctx, in, user.ID, "unknown_or_inactive")
		return Result{}, ErrInvalidCredentials
	}

	ok, needsRehash, vErr := password.Verify(in.Password, user.Hash, s.deps.Pepper)
	if vErr != nil {
		// A malformed stored hash is a server-side data bug. We log
		// it but return invalid_credentials so the client (and any
		// observer) can't tell a real password mismatch from a
		// corrupted hash.
		s.deps.Log.WarnContext(ctx, "login: password verify error",
			slog.String("user_id", user.ID),
			slog.String("err", vErr.Error()))
		s.emitFailed(ctx, in, user.ID, "hash_error")
		return Result{}, ErrInvalidCredentials
	}
	if !ok {
		// Record the failure against the lockout counter. This is
		// the only path that increments — unknown emails are NOT
		// counted (else we'd be a registration probe again).
		if _, rfErr := s.deps.Limiter.RecordFailure(ctx, user.ID); rfErr != nil {
			s.deps.Log.WarnContext(ctx, "login: record failure",
				slog.String("user_id", user.ID),
				slog.String("err", rfErr.Error()))
		}
		s.emitFailed(ctx, in, user.ID, "wrong_password")
		return Result{}, ErrInvalidCredentials
	}

	// Password is correct. Lockout check happens HERE, after the
	// password verify, per the §12.2 oracle-avoidance rule: a wrong
	// password against a locked account must look identical to a
	// wrong password against an unlocked one, but a correct password
	// against a locked account is allowed to surface the lock.
	locked, lockedUntil, ilErr := s.deps.Limiter.IsLocked(ctx, user.ID)
	if ilErr != nil {
		s.deps.Log.WarnContext(ctx, "login: lockout check error",
			slog.String("user_id", user.ID),
			slog.String("err", ilErr.Error()))
		// We could fail closed, but the password was correct — bail
		// the user out gracefully and let the lockout sweeper catch
		// repeat offenders. The audit trail records the issue.
	}
	if locked {
		s.emitLocked(ctx, in, user.ID, lockedUntil)
		retry := lockedUntil.Sub(s.deps.Now())
		if retry < 0 {
			retry = 0
		}
		return Result{}, withRetryAfter(ErrLocked, retry)
	}

	// Best-effort rehash if argon2 parameters have rotated.
	if needsRehash && s.deps.Rehash != nil {
		newHash, hErr := password.Hash(in.Password, s.deps.Pepper)
		if hErr != nil {
			s.deps.Log.WarnContext(ctx, "login: rehash compute failed",
				slog.String("user_id", user.ID),
				slog.String("err", hErr.Error()))
		} else if rhErr := s.deps.Rehash(ctx, user.ID, newHash); rhErr != nil {
			s.deps.Log.WarnContext(ctx, "login: rehash persist failed",
				slog.String("user_id", user.ID),
				slog.String("err", rhErr.Error()))
		}
	}

	// 2FA gate. If TOTPLookup is wired AND the account has 2FA
	// enabled, we branch: either consume the code from this same
	// request, or mint an intermediate token and ask for it.
	tot, hasTOTP := s.loadTOTP(ctx, user.ID)

	switch {
	case !hasTOTP:
		// 2FA disabled or not wired — straight to session creation.
		return s.completeLogin(ctx, in, user.ID)

	case in.TOTPCode != "" || in.RecoveryCode != "":
		// First-call body carried a code. Verify it inline.
		if !s.verifyTOTPOrRecovery(in, tot) {
			if _, rfErr := s.deps.Limiter.RecordFailure(ctx, user.ID); rfErr != nil {
				s.deps.Log.WarnContext(ctx, "login: record failure (totp)",
					slog.String("user_id", user.ID),
					slog.String("err", rfErr.Error()))
			}
			s.emitFailed(ctx, in, user.ID, "wrong_totp")
			return Result{}, ErrTOTPInvalid
		}
		return s.completeLogin(ctx, in, user.ID)

	default:
		// 2FA required but code not provided. Issue an intermediate
		// token so the client can submit just the code on the next
		// call without re-typing the password.
		token, gErr := generateIntermediateToken()
		if gErr != nil {
			return Result{}, fmt.Errorf("login: %w", gErr)
		}
		if sErr := s.deps.Intermediate.Store(ctx, token, user.ID, s.deps.IntermediateTTL); sErr != nil {
			return Result{}, fmt.Errorf("login: store intermediate: %w", sErr)
		}
		// Note: NO success event yet — that fires only when the
		// session is created. Per the issue spec the "totp_required"
		// state is a transition, not an outcome.
		return Result{
			UserID:            user.ID,
			RequiresTOTP:      true,
			IntermediateToken: token,
		}, nil
	}
}

// finalizeTOTP handles the second call: the client has the
// intermediate token from a prior call and is submitting the TOTP
// code. We do NOT re-verify the password and we do NOT hit the
// rate-limiter on this path — the first call already paid both costs.
func (s *Service) finalizeTOTP(ctx context.Context, in Input) (Result, error) {
	userID, lErr := s.deps.Intermediate.Load(ctx, in.IntermediateToken)
	if lErr != nil {
		if errors.Is(lErr, ErrIntermediateNotFound) {
			s.emitFailed(ctx, in, "", "intermediate_expired")
			return Result{}, ErrIntermediateExpired
		}
		return Result{}, fmt.Errorf("login: load intermediate: %w", lErr)
	}

	tot, hasTOTP := s.loadTOTP(ctx, userID)
	if !hasTOTP {
		// The user had TOTP enabled at first-call time but doesn't
		// now (operator disabled it mid-flow). Drop the token and
		// finish the login — there's nothing further to verify.
		_ = s.deps.Intermediate.Delete(ctx, in.IntermediateToken)
		return s.completeLogin(ctx, in, userID)
	}

	if !s.verifyTOTPOrRecovery(in, tot) {
		// Don't delete the intermediate yet — the user might re-try
		// with a fresh authenticator code within the same window.
		// The token's own TTL bounds the retry attempts; we still
		// record the failure against the lockout counter.
		if _, rfErr := s.deps.Limiter.RecordFailure(ctx, userID); rfErr != nil {
			s.deps.Log.WarnContext(ctx, "login: record failure (totp)",
				slog.String("user_id", userID),
				slog.String("err", rfErr.Error()))
		}
		s.emitFailed(ctx, in, userID, "wrong_totp")
		return Result{}, ErrTOTPInvalid
	}

	// Single-use intermediate token: consume it so a leaked token
	// can't be replayed.
	_ = s.deps.Intermediate.Delete(ctx, in.IntermediateToken)

	return s.completeLogin(ctx, in, userID)
}

// completeLogin mints the session, fires the audit success event, and
// clears the failure counter. Used by both the no-2FA branch and the
// post-2FA finalization.
func (s *Service) completeLogin(ctx context.Context, in Input, userID string) (Result, error) {
	token, err := s.deps.Sessions.Create(ctx, userID, nil, s.deps.SessionAbsoluteTTL, s.deps.SessionIdleTTL)
	if err != nil {
		s.deps.Log.WarnContext(ctx, "login: session create failed",
			slog.String("user_id", userID),
			slog.String("err", err.Error()))
		return Result{}, fmt.Errorf("login: create session: %w", err)
	}

	if rsErr := s.deps.Limiter.RecordSuccess(ctx, userID); rsErr != nil {
		s.deps.Log.WarnContext(ctx, "login: record success failed",
			slog.String("user_id", userID),
			slog.String("err", rsErr.Error()))
	}

	expiresAt := s.deps.Now().Add(s.deps.SessionAbsoluteTTL)
	s.emitSuccess(ctx, in, userID)
	return Result{
		UserID:    userID,
		Token:     token,
		ExpiresAt: expiresAt,
	}, nil
}

// lookup wraps Deps.Lookup with the "unknown user" -> ErrUserNotFound
// normalization that the rest of the service depends on. A nil error
// + zero-value UserRecord is treated as "found a row but it's blank",
// which we surface as "not found" so the caller's emailExists check
// can rely on (err != nil).
func (s *Service) lookup(ctx context.Context, email string) (UserRecord, error) {
	user, err := s.deps.Lookup(ctx, email)
	if err != nil {
		return UserRecord{}, err
	}
	if user.ID == "" {
		return UserRecord{}, ErrUserNotFound
	}
	return user, nil
}

// loadTOTP wraps Deps.TOTPLookup with the same not-wired / not-enabled
// merging the service needs to make a single decision.
func (s *Service) loadTOTP(ctx context.Context, userID string) (TOTPRecord, bool) {
	if s.deps.TOTPLookup == nil {
		return TOTPRecord{}, false
	}
	rec, err := s.deps.TOTPLookup(ctx, userID)
	if err != nil {
		if !errors.Is(err, ErrTOTPNotEnabled) {
			// A real error from the lookup (table missing, query
			// failed, etc.) gets logged but is treated as "2FA off"
			// so a broken column doesn't lock everyone out. The
			// production migration adds user_totp; until it lands
			// we expect this branch.
			s.deps.Log.WarnContext(ctx, "login: TOTP lookup error",
				slog.String("user_id", userID),
				slog.String("err", err.Error()))
		}
		return TOTPRecord{}, false
	}
	if !rec.Enabled {
		return TOTPRecord{}, false
	}
	return rec, true
}

// verifyDummy runs an argon2id verify against the package's dummy
// hash. Its only purpose is to keep the wall-clock of the
// unknown-email and known-email-wrong-password paths within a few
// milliseconds of each other. The boolean return is always ignored.
//
// Returns false on any error — including the (unrecoverable)
// crypto/rand failure during dummy-hash construction. The login still
// short-circuits to ErrInvalidCredentials in that case; we don't want
// to expose "dummy hash unavailable" as a distinguishable state.
func (s *Service) verifyDummy(password string) bool {
	h, err := getDummyHash(s.deps.Pepper)
	if err != nil {
		return false
	}
	return s.verifyAgainst(password, h)
}

// verifyAgainst is the same primitive password.Verify uses, factored
// out so verifyDummy and the real path go through identical code.
func (s *Service) verifyAgainst(plaintext, hash string) bool {
	ok, _, err := passwordVerify(plaintext, hash, s.deps.Pepper)
	if err != nil {
		return false
	}
	return ok
}

// passwordVerify is a package-private indirection over
// auth/password.Verify so unit tests can swap in a constant-time stub
// when they don't want to pay argon2 latency. Production callers go
// straight through.
var passwordVerify = password.Verify

// verifyTOTPOrRecovery dispatches to the TOTP or recovery-code
// verifier depending on which one the client supplied. If the client
// supplied both (which it shouldn't), we honor TOTPCode first; the
// recovery code is a fallback intended for the "lost device" case.
//
// Returns true on a match, false otherwise. Recovery-code matches
// do NOT consume the slot here — that's a follow-up issue once the
// user_totp_recovery_codes table lands; the verifier here is read-only.
func (s *Service) verifyTOTPOrRecovery(in Input, rec TOTPRecord) bool {
	if in.TOTPCode != "" {
		return totp.Verify(rec.SecretBase32, in.TOTPCode)
	}
	if in.RecoveryCode != "" {
		_, ok := totp.VerifyRecoveryCode(in.RecoveryCode, rec.RecoveryHashes)
		return ok
	}
	return false
}

// --- audit helpers ---
//
// The audit events shape mirrors docs/06-auth-permissions.md §13:
// each event carries the actor (user_id when known), IP, UA, plus a
// metadata blob with the email entered (raw — admins need the
// candidate to investigate enumeration attacks) and a short reason
// code for failed_login.
//
// We deliberately keep the audit step non-blocking from the user's
// point of view by ignoring the emit error: a failed audit row must
// not turn a valid login into a 500.

func (s *Service) emitAttempt(ctx context.Context, in Input, userID string) {
	opts := s.baseOpts(in)
	opts = append(opts,
		audit.WithSeverity(audit.SeverityInfo),
		audit.WithMetadata(map[string]any{"email": in.Email}),
	)
	_ = s.actorEmitter(userID).Emit(ctx, "auth.login.attempt", opts...)
}

func (s *Service) emitSuccess(ctx context.Context, in Input, userID string) {
	opts := s.baseOpts(in)
	opts = append(opts,
		audit.WithSeverity(audit.SeverityInfo),
		audit.WithTarget("user", userID),
	)
	_ = s.actorEmitter(userID).Emit(ctx, "auth.login.success", opts...)
}

func (s *Service) emitFailed(ctx context.Context, in Input, userID, reason string) {
	opts := s.baseOpts(in)
	opts = append(opts,
		audit.WithSeverity(audit.SeverityWarning),
		audit.WithMetadata(map[string]any{
			"email":  in.Email,
			"reason": reason,
		}),
	)
	_ = s.actorEmitter(userID).Emit(ctx, "auth.login.failed", opts...)
}

func (s *Service) emitLocked(ctx context.Context, in Input, userID string, lockedUntil time.Time) {
	opts := s.baseOpts(in)
	opts = append(opts,
		audit.WithSeverity(audit.SeverityWarning),
		audit.WithTarget("user", userID),
		audit.WithMetadata(map[string]any{
			"locked_until": lockedUntil.UTC().Format(time.RFC3339),
		}),
	)
	_ = s.actorEmitter(userID).Emit(ctx, "auth.login.locked", opts...)
}

// actorEmitter pins the actor on a derived emitter without mutating
// the base one. Empty userID returns the root emitter — useful for
// pre-auth events where we don't yet know who the user is.
func (s *Service) actorEmitter(userID string) *audit.Emitter {
	if userID == "" {
		return s.deps.AuditEmitter
	}
	return s.deps.AuditEmitter.WithActor(userID)
}

// baseOpts builds the per-emit options that carry IP + user-agent.
// We use WithIP and a metadata "user_agent" key rather than the
// audit package's WithHTTP because the service doesn't see the
// *http.Request — the handler hands it strings.
func (s *Service) baseOpts(in Input) []audit.EmitOption {
	opts := make([]audit.EmitOption, 0, 2)
	if in.IP != "" {
		opts = append(opts, audit.WithIP(in.IP))
	}
	if in.UserAgent != "" {
		opts = append(opts, audit.WithMetadata(map[string]any{
			"user_agent": in.UserAgent,
		}))
	}
	return opts
}

// withRetryAfter wraps err so the handler can extract a Retry-After
// hint without sniffing the error message. Implemented as a thin
// struct so errors.Is(unwrap chain) still hits the sentinel.
type retryAfterError struct {
	inner error
	wait  time.Duration
}

func withRetryAfter(err error, wait time.Duration) error {
	return &retryAfterError{inner: err, wait: wait}
}

func (e *retryAfterError) Error() string {
	return e.inner.Error()
}

func (e *retryAfterError) Unwrap() error {
	return e.inner
}

// retryAfter extracts the wait hint from err if it was wrapped via
// withRetryAfter. Returns 0 otherwise.
func retryAfter(err error) time.Duration {
	var r *retryAfterError
	if errors.As(err, &r) {
		return r.wait
	}
	return 0
}
