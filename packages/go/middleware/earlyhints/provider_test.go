package earlyhints

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHint_LinkHeader(t *testing.T) {
	cases := []struct {
		name string
		in   Hint
		want string
	}{
		{
			name: "url only",
			in:   Hint{URL: "/x.css"},
			want: "</x.css>; rel=preload",
		},
		{
			name: "with as",
			in:   Hint{URL: "/x.css", As: "style"},
			want: "</x.css>; rel=preload; as=style",
		},
		{
			name: "anonymous crossorigin",
			in:   Hint{URL: "/x.woff2", As: "font", CrossOrigin: "anonymous"},
			want: "</x.woff2>; rel=preload; as=font; crossorigin",
		},
		{
			name: "use-credentials crossorigin",
			in:   Hint{URL: "/x", As: "fetch", CrossOrigin: "use-credentials"},
			want: "</x>; rel=preload; as=fetch; crossorigin=use-credentials",
		},
		{
			name: "fetchpriority high",
			in:   Hint{URL: "/x.css", As: "style", FetchPriority: "high"},
			want: "</x.css>; rel=preload; as=style; fetchpriority=high",
		},
		{
			name: "empty url returns empty",
			in:   Hint{URL: "", As: "style"},
			want: "",
		},
		{
			name: "all fields",
			in: Hint{
				URL:           "https://cdn.example.com/x.woff2",
				As:            "font",
				CrossOrigin:   "anonymous",
				FetchPriority: "high",
			},
			want: "<https://cdn.example.com/x.woff2>; rel=preload; as=font; crossorigin; fetchpriority=high",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.in.linkHeader()
			if got != tc.want {
				t.Errorf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestStaticProvider_HintsFor(t *testing.T) {
	p := NewStaticProvider(map[string][]Hint{
		"/":      {{URL: "/home.css", As: "style"}},
		"/about": {{URL: "/about.css", As: "style"}},
	})

	t.Run("known path returns hints", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		hs, err := p.HintsFor(req)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(hs) != 1 || hs[0].URL != "/home.css" {
			t.Errorf("unexpected hints: %+v", hs)
		}
	})

	t.Run("unknown path returns nil", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/missing", nil)
		hs, err := p.HintsFor(req)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if hs != nil {
			t.Errorf("expected nil, got %+v", hs)
		}
	})
}

func TestStaticProvider_NilSafeAndEmpty(t *testing.T) {
	var p *StaticProvider
	req := httptest.NewRequest("GET", "/", nil)
	hs, err := p.HintsFor(req)
	if err != nil || hs != nil {
		t.Errorf("nil provider: hs=%v err=%v", hs, err)
	}

	p2 := NewStaticProvider(nil)
	hs, err = p2.HintsFor(req)
	if err != nil || hs != nil {
		t.Errorf("empty provider: hs=%v err=%v", hs, err)
	}

	p3 := NewStaticProvider(map[string][]Hint{})
	hs, err = p3.HintsFor(req)
	if err != nil || hs != nil {
		t.Errorf("zero-entry provider: hs=%v err=%v", hs, err)
	}
}

func TestStaticProvider_DefensivelyCopiesInput(t *testing.T) {
	input := map[string][]Hint{
		"/": {{URL: "/orig.css", As: "style"}},
	}
	p := NewStaticProvider(input)
	// Mutate input post-construction.
	input["/"] = []Hint{{URL: "/mutated.css", As: "style"}}

	req := httptest.NewRequest("GET", "/", nil)
	hs, _ := p.HintsFor(req)
	if len(hs) != 1 || hs[0].URL != "/orig.css" {
		t.Errorf("provider should snapshot input; got %+v", hs)
	}
}

func TestHintsProviderFunc_Adapter(t *testing.T) {
	called := false
	wantURL := "/from-closure.css"
	var p HintsProvider = HintsProviderFunc(func(r *http.Request) ([]Hint, error) {
		called = true
		if r == nil {
			t.Error("adapter received nil request")
		}
		return []Hint{{URL: wantURL, As: "style"}}, nil
	})
	hs, err := p.HintsFor(httptest.NewRequest("GET", "/", nil))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !called {
		t.Error("adapter did not invoke closure")
	}
	if len(hs) != 1 || hs[0].URL != wantURL {
		t.Errorf("got %+v want one hint for %q", hs, wantURL)
	}
}

func TestValidatePreloadAs(t *testing.T) {
	known := []string{"style", "script", "font", "image", "fetch", "audio", "video"}
	for _, k := range known {
		if !validatePreloadAs(k) {
			t.Errorf("%q should be valid", k)
		}
	}
	if validatePreloadAs("bogus") {
		t.Error(`"bogus" should not be valid`)
	}
	if !validatePreloadAs("") {
		t.Error("empty string should be valid (means no as= emitted)")
	}
}

func TestBudgetReached(t *testing.T) {
	if budgetReached(0, 100, 50) != true {
		t.Error("100 over 50 should trigger")
	}
	if budgetReached(0, 50, 100) != false {
		t.Error("50 under 100 should pass")
	}
	// Default budget when maxBytes <= 0.
	if budgetReached(0, 100, 0) != false {
		t.Error("with maxBytes=0 (defaulting to 8KiB), 100 bytes should pass")
	}
	if budgetReached(0, 9000, 0) != true {
		t.Error("with maxBytes=0 (defaulting to 8KiB), 9000 bytes should trigger")
	}
}

func TestLinkHeader_Stable(t *testing.T) {
	h := Hint{URL: "/static/x.css", As: "style"}
	got := h.linkHeader()
	if !strings.HasPrefix(got, "</static/x.css>; rel=preload") {
		t.Errorf("link header prefix mismatch: %q", got)
	}
}
