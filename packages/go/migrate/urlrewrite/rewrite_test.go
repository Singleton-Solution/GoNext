package urlrewrite

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

// fixtureMap is a small media map used by most tests.
func fixtureMap() map[string]MediaRef {
	return map[string]MediaRef{
		"https://old.example.com/wp-content/uploads/2024/03/a.jpg": {
			ID:  uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			URL: "https://cdn.gonext.example/m/a.jpg",
		},
		"https://old.example.com/wp-content/uploads/2024/03/b.png": {
			ID:  uuid.MustParse("22222222-2222-2222-2222-222222222222"),
			URL: "https://cdn.gonext.example/m/b.png",
		},
		"https://old.example.com/wp-content/uploads/2024/03/v.mp4": {
			URL: "https://cdn.gonext.example/m/v.mp4",
		},
		// Path-only key (used by LegacyHosts test).
		"/wp-content/uploads/2024/03/c.jpg": {
			URL: "https://cdn.gonext.example/m/c.jpg",
		},
	}
}

func TestRewrite_ImgSrcAndHref(t *testing.T) {
	r := New(Options{Map: fixtureMap()})
	in := `<p>See <a href="https://old.example.com/wp-content/uploads/2024/03/a.jpg">photo</a> and ` +
		`<img alt="not a url" src="https://old.example.com/wp-content/uploads/2024/03/b.png" /></p>`
	out, n := r.RewriteString(in)
	if n != 2 {
		t.Errorf("count: got %d want 2", n)
	}
	if !strings.Contains(out, `href="https://cdn.gonext.example/m/a.jpg"`) {
		t.Errorf("href not rewritten: %s", out)
	}
	if !strings.Contains(out, `src="https://cdn.gonext.example/m/b.png"`) {
		t.Errorf("src not rewritten: %s", out)
	}
	// alt unchanged.
	if !strings.Contains(out, `alt="not a url"`) {
		t.Errorf("alt mutated: %s", out)
	}
}

func TestRewrite_VideoAndAudioSrc(t *testing.T) {
	r := New(Options{Map: fixtureMap()})
	in := `<video src="https://old.example.com/wp-content/uploads/2024/03/v.mp4" poster="https://old.example.com/wp-content/uploads/2024/03/a.jpg" controls></video>`
	out, n := r.RewriteString(in)
	if n != 2 {
		t.Errorf("count: got %d want 2", n)
	}
	if !strings.Contains(out, `src="https://cdn.gonext.example/m/v.mp4"`) {
		t.Errorf("video src not rewritten: %s", out)
	}
	if !strings.Contains(out, `poster="https://cdn.gonext.example/m/a.jpg"`) {
		t.Errorf("poster not rewritten: %s", out)
	}
}

func TestRewrite_Srcset(t *testing.T) {
	r := New(Options{Map: fixtureMap()})
	in := `<img srcset="https://old.example.com/wp-content/uploads/2024/03/a.jpg 1x, https://old.example.com/wp-content/uploads/2024/03/b.png 2x" src="x">`
	out, n := r.RewriteString(in)
	if n != 2 {
		t.Errorf("count: got %d want 2", n)
	}
	if !strings.Contains(out, "https://cdn.gonext.example/m/a.jpg 1x") {
		t.Errorf("srcset 1x not rewritten: %s", out)
	}
	if !strings.Contains(out, "https://cdn.gonext.example/m/b.png 2x") {
		t.Errorf("srcset 2x not rewritten: %s", out)
	}
}

func TestRewrite_StyleUrl(t *testing.T) {
	r := New(Options{Map: fixtureMap()})
	in := `<div style="background:url(https://old.example.com/wp-content/uploads/2024/03/a.jpg) center">`
	out, n := r.RewriteString(in)
	if n != 1 {
		t.Errorf("count: got %d want 1", n)
	}
	if !strings.Contains(out, "url(https://cdn.gonext.example/m/a.jpg)") {
		t.Errorf("url() not rewritten: %s", out)
	}
}

func TestRewrite_StyleUrlQuoted(t *testing.T) {
	r := New(Options{Map: fixtureMap()})
	in := `<div style="background:url('https://old.example.com/wp-content/uploads/2024/03/b.png')">`
	out, _ := r.RewriteString(in)
	// Quoting preserved (single quote inside, double quote attr).
	if !strings.Contains(out, `url('https://cdn.gonext.example/m/b.png')`) {
		t.Errorf("quoted url() not rewritten with quotes preserved: %s", out)
	}
}

func TestRewrite_LegacyHostPathFallback(t *testing.T) {
	r := New(Options{
		Map:         fixtureMap(),
		LegacyHosts: []string{"staging.example.com"},
	})
	in := `<img src="https://staging.example.com/wp-content/uploads/2024/03/c.jpg">`
	out, n := r.RewriteString(in)
	if n != 1 {
		t.Errorf("count: got %d want 1 (path-only fallback)", n)
	}
	if !strings.Contains(out, `src="https://cdn.gonext.example/m/c.jpg"`) {
		t.Errorf("path-only key not matched: %s", out)
	}
}

func TestRewrite_NewBaseURLFallback(t *testing.T) {
	// Map is empty for the specific URL, but NewBaseURL kicks in.
	r := New(Options{
		Map:        map[string]MediaRef{},
		NewBaseURL: "https://cdn.gonext.example",
	})
	in := `<img src="https://old.example.com/wp-content/uploads/2024/03/z.jpg">`
	out, n := r.RewriteString(in)
	if n != 1 {
		t.Errorf("count: got %d want 1", n)
	}
	if !strings.Contains(out, "https://cdn.gonext.example/wp-content/uploads/2024/03/z.jpg") {
		t.Errorf("NewBaseURL fallback not applied: %s", out)
	}
}

func TestRewrite_UnknownURLLeftAlone(t *testing.T) {
	r := New(Options{Map: fixtureMap()})
	in := `<img src="https://other.example.com/asset.jpg">`
	out, n := r.RewriteString(in)
	if n != 0 {
		t.Errorf("count: got %d want 0", n)
	}
	if out != in {
		t.Errorf("output mutated for unknown URL: %s", out)
	}
}

func TestRewrite_SingleQuoteAttr(t *testing.T) {
	r := New(Options{Map: fixtureMap()})
	in := `<img src='https://old.example.com/wp-content/uploads/2024/03/a.jpg'>`
	out, _ := r.RewriteString(in)
	if !strings.Contains(out, `src='https://cdn.gonext.example/m/a.jpg'`) {
		t.Errorf("single-quote not preserved: %s", out)
	}
}

func TestRewrite_DataSrc(t *testing.T) {
	// Lazy-load patterns frequently use data-src.
	r := New(Options{Map: fixtureMap()})
	in := `<img data-src="https://old.example.com/wp-content/uploads/2024/03/a.jpg" src="placeholder.gif">`
	out, n := r.RewriteString(in)
	if n != 1 {
		t.Errorf("count: got %d want 1", n)
	}
	if !strings.Contains(out, `data-src="https://cdn.gonext.example/m/a.jpg"`) {
		t.Errorf("data-src not rewritten: %s", out)
	}
}

func TestRewrite_NilRewriter(t *testing.T) {
	var r *Rewriter
	out, n := r.Rewrite([]byte("anything"))
	if out != nil || n != 0 {
		t.Errorf("nil receiver should return (nil, 0); got (%q, %d)", out, n)
	}
}

func TestRewrite_EmptyOptions(t *testing.T) {
	r := New(Options{})
	out, n := r.RewriteString(`<img src="https://old.example.com/wp-content/uploads/2024/03/a.jpg">`)
	if n != 0 {
		t.Errorf("count: got %d want 0", n)
	}
	if out != `<img src="https://old.example.com/wp-content/uploads/2024/03/a.jpg">` {
		t.Errorf("output: %s", out)
	}
}

func TestRewrite_RealPostFixture(t *testing.T) {
	// Real-world post mixing href, img with srcset, video, and a
	// style attr in one paragraph.
	r := New(Options{Map: fixtureMap()})
	in := `<p>Look at this <a href="https://old.example.com/wp-content/uploads/2024/03/a.jpg">photo</a>:</p>
<figure class="wp-block-image">
  <img src="https://old.example.com/wp-content/uploads/2024/03/a.jpg"
       srcset="https://old.example.com/wp-content/uploads/2024/03/a.jpg 1x, https://old.example.com/wp-content/uploads/2024/03/b.png 2x"
       alt="A photo" />
</figure>
<video src="https://old.example.com/wp-content/uploads/2024/03/v.mp4" poster="https://old.example.com/wp-content/uploads/2024/03/a.jpg"></video>
<div style="background: url(https://old.example.com/wp-content/uploads/2024/03/b.png) center / cover;">hero</div>`

	out, n := r.RewriteString(in)
	// Expected: 1 href + 1 img src + 2 srcset + 1 video src + 1 poster + 1 style url = 7
	if n != 7 {
		t.Errorf("count: got %d want 7", n)
	}
	if strings.Contains(out, "old.example.com") {
		t.Errorf("old host still present: %s", out)
	}
	if strings.Count(out, "cdn.gonext.example") != 7 {
		t.Errorf("expected 7 cdn refs, got %d in: %s",
			strings.Count(out, "cdn.gonext.example"), out)
	}
}

func TestSplitOnceWS(t *testing.T) {
	a, b, ok := splitOnceWS("url descriptor")
	if !ok || a != "url" || b != "descriptor" {
		t.Errorf("split: got (%q,%q,%v)", a, b, ok)
	}
	a, b, ok = splitOnceWS("just-url")
	if ok || a != "just-url" || b != "" {
		t.Errorf("no-split: got (%q,%q,%v)", a, b, ok)
	}
}
