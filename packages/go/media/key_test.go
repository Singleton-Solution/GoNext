package media

import "testing"

func TestSortedQueryKey_NoQueryString(t *testing.T) {
	ext := SortedQueryKey()
	in := "media/abc.webp"
	if got := ext(in); got != in {
		t.Errorf("ext(%q) = %q, want %q", in, got, in)
	}
}

func TestSortedQueryKey_EmptyQuery(t *testing.T) {
	ext := SortedQueryKey()
	in := "media/abc?"
	if got := ext(in); got != "media/abc?" {
		t.Errorf("ext(%q) = %q, want %q", in, got, "media/abc?")
	}
}

func TestSortedQueryKey_CollapsesParamOrder(t *testing.T) {
	ext := SortedQueryKey()
	tests := []struct {
		name string
		a, b string
	}{
		{"two-param swap", "p?w=800&h=600", "p?h=600&w=800"},
		{"three-param permutation", "p?fit=cover&h=600&w=800", "p?w=800&fit=cover&h=600"},
		{"already sorted is idempotent", "p?a=1&b=2&c=3", "p?a=1&b=2&c=3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ga := ext(tt.a)
			gb := ext(tt.b)
			if ga != gb {
				t.Errorf("canonical(%q)=%q != canonical(%q)=%q", tt.a, ga, tt.b, gb)
			}
		})
	}
}

func TestSortedQueryKey_PreservesRepeatedValueOrder(t *testing.T) {
	// Repeated keys with different value orders ARE semantically
	// different (e.g. ?tag=a&tag=b vs ?tag=b&tag=a may mean different
	// things to upstream code) and should NOT collapse.
	ext := SortedQueryKey()
	a := ext("p?tag=a&tag=b")
	b := ext("p?tag=b&tag=a")
	if a == b {
		t.Errorf("expected repeated-value order to be preserved, both canonicalized to %q", a)
	}
}

func TestSortedQueryKey_EmptyValuesPreserved(t *testing.T) {
	ext := SortedQueryKey()
	// fit= (empty value) is a legitimate param the variant pipeline
	// might receive; we don't want it silently dropped.
	got := ext("p?fit=&w=100")
	want := "p?fit=&w=100"
	if got != want {
		t.Errorf("ext = %q, want %q", got, want)
	}
}

func TestSortedQueryKey_MalformedQueryReturnsRaw(t *testing.T) {
	ext := SortedQueryKey()
	// A literal '%' followed by non-hex is invalid percent-encoding.
	// url.ParseQuery returns an error; we fall back to the raw key
	// rather than silently dropping work into a "every request a
	// new leader" hole.
	in := "p?bad=%ZZ"
	if got := ext(in); got != in {
		t.Errorf("ext(%q) = %q, want %q (fallback to raw)", in, got, in)
	}
}

func TestSortedQueryKey_SpecialCharsAreEscaped(t *testing.T) {
	ext := SortedQueryKey()
	// Space in a value should round-trip through QueryEscape into '+'.
	// We don't pin the exact output (Go's encoding choice), but two
	// equivalent inputs must still canonicalize to the same string.
	a := ext("p?caption=hello world&w=100")
	b := ext("p?w=100&caption=hello world")
	if a != b {
		t.Errorf("space-bearing values failed to collapse: %q vs %q", a, b)
	}
}
