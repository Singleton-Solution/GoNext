package comments

import (
	"strings"
	"unicode"
)

// sanitizeContent strips HTML tags from a user-submitted comment
// body. Replaces the bluemonday UGC profile (issue #284) for now;
// when the real sanitiser lands the surface stays the same.
//
// Strategy: this is a hand-rolled HTML-tag stripper, not a parser.
// It treats anything between '<' and '>' (inclusive) as a tag and
// drops it. The leftover text is then HTML-escaped so an attacker
// can't smuggle a tag-shaped string through (e.g. "foo<script>bar")
// and have it interpreted as HTML on a downstream renderer.
//
// We accept a small false-positive rate: a literal "<" in prose
// will be dropped along with the next ">" character (or until EOF
// if there isn't one). The error mode is conservative — text loss,
// not script execution — which is the right trade for a comment box.
//
// Multiple consecutive whitespace runs collapse to a single space
// because tag-stripping leaves leading/trailing spaces and double-
// newlines that look ugly when re-rendered.
func sanitizeContent(raw string) string {
	stripped := stripTags(raw)
	escaped := htmlEscape(stripped)
	return collapseWhitespace(escaped)
}

// stripTags drops <...> sequences. The simplest correct implementation:
// walk the string, when we see '<' fast-forward to the next '>' (or
// end). An unmatched '<' at end-of-input is dropped (along with the
// rest of the string from that '<' onward); this defeats partial-tag
// injection ("foo<scr" + "ipt>...") at the cost of dropping a literal
// trailing '<', which we deem acceptable for a comment.
func stripTags(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	in := false
	for _, r := range s {
		if !in {
			if r == '<' {
				in = true
				continue
			}
			b.WriteRune(r)
			continue
		}
		// in == true: looking for the closing '>'
		if r == '>' {
			in = false
		}
	}
	return b.String()
}

// htmlEscape replaces the five HTML metacharacters with their named
// entities. We don't use html.EscapeString here because the standard
// library version escapes '&' which double-encodes any entity-like
// substring that survived stripTags — a deliberate choice; we'd
// rather see "&amp;" than risk an entity slipping through.
func htmlEscape(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		"\"", "&quot;",
		"'", "&#39;",
	)
	return r.Replace(s)
}

// collapseWhitespace collapses runs of whitespace into a single space
// and trims leading/trailing whitespace. Newlines are preserved as
// literal '\n' so the frontend's white-space: pre-wrap CSS can
// reproduce paragraph breaks.
func collapseWhitespace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	var lastWasSpace bool
	for _, r := range s {
		if r == '\n' {
			b.WriteRune('\n')
			lastWasSpace = false
			continue
		}
		if unicode.IsSpace(r) {
			if !lastWasSpace {
				b.WriteRune(' ')
				lastWasSpace = true
			}
			continue
		}
		b.WriteRune(r)
		lastWasSpace = false
	}
	return strings.TrimSpace(b.String())
}

// countURLs returns the number of URL-like substrings in s. Used by
// the spam check; a hand-rolled scan is enough — we just count
// "http://" / "https://" occurrences. Case-insensitive.
func countURLs(s string) int {
	lower := strings.ToLower(s)
	return strings.Count(lower, "http://") + strings.Count(lower, "https://")
}
