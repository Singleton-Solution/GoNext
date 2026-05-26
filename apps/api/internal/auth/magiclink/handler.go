package magiclink

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

	"github.com/Singleton-Solution/GoNext/packages/go/audit"
	"github.com/Singleton-Solution/GoNext/packages/go/email"
	"github.com/Singleton-Solution/GoNext/packages/go/ratelimit"
	"github.com/Singleton-Solution/GoNext/packages/go/session"
)

// Routes mounted by [Handler.Routes].
const (
	// RequestPath is the request-issuance route. The handler reads
	// {email} and always returns 200 (enumeration-safe).
	RequestPath = "POST /api/v1/auth/magic-link/request"

	// VerifyPath is the link-consumption route. The handler reads
	// ?token=<hex>, mints a session on success, and redirects to
	// the configured success URL.
	VerifyPath = "GET /api/v1/auth/magic-link"
)

// maxBodyBytes caps the request body. A magic-link request body is a
// single email; 4 KiB is the same ceiling the login and password-reset
// handlers use — large enough for legitimate inputs, small enough to
// wedge a JSON parser attack.
const maxBodyBytes = 4 * 1024

// Options configures a [Handler]. Required fields are validated at
// construction time so misconfiguration crashes boot, not the first
// request.
type Options struct {
	// Tokens persists hashed token rows. Required.
	Tokens TokenStore

	// Users resolves email -> user_id. Required.
	Users UserStore

	// Sessions mints the session on successful verify. Required.
	// Production wiring passes the real *session.Manager; tests can
	// drop in a stub satisfying [SessionCreator].
	Sessions SessionCreator

	// Sender delivers the magic-link email. Required. Wiring picks
	// LogSender in dev, SMTPSender in prod, NoopSender in tests.
	Sender email.Sender

	// Templates, when non-nil, renders the magic-link body from the
	// shared email templates. When nil, the handler falls back to an
	// inline body — adequate for tests but production should wire
	// templates so the email picks up brand color and copy. The
	// magic-link template re-uses TemplatePasswordReset's wording with
	// a different ResetURL — the recipient sees "Click this link to
	// sign in" copy regardless.
	Templates *email.Templates

	// Brand is the per-deployment branding fed to template rendering.
	Brand email.BrandContext

	// LinkURL is the public base URL the user clicks, e.g.
	// "https://app.example.com/api/v1/auth/magic-link". The handler
	// appends "?token=<hex>".
	LinkURL string

	// SuccessRedirect is where a successful verify redirects to,
	// e.g. "/" for the admin shell root. Defaults to "/" when empty.
	SuccessRedirect string

	// Limiter is the per-IP token bucket for the request endpoint.
	// Required. The standard policy is 5 attempts / 15 minutes.
	Limiter ratelimit.Limiter

	// Audit is the audit emitter. May be nil — when nil no audit rows
	// are emitted and the handler logs a single WARN at startup.
	Audit *audit.Emitter

	// FromAddress overrides the per-Sender default From: address.
	// Empty falls back to the Sender's configured From.
	FromAddress string

	// Subject is the email subject line. Defaults to "Your sign-in
	// link" when empty.
	Subject string

	// TTL is the lifetime of a magic-link token. Defaults to
	// [DefaultTTL] (15 minutes) when zero.
	TTL time.Duration

	// SessionAbsoluteTTL is the absolute lifetime passed to
	// Sessions.Create. Required (> 0). Matches the deployment's login
	// session TTL.
	SessionAbsoluteTTL time.Duration

	// SessionIdleTTL is the rolling idle window passed to
	// Sessions.Create. Required (> 0, <= SessionAbsoluteTTL).
	SessionIdleTTL time.Duration

	// Insecure, when true, drops the Secure attribute from the session
	// cookie so plain-HTTP dev servers work. Production must leave it
	// false.
	Insecure bool

	// CookieDomain overrides the session cookie's Domain attribute.
	// Empty scopes the cookie to the exact host (browser default).
	CookieDomain string

	// CookieName overrides the session cookie name. Empty falls back
	// to [session.CookieName] ("sid"). Useful when the admin and
	// public shells run on the same eTLD+1.
	CookieName string

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
		return nil, errors.New("magiclink: Options.Tokens is required")
	}
	if opts.Users == nil {
		return nil, errors.New("magiclink: Options.Users is required")
	}
	if opts.Sessions == nil {
		return nil, errors.New("magiclink: Options.Sessions is required")
	}
	if opts.Sender == nil {
		return nil, errors.New("magiclink: Options.Sender is required")
	}
	if opts.LinkURL == "" {
		return nil, errors.New("magiclink: Options.LinkURL is required")
	}
	if opts.Limiter == nil {
		return nil, errors.New("magiclink: Options.Limiter is required")
	}
	if opts.SessionAbsoluteTTL <= 0 {
		return nil, errors.New("magiclink: Options.SessionAbsoluteTTL must be > 0")
	}
	if opts.SessionIdleTTL <= 0 || opts.SessionIdleTTL > opts.SessionAbsoluteTTL {
		return nil, errors.New("magiclink: Options.SessionIdleTTL must be > 0 and <= SessionAbsoluteTTL")
	}
	if opts.TTL <= 0 {
		opts.TTL = DefaultTTL
	}
	if opts.Subject == "" {
		opts.Subject = "Your sign-in link"
	}
	if opts.SuccessRedirect == "" {
		opts.SuccessRedirect = "/"
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	if opts.Audit == nil {
		opts.Log.Warn("magiclink: no audit emitter wired; events will be dropped")
	}
	return &Handler{opts: opts}, nil
}

// Routes mounts both endpoints on mux. The endpoints are anonymous;
// rate-limit on the request endpoint is enforced per-IP at the start
// of the handler.
func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc(RequestPath, h.handleRequest)
	mux.HandleFunc(VerifyPath, h.handleVerify)
}

// requestBody is the JSON shape of POST /magic-link/request.
type requestBody struct {
	Email string `json:"email"`
}

// handleRequest implements POST /magic-link/request. The endpoint is
// enumeration-safe: it always returns 200 regardless of whether the
// email is known. The rate limit and audit event still record the
// attempt so a probe is visible to operators.
func (h *Handler) handleRequest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ip := clientIP(r)

	if !h.checkRateLimit(ctx, w, ip) {
		return
	}

	body, err := decodeRequestBody(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_request")
		return
	}

	emailAddr := strings.TrimSpace(body.Email)
	if emailAddr == "" {
		// Wire response stays 200 — the contract is "always 200 on a
		// well-formed request, regardless of input validity at the
		// user-existence layer". Empty email is a misconfigured
		// client, not a probe; log and move on.
		h.opts.Log.WarnContext(ctx, "magiclink: empty email in request")
		w.WriteHeader(http.StatusOK)
		return
	}

	userID, err := h.opts.Users.LookupIDByEmail(ctx, emailAddr)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			// Audit the probe but return success. The wire response
			// is identical to the success path.
			h.emitAudit(ctx, r, "auth.magic_link.requested", audit.SeverityInfo, map[string]any{
				"recipient": maskEmail(emailAddr),
				"outcome":   "no_user",
			})
			w.WriteHeader(http.StatusOK)
			return
		}
		h.opts.Log.ErrorContext(ctx, "magiclink: lookup user failed",
			slog.String("err", err.Error()))
		w.WriteHeader(http.StatusOK)
		return
	}

	plain, err := generateToken()
	if err != nil {
		h.opts.Log.ErrorContext(ctx, "magiclink: token gen failed",
			slog.String("err", err.Error()))
		w.WriteHeader(http.StatusOK)
		return
	}
	tokenHash := hashToken(plain)
	expiresAt := h.opts.Now().Add(h.opts.TTL)
	if err := h.opts.Tokens.Save(ctx, tokenHash, userID, expiresAt); err != nil {
		h.opts.Log.ErrorContext(ctx, "magiclink: save token failed",
			slog.String("err", err.Error()),
			slog.String("user_id", userID))
		w.WriteHeader(http.StatusOK)
		return
	}

	link := h.buildLink(plain)
	msg, err := h.buildMessage(emailAddr, link)
	if err != nil {
		h.opts.Log.ErrorContext(ctx, "magiclink: build email failed",
			slog.String("err", err.Error()),
			slog.String("user_id", userID))
		w.WriteHeader(http.StatusOK)
		return
	}
	if err := h.opts.Sender.Send(ctx, msg); err != nil {
		h.opts.Log.ErrorContext(ctx, "magiclink: send email failed",
			slog.String("err", err.Error()),
			slog.String("user_id", userID))
		// We do NOT delete the stored token here. The user may have
		// clicked an earlier send's link in the meantime; tearing it
		// down would invalidate that copy too. The TTL sweep cleans
		// up unredeemed tokens within 15 minutes.
		w.WriteHeader(http.StatusOK)
		return
	}

	h.emitAudit(ctx, r, "auth.magic_link.requested", audit.SeverityInfo, map[string]any{
		"actor_user_id": userID,
		"recipient":     maskEmail(emailAddr),
		"outcome":       "issued",
	})

	w.WriteHeader(http.StatusOK)
}

// handleVerify implements GET /magic-link?token=<hex>. On success it
// mints a session, sets the cookie, and redirects to the configured
// success URL. On failure it returns 410 Gone with a JSON error body
// so the admin SPA can render a friendly retry page.
func (h *Handler) handleVerify(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	plain := r.URL.Query().Get("token")
	if !validToken(plain) {
		h.emitAudit(ctx, r, "auth.magic_link.invalid", audit.SeverityWarning, map[string]any{
			"reason": "malformed",
		})
		writeJSONError(w, http.StatusGone, "invalid_or_expired_token")
		return
	}

	tokenHash := hashToken(plain)
	userID, err := h.opts.Tokens.Consume(ctx, tokenHash, h.opts.Now())
	if err != nil {
		if errors.Is(err, ErrTokenNotFound) {
			h.emitAudit(ctx, r, "auth.magic_link.invalid", audit.SeverityWarning, map[string]any{
				"reason": "not_found_or_expired",
			})
			writeJSONError(w, http.StatusGone, "invalid_or_expired_token")
			return
		}
		h.opts.Log.ErrorContext(ctx, "magiclink: consume token failed",
			slog.String("err", err.Error()))
		writeJSONError(w, http.StatusInternalServerError, "internal_error")
		return
	}

	// Mint the session. Data is empty — the magic-link flow doesn't
	// carry any payload that should persist across the session
	// lifetime; subsequent requests resolve the principal off user_id
	// the same way a password login does.
	sessToken, err := h.opts.Sessions.Create(ctx, userID, nil, h.opts.SessionAbsoluteTTL, h.opts.SessionIdleTTL)
	if err != nil {
		h.opts.Log.ErrorContext(ctx, "magiclink: session create failed",
			slog.String("err", err.Error()),
			slog.String("user_id", userID))
		writeJSONError(w, http.StatusInternalServerError, "internal_error")
		return
	}

	session.SetCookie(w, sessToken, session.CookieOptions{
		Name:     h.opts.CookieName,
		Domain:   h.opts.CookieDomain,
		MaxAge:   h.opts.SessionAbsoluteTTL,
		Insecure: h.opts.Insecure,
	})

	h.emitAudit(ctx, r, "auth.magic_link.consumed", audit.SeverityInfo, map[string]any{
		"actor_user_id": userID,
	})

	http.Redirect(w, r, h.opts.SuccessRedirect, http.StatusSeeOther)
}

// checkRateLimit consults the per-IP bucket and writes a 429 response
// if the request is over budget. Returns true when the caller should
// proceed.
//
// We fail OPEN on limiter errors — the rate-limit availability isn't
// a single point of failure for the sign-in flow; an outage of the
// limiter should not lock everyone out of receiving a sign-in link.
// (Contrast with the verify-send endpoint which fails closed because
// that limiter exists to prevent the recipient from being spammed.)
func (h *Handler) checkRateLimit(ctx context.Context, w http.ResponseWriter, ip string) bool {
	if h.opts.Limiter == nil {
		return true
	}
	allowed, retryAfter, err := h.opts.Limiter.Allow(ctx, "magiclink:request:"+ip)
	if err != nil {
		h.opts.Log.WarnContext(ctx, "magiclink: rate limiter error; failing open",
			slog.String("err", err.Error()),
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

// buildLink composes the magic-link URL from LinkURL and the plaintext
// token. Identical pattern to passwordreset.buildLink — parse and
// re-encode so callers can pass either a query-less URL or one that
// already carries path/query.
func (h *Handler) buildLink(plain string) string {
	u, err := url.Parse(h.opts.LinkURL)
	if err != nil || u.Scheme == "" {
		sep := "?"
		if strings.Contains(h.opts.LinkURL, "?") {
			sep = "&"
		}
		return h.opts.LinkURL + sep + "token=" + plain
	}
	q := u.Query()
	q.Set("token", plain)
	u.RawQuery = q.Encode()
	return u.String()
}

// buildMessage assembles the outbound magic-link email. When templates
// are wired the body comes from the shared password-reset template
// (the wording is generic enough — "click this link to set your
// session" — that the same body serves both flows). Otherwise we fall
// back to a minimal inline body.
func (h *Handler) buildMessage(recipient, link string) (email.Message, error) {
	tags := map[string]string{
		"flow":     "auth.magic_link",
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
		TextBody: buildTextBody(link, h.opts.TTL),
		HTMLBody: buildHTMLBody(link, h.opts.TTL),
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
		h.opts.Log.WarnContext(ctx, "magiclink: audit emit failed",
			slog.String("err", err.Error()),
			slog.String("event", evt))
	}
}

// decodeRequestBody decodes POST /magic-link/request's body, enforcing
// the byte cap, disallowing unknown fields, and rejecting trailing
// junk after the first JSON value.
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
		return requestBody{}, errors.New("magiclink: trailing data after JSON body")
	}
	return body, nil
}

// clientIP picks the client IP for rate-limit + audit purposes.
// Mirrors passwordreset.clientIP.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// formatTTL renders a duration for the "the link expires in X" copy.
// Magic-link TTLs are sub-hour, so the helper covers the minutes path
// explicitly (passwordreset.formatTTL hard-codes "1 hour" for the
// sub-hour branch because its DefaultTTL is 1h).
func formatTTL(d time.Duration) string {
	if d <= 0 {
		return "15 minutes"
	}
	if d >= time.Hour {
		hours := int(d.Round(time.Hour) / time.Hour)
		if hours == 1 {
			return "1 hour"
		}
		return fmt.Sprintf("%d hours", hours)
	}
	minutes := int(d.Round(time.Minute) / time.Minute)
	if minutes <= 1 {
		return "1 minute"
	}
	return fmt.Sprintf("%d minutes", minutes)
}

// buildTextBody is the inline fallback used when no Templates are
// wired. Kept deliberately short — one-click completion needs no
// preamble.
func buildTextBody(link string, ttl time.Duration) string {
	return "Click the link below to sign in. The link expires in " +
		formatTTL(ttl) + " and can only be used once.\n\n" +
		"  " + link + "\n\n" +
		"If you didn't request this, you can safely ignore this email.\n"
}

// buildHTMLBody is the HTML fallback. Same content as the text body,
// minimal markup so a screen-reader pass produces the same content.
func buildHTMLBody(link string, ttl time.Duration) string {
	return `<!doctype html><html><body>` +
		`<p>Click the link below to sign in. The link expires in ` + formatTTL(ttl) + ` and can only be used once.</p>` +
		`<p><a href="` + link + `">` + link + `</a></p>` +
		`<p>If you didn't request this, you can safely ignore this email.</p>` +
		`</body></html>`
}

// maskEmail returns a logger-safe version of an email address.
// Mirrors passwordreset.maskEmail.
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

// writeJSONError mirrors the verify and passwordreset packages'
// helpers. We keep a copy here so the package doesn't import a peer
// for one trivial helper.
func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
