package login

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/session"
)

// Path is the canonical route the handler mounts at. Exported so a
// caller assembling the OpenAPI doc or test mux can reference it
// without re-spelling the constant.
const Path = "POST /api/v1/auth/login"

// maxBodyBytes caps the request body. Login payloads are tiny (four
// short strings); anything larger is either a misconfigured client
// or an attack trying to wedge the JSON parser.
const maxBodyBytes = 4 * 1024

// Mount wires the login handler onto mux. Returns an error if Deps is
// incomplete — main.go's wiring code should propagate that to a
// process exit so a misconfigured deployment never starts.
//
// The function is the only public entry point in this package; the
// service and types are exported only so tests across the api module
// can construct them directly.
func Mount(mux *http.ServeMux, d Deps) error {
	svc, err := NewService(d)
	if err != nil {
		return err
	}
	h, err := NewHandler(svc, d)
	if err != nil {
		return err
	}
	mux.Handle(Path, h)
	return nil
}

// Handler is the http.Handler for POST /api/v1/auth/login. It is a
// thin wrapper over Service.Authenticate that handles request
// decoding, cookie setting, and error → status mapping.
type Handler struct {
	svc *Service
	d   Deps
}

// NewHandler builds a Handler that calls into the supplied Service.
// Exported so tests can stitch a custom service in place; production
// callers should use Mount which constructs both.
func NewHandler(svc *Service, d Deps) (*Handler, error) {
	if svc == nil {
		return nil, errors.New("login.NewHandler: svc is required")
	}
	if err := d.validate(); err != nil {
		return nil, err
	}
	d.defaults()
	return &Handler{svc: svc, d: d}, nil
}

// requestBody is the JSON shape accepted on the wire. Field names
// match the issue spec verbatim — totp_code and recovery_code are
// snake_case for consistency with the rest of the API.
type requestBody struct {
	Email             string `json:"email"`
	Password          string `json:"password"`
	TOTPCode          string `json:"totp_code,omitempty"`
	RecoveryCode      string `json:"recovery_code,omitempty"`
	IntermediateToken string `json:"intermediate_token,omitempty"`
}

// successResponse is the JSON returned on a fully-authenticated login.
// We deliberately do NOT include the session token here — the token
// is delivered exclusively through the Set-Cookie header. Echoing it
// in the body would defeat the HttpOnly attribute (any XHR-capable
// JS could read it from the response body).
type successResponse struct {
	UserID    string    `json:"user_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

// totpRequiredResponse is the JSON returned when the password matched
// but 2FA is required. The client echoes IntermediateToken on the
// follow-up request.
type totpRequiredResponse struct {
	IntermediateToken string   `json:"intermediate_token"`
	Requires          []string `json:"requires"`
}

// errorResponse is the JSON shape for the failure branches. We use a
// machine-readable error code rather than a human message — clients
// localize their own copy.
type errorResponse struct {
	Error string `json:"error"`
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// http.ServeMux's "POST /path" pattern already enforces method,
	// but we double-check for the case where this handler is mounted
	// without method matching (chi, gorilla, etc.).
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}

	body, err := decodeRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}

	in := Input{
		Email:             body.Email,
		Password:          body.Password,
		TOTPCode:          body.TOTPCode,
		RecoveryCode:      body.RecoveryCode,
		IntermediateToken: body.IntermediateToken,
		IP:                clientIP(r),
		UserAgent:         r.UserAgent(),
	}

	res, err := h.svc.Authenticate(r.Context(), in)
	if err != nil {
		h.writeAuthError(w, r, err)
		return
	}

	if res.RequiresTOTP {
		writeJSON(w, http.StatusOK, totpRequiredResponse{
			IntermediateToken: res.IntermediateToken,
			Requires:          []string{"totp"},
		})
		return
	}

	session.SetCookie(w, res.Token, session.CookieOptions{
		Name:     h.d.CookieName,
		Domain:   h.d.CookieDomain,
		MaxAge:   h.d.SessionAbsoluteTTL,
		Insecure: h.d.Insecure,
	})
	writeJSON(w, http.StatusOK, successResponse{
		UserID:    res.UserID,
		ExpiresAt: res.ExpiresAt,
	})
}

// writeAuthError maps a service error to the right HTTP status +
// error code. The mapping is the AC for issue #124:
//
//	ErrInvalidCredentials        → 401 invalid_credentials
//	ErrTOTPInvalid               → 401 invalid_credentials
//	ErrIntermediateExpired       → 401 invalid_credentials
//	ErrLocked                    → 423 account_locked + Retry-After
//	ErrRateLimited               → 429 rate_limited     + Retry-After
//	anything else (internal)     → 500 internal_error
func (h *Handler) writeAuthError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrInvalidCredentials),
		errors.Is(err, ErrTOTPInvalid),
		errors.Is(err, ErrIntermediateExpired):
		writeError(w, http.StatusUnauthorized, "invalid_credentials")
		return
	case errors.Is(err, ErrLocked):
		setRetryAfter(w, retryAfter(err))
		writeError(w, http.StatusLocked, "account_locked")
		return
	case errors.Is(err, ErrRateLimited):
		setRetryAfter(w, retryAfter(err))
		writeError(w, http.StatusTooManyRequests, "rate_limited")
		return
	default:
		// Genuine internal error — log it (with the request ID if
		// available) and return a generic 500. The error message is
		// NEVER echoed to the wire because it may contain table
		// names, query fragments, or other ops-only context.
		h.d.Log.WarnContext(r.Context(), "login: internal error",
			slog.String("err", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal_error")
	}
}

// decodeRequest reads + parses the JSON body. We limit the byte count
// up front, disallow unknown fields (catches typos and shape drift),
// and reject an empty body cleanly.
func decodeRequest(r *http.Request) (requestBody, error) {
	r.Body = http.MaxBytesReader(nil, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var body requestBody
	if err := dec.Decode(&body); err != nil {
		return requestBody{}, err
	}
	// Reject trailing junk after the first JSON value — DisallowUnknownFields
	// catches extra keys inside the object but not bytes after the close
	// brace. A second decode that doesn't return EOF means the request
	// carried more than one JSON document.
	var tail json.RawMessage
	if err := dec.Decode(&tail); !errors.Is(err, io.EOF) {
		return requestBody{}, errors.New("login: trailing data after JSON body")
	}
	return body, nil
}

// clientIP picks the request's client IP for rate-limit + audit
// purposes. We prefer X-Forwarded-For only if the immediate peer is
// loopback — running behind a fully trusted proxy CIDR list is the
// audit package's concern, not ours. For the login handler the safe
// default is "whatever the socket says"; operators who terminate TLS
// at a reverse proxy will already have an X-Forwarded-For middleware
// installed in front of us.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// setRetryAfter writes the Retry-After header with a rounded-up
// integer seconds value. Sub-second TTLs are rounded up to 1 to avoid
// the unhelpful "Retry-After: 0" that some clients interpret as
// "no waiting required".
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

// writeJSON is the package's house response writer. Identical shape
// to the healthz package's helper — we keep our own copy because
// importing the healthz internal package would invert the dependency
// direction (auth → healthz makes no architectural sense).
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// writeError is writeJSON specialized for the failure shape, with a
// little defensiveness against the rare callers that pass an empty
// code by accident (which we'd then render as `{"error":""}`).
func writeError(w http.ResponseWriter, status int, code string) {
	if code == "" {
		code = "error"
	}
	writeJSON(w, status, errorResponse{Error: code})
}

// Compile-time guard: NewHandler must implement http.Handler. The
// type system gives us this for free via the method receiver, but a
// dummy assignment makes the intent explicit and catches accidental
// receiver-by-value mistakes during refactors.
var _ http.Handler = (*Handler)(nil)
