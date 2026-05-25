package shortcode

import (
	"strconv"
	"strings"
	"unicode"
)

// Shortcode is a single parsed shortcode occurrence. The fields
// mirror the WP shape ([name attr="value"]inner[/name]):
//
//   - Name is the bare tag identifier, lowercased.
//   - Attrs is the parsed attribute map. Positional values (no key)
//     are stored under string-form numeric keys ("0", "1", …) in the
//     order they appeared, mirroring how shortcode_atts() exposes
//     them to PHP shortcode handlers.
//   - Inner is the content between the opening and closing tags.
//     Empty for self-closing shortcodes.
//   - SelfClosing reports whether the source used the [name /] form
//     rather than [name]...[/name]. Some translators emit different
//     blocks depending on this.
//   - Raw is the verbatim source text including the surrounding
//     brackets. Used by ModePreserve.
type Shortcode struct {
	Name        string
	Attrs       map[string]string
	Inner       string
	SelfClosing bool
	Raw         string
}

// scanResult is what scanShortcodes returns: an interleaved list of
// literal text runs and parsed shortcode occurrences in source order.
// A consumer (e.g. Process) walks the slice to either keep literal
// runs intact or substitute shortcode tokens with the chosen mode's
// output.
type scanResult struct {
	// Tokens is the interleaved sequence. Each element is either a
	// *literalToken (raw text run, possibly empty for symmetry) or
	// a *shortcodeToken (parsed Shortcode).
	Tokens []scanToken
}

// scanToken is a sum type for the literal | shortcode interleave.
// We use a tiny interface instead of an `any` slice so the consumer
// can type-switch with the compiler's help.
type scanToken interface{ scanTag() }

type literalToken struct{ Text string }

func (*literalToken) scanTag() {}

type shortcodeToken struct{ Code Shortcode }

func (*shortcodeToken) scanTag() {}

// scanShortcodes walks src once, emitting alternating literalToken
// and shortcodeToken values. The implementation is hand-rolled
// rather than regex-based because:
//
//   - WP allows nested shortcodes ([row][col]...[/col][/row]). A
//     greedy regex over [name](.*?)[/name] mismatches the closer.
//   - Attribute values may contain bracket characters (URLs in
//     double-quoted values), which a naïve regex breaks on.
//   - The migration tooling runs once per site, so the cost of an
//     interpreted regex is a non-issue but the cost of *wrong*
//     parsing is a bad import — manual scanning gets us the
//     correctness we need.
//
// Unbalanced or malformed brackets degrade to literal text rather
// than aborting: WP itself tolerates these, and migration must
// preserve the user's intent.
func scanShortcodes(src string) *scanResult {
	out := &scanResult{}
	if src == "" {
		return out
	}

	var (
		i   = 0
		buf strings.Builder
	)
	flushLiteral := func() {
		if buf.Len() == 0 {
			return
		}
		out.Tokens = append(out.Tokens, &literalToken{Text: buf.String()})
		buf.Reset()
	}

	for i < len(src) {
		c := src[i]
		// Escape form: WP uses [[name]] for an escaped opening
		// shortcode. We emit the inner [name] as literal text and
		// skip both outer brackets.
		if c == '[' && i+1 < len(src) && src[i+1] == '[' {
			// Find the matching ]]; if absent treat as literal.
			end := strings.Index(src[i+2:], "]]")
			if end < 0 {
				buf.WriteByte(c)
				i++
				continue
			}
			buf.WriteByte('[')
			buf.WriteString(src[i+2 : i+2+end])
			buf.WriteByte(']')
			i = i + 2 + end + 2
			continue
		}
		if c != '[' {
			buf.WriteByte(c)
			i++
			continue
		}
		// Candidate shortcode opener. Try to parse it. On any
		// failure (unbalanced, not actually a shortcode shape) emit
		// the literal '[' and advance one byte.
		sc, consumed, ok := tryParseShortcode(src, i)
		if !ok {
			buf.WriteByte('[')
			i++
			continue
		}
		flushLiteral()
		out.Tokens = append(out.Tokens, &shortcodeToken{Code: sc})
		i += consumed
	}
	flushLiteral()
	return out
}

// tryParseShortcode attempts to parse a shortcode starting at i.
// Returns the parsed Shortcode, the number of bytes consumed (so
// the caller can advance), and ok=false when src[i:] does not look
// like a valid shortcode. ok=false leaves no state behind; the
// caller emits src[i] as literal and tries again at i+1.
func tryParseShortcode(src string, i int) (Shortcode, int, bool) {
	if i >= len(src) || src[i] != '[' {
		return Shortcode{}, 0, false
	}
	// Find the matching close bracket for the opening tag. Bracket
	// characters inside a quoted attribute value are tolerated.
	openEnd := findTagClose(src, i+1)
	if openEnd < 0 {
		return Shortcode{}, 0, false
	}
	body := src[i+1 : openEnd]
	if body == "" {
		return Shortcode{}, 0, false
	}
	// Closing tag form is [/name] — not a shortcode opener.
	if body[0] == '/' {
		return Shortcode{}, 0, false
	}

	selfClosing := false
	if body[len(body)-1] == '/' {
		selfClosing = true
		body = strings.TrimRight(body[:len(body)-1], " \t")
	}

	name, attrStr := splitNameAttrs(body)
	if !isShortcodeName(name) {
		return Shortcode{}, 0, false
	}
	attrs := parseAttrs(attrStr)
	rawEnd := openEnd + 1
	sc := Shortcode{
		Name:        strings.ToLower(name),
		Attrs:       attrs,
		SelfClosing: selfClosing,
		Raw:         src[i:rawEnd],
	}
	if selfClosing {
		return sc, rawEnd - i, true
	}

	// Enclosing form: scan for the matching closing tag. We allow
	// nested same-name shortcodes by depth-counting open/close pairs.
	closer := "[/" + sc.Name + "]"
	depth := 1
	pos := rawEnd
	for pos < len(src) {
		// Lookahead for either another opener of the same name or
		// a closer. We compare case-insensitively to match WP's
		// tolerance.
		if strings.HasPrefix(strings.ToLower(src[pos:]), "["+sc.Name) {
			// Confirm it's actually a tag-shaped opener (next char
			// after the name is space, '/', or ']') — otherwise the
			// match is incidental ("[fooNotAShortcode]" doesn't open).
			nameEnd := pos + 1 + len(sc.Name)
			if nameEnd < len(src) {
				nxt := src[nameEnd]
				if nxt == ' ' || nxt == '\t' || nxt == '/' || nxt == ']' {
					depth++
					// Skip over this opener to avoid double-counting.
					oe := findTagClose(src, pos+1)
					if oe < 0 {
						return Shortcode{}, 0, false
					}
					pos = oe + 1
					continue
				}
			}
		}
		if strings.HasPrefix(strings.ToLower(src[pos:]), closer) {
			depth--
			if depth == 0 {
				sc.Inner = src[rawEnd:pos]
				end := pos + len(closer)
				sc.Raw = src[i:end]
				return sc, end - i, true
			}
			pos += len(closer)
			continue
		}
		pos++
	}
	// Unbalanced: treat the opener as a self-closing emit so the
	// content downstream of it still passes through. This matches
	// WP's tolerant behaviour.
	sc.SelfClosing = true
	return sc, rawEnd - i, true
}

// findTagClose scans from start looking for the unquoted ']' that
// closes a shortcode opener. Bracket chars inside double- or
// single-quoted attribute values are skipped. Returns -1 if no
// close bracket is found before EOL or EOF.
func findTagClose(src string, start int) int {
	var quote byte // 0, '"', or '\''
	for i := start; i < len(src); i++ {
		c := src[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			quote = c
		case ']':
			return i
		case '\n':
			// Shortcode openers are one-liners in practice. A bare
			// newline before ']' is almost always a stray '[' in
			// prose; refuse the match and let the caller emit it
			// literally.
			return -1
		}
	}
	return -1
}

// splitNameAttrs splits a shortcode body into (name, attrsText).
// The name is the run of leading non-space chars; the rest (with
// any leading whitespace trimmed) is the attrs blob.
func splitNameAttrs(body string) (name, rest string) {
	for i := 0; i < len(body); i++ {
		if body[i] == ' ' || body[i] == '\t' {
			return body[:i], strings.TrimLeft(body[i:], " \t")
		}
	}
	return body, ""
}

// isShortcodeName mirrors WP's `\w` + `-` shortcode name pattern,
// with a leading-letter requirement so we don't pick up [123] as a
// shortcode.
func isShortcodeName(s string) bool {
	if s == "" {
		return false
	}
	first := rune(s[0])
	if !unicode.IsLetter(first) && first != '_' {
		return false
	}
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-' {
			return false
		}
	}
	return true
}

// parseAttrs implements the WP shortcode_parse_atts() grammar
// (loosely): a whitespace-separated list of key="value", key='value',
// key=value, or bare positional values. Positional values are bucket-
// keyed by their position ("0", "1", …) so the caller can address
// them deterministically.
func parseAttrs(s string) map[string]string {
	out := map[string]string{}
	if s == "" {
		return out
	}
	var (
		i        = 0
		posIndex = 0
	)
	for i < len(s) {
		// Skip whitespace.
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		if i >= len(s) {
			break
		}
		// Read a token: name=… or bare value.
		start := i
		for i < len(s) && s[i] != '=' && s[i] != ' ' && s[i] != '\t' {
			i++
		}
		tok := s[start:i]
		if i < len(s) && s[i] == '=' {
			// key=value.
			key := strings.ToLower(tok)
			i++ // skip '='
			val, n := readAttrValue(s[i:])
			out[key] = val
			i += n
			continue
		}
		// Bare positional.
		out[strconv.Itoa(posIndex)] = unquote(tok)
		posIndex++
	}
	return out
}

// readAttrValue consumes a single attr value from s and returns it
// along with the number of bytes consumed. Handles double-quoted,
// single-quoted, and bare forms.
func readAttrValue(s string) (string, int) {
	if s == "" {
		return "", 0
	}
	switch s[0] {
	case '"':
		// Double-quoted; read until the next unescaped '"'.
		end := strings.IndexByte(s[1:], '"')
		if end < 0 {
			return s[1:], len(s)
		}
		return s[1 : 1+end], 1 + end + 1
	case '\'':
		end := strings.IndexByte(s[1:], '\'')
		if end < 0 {
			return s[1:], len(s)
		}
		return s[1 : 1+end], 1 + end + 1
	default:
		// Bare value: up to whitespace.
		for i := 0; i < len(s); i++ {
			if s[i] == ' ' || s[i] == '\t' {
				return s[:i], i
			}
		}
		return s, len(s)
	}
}

// unquote removes a single surrounding quote pair if present.
func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') ||
			(s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
