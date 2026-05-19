package imageproc

import (
	"strings"
	"testing"
)

// makeResult builds a known-shape Result for the srcset tests. The
// dimensions are picked to match what the upload pipeline emits at
// the default sizes; the bytes are zero because the srcset path
// doesn't look at them.
func makeResult() *Result {
	return &Result{
		SourceFormat: "jpeg",
		SourceWidth:  2048,
		SourceHeight: 1536,
		Variants: []Variant{
			{Name: VariantThumbnail, Format: FormatWebP, Width: 256, Height: 192, KeySuffix: ".thumb.webp"},
			{Name: VariantMedium, Format: FormatWebP, Width: 768, Height: 576, KeySuffix: ".medium.webp"},
			{Name: VariantLarge, Format: FormatWebP, Width: 1536, Height: 1152, KeySuffix: ".large.webp"},
			{Name: VariantOriginal, Format: FormatWebP, Width: 2048, Height: 1536, KeySuffix: ".orig.webp"},
			{Name: VariantThumbnail, Format: FormatAVIF, Width: 256, Height: 192, KeySuffix: ".thumb.avif"},
		},
	}
}

func TestBuildSrcSet_OrdersNarrowestFirst(t *testing.T) {
	t.Parallel()
	r := makeResult()
	resolver := PrefixURLResolver("https://cdn.example.com", "2026/01/photo.jpg")

	got := BuildSrcSet(r, FormatWebP, resolver)
	want := "https://cdn.example.com/2026/01/photo.jpg.thumb.webp 256w, " +
		"https://cdn.example.com/2026/01/photo.jpg.medium.webp 768w, " +
		"https://cdn.example.com/2026/01/photo.jpg.large.webp 1536w, " +
		"https://cdn.example.com/2026/01/photo.jpg.orig.webp 2048w"
	if got != want {
		t.Errorf("BuildSrcSet:\n got: %s\nwant: %s", got, want)
	}
}

// TestBuildSrcSet_FormatFilter pins that BuildSrcSet emits only
// variants of the requested format — passing FormatAVIF on a Result
// that has one AVIF entry must not pollute the result with WebP
// siblings.
func TestBuildSrcSet_FormatFilter(t *testing.T) {
	t.Parallel()
	r := makeResult()
	resolver := PrefixURLResolver("", "k")

	got := BuildSrcSet(r, FormatAVIF, resolver)
	if !strings.Contains(got, ".thumb.avif") {
		t.Errorf("AVIF srcset missing thumb: %s", got)
	}
	if strings.Contains(got, ".webp") {
		t.Errorf("AVIF srcset contains a WebP entry: %s", got)
	}
}

// TestBuildSrcSet_EmptyOnNoMatch documents the fallback for a Result
// with no variants of the requested format: empty string, so the
// caller can fall back to a bare <img src>.
func TestBuildSrcSet_EmptyOnNoMatch(t *testing.T) {
	t.Parallel()
	r := makeResult()
	resolver := PrefixURLResolver("", "k")
	got := BuildSrcSet(r, FormatPNG, resolver)
	if got != "" {
		t.Errorf("expected empty srcset for missing format, got %q", got)
	}
}

// TestBuildSrcSet_NilSafety pins the defensive contract: nil inputs
// don't panic.
func TestBuildSrcSet_NilSafety(t *testing.T) {
	t.Parallel()
	if got := BuildSrcSet(nil, FormatWebP, nil); got != "" {
		t.Errorf("BuildSrcSet(nil) = %q, want empty", got)
	}
	if got := BuildSrcSet(makeResult(), FormatWebP, nil); got != "" {
		t.Errorf("BuildSrcSet with nil resolver = %q, want empty", got)
	}
}

// TestEntries_DeterministicOrder ensures the (Width, URL) ordering
// is stable across runs. We rely on the determinism in the JSON
// encoded into the REST surface.
func TestEntries_DeterministicOrder(t *testing.T) {
	t.Parallel()
	r := makeResult()
	resolver := PrefixURLResolver("p", "k")
	first := Entries(r, FormatWebP, resolver)
	second := Entries(r, FormatWebP, resolver)
	if len(first) != len(second) {
		t.Fatalf("Entries length drift: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Errorf("Entries[%d] drift: %v vs %v", i, first[i], second[i])
		}
	}
	// Sorted narrowest first.
	for i := 1; i < len(first); i++ {
		if first[i].Width < first[i-1].Width {
			t.Errorf("Entries not narrowest-first: %v", first)
			break
		}
	}
}

// TestPictureSources_OrdersByFormatModernity covers the <picture>
// helper: AVIF must come first (so capable clients pick it), WebP
// second, then JPEG/PNG fallbacks.
func TestPictureSources_OrdersByFormatModernity(t *testing.T) {
	t.Parallel()
	r := makeResult()
	resolver := PrefixURLResolver("", "k")

	sources := PictureSources(r, resolver)
	if len(sources) < 2 {
		t.Fatalf("PictureSources len = %d, want >= 2 (avif, webp)", len(sources))
	}
	if sources[0].Type != "image/avif" {
		t.Errorf("first source = %q, want image/avif", sources[0].Type)
	}
	if sources[1].Type != "image/webp" {
		t.Errorf("second source = %q, want image/webp", sources[1].Type)
	}
}

// TestPrefixURLResolver covers the URL-shape resolver used by the
// REST and theme surfaces. Trailing slashes on the prefix must not
// produce a "//" in the output; an empty prefix must produce a
// bare-key URL.
func TestPrefixURLResolver(t *testing.T) {
	t.Parallel()
	cases := []struct {
		prefix, key string
		want        string
	}{
		{"https://cdn.example.com", "2026/01/a.jpg", "https://cdn.example.com/2026/01/a.jpg.thumb.webp"},
		{"https://cdn.example.com/", "2026/01/a.jpg", "https://cdn.example.com/2026/01/a.jpg.thumb.webp"},
		{"", "k", "k.thumb.webp"},
	}
	v := Variant{Name: VariantThumbnail, Format: FormatWebP, KeySuffix: ".thumb.webp"}
	for _, c := range cases {
		got := PrefixURLResolver(c.prefix, c.key)(v)
		if got != c.want {
			t.Errorf("PrefixURLResolver(%q,%q): got %q, want %q", c.prefix, c.key, got, c.want)
		}
	}
}

// TestSizesAttribute is a small contract check. Themes hard-code the
// expected breakpoints; a silent change would shift layout.
func TestSizesAttribute(t *testing.T) {
	t.Parallel()
	got := SizesAttribute()
	for _, want := range []string{"256px", "768px", "1536px", "max-width"} {
		if !strings.Contains(got, want) {
			t.Errorf("SizesAttribute missing %q in %q", want, got)
		}
	}
}
