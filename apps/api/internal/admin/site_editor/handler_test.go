package site_editor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// stubSource is a minimal in-memory PartsSource backed by a map.
// Constructed via newStubSource which builds an fstest.MapFS to feed
// the FSPartsSource — that way the tests cover the production source
// implementation, not a parallel test-only one.
type stubSource struct {
	theme string
	meta  []PartMeta
	files map[string]string
}

func (s *stubSource) ActiveTheme(_ context.Context) (string, error) {
	if s.theme == "" {
		return "", ErrNoActiveTheme
	}
	return s.theme, nil
}
func (s *stubSource) List(_ context.Context) ([]PartMeta, error) { return s.meta, nil }
func (s *stubSource) Read(_ context.Context, name string) ([]byte, error) {
	v, ok := s.files[name]
	if !ok {
		return nil, ErrPartNotFound
	}
	return []byte(v), nil
}

func newStubSource() *stubSource {
	return &stubSource{
		theme: "gn-hello",
		meta: []PartMeta{
			{Name: "header", Title: "Header", Area: "header"},
			{Name: "footer", Title: "Footer", Area: "footer"},
			{Name: "sidebar", Title: "Sidebar", Area: "sidebar"},
		},
		files: map[string]string{
			"header":  "<p>Hello header</p>",
			"footer":  "<p>Footer line</p>",
			"sidebar": "<p>Side</p>",
		},
	}
}

// adminPrincipal carries the theme.edit_parts capability via the
// "admin" role under the default policy.
func adminPrincipal() policy.Principal {
	return policy.Principal{UserID: "user:1", Roles: []policy.Role{policy.RoleAdmin}}
}

// editorPrincipal lacks theme.edit_parts. Editors get most write
// caps but not the site-editor surface.
func editorPrincipal() policy.Principal {
	return policy.Principal{UserID: "user:2", Roles: []policy.Role{policy.RoleEditor}}
}

func newTestServer(t *testing.T, source PartsSource, store OverrideStore) (*http.ServeMux, *Handler) {
	t.Helper()
	pol := policy.NewBasicPolicy(policy.DefaultRoleCapabilities())
	mux := http.NewServeMux()
	h, err := Mount(mux, "/api/v1/admin/site_editor", Deps{
		Source:    source,
		Overrides: store,
		Policy:    pol,
	})
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}
	return mux, h
}

func doRequest(t *testing.T, mux http.Handler, method, path string, pr *policy.Principal, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, path, bodyReader)
	if pr != nil {
		req = req.WithContext(policy.WithPrincipal(req.Context(), *pr))
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

func TestList_ReturnsParsedTrees(t *testing.T) {
	src := newStubSource()
	store := NewMemoryOverrideStore()
	mux, _ := newTestServer(t, src, store)

	pr := adminPrincipal()
	rr := doRequest(t, mux, http.MethodGet, "/api/v1/admin/site_editor/parts", &pr, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rr.Code, rr.Body.String())
	}

	var resp listResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body = %s", err, rr.Body.String())
	}
	if resp.Theme != "gn-hello" {
		t.Fatalf("theme = %q, want gn-hello", resp.Theme)
	}
	if len(resp.Parts) != 3 {
		t.Fatalf("parts = %d, want 3 (header/footer/sidebar)", len(resp.Parts))
	}

	// Each part should have a parsed tree.
	for _, p := range resp.Parts {
		if len(p.Blocks) == 0 {
			t.Errorf("part %q: blocks empty; want at least one parsed paragraph", p.Name)
		}
		if p.Overridden {
			t.Errorf("part %q: overridden true on a fresh install", p.Name)
		}
	}
}

func TestList_ReflectsOverride(t *testing.T) {
	src := newStubSource()
	store := NewMemoryOverrideStore()
	override := BlockTree{{Name: "core/paragraph", Attrs: map[string]any{"content": "Overridden header"}}}
	if err := store.Put(context.Background(), "gn-hello", "header", override); err != nil {
		t.Fatalf("seed override: %v", err)
	}

	mux, _ := newTestServer(t, src, store)
	pr := adminPrincipal()
	rr := doRequest(t, mux, http.MethodGet, "/api/v1/admin/site_editor/parts", &pr, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rr.Code, rr.Body.String())
	}

	var resp listResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	var seen bool
	for _, p := range resp.Parts {
		if p.Name != "header" {
			continue
		}
		seen = true
		if !p.Overridden {
			t.Errorf("header: overridden = false, want true")
		}
		if len(p.Blocks) != 1 || p.Blocks[0].Name != "core/paragraph" {
			t.Errorf("header: unexpected blocks: %+v", p.Blocks)
		}
		if c, _ := p.Blocks[0].Attrs["content"].(string); c != "Overridden header" {
			t.Errorf("header content = %q, want Overridden header", c)
		}
	}
	if !seen {
		t.Fatal("header part missing from response")
	}
}

func TestPut_PersistsAndValidates(t *testing.T) {
	src := newStubSource()
	store := NewMemoryOverrideStore()
	mux, _ := newTestServer(t, src, store)

	body, _ := json.Marshal(overrideRequest{
		Blocks: BlockTree{{Name: "core/paragraph", Attrs: map[string]any{"content": "new"}}},
	})
	pr := adminPrincipal()
	rr := doRequest(t, mux, http.MethodPut, "/api/v1/admin/site_editor/parts/footer", &pr, body)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rr.Code, rr.Body.String())
	}

	// Override actually persisted?
	tree, found, err := store.Get(context.Background(), "gn-hello", "footer")
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if !found {
		t.Fatal("override not found in store after PUT")
	}
	if len(tree) != 1 || tree[0].Name != "core/paragraph" {
		t.Errorf("stored tree = %+v", tree)
	}
}

func TestPut_RejectsUnknownBlock(t *testing.T) {
	src := newStubSource()
	store := NewMemoryOverrideStore()
	mux, _ := newTestServer(t, src, store)

	body, _ := json.Marshal(overrideRequest{
		Blocks: BlockTree{{Name: "evil/script", Attrs: map[string]any{"src": "x"}}},
	})
	pr := adminPrincipal()
	rr := doRequest(t, mux, http.MethodPut, "/api/v1/admin/site_editor/parts/header", &pr, body)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body = %s", rr.Code, rr.Body.String())
	}
	if _, found, _ := store.Get(context.Background(), "gn-hello", "header"); found {
		t.Error("override persisted despite validation failure")
	}
}

func TestPut_RejectsUndeclaredPart(t *testing.T) {
	src := newStubSource()
	store := NewMemoryOverrideStore()
	mux, _ := newTestServer(t, src, store)

	body, _ := json.Marshal(overrideRequest{Blocks: BlockTree{}})
	pr := adminPrincipal()
	rr := doRequest(t, mux, http.MethodPut, "/api/v1/admin/site_editor/parts/nonexistent", &pr, body)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", rr.Code, rr.Body.String())
	}
}

func TestPut_RejectsMissingBlocksField(t *testing.T) {
	src := newStubSource()
	store := NewMemoryOverrideStore()
	mux, _ := newTestServer(t, src, store)

	pr := adminPrincipal()
	rr := doRequest(t, mux, http.MethodPut, "/api/v1/admin/site_editor/parts/header", &pr, []byte(`{}`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rr.Code, rr.Body.String())
	}
}

func TestPut_AcceptsExplicitEmptyArray(t *testing.T) {
	src := newStubSource()
	store := NewMemoryOverrideStore()
	mux, _ := newTestServer(t, src, store)

	pr := adminPrincipal()
	rr := doRequest(t, mux, http.MethodPut, "/api/v1/admin/site_editor/parts/header", &pr, []byte(`{"blocks":[]}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rr.Code, rr.Body.String())
	}
}

func TestDelete_RemovesOverride(t *testing.T) {
	src := newStubSource()
	store := NewMemoryOverrideStore()
	if err := store.Put(context.Background(), "gn-hello", "footer", BlockTree{{Name: "core/paragraph"}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	mux, _ := newTestServer(t, src, store)

	pr := adminPrincipal()
	rr := doRequest(t, mux, http.MethodDelete, "/api/v1/admin/site_editor/parts/footer", &pr, nil)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body = %s", rr.Code, rr.Body.String())
	}
	if _, found, _ := store.Get(context.Background(), "gn-hello", "footer"); found {
		t.Error("override still present after DELETE")
	}
}

func TestDelete_IsIdempotent(t *testing.T) {
	src := newStubSource()
	store := NewMemoryOverrideStore()
	mux, _ := newTestServer(t, src, store)

	pr := adminPrincipal()
	rr := doRequest(t, mux, http.MethodDelete, "/api/v1/admin/site_editor/parts/footer", &pr, nil)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body = %s", rr.Code, rr.Body.String())
	}
}

func TestDelete_RejectsUndeclaredPart(t *testing.T) {
	src := newStubSource()
	store := NewMemoryOverrideStore()
	mux, _ := newTestServer(t, src, store)

	pr := adminPrincipal()
	rr := doRequest(t, mux, http.MethodDelete, "/api/v1/admin/site_editor/parts/bogus", &pr, nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", rr.Code, rr.Body.String())
	}
}

func TestCapabilityGate_Denies(t *testing.T) {
	src := newStubSource()
	store := NewMemoryOverrideStore()
	mux, _ := newTestServer(t, src, store)

	// Unauthenticated.
	rr := doRequest(t, mux, http.MethodGet, "/api/v1/admin/site_editor/parts", nil, nil)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("anonymous status = %d, want 401", rr.Code)
	}

	// Editor lacks theme.edit_parts.
	pr := editorPrincipal()
	rr = doRequest(t, mux, http.MethodGet, "/api/v1/admin/site_editor/parts", &pr, nil)
	if rr.Code != http.StatusForbidden {
		t.Errorf("editor status = %d, want 403", rr.Code)
	}

	body, _ := json.Marshal(overrideRequest{Blocks: BlockTree{}})
	rr = doRequest(t, mux, http.MethodPut, "/api/v1/admin/site_editor/parts/header", &pr, body)
	if rr.Code != http.StatusForbidden {
		t.Errorf("editor PUT status = %d, want 403", rr.Code)
	}

	rr = doRequest(t, mux, http.MethodDelete, "/api/v1/admin/site_editor/parts/header", &pr, nil)
	if rr.Code != http.StatusForbidden {
		t.Errorf("editor DELETE status = %d, want 403", rr.Code)
	}
}

func TestPut_OversizedBodyRejected(t *testing.T) {
	src := newStubSource()
	store := NewMemoryOverrideStore()
	mux, _ := newTestServer(t, src, store)

	// 2 MiB of filler in a single attribute. The validator never sees
	// it — the size cap fires first.
	big := strings.Repeat("a", 2*1024*1024)
	body, _ := json.Marshal(overrideRequest{
		Blocks: BlockTree{{Name: "core/paragraph", Attrs: map[string]any{"content": big}}},
	})
	pr := adminPrincipal()
	rr := doRequest(t, mux, http.MethodPut, "/api/v1/admin/site_editor/parts/header", &pr, body)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body length = %d", rr.Code, rr.Body.Len())
	}
}

func TestMount_RequiresDeps(t *testing.T) {
	mux := http.NewServeMux()
	if _, err := Mount(mux, "/x", Deps{}); err == nil {
		t.Fatal("Mount with empty Deps did not error")
	}
	if _, err := Mount(mux, "/x", Deps{Source: newStubSource()}); err == nil {
		t.Fatal("Mount with missing Overrides did not error")
	}
}

// TestFSPartsSource_RoundTrips covers the FS-backed source against an
// fstest.MapFS. The handler tests use a hand-written stub source for
// speed; this one pins the production source's contract.
func TestFSPartsSource_RoundTrips(t *testing.T) {
	fsys := fstest.MapFS{
		"header.html": &fstest.MapFile{Data: []byte("<p>hi</p>")},
		"footer.html": &fstest.MapFile{Data: []byte("<p>bye</p>")},
	}
	meta := []PartMeta{
		{Name: "header", Title: "Site Header", Area: "header"},
		{Name: "footer", Title: "Site Footer", Area: "footer"},
	}
	src := NewFSPartsSource(
		fsys,
		func(context.Context) (string, error) { return "test", nil },
		func(context.Context, string) ([]PartMeta, error) { return meta, nil },
	)

	got, err := src.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("List len = %d, want 2", len(got))
	}
	// Sorted alphabetically.
	if got[0].Name != "footer" || got[1].Name != "header" {
		t.Errorf("List order = %v", got)
	}

	raw, err := src.Read(context.Background(), "header")
	if err != nil {
		t.Fatalf("Read header: %v", err)
	}
	if string(raw) != "<p>hi</p>" {
		t.Errorf("Read header = %q", raw)
	}

	if _, err := src.Read(context.Background(), "nonexistent"); !errors.Is(err, ErrPartNotFound) {
		t.Errorf("Read nonexistent err = %v, want ErrPartNotFound", err)
	}
}

func TestResolver_OverrideWins(t *testing.T) {
	src := newStubSource()
	store := NewMemoryOverrideStore()
	resolver := NewResolver(src, store)

	ctx := context.Background()
	// Disk-path read: parses on-disk HTML.
	tree, overridden, err := resolver.Resolve(ctx, "gn-hello", "header")
	if err != nil {
		t.Fatalf("disk Resolve: %v", err)
	}
	if overridden {
		t.Error("disk Resolve: overridden = true, want false")
	}
	if len(tree) == 0 || tree[0].Name != "core/paragraph" {
		t.Errorf("disk Resolve tree = %+v", tree)
	}

	// Override path wins.
	if err := store.Put(ctx, "gn-hello", "header", BlockTree{{Name: "core/heading"}}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	tree, overridden, err = resolver.Resolve(ctx, "gn-hello", "header")
	if err != nil {
		t.Fatalf("override Resolve: %v", err)
	}
	if !overridden {
		t.Error("override Resolve: overridden = false, want true")
	}
	if len(tree) != 1 || tree[0].Name != "core/heading" {
		t.Errorf("override Resolve tree = %+v", tree)
	}
}

func TestHTMLToBlocks_KnownInputs(t *testing.T) {
	// Pins the converter contract for the parts we ship: gn-hello
	// header (group → site-title + navigation) and gn-hello footer
	// (group → paragraph).
	src := newStubSource()
	resolver := NewResolver(src, NewMemoryOverrideStore())

	// We use the real gn-hello header markup so the test fails if the
	// converter regresses on the real shipping content.
	src.files["header"] = "<!-- wp:group {\"tagName\":\"header\"} -->\n<header><!-- wp:site-title /--></header>\n<!-- /wp:group -->\n"
	resolver.InvalidateAll()

	tree, _, err := resolver.Resolve(context.Background(), "gn-hello", "header")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(tree) == 0 {
		t.Fatal("Resolve returned empty tree for known gutenberg input")
	}
	if tree[0].Name != "core/group" {
		t.Errorf("top-level block name = %q, want core/group", tree[0].Name)
	}
}
