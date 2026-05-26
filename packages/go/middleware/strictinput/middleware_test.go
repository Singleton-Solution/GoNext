package strictinput

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newGoodHandler(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func TestDisabled_IsPassthrough(t *testing.T) {
	t.Parallel()
	mw := Middleware(Config{Enabled: false})
	h := mw(newGoodHandler(t))
	body := strings.NewReader(`{"extra": "field"}`)
	req := httptest.NewRequest("POST", "/api/v1/anything", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Errorf("status = %d, want 200", rr.Code)
	}
}

func TestEnabled_GraphQL_AllowsValidEnvelope(t *testing.T) {
	t.Parallel()
	mw := Middleware(Config{Enabled: true})
	h := mw(newGoodHandler(t))
	body := bytes.NewBufferString(`{"query":"{posts{id}}","variables":{"limit":10}}`)
	req := httptest.NewRequest("POST", "/api/graphql", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Errorf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
}

func TestEnabled_GraphQL_RejectsUnknownKey(t *testing.T) {
	t.Parallel()
	mw := Middleware(Config{Enabled: true})
	h := mw(newGoodHandler(t))
	body := bytes.NewBufferString(`{"query":"{posts{id}}","debug":true}`)
	req := httptest.NewRequest("POST", "/api/graphql", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 400 {
		t.Errorf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

func TestEnabled_GraphQL_RejectsExcessiveDepth(t *testing.T) {
	t.Parallel()
	mw := Middleware(Config{Enabled: true, MaxGraphQLVariableDepth: 3})
	h := mw(newGoodHandler(t))
	// Variables nested 5 deep: {a:{b:{c:{d:{e:1}}}}}.
	body := bytes.NewBufferString(`{"query":"q","variables":{"a":{"b":{"c":{"d":{"e":1}}}}}}`)
	req := httptest.NewRequest("POST", "/api/graphql", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 400 {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestEnabled_REST_RejectsMalformedJSON(t *testing.T) {
	t.Parallel()
	mw := Middleware(Config{Enabled: true})
	h := mw(newGoodHandler(t))
	body := strings.NewReader(`{"title": "broken`)
	req := httptest.NewRequest("POST", "/api/v1/posts", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 400 {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestEnabled_REST_AllowsExtraFields(t *testing.T) {
	// The middleware does not duplicate the per-handler
	// DisallowUnknownFields check — extra fields are the handler's
	// concern. The middleware only asserts shape (valid JSON, single
	// value, no trailing data).
	t.Parallel()
	mw := Middleware(Config{Enabled: true})
	h := mw(newGoodHandler(t))
	body := strings.NewReader(`{"title": "ok", "extra": "field"}`)
	req := httptest.NewRequest("POST", "/api/v1/posts", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Errorf("status = %d, want 200 (handler is the unknown-fields gate)", rr.Code)
	}
}

func TestEnabled_BypassesGET(t *testing.T) {
	t.Parallel()
	mw := Middleware(Config{Enabled: true})
	h := mw(newGoodHandler(t))
	req := httptest.NewRequest("GET", "/api/v1/posts", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Errorf("status = %d, want 200", rr.Code)
	}
}

func TestIsJSON(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"application/json":                  true,
		"application/json; charset=utf-8":   true,
		"application/problem+json":          true,
		"application/vnd.custom+json":       true,
		"text/html":                         false,
		"":                                  false,
		"multipart/form-data; boundary=xyz": false,
	}
	for in, want := range cases {
		if got := isJSON(in); got != want {
			t.Errorf("isJSON(%q) = %v, want %v", in, got, want)
		}
	}
}
