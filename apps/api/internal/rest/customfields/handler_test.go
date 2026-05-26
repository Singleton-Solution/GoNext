package customfields

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	cf "github.com/Singleton-Solution/GoNext/packages/go/customfields"
)

func newMux(t *testing.T) (*http.ServeMux, cf.Store) {
	t.Helper()
	store := cf.NewMemoryStore()
	mux := http.NewServeMux()
	if err := MountGroups(mux, "/api/v1/custom-fields/groups", Deps{Store: store}); err != nil {
		t.Fatalf("mount groups: %v", err)
	}
	if err := MountMeta(mux, "/api/v1/posts", Deps{Store: store}); err != nil {
		t.Fatalf("mount meta: %v", err)
	}
	return mux, store
}

func TestCreateAndGetGroup(t *testing.T) {
	t.Parallel()
	mux, _ := newMux(t)

	in := `{
		"slug": "product",
		"title": "Product",
		"schema": {"type":"object","properties":{"price":{"type":"number"}}}
	}`
	req := httptest.NewRequest("POST", "/api/v1/custom-fields/groups", strings.NewReader(in))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != 201 {
		t.Fatalf("create status = %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	var g cf.FieldGroup
	_ = json.Unmarshal(rr.Body.Bytes(), &g)
	if g.ID == "" || g.Slug != "product" {
		t.Errorf("unexpected group: %+v", g)
	}

	// GET by id.
	req = httptest.NewRequest("GET", "/api/v1/custom-fields/groups/"+g.ID, nil)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Errorf("get status = %d, want 200", rr.Code)
	}
}

func TestPutMeta_ValidatesAgainstSchema(t *testing.T) {
	t.Parallel()
	mux, store := newMux(t)
	g, err := store.InsertGroup(nil, cf.FieldGroupCreate{
		Slug:   "product",
		Title:  "Product",
		Schema: json.RawMessage(`{"type":"object","required":["price"],"properties":{"price":{"type":"number"}}}`),
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Happy path.
	good := bytes.NewBufferString(`{"price": 9.99}`)
	req := httptest.NewRequest("PUT", "/api/v1/posts/post-1/meta/"+g.ID, good)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Errorf("put status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	// Bad payload — missing required field.
	bad := bytes.NewBufferString(`{}`)
	req = httptest.NewRequest("PUT", "/api/v1/posts/post-1/meta/"+g.ID, bad)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != 422 {
		t.Errorf("bad-put status = %d, want 422", rr.Code)
	}
}

func TestUpdateGroup_RequiresIfMatch(t *testing.T) {
	t.Parallel()
	mux, store := newMux(t)
	g, _ := store.InsertGroup(nil, cf.FieldGroupCreate{
		Slug:   "product",
		Title:  "Product",
		Schema: json.RawMessage(`{"type":"object"}`),
	})

	req := httptest.NewRequest("PATCH", "/api/v1/custom-fields/groups/"+g.ID,
		strings.NewReader(`{"title":"renamed"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusPreconditionRequired {
		t.Errorf("missing if-match status = %d, want 428", rr.Code)
	}
}

func TestDeleteGroup_CascadesMeta(t *testing.T) {
	t.Parallel()
	mux, store := newMux(t)
	g, _ := store.InsertGroup(nil, cf.FieldGroupCreate{
		Slug:   "product",
		Title:  "Product",
		Schema: json.RawMessage(`{"type":"object"}`),
	})
	_, _ = store.PutMeta(nil, "post-1", g.ID, json.RawMessage(`{}`))

	req := httptest.NewRequest("DELETE", "/api/v1/custom-fields/groups/"+g.ID, nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != 204 {
		t.Errorf("delete status = %d, want 204", rr.Code)
	}

	// Meta should be gone.
	if rows, _ := store.ListMeta(nil, "post-1"); len(rows) != 0 {
		t.Errorf("meta survived group delete: %v", rows)
	}
}
