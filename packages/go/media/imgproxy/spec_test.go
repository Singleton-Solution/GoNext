package imgproxy

import (
	"errors"
	"strings"
	"testing"
)

func TestParse_HappyPath(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want Spec
	}{
		{
			name: "all tokens explicit",
			raw:  "w-800.h-600.q-85.fit-cover.webp",
			want: Spec{Width: 800, Height: 600, Quality: 85, Fit: FitCover, Format: FormatWebP},
		},
		{
			name: "order does not matter",
			raw:  "webp.fit-contain.h-200.q-50.w-100",
			want: Spec{Width: 100, Height: 200, Quality: 50, Fit: FitContain, Format: FormatWebP},
		},
		{
			name: "defaults applied",
			raw:  "w-400",
			want: Spec{Width: 400, Quality: DefaultQuality, Fit: FitCover, Format: FormatWebP},
		},
		{
			name: "height-only",
			raw:  "h-300",
			want: Spec{Height: 300, Quality: DefaultQuality, Fit: FitCover, Format: FormatWebP},
		},
		{
			name: "jpeg format",
			raw:  "w-200.jpeg",
			want: Spec{Width: 200, Quality: DefaultQuality, Fit: FitCover, Format: FormatJPEG},
		},
		{
			name: "jpg alias",
			raw:  "w-200.jpg",
			want: Spec{Width: 200, Quality: DefaultQuality, Fit: FitCover, Format: FormatJPEG},
		},
		{
			name: "png format",
			raw:  "w-200.png",
			want: Spec{Width: 200, Quality: DefaultQuality, Fit: FitCover, Format: FormatPNG},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(tc.raw)
			if err != nil {
				t.Fatalf("Parse(%q) returned error: %v", tc.raw, err)
			}
			if got != tc.want {
				t.Fatalf("Parse(%q) = %+v, want %+v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestParse_Errors(t *testing.T) {
	tests := []struct {
		name        string
		raw         string
		wantContain string
	}{
		{name: "empty", raw: "", wantContain: "empty"},
		{name: "unknown key", raw: "x-10", wantContain: "unknown token"},
		{name: "unknown format", raw: "w-100.tiff", wantContain: "unknown token"},
		{name: "w out of range", raw: "w-99999", wantContain: "w out of range"},
		{name: "h out of range", raw: "h-0", wantContain: "h out of range"},
		{name: "q out of range", raw: "w-100.q-200", wantContain: "q out of range"},
		{name: "negative q", raw: "w-100.q--5", wantContain: "q out of range"},
		{name: "unknown fit", raw: "w-100.fit-stretch", wantContain: "unknown fit"},
		{name: "duplicate w", raw: "w-100.w-200", wantContain: "duplicate w"},
		{name: "duplicate format", raw: "w-100.webp.png", wantContain: "duplicate format"},
		{name: "no dimension", raw: "q-80.webp", wantContain: "at least one of w or h"},
		{name: "path separator", raw: "w-100/h-200", wantContain: "path separators"},
		{name: "empty token", raw: "w-100..h-200", wantContain: "empty token"},
		{name: "non-numeric w", raw: "w-abc", wantContain: "w out of range"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.raw)
			if err == nil {
				t.Fatalf("Parse(%q) returned nil error, want %s", tc.raw, tc.wantContain)
			}
			if !errors.Is(err, ErrInvalidSpec) {
				t.Fatalf("Parse(%q) error = %v, want errors.Is(err, ErrInvalidSpec) = true", tc.raw, err)
			}
			if !strings.Contains(err.Error(), tc.wantContain) {
				t.Fatalf("Parse(%q) error = %q, want substring %q", tc.raw, err.Error(), tc.wantContain)
			}
		})
	}
}

func TestSpec_Canonical_OrderIndependent(t *testing.T) {
	a, err := Parse("w-800.h-600.q-85.fit-cover.webp")
	if err != nil {
		t.Fatalf("Parse a: %v", err)
	}
	b, err := Parse("webp.h-600.fit-cover.q-85.w-800")
	if err != nil {
		t.Fatalf("Parse b: %v", err)
	}
	if a.Canonical() != b.Canonical() {
		t.Fatalf("canonical differs:\n a=%q\n b=%q", a.Canonical(), b.Canonical())
	}
}

func TestSpec_Canonical_DefaultsIncluded(t *testing.T) {
	s, err := Parse("w-100")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := s.Canonical()
	wantParts := []string{"w-100", "q-82", "fit-cover", "webp"}
	for _, p := range wantParts {
		if !strings.Contains(got, p) {
			t.Fatalf("canonical %q missing %q", got, p)
		}
	}
}

func TestSpec_TooLongRejected(t *testing.T) {
	// Build a string > 256 bytes via duplicated tokens. Note Parse
	// will catch the duplicate before the length, but we want the
	// length-first defence to fire — use a single long token.
	long := "w-" + strings.Repeat("1", 300)
	_, err := Parse(long)
	if err == nil {
		t.Fatal("expected error for over-long spec")
	}
	if !strings.Contains(err.Error(), "256 bytes") {
		t.Fatalf("expected length error, got %v", err)
	}
}
