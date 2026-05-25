package render

import (
	"strings"
	"testing"
)

// Tests for the core renderers. Each fixture is a tiny JSON-shaped
// block, run through Walk against a registry seeded by
// RegisterCoreBlocks. We assert against canonical HTML; subtle
// formatting differences (e.g. self-closing vs not) follow the TS
// `save()` output in packages/ts/blocks-core/src/*/save.ts.

func walkOne(t *testing.T, blockType string, attrs map[string]any, inner []Block) string {
	t.Helper()
	reg := NewRegistry()
	MustRegisterCoreBlocks(reg)
	w := New(reg)
	res := w.Walk(BlockTree{
		{Type: blockType, Attributes: attrs, InnerBlocks: inner},
	}, nil)
	if len(res.Errors) != 0 {
		t.Fatalf("unexpected errors for %s: %v", blockType, res.Errors)
	}
	return string(res.HTML)
}

func TestCoreRegister_AllSixteenSeeded(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	if err := RegisterCoreBlocks(reg); err != nil {
		t.Fatalf("RegisterCoreBlocks: %v", err)
	}
	want := []string{
		"core/paragraph", "core/heading", "core/list", "core/image",
		"core/quote", "core/code", "core/separator", "core/spacer",
		"core/columns", "core/group", "core/table", "core/gallery",
		"core/video", "core/button", "core/file", "core/embed",
	}
	if reg.Len() != len(want) {
		t.Fatalf("registered %d block types, want %d", reg.Len(), len(want))
	}
	for _, n := range want {
		if !reg.Has(n) {
			t.Fatalf("missing core block: %s", n)
		}
	}
}

func TestCoreRegister_DuplicateRejected(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	MustRegisterCoreBlocks(reg)
	if err := RegisterCoreBlocks(reg); err == nil {
		t.Fatal("expected duplicate-registration error on second call")
	}
}

func TestParagraph_Basic(t *testing.T) {
	t.Parallel()
	got := walkOne(t, "core/paragraph", map[string]any{
		"content": "Hello, world.",
	}, nil)
	if got != `<p class="gn-block-paragraph">Hello, world.</p>` {
		t.Fatalf("got %q", got)
	}
}

func TestParagraph_EscapesHTML(t *testing.T) {
	t.Parallel()
	got := walkOne(t, "core/paragraph", map[string]any{
		"content": `<script>alert("x")</script>`,
	}, nil)
	if strings.Contains(got, "<script>") {
		t.Fatalf("script not escaped: %q", got)
	}
	if !strings.Contains(got, "&lt;script&gt;") {
		t.Fatalf("expected escaped script: %q", got)
	}
}

func TestParagraph_AlignAndDropCap(t *testing.T) {
	t.Parallel()
	got := walkOne(t, "core/paragraph", map[string]any{
		"content": "hi",
		"align":   "center",
		"dropCap": true,
	}, nil)
	if !strings.Contains(got, "has-text-align-center") {
		t.Fatalf("missing align class: %q", got)
	}
	if !strings.Contains(got, "has-drop-cap") {
		t.Fatalf("missing drop-cap class: %q", got)
	}
}

func TestHeading_Level(t *testing.T) {
	t.Parallel()
	got := walkOne(t, "core/heading", map[string]any{
		"content": "Section",
		"level":   float64(3),
	}, nil)
	if !strings.HasPrefix(got, "<h3") {
		t.Fatalf("expected <h3 prefix, got %q", got)
	}
	if !strings.HasSuffix(got, "</h3>") {
		t.Fatalf("expected </h3> suffix, got %q", got)
	}
	if !strings.Contains(got, "gn-block-heading--level-3") {
		t.Fatalf("missing level class: %q", got)
	}
}

func TestHeading_AnchorIsId(t *testing.T) {
	t.Parallel()
	got := walkOne(t, "core/heading", map[string]any{
		"content": "Section",
		"level":   float64(2),
		"anchor":  "intro",
	}, nil)
	if !strings.Contains(got, `id="intro"`) {
		t.Fatalf("missing id=\"intro\": %q", got)
	}
}

func TestHeading_DefaultsToLevel2(t *testing.T) {
	t.Parallel()
	got := walkOne(t, "core/heading", map[string]any{
		"content": "Default",
	}, nil)
	if !strings.HasPrefix(got, "<h2") {
		t.Fatalf("expected default h2, got %q", got)
	}
}

func TestList_Unordered(t *testing.T) {
	t.Parallel()
	got := walkOne(t, "core/list", map[string]any{
		"values": []any{"one", "two"},
	}, nil)
	if !strings.HasPrefix(got, "<ul") {
		t.Fatalf("expected <ul prefix: %q", got)
	}
	if !strings.Contains(got, "<li>one</li><li>two</li>") {
		t.Fatalf("missing items: %q", got)
	}
}

func TestList_Ordered(t *testing.T) {
	t.Parallel()
	got := walkOne(t, "core/list", map[string]any{
		"values":  []any{"a"},
		"ordered": true,
	}, nil)
	if !strings.HasPrefix(got, "<ol") {
		t.Fatalf("expected <ol prefix: %q", got)
	}
}

func TestImage_Basic(t *testing.T) {
	t.Parallel()
	got := walkOne(t, "core/image", map[string]any{
		"src":     "https://example.com/x.jpg",
		"alt":     "An x",
		"caption": "A cap",
	}, nil)
	if !strings.Contains(got, `<img src="https://example.com/x.jpg"`) {
		t.Fatalf("missing img src: %q", got)
	}
	if !strings.Contains(got, `alt="An x"`) {
		t.Fatalf("missing alt: %q", got)
	}
	if !strings.Contains(got, "<figcaption>A cap</figcaption>") {
		t.Fatalf("missing caption: %q", got)
	}
}

func TestImage_EmptySrcEmitsEmptyFigure(t *testing.T) {
	t.Parallel()
	got := walkOne(t, "core/image", map[string]any{}, nil)
	if !strings.Contains(got, "gn-block-image--empty") {
		t.Fatalf("expected empty marker: %q", got)
	}
}

func TestQuote_WithCitation(t *testing.T) {
	t.Parallel()
	got := walkOne(t, "core/quote", map[string]any{
		"content":  "Truth.",
		"citation": "Author",
	}, nil)
	if !strings.Contains(got, "<blockquote") {
		t.Fatalf("missing blockquote: %q", got)
	}
	if !strings.Contains(got, "<cite>Author</cite>") {
		t.Fatalf("missing cite: %q", got)
	}
}

func TestCode_Language(t *testing.T) {
	t.Parallel()
	got := walkOne(t, "core/code", map[string]any{
		"content":  "x := 1",
		"language": "go",
	}, nil)
	if !strings.Contains(got, "language-go") {
		t.Fatalf("missing language class: %q", got)
	}
	if !strings.Contains(got, "<pre") || !strings.Contains(got, "<code>") {
		t.Fatalf("missing pre/code wrappers: %q", got)
	}
}

func TestSeparator_Style(t *testing.T) {
	t.Parallel()
	got := walkOne(t, "core/separator", map[string]any{
		"style": "wide",
	}, nil)
	if !strings.Contains(got, "gn-block-separator--wide") {
		t.Fatalf("missing style class: %q", got)
	}
	if !strings.Contains(got, "<hr") {
		t.Fatalf("expected hr: %q", got)
	}
}

func TestSpacer_Height(t *testing.T) {
	t.Parallel()
	got := walkOne(t, "core/spacer", map[string]any{
		"height": float64(40),
	}, nil)
	if !strings.Contains(got, "height:40px") {
		t.Fatalf("missing height: %q", got)
	}
}

func TestColumns_WrapsInner(t *testing.T) {
	t.Parallel()
	got := walkOne(t, "core/columns", map[string]any{
		"columns": float64(2),
	}, []Block{
		{Type: "core/paragraph", Attributes: map[string]any{"content": "hi"}},
	})
	if !strings.Contains(got, "gn-block-columns--cols-2") {
		t.Fatalf("missing cols class: %q", got)
	}
	if !strings.Contains(got, "Hello") && !strings.Contains(got, "<p ") {
		// The inner paragraph must appear; we look for <p
		// rather than the exact text.
		t.Fatalf("expected inner paragraph rendered: %q", got)
	}
}

func TestColumns_VerticalAlignment(t *testing.T) {
	t.Parallel()
	got := walkOne(t, "core/columns", map[string]any{
		"columns":           float64(3),
		"verticalAlignment": "center",
	}, nil)
	if !strings.Contains(got, "is-vertically-aligned-center") {
		t.Fatalf("missing alignment class: %q", got)
	}
}

func TestGroup_WrapsInnerWithChosenTag(t *testing.T) {
	t.Parallel()
	got := walkOne(t, "core/group", map[string]any{
		"tagName": "section",
	}, []Block{
		{Type: "core/heading", Attributes: map[string]any{"content": "H", "level": float64(2)}},
	})
	if !strings.HasPrefix(got, "<section") {
		t.Fatalf("expected section prefix: %q", got)
	}
	if !strings.HasSuffix(got, "</section>") {
		t.Fatalf("expected /section suffix: %q", got)
	}
}

func TestGroup_DisallowedTagFallsBackToDiv(t *testing.T) {
	t.Parallel()
	got := walkOne(t, "core/group", map[string]any{
		"tagName": "script", // not in allowlist
	}, nil)
	if !strings.HasPrefix(got, "<div") {
		t.Fatalf("expected div fallback: %q", got)
	}
}

func TestTable_BodyRowsAndCaption(t *testing.T) {
	t.Parallel()
	got := walkOne(t, "core/table", map[string]any{
		"caption": "Pricing",
		"body": []any{
			[]any{"A", "B"},
			[]any{"C", "D"},
		},
	}, nil)
	if !strings.Contains(got, "<caption>Pricing</caption>") {
		t.Fatalf("missing caption: %q", got)
	}
	if !strings.Contains(got, "<td>A</td><td>B</td>") {
		t.Fatalf("missing first row: %q", got)
	}
}

func TestGallery_Items(t *testing.T) {
	t.Parallel()
	got := walkOne(t, "core/gallery", map[string]any{
		"columns": float64(2),
		"images": []any{
			map[string]any{"src": "https://example.com/1.jpg", "alt": "one"},
			map[string]any{"src": "https://example.com/2.jpg", "alt": "two"},
		},
	}, nil)
	if !strings.Contains(got, "gn-block-gallery--cols-2") {
		t.Fatalf("missing cols: %q", got)
	}
	if strings.Count(got, "<img ") != 2 {
		t.Fatalf("expected 2 imgs, got %q", got)
	}
}

func TestVideo_FlagsApplied(t *testing.T) {
	t.Parallel()
	got := walkOne(t, "core/video", map[string]any{
		"src":      "https://example.com/x.mp4",
		"autoplay": true,
		"muted":    true,
		"loop":     true,
	}, nil)
	for _, f := range []string{"autoplay", "muted", "loop"} {
		if !strings.Contains(got, f) {
			t.Fatalf("missing %s flag: %q", f, got)
		}
	}
}

func TestButton_RendersAnchor(t *testing.T) {
	t.Parallel()
	got := walkOne(t, "core/button", map[string]any{
		"text":  "Click",
		"url":   "https://example.com/",
		"style": "primary",
	}, nil)
	if !strings.Contains(got, `<a href="https://example.com/"`) {
		t.Fatalf("missing href: %q", got)
	}
	if !strings.Contains(got, "gn-block-button--primary") {
		t.Fatalf("missing style class: %q", got)
	}
	if !strings.Contains(got, ">Click</a>") {
		t.Fatalf("missing text: %q", got)
	}
}

func TestFile_RendersDownloadLink(t *testing.T) {
	t.Parallel()
	got := walkOne(t, "core/file", map[string]any{
		"href":     "https://example.com/x.pdf",
		"fileName": "x.pdf",
	}, nil)
	if !strings.Contains(got, "download") {
		t.Fatalf("missing download attr: %q", got)
	}
	if !strings.Contains(got, "x.pdf") {
		t.Fatalf("missing fileName: %q", got)
	}
}

func TestEmbed_Wraps(t *testing.T) {
	t.Parallel()
	got := walkOne(t, "core/embed", map[string]any{
		"provider": "youtube",
		"url":      "https://youtube.com/watch?v=x",
	}, nil)
	if !strings.Contains(got, "gn-block-embed--youtube") {
		t.Fatalf("missing provider class: %q", got)
	}
}

func TestCoreRenderers_NestedColumnsWithHeadingAndParagraph(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	MustRegisterCoreBlocks(reg)
	w := New(reg)
	res := w.Walk(BlockTree{
		{
			Type:       "core/columns",
			Attributes: map[string]any{"columns": float64(2)},
			InnerBlocks: []Block{
				{Type: "core/heading", Attributes: map[string]any{"content": "Hello", "level": float64(2)}},
				{Type: "core/paragraph", Attributes: map[string]any{"content": "world"}},
			},
		},
	}, nil)
	if len(res.Errors) != 0 {
		t.Fatalf("errors: %v", res.Errors)
	}
	html := string(res.HTML)
	if !strings.Contains(html, "<h2") {
		t.Fatalf("missing h2: %q", html)
	}
	if !strings.Contains(html, "<p ") {
		t.Fatalf("missing p: %q", html)
	}
	if !strings.Contains(html, "gn-block-columns--cols-2") {
		t.Fatalf("missing columns class: %q", html)
	}
}
