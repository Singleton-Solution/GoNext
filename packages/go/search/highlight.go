package search

import (
	"html"
	"strings"
	"unicode"
)

// markOpen and markClose are the tag pair the Highlight helper
// emits. Keeping them constants makes the (single) sanitisation rule
// easy to inspect: the helper escapes EVERYTHING and then injects
// these two tokens — there is no other HTML produced.
const (
	markOpen  = "<mark>"
	markClose = "</mark>"
)

// Highlight returns text with each occurrence of any term in terms
// wrapped in <mark>…</mark>. The wrapping is case-insensitive (so a
// query for "go" highlights "Go" in the source) but preserves the
// original casing in the output.
//
// Safety contract: text is HTML-escaped first, then the <mark>
// tokens are spliced in. A title like `<script>alert(1)</script>`
// renders as
//
//	&lt;script&gt;alert(1)&lt;/script&gt;
//
// in the output — never as a live tag. The only HTML the helper
// emits is the <mark> tokens themselves.
//
// Terms with no characters (empty strings, after trimming) are
// skipped. Duplicate terms (case-insensitive) are de-duplicated so a
// query like "go go" doesn't wrap matches twice.
//
// Matching is on a word-prefix basis: a term "post" matches "post"
// and "posting" but not "compost". This mirrors what
// `plainto_tsquery` does on the database side (stemming aside) and
// keeps the UI consistent with the relevance ordering.
//
// The helper does NOT truncate the text; trim to an excerpt window
// before calling if you want a short snippet.
func Highlight(text string, terms []string) string {
	if text == "" {
		return ""
	}
	escaped := html.EscapeString(text)
	if len(terms) == 0 {
		return escaped
	}

	// Normalize and dedupe terms. The seen map is keyed by the
	// lower-cased form so the second "go" in ["Go", "go"] is dropped.
	dedup := make([]string, 0, len(terms))
	seen := make(map[string]struct{}, len(terms))
	for _, t := range terms {
		trimmed := strings.TrimSpace(t)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		dedup = append(dedup, trimmed)
	}
	if len(dedup) == 0 {
		return escaped
	}

	return wrapTerms(escaped, dedup)
}

// wrapTerms walks the escaped text once and wraps every word-prefix
// match against any term. It runs in O(N*T) where N is the input
// length and T is the number of terms — fine for excerpt-sized
// inputs (a few hundred characters at most).
//
// The walk is byte-positional rather than rune-positional because
// the input is already HTML-escaped (so every entity is a
// well-formed ASCII byte sequence) and we only ever splice in
// ASCII <mark> tags. UTF-8 sequences inside the text pass through
// untouched.
func wrapTerms(escaped string, terms []string) string {
	var out strings.Builder
	out.Grow(len(escaped) + len(terms)*len(markOpen))

	i := 0
	for i < len(escaped) {
		// Find the start of the next word. Non-word bytes are
		// copied across as-is.
		if !isWordStart(escaped, i) {
			out.WriteByte(escaped[i])
			i++
			continue
		}

		// At a word boundary. Compute the word's end (one past the
		// last word byte). The word may contain entity references
		// like "&amp;" — those started with '&' which isWordStart
		// rejects, so we don't accidentally include them in the
		// match. Worst case: an entity terminates a word.
		end := i
		for end < len(escaped) && isWordByte(escaped[end]) {
			end++
		}
		word := escaped[i:end]

		// Match: case-insensitive prefix of any configured term.
		matched := false
		for _, term := range terms {
			if hasPrefixFold(word, term) {
				matched = true
				break
			}
		}
		if matched {
			out.WriteString(markOpen)
			out.WriteString(word)
			out.WriteString(markClose)
		} else {
			out.WriteString(word)
		}
		i = end
	}
	return out.String()
}

// isWordStart reports whether the byte at i begins a word — i.e.
// the byte itself is a word byte AND either i is 0 or the previous
// byte is a non-word byte. Used to find word boundaries during the
// wrapTerms walk so a term doesn't partial-match the middle of a
// longer word.
func isWordStart(s string, i int) bool {
	if !isWordByte(s[i]) {
		return false
	}
	if i == 0 {
		return true
	}
	return !isWordByte(s[i-1])
}

// isWordByte reports whether b is part of a word (letter, digit, or
// underscore). ASCII-only: the high bit-set bytes that introduce
// UTF-8 continuation sequences are treated as non-word so a Latin-1
// title still splits at multibyte boundaries. The current MVP
// corpus is English-only (per the migration's dictionary choice);
// the multibyte-aware variant is a follow-up.
func isWordByte(b byte) bool {
	if b == '_' {
		return true
	}
	r := rune(b)
	return r < 0x80 && (unicode.IsLetter(r) || unicode.IsDigit(r))
}

// hasPrefixFold is a case-insensitive strings.HasPrefix. Avoids the
// allocation of strings.ToLower(word) by walking both inputs in
// parallel.
func hasPrefixFold(word, prefix string) bool {
	if len(word) < len(prefix) {
		return false
	}
	for i := 0; i < len(prefix); i++ {
		a := word[i]
		b := prefix[i]
		if a == b {
			continue
		}
		if toLowerASCII(a) != toLowerASCII(b) {
			return false
		}
	}
	return true
}

// toLowerASCII folds an ASCII letter to lower case. Non-letters and
// non-ASCII bytes are returned unchanged.
func toLowerASCII(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + ('a' - 'A')
	}
	return b
}
