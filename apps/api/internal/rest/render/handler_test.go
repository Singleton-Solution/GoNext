package render

import (
	"bytes"
	"encoding/json"
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	blockrender "github.com/Singleton-Solution/GoNext/packages/go/blocks/render"
)

// newTestHarness wires Mount onto a fresh ServeMux against a
// registry pre-seeded with the core block renderers. Returns the
// mux so each test can issue a real HTTP request through it.
func newTestHarness(t *testing.T) *http.ServeMux {
	t.Helper()
	reg := blockrender.NewRegistry()
	blockrender.MustRegisterCoreBlocks(reg)
	mux := http.NewServeMux()
	if err := Mount(mux, "/api/v1", Deps{Registry: reg}); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	return mux
}

func post(t *testing.T, mux *http.ServeMux, body string) (*http.Response, []byte) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/render/preview", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	res := rr.Result()
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	return res, b
}

func TestPreview_RendersBlockTree(t *testing.T) {
	t.Parallel()
	mux := newTestHarness(t)
	body := `{
	  "blocks": [
	    {"type":"core/heading","attributes":{"content":"Hello","level":2}},
	    {"type":"core/paragraph","attributes":{"content":"world"}}
	  ]
	}`
	res, raw := post(t, mux, body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", res.StatusCode, raw)
	}
	var resp PreviewResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !strings.Contains(resp.HTML, "<h2") {
		t.Fatalf("missing h2: %q", resp.HTML)
	}
	if !strings.Contains(resp.HTML, "<p ") {
		t.Fatalf("missing p: %q", resp.HTML)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("unexpected errors: %+v", resp.Errors)
	}
}

func TestPreview_UnknownBlockReturns200WithError(t *testing.T) {
	t.Parallel()
	mux := newTestHarness(t)
	body := `{
	  "blocks": [
	    {"type":"plugin/ghost","attributes":{}}
	  ]
	}`
	res, raw := post(t, mux, body)
	// Unknown blocks are non-fatal — the response carries an
	// HTML placeholder and a per-block error.
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", res.StatusCode, raw)
	}
	var resp PreviewResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !strings.Contains(resp.HTML, "gn-block-unknown") {
		t.Fatalf("missing placeholder: %q", resp.HTML)
	}
	if len(resp.Errors) != 1 {
		t.Fatalf("expected 1 error, got %+v", resp.Errors)
	}
	if resp.Errors[0].BlockType != "plugin/ghost" {
		t.Fatalf("error block type = %q", resp.Errors[0].BlockType)
	}
	if resp.Errors[0].Path != "/0" {
		t.Fatalf("error path = %q", resp.Errors[0].Path)
	}
}

func TestPreview_ContextThreadedToRenderers(t *testing.T) {
	t.Parallel()
	reg := blockrender.NewRegistry()
	reg.MustRegister("test/title", blockrender.BlockSpec{
		Render:      makeEchoRenderer("postId"),
		UsesContext: []string{"postId"},
	})
	mux := http.NewServeMux()
	if err := Mount(mux, "/api/v1", Deps{Registry: reg}); err != nil {
		t.Fatalf("Mount: %v", err)
	}

	body := `{
	  "blocks": [ {"type":"test/title","attributes":{}} ],
	  "context": { "postId": "p-42", "postType": "post" }
	}`
	res, raw := post(t, mux, body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", res.StatusCode, raw)
	}
	var resp PreviewResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !strings.Contains(resp.HTML, "p-42") {
		t.Fatalf("missing postId in output: %q", resp.HTML)
	}
}

func TestPreview_MethodGetReturns405(t *testing.T) {
	t.Parallel()
	mux := newTestHarness(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/render/preview", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rr.Code)
	}
	if got := rr.Header().Get("Allow"); got != http.MethodPost {
		t.Fatalf("Allow header = %q, want POST", got)
	}
}

func TestPreview_EmptyBodyReturns400(t *testing.T) {
	t.Parallel()
	mux := newTestHarness(t)
	res, raw := post(t, mux, "")
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", res.StatusCode, raw)
	}
}

func TestPreview_InvalidJSONReturns400(t *testing.T) {
	t.Parallel()
	mux := newTestHarness(t)
	res, raw := post(t, mux, "{not json")
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "invalid_json") {
		t.Fatalf("expected invalid_json code, body=%s", raw)
	}
}

func TestPreview_InvalidBlocksPayloadReturns400(t *testing.T) {
	t.Parallel()
	mux := newTestHarness(t)
	// blocks is a string, not an array — the inner DecodeTree
	// should reject it.
	body := `{"blocks":"not-an-array"}`
	res, raw := post(t, mux, body)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "invalid_blocks") {
		t.Fatalf("expected invalid_blocks code: %s", raw)
	}
}

func TestPreview_OmittedBlocksRendersEmpty(t *testing.T) {
	t.Parallel()
	mux := newTestHarness(t)
	body := `{"context":{"postId":"p-1"}}`
	res, raw := post(t, mux, body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", res.StatusCode, raw)
	}
	var resp PreviewResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.HTML != "" {
		t.Fatalf("expected empty html, got %q", resp.HTML)
	}
}

func TestPreview_BodyTooLargeReturns400(t *testing.T) {
	t.Parallel()
	mux := newTestHarness(t)
	// Build a payload larger than maxBodyBytes.
	big := bytes.Repeat([]byte("a"), int(maxBodyBytes)+1)
	wrapped := `{"blocks":[],"junk":"` + string(big) + `"}`
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/render/preview", strings.NewReader(wrapped))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestNewHandler_NilRegistryRejected(t *testing.T) {
	t.Parallel()
	if _, err := NewHandler(Deps{}); err == nil {
		t.Fatal("expected error for nil registry")
	}
}

// makeEchoRenderer returns a Renderer that echoes the named context
// key as an HTML text node. Used in the context-threading test
// above; lives at file scope so we can keep the test body short.
func makeEchoRenderer(key string) blockrender.Renderer {
	return func(_ blockrender.Block, _ template.HTML, ctx blockrender.Context) (template.HTML, error) {
		v, _ := ctx[key].(string)
		return template.HTML(v), nil
	}
}
