package auth

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/log"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
	"github.com/Singleton-Solution/GoNext/packages/go/session"
)

// fakeStore is an in-memory SessionStore for unit tests. It exposes
// the token-to-session map directly so tests can pre-populate it and
// it lets us inject an arbitrary error to simulate transient failures.
type fakeStore struct {
	sessions map[string]session.Session
	err      error // when non-nil, Get returns this regardless of token
	calls    []fakeStoreCall
}

type fakeStoreCall struct {
	token   string
	idleTTL time.Duration
}

func newFakeStore() *fakeStore {
	return &fakeStore{sessions: make(map[string]session.Session)}
}

// Get implements SessionStore. The fake records each call so tests can
// assert on the token / idleTTL passed in; it returns the injected
// error if set, otherwise looks up the token.
func (f *fakeStore) Get(ctx context.Context, token string, idleTTL time.Duration) (session.Session, error) {
	f.calls = append(f.calls, fakeStoreCall{token: token, idleTTL: idleTTL})
	if f.err != nil {
		return session.Session{}, f.err
	}
	s, ok := f.sessions[token]
	if !ok {
		return session.Session{}, session.ErrNotFound
	}
	return s, nil
}

// fakePolicy is an in-memory Policy for the RequireCapability tests.
// allowed is the set of capabilities the principal holds; everything
// else is denied. The principal's roles are not consulted — the fake
// is intentionally orthogonal to BasicPolicy so the test asserts on
// the middleware, not on the real role-resolution logic.
type fakePolicy struct {
	allowed map[policy.Capability]struct{}
	calls   []fakePolicyCall
}

type fakePolicyCall struct {
	principal  policy.Principal
	capability policy.Capability
}

func newFakePolicy(allow ...policy.Capability) *fakePolicy {
	m := make(map[policy.Capability]struct{}, len(allow))
	for _, c := range allow {
		m[c] = struct{}{}
	}
	return &fakePolicy{allowed: m}
}

func (f *fakePolicy) Can(p policy.Principal, c policy.Capability, _ any) policy.Decision {
	f.calls = append(f.calls, fakePolicyCall{principal: p, capability: c})
	if _, ok := f.allowed[c]; ok {
		return policy.Decision{Allowed: true, Reason: "ok"}
	}
	return policy.Decision{Allowed: false, Reason: "denied"}
}

// principalRecordingHandler is the next handler used by the
// RequireSession / OptionalSession tests. It captures the principal
// off the context and the logger off the context so tests can assert
// on them after a successful invocation.
func principalRecordingHandler(captured **principalCapture) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c := &principalCapture{}
		c.principal, c.ok = policy.FromContext(r.Context())
		// Pulling the logger off the ctx and emitting one line lets the
		// test confirm log.WithRequest threaded user_id onto the
		// context. We don't assert the line text here — we use a
		// dedicated test (TestRequireSession_AttachesLogFields) for
		// that with a buffer-backed logger.
		_ = log.FromContext(r.Context())
		c.calledWith = r.Context()
		*captured = c
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}
}

type principalCapture struct {
	principal  policy.Principal
	ok         bool
	calledWith context.Context
}

// sessionWithRoles is a tiny constructor for tests: returns a
// session.Session populated with a UserID and roles already stored
// under the "roles" key the way the login handler would.
func sessionWithRoles(userID string, roles ...string) session.Session {
	roleAny := make([]any, len(roles))
	for i, r := range roles {
		roleAny[i] = r
	}
	return session.Session{
		Token:          "test-token",
		UserID:         userID,
		CreatedAt:      time.Now().UTC(),
		LastSeenAt:     time.Now().UTC(),
		AbsoluteExpiry: time.Now().UTC().Add(time.Hour),
		Data: map[string]any{
			"roles": roleAny,
		},
	}
}

// --- RequireSession ----------------------------------------------------

func TestRequireSession_MissingCookie_Returns401(t *testing.T) {
	store := newFakeStore()
	var captured *principalCapture
	mw := requireSession(store)
	h := mw(principalRecordingHandler(&captured))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", rec.Code)
	}
	assertJSONErrorBody(t, rec.Body.String(), "unauthorized")
	if captured != nil {
		t.Error("next handler was called when it should have been blocked")
	}
	if len(store.calls) != 0 {
		t.Errorf("store.Get called %d times despite missing cookie", len(store.calls))
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type: got %q, want JSON", ct)
	}
}

func TestRequireSession_EmptyCookieValue_Returns401(t *testing.T) {
	store := newFakeStore()
	var captured *principalCapture
	mw := requireSession(store)
	h := mw(principalRecordingHandler(&captured))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	// An empty-value cookie is the browser-side equivalent of "no
	// cookie" — Go's r.Cookie returns the parsed cookie but with an
	// empty Value, which the middleware must treat as missing.
	req.AddCookie(&http.Cookie{Name: DefaultCookieName, Value: ""})
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", rec.Code)
	}
	if captured != nil {
		t.Error("next handler should not have been called")
	}
	if len(store.calls) != 0 {
		t.Errorf("empty cookie should short-circuit before store.Get")
	}
}

func TestRequireSession_BadToken_Returns401(t *testing.T) {
	store := newFakeStore()
	store.err = session.ErrInvalidToken
	var captured *principalCapture
	mw := requireSession(store)
	h := mw(principalRecordingHandler(&captured))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.AddCookie(&http.Cookie{Name: DefaultCookieName, Value: "garbage"})
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", rec.Code)
	}
	assertJSONErrorBody(t, rec.Body.String(), "unauthorized")
	if captured != nil {
		t.Error("next handler should not have been called")
	}
	if len(store.calls) != 1 {
		t.Errorf("store.Get should have been called once, got %d", len(store.calls))
	}
}

func TestRequireSession_ExpiredSession_Returns401(t *testing.T) {
	store := newFakeStore()
	// session.ErrNotFound is what Manager.Get returns for both
	// "never existed" and "passed absolute TTL" — see manager.go.
	store.err = session.ErrNotFound
	var captured *principalCapture
	mw := requireSession(store)
	h := mw(principalRecordingHandler(&captured))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.AddCookie(&http.Cookie{Name: DefaultCookieName, Value: "expired-token"})
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", rec.Code)
	}
	if captured != nil {
		t.Error("next handler should not have been called")
	}
}

func TestRequireSession_TransientStoreError_Returns401(t *testing.T) {
	// A redis blip while loading the session must fail closed: a 500
	// would be more honest about the cause but would also leak the
	// fact that an unauthenticated probe got past the cookie check
	// and reached the store. 401 matches the failure mode the user
	// sees for a missing/expired cookie.
	store := newFakeStore()
	store.err = errors.New("redis: connection refused")
	var captured *principalCapture
	mw := requireSession(store)
	h := mw(principalRecordingHandler(&captured))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.AddCookie(&http.Cookie{Name: DefaultCookieName, Value: "any"})
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", rec.Code)
	}
	if captured != nil {
		t.Error("next handler should not have been called on transient error")
	}
}

func TestRequireSession_ValidSession_AttachesPrincipalAndCallsNext(t *testing.T) {
	store := newFakeStore()
	store.sessions["good-token"] = sessionWithRoles("user-42", "admin", "editor")
	var captured *principalCapture
	mw := requireSession(store)
	h := mw(principalRecordingHandler(&captured))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.AddCookie(&http.Cookie{Name: DefaultCookieName, Value: "good-token"})
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rec.Code)
	}
	if captured == nil {
		t.Fatal("next handler was not called on a valid session")
	}
	if !captured.ok {
		t.Fatal("expected principal on context")
	}
	if captured.principal.UserID != "user-42" {
		t.Errorf("UserID: got %q, want %q", captured.principal.UserID, "user-42")
	}
	wantRoles := []policy.Role{"admin", "editor"}
	if len(captured.principal.Roles) != len(wantRoles) {
		t.Fatalf("roles: got %v, want %v", captured.principal.Roles, wantRoles)
	}
	for i, r := range captured.principal.Roles {
		if r != wantRoles[i] {
			t.Errorf("Roles[%d]: got %q, want %q", i, r, wantRoles[i])
		}
	}

	// store was called with the default idle TTL.
	if len(store.calls) != 1 {
		t.Fatalf("store.Get calls: got %d, want 1", len(store.calls))
	}
	if store.calls[0].token != "good-token" {
		t.Errorf("store called with token %q, want %q", store.calls[0].token, "good-token")
	}
	if store.calls[0].idleTTL != DefaultIdleTTL {
		t.Errorf("idleTTL: got %v, want %v", store.calls[0].idleTTL, DefaultIdleTTL)
	}
}

func TestRequireSession_AttachesLogFields(t *testing.T) {
	// Replace the default logger with one that writes into a buffer
	// so we can confirm user_id is attached to the context-bound
	// logger. We capture by emitting a log line from the next handler
	// and parsing the JSON output.
	buf := &lineBuffer{}
	captureLogger := slog.New(slog.NewJSONHandler(buf, nil))

	store := newFakeStore()
	store.sessions["good-token"] = sessionWithRoles("user-42", "admin")
	mw := requireSession(store)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Pull the logger off the request context. WithRequest should
		// have made it carry a user_id="user-42" field.
		log.FromContext(r.Context()).Info("downstream")
		w.WriteHeader(http.StatusOK)
	}))

	// Seed the request context with our capturing logger so that
	// log.FromContext on the way down picks it up before WithRequest
	// derives a child.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/p", nil)
	req = req.WithContext(log.WithLogger(req.Context(), captureLogger))
	req.AddCookie(&http.Cookie{Name: DefaultCookieName, Value: "good-token"})
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	if !strings.Contains(buf.String(), `"user_id":"user-42"`) {
		t.Errorf("log output did not include user_id field: %s", buf.String())
	}
}

func TestRequireSession_CustomOptions(t *testing.T) {
	store := newFakeStore()
	store.sessions["good"] = sessionWithRoles("u-1", "editor")

	customBuilder := func(s session.Session) policy.Principal {
		// Custom builder injects an extra role.
		p := DefaultPrincipal(s)
		p.Roles = append(p.Roles, policy.Role("custom"))
		return p
	}

	var captured *principalCapture
	mw := requireSession(store,
		WithCookieName("custom_sid"),
		WithIdleTTL(15*time.Minute),
		WithPrincipalBuilder(customBuilder),
	)
	h := mw(principalRecordingHandler(&captured))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/p", nil)
	// Set the custom cookie name; the default cookie name should NOT
	// be consulted.
	req.AddCookie(&http.Cookie{Name: "custom_sid", Value: "good"})
	req.AddCookie(&http.Cookie{Name: DefaultCookieName, Value: "wrong"})
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%q", rec.Code, rec.Body.String())
	}
	if captured == nil || !captured.ok {
		t.Fatal("principal not attached")
	}
	if captured.principal.UserID != "u-1" {
		t.Errorf("UserID: got %q, want %q", captured.principal.UserID, "u-1")
	}
	// Custom builder appended "custom".
	foundCustom := false
	for _, r := range captured.principal.Roles {
		if r == "custom" {
			foundCustom = true
		}
	}
	if !foundCustom {
		t.Errorf("custom role not appended; roles=%v", captured.principal.Roles)
	}
	if len(store.calls) != 1 || store.calls[0].idleTTL != 15*time.Minute {
		t.Errorf("custom idleTTL not propagated: %+v", store.calls)
	}
	if store.calls[0].token != "good" {
		t.Errorf("custom cookie not consulted: %+v", store.calls)
	}
}

func TestWithOptions_IgnoreInvalidInputs(t *testing.T) {
	// Each option ignores its zero/invalid value to keep the default.
	// This guards against a caller passing a nil builder or negative
	// duration by mistake silently breaking the middleware.
	cfg := defaultOptions()
	WithIdleTTL(0)(&cfg)
	WithIdleTTL(-1)(&cfg)
	WithCookieName("")(&cfg)
	WithPrincipalBuilder(nil)(&cfg)
	if cfg.IdleTTL != DefaultIdleTTL {
		t.Errorf("IdleTTL was overwritten: %v", cfg.IdleTTL)
	}
	if cfg.CookieName != DefaultCookieName {
		t.Errorf("CookieName was overwritten: %q", cfg.CookieName)
	}
	if cfg.PrincipalBuilder == nil {
		t.Error("PrincipalBuilder was overwritten with nil")
	}
}

// --- OptionalSession --------------------------------------------------

func TestOptionalSession_MissingCookie_FallsThroughAnonymous(t *testing.T) {
	store := newFakeStore()
	var captured *principalCapture
	mw := optionalSession(store)
	h := mw(principalRecordingHandler(&captured))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/public", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rec.Code)
	}
	if captured == nil {
		t.Fatal("next handler should have been called")
	}
	if !captured.ok {
		t.Error("anonymous principal not attached to context")
	}
	if captured.principal.UserID != "" {
		t.Errorf("anonymous principal should have empty UserID, got %q", captured.principal.UserID)
	}
	if len(captured.principal.Roles) != 0 {
		t.Errorf("anonymous principal should have no roles, got %v", captured.principal.Roles)
	}
}

func TestOptionalSession_InvalidSession_FallsThroughAnonymous(t *testing.T) {
	store := newFakeStore()
	store.err = session.ErrInvalidToken
	var captured *principalCapture
	mw := optionalSession(store)
	h := mw(principalRecordingHandler(&captured))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/public", nil)
	req.AddCookie(&http.Cookie{Name: DefaultCookieName, Value: "bad"})
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200 (anonymous fallthrough)", rec.Code)
	}
	if captured == nil || !captured.ok {
		t.Fatal("anonymous principal not attached on invalid session")
	}
	if captured.principal.UserID != "" {
		t.Errorf("anonymous principal should have empty UserID, got %q", captured.principal.UserID)
	}
}

func TestOptionalSession_ValidSession_AttachesPrincipal(t *testing.T) {
	store := newFakeStore()
	store.sessions["good"] = sessionWithRoles("u-7", "subscriber")
	var captured *principalCapture
	mw := optionalSession(store)
	h := mw(principalRecordingHandler(&captured))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/public", nil)
	req.AddCookie(&http.Cookie{Name: DefaultCookieName, Value: "good"})
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	if captured == nil || !captured.ok {
		t.Fatal("principal not attached on valid session")
	}
	if captured.principal.UserID != "u-7" {
		t.Errorf("UserID: got %q, want %q", captured.principal.UserID, "u-7")
	}
}

// --- RequireCapability ------------------------------------------------

func TestRequireCapability_NoPrincipal_Returns401(t *testing.T) {
	pol := newFakePolicy(policy.CapEditPosts)
	called := false
	mw := RequireCapability(pol, policy.CapEditPosts)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/p", nil)
	// No principal in the context — RequireSession was not used.
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", rec.Code)
	}
	assertJSONErrorBody(t, rec.Body.String(), "unauthorized")
	if called {
		t.Error("next handler was called without a principal")
	}
	if len(pol.calls) != 0 {
		t.Errorf("policy.Can called %d times without a principal", len(pol.calls))
	}
}

func TestRequireCapability_PrincipalLacksCapability_Returns403(t *testing.T) {
	pol := newFakePolicy() // empty allow set: deny everything
	called := false
	mw := RequireCapability(pol, policy.CapPublishPosts)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/p", nil)
	req = req.WithContext(policy.WithPrincipal(req.Context(), policy.Principal{
		UserID: "u-1", Roles: []policy.Role{"subscriber"},
	}))
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status: got %d, want 403", rec.Code)
	}
	assertJSONErrorBody(t, rec.Body.String(), "forbidden")
	if called {
		t.Error("next handler should not have been called on denied capability")
	}
	if len(pol.calls) != 1 {
		t.Fatalf("policy.Can calls: got %d, want 1", len(pol.calls))
	}
	if pol.calls[0].capability != policy.CapPublishPosts {
		t.Errorf("policy called with wrong capability: %v", pol.calls[0].capability)
	}
	if pol.calls[0].principal.UserID != "u-1" {
		t.Errorf("policy called with wrong principal: %+v", pol.calls[0].principal)
	}
}

func TestRequireCapability_PrincipalHasCapability_CallsNext(t *testing.T) {
	pol := newFakePolicy(policy.CapEditPosts)
	called := false
	mw := RequireCapability(pol, policy.CapEditPosts)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("done"))
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/p", nil)
	req = req.WithContext(policy.WithPrincipal(req.Context(), policy.Principal{
		UserID: "u-2", Roles: []policy.Role{"editor"},
	}))
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rec.Code)
	}
	if !called {
		t.Error("next handler was not called on allowed capability")
	}
	if rec.Body.String() != "done" {
		t.Errorf("body: got %q, want %q", rec.Body.String(), "done")
	}
}

func TestRequireCapability_EndToEndWithRequireSession(t *testing.T) {
	// Full pipeline: RequireSession loads the principal, then
	// RequireCapability checks it. This is the real wiring shape.
	store := newFakeStore()
	store.sessions["good"] = sessionWithRoles("u-99", "editor")
	pol := newFakePolicy(policy.CapEditPosts)

	called := false
	chain := requireSession(store)(RequireCapability(pol, policy.CapEditPosts)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		// Confirm the principal is still readable downstream.
		p, _ := policy.FromContext(r.Context())
		if p.UserID != "u-99" {
			t.Errorf("downstream principal UserID: got %q, want %q", p.UserID, "u-99")
		}
		w.WriteHeader(http.StatusOK)
	})))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/edit", nil)
	req.AddCookie(&http.Cookie{Name: DefaultCookieName, Value: "good"})
	chain.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200; body=%q", rec.Code, rec.Body.String())
	}
	if !called {
		t.Error("inner handler not called")
	}
	if len(pol.calls) != 1 {
		t.Fatalf("policy called %d times, want 1", len(pol.calls))
	}
	if pol.calls[0].principal.UserID != "u-99" {
		t.Errorf("policy saw wrong principal: %+v", pol.calls[0].principal)
	}
}

// --- PrincipalFromRequest, AnonymousPrincipal -------------------------

func TestPrincipalFromRequest_WithPrincipal(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(policy.WithPrincipal(req.Context(), policy.Principal{
		UserID: "u-1", Roles: []policy.Role{"admin"},
	}))
	p := PrincipalFromRequest(req)
	if p.UserID != "u-1" {
		t.Errorf("got UserID=%q, want %q", p.UserID, "u-1")
	}
}

func TestPrincipalFromRequest_NoPrincipal(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	p := PrincipalFromRequest(req)
	if p.UserID != "" {
		t.Errorf("expected zero principal, got %+v", p)
	}
	if len(p.Roles) != 0 {
		t.Errorf("expected no roles, got %v", p.Roles)
	}
}

func TestPrincipalFromRequest_NilRequest(t *testing.T) {
	p := PrincipalFromRequest(nil)
	if p.UserID != "" || len(p.Roles) != 0 {
		t.Errorf("expected zero principal for nil request, got %+v", p)
	}
}

func TestAnonymousPrincipal_IsZero(t *testing.T) {
	p := AnonymousPrincipal()
	if p.UserID != "" {
		t.Errorf("AnonymousPrincipal has UserID %q", p.UserID)
	}
	if len(p.Roles) != 0 {
		t.Errorf("AnonymousPrincipal has roles %v", p.Roles)
	}
}

// --- DefaultPrincipal / rolesFromData ---------------------------------

func TestDefaultPrincipal_AllShapes(t *testing.T) {
	cases := []struct {
		name string
		data map[string]any
		want []policy.Role
	}{
		{"nil data", nil, nil},
		{"empty data", map[string]any{}, nil},
		{"no roles key", map[string]any{"factors": []any{"password"}}, nil},
		{"roles as []any of strings",
			map[string]any{"roles": []any{"admin", "editor"}},
			[]policy.Role{"admin", "editor"},
		},
		{"roles as []string",
			map[string]any{"roles": []string{"author"}},
			[]policy.Role{"author"},
		},
		{"roles as []policy.Role",
			map[string]any{"roles": []policy.Role{"subscriber"}},
			[]policy.Role{"subscriber"},
		},
		{"roles with non-string entries skipped",
			map[string]any{"roles": []any{"admin", 42, nil, "editor"}},
			[]policy.Role{"admin", "editor"},
		},
		{"roles with empty strings skipped",
			map[string]any{"roles": []any{"", "admin", ""}},
			[]policy.Role{"admin"},
		},
		{"roles with empty []string entries skipped",
			map[string]any{"roles": []string{"", "editor"}},
			[]policy.Role{"editor"},
		},
		{"roles wrong type (string)",
			map[string]any{"roles": "admin"},
			nil,
		},
		{"roles wrong type (map)",
			map[string]any{"roles": map[string]any{"name": "admin"}},
			nil,
		},
		{"roles empty []any",
			map[string]any{"roles": []any{}},
			nil,
		},
		{"roles []any of only non-strings",
			map[string]any{"roles": []any{1, 2, 3}},
			nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sess := session.Session{UserID: "u", Data: tc.data}
			p := DefaultPrincipal(sess)
			if p.UserID != "u" {
				t.Errorf("UserID: got %q, want %q", p.UserID, "u")
			}
			if len(p.Roles) != len(tc.want) {
				t.Fatalf("Roles len: got %d, want %d (%v vs %v)",
					len(p.Roles), len(tc.want), p.Roles, tc.want)
			}
			for i, r := range p.Roles {
				if r != tc.want[i] {
					t.Errorf("Roles[%d]: got %q, want %q", i, r, tc.want[i])
				}
			}
		})
	}
}

func TestDefaultPrincipal_TypedRolesAreCopied(t *testing.T) {
	// rolesFromData copies []policy.Role so a caller can't mutate the
	// session blob through the returned principal.
	orig := []policy.Role{"admin", "editor"}
	data := map[string]any{"roles": orig}
	p := DefaultPrincipal(session.Session{UserID: "u", Data: data})
	if len(p.Roles) != 2 {
		t.Fatalf("expected 2 roles, got %d", len(p.Roles))
	}
	p.Roles[0] = "tampered"
	if orig[0] != "admin" {
		t.Errorf("mutating principal.Roles leaked into the session data: orig=%v", orig)
	}
}

// --- writeJSONError ---------------------------------------------------

func TestWriteJSONError_ShapeAndHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSONError(rec, http.StatusUnauthorized, "unauthorized")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("code: got %d, want 401", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type: got %q, want JSON", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control: got %q, want no-store", cc)
	}
	assertJSONErrorBody(t, rec.Body.String(), "unauthorized")
}

// --- helpers ---------------------------------------------------------

// assertJSONErrorBody checks that body is exactly `{"error": <msg>}` —
// shape AND content. Tolerates the trailing newline json.Encoder adds.
func assertJSONErrorBody(t *testing.T, body, want string) {
	t.Helper()
	body = strings.TrimSpace(body)
	var parsed struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("body %q is not JSON: %v", body, err)
	}
	if parsed.Error != want {
		t.Errorf("body error field: got %q, want %q", parsed.Error, want)
	}
}

// lineBuffer is a thread-unsafe in-memory writer for capturing log
// output. Tests are single-goroutine so the lack of locking is fine.
type lineBuffer struct {
	buf []byte
}

func (b *lineBuffer) Write(p []byte) (int, error) {
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (b *lineBuffer) String() string { return string(b.buf) }
