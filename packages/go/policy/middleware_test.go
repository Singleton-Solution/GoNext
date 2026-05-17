package policy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRequire is the table-driven middleware test. It covers:
//
//   - 401 when no Principal is on the context (unauthenticated).
//   - 403 when the Principal lacks the capability.
//   - 200 (handler reached) when the Principal has the capability.
//   - 403 when the Principal has roles but none grants the cap.
//   - 403 when the Principal has the empty roles slice.
//
// "handler reached" is checked via a sentinel handler that sets a header;
// a 403 response from Require never reaches the wrapped handler, so the
// header is absent.
func TestRequire(t *testing.T) {
	const sentinelHeader = "X-Reached-Inner"
	pol := NewBasicPolicy(DefaultRoleCapabilities())

	innerCalled := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(sentinelHeader, "yes")
		w.WriteHeader(http.StatusOK)
	}

	cases := []struct {
		name         string
		setPrincipal bool
		principal    Principal
		capability   Capability
		wantStatus   int
		wantReached  bool
		wantBodySub  string // substring of response body
	}{
		{
			name:         "no principal -> 401",
			setPrincipal: false,
			capability:   CapRead,
			wantStatus:   http.StatusUnauthorized,
			wantReached:  false,
			wantBodySub:  "unauthorized",
		},
		{
			name:         "principal lacks cap -> 403",
			setPrincipal: true,
			principal:    Principal{UserID: "user:1", Roles: []Role{RoleSubscriber}},
			capability:   CapManageOptions,
			wantStatus:   http.StatusForbidden,
			wantReached:  false,
			wantBodySub:  "no role on principal grants",
		},
		{
			name:         "principal has cap -> 200",
			setPrincipal: true,
			principal:    Principal{UserID: "user:1", Roles: []Role{RoleAdmin}},
			capability:   CapManageOptions,
			wantStatus:   http.StatusOK,
			wantReached:  true,
		},
		{
			name:         "principal empty roles -> 403",
			setPrincipal: true,
			principal:    Principal{UserID: "user:1"},
			capability:   CapRead,
			wantStatus:   http.StatusForbidden,
			wantReached:  false,
			wantBodySub:  "no roles",
		},
		{
			name:         "principal with author cannot install plugins -> 403",
			setPrincipal: true,
			principal:    Principal{UserID: "user:1", Roles: []Role{RoleAuthor}},
			capability:   CapInstallPlugins,
			wantStatus:   http.StatusForbidden,
			wantReached:  false,
		},
		{
			name:         "super_admin reaches handler for manage_install",
			setPrincipal: true,
			principal:    Principal{UserID: "user:1", Roles: []Role{RoleSuperAdmin}},
			capability:   CapManageInstall,
			wantStatus:   http.StatusOK,
			wantReached:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handler := Require(pol, tc.capability)(http.HandlerFunc(innerCalled))

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.setPrincipal {
				req = req.WithContext(WithPrincipal(req.Context(), tc.principal))
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d (body=%q)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			reached := rec.Header().Get(sentinelHeader) == "yes"
			if reached != tc.wantReached {
				t.Errorf("reached inner = %v, want %v", reached, tc.wantReached)
			}
			if tc.wantBodySub != "" && !strings.Contains(rec.Body.String(), tc.wantBodySub) {
				t.Errorf("body = %q, want substring %q", rec.Body.String(), tc.wantBodySub)
			}
		})
	}
}

// TestRequire_PrincipalIsObservableInHandler verifies that the wrapped
// handler still sees the Principal on the context via FromContext.
// Require should not strip or replace the principal on its way through.
func TestRequire_PrincipalIsObservableInHandler(t *testing.T) {
	pol := NewBasicPolicy(DefaultRoleCapabilities())
	want := Principal{UserID: "user:99", Roles: []Role{RoleAdmin}}

	var sawUserID string
	handler := Require(pol, CapManageOptions)(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			p, ok := FromContext(r.Context())
			if ok {
				sawUserID = p.UserID
			}
			w.WriteHeader(http.StatusNoContent)
		}))

	req := httptest.NewRequest(http.MethodGet, "/", nil).
		WithContext(WithPrincipal(t.Context(), want))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if sawUserID != want.UserID {
		t.Errorf("inner handler saw UserID %q, want %q", sawUserID, want.UserID)
	}
}
