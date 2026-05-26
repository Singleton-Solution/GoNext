package account

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Singleton-Solution/GoNext/packages/go/policy"
	"github.com/Singleton-Solution/GoNext/packages/go/settings"
)

type fakeSettings struct {
	value any
	err   error
}

func (f *fakeSettings) Read(_ context.Context, _ string) (any, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.value, nil
}

func newHarness(t *testing.T, sr SettingsReader, prod ExportProducer) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	if err := Mount(mux, "/api/v1/account", Deps{
		Settings: sr,
		Producer: prod,
		Logger:   slog.New(slog.NewJSONHandler(io.Discard, nil)),
	}); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	return mux
}

func TestExport_Enabled_Success(t *testing.T) {
	sr := &fakeSettings{value: true}
	prod := func(_ context.Context, userID string) (map[string]any, error) {
		return map[string]any{"posts": []string{"hello"}}, nil
	}
	mux := newHarness(t, sr, prod)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/account/data/export", nil)
	req = req.WithContext(policy.WithPrincipal(req.Context(), policy.Principal{
		UserID: "u:1", Roles: []policy.Role{policy.RoleSubscriber},
	}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var env map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&env)
	if env["user_id"] != "u:1" {
		t.Fatalf("unexpected user_id: %+v", env)
	}
}

func TestExport_Disabled_Forbidden(t *testing.T) {
	sr := &fakeSettings{value: false}
	prod := func(_ context.Context, _ string) (map[string]any, error) {
		t.Fatal("producer should not run when disabled")
		return nil, nil
	}
	mux := newHarness(t, sr, prod)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/account/data/export", nil)
	req = req.WithContext(policy.WithPrincipal(req.Context(), policy.Principal{UserID: "u:1"}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestExport_Anonymous_Unauthorized(t *testing.T) {
	sr := &fakeSettings{value: true}
	prod := func(_ context.Context, _ string) (map[string]any, error) { return nil, nil }
	mux := newHarness(t, sr, prod)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/account/data/export", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestExport_ProducerError(t *testing.T) {
	sr := &fakeSettings{value: true}
	prod := func(_ context.Context, _ string) (map[string]any, error) {
		return nil, errors.New("boom")
	}
	mux := newHarness(t, sr, prod)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/account/data/export", nil)
	req = req.WithContext(policy.WithPrincipal(req.Context(), policy.Principal{UserID: "u:1"}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

// Guard that the privacy setting key is the same wire value we expect.
func TestPrivacyKeyConstant(t *testing.T) {
	if settings.PrivacyAllowGDPRSelfService != "core.privacy.allow_gdpr_self_service" {
		t.Fatalf("privacy key drift: %s", settings.PrivacyAllowGDPRSelfService)
	}
}
