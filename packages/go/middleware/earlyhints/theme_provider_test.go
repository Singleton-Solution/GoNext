package earlyhints

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestThemeAwareProvider_NilResolverReturnsNil(t *testing.T) {
	p := NewThemeAwareProvider(nil, ThemeAwareOptions{})
	hs, err := p.HintsFor(htmlReq("/"))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if hs != nil {
		t.Errorf("expected nil hints, got %+v", hs)
	}
}

func TestThemeAwareProvider_NilProviderSafe(t *testing.T) {
	var p *ThemeAwareProvider
	hs, err := p.HintsFor(htmlReq("/"))
	if err != nil || hs != nil {
		t.Errorf("nil provider should be safe: hs=%+v err=%v", hs, err)
	}
}

func TestThemeAwareProvider_EmitsStylesheetHint(t *testing.T) {
	resolver := ThemeStyleResolverFunc(func(r *http.Request) (string, []Hint) {
		return "/themes/active/style.css", nil
	})
	p := NewThemeAwareProvider(resolver, ThemeAwareOptions{})
	hs, err := p.HintsFor(htmlReq("/"))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(hs) != 1 {
		t.Fatalf("want 1 hint, got %d", len(hs))
	}
	h := hs[0]
	if h.URL != "/themes/active/style.css" {
		t.Errorf("URL: got %q", h.URL)
	}
	if h.As != "style" {
		t.Errorf("default As: got %q want style", h.As)
	}
	if h.FetchPriority != "high" {
		t.Errorf("default FetchPriority: got %q want high", h.FetchPriority)
	}
}

func TestThemeAwareProvider_IncludesExtras(t *testing.T) {
	resolver := ThemeStyleResolverFunc(func(r *http.Request) (string, []Hint) {
		return "/style.css", []Hint{
			{URL: "/font.woff2", As: "font", CrossOrigin: "anonymous"},
			{URL: "/hero.jpg", As: "image", FetchPriority: "high"},
		}
	})
	p := NewThemeAwareProvider(resolver, ThemeAwareOptions{})
	hs, err := p.HintsFor(htmlReq("/"))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(hs) != 3 {
		t.Fatalf("want 3 hints, got %d: %+v", len(hs), hs)
	}
	if hs[0].URL != "/style.css" {
		t.Errorf("expected stylesheet first: %+v", hs)
	}
}

func TestThemeAwareProvider_DeduplicatesExtras(t *testing.T) {
	resolver := ThemeStyleResolverFunc(func(r *http.Request) (string, []Hint) {
		return "/style.css", []Hint{
			{URL: "/style.css", As: "style"}, // duplicate of stylesheet
			{URL: "/font.woff2", As: "font"},
			{URL: "/font.woff2", As: "font"}, // duplicate of self
		}
	})
	p := NewThemeAwareProvider(resolver, ThemeAwareOptions{})
	hs, _ := p.HintsFor(htmlReq("/"))
	if len(hs) != 2 {
		t.Errorf("want 2 unique hints, got %d: %+v", len(hs), hs)
	}
}

func TestThemeAwareProvider_DefaultsAsForExtras(t *testing.T) {
	resolver := ThemeStyleResolverFunc(func(r *http.Request) (string, []Hint) {
		return "", []Hint{{URL: "/something"}} // no As provided
	})
	p := NewThemeAwareProvider(resolver, ThemeAwareOptions{})
	hs, _ := p.HintsFor(htmlReq("/"))
	if len(hs) != 1 {
		t.Fatalf("want 1 hint, got %d", len(hs))
	}
	if hs[0].As != "fetch" {
		t.Errorf("expected default As=fetch, got %q", hs[0].As)
	}
}

func TestThemeAwareProvider_NonHTMLAcceptSkipped(t *testing.T) {
	resolver := ThemeStyleResolverFunc(func(r *http.Request) (string, []Hint) {
		t.Error("resolver should not be called for non-HTML request")
		return "/x.css", nil
	})
	p := NewThemeAwareProvider(resolver, ThemeAwareOptions{})
	req := httptest.NewRequest("GET", "/api/v1/posts", nil)
	req.Header.Set("Accept", "application/json")
	hs, err := p.HintsFor(req)
	if err != nil || hs != nil {
		t.Errorf("want nil hints for JSON Accept: hs=%+v err=%v", hs, err)
	}
}

func TestThemeAwareProvider_AssetPathSkipped(t *testing.T) {
	resolver := ThemeStyleResolverFunc(func(r *http.Request) (string, []Hint) {
		t.Error("resolver should not be called for asset paths")
		return "/x.css", nil
	})
	p := NewThemeAwareProvider(resolver, ThemeAwareOptions{})
	req := httptest.NewRequest("GET", "/static/app.js", nil) // has extension
	hs, _ := p.HintsFor(req)
	if hs != nil {
		t.Errorf("want nil hints for asset path: %+v", hs)
	}
}

func TestThemeAwareProvider_POSTSkipped(t *testing.T) {
	resolver := ThemeStyleResolverFunc(func(r *http.Request) (string, []Hint) {
		t.Error("resolver should not be called for POST")
		return "/x.css", nil
	})
	p := NewThemeAwareProvider(resolver, ThemeAwareOptions{})
	req := httptest.NewRequest("POST", "/", nil)
	req.Header.Set("Accept", "text/html")
	hs, _ := p.HintsFor(req)
	if hs != nil {
		t.Errorf("want nil hints for POST: %+v", hs)
	}
}

func TestThemeAwareProvider_CustomPredicate(t *testing.T) {
	resolver := ThemeStyleResolverFunc(func(r *http.Request) (string, []Hint) {
		return "/only-for-posts.css", nil
	})
	p := NewThemeAwareProvider(resolver, ThemeAwareOptions{
		PathPredicate: func(r *http.Request) bool { return r.URL.Path == "/blog" },
	})
	if hs, _ := p.HintsFor(httptest.NewRequest("GET", "/blog", nil)); len(hs) != 1 {
		t.Errorf("custom predicate match: got %d hints", len(hs))
	}
	if hs, _ := p.HintsFor(httptest.NewRequest("GET", "/", nil)); hs != nil {
		t.Errorf("custom predicate miss should yield nil, got %+v", hs)
	}
}

func TestThemeAwareProvider_CustomStyleAttrs(t *testing.T) {
	resolver := ThemeStyleResolverFunc(func(r *http.Request) (string, []Hint) {
		return "https://cdn.example.com/style.css", nil
	})
	p := NewThemeAwareProvider(resolver, ThemeAwareOptions{
		StyleCrossOrigin:   "anonymous",
		StyleFetchPriority: "low",
	})
	hs, _ := p.HintsFor(htmlReq("/"))
	if len(hs) != 1 {
		t.Fatalf("want 1 hint, got %d", len(hs))
	}
	if hs[0].CrossOrigin != "anonymous" {
		t.Errorf("CrossOrigin: got %q", hs[0].CrossOrigin)
	}
	if hs[0].FetchPriority != "low" {
		t.Errorf("FetchPriority: got %q", hs[0].FetchPriority)
	}
}

func TestThemeAwareProvider_NoResolverOutputNoHints(t *testing.T) {
	resolver := ThemeStyleResolverFunc(func(r *http.Request) (string, []Hint) {
		return "", nil // theme not seeded
	})
	p := NewThemeAwareProvider(resolver, ThemeAwareOptions{})
	hs, _ := p.HintsFor(htmlReq("/"))
	if hs != nil {
		t.Errorf("want nil hints, got %+v", hs)
	}
}

func TestHasFileExtension(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"/", false},
		{"/foo", false},
		{"/foo/bar", false},
		{"/foo.css", true},
		{"/foo/bar.js", true},
		{"/foo.bar/baz", false},
		{"/foo/.", false},
		{"/foo/..", false},
	}
	for _, tc := range cases {
		if got := hasFileExtension(tc.in); got != tc.want {
			t.Errorf("%q: got %v want %v", tc.in, got, tc.want)
		}
	}
}

func htmlReq(path string) *http.Request {
	r := httptest.NewRequest("GET", path, nil)
	r.Header.Set("Accept", "text/html,application/xhtml+xml")
	return r
}
