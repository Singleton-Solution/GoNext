package frontend

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandler_RegisterAndServeBundle(t *testing.T) {
	h := NewHandler(nil)
	src := []byte(`export const hi = "hello";` + "\n")
	err := h.Register(PluginBundle{
		Slug: "seo",
		Entries: []BundleEntry{
			{Path: "seo.mjs", Bytes: src},
		},
		Imports: map[string]string{
			"@plugin/seo": "/api/plugins/seo/web/seo.mjs",
		},
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/plugins/seo/web/seo.mjs", nil)
	rec := httptest.NewRecorder()
	h.ServeBundle(rec, req)
	resp := rec.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != string(src) {
		t.Errorf("body: got %q", body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/javascript" {
		t.Errorf("content-type: got %q", ct)
	}
	if sri := resp.Header.Get("X-SRI"); !strings.HasPrefix(sri, "sha256-") {
		t.Errorf("X-SRI: got %q", sri)
	}
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("cache-control: got %q", cc)
	}
	if etag := resp.Header.Get("ETag"); etag == "" || !strings.HasPrefix(etag, `"`) {
		t.Errorf("ETag: got %q", etag)
	}
}

func TestHandler_NotFound(t *testing.T) {
	h := NewHandler(nil)
	req := httptest.NewRequest(http.MethodGet, "/api/plugins/none/web/foo.mjs", nil)
	rec := httptest.NewRecorder()
	h.ServeBundle(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status: %d", rec.Code)
	}
}

func TestHandler_PathTraversalRejected(t *testing.T) {
	h := NewHandler(nil)
	_ = h.Register(PluginBundle{
		Slug:    "p",
		Entries: []BundleEntry{{Path: "ok.mjs", Bytes: []byte("ok")}},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/plugins/p/web/../etc/passwd", nil)
	rec := httptest.NewRecorder()
	h.ServeBundle(rec, req)
	if rec.Code != http.StatusBadRequest && rec.Code != http.StatusNotFound {
		t.Errorf("traversal: got %d", rec.Code)
	}
}

func TestHandler_RegisterRejectsBadPath(t *testing.T) {
	h := NewHandler(nil)
	err := h.Register(PluginBundle{
		Slug:    "p",
		Entries: []BundleEntry{{Path: "../bad.mjs", Bytes: []byte("x")}},
	})
	if err == nil {
		t.Errorf("expected error on bad path")
	}
}

func TestHandler_ImportMapComposition(t *testing.T) {
	h := NewHandler(nil)
	_ = h.Register(PluginBundle{
		Slug:    "a",
		Entries: []BundleEntry{{Path: "a.mjs", Bytes: []byte("a")}},
		Imports: map[string]string{"@plugin/a": "/api/plugins/a/web/a.mjs"},
	})
	_ = h.Register(PluginBundle{
		Slug:    "b",
		Entries: []BundleEntry{{Path: "b.mjs", Bytes: []byte("b")}},
		Imports: map[string]string{"@plugin/b": "/api/plugins/b/web/b.mjs"},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/plugins/import-map.json", nil)
	rec := httptest.NewRecorder()
	h.ServeImportMap(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/importmap+json" {
		t.Errorf("content-type: %q", ct)
	}
	var parsed struct {
		Imports map[string]string `json:"imports"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, rec.Body.String())
	}
	if parsed.Imports["@plugin/a"] != "/api/plugins/a/web/a.mjs" {
		t.Errorf("a: %v", parsed.Imports)
	}
	if parsed.Imports["@plugin/b"] != "/api/plugins/b/web/b.mjs" {
		t.Errorf("b: %v", parsed.Imports)
	}
}

func TestHandler_ImportCollisionRejected(t *testing.T) {
	h := NewHandler(nil)
	_ = h.Register(PluginBundle{
		Slug:    "a",
		Imports: map[string]string{"shared": "/api/plugins/a/web/x.mjs"},
	})
	err := h.Register(PluginBundle{
		Slug:    "b",
		Imports: map[string]string{"shared": "/api/plugins/b/web/y.mjs"},
	})
	if err == nil {
		t.Errorf("expected collision error")
	}
}

func TestHandler_Unregister(t *testing.T) {
	h := NewHandler(nil)
	_ = h.Register(PluginBundle{
		Slug:    "a",
		Entries: []BundleEntry{{Path: "a.mjs", Bytes: []byte("a")}},
		Imports: map[string]string{"@plugin/a": "/api/plugins/a/web/a.mjs"},
	})
	h.Unregister("a")
	req := httptest.NewRequest(http.MethodGet, "/api/plugins/a/web/a.mjs", nil)
	rec := httptest.NewRecorder()
	h.ServeBundle(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("after unregister: %d", rec.Code)
	}
	if got := h.ImportMapSnapshot(); len(got) != 0 {
		t.Errorf("imports remaining: %v", got)
	}
}

func TestHandler_NotModified(t *testing.T) {
	h := NewHandler(nil)
	_ = h.Register(PluginBundle{
		Slug:    "p",
		Entries: []BundleEntry{{Path: "p.mjs", Bytes: []byte("x")}},
	})
	req1 := httptest.NewRequest(http.MethodGet, "/api/plugins/p/web/p.mjs", nil)
	rec1 := httptest.NewRecorder()
	h.ServeBundle(rec1, req1)
	etag := rec1.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no etag")
	}

	req2 := httptest.NewRequest(http.MethodGet, "/api/plugins/p/web/p.mjs", nil)
	req2.Header.Set("If-None-Match", etag)
	rec2 := httptest.NewRecorder()
	h.ServeBundle(rec2, req2)
	if rec2.Code != http.StatusNotModified {
		t.Errorf("304: got %d", rec2.Code)
	}
}

func TestHandler_SRIByURL(t *testing.T) {
	h := NewHandler(nil)
	_ = h.Register(PluginBundle{
		Slug:    "p",
		Entries: []BundleEntry{{Path: "p.mjs", Bytes: []byte("hello")}},
	})
	sri := h.SRIByURL("/api/plugins/p/web/p.mjs")
	if !strings.HasPrefix(sri, "sha256-") {
		t.Errorf("sri: %q", sri)
	}
	if h.SRIByURL("/api/plugins/p/web/missing.mjs") != "" {
		t.Errorf("expected empty SRI for unknown URL")
	}
}

func TestHandler_ImportMapScriptTag(t *testing.T) {
	h := NewHandler(nil)
	_ = h.Register(PluginBundle{
		Slug:    "a",
		Imports: map[string]string{"@plugin/a": "/api/plugins/a/web/a.mjs"},
	})
	tag := h.ImportMapScriptTag()
	if !strings.HasPrefix(tag, `<script type="importmap">`) {
		t.Errorf("tag: %q", tag)
	}
	if !strings.Contains(tag, "@plugin/a") {
		t.Errorf("missing key: %q", tag)
	}
	if !strings.HasSuffix(tag, "</script>") {
		t.Errorf("trailer: %q", tag)
	}
}

func TestHandler_HEADResponse(t *testing.T) {
	h := NewHandler(nil)
	_ = h.Register(PluginBundle{
		Slug:    "p",
		Entries: []BundleEntry{{Path: "p.mjs", Bytes: []byte("xxxxxxxxxxxx")}},
	})
	req := httptest.NewRequest(http.MethodHead, "/api/plugins/p/web/p.mjs", nil)
	rec := httptest.NewRecorder()
	h.ServeBundle(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("head: %d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("head body: %q", rec.Body.String())
	}
	if rec.Header().Get("Content-Length") == "" {
		t.Errorf("missing content-length")
	}
}
