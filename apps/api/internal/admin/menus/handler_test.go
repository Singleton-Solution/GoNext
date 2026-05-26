package menus

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	pkgmenus "github.com/Singleton-Solution/GoNext/packages/go/menus"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

const base = "/api/v1/admin/menus"

type harness struct {
	mux   *http.ServeMux
	store *pkgmenus.MemoryStore
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	store := pkgmenus.NewMemoryStore()
	pol := policy.NewBasicPolicy(policy.DefaultRoleCapabilities())
	mux := http.NewServeMux()
	if err := Mount(mux, base, Deps{
		Store:  store,
		Policy: pol,
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
	}); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	return &harness{mux: mux, store: store}
}

func adminPrincipal() policy.Principal {
	return policy.Principal{UserID: "u:1", Roles: []policy.Role{policy.RoleAdmin}}
}

func subscriberPrincipal() policy.Principal {
	return policy.Principal{UserID: "u:2", Roles: []policy.Role{policy.RoleSubscriber}}
}

func (h *harness) do(req *http.Request, pr *policy.Principal) *httptest.ResponseRecorder {
	if pr != nil {
		req = req.WithContext(policy.WithPrincipal(req.Context(), *pr))
	}
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	return rec
}

func TestCreateMenu(t *testing.T) {
	h := newHarness(t)
	pr := adminPrincipal()
	body, _ := json.Marshal(map[string]string{"slug": "primary", "name": "Primary"})
	rec := h.do(httptest.NewRequest(http.MethodPost, base, bytes.NewReader(body)), &pr)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var m pkgmenus.Menu
	if err := json.NewDecoder(rec.Body).Decode(&m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if m.Slug != "primary" || m.Name != "Primary" {
		t.Fatalf("created shape wrong: %+v", m)
	}
}

func TestSubscriberForbidden(t *testing.T) {
	h := newHarness(t)
	pr := subscriberPrincipal()
	rec := h.do(httptest.NewRequest(http.MethodGet, base, nil), &pr)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestItemsCRUDAndReorder(t *testing.T) {
	h := newHarness(t)
	pr := adminPrincipal()

	// Create menu.
	body, _ := json.Marshal(map[string]string{"slug": "x", "name": "X"})
	rec := h.do(httptest.NewRequest(http.MethodPost, base, bytes.NewReader(body)), &pr)
	var m pkgmenus.Menu
	_ = json.NewDecoder(rec.Body).Decode(&m)

	// Append two items.
	body, _ = json.Marshal(map[string]string{"path": "001", "label": "Home", "url": "/"})
	rec = h.do(httptest.NewRequest(http.MethodPost, base+"/"+m.ID.String()+"/items", bytes.NewReader(body)), &pr)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create item 1: %d %s", rec.Code, rec.Body.String())
	}
	var item1 pkgmenus.MenuItem
	_ = json.NewDecoder(rec.Body).Decode(&item1)

	body, _ = json.Marshal(map[string]string{"path": "002", "label": "About", "url": "/about"})
	rec = h.do(httptest.NewRequest(http.MethodPost, base+"/"+m.ID.String()+"/items", bytes.NewReader(body)), &pr)
	var item2 pkgmenus.MenuItem
	_ = json.NewDecoder(rec.Body).Decode(&item2)

	// Reorder — swap them.
	body, _ = json.Marshal(map[string]any{
		"items": []map[string]string{
			{"id": item1.ID.String(), "path": "002"},
			{"id": item2.ID.String(), "path": "001"},
		},
	})
	rec = h.do(httptest.NewRequest(http.MethodPost, base+"/"+m.ID.String()+"/items/reorder", bytes.NewReader(body)), &pr)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("reorder: %d %s", rec.Code, rec.Body.String())
	}

	// Verify ordering.
	rec = h.do(httptest.NewRequest(http.MethodGet, base+"/"+m.ID.String(), nil), &pr)
	if rec.Code != http.StatusOK {
		t.Fatalf("get: %d %s", rec.Code, rec.Body.String())
	}
	var bundle pkgmenus.MenuWithItems
	_ = json.NewDecoder(rec.Body).Decode(&bundle)
	if len(bundle.Items) != 2 || bundle.Items[0].Label != "About" {
		t.Fatalf("expected About first after reorder: %+v", bundle.Items)
	}
}
