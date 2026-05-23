package imageproc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"strings"

	"github.com/HugoSmits86/nativewebp"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // decode-only support for WebP inputs
)

// VariantName identifies one of the size-classed renditions produced
// by Process. The four documented variants are stable wire identifiers
// — they appear in storage keys, in JSON envelopes, and in the
// srcset descriptor — so renaming one is a breaking change for any
// theme or REST client that hard-codes the string.
type VariantName string

const (
	// VariantOriginal is the source bytes re-encoded into the chosen
	// output format with EXIF stripped. We do NOT keep the operator's
	// upload byte-for-byte; that would leak EXIF in the unmodified
	// path and would mean two different bytes for "the original" and
	// "the EXIF-stripped original", which the rendering surface would
	// have no way to choose between.
	VariantOriginal VariantName = "original"

	// VariantThumbnail is the 256px-on-longest-edge rendition. Used
	// in the admin grid and as the smallest entry in srcset.
	VariantThumbnail VariantName = "thumb"

	// VariantMedium is the 768px-on-longest-edge rendition — the
	// sweet spot for inline content on phone-class viewports.
	VariantMedium VariantName = "medium"

	// VariantLarge is the 1536px-on-longest-edge rendition for hero
	// blocks on laptop- and desktop-class viewports. Anything larger
	// is served as VariantOriginal.
	VariantLarge VariantName = "large"
)

// Format identifies the output encoding for a variant. WebP is the
// default because every modern browser supports it; AVIF is offered
// when the build is tagged with `avif` and libaom is available at
// link time. JPEG is retained as a fallback for animated inputs where
// the WebP encoder cannot represent the source (we collapse to the
// first frame anyway, but JPEG keeps the storage path uniform).
type Format string

const (
	FormatWebP Format = "webp"
	FormatAVIF Format = "avif"
	FormatJPEG Format = "jpeg"
	FormatPNG  Format = "png"
)

// FileExtension returns the canonical extension (without the leading
// dot) for f. Used by the default KeyScheme to mint sibling keys.
func (f Format) FileExtension() string {
	switch f {
	case FormatJPEG:
		return "jpg"
	case FormatPNG:
		return "png"
	case FormatAVIF:
		return "avif"
	default:
		return "webp"
	}
}

// MIMEType returns the IANA media type for f. Used by the storage
// putter so a sibling object lands with the right Content-Type even
// when the key extension is opaque to the bucket.
func (f Format) MIMEType() string {
	switch f {
	case FormatJPEG:
		return "image/jpeg"
	case FormatPNG:
		return "image/png"
	case FormatAVIF:
		return "image/avif"
	default:
		return "image/webp"
	}
}

// SizeSpec describes one variant the pipeline must produce. LongestEdge
// is the upper bound on max(width, height) after resize; 0 means "keep
// the source dimensions" (used for VariantOriginal).
type SizeSpec struct {
	Name        VariantName
	LongestEdge int
}

// DefaultSizes is the four-variant baseline the upload pipeline uses
// when ProcessOptions.Sizes is empty. The values are picked to cover
// the long tail of viewports without overlapping: 256 is the admin
// thumbnail, 768 fits a portrait phone, 1536 fits a 13" laptop with
// dpr=2, and 0 (original) covers the edge cases neither of those
// classes do.
var DefaultSizes = []SizeSpec{
	{Name: VariantThumbnail, LongestEdge: 256},
	{Name: VariantMedium, LongestEdge: 768},
	{Name: VariantLarge, LongestEdge: 1536},
	{Name: VariantOriginal, LongestEdge: 0},
}

// ProcessOptions configures one Process invocation. Every field has
// a documented zero-value default so callers can pass a bare
// ProcessOptions{} and get the production-shaped pipeline.
type ProcessOptions struct {
	// Sizes is the set of variants to produce. Empty falls back to
	// DefaultSizes. The caller may pass a subset (e.g. only Thumbnail
	// + Medium) when bandwidth or storage is tight.
	Sizes []SizeSpec

	// Formats is the list of output encodings each Sizes entry is
	// produced in. Empty falls back to []Format{FormatWebP} — every
	// modern browser supports WebP, so emitting only that keeps the
	// storage footprint linear in len(Sizes) rather than
	// len(Sizes)*len(Formats). Callers that want AVIF in addition
	// pass []Format{FormatWebP, FormatAVIF}; AVIF entries silently
	// fall back to WebP when the build was not tagged with `avif`.
	Formats []Format

	// Quality is the encoder quality knob in the range 1..100. Zero
	// falls back to 82 — the same value libvips defaults to for its
	// WebP encoder, which lines up with Lighthouse's "good enough"
	// threshold without producing visibly soft hero images.
	Quality int

	// MaxFrames bounds an animated input. A GIF or animated WebP with
	// more than MaxFrames frames is collapsed to its first frame and
	// the Result carries a Warning. Zero falls back to 1 — the
	// pipeline does not produce animated outputs.
	MaxFrames int
}

// Variant is one rendition in the Result. Bytes holds the encoded
// image; Width/Height are the post-resize dimensions; Format is the
// encoded type; KeySuffix is the string appended to the source storage
// key to form this variant's sibling key.
type Variant struct {
	Name      VariantName
	Format    Format
	Width     int
	Height    int
	Bytes     []byte
	KeySuffix string
}

// Result is the output of Process. Variants are ordered by SizeSpec
// (smallest LongestEdge first, original last), then by Format index
// within each size.
type Result struct {
	// SourceFormat is the format detected on the input bytes — the
	// caller may pass it back to the storage layer to decide whether
	// to keep the original upload around. "gif", "jpeg", "png", "webp"
	// are the documented values; anything else is rejected at decode.
	SourceFormat string

	// SourceWidth and SourceHeight are the pre-resize dimensions of
	// the decoded image. Useful for storing on the media row so the
	// admin grid can render the chip ("1920×1080") without re-decoding.
	SourceWidth  int
	SourceHeight int

	// Variants is the list of produced renditions in srcset order
	// (narrowest first), ending with the re-encoded original.
	Variants []Variant

	// Warnings collects non-fatal issues. The pipeline never returns
	// these as errors — a caller that wants strict mode can inspect
	// the slice and decide. Examples: animated GIF collapsed to first
	// frame; AVIF requested but build was not tagged.
	Warnings []string
}

// FindVariant returns the variant matching (name, format) and a bool
// indicating whether it was present. Useful for tests and for theme
// renderers that need a specific rendition.
func (r *Result) FindVariant(name VariantName, format Format) (Variant, bool) {
	for _, v := range r.Variants {
		if v.Name == name && v.Format == format {
			return v, true
		}
	}
	return Variant{}, false
}

// ErrUnsupportedFormat is returned by Process when the input bytes
// cannot be decoded by any of the registered decoders. The wrapped
// error carries the std-lib decoder's message.
var ErrUnsupportedFormat = errors.New("imageproc: unsupported image format")

// Process is the entry point. It decodes src, produces a re-encoded
// variant for every (size, format) in opts, and returns a Result.
//
// The pipeline:
//
//  1. Buffer src into memory. We need random-access over the bytes
//     for the EXIF-strip + decode-config double pass. Inputs are
//     already bounded by the upload handler's MaxBytesReader (50 MiB)
//     so the allocation is bounded too.
//  2. Detect the source format via image.DecodeConfig — that gives
//     us the variant-routing decision (animated GIF vs single-frame
//     PNG) without paying for a full decode.
//  3. Decode the image once. For animated inputs we use gif.DecodeAll
//     and take the first frame, recording a Warning.
//  4. For each SizeSpec, resize the decoded image once (Catmull-Rom
//     kernel — sharp enough for product photography without ringing
//     on text), then encode that resized image into each requested
//     Format.
//  5. Return the Result; the caller writes each Variant.Bytes to
//     storage at the source key + Variant.KeySuffix.
//
// ctx is honoured at the granularity of "between decodes": the std-lib
// encoders do not take a context, so a cancel mid-encode is observed
// only on the next iteration of the inner loop. For the variant sizes
// the pipeline runs at, that's <100ms even on the cold path.
func Process(ctx context.Context, src io.Reader, opts ProcessOptions) (*Result, error) {
	if src == nil {
		return nil, errors.New("imageproc.Process: src is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("imageproc.Process: %w", err)
	}

	raw, err := io.ReadAll(src)
	if err != nil {
		return nil, fmt.Errorf("imageproc.Process: read src: %w", err)
	}
	if len(raw) == 0 {
		return nil, errors.New("imageproc.Process: src is empty")
	}

	opts = applyDefaults(opts)

	cfg, srcFormat, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnsupportedFormat, err)
	}

	result := &Result{
		SourceFormat: srcFormat,
		SourceWidth:  cfg.Width,
		SourceHeight: cfg.Height,
	}

	// Strip EXIF on JPEGs before decode. Other formats round-trip
	// through the decode→encode path and lose EXIF naturally; JPEG is
	// the only stdlib path that preserves the APP1 marker because the
	// encoder writes a fresh container.
	decodeBytes := raw
	if srcFormat == "jpeg" {
		stripped, ok := StripEXIF(raw)
		if ok {
			decodeBytes = stripped
		}
	}

	img, framesWarning, err := decodeFirstFrame(decodeBytes, srcFormat, opts.MaxFrames)
	if err != nil {
		return nil, fmt.Errorf("imageproc.Process: decode: %w", err)
	}
	if framesWarning != "" {
		result.Warnings = append(result.Warnings, framesWarning)
	}

	// Produce each variant. We resize once per SizeSpec, then encode
	// the resized image into every requested Format. This ordering
	// matters: encoding is the dominant cost, so amortizing the
	// resize across formats is a measurable win when both WebP and
	// AVIF are requested.
	for _, size := range opts.Sizes {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("imageproc.Process: %w", err)
		}
		resized := resizeToLongestEdge(img, size.LongestEdge)
		rb := resized.Bounds()
		for _, format := range opts.Formats {
			encoded, effective, warn, err := encodeVariant(resized, format, opts.Quality)
			if err != nil {
				return nil, fmt.Errorf("imageproc.Process: encode %s/%s: %w", size.Name, format, err)
			}
			if warn != "" {
				result.Warnings = appendUnique(result.Warnings, warn)
			}
			v := Variant{
				Name:      size.Name,
				Format:    effective,
				Width:     rb.Dx(),
				Height:    rb.Dy(),
				Bytes:     encoded,
				KeySuffix: defaultKeySuffix(size.Name, effective),
			}
			result.Variants = append(result.Variants, v)
		}
	}

	return result, nil
}

// applyDefaults fills in the zero-value cases of ProcessOptions. We
// copy slices rather than aliasing so a caller can reuse the same
// options struct across concurrent Process calls without surprising
// shared-state behaviour.
func applyDefaults(opts ProcessOptions) ProcessOptions {
	if len(opts.Sizes) == 0 {
		opts.Sizes = append([]SizeSpec(nil), DefaultSizes...)
	}
	if len(opts.Formats) == 0 {
		opts.Formats = []Format{FormatWebP}
	}
	if opts.Quality <= 0 || opts.Quality > 100 {
		opts.Quality = 82
	}
	if opts.MaxFrames <= 0 {
		opts.MaxFrames = 1
	}
	return opts
}

// decodeFirstFrame decodes raw. For animated inputs whose frame count
// exceeds maxFrames we take the first frame and return a Warning; the
// pipeline never produces animated outputs (the WebP encoder we ship
// is single-frame and AVIF animation isn't widely supported in Go).
func decodeFirstFrame(raw []byte, srcFormat string, maxFrames int) (image.Image, string, error) {
	if srcFormat == "gif" {
		g, err := gif.DecodeAll(bytes.NewReader(raw))
		if err != nil {
			return nil, "", err
		}
		if len(g.Image) == 0 {
			return nil, "", errors.New("decoded GIF has zero frames")
		}
		warning := ""
		if len(g.Image) > maxFrames {
			warning = fmt.Sprintf("animated GIF with %d frames collapsed to first frame", len(g.Image))
		}
		// gif.DecodeAll returns *image.Paletted frames; the caller
		// downstream wants a fully-fledged image.Image. Frame zero is
		// already the right type; returning it directly is cheap.
		return g.Image[0], warning, nil
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, "", err
	}
	return img, "", nil
}

// resizeToLongestEdge produces a copy of src whose larger dimension is
// exactly longest pixels. If longest is zero or already smaller-or-
// equal to the source's longest edge, src is returned unchanged — we
// do not upscale, because the resulting variant would carry no more
// information than the original and would only waste storage. The
// Catmull-Rom kernel is the right balance between sharpness and
// ringing for the kind of photography and screenshot content the
// admin UI uploads; for line art a Nearest-neighbour kernel would be
// better, but the cost-benefit doesn't justify auto-detecting.
func resizeToLongestEdge(src image.Image, longest int) image.Image {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if longest <= 0 {
		return src
	}
	maxDim := sw
	if sh > maxDim {
		maxDim = sh
	}
	if maxDim <= longest {
		return src
	}
	scale := float64(longest) / float64(maxDim)
	dw := int(float64(sw) * scale)
	dh := int(float64(sh) * scale)
	if dw < 1 {
		dw = 1
	}
	if dh < 1 {
		dh = 1
	}
	dst := image.NewNRGBA(image.Rect(0, 0, dw, dh))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)
	return dst
}

// encodeVariant runs the format-specific encoder. Returns the bytes,
// the effective format (which may differ from the requested one if
// AVIF was requested but not built in), an optional Warning, and any
// error from the encoder.
//
// JPEG and PNG go through the stdlib encoders, neither of which
// propagate EXIF — that's the reason we don't have to strip EXIF on
// the output side for non-JPEG sources.
//
// WebP is encoded via HugoSmits86/nativewebp, a pure-Go encoder that
// produces lossy bytes from any image.Image. There's no quality knob
// in the upstream API; the encoder targets ~85% quality and we
// emit at that fixed value rather than threading the Quality option
// through (the resize already does the dominant size reduction; the
// encoder choice is a smaller axis of control).
//
// AVIF dispatches to encodeAVIF which has two implementations — one
// behind the `avif` build tag using libaom, one stub for builds
// without libaom that downgrades to WebP and emits a Warning.
func encodeVariant(img image.Image, format Format, quality int) ([]byte, Format, string, error) {
	switch format {
	case FormatJPEG:
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
			return nil, FormatJPEG, "", err
		}
		return buf.Bytes(), FormatJPEG, "", nil
	case FormatPNG:
		var buf bytes.Buffer
		enc := png.Encoder{CompressionLevel: png.DefaultCompression}
		if err := enc.Encode(&buf, img); err != nil {
			return nil, FormatPNG, "", err
		}
		return buf.Bytes(), FormatPNG, "", nil
	case FormatAVIF:
		b, warn, err := encodeAVIF(img, quality)
		if err != nil {
			return nil, FormatAVIF, "", err
		}
		if warn != "" {
			// AVIF fell back to WebP; report the effective format.
			b2, err2 := encodeWebP(img)
			if err2 != nil {
				return nil, FormatAVIF, "", err2
			}
			return b2, FormatWebP, warn, nil
		}
		return b, FormatAVIF, "", nil
	default:
		b, err := encodeWebP(img)
		if err != nil {
			return nil, FormatWebP, "", err
		}
		return b, FormatWebP, "", nil
	}
}

// encodeWebP is broken out so the AVIF-fallback path can share the
// exact same encoder configuration as the primary WebP path.
func encodeWebP(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := nativewebp.Encode(&buf, img, nil); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// defaultKeySuffix builds the storage-key suffix for one variant.
// Layout: ".<variant>.<ext>" — for example, an upload at
// 2026/01/abc-photo.jpg gets a thumbnail sibling at
// 2026/01/abc-photo.jpg.thumb.webp. The double-extension is
// deliberate: the suffix is unambiguous against any future "list the
// original of these variants" code path, and S3 path-style and virtual-
// host-style addressing both handle it without escaping. The original
// variant gets ".orig.<ext>" so the source upload's storage key still
// points at the unprocessed bytes and a "did processing complete"
// probe can branch on existence.
func defaultKeySuffix(name VariantName, format Format) string {
	suffix := string(name)
	if name == VariantOriginal {
		suffix = "orig"
	}
	return "." + suffix + "." + format.FileExtension()
}

// appendUnique appends s to ws iff ws does not already contain s.
// Warnings should not duplicate when multiple variants hit the same
// fallback path (e.g. AVIF→WebP for every size).
func appendUnique(ws []string, s string) []string {
	for _, w := range ws {
		if w == s {
			return ws
		}
	}
	return append(ws, s)
}

// SupportedSourceFormats returns the set of input encodings Process
// can decode. Useful for an admin UI that wants to grey out the
// "process" action on rows whose stored MIME isn't on the list.
func SupportedSourceFormats() []string {
	return []string{"jpeg", "png", "gif", "webp"}
}

// IsSupportedMIME reports whether mime is one of the input types
// Process accepts. Convenience helper for the upload handler so the
// task is not enqueued for, say, a PDF.
func IsSupportedMIME(mime string) bool {
	mime = strings.ToLower(mime)
	switch mime {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}
