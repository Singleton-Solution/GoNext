package html2blocks

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// updateGolden, when set, rewrites the golden file from a test run.
// `go test ./migrate/html2blocks -update` regenerates the snapshot when
// the converter's output legitimately changes. Required for any change
// to the realistic-post tree; gates against accidental rot.
var updateGolden = flag.Bool("update", false, "rewrite testdata golden files")

// TestPlainParagraph covers the smallest possible input — a single
// `<p>` becoming a single core/paragraph with the correct content.
func TestPlainParagraph(t *testing.T) {
	got, err := Convert([]byte(`<p>Hello, world.</p>`))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 block, got %d: %#v", len(got), got)
	}
	if got[0].Name != BlockParagraph {
		t.Errorf("expected %q, got %q", BlockParagraph, got[0].Name)
	}
	if got[0].Attrs["content"] != "Hello, world." {
		t.Errorf("content = %q", got[0].Attrs["content"])
	}
}

// TestHeadingsBecomeHeadingBlocks ensures h1..h6 map to core/heading
// with a `level` attribute carrying the numeric rank.
func TestHeadingsBecomeHeadingBlocks(t *testing.T) {
	got, err := Convert([]byte(`<h1>foo</h1><h2>bar</h2>`))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 blocks, got %d: %#v", len(got), got)
	}
	for i, want := range []struct {
		content string
		level   int
	}{
		{"foo", 1},
		{"bar", 2},
	} {
		if got[i].Name != BlockHeading {
			t.Errorf("block %d: name = %q", i, got[i].Name)
		}
		if got[i].Attrs["content"] != want.content {
			t.Errorf("block %d: content = %q", i, got[i].Attrs["content"])
		}
		if got[i].Attrs["level"] != want.level {
			t.Errorf("block %d: level = %v (want %d)", i, got[i].Attrs["level"], want.level)
		}
	}
}

// TestNestedBlockquoteHasInnerParagraph verifies that a blockquote
// wrapping a paragraph emits a core/quote with the paragraph as an
// inner block, and that the quote's textual `value` mirrors the
// paragraph content.
func TestNestedBlockquoteHasInnerParagraph(t *testing.T) {
	got, err := Convert([]byte(`<blockquote><p>x</p></blockquote>`))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(got) != 1 || got[0].Name != BlockQuote {
		t.Fatalf("expected single quote, got %#v", got)
	}
	if len(got[0].InnerBlocks) != 1 || got[0].InnerBlocks[0].Name != BlockParagraph {
		t.Fatalf("expected inner paragraph, got %#v", got[0].InnerBlocks)
	}
	if got[0].InnerBlocks[0].Attrs["content"] != "x" {
		t.Errorf("inner paragraph content = %q", got[0].InnerBlocks[0].Attrs["content"])
	}
}

// TestGutenbergImageComment verifies the comment-delimited path: a
// `<!-- wp:image {"id":42} -->` wrapper should land in attrs["id"] and
// the inner `<img>` should hydrate the url/alt fields.
func TestGutenbergImageComment(t *testing.T) {
	in := `<!-- wp:image {"id":42} --><figure class="wp-block-image"><img src="https://example.com/x.png" alt="hello"/></figure><!-- /wp:image -->`
	got, err := Convert([]byte(in))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(got) != 1 || got[0].Name != BlockImage {
		t.Fatalf("expected core/image, got %#v", got)
	}
	// JSON unmarshalling yields float64 for numbers; the comment's
	// {"id":42} therefore comes through as float64(42).
	if got[0].Attrs["id"] != float64(42) {
		t.Errorf("id = %v (%T)", got[0].Attrs["id"], got[0].Attrs["id"])
	}
	if got[0].Attrs["url"] != "https://example.com/x.png" {
		t.Errorf("url = %v", got[0].Attrs["url"])
	}
	if got[0].Attrs["alt"] != "hello" {
		t.Errorf("alt = %v", got[0].Attrs["alt"])
	}
}

// TestListUnordered verifies a `<ul>` produces core/list with the
// items captured in `values` and `ordered=false`.
func TestListUnordered(t *testing.T) {
	got, err := Convert([]byte(`<ul><li>a</li><li>b</li></ul>`))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(got) != 1 || got[0].Name != BlockList {
		t.Fatalf("expected core/list, got %#v", got)
	}
	if got[0].Attrs["ordered"] != false {
		t.Errorf("ordered = %v", got[0].Attrs["ordered"])
	}
	values, ok := got[0].Attrs["values"].([]string)
	if !ok {
		t.Fatalf("values = %v (%T)", got[0].Attrs["values"], got[0].Attrs["values"])
	}
	if !reflect.DeepEqual(values, []string{"a", "b"}) {
		t.Errorf("values = %v", values)
	}
}

// TestCodeBlock verifies a `<pre><code>` lands as core/code with the
// content preserved verbatim and a language hint extracted from the
// `language-go` className.
func TestCodeBlock(t *testing.T) {
	got, err := Convert([]byte(`<pre><code class="language-go">x</code></pre>`))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(got) != 1 || got[0].Name != BlockCode {
		t.Fatalf("expected core/code, got %#v", got)
	}
	if got[0].Attrs["content"] != "x" {
		t.Errorf("content = %v", got[0].Attrs["content"])
	}
	if got[0].Attrs["language"] != "go" {
		t.Errorf("language = %v", got[0].Attrs["language"])
	}
}

// TestUnknownTagFallsBack verifies an unrecognised element (here
// `<marquee>`) routes to the paragraph fallback with raw HTML in
// `content`. This is the lossy-but-honest contract — bytes survive.
func TestUnknownTagFallsBack(t *testing.T) {
	got, err := Convert([]byte(`<marquee>scroll</marquee>`))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(got) != 1 || got[0].Name != BlockParagraph {
		t.Fatalf("expected paragraph fallback, got %#v", got)
	}
	content, _ := got[0].Attrs["content"].(string)
	if !strings.Contains(content, "<marquee>") || !strings.Contains(content, "scroll") {
		t.Errorf("expected raw HTML in fallback content, got %q", content)
	}
}

// TestWhitespaceOnlyParagraphSkipped guards against empty blocks
// polluting the tree when WP emits stray `<p> </p>` for spacing.
func TestWhitespaceOnlyParagraphSkipped(t *testing.T) {
	got, err := Convert([]byte(`<p>real</p><p>   </p><p>also real</p>`))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 blocks (no empty middle), got %d: %#v", len(got), got)
	}
	if got[0].Attrs["content"] != "real" || got[1].Attrs["content"] != "also real" {
		t.Errorf("unexpected content: %#v", got)
	}
}

// TestSeparator verifies `<hr/>` round-trips as core/separator.
func TestSeparator(t *testing.T) {
	got, err := Convert([]byte(`<hr/>`))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(got) != 1 || got[0].Name != BlockSeparator {
		t.Fatalf("expected core/separator, got %#v", got)
	}
}

// TestEmptyInput verifies an empty body produces an empty (non-nil)
// slice — callers shouldn't have to nil-guard the result.
func TestEmptyInput(t *testing.T) {
	got, err := Convert(nil)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if got == nil {
		t.Fatalf("expected non-nil slice")
	}
	if len(got) != 0 {
		t.Fatalf("expected empty slice, got %#v", got)
	}
}

// TestRealisticPostSnapshot guards against accidental regression on a
// representative 1KB-ish WP post body. Update with `-update` after a
// reviewed intentional change to the converter output.
func TestRealisticPostSnapshot(t *testing.T) {
	in, err := os.ReadFile(filepath.Join("testdata", "realistic_post.html"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	got, err := Convert(in)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	// Canonicalise as indented JSON so a human reading the golden
	// file can spot diffs at a glance.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(got); err != nil {
		t.Fatalf("encode: %v", err)
	}
	goldenPath := filepath.Join("testdata", "realistic_post.golden.json")
	if *updateGolden {
		if err := os.WriteFile(goldenPath, buf.Bytes(), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden (run with -update first?): %v", err)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("snapshot mismatch — re-run with -update if intentional\nGOT:\n%s\nWANT:\n%s", buf.String(), string(want))
	}
}
