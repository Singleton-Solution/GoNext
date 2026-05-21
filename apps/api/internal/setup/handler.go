package setup

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
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Singleton-Solution/GoNext/packages/go/session"
)

// Paths exposed by the package. Both live under /api/v1/setup so the
// reverse-proxy CORS / rate-limit rules that already cover /api/v1/*
// apply without per-route exceptions.
const (
	// StatusPath returns the current install state. Safe to poll — it
	// never mutates anything and is not rate-limited (a probe response
	// is constant-size and constant-time).
	StatusPath = "GET /api/v1/setup/status"

	// InstallPath performs the one-time install. Rate-limited per IP
	// and locked once installation_completed_at is set.
	InstallPath = "POST /api/v1/setup/install"
)

// maxBodyBytes caps the install payload. Email + password + site name +
// URL are all short strings; a request body larger than this is either
// a misconfigured client or an attempt to wedge the JSON parser.
const maxBodyBytes = 8 * 1024

// Mount wires the setup package's routes onto mux. Returns an error if
// Deps is incomplete — main.go propagates that to a startup failure so
// a misconfigured deployment never starts.
func Mount(mux *http.ServeMux, d Deps) error {
	h, err := NewHandler(d)
	if err != nil {
		return err
	}
	mux.Handle(StatusPath, http.HandlerFunc(h.Status))
	mux.Handle(InstallPath, http.HandlerFunc(h.Install))
	return nil
}

// Handler holds the wired dependencies and serves both endpoints.
type Handler struct {
	d Deps
}

// NewHandler validates Deps and returns a Handler ready to serve.
func NewHandler(d Deps) (*Handler, error) {
	if err := d.validate(); err != nil {
		return nil, err
	}
	d.defaults()
	return &Handler{d: d}, nil
}

// statusResponse is the JSON returned by GET /api/v1/setup/status. We
// surface both flags so the admin middleware can branch on either
// (installation_completed for the lock, user_count for an edge case
// where someone bypassed the lock via psql but never set the option).
type statusResponse struct {
	InstallationCompleted bool `json:"installation_completed"`
	UserCount             int  `json:"user_count"`
}

// installRequest is the wire shape POST /api/v1/setup/install accepts.
// All four fields are required.
type installRequest struct {
	AdminEmail    string `json:"admin_email"`
	AdminPassword string `json:"admin_password"`
	SiteName      string `json:"site_name"`
	SiteURL       string `json:"site_url"`
}

// installResponse is the success body. The session token is delivered
// exclusively through the Set-Cookie header — echoing it in the body
// would defeat HttpOnly.
type installResponse struct {
	UserID    string    `json:"user_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

// errorResponse is the failure shape. `code` is machine-readable;
// `message` is a short developer-friendly string the wizard can show
// when it lacks a localized copy for the code yet.
type errorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

// Status implements GET /api/v1/setup/status. Used by:
//
//   - the admin middleware, on every navigation, to decide whether to
//     redirect to /setup;
//   - the wizard, on mount, to short-circuit to /admin/login if the
//     window has already closed between the operator opening the tab
//     and reaching the form.
//
// Deliberately unguarded by the rate-limiter: the response is
// constant-shape, leaks no secret, and is the very thing the wizard
// must call to know whether to render itself.
func (h *Handler) Status(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "")
		return
	}

	completed, err := h.installCompleted(r.Context())
	if err != nil {
		h.d.Log.WarnContext(r.Context(), "setup: status read failed",
			slog.String("err", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal_error", "")
		return
	}

	// user_count would ideally be a SELECT count(*) but we don't want to
	// drag a UserCounter seam in just for an integer the middleware
	// barely uses. The install marker IS the canonical signal — if it's
	// set, treat the count as ≥ 1; otherwise as 0. This matches the
	// invariant the handler maintains (install always writes both).
	count := 0
	if completed {
		count = 1
	}

	writeJSON(w, http.StatusOK, statusResponse{
		InstallationCompleted: completed,
		UserCount:             count,
	})
}

// Install implements POST /api/v1/setup/install. The handler is the
// only mutator in the package; every defense lives here:
//
//  1. Method check (POST).
//  2. Install-lock check (423 if already installed).
//  3. Per-IP rate-limit (429 with Retry-After if exhausted).
//  4. Body decode + validation (400 on shape / length / format).
//  5. Password hash + user create + option write (500 on any step
//     failing — but only after the lock check has succeeded, so a
//     post-install hijack can't slip through on a transient DB error).
//  6. Session create + cookie set + 200 JSON body.
//
// On every error path the handler emits a structured log line at WARN
// or higher; an operator triaging a stuck installer can grep on
// `component=setup` to see exactly which gate the request hit.
func (h *Handler) Install(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "")
		return
	}

	// Lock check FIRST so a post-install request never reaches the
	// rate-limit (and so an attacker can't probe whether the lock
	// exists by burning through the rate budget).
	completed, err := h.installCompleted(r.Context())
	if err != nil {
		h.d.Log.WarnContext(r.Context(), "setup: install lock read failed",
			slog.String("err", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal_error", "")
		return
	}
	if completed {
		writeError(w, http.StatusLocked, "already_installed",
			"GoNext is already installed.")
		return
	}

	// Per-IP rate limit. Anchored on the connection peer address — the
	// reverse proxy layer (if any) is expected to write X-Forwarded-For
	// and a trusted-proxy middleware to consume it before we see the
	// request. Here we use the socket so the install endpoint behaves
	// safely even on a bare bind.
	ip := clientIP(r)
	allowed, retryAfter, lErr := h.d.Limiter.Allow(r.Context(), "setup:install:"+ip)
	if lErr != nil {
		// Fail closed: a misbehaving rate-limit backend must not turn
		// the install endpoint into a free probe. Log and refuse.
		h.d.Log.WarnContext(r.Context(), "setup: rate-limit backend error",
			slog.String("err", lErr.Error()))
		writeError(w, http.StatusServiceUnavailable, "ratelimit_unavailable", "")
		return
	}
	if !allowed {
		setRetryAfter(w, retryAfter)
		writeError(w, http.StatusTooManyRequests, "rate_limited",
			"Too many install attempts. Try again later.")
		return
	}

	req, err := decodeRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	// Validate every input BEFORE any DB write. Validation errors carry
	// a stable field name so the wizard can highlight the right step.
	if vErr := req.validate(); vErr != nil {
		writeError(w, http.StatusBadRequest, vErr.code, vErr.message)
		return
	}

	// Hash + persist + mark installed. Each step is logged at WARN on
	// failure with enough context to triage but not enough to leak the
	// candidate email (which an attacker might inject to confirm a
	// timing channel).
	hash, hErr := h.d.Hash(req.AdminPassword, h.d.Pepper)
	if hErr != nil {
		h.d.Log.WarnContext(r.Context(), "setup: password hash failed",
			slog.String("err", hErr.Error()))
		writeError(w, http.StatusInternalServerError, "internal_error", "")
		return
	}

	email := strings.TrimSpace(strings.ToLower(req.AdminEmail))
	userID, cErr := h.d.Users.Create(r.Context(), UserCreateInput{
		Email:        email,
		Handle:       deriveHandle(email),
		DisplayName:  deriveDisplayName(email),
		PasswordHash: hash,
		Role:         "super_admin",
	})
	if cErr != nil {
		h.d.Log.WarnContext(r.Context(), "setup: user create failed",
			slog.String("err", cErr.Error()))
		writeError(w, http.StatusInternalServerError, "internal_error", "")
		return
	}

	// Site options. We write the operator-supplied values FIRST and the
	// install marker LAST so a partial failure (DB blip after a site
	// name update) doesn't lock the wizard out while leaving the site
	// half-configured. If the marker write fails the operator can retry
	// (the rate-limiter still allows another attempt within the budget)
	// and the user_create step will fail with a unique-email constraint
	// — surfaced as a 500 here, which the wizard renders as "try again".
	if wErr := h.d.Options.Write(r.Context(), SiteNameOptionKey, req.SiteName); wErr != nil {
		h.d.Log.WarnContext(r.Context(), "setup: site name write failed",
			slog.String("err", wErr.Error()))
		writeError(w, http.StatusInternalServerError, "internal_error", "")
		return
	}
	if wErr := h.d.Options.Write(r.Context(), SiteURLOptionKey, req.SiteURL); wErr != nil {
		h.d.Log.WarnContext(r.Context(), "setup: site URL write failed",
			slog.String("err", wErr.Error()))
		writeError(w, http.StatusInternalServerError, "internal_error", "")
		return
	}
	completedAt := h.d.Now().UTC().Format(time.RFC3339)
	if wErr := h.d.Options.Write(r.Context(), InstallationOptionKey, completedAt); wErr != nil {
		h.d.Log.WarnContext(r.Context(), "setup: install marker write failed",
			slog.String("err", wErr.Error()))
		writeError(w, http.StatusInternalServerError, "internal_error", "")
		return
	}

	// Session: log the operator straight in.
	token, sErr := h.d.Sessions.Create(r.Context(), userID, nil, h.d.SessionAbsoluteTTL, h.d.SessionIdleTTL)
	if sErr != nil {
		h.d.Log.WarnContext(r.Context(), "setup: session create failed",
			slog.String("user_id", userID),
			slog.String("err", sErr.Error()))
		writeError(w, http.StatusInternalServerError, "internal_error", "")
		return
	}
	session.SetCookie(w, token, session.CookieOptions{
		Name:     h.d.CookieName,
		Domain:   h.d.CookieDomain,
		MaxAge:   h.d.SessionAbsoluteTTL,
		Insecure: h.d.Insecure,
	})

	expiresAt := h.d.Now().Add(h.d.SessionAbsoluteTTL)
	h.d.Log.InfoContext(r.Context(), "setup: install completed",
		slog.String("user_id", userID),
	)
	writeJSON(w, http.StatusOK, installResponse{
		UserID:    userID,
		ExpiresAt: expiresAt,
	})
}

// installCompleted reads the install marker, treating any non-nil read
// as "not yet installed" rather than panicking. The caller maps a read
// error to a 500 separately.
func (h *Handler) installCompleted(ctx context.Context) (bool, error) {
	return h.d.Options.Has(ctx, InstallationOptionKey)
}

// validationError is a tiny carrier that lets validate() return a
// (field-code, message) pair the handler maps to a 400 body.
type validationError struct {
	code    string
	message string
}

// validate runs the install payload through the format + length gates.
// Each branch returns a stable code the wizard's i18n table can key off.
func (r installRequest) validate() *validationError {
	email := strings.TrimSpace(r.AdminEmail)
	if email == "" {
		return &validationError{code: "invalid_email", message: "Email is required."}
	}
	if !looksLikeEmail(email) {
		return &validationError{code: "invalid_email", message: "Email format is invalid."}
	}
	if utf8.RuneCountInString(r.AdminPassword) < MinPasswordLength {
		return &validationError{
			code: "weak_password",
			message: fmt.Sprintf(
				"Password must be at least %d characters.", MinPasswordLength,
			),
		}
	}
	name := strings.TrimSpace(r.SiteName)
	if name == "" {
		return &validationError{code: "invalid_site_name", message: "Site name is required."}
	}
	if utf8.RuneCountInString(name) > 200 {
		return &validationError{code: "invalid_site_name", message: "Site name is too long."}
	}
	siteURL := strings.TrimSpace(r.SiteURL)
	if siteURL == "" {
		return &validationError{code: "invalid_site_url", message: "Site URL is required."}
	}
	parsed, uErr := url.Parse(siteURL)
	if uErr != nil || parsed.Scheme == "" || parsed.Host == "" {
		return &validationError{code: "invalid_site_url", message: "Site URL must be absolute (e.g. https://example.com)."}
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return &validationError{code: "invalid_site_url", message: "Site URL must use http or https."}
	}
	return nil
}

// looksLikeEmail does the minimal smell test: one '@', non-empty local
// part, host with at least one '.'. We deliberately do NOT regex against
// RFC 5322 — the canonical regex is famously 600 characters long and
// still wrong on edge cases. The DB column is citext NOT NULL UNIQUE; a
// pathological string that passes this check but the SQL layer rejects
// will surface as a 500, which the wizard renders as "retry".
func looksLikeEmail(s string) bool {
	at := strings.IndexByte(s, '@')
	if at <= 0 || at == len(s)-1 {
		return false
	}
	host := s[at+1:]
	if !strings.Contains(host, ".") {
		return false
	}
	// No whitespace anywhere.
	if strings.ContainsAny(s, " \t\r\n") {
		return false
	}
	return true
}

// deriveHandle picks a default users.handle from the email's local part.
// The migration's UNIQUE constraint on handle will reject collisions;
// on a fresh install there are no other rows so the local part wins.
//
// Lower-cased and stripped of '+' suffixes (gmail-style aliases) so the
// resulting handle is the bare identifier the operator would type at
// login. citext on the column collapses case anyway, but normalizing
// here keeps audit lines readable.
func deriveHandle(email string) string {
	at := strings.IndexByte(email, '@')
	if at <= 0 {
		return email
	}
	local := email[:at]
	if plus := strings.IndexByte(local, '+'); plus > 0 {
		local = local[:plus]
	}
	return strings.ToLower(local)
}

// deriveDisplayName produces a human-friendly default for users.display_name.
// We use the local part with the first letter upper-cased; operators can
// edit it from the admin UI later.
func deriveDisplayName(email string) string {
	h := deriveHandle(email)
	if h == "" {
		return "Admin"
	}
	runes := []rune(h)
	runes[0] = upperFirst(runes[0])
	return string(runes)
}

// upperFirst is strings.ToUpper for a single rune without allocating a
// string per call.
func upperFirst(r rune) rune {
	if r >= 'a' && r <= 'z' {
		return r - 'a' + 'A'
	}
	return r
}

// decodeRequest parses the JSON body. MaxBytesReader caps the body up
// front; DisallowUnknownFields catches typos and shape drift early. An
// empty body or trailing junk returns a stable error string.
func decodeRequest(r *http.Request) (installRequest, error) {
	r.Body = http.MaxBytesReader(nil, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var req installRequest
	if err := dec.Decode(&req); err != nil {
		return installRequest{}, errors.New("Request body must be valid JSON.")
	}
	var tail json.RawMessage
	if err := dec.Decode(&tail); !errors.Is(err, io.EOF) {
		return installRequest{}, errors.New("Trailing data after JSON body.")
	}
	return req, nil
}

// clientIP returns the request's connection peer. The reverse proxy
// (if any) is expected to write a trusted X-Forwarded-For and a
// middleware to rewrite RemoteAddr; we just read whatever the socket
// reports here. Matches the login handler's policy.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// setRetryAfter writes the Retry-After header with a rounded-up
// integer seconds value, matching RFC 7231 §7.1.3. Sub-second TTLs are
// rounded up to 1 to avoid `Retry-After: 0` which some clients treat
// as "no waiting required".
func setRetryAfter(w http.ResponseWriter, wait time.Duration) {
	if wait <= 0 {
		return
	}
	seconds := int(wait / time.Second)
	if time.Duration(seconds)*time.Second < wait {
		seconds++
	}
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
}

// writeJSON is the package's house response writer. Identical shape to
// the login package's helper; kept local so setup doesn't import login
// just for one function.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// writeError serializes the standard failure shape. An empty code is
// defensively rewritten to "error" so a buggy caller doesn't produce
// `{"code":""}` on the wire.
func writeError(w http.ResponseWriter, status int, code, message string) {
	if code == "" {
		code = "error"
	}
	writeJSON(w, status, errorResponse{Code: code, Message: message})
}

// Compile-time guard: Handler's methods satisfy http.HandlerFunc.
var (
	_ http.HandlerFunc = (*Handler)(nil).Status
	_ http.HandlerFunc = (*Handler)(nil).Install
)
