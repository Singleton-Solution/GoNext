package media

import (
	"net/url"
	"sort"
	"strings"
)

// KeyExtractor canonicalizes a raw lookup key into the form the
// Coalescer will use for singleflight.
//
// Two semantically equivalent keys (e.g., the same variant spec with
// query-string parameters in different order) should canonicalize to
// the same output, or the Coalescer will start one render per surface
// form and the stampede defense is defeated for half of the callers.
//
// Implementations MUST be deterministic and pure — same input always
// produces the same output, no side effects. The Coalescer calls the
// KeyExtractor once per Get on the hot path; it should be cheap.
//
// If nil is passed in CoalescerOptions, the raw key is used unchanged.
type KeyExtractor func(rawKey string) string

// SortedQueryKey returns a KeyExtractor that canonicalizes a key of
// the form "<path>?<query>" by sorting the query parameters
// alphabetically and re-encoding them. Keys without a "?" are returned
// unchanged.
//
// This is the right choice for the variant proxy, where keys look
// like "media/{id}?w=800&h=600&fit=cover" — the same render spec can
// arrive with parameters in any order depending on how upstream code
// built the URL, and we want all surface forms to collapse to one
// in-flight render.
//
// The output is intentionally NOT a valid URL — query values are
// re-encoded with url.Values.Encode which uses '+' for spaces, which
// matches what a sorted comparator would produce but is fine because
// the output is only used as a singleflight key (an opaque string),
// never parsed back.
//
// Empty parameter values are preserved (w=800&fit= stays as fit= in
// the canonical form), and duplicate keys keep all their values in
// the order they appeared after sorting the key.
func SortedQueryKey() KeyExtractor {
	return func(raw string) string {
		idx := strings.IndexByte(raw, '?')
		if idx < 0 {
			return raw
		}
		path, query := raw[:idx], raw[idx+1:]
		if query == "" {
			return path + "?"
		}
		vals, err := url.ParseQuery(query)
		if err != nil {
			// Malformed query — return the raw key unchanged so the
			// caller still gets some coalescing on exact duplicates.
			// We don't want a parse error to silently fall through to
			// "every request renders independently."
			return raw
		}
		keys := make([]string, 0, len(vals))
		for k := range vals {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		b.Grow(len(raw))
		b.WriteString(path)
		b.WriteByte('?')
		first := true
		for _, k := range keys {
			vs := vals[k]
			// Preserve order of repeated values for a given key — the
			// caller may legitimately use repeated keys (?tag=a&tag=b)
			// and reordering them would change meaning.
			for _, v := range vs {
				if !first {
					b.WriteByte('&')
				}
				first = false
				b.WriteString(url.QueryEscape(k))
				b.WriteByte('=')
				b.WriteString(url.QueryEscape(v))
			}
		}
		return b.String()
	}
}
