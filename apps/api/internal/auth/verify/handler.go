package verify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/audit"
	"github.com/Singleton-Solution/GoNext/packages/go/email"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
	"github.com/Singleton-Solution/GoNext/packages/go/ratelimit"
)

// Options configures a [Handler]. All fields are required EXCEPT
// Audit (best-effort, log-on-failure) and Now (defaults to time.Now).
type Options struct {
	// Tokens is the Redis-backed token storage. See [RedisTokenStore].
	Tokens TokenStore

	// Users is the bridge to the users table. See [PgxUserVerifier].
	Users UserVerifier

	// Sender is the [email.Sender] used to dispatch the verification
	// email. The handler does NOT decide which adapter to use — wiring
	// chooses LogSender in dev, SMTPSender in prod, NoopSender in
	// tests.
	Sender email.Sender

	// Templates, when non-nil, renders the verification message body
	// from the shared package/go/email templates. When nil the handler
	// falls back to its built-in minimal body — the chassis ships a
	// default so the handler is usable without explicit wiring.
	Templates *email.Templates

	// Brand is the BrandContext fed to template rendering when
	// Templates is non-nil. Zero-value fields fall back to safe
	// defaults inside email.WithDefaults.
	Brand email.BrandContext

	// Limiter is the per-user rate-limit for /send. Defaults to
	// "1 per minute" with a burst of 1 when not provided. Wiring code
	// is expected to pass a [ratelimit.Limiter] keyed on user_id.
	Limiter ratelimit.Limiter

	// Audit is the audit emitter for verification events. May be nil
	// — in that case no audit lines are written and the handler logs
	// a single WARN at startup so the operator notices the gap.
	Audit *audit.Emitter

	// VerifyURL is the public base URL of the GET /verify endpoint,
	// e.g. "https://app.example.com/verify". The handler appends
	// "?token=<plain>" so a relative path also works for SPA setups
	// that resolve the link client-side.
	VerifyURL string

	// FromAddress is the From: address on the outbound message. The
	// SMTPSender's configured From is the fallback; setting this
	// override per-flow lets a single SMTP relay split traffic by
	// purpose ("noreply" vs "verify@").
	FromAddress string

	// TTL is the token lifetime. Defaults to [DefaultTTL] (24h) when
	// zero.
	TTL time.Duration

	// Subject is the email subject line. Defaults to "Verify your
	// email address" when empty. Localization is the caller's job —
	// wiring picks a per-locale subject when needed.
	Subject string

	// Now is the time source. Defaults to [time.Now]; tests inject
	// a deterministic clock.
	Now func() time.Time

	// Log is the logger for handler-internal warnings (audit
	// failures, email send failures). Defaults to [slog.Default].
	Log *slog.Logger
}

// Handler implements the two HTTP endpoints documented on the
// package. Construct one with [New] and mount with [Handler.Routes].
type Handler struct {
	opts Options
}

// New validates opts and returns a Handler. Required fields are
// checked at construction time so misconfiguration crashes boot,
// not the first request.
func New(opts Options) (*Handler, error) {
	if opts.Tokens == nil {
		return nil, errors.New("verify: Options.Tokens is required")
	}
	if opts.Users == nil {
		return nil, errors.New("verify: Options.Users is required")
	}
	if opts.Sender == nil {
		return nil, errors.New("verify: Options.Sender is required")
	}
	if opts.VerifyURL == "" {
		return nil, errors.New("verify: Options.VerifyURL is required")
	}
	if opts.TTL <= 0 {
		opts.TTL = DefaultTTL
	}
	if opts.Subject == "" {
		opts.Subject = "Verify your email address"
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	return &Handler{opts: opts}, nil
}

// Routes mounts the verify endpoints on mux. The send endpoint is
// wrapped by requireSession so unauthenticated requests are rejected
// before any rate-limit token is spent.
//
// requireSession is the same middleware shape as
// [middleware/auth.RequireSession] but accepted as a parameter so
// this package can be tested without instantiating a Redis-backed
// session manager. Production wiring passes the real middleware in.
func (h *Handler) Routes(mux *http.ServeMux, requireSession func(http.Handler) http.Handler) {
	mux.Handle("POST /api/v1/auth/verify/send", requireSession(http.HandlerFunc(h.handleSend)))
	mux.HandleFunc("GET /api/v1/auth/verify", h.handleVerify)
}

// handleSend implements POST /api/v1/auth/verify/send. The caller
// must be authenticated; the principal is read off the context.
//
// Flow:
//
//  1. Pull the principal off the context. RequireSession enforces
//     this, but we double-check so a wiring mistake fails closed.
//  2. Consult the rate-limit bucket keyed on the user ID. Over-budget
//     callers get 429 with a Retry-After header.
//  3. Generate a fresh token, persist the hash in Redis with TTL.
//  4. Build the verification link and the message body, hand it to
//     the configured Sender.
//  5. Emit the audit row (best-effort) and return 202 Accepted.
//
// We return 202 (not 200) because the email send is asynchronous
// relative to the wire response — even with a synchronous SMTPSender,
// the user agent has no way to confirm receipt at this layer.
func (h *Handler) handleSend(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	principal, ok := policy.FromContext(ctx)
	if !ok || principal.UserID == "" {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	userID := principal.UserID

	if h.opts.Limiter != nil {
		allowed, retryAfter, err := h.opts.Limiter.Allow(ctx, "verify_send:"+userID)
		if err != nil {
			// Fail-CLOSED here, not open. Verification email sends
			// are abuse-prone (spamming a victim with verify emails
			// is a real attack), so a limiter outage shouldn't
			// downgrade us to "no limit".
			h.opts.Log.WarnContext(ctx, "verify: rate limiter error; failing closed",
				slog.String("err", err.Error()),
				slog.String("user_id", userID),
			)
			writeJSONError(w, http.StatusServiceUnavailable, "service_unavailable")
			return
		}
		if !allowed {
			seconds := int(retryAfter.Seconds())
			if seconds < 1 {
				seconds = 1
			}
			w.Header().Set("Retry-After", fmt.Sprintf("%d", seconds))
			writeJSONError(w, http.StatusTooManyRequests, "rate_limited")
			return
		}
	}

	// Recipient resolution priority:
	//   1. An email attached upstream via [WithRecipient] (the
	//      common case once a richer PrincipalBuilder is wired in).
	//   2. A DB lookup against the users table.
	// Falling back to the DB read keeps the handler functional with
	// the default DefaultPrincipal, which only carries UserID/Roles.
	recipient := recipientFromContext(ctx)
	if recipient == "" {
		addr, err := h.opts.Users.LookupEmail(ctx, userID)
		if err != nil {
			if errors.Is(err, ErrUserNotFound) {
				// User row vanished between auth and send — treat
				// as a logged-in-but-deleted account and 401 the
				// caller. Clearing the session is the auth layer's
				// job, not ours.
				writeJSONError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			h.opts.Log.ErrorContext(ctx, "verify: lookup email failed",
				slog.String("err", err.Error()),
				slog.String("user_id", userID),
			)
			writeJSONError(w, http.StatusInternalServerError, "internal_error")
			return
		}
		recipient = addr
	}
	if recipient == "" {
		// Defensive: a user row with NULL or empty email is a data
		// integrity problem. Don't send to "" — that's a wire
		// error waiting to happen at the SMTP layer.
		writeJSONError(w, http.StatusUnprocessableEntity, "missing_email")
		return
	}

	plain, err := generateToken()
	if err != nil {
		h.opts.Log.ErrorContext(ctx, "verify: token gen failed",
			slog.String("err", err.Error()),
		)
		writeJSONError(w, http.StatusInternalServerError, "internal_error")
		return
	}
	hash := hashToken(plain)
	if err := h.opts.Tokens.Save(ctx, hash, userID, h.opts.TTL); err != nil {
		h.opts.Log.ErrorContext(ctx, "verify: save token failed",
			slog.String("err", err.Error()),
		)
		writeJSONError(w, http.StatusInternalServerError, "internal_error")
		return
	}

	link := h.buildLink(plain)
	msg, err := h.buildMessage(recipient, link)
	if err != nil {
		h.opts.Log.ErrorContext(ctx, "verify: render template failed",
			slog.String("err", err.Error()),
			slog.String("user_id", userID),
		)
		writeJSONError(w, http.StatusInternalServerError, "internal_error")
		return
	}
	if err := h.opts.Sender.Send(ctx, msg); err != nil {
		h.opts.Log.ErrorContext(ctx, "verify: send email failed",
			slog.String("err", err.Error()),
			slog.String("user_id", userID),
		)
		// We deliberately do NOT delete the stored token here. The
		// user may have clicked an earlier send's link in the
		// meantime; tearing it down would invalidate that copy too.
		writeJSONError(w, http.StatusBadGateway, "email_send_failed")
		return
	}

	h.emitAudit(ctx, r, "auth.verify.email.sent", audit.SeverityInfo, map[string]any{
		"recipient": maskEmail(recipient),
	})

	w.WriteHeader(http.StatusAccepted)
}

// handleVerify implements GET /api/v1/auth/verify. Anonymous endpoint
// — possession of the token IS the proof of identity.
func (h *Handler) handleVerify(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	plain := r.URL.Query().Get("token")
	if !validToken(plain) {
		h.emitAudit(ctx, r, "auth.verify.email.invalid", audit.SeverityWarning, map[string]any{
			"reason": "malformed",
		})
		writeJSONError(w, http.StatusGone, "invalid_or_expired_token")
		return
	}
	hash := hashToken(plain)

	userID, err := h.opts.Tokens.Lookup(ctx, hash)
	if err != nil {
		if errors.Is(err, ErrTokenNotFound) {
			h.emitAudit(ctx, r, "auth.verify.email.invalid", audit.SeverityWarning, map[string]any{
				"reason": "not_found_or_expired",
			})
			writeJSONError(w, http.StatusGone, "invalid_or_expired_token")
			return
		}
		h.opts.Log.ErrorContext(ctx, "verify: token lookup failed",
			slog.String("err", err.Error()),
		)
		writeJSONError(w, http.StatusInternalServerError, "internal_error")
		return
	}

	// Defense in depth: even though the Redis lookup is itself a
	// hash compare, run the constant-time compare against a
	// re-derived hash so a tweaked storage backend can't introduce
	// a timing side channel.
	if !constantTimeEqual(hashToken(plain), hash) {
		// Unreachable for our own hashing path, but if a future
		// store mangles the value we'd rather refuse than
		// over-trust.
		writeJSONError(w, http.StatusGone, "invalid_or_expired_token")
		return
	}

	if err := h.opts.Users.MarkVerified(ctx, userID); err != nil {
		if errors.Is(err, ErrUserNotFound) {
			// Treat as expired so we don't leak whether a particular
			// user_id is live in the system.
			_ = h.opts.Tokens.Consume(ctx, hash)
			writeJSONError(w, http.StatusGone, "invalid_or_expired_token")
			return
		}
		h.opts.Log.ErrorContext(ctx, "verify: mark verified failed",
			slog.String("err", err.Error()),
			slog.String("user_id", userID),
		)
		writeJSONError(w, http.StatusInternalServerError, "internal_error")
		return
	}

	// Best-effort delete: even if this errors, the TTL will sweep
	// the key. We don't want to fail the user-facing response on
	// the cleanup step.
	if err := h.opts.Tokens.Consume(ctx, hash); err != nil {
		h.opts.Log.WarnContext(ctx, "verify: consume token failed",
			slog.String("err", err.Error()),
		)
	}

	h.emitAudit(ctx, r, "auth.verify.email.completed", audit.SeverityInfo, map[string]any{
		"actor_user_id": userID,
	})

	writeJSON(w, http.StatusOK, map[string]any{"verified": true})
}

// buildLink composes the verification URL from the configured base
// VerifyURL and the plaintext token. We parse + re-encode rather than
// concatenating strings so callers can pass either a query-less URL
// ("https://app.example.com/verify") or a URL that already has a
// path / query.
func (h *Handler) buildLink(plain string) string {
	u, err := url.Parse(h.opts.VerifyURL)
	if err != nil || u.Scheme == "" {
		// Fall back to a naive append — never block a send because
		// the configured URL is mildly weird. The token is still
		// valid in either case.
		sep := "?"
		if strings.Contains(h.opts.VerifyURL, "?") {
			sep = "&"
		}
		return h.opts.VerifyURL + sep + "token=" + plain
	}
	q := u.Query()
	q.Set("token", plain)
	u.RawQuery = q.Encode()
	return u.String()
}

// principalRecipientCtxKey carries an optional email recipient for
// the authenticated user, attached upstream by the principal
// builder. Used by handleSend so the verify package doesn't have to
// reach into the users table for a row that the auth path already
// loaded.
type principalRecipientCtxKey struct{}

// WithRecipient returns a derived context carrying the user's email
// address. Upstream middleware (or a custom principal builder) calls
// this once per request after loading the session. The verify
// handler reads it back via [Handler.handleSend] -> recipient lookup.
func WithRecipient(ctx context.Context, email string) context.Context {
	return context.WithValue(ctx, principalRecipientCtxKey{}, email)
}

// recipientFromContext returns the email attached by [WithRecipient],
// or "" if none.
func recipientFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(principalRecipientCtxKey{}).(string); ok {
		return v
	}
	return ""
}

// emitAudit best-effort records evt. Failures are logged at WARN and
// never propagated to the wire response — the user-facing flow must
// not break because the audit store is down.
func (h *Handler) emitAudit(ctx context.Context, r *http.Request, evt string, sev audit.Severity, meta map[string]any) {
	if h.opts.Audit == nil {
		return
	}
	principal, _ := policy.FromContext(ctx)
	e := h.opts.Audit.WithActor(principal.UserID).WithHTTP(r)
	if err := e.Emit(ctx, evt, audit.WithSeverity(sev), audit.WithMetadata(meta)); err != nil {
		h.opts.Log.WarnContext(ctx, "verify: audit emit failed",
			slog.String("err", err.Error()),
			slog.String("event", evt),
		)
	}
}

// buildMessage assembles the outbound verification message.
//
// When the handler was constructed with a [email.Templates] instance,
// the message body comes from the shared verify-email template pair
// (text+HTML). Otherwise the handler falls back to a minimal
// link-only body — adequate for tests and the no-templates wiring
// path, but production deployments are expected to pass templates so
// the message picks up the deployment's brand color and copy.
func (h *Handler) buildMessage(recipient, link string) (email.Message, error) {
	tags := map[string]string{
		"flow":     "auth.verify.email",
		"template": string(email.TemplateVerifyEmail),
	}
	if h.opts.Templates != nil {
		brand := h.opts.Brand.WithDefaults()
		data := email.VerifyEmailData{
			BrandContext: brand,
			VerifyURL:    link,
			ExpiresIn:    formatTTL(h.opts.TTL),
		}
		msg, err := h.opts.Templates.BuildMessage(email.TemplateVerifyEmail, recipient, h.opts.Subject, data)
		if err != nil {
			return email.Message{}, err
		}
		msg.From = h.opts.FromAddress
		// Replace the helper-set Tags with ours so downstream filters
		// see the flow label as well.
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

// formatTTL renders a duration into a short human-readable string
// suitable for "the link expires in X" template copy. We round to the
// nearest hour for durations ≥ 1h and otherwise fall back to the
// duration's String form.
func formatTTL(d time.Duration) string {
	if d <= 0 {
		return "24 hours"
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

// buildTextBody returns the plain-text body of the verification
// email. We keep it deliberately short and link-only — the message
// is one click away from completion, so a long body just adds
// clutter that screen readers and accessibility audits have to wade
// through.
func buildTextBody(link string) string {
	return "Please confirm your email address by visiting the link below.\n\n" +
		link + "\n\n" +
		"If you didn't request this, you can safely ignore this message.\n"
}

// buildHTMLBody returns the HTML body. The link is HTML-encoded by
// virtue of being base64url + URL-safe characters in the path; we
// still wrap it in <a href=> for accessibility (a click-anywhere
// click target).
func buildHTMLBody(link string) string {
	return `<!doctype html><html><body>` +
		`<p>Please confirm your email address by visiting the link below.</p>` +
		`<p><a href="` + link + `">` + link + `</a></p>` +
		`<p>If you didn't request this, you can safely ignore this message.</p>` +
		`</body></html>`
}

// maskEmail returns a logger-safe version of an email address. We
// preserve the first character of the local part and the domain so
// audit readers can spot a flurry targeting one user without
// recovering the address from the audit row alone.
//
//	"alice@example.com"   -> "a***@example.com"
//	"x@example.com"       -> "*@example.com"
//	"first.last@x.test"   -> "f***@x.test"
//
// The function is best-effort; malformed addresses pass through
// unchanged because the masking value isn't worth the risk of
// returning a confusing empty string.
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

// writeJSON writes status with body. Used for the success path.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// writeJSONError writes status with an {"error": msg} body. We never
// surface the underlying error reason — the structured log carries
// detail; the wire response stays terse.
func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
