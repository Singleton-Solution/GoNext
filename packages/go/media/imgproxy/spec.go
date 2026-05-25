package imgproxy

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Spec is the parsed, validated form of a /img/{id}/{spec} URL
// segment. Every field has a documented zero-value default so the
// transformer can rely on a fully-populated struct after Parse
// returns without an error.
//
// The on-wire form is dot-separated key-value pairs, e.g.
// "w-800.h-600.q-85.fit-cover.webp". Parse is forgiving about token
// order (the format token may appear anywhere) and forgiving about
// missing dimensions (at least one of w/h must be present — the other
// is derived from the source at transform time).
type Spec struct {
	// Width is the requested output width in pixels. Zero means
	// "derive from height and source aspect ratio". The transformer
	// rejects a Spec with both Width and Height zero — that case is
	// caught in Parse so a downstream caller can rely on at least one
	// dimension being set.
	Width int

	// Height is the requested output height in pixels. Same zero-value
	// semantics as Width.
	Height int

	// Quality is the encoder quality knob in the range 1..100. The
	// default of 82 matches imageproc.DefaultQuality and lines up with
	// the libvips default for WebP encoding.
	Quality int

	// Fit chooses the resize semantics:
	//
	//   - FitCover: scale the source so the smaller dimension matches
	//     the requested box, then center-crop the larger dimension. The
	//     output has exactly (Width, Height) pixels and no letterbox
	//     bars. Default — this is what the marketing-content surface
	//     wants 90% of the time.
	//
	//   - FitContain: scale the source so the larger dimension matches
	//     the requested box; the smaller dimension shrinks
	//     proportionally. The output may be smaller than (Width, Height)
	//     on one axis. Used by surfaces that want the entire source
	//     visible without cropping (e.g., product galleries).
	Fit FitMode

	// Format is the requested output encoding. WebP is the default
	// when no format token is supplied; the route handler may override
	// this via Accept negotiation in a follow-up.
	Format Format
}

// FitMode selects between cover and contain resize semantics. See
// the Spec.Fit doc block for the per-mode behaviour.
type FitMode string

const (
	// FitCover crops to fill the requested dimensions exactly.
	FitCover FitMode = "cover"

	// FitContain scales the longer edge to fit and may produce a
	// smaller image than requested on the shorter edge.
	FitContain FitMode = "contain"
)

// Format identifies the output encoding for a generated variant. The
// proxy supports a strict subset of the formats imageproc knows about
// — AVIF is omitted because the on-demand path can't afford a 30s
// libaom encode for a cold cache miss.
type Format string

const (
	FormatWebP Format = "webp"
	FormatJPEG Format = "jpeg"
	FormatPNG  Format = "png"
)

// MIMEType returns the IANA media type for f. Used by the HTTP
// handler to set Content-Type on the cached output.
func (f Format) MIMEType() string {
	switch f {
	case FormatJPEG:
		return "image/jpeg"
	case FormatPNG:
		return "image/png"
	default:
		return "image/webp"
	}
}

// FileExtension returns the canonical extension for f (without the
// leading dot). Used by the cache key builder so the rendered file
// lands at a path the operator can serve directly with a static file
// server if they bypass the API.
func (f Format) FileExtension() string {
	switch f {
	case FormatJPEG:
		return "jpg"
	case FormatPNG:
		return "png"
	default:
		return "webp"
	}
}

// Default values for spec fields. Picked to match imageproc so a
// caller that doesn't pass a token gets the same output shape.
const (
	// DefaultQuality is the encoder quality applied when the spec
	// omits a q-N token. Matches imageproc.ProcessOptions.Quality's
	// applied default.
	DefaultQuality = 82

	// MaxDimension caps the requested width/height. The proxy refuses
	// to render a >8K image because the resulting bytes are unlikely
	// to be useful (no display can show them) and the memory footprint
	// (~256 MiB for an 8K RGBA) is large enough to be a DoS vector.
	MaxDimension = 8192
)

// ErrInvalidSpec is returned by Parse when the spec string fails
// validation. The wrapped error carries a human-readable reason.
var ErrInvalidSpec = errors.New("imgproxy: invalid spec")

// Canonical returns the canonical string form of s — dot-separated
// tokens in a deterministic order. Used by the cache layer and by
// the coalescer key extractor so semantically equivalent specs
// (e.g., "h-600.w-800" vs "w-800.h-600") collapse to one cache
// entry and one in-flight render.
//
// Order: w, h, q, fit, format. We always emit every token, even
// the defaults — the canonical key is a fingerprint of the
// post-defaulted Spec, not of the input string.
func (s Spec) Canonical() string {
	parts := make([]string, 0, 5)
	if s.Width > 0 {
		parts = append(parts, "w-"+strconv.Itoa(s.Width))
	}
	if s.Height > 0 {
		parts = append(parts, "h-"+strconv.Itoa(s.Height))
	}
	parts = append(parts, "q-"+strconv.Itoa(s.Quality))
	parts = append(parts, "fit-"+string(s.Fit))
	parts = append(parts, string(s.Format))
	return strings.Join(parts, ".")
}

// Parse turns a raw spec string into a validated Spec. Returns
// ErrInvalidSpec (wrapped with a reason) on any failure — empty
// input, unknown token, out-of-range value, conflicting tokens.
//
// The grammar:
//
//	spec   := token ("." token)*
//	token  := dimension | quality | fit | format
//	dimension := ("w" | "h") "-" digit+
//	quality   := "q" "-" digit+
//	fit       := "fit" "-" ("cover" | "contain")
//	format    := "webp" | "jpeg" | "jpg" | "png"
//
// Unknown tokens are rejected rather than silently ignored — an
// allowlist is the safer posture because a typo in the URL ("fit-cove")
// should surface as a 400, not as "default fit silently used".
func Parse(raw string) (Spec, error) {
	if raw == "" {
		return Spec{}, fmt.Errorf("%w: spec is empty", ErrInvalidSpec)
	}
	if strings.ContainsAny(raw, "/\\") {
		// Defensive: the route uses the spec as a path segment, so a
		// slash would slip past the router's pattern check. The handler
		// extracts the value via r.PathValue which won't include path
		// separators, but parsing the literal token gives us a clear
		// error if anyone re-uses Parse outside that context.
		return Spec{}, fmt.Errorf("%w: spec must not contain path separators", ErrInvalidSpec)
	}
	if len(raw) > 256 {
		// 256 bytes is large enough for any reasonable combination of
		// allowed tokens (w-8192.h-8192.q-100.fit-contain.webp is 33
		// bytes) and small enough that a deliberately bloated spec
		// can't push the cache-key string into the megabyte range.
		return Spec{}, fmt.Errorf("%w: spec exceeds 256 bytes", ErrInvalidSpec)
	}

	s := Spec{
		Quality: DefaultQuality,
		Fit:     FitCover,
		Format:  FormatWebP,
	}
	seen := make(map[string]struct{}, 5)
	tokens := strings.Split(raw, ".")
	for _, tok := range tokens {
		if tok == "" {
			return Spec{}, fmt.Errorf("%w: empty token", ErrInvalidSpec)
		}
		key, value, hasValue := splitToken(tok)
		// Format tokens are valueless ("webp", "png", "jpeg"); the
		// allowlist check handles them in the default branch below.
		switch {
		case key == "w" && hasValue:
			if _, dup := seen["w"]; dup {
				return Spec{}, fmt.Errorf("%w: duplicate w token", ErrInvalidSpec)
			}
			seen["w"] = struct{}{}
			w, err := strconv.Atoi(value)
			if err != nil || w < 1 || w > MaxDimension {
				return Spec{}, fmt.Errorf("%w: w out of range 1..%d", ErrInvalidSpec, MaxDimension)
			}
			s.Width = w
		case key == "h" && hasValue:
			if _, dup := seen["h"]; dup {
				return Spec{}, fmt.Errorf("%w: duplicate h token", ErrInvalidSpec)
			}
			seen["h"] = struct{}{}
			h, err := strconv.Atoi(value)
			if err != nil || h < 1 || h > MaxDimension {
				return Spec{}, fmt.Errorf("%w: h out of range 1..%d", ErrInvalidSpec, MaxDimension)
			}
			s.Height = h
		case key == "q" && hasValue:
			if _, dup := seen["q"]; dup {
				return Spec{}, fmt.Errorf("%w: duplicate q token", ErrInvalidSpec)
			}
			seen["q"] = struct{}{}
			q, err := strconv.Atoi(value)
			if err != nil || q < 1 || q > 100 {
				return Spec{}, fmt.Errorf("%w: q out of range 1..100", ErrInvalidSpec)
			}
			s.Quality = q
		case key == "fit" && hasValue:
			if _, dup := seen["fit"]; dup {
				return Spec{}, fmt.Errorf("%w: duplicate fit token", ErrInvalidSpec)
			}
			seen["fit"] = struct{}{}
			switch FitMode(value) {
			case FitCover, FitContain:
				s.Fit = FitMode(value)
			default:
				return Spec{}, fmt.Errorf("%w: unknown fit %q", ErrInvalidSpec, value)
			}
		default:
			// No "key-value" split — must be a format token, or it's
			// unknown. Treat "jpg" as an alias for "jpeg" so callers
			// that copy the URL from a file manager don't get a 400.
			if hasValue {
				return Spec{}, fmt.Errorf("%w: unknown token %q", ErrInvalidSpec, tok)
			}
			if _, dup := seen["format"]; dup {
				return Spec{}, fmt.Errorf("%w: duplicate format token", ErrInvalidSpec)
			}
			seen["format"] = struct{}{}
			switch strings.ToLower(tok) {
			case "webp":
				s.Format = FormatWebP
			case "jpeg", "jpg":
				s.Format = FormatJPEG
			case "png":
				s.Format = FormatPNG
			default:
				return Spec{}, fmt.Errorf("%w: unknown token %q", ErrInvalidSpec, tok)
			}
		}
	}

	if s.Width == 0 && s.Height == 0 {
		return Spec{}, fmt.Errorf("%w: at least one of w or h is required", ErrInvalidSpec)
	}

	return s, nil
}

// splitToken splits "key-value" into (key, value, true). A token
// without a "-" returns (token, "", false). Helper for Parse.
func splitToken(tok string) (string, string, bool) {
	idx := strings.IndexByte(tok, '-')
	if idx < 0 {
		return tok, "", false
	}
	return tok[:idx], tok[idx+1:], true
}

// allowedKeys is the set of token keys the parser recognises. Used by
// the AllowedKeys helper so callers can build documentation or error
// messages off the same source of truth.
var allowedKeys = []string{"w", "h", "q", "fit"}

// AllowedKeys returns the list of recognised key-value token keys
// (excluding format which is positional). Sorted alphabetically for
// determinism.
func AllowedKeys() []string {
	out := make([]string, len(allowedKeys))
	copy(out, allowedKeys)
	sort.Strings(out)
	return out
}
