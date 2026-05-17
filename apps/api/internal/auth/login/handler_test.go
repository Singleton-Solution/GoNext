package login

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	pquerna "github.com/pquerna/otp/totp"

	"github.com/Singleton-Solution/GoNext/packages/go/auth/totp"
	"github.com/Singleton-Solution/GoNext/packages/go/ratelimit"
	"github.com/Singleton-Solution/GoNext/packages/go/session"
)

// newTestHandler wires a handler against the shared fixture so tests
// can drive HTTP directly. Returns the handler plus the fixture so
// assertions can inspect emitted audit events, session creates, etc.
func newTestHandler(t *testing.T) (*Handler, *testFixture) {
	t.Helper()
	f := newFixture(t)
	svc, err := NewService(f.deps)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	h, err := NewHandler(svc, f.deps)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	return h, f
}

// doRequest builds a POST /api/v1/auth/login request, body marshaled
// from v, and runs it through h. Returns the recorder.
func doRequest(t *testing.T, h http.Handler, v any) *httptest.ResponseRecorder {
	t.Helper()
	var bodyBytes []byte
	switch x := v.(type) {
	case string:
		bodyBytes = []byte(x)
	case []byte:
		bodyBytes = x
	default:
		var err error
		bodyBytes, err = json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "10.0.0.1:5432"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestHandler_HappyPath(t *testing.T) {
	h, f := newTestHandler(t)
	f.addUser(t, "u-1", "alice@example.com", "pwd", "active")

	rr := doRequest(t, h, requestBody{
		Email:    "alice@example.com",
		Password: "pwd",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rr.Code, rr.Body)
	}
	var body successResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.UserID != "u-1" {
		t.Errorf("user_id: got %q, want u-1", body.UserID)
	}
	if body.ExpiresAt.IsZero() {
		t.Error("expires_at: zero")
	}
	// Set-Cookie must carry the session cookie. We use Insecure=false
	// so the Secure attribute lands.
	cookies := rr.Result().Cookies()
	var found bool
	for _, c := range cookies {
		if c.Name == session.CookieName {
			found = true
			if c.Value == "" {
				t.Error("cookie value empty")
			}
			if !c.HttpOnly {
				t.Error("cookie not HttpOnly")
			}
			if !c.Secure {
				t.Error("cookie not Secure")
			}
		}
	}
	if !found {
		t.Errorf("session cookie missing; got cookies: %v", cookies)
	}
	// And NEVER expose the token in the JSON body.
	if strings.Contains(rr.Body.String(), "fake-token-") {
		t.Error("token leaked into response body")
	}
}

func TestHandler_WrongPasswordReturns401(t *testing.T) {
	h, f := newTestHandler(t)
	f.addUser(t, "u-1", "alice@example.com", "pwd", "active")

	rr := doRequest(t, h, requestBody{Email: "alice@example.com", Password: "wrong"})
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want 401; body=%s", rr.Code, rr.Body)
	}
	var body errorResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if body.Error != "invalid_credentials" {
		t.Errorf("error: got %q, want invalid_credentials", body.Error)
	}
	// And no session cookie.
	for _, c := range rr.Result().Cookies() {
		if c.Name == session.CookieName {
			t.Errorf("session cookie set on failure: %v", c)
		}
	}
	// Audit emitted failed event.
	if got, want := f.countEvents(t, "auth.login.failed"), 1; got != want {
		t.Errorf("failed events: got %d, want %d", got, want)
	}
}

func TestHandler_UnknownEmailReturns401NotFour04(t *testing.T) {
	h, _ := newTestHandler(t)
	rr := doRequest(t, h, requestBody{Email: "nobody@example.com", Password: "x"})
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want 401 (not 404 — would enumerate)", rr.Code)
	}
	var body errorResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if body.Error != "invalid_credentials" {
		t.Errorf("error: got %q, want invalid_credentials", body.Error)
	}
}

func TestHandler_MalformedJSONReturns400(t *testing.T) {
	h, _ := newTestHandler(t)

	rr := doRequest(t, h, `{"email": "alice@example.com", "password"`) // broken
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rr.Code, rr.Body)
	}
	var body errorResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if body.Error != "invalid_request" {
		t.Errorf("error: got %q, want invalid_request", body.Error)
	}
}

func TestHandler_UnknownFieldRejected(t *testing.T) {
	h, _ := newTestHandler(t)
	rr := doRequest(t, h, `{"email":"a@b.com","password":"p","secret_admin":"true"}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rr.Code, rr.Body)
	}
}

func TestHandler_TrailingGarbageRejected(t *testing.T) {
	h, _ := newTestHandler(t)
	rr := doRequest(t, h, `{"email":"a@b.com","password":"p"}{"extra":1}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400 (trailing JSON); body=%s", rr.Code, rr.Body)
	}
}

func TestHandler_LockedReturns423WithRetryAfter(t *testing.T) {
	h, f := newTestHandler(t)
	f.addUser(t, "u-1", "alice@example.com", "pwd", "active")
	// Drive into lockout (threshold = 3 in the fixture).
	for i := 0; i < 3; i++ {
		_, _ = f.limiter.RecordFailure(context.Background(), "u-1")
	}
	rr := doRequest(t, h, requestBody{Email: "alice@example.com", Password: "pwd"})
	if rr.Code != http.StatusLocked {
		t.Fatalf("status: got %d, want 423; body=%s", rr.Code, rr.Body)
	}
	ra := rr.Header().Get("Retry-After")
	if ra == "" {
		t.Fatal("Retry-After header missing")
	}
	if n, err := strconv.Atoi(ra); err != nil || n <= 0 {
		t.Errorf("Retry-After: got %q, want positive integer", ra)
	}
	var body errorResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if body.Error != "account_locked" {
		t.Errorf("error: got %q, want account_locked", body.Error)
	}
}

func TestHandler_TOTPRequiredFlow(t *testing.T) {
	h, f := newTestHandler(t)
	f.addUser(t, "u-1", "alice@example.com", "pwd", "active")
	sec, err := totp.Generate("GoNext", "alice@example.com")
	if err != nil {
		t.Fatalf("totp.Generate: %v", err)
	}
	f.addTOTP(t, "u-1", sec.Base32)

	// 1st call: password OK, expect intermediate token + requires=[totp].
	rr := doRequest(t, h, requestBody{Email: "alice@example.com", Password: "pwd"})
	if rr.Code != http.StatusOK {
		t.Fatalf("first status: got %d, want 200; body=%s", rr.Code, rr.Body)
	}
	for _, c := range rr.Result().Cookies() {
		if c.Name == session.CookieName {
			t.Errorf("session cookie set on first call (should wait for 2FA)")
		}
	}
	var first totpRequiredResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &first); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if first.IntermediateToken == "" {
		t.Fatal("intermediate_token: empty")
	}
	if len(first.Requires) != 1 || first.Requires[0] != "totp" {
		t.Errorf("requires: got %v, want [totp]", first.Requires)
	}

	// 2nd call: send TOTP + intermediate token.
	code, err := pquerna.GenerateCode(sec.Base32, time.Now().UTC())
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}
	rr2 := doRequest(t, h, requestBody{
		TOTPCode:          code,
		IntermediateToken: first.IntermediateToken,
	})
	if rr2.Code != http.StatusOK {
		t.Fatalf("second status: got %d, want 200; body=%s", rr2.Code, rr2.Body)
	}
	var final successResponse
	if err := json.Unmarshal(rr2.Body.Bytes(), &final); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if final.UserID != "u-1" {
		t.Errorf("user_id: got %q, want u-1", final.UserID)
	}
	// Session cookie set on second call.
	var found bool
	for _, c := range rr2.Result().Cookies() {
		if c.Name == session.CookieName {
			found = true
		}
	}
	if !found {
		t.Errorf("session cookie missing on 2FA finalize")
	}
}

func TestHandler_TOTPWrongCodeReturns401(t *testing.T) {
	h, f := newTestHandler(t)
	f.addUser(t, "u-1", "alice@example.com", "pwd", "active")
	sec, err := totp.Generate("GoNext", "alice@example.com")
	if err != nil {
		t.Fatalf("totp.Generate: %v", err)
	}
	f.addTOTP(t, "u-1", sec.Base32)

	rr := doRequest(t, h, requestBody{Email: "alice@example.com", Password: "pwd"})
	var first totpRequiredResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &first)
	if first.IntermediateToken == "" {
		t.Fatal("no intermediate token")
	}

	rr2 := doRequest(t, h, requestBody{
		TOTPCode:          "000000",
		IntermediateToken: first.IntermediateToken,
	})
	if rr2.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want 401; body=%s", rr2.Code, rr2.Body)
	}
	var body errorResponse
	_ = json.Unmarshal(rr2.Body.Bytes(), &body)
	if body.Error != "invalid_credentials" {
		t.Errorf("error: got %q, want invalid_credentials", body.Error)
	}
}

func TestHandler_MethodNotAllowed(t *testing.T) {
	h, _ := newTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/login", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status: got %d, want 405", rr.Code)
	}
	if rr.Header().Get("Allow") != http.MethodPost {
		t.Errorf("Allow header: got %q, want POST", rr.Header().Get("Allow"))
	}
}

func TestHandler_MountInstallsRoute(t *testing.T) {
	mux := http.NewServeMux()
	f := newFixture(t)
	f.addUser(t, "u-1", "alice@example.com", "pwd", "active")
	if err := Mount(mux, f.deps); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	rr := doRequest(t, mux, requestBody{Email: "alice@example.com", Password: "pwd"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rr.Code, rr.Body)
	}
}

func TestHandler_MountRejectsBadDeps(t *testing.T) {
	mux := http.NewServeMux()
	if err := Mount(mux, Deps{}); err == nil {
		t.Fatal("expected error from empty Deps")
	}
}

func TestHandler_BodyTooLarge(t *testing.T) {
	h, _ := newTestHandler(t)
	big := strings.Repeat("a", maxBodyBytes+1)
	body := []byte(`{"email":"a@b.com","password":"` + big + `"}`)
	rr := doRequest(t, h, body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400 (body too large); body=%s", rr.Code, rr.Body)
	}
}

func TestHandler_RateLimitedReturns429(t *testing.T) {
	// Build a fixture with a tight IP bucket (capacity 1, slow refill)
	// so the second call within a test always lands on an empty
	// bucket and returns 429.
	f := newFixture(t)
	ipLim, err := ratelimit.NewMemoryLimiter(ratelimit.Policy{Capacity: 1, RefillRate: 0.01})
	if err != nil {
		t.Fatalf("NewMemoryLimiter: %v", err)
	}
	emailLim, err := ratelimit.NewMemoryLimiter(ratelimit.Policy{Capacity: 100, RefillRate: 100})
	if err != nil {
		t.Fatalf("NewMemoryLimiter: %v", err)
	}
	tight, err := ratelimit.NewLoginAttemptLimiter(ratelimit.LoginAttemptOptions{
		IPLimiter:    ipLim,
		EmailLimiter: emailLim,
		FailureStore: ratelimit.NewMemoryFailureStore(),
	})
	if err != nil {
		t.Fatalf("NewLoginAttemptLimiter: %v", err)
	}
	f.deps.Limiter = tight
	f.addUser(t, "u-1", "alice@example.com", "pwd", "active")
	svc, err := NewService(f.deps)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	h, err := NewHandler(svc, f.deps)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	// Burn the burst (capacity=1).
	_ = doRequest(t, h, requestBody{Email: "alice@example.com", Password: "pwd"})
	rr := doRequest(t, h, requestBody{Email: "alice@example.com", Password: "pwd"})
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("status: got %d, want 429; body=%s", rr.Code, rr.Body)
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Error("Retry-After header missing on 429")
	}
}

// TestHandler_IntermediateTokenIsSingleUse covers the replay-resist
// path: a successful 2FA finalize must invalidate the intermediate
// token so a leaked one can't be reused.
func TestHandler_IntermediateTokenIsSingleUse(t *testing.T) {
	h, f := newTestHandler(t)
	f.addUser(t, "u-1", "alice@example.com", "pwd", "active")
	sec, err := totp.Generate("GoNext", "alice@example.com")
	if err != nil {
		t.Fatalf("totp.Generate: %v", err)
	}
	f.addTOTP(t, "u-1", sec.Base32)

	rr := doRequest(t, h, requestBody{Email: "alice@example.com", Password: "pwd"})
	var first totpRequiredResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &first)

	code, err := pquerna.GenerateCode(sec.Base32, time.Now().UTC())
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}
	rr2 := doRequest(t, h, requestBody{TOTPCode: code, IntermediateToken: first.IntermediateToken})
	if rr2.Code != http.StatusOK {
		t.Fatalf("first finalize: %d %s", rr2.Code, rr2.Body)
	}

	// Replay the SAME intermediate token: must fail.
	rr3 := doRequest(t, h, requestBody{TOTPCode: code, IntermediateToken: first.IntermediateToken})
	if rr3.Code != http.StatusUnauthorized {
		t.Fatalf("replay: got %d, want 401; body=%s", rr3.Code, rr3.Body)
	}
}

// TestHandler_InternalErrorBubblesAs500 simulates a session-creator
// failure and verifies the handler returns 500 with the generic
// internal_error body — never the raw error.
func TestHandler_InternalErrorBubblesAs500(t *testing.T) {
	h, f := newTestHandler(t)
	f.addUser(t, "u-1", "alice@example.com", "pwd", "active")
	f.sessionCreator.err = errors.New("redis-is-down: connection refused")

	rr := doRequest(t, h, requestBody{Email: "alice@example.com", Password: "pwd"})
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d, want 500; body=%s", rr.Code, rr.Body)
	}
	if strings.Contains(rr.Body.String(), "redis-is-down") {
		t.Error("internal error message leaked to wire")
	}
	if !strings.Contains(rr.Body.String(), "internal_error") {
		t.Errorf("body: got %q, want internal_error", rr.Body.String())
	}
}
