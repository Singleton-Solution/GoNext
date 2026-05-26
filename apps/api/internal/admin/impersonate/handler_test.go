package impersonate

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Singleton-Solution/GoNext/packages/go/audit"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
	"github.com/Singleton-Solution/GoNext/packages/go/session"
)

// fakeSessions records what was minted and returns a stable token.
type fakeSessions struct {
	lastUserID string
	lastData   map[string]any
}

func (f *fakeSessions) Create(_ context.Context, userID string, data map[string]any, _, _ time.Duration) (string, error) {
	f.lastUserID = userID
	f.lastData = data
	return "minted-token", nil
}

func newHarness(t *testing.T, lookup UserLookup) (*http.ServeMux, *fakeSessions, *audit.MemoryStore) {
	t.Helper()
	store := audit.NewMemoryStore()
	emitter := audit.NewEmitter(store)
	sessions := &fakeSessions{}
	pol := policy.NewBasicPolicy(policy.DefaultRoleCapabilities())
	mux := http.NewServeMux()
	if err := Mount(mux, "/api/v1/admin/users", Deps{
		Sessions:   sessions,
		Policy:     pol,
		Audit:      emitter,
		Logger:     slog.New(slog.NewJSONHandler(io.Discard, nil)),
		UserLookup: lookup,
	}); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	return mux, sessions, store
}

func TestImpersonate_SuperAdminSuccess(t *testing.T) {
	target := uuid.New()
	mux, fs, audStore := newHarness(t, func(_ context.Context, id uuid.UUID) (bool, error) {
		return id == target, nil
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/users/"+target.String()+"/impersonate", nil)
	req.AddCookie(&http.Cookie{Name: "sid", Value: "original-token"})
	req = req.WithContext(policy.WithPrincipal(req.Context(), policy.Principal{
		UserID: "actor:1", Roles: []policy.Role{policy.RoleSuperAdmin},
	}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if fs.lastUserID != target.String() {
		t.Fatalf("expected target=%s, got %s", target.String(), fs.lastUserID)
	}
	if fs.lastData["impersonation"] != true {
		t.Fatalf("expected impersonation flag in session data: %+v", fs.lastData)
	}
	if fs.lastData["original_token"] != "original-token" {
		t.Fatalf("expected original_token recorded: %+v", fs.lastData)
	}
	// Audit event should be present with target=user/<id>.
	evs, _ := audStore.List(context.Background(), audit.Filter{})
	if len(evs) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(evs))
	}
	if evs[0].EventType != EventImpersonationStarted {
		t.Fatalf("event type %q", evs[0].EventType)
	}
}

func TestImpersonate_AdminDenied(t *testing.T) {
	target := uuid.New()
	mux, _, _ := newHarness(t, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/users/"+target.String()+"/impersonate", nil)
	req = req.WithContext(policy.WithPrincipal(req.Context(), policy.Principal{
		UserID: "u:1", Roles: []policy.Role{policy.RoleAdmin},
	}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestImpersonate_AnonymousDenied(t *testing.T) {
	mux, _, _ := newHarness(t, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/users/"+uuid.New().String()+"/impersonate", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestImpersonate_TargetNotFound(t *testing.T) {
	mux, _, _ := newHarness(t, func(_ context.Context, _ uuid.UUID) (bool, error) { return false, nil })
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/users/"+uuid.New().String()+"/impersonate", nil)
	req = req.WithContext(policy.WithPrincipal(req.Context(), policy.Principal{
		UserID: "actor:1", Roles: []policy.Role{policy.RoleSuperAdmin},
	}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

// fakeReader records lookups and returns a canned session.
type fakeReader struct {
	sess session.Session
	err  error
}

func (f *fakeReader) Get(_ context.Context, _ string, _ time.Duration) (session.Session, error) {
	return f.sess, f.err
}

type fakeDeleter struct {
	deleted string
}

func (f *fakeDeleter) Delete(_ context.Context, token string) error {
	f.deleted = token
	return nil
}

func TestBanner_Whoami_NotImpersonating(t *testing.T) {
	reader := &fakeReader{sess: session.Session{UserID: "u:1", Data: map[string]any{}}}
	deleter := &fakeDeleter{}
	mux := http.NewServeMux()
	if err := MountBanner(mux, "/api/v1/auth/impersonation", Deps{
		Reader:  reader,
		Deleter: deleter,
		Policy:  policy.NewBasicPolicy(policy.DefaultRoleCapabilities()),
		Audit:   audit.NewEmitter(audit.NewMemoryStore()),
		Logger:  slog.New(slog.NewJSONHandler(io.Discard, nil)),
	}); err != nil {
		t.Fatalf("MountBanner: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/impersonation", nil)
	req.AddCookie(&http.Cookie{Name: "sid", Value: "tok"})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	var body map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&body)
	if body["impersonation"] != false {
		t.Fatalf("expected impersonation=false, got %+v", body)
	}
}

func TestBanner_Whoami_Impersonating(t *testing.T) {
	reader := &fakeReader{sess: session.Session{
		UserID: "target:1",
		Data: map[string]any{
			"impersonation":  true,
			"actor_user_id":  "actor:1",
			"original_token": "orig-token",
		},
	}}
	deleter := &fakeDeleter{}
	mux := http.NewServeMux()
	_ = MountBanner(mux, "/api/v1/auth/impersonation", Deps{
		Reader: reader, Deleter: deleter,
		Policy: policy.NewBasicPolicy(policy.DefaultRoleCapabilities()),
		Audit:  audit.NewEmitter(audit.NewMemoryStore()),
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/impersonation", nil)
	req.AddCookie(&http.Cookie{Name: "sid", Value: "tok"})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var body map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&body)
	if body["impersonation"] != true {
		t.Fatalf("expected impersonation=true, got %+v", body)
	}
	if body["actor_user_id"] != "actor:1" || body["target_user_id"] != "target:1" {
		t.Fatalf("unexpected body: %+v", body)
	}
}

func TestBanner_Exit_TearsDownAndRestoresCookie(t *testing.T) {
	reader := &fakeReader{sess: session.Session{
		UserID: "target:1",
		Data: map[string]any{
			"impersonation":  true,
			"original_token": "orig-token",
		},
	}}
	deleter := &fakeDeleter{}
	mux := http.NewServeMux()
	_ = MountBanner(mux, "/api/v1/auth/impersonation", Deps{
		Reader: reader, Deleter: deleter,
		Policy: policy.NewBasicPolicy(policy.DefaultRoleCapabilities()),
		Audit:  audit.NewEmitter(audit.NewMemoryStore()),
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/auth/impersonation", nil)
	req.AddCookie(&http.Cookie{Name: "sid", Value: "imp-token"})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if deleter.deleted != "imp-token" {
		t.Fatalf("expected impersonated token deleted, got %q", deleter.deleted)
	}
	cookies := rec.Result().Cookies()
	found := false
	for _, c := range cookies {
		if c.Value == "orig-token" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected original cookie restored: %+v", cookies)
	}
}

func TestImpersonate_CookieIsSet(t *testing.T) {
	target := uuid.New()
	mux, _, _ := newHarness(t, func(_ context.Context, id uuid.UUID) (bool, error) {
		return id == target, nil
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/users/"+target.String()+"/impersonate", nil)
	req = req.WithContext(policy.WithPrincipal(req.Context(), policy.Principal{
		UserID: "actor:1", Roles: []policy.Role{policy.RoleSuperAdmin},
	}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	res := rec.Result()
	defer res.Body.Close()
	found := false
	for _, c := range res.Cookies() {
		if c.Value == "minted-token" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected impersonated cookie in response: %v", res.Cookies())
	}

	var body map[string]any
	_ = json.NewDecoder(res.Body).Decode(&body)
	if body["impersonated_user_id"] != target.String() {
		t.Fatalf("unexpected body: %+v", body)
	}
	_ = io.Discard
}
