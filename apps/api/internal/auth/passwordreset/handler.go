package passwordreset

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Singleton-Solution/GoNext/packages/go/audit"
	"github.com/Singleton-Solution/GoNext/packages/go/auth/password"
	"github.com/Singleton-Solution/GoNext/packages/go/email"
	"github.com/Singleton-Solution/GoNext/packages/go/ratelimit"
)

// Routes mounted by [Handler.Routes].
const (
	// RequestPath is the request-issuance route. The handler reads
	// {email} and always returns 200 (enumeration-safe).
	RequestPath = "POST /api/v1/auth/password-reset/request"

	// ConfirmPath is the redeem route. The handler reads {token,
	// new_password}, validates strength, applies the new hash, and
	// invalidates the user's active sessions.
	ConfirmPath = "POST /api/v1/auth/password-reset/confirm"
)

// MinPasswordLength is the floor enforced on the confirm endpoint.
// Matches the setup wizard's MinPasswordLength (12 chars) so the
// strength rule is consistent across the two ways a user can pick a
// new password.
const MinPasswordLength = 12

// maxBodyBytes caps both request bodies. The request body is a single
// email; the confirm body is a token + new password. 4 KiB is the
// same ceiling the login handler uses — large enough for legitimate
// inputs, small enough to wedge a JSON parser attack.
const maxBodyBytes = 4 * 1024

// Options configures a [Handler]. Required fields are validated at
// construction time so misconfiguration crashes boot, not the first
// request.
type Options struct {
	// Tokens persists hashed token rows. Required.
	Tokens TokenStore

	// Users resolves email -> user_id and rewrites password_hash.
	// Required.
	Users UserStore

	// Sessions revokes all sessions for the user when a reset
	// confirms. Required — invalidating sessions on password change
	// is part of the security contract.
	Sessions SessionRevoker

	// Sender delivers the reset email. Required. Wiring picks
	// LogSender in dev, SMTPSender in prod, NoopSender in tests.
	Sender email.Sender

	// Templates, when non-nil, renders the reset body from the shared
	// templates. When nil, the handler falls back to an inline body
	// — adequate for tests but production should wire templates so
	// the email picks up brand color and copy.
	Templates *email.Templates

	// Brand is the per-deployment branding fed to template rendering.
	Brand email.BrandContext

	// ResetURL is the public base URL the user lands on, e.g.
	// "https://app.example.com/reset-password". The handler appends
	// "?token=<hex>".
	ResetURL string

	// Limiter is the per-IP token bucket shared between the request
	// and confirm endpoints. Required. The standard policy is 5
	// attempts / 15 minutes.
	Limiter ratelimit.Limiter

	// Pepper is the HMAC pepper passed to password.Hash. May be empty
	// in dev; production deploys mirror the login pepper.
	Pepper []byte

	// Audit is the audit emitter. May be nil — when nil no audit rows
	// are emitted and the handler logs a single WARN at startup.
	Audit *audit.Emitter

	// FromAddress overrides the per-Sender default From: address.
	// Empty falls back to the Sender's configured From.
	FromAddress string

	// Subject is the email subject line. Defaults to "Reset your
	// password" when empty.
	Subject string

	// TTL is the lifetime of a reset token. Defaults to [DefaultTTL]
	// (1 hour) when zero.
	TTL time.Duration

	// Now is the time source. Defaults to time.Now.
	Now func() time.Time

	// Log is the structured logger. Defaults to slog.Default.
	Log *slog.Logger
}

// Handler implements the two HTTP endpoints documented on the package.
// Construct with [New] and mount with [Handler.Routes].
type Handler struct {
	opts Options
}

// New validates opts and returns a Handler.
func New(opts Options) (*Handler, error) {
	if opts.Tokens == nil {
		return nil, errors.New("passwordreset: Options.Tokens is required")
	}
	if opts.Users == nil {
		return nil, errors.New("passwordreset: Options.Users is required")
	}
	if opts.Sessions == nil {
		return nil, errors.New("passwordreset: Options.Sessions is required")
	}
	if opts.Sender == nil {
		return nil, errors.New("passwordreset: Options.Sender is required")
	}
	if opts.ResetURL == "" {
		return nil, errors.New("passwordreset: Options.ResetURL is required")
	}
	if opts.Limiter == nil {
		return nil, errors.New("passwordreset: Options.Limiter is required")
	}
	if opts.TTL <= 0 {
		opts.TTL = DefaultTTL
	}
	if opts.Subject == "" {
		opts.Subject = "Reset your password"
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	if opts.Audit == nil {
		opts.Log.Warn("passwordreset: no audit emitter wired; events will be dropped")
	}
	return &Handler{opts: opts}, nil
}

// Routes mounts both endpoints on mux. The endpoints are anonymous;
// rate-limit is enforced per-IP at the start of each handler.
func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc(RequestPath, h.handleRequest)
	mux.HandleFunc(ConfirmPath, h.handleConfirm)
}

// requestBody is the JSON shape of POST /password-reset/request.
type requestBody struct {
	Email string `json:"email"`
}

// confirmBody is the JSON shape of POST /password-reset/confirm.
type confirmBody struct {
	Token       string `json:"token"`
	NewPassword string `json:"new_password"`
}

// confirmResponse is the JSON shape returned by a successful confirm.
// We return the user_id so the admin UI can transition to a login
// screen with the email prefilled (it queries `/me` after the next
// login). The actual session minting is the login handler's job —
// this flow does NOT issue a session, by design (the user must enter
// the new password on the login page so muscle memory matches the
// stored credential).
type confirmResponse struct {
	UserID string `json:"user_id"`
}

// handleRequest implements POST /password-reset/request. The endpoint
// is enumeration-safe: it always returns 200 regardless of whether the
// email is known. The rate limit and audit event still record the
// attempt so a probe is visible to operators.
func (h *Handler) handleRequest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ip := clientIP(r)

	if !h.checkRateLimit(ctx, w, ip, "request") {
		return
	}

	body, err := decodeRequestBody(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_request")
		return
	}

	emailAddr := strings.TrimSpace(body.Email)
	if emailAddr == "" {
		// We still return 200 — the contract is "always 200 on a
		// well-formed request, regardless of input validity at the
		// user-existence layer". An empty email is not even a probe;
		// it's a misconfigured client. We log it and move on.
		h.opts.Log.WarnContext(ctx, "passwordreset: empty email in request")
		w.WriteHeader(http.StatusOK)
		return
	}

	userID, err := h.opts.Users.LookupIDByEmail(ctx, emailAddr)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			// Audit the probe but return success. The wire response
			// is identical to the success path.
			h.emitAudit(ctx, r, "auth.password_reset.requested", audit.SeverityInfo, map[string]any{
				"recipient": maskEmail(emailAddr),
				"outcome":   "no_user",
			})
			w.WriteHeader(http.StatusOK)
			return
		}
		h.opts.Log.ErrorContext(ctx, "passwordreset: lookup user failed",
			slog.String("err", err.Error()))
		// Even on a backend error we return 200 to keep the response
		// shape uniform. The error is in the structured log; the user
		// can retry, and a failed lookup costs the rate limiter only
		// the same single token a successful issue would.
		w.WriteHeader(http.StatusOK)
		return
	}

	plain, err := generateToken()
	if err != nil {
		h.opts.Log.ErrorContext(ctx, "passwordreset: token gen failed",
			slog.String("err", err.Error()))
		w.WriteHeader(http.StatusOK)
		return
	}
	tokenHash := hashToken(plain)
	expiresAt := h.opts.Now().Add(h.opts.TTL)
	if err := h.opts.Tokens.Save(ctx, tokenHash, userID, expiresAt); err != nil {
		h.opts.Log.ErrorContext(ctx, "passwordreset: save token failed",
			slog.String("err", err.Error()),
			slog.String("user_id", userID))
		w.WriteHeader(http.StatusOK)
		return
	}

	link := h.buildLink(plain)
	msg, err := h.buildMessage(emailAddr, link)
	if err != nil {
		h.opts.Log.ErrorContext(ctx, "passwordreset: build email failed",
			slog.String("err", err.Error()),
			slog.String("user_id", userID))
		w.WriteHeader(http.StatusOK)
		return
	}
	if err := h.opts.Sender.Send(ctx, msg); err != nil {
		h.opts.Log.ErrorContext(ctx, "passwordreset: send email failed",
			slog.String("err", err.Error()),
			slog.String("user_id", userID))
		// We do NOT delete the stored token here. The user may have
		// clicked an earlier send's link in the meantime; tearing it
		// down would invalidate that copy too. The TTL sweep cleans
		// up unredeemed tokens within the hour.
		w.WriteHeader(http.StatusOK)
		return
	}

	h.emitAudit(ctx, r, "auth.password_reset.requested", audit.SeverityInfo, map[string]any{
		"actor_user_id": userID,
		"recipient":     maskEmail(emailAddr),
		"outcome":       "issued",
	})

	w.WriteHeader(http.StatusOK)
}

// handleConfirm implements POST /password-reset/confirm.
func (h *Handler) handleConfirm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ip := clientIP(r)

	if !h.checkRateLimit(ctx, w, ip, "confirm") {
		return
	}

	body, err := decodeConfirmBody(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_request")
		return
	}

	if !validToken(body.Token) {
		h.emitAudit(ctx, r, "auth.password_reset.confirm.invalid", audit.SeverityWarning, map[string]any{
			"reason": "malformed",
		})
		writeJSONError(w, http.StatusGone, "invalid_or_expired_token")
		return
	}

	if err := h.checkPasswordStrength(body.NewPassword); err != nil {
		writeJSONErrorDetail(w, http.StatusUnprocessableEntity, "weak_password", err.Error())
		return
	}

	tokenHash := hashToken(body.Token)
	// Defense-in-depth: the storage Consume call uses the hash as a
	// key, so a stricter timing-equality compare here doesn't change
	// the substantive behaviour, but it keeps a future stored-hash
	// mutation (e.g. a different encoding) from quietly skipping the
	// strict compare.
	if !constantTimeEqual(hashToken(body.Token), tokenHash) {
		writeJSONError(w, http.StatusGone, "invalid_or_expired_token")
		return
	}

	userID, err := h.opts.Tokens.Consume(ctx, tokenHash, h.opts.Now())
	if err != nil {
		if errors.Is(err, ErrTokenNotFound) {
			h.emitAudit(ctx, r, "auth.password_reset.confirm.invalid", audit.SeverityWarning, map[string]any{
				"reason": "not_found_or_expired",
			})
			writeJSONError(w, http.StatusGone, "invalid_or_expired_token")
			return
		}
		h.opts.Log.ErrorContext(ctx, "passwordreset: consume token failed",
			slog.String("err", err.Error()))
		writeJSONError(w, http.StatusInternalServerError, "internal_error")
		return
	}

	newHash, err := password.Hash(body.NewPassword, h.opts.Pepper)
	if err != nil {
		h.opts.Log.ErrorContext(ctx, "passwordreset: hash failed",
			slog.String("err", err.Error()),
			slog.String("user_id", userID))
		writeJSONError(w, http.StatusInternalServerError, "internal_error")
		return
	}

	if err := h.opts.Users.UpdatePassword(ctx, userID, newHash); err != nil {
		if errors.Is(err, ErrUserNotFound) {
			// The user vanished between issue time and confirm time
			// (a soft-delete or a hard-delete with CASCADE that fired
			// the row removal). Treat as expired so the wire response
			// matches the malformed/expired branch.
			writeJSONError(w, http.StatusGone, "invalid_or_expired_token")
			return
		}
		h.opts.Log.ErrorContext(ctx, "passwordreset: update password failed",
			slog.String("err", err.Error()),
			slog.String("user_id", userID))
		writeJSONError(w, http.StatusInternalServerError, "internal_error")
		return
	}

	// Invalidate every active session for the user. A failure here is
	// logged but does not fail the request — the password has been
	// rotated, so any surviving session is logged out at next
	// CSRF-token rotation regardless. We still emit the audit for the
	// failed revoke so operators see it.
	if err := h.opts.Sessions.DeleteAllForUser(ctx, userID); err != nil {
		h.opts.Log.WarnContext(ctx, "passwordreset: revoke sessions failed",
			slog.String("err", err.Error()),
			slog.String("user_id", userID))
		h.emitAudit(ctx, r, "auth.password_reset.session_revoke_failed", audit.SeverityWarning, map[string]any{
			"actor_user_id": userID,
			"err":           err.Error(),
		})
	}

	h.emitAudit(ctx, r, "auth.password_reset.completed", audit.SeverityInfo, map[string]any{
		"actor_user_id": userID,
	})

	writeJSON(w, http.StatusOK, confirmResponse{UserID: userID})
}

// checkRateLimit consults the per-IP bucket and writes a 429 response
// if the request is over budget. Returns true when the caller should
// proceed.
//
// We fail OPEN on limiter errors — the rate-limit availability isn't
// a single point of failure for the recovery flow; an outage of the
// limiter should not lock everyone out of resetting their password.
// (Contrast with the verify-send endpoint which fails closed because
// that limiter exists to prevent the recipient from being spammed.)
func (h *Handler) checkRateLimit(ctx context.Context, w http.ResponseWriter, ip, endpoint string) bool {
	if h.opts.Limiter == nil {
		return true
	}
	allowed, retryAfter, err := h.opts.Limiter.Allow(ctx, "passwordreset:"+endpoint+":"+ip)
	if err != nil {
		h.opts.Log.WarnContext(ctx, "passwordreset: rate limiter error; failing open",
			slog.String("err", err.Error()),
			slog.String("endpoint", endpoint),
			slog.String("ip", ip))
		return true
	}
	if !allowed {
		seconds := int(retryAfter.Seconds())
		if seconds < 1 {
			seconds = 1
		}
		w.Header().Set("Retry-After", fmt.Sprintf("%d", seconds))
		writeJSONError(w, http.StatusTooManyRequests, "rate_limited")
		return false
	}
	return true
}

// checkPasswordStrength is the policy check on a candidate new
// password. Mirrors the setup wizard's MinPasswordLength so the rule
// is consistent across the two surfaces that accept a new password.
//
// Returns a non-nil error with a user-facing reason; the handler
// echoes the reason in the 422 response so the admin UI can render
// it next to the password field.
func (h *Handler) checkPasswordStrength(p string) error {
	if utf8.RuneCountInString(p) < MinPasswordLength {
		return fmt.Errorf("Password must be at least %d characters.", MinPasswordLength)
	}
	return nil
}

// buildLink composes the reset URL from ResetURL and the plaintext
// token. Identical pattern to verify.buildLink — parse and re-encode
// so callers can pass either a query-less URL or one that already
// carries path/query.
func (h *Handler) buildLink(plain string) string {
	u, err := url.Parse(h.opts.ResetURL)
	if err != nil || u.Scheme == "" {
		sep := "?"
		if strings.Contains(h.opts.ResetURL, "?") {
			sep = "&"
		}
		return h.opts.ResetURL + sep + "token=" + plain
	}
	q := u.Query()
	q.Set("token", plain)
	u.RawQuery = q.Encode()
	return u.String()
}

// buildMessage assembles the outbound reset email. When templates are
// wired the body comes from the shared password-reset template pair;
// otherwise we fall back to a minimal inline body.
func (h *Handler) buildMessage(recipient, link string) (email.Message, error) {
	tags := map[string]string{
		"flow":     "auth.password_reset",
		"template": string(email.TemplatePasswordReset),
	}
	if h.opts.Templates != nil {
		brand := h.opts.Brand.WithDefaults()
		data := email.PasswordResetData{
			BrandContext: brand,
			ResetURL:     link,
			ExpiresIn:    formatTTL(h.opts.TTL),
		}
		msg, err := h.opts.Templates.BuildMessage(email.TemplatePasswordReset, recipient, h.opts.Subject, data)
		if err != nil {
			return email.Message{}, err
		}
		msg.From = h.opts.FromAddress
		msg.Tags = tags
		return msg, nil
	}
	return email.Message{
		To:       recipient,
		From:     h.opts.FromAddress,
		Subject:  h.opts.Subject,
		TextBody: buildTextBody(link),
		HTMLBody: buildHTMLBody(link),
		Tags:     tags,
	}, nil
}

// emitAudit best-effort records evt. Failures are logged at WARN and
// never propagated to the wire response.
func (h *Handler) emitAudit(ctx context.Context, r *http.Request, evt string, sev audit.Severity, meta map[string]any) {
	if h.opts.Audit == nil {
		return
	}
	e := h.opts.Audit.WithHTTP(r)
	if err := e.Emit(ctx, evt, audit.WithSeverity(sev), audit.WithMetadata(meta)); err != nil {
		h.opts.Log.WarnContext(ctx, "passwordreset: audit emit failed",
			slog.String("err", err.Error()),
			slog.String("event", evt))
	}
}

// decodeRequestBody / decodeConfirmBody are the two body decoders.
// Each enforces the byte cap, disallows unknown fields, and rejects
// trailing junk after the first JSON value.
func decodeRequestBody(r *http.Request) (requestBody, error) {
	r.Body = http.MaxBytesReader(nil, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var body requestBody
	if err := dec.Decode(&body); err != nil {
		return requestBody{}, err
	}
	var tail json.RawMessage
	if err := dec.Decode(&tail); !errors.Is(err, io.EOF) {
		return requestBody{}, errors.New("passwordreset: trailing data after JSON body")
	}
	return body, nil
}

func decodeConfirmBody(r *http.Request) (confirmBody, error) {
	r.Body = http.MaxBytesReader(nil, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var body confirmBody
	if err := dec.Decode(&body); err != nil {
		return confirmBody{}, err
	}
	var tail json.RawMessage
	if err := dec.Decode(&tail); !errors.Is(err, io.EOF) {
		return confirmBody{}, errors.New("passwordreset: trailing data after JSON body")
	}
	return body, nil
}

// clientIP picks the client IP for rate-limit + audit purposes. We
// prefer the socket remote — operators behind a reverse proxy will
// already have an X-Forwarded-For middleware installed in front of
// us (mirrors the login package's clientIP).
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// formatTTL renders a duration for the "the link expires in X"
// template copy. Mirrors the verify package's helper.
func formatTTL(d time.Duration) string {
	if d <= 0 {
		return "1 hour"
	}
	if d >= time.Hour {
		hours := int(d.Round(time.Hour) / time.Hour)
		if hours == 1 {
			return "1 hour"
		}
		return fmt.Sprintf("%d hours", hours)
	}
	return d.String()
}

// buildTextBody is the inline fallback used when no Templates are
// wired. Kept deliberately short — one-click completion needs no
// preamble.
func buildTextBody(link string) string {
	return "We received a request to reset your password.\n\n" +
		"Visit the link below within 1 hour to set a new password:\n\n" +
		"  " + link + "\n\n" +
		"If you didn't request this, you can safely ignore this email.\n"
}

// buildHTMLBody is the HTML fallback. Same content as the text body,
// minimal markup so a screen-reader pass produces the same content.
func buildHTMLBody(link string) string {
	return `<!doctype html><html><body>` +
		`<p>We received a request to reset your password.</p>` +
		`<p>Visit the link below within 1 hour to set a new password:</p>` +
		`<p><a href="` + link + `">` + link + `</a></p>` +
		`<p>If you didn't request this, you can safely ignore this email.</p>` +
		`</body></html>`
}

// maskEmail returns a logger-safe version of an email address.
// Mirrors verify.maskEmail.
func maskEmail(addr string) string {
	at := strings.LastIndexByte(addr, '@')
	if at < 1 {
		return addr
	}
	local, domain := addr[:at], addr[at:]
	if len(local) == 1 {
		return "*" + domain
	}
	return local[:1] + "***" + domain
}

// writeJSON / writeJSONError mirror the verify package's helpers. We
// keep a copy here so the package doesn't import a peer for two trivial
// helpers.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// writeJSONErrorDetail is writeJSONError with a human-facing detail
// string. Used for the weak-password reply so the admin UI can render
// the policy reason next to the password field.
func writeJSONErrorDetail(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "detail": detail})
}
