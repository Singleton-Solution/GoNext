package search

import (
	"strings"
	"testing"
)

// TestHighlight_EscapesBeforeWrapping is the safety contract: a hit
// whose title contains real HTML must come back with that HTML
// escaped, with only the <mark> tokens left as live tags. This is
// the property that lets consumers drop ExcerptHTML straight into a
// template without re-escaping.
func TestHighlight_EscapesBeforeWrapping(t *testing.T) {
	out := Highlight(`<script>alert("xss")</script> rocks`, []string{"rocks"})

	// The escaped script tag must still be present...
	if !strings.Contains(out, "&lt;script&gt;") {
		t.Errorf("output missing escaped <script>: %q", out)
	}
	// ...and there should be no real <script> tag.
	if strings.Contains(strings.ToLower(out), "<script>") {
		t.Errorf("output contains live <script> tag: %q", out)
	}
	// The match should be wrapped.
	if !strings.Contains(out, "<mark>rocks</mark>") {
		t.Errorf("output missing <mark>rocks</mark>: %q", out)
	}
}

// TestHighlight_WrapsTermCaseInsensitive verifies the matcher
// preserves the source casing in the output even when the term
// supplied is a different case.
func TestHighlight_WrapsTermCaseInsensitive(t *testing.T) {
	out := Highlight("Go is great", []string{"go"})

	if !strings.Contains(out, "<mark>Go</mark>") {
		t.Errorf("expected <mark>Go</mark>, got %q", out)
	}
}

// TestHighlight_MultipleTermsAndDedupe ensures multiple terms work
// and that ["go", "Go"] is deduped so we don't apply the wrap twice.
func TestHighlight_MultipleTermsAndDedupe(t *testing.T) {
	out := Highlight("Go programming", []string{"go", "Go", "programming"})

	if c := strings.Count(out, "<mark>"); c != 2 {
		t.Errorf("expected 2 <mark> openings, got %d: %q", c, out)
	}
	if !strings.Contains(out, "<mark>programming</mark>") {
		t.Errorf("expected programming to be wrapped: %q", out)
	}
}

// TestHighlight_EmptyAndZeroValueInputs documents the no-op paths.
func TestHighlight_EmptyAndZeroValueInputs(t *testing.T) {
	if out := Highlight("", []string{"go"}); out != "" {
		t.Errorf("empty input must return empty, got %q", out)
	}
	if out := Highlight("hello", nil); out != "hello" {
		t.Errorf("nil terms must return escaped input, got %q", out)
	}
	if out := Highlight("a&b", []string{}); out != "a&amp;b" {
		t.Errorf("empty terms must still escape, got %q", out)
	}
}

// TestHighlight_DoesNotMatchInsideWord exercises the word-boundary
// rule: "post" must not wrap "compost".
func TestHighlight_DoesNotMatchInsideWord(t *testing.T) {
	out := Highlight("compost happens", []string{"post"})

	if strings.Contains(out, "<mark>") {
		t.Errorf("expected no wrap inside compost, got %q", out)
	}
}

// TestHighlight_PrefixMatchAcrossWords confirms a prefix match
// works at word starts ("post" matches "posting" but not "compost").
func TestHighlight_PrefixMatchAcrossWords(t *testing.T) {
	out := Highlight("posting is hard, compost is easy", []string{"post"})

	if !strings.Contains(out, "<mark>posting</mark>") {
		t.Errorf("expected <mark>posting</mark>, got %q", out)
	}
	if strings.Contains(out, "<mark>compost") {
		t.Errorf("compost must not be wrapped, got %q", out)
	}
}

// TestHighlight_AmpersandInTextStaysSafe is a regression: an
// ampersand in source text must not break the word-boundary walker.
func TestHighlight_AmpersandInTextStaysSafe(t *testing.T) {
	out := Highlight("Q&A about go", []string{"go"})

	if !strings.Contains(out, "<mark>go</mark>") {
		t.Errorf("expected <mark>go</mark>, got %q", out)
	}
	if !strings.Contains(out, "&amp;") {
		t.Errorf("ampersand must remain escaped, got %q", out)
	}
}

// TestTokenize_SplitsAndDedupes covers the helper that turns the
// query string into wrap terms.
func TestTokenize_SplitsAndDedupes(t *testing.T) {
	got := tokenize("Go go programming! go")

	if len(got) != 2 {
		t.Fatalf("want 2 unique tokens, got %d: %#v", len(got), got)
	}
	if got[0] != "Go" || got[1] != "programming" {
		t.Errorf("unexpected token list: %#v", got)
	}
}

// TestTokenize_RejectsPunctuation makes sure SQL-shaped junk is
// stripped before highlighting — the rendered output should never
// contain a literal apostrophe-quote from a malicious term.
func TestTokenize_RejectsPunctuation(t *testing.T) {
	got := tokenize(`'; DROP TABLE posts; --`)

	for _, tok := range got {
		if strings.ContainsAny(tok, `';-`) {
			t.Errorf("tokenize leaked punctuation: %q", tok)
		}
	}
}
