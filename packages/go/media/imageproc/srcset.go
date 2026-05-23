package imageproc

import (
	"fmt"
	"sort"
	"strings"
)

// SrcSetEntry is one entry in an HTML srcset attribute. URL is the
// addressable variant URL; Width is the rendered pixel width used as
// the "w" descriptor the browser uses to pick a variant.
type SrcSetEntry struct {
	URL   string
	Width int
}

// String renders one entry in the "<url> <w>w" shape the browser
// parser expects. Separated as a method so callers that emit srcset
// into JSON (rather than HTML) can build a list of (URL, Width) pairs
// without paying for the string formatting.
func (e SrcSetEntry) String() string {
	return fmt.Sprintf("%s %dw", e.URL, e.Width)
}

// URLResolver turns a Variant's KeySuffix into an externally-
// addressable URL. The default resolver in REST and theme paths just
// concatenates a base URL with the source key + suffix; tests and
// integration code may swap in a CDN-aware resolver that signs the URL.
type URLResolver func(variant Variant) string

// PrefixURLResolver returns a URLResolver that prepends prefix and a
// "/" if needed to a fixed sourceKey, then appends the variant's
// KeySuffix. Used by the REST surface when the public URL is the
// bucket's public endpoint and the variant lives alongside the
// original.
//
//	resolver := PrefixURLResolver("https://cdn.example.com", "2026/01/abc-photo.jpg")
//	resolver(thumbVariant) // "https://cdn.example.com/2026/01/abc-photo.jpg.thumb.webp"
func PrefixURLResolver(prefix, sourceKey string) URLResolver {
	base := strings.TrimRight(prefix, "/")
	return func(v Variant) string {
		if base == "" {
			return sourceKey + v.KeySuffix
		}
		return base + "/" + sourceKey + v.KeySuffix
	}
}

// BuildSrcSet renders r into an HTML srcset attribute value, restricted
// to variants of the given format. Variants are emitted narrowest-
// first, which is the order the browser uses to pick the smallest
// candidate matching the layout width — emitting widest-first would
// still parse but is harder for a developer to inspect by eye.
//
// The Original variant is included as the widest entry (using its
// post-decode Width). If the caller wants to exclude it (because the
// theme prefers <picture> with a separate <source> for the full-size),
// they can post-process the returned string or call Entries and filter.
//
// Returns an empty string if no variant matches format — the caller
// should treat that as "no srcset, fall back to a single <img src>".
func BuildSrcSet(r *Result, format Format, resolver URLResolver) string {
	if r == nil || resolver == nil {
		return ""
	}
	entries := Entries(r, format, resolver)
	if len(entries) == 0 {
		return ""
	}
	parts := make([]string, 0, len(entries))
	for _, e := range entries {
		parts = append(parts, e.String())
	}
	return strings.Join(parts, ", ")
}

// Entries returns the per-variant SrcSetEntry list. Useful when the
// caller wants to emit srcset into JSON (the admin API does this so
// the frontend can render its own <img>) or to compose a <picture>
// element with multiple <source>s. Sorted narrowest-first; ties on
// Width break by Variant Name for determinism.
func Entries(r *Result, format Format, resolver URLResolver) []SrcSetEntry {
	if r == nil || resolver == nil {
		return nil
	}
	var entries []SrcSetEntry
	for _, v := range r.Variants {
		if v.Format != format {
			continue
		}
		w := v.Width
		if w <= 0 {
			// Defensive: a variant with no recorded width can't be in
			// the srcset (the browser has no way to choose it).
			continue
		}
		entries = append(entries, SrcSetEntry{URL: resolver(v), Width: w})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Width != entries[j].Width {
			return entries[i].Width < entries[j].Width
		}
		return entries[i].URL < entries[j].URL
	})
	return entries
}

// SizesAttribute is a convenience helper that returns the canonical
// `sizes="..."` companion attribute. The default lays out variants
// at their natural breakpoint:
//
//	(max-width: 480px) 256px,
//	(max-width: 1024px) 768px,
//	1536px
//
// Callers wiring a specific theme will usually want to pass their own
// rules and skip this helper; it's here for the rendering surface
// where a sane default beats no srcset.
func SizesAttribute() string {
	return "(max-width: 480px) 256px, (max-width: 1024px) 768px, 1536px"
}

// PictureSources builds the per-format <source> entries for a
// <picture> element. Returns a slice of (srcset, type) pairs in the
// canonical order: AVIF first (if present), then WebP, then any
// JPEG/PNG fallbacks. The browser uses the first matching <source>;
// putting the smaller, modern formats first lets capable clients
// pick them and lets older clients fall through to the <img> tag.
//
// The function is small enough that templating engines call it
// directly: `{{ range PictureSources $result $resolver }}<source
// srcset="{{ .SrcSet }}" type="{{ .Type }}">{{ end }}`.
func PictureSources(r *Result, resolver URLResolver) []PictureSource {
	if r == nil || resolver == nil {
		return nil
	}
	order := []Format{FormatAVIF, FormatWebP, FormatJPEG, FormatPNG}
	out := make([]PictureSource, 0, len(order))
	for _, f := range order {
		s := BuildSrcSet(r, f, resolver)
		if s == "" {
			continue
		}
		out = append(out, PictureSource{SrcSet: s, Type: f.MIMEType()})
	}
	return out
}

// PictureSource describes one entry in a <picture> element. The wire
// shape mirrors the HTML attribute names so a template can emit it
// without further renaming.
type PictureSource struct {
	SrcSet string `json:"srcset"`
	Type   string `json:"type"`
}
