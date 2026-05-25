package urlrewrite

import (
	"regexp"
	"strings"

	"github.com/google/uuid"
)

// MediaRef is the destination side of a rewrite mapping. The
// migrator records both the GoNext-side media id (for callers that
// want to emit a referenced-id attribute) and the new public URL
// (for the actual textual substitution). One or both fields may be
// empty when the migrator only knew one of them.
type MediaRef struct {
	// ID is the GoNext media row id. Optional: callers that don't
	// care about the id can leave it zero.
	ID uuid.UUID

	// URL is the new public URL where the binary now lives. Used as
	// the substitute text in href / src / srcset / inline-url.
	URL string
}

// Options configure a Rewriter.
type Options struct {
	// Map is the source URL → MediaRef lookup. Keys are matched
	// case-insensitively; lookups normalise by lowercasing the
	// scheme + host fragment. Required.
	Map map[string]MediaRef

	// LegacyHosts is an optional set of hostnames that should be
	// considered "same site as the WP origin" for relaxed matching.
	// When a candidate URL on a known legacy host matches a
	// path-relative key in Map (e.g. "/wp-content/uploads/2024/03/a.jpg"
	// or "wp-content/uploads/2024/03/a.jpg"), the rewriter falls back
	// to the path-based mapping. Useful when a site has moved between
	// staging and production WP hosts.
	LegacyHosts []string

	// NewBaseURL, when non-empty, replaces any unmapped reference to
	// "<oldhost>/wp-content/uploads/..." with the same path under
	// NewBaseURL. This is the safety net for binaries the migrator
	// already moved but didn't end up in Map — common when an admin
	// re-uploaded one file post-migration.
	NewBaseURL string
}

// Rewriter is the rewriting engine. Construct with New.
type Rewriter struct {
	opts Options
}

// New returns a Rewriter using the given options. Map may be nil
// (in which case Rewrite is a no-op).
func New(opts Options) *Rewriter {
	return &Rewriter{opts: opts}
}

// ---------------------------------------------------------------------
// Regex pre-compilation. We intentionally avoid html.Parse for two
// reasons:
//   - WP post content is HTML *fragments*, not full documents; the
//     stdlib parser injects an implicit <html><body>… wrapper which
//     downstream html2blocks then has to undo.
//   - The five patterns below are well-formed enough that an attribute-
//     anchored regex gives us deterministic substitutions with no
//     reflow.
//
// All matchers operate on full HTML byte slices and respect quoting:
// the URL value is captured between the quotes, so something like
//   <img alt="see /wp-content/" src="…">
// only rewrites the src, never the alt.
// ---------------------------------------------------------------------

// attrSrcRe matches src/href/poster/data-src attributes on common
// media-bearing tags. The leading attribute name list is alternated
// to keep the pattern explicit — we don't want to rewrite e.g. an
// arbitrary data-config attribute that happens to contain a URL.
var attrSrcRe = regexp.MustCompile(`(?i)(\b(?:src|href|poster|data-src)\s*=\s*)("([^"]*)"|'([^']*)')`)

// srcsetRe matches a srcset attribute and captures the full value;
// we then split on commas inside the substitution helper because
// srcset is a list of "url descriptor" pairs.
var srcsetRe = regexp.MustCompile(`(?i)(\bsrcset\s*=\s*)("([^"]*)"|'([^']*)')`)

// styleRe matches a style attribute; we then run urlFuncRe over its
// captured value.
var styleRe = regexp.MustCompile(`(?i)(\bstyle\s*=\s*)("([^"]*)"|'([^']*)')`)

// urlFuncRe matches the url(...) form inside CSS. Three variants:
// url(x), url('x'), url("x"). The url body is the first non-empty
// of the three capture groups.
var urlFuncRe = regexp.MustCompile(`url\(\s*(?:"([^"]*)"|'([^']*)'|([^)\s]+))\s*\)`)

// Rewrite walks the HTML byte slice and returns a rewritten copy
// plus the number of substitutions performed. The original slice
// is never mutated. Empty input yields (nil, 0).
//
// The function performs at most one regex sweep per category; each
// matched attribute value is mapped through resolve and emitted
// back into the output with the same quote style.
func (r *Rewriter) Rewrite(content []byte) ([]byte, int) {
	if r == nil || len(content) == 0 {
		return nil, 0
	}
	if len(r.opts.Map) == 0 && r.opts.NewBaseURL == "" {
		return content, 0
	}
	var count int
	src := content

	// 1. src / href / poster / data-src.
	src = attrSrcRe.ReplaceAllFunc(src, func(m []byte) []byte {
		groups := attrSrcRe.FindSubmatch(m)
		prefix := groups[1]
		valDouble := groups[3]
		valSingle := groups[4]
		val := valDouble
		quote := byte('"')
		if len(val) == 0 && len(valSingle) > 0 {
			val = valSingle
			quote = '\''
		}
		newVal, n := r.resolve(string(val))
		count += n
		out := append([]byte{}, prefix...)
		out = append(out, quote)
		out = append(out, []byte(newVal)...)
		out = append(out, quote)
		return out
	})

	// 2. srcset (comma-separated "url descriptor" list).
	src = srcsetRe.ReplaceAllFunc(src, func(m []byte) []byte {
		groups := srcsetRe.FindSubmatch(m)
		prefix := groups[1]
		valDouble := groups[3]
		valSingle := groups[4]
		val := valDouble
		quote := byte('"')
		if len(val) == 0 && len(valSingle) > 0 {
			val = valSingle
			quote = '\''
		}
		newVal, n := r.rewriteSrcsetValue(string(val))
		count += n
		out := append([]byte{}, prefix...)
		out = append(out, quote)
		out = append(out, []byte(newVal)...)
		out = append(out, quote)
		return out
	})

	// 3. style="…url(…)…".
	src = styleRe.ReplaceAllFunc(src, func(m []byte) []byte {
		groups := styleRe.FindSubmatch(m)
		prefix := groups[1]
		valDouble := groups[3]
		valSingle := groups[4]
		val := valDouble
		quote := byte('"')
		if len(val) == 0 && len(valSingle) > 0 {
			val = valSingle
			quote = '\''
		}
		newVal, n := r.rewriteStyleValue(string(val))
		count += n
		out := append([]byte{}, prefix...)
		out = append(out, quote)
		out = append(out, []byte(newVal)...)
		out = append(out, quote)
		return out
	})

	return src, count
}

// RewriteString is the string-flavoured wrapper.
func (r *Rewriter) RewriteString(content string) (string, int) {
	out, n := r.Rewrite([]byte(content))
	return string(out), n
}

// rewriteSrcsetValue parses an srcset attr value (a comma-separated
// list of "<url> [<descriptor>]" entries) and rewrites each url
// independently. Commas inside URLs aren't legal in srcset per the
// HTML spec, so a plain Split is safe enough for the migration
// import path.
func (r *Rewriter) rewriteSrcsetValue(val string) (string, int) {
	parts := strings.Split(val, ",")
	count := 0
	for i, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		// Split on whitespace once to separate url + descriptor.
		urlStr, desc, hasDesc := splitOnceWS(p)
		newURL, n := r.resolve(urlStr)
		count += n
		if hasDesc {
			parts[i] = newURL + " " + desc
		} else {
			parts[i] = newURL
		}
	}
	return strings.Join(parts, ", "), count
}

// rewriteStyleValue rewrites url(...) occurrences inside a style
// attribute value.
func (r *Rewriter) rewriteStyleValue(val string) (string, int) {
	count := 0
	out := urlFuncRe.ReplaceAllStringFunc(val, func(m string) string {
		groups := urlFuncRe.FindStringSubmatch(m)
		// One of [1],[2],[3] is non-empty.
		raw := groups[1]
		if raw == "" {
			raw = groups[2]
		}
		if raw == "" {
			raw = groups[3]
		}
		newURL, n := r.resolve(raw)
		count += n
		// Preserve the quoting style by checking groups.
		switch {
		case groups[1] != "":
			return `url("` + newURL + `")`
		case groups[2] != "":
			return `url('` + newURL + `')`
		default:
			return `url(` + newURL + `)`
		}
	})
	return out, count
}

// resolve maps a single candidate URL through the rewriter's table.
// Returns the (possibly unchanged) URL and 1 if a rewrite happened,
// 0 otherwise.
//
// The lookup tries, in order:
//   1. Exact map hit on the URL.
//   2. Lowercased URL (case-insensitive match — WP URLs are often
//      lowercase in fixtures but mixed in real exports).
//   3. Path-only key when the host is in LegacyHosts.
//   4. NewBaseURL substitution for any *URL containing
//      "/wp-content/uploads/" that wasn't matched above.
func (r *Rewriter) resolve(raw string) (string, int) {
	if raw == "" {
		return raw, 0
	}
	if ref, ok := r.opts.Map[raw]; ok && ref.URL != "" {
		return ref.URL, 1
	}
	lower := strings.ToLower(raw)
	if lower != raw {
		if ref, ok := r.opts.Map[lower]; ok && ref.URL != "" {
			return ref.URL, 1
		}
	}
	// Path-only fallback.
	if path := relativePathIfLegacyHost(raw, r.opts.LegacyHosts); path != "" {
		if ref, ok := r.opts.Map[path]; ok && ref.URL != "" {
			return ref.URL, 1
		}
	}
	// NewBaseURL safety net.
	if r.opts.NewBaseURL != "" {
		if idx := strings.Index(raw, "/wp-content/uploads/"); idx >= 0 {
			return strings.TrimRight(r.opts.NewBaseURL, "/") + raw[idx:], 1
		}
	}
	return raw, 0
}

// relativePathIfLegacyHost returns the path portion of raw when the
// host matches one of the legacy hosts (case-insensitive). Returns
// "" otherwise.
//
// The path is returned exactly as it appears in raw — leading slash
// preserved — so callers can use it as a direct map key when their
// map uses path keys.
func relativePathIfLegacyHost(raw string, hosts []string) string {
	// Cheap host parse: we don't want to drag in net/url for this hot
	// path. Look for "://", then the next '/'.
	scheme := strings.Index(raw, "://")
	if scheme < 0 {
		return ""
	}
	rest := raw[scheme+3:]
	slash := strings.IndexByte(rest, '/')
	if slash < 0 {
		return ""
	}
	host := strings.ToLower(rest[:slash])
	path := rest[slash:]
	for _, h := range hosts {
		if strings.EqualFold(h, host) {
			return path
		}
	}
	return ""
}

// splitOnceWS splits s on the first run of whitespace. Returns the
// two halves and a boolean reporting whether the split happened.
func splitOnceWS(s string) (string, string, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' || s[i] == '\t' {
			return s[:i], strings.TrimLeft(s[i:], " \t"), true
		}
	}
	return s, "", false
}
