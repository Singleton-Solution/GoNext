package blocks

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	rb "github.com/Singleton-Solution/GoNext/packages/go/blocks/reusable"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

const reusableBase = "/api/v1/admin/blocks/reusable"

type reusableHarness struct {
	mux    *http.ServeMux
	store  *rb.MemoryStore
	policy policy.Policy
}

func newReusableHarness(t *testing.T) *reusableHarness {
	t.Helper()
	store := rb.NewMemoryStore()
	pol := policy.NewBasicPolicy(policy.DefaultRoleCapabilities())
	mux := http.NewServeMux()
	if err := MountReusable(mux, reusableBase, ReusableDeps{
		Store:  store,
		Policy: pol,
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
	}); err != nil {
		t.Fatalf("MountReusable: %v", err)
	}
	return &reusableHarness{mux: mux, store: store, policy: pol}
}

func editorPrincipal() policy.Principal {
	return policy.Principal{UserID: "user:1", Roles: []policy.Role{policy.RoleEditor}}
}

func subscriberPrincipal() policy.Principal {
	return policy.Principal{UserID: "user:2", Roles: []policy.Role{policy.RoleSubscriber}}
}

func (h *reusableHarness) do(req *http.Request, pr *policy.Principal) *httptest.ResponseRecorder {
	if pr != nil {
		req = req.WithContext(policy.WithPrincipal(req.Context(), *pr))
	}
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	return rec
}

func TestReusable_CreateGetList(t *testing.T) {
	h := newReusableHarness(t)
	pr := editorPrincipal()

	body, _ := json.Marshal(map[string]any{
		"name":    "Pricing CTA",
		"attrs":   map[string]string{"icon": "dollar"},
		"content": []any{map[string]any{"type": "core/paragraph", "attributes": map[string]string{"text": "hi"}}},
	})
	createReq := httptest.NewRequest(http.MethodPost, reusableBase, bytes.NewReader(body))
	createRec := h.do(createReq, &pr)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", createRec.Code, createRec.Body.String())
	}

	var created ReusableView
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	if created.ID == "" || created.Name != "Pricing CTA" {
		t.Fatalf("created shape wrong: %+v", created)
	}

	getReq := httptest.NewRequest(http.MethodGet, reusableBase+"/"+created.ID, nil)
	getRec := h.do(getReq, &pr)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%s", getRec.Code, getRec.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, reusableBase, nil)
	listRec := h.do(listReq, &pr)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", listRec.Code, listRec.Body.String())
	}
	if !strings.Contains(listRec.Body.String(), "Pricing CTA") {
		t.Fatalf("list body missing entry: %s", listRec.Body.String())
	}
}

func TestReusable_UpdateAndDelete(t *testing.T) {
	h := newReusableHarness(t)
	pr := editorPrincipal()

	created, _ := h.store.Create(t.Context(), rb.Entry{Name: "old"})

	body, _ := json.Marshal(map[string]any{
		"name":    "new",
		"content": []any{},
	})
	updReq := httptest.NewRequest(http.MethodPut, reusableBase+"/"+created.ID.String(), bytes.NewReader(body))
	updRec := h.do(updReq, &pr)
	if updRec.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", updRec.Code, updRec.Body.String())
	}

	delReq := httptest.NewRequest(http.MethodDelete, reusableBase+"/"+created.ID.String(), nil)
	delRec := h.do(delReq, &pr)
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", delRec.Code, delRec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, reusableBase+"/"+created.ID.String(), nil)
	getRec := h.do(getReq, &pr)
	if getRec.Code != http.StatusNotFound {
		t.Fatalf("get after delete status=%d body=%s", getRec.Code, getRec.Body.String())
	}
}

func TestReusable_GatedByEditPostsCap(t *testing.T) {
	h := newReusableHarness(t)
	sub := subscriberPrincipal()

	body, _ := json.Marshal(map[string]any{"name": "x"})
	req := httptest.NewRequest(http.MethodPost, reusableBase, bytes.NewReader(body))
	rec := h.do(req, &sub)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for subscriber, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestReusable_RejectsInvalidJSON(t *testing.T) {
	h := newReusableHarness(t)
	pr := editorPrincipal()
	req := httptest.NewRequest(http.MethodPost, reusableBase, strings.NewReader("{not json"))
	rec := h.do(req, &pr)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestReusable_RejectsBadID(t *testing.T) {
	h := newReusableHarness(t)
	pr := editorPrincipal()
	req := httptest.NewRequest(http.MethodGet, reusableBase+"/not-a-uuid", nil)
	rec := h.do(req, &pr)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestReusable_RejectsEmptyName(t *testing.T) {
	h := newReusableHarness(t)
	pr := editorPrincipal()
	body, _ := json.Marshal(map[string]any{"name": ""})
	req := httptest.NewRequest(http.MethodPost, reusableBase, bytes.NewReader(body))
	rec := h.do(req, &pr)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}
