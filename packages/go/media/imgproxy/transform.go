package imgproxy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"os"
	"sync"

	"github.com/HugoSmits86/nativewebp"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // decode-only support for WebP inputs
)

// Backend identifies the transformer implementation. Selected at
// runtime via the GONEXT_IMAGEPROC environment variable; the
// govips path is only available in builds compiled with the `vips`
// build tag (apps/api/Dockerfile sets the right env so a release
// container picks govips, and `go build` without the tag silently
// falls back to stdlib for the development loop).
type Backend string

const (
	// BackendStdlib uses image/jpeg, image/png and the pure-Go
	// HugoSmits86/nativewebp encoder. Always available.
	BackendStdlib Backend = "stdlib"

	// BackendGovips uses github.com/davidbyttow/govips/v2/vips. Only
	// selected when the build was compiled with the `vips` build tag
	// AND govips.Startup() returns no error.
	BackendGovips Backend = "govips"
)

// envBackend is the environment variable the operator sets to pin
// the backend. Documented in the package doc; the value is
// case-insensitive.
const envBackend = "GONEXT_IMAGEPROC"

// ErrEmptySource is returned by Transform when the input reader
// yields no bytes. Surfaced as a distinct sentinel so the HTTP
// handler can distinguish "client uploaded an empty file" (400) from
// "transform failed mid-decode" (500).
var ErrEmptySource = errors.New("imgproxy: source is empty")

// ErrUnsupportedFormat is returned when the input bytes cannot be
// decoded by any of the registered decoders. The wrapped error
// carries the decoder's error message.
var ErrUnsupportedFormat = errors.New("imgproxy: unsupported source format")

// Result is the output of Transform. Bytes holds the encoded image;
// Width and Height are the post-resize dimensions; Format echoes
// the requested format (or the effective one if the transformer
// fell back).
type Result struct {
	Bytes   []byte
	Width   int
	Height  int
	Format  Format
	Backend Backend
}

// Transformer is the interface both backends implement. Kept as an
// interface (rather than a switch in Transform) so a test can inject
// a deterministic stub without exporting the env var.
type Transformer interface {
	Transform(ctx context.Context, src io.Reader, spec Spec) (*Result, error)
}

// stdlibTransformer is the always-available pure-Go backend. The
// receiver is empty because the backend holds no per-instance state
// — image/jpeg etc. are safe for concurrent use.
type stdlibTransformer struct{}

// NewStdlibTransformer returns a Transformer that uses the pure-Go
// encoders. Exported so callers can pin to stdlib explicitly (e.g.,
// a test that wants to verify the fallback path).
func NewStdlibTransformer() Transformer {
	return stdlibTransformer{}
}

// Transform implements the Transformer interface for the stdlib
// backend. The pipeline:
//
//  1. Read src into memory. We need random-access for the decode
//     pass, and the route handler bounds the source size upstream so
//     the buffering is bounded.
//  2. Decode once via image.Decode (which dispatches to JPEG / PNG
//     / WebP / GIF based on magic bytes).
//  3. Compute the target dimensions from the Spec and source bounds.
//  4. Resize via golang.org/x/image/draw with the Catmull-Rom
//     kernel — the same kernel imageproc uses for the upload-time
//     variants. For FitCover we crop the source before scaling so
//     the resulting bitmap has exactly (W, H) pixels.
//  5. Encode into the requested format.
func (stdlibTransformer) Transform(ctx context.Context, src io.Reader, spec Spec) (*Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("imgproxy.Transform: %w", err)
	}
	if src == nil {
		return nil, errors.New("imgproxy.Transform: src is nil")
	}

	raw, err := io.ReadAll(src)
	if err != nil {
		return nil, fmt.Errorf("imgproxy.Transform: read src: %w", err)
	}
	if len(raw) == 0 {
		return nil, ErrEmptySource
	}

	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnsupportedFormat, err)
	}

	resized := resizeStdlib(img, spec)
	rb := resized.Bounds()

	encoded, effFormat, err := encodeStdlib(resized, spec)
	if err != nil {
		return nil, fmt.Errorf("imgproxy.Transform: encode %s: %w", spec.Format, err)
	}

	return &Result{
		Bytes:   encoded,
		Width:   rb.Dx(),
		Height:  rb.Dy(),
		Format:  effFormat,
		Backend: BackendStdlib,
	}, nil
}

// resizeStdlib produces a copy of src sized to match the Spec. The
// caller asks for at least one of Width/Height; the other is
// derived from the source aspect ratio so the output is never
// distorted. FitCover crops the source's center to the requested
// aspect ratio before scaling; FitContain scales the longer edge to
// the requested box.
//
// The Catmull-Rom kernel matches what imageproc uses for the upload-
// time pipeline. The Over operator is fine for both opaque and
// transparent inputs because we draw onto a freshly allocated
// destination.
func resizeStdlib(src image.Image, spec Spec) image.Image {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if sw == 0 || sh == 0 {
		return src
	}

	tw, th := targetDimensions(sw, sh, spec)
	if tw == sw && th == sh {
		return src
	}

	if spec.Fit == FitCover && spec.Width > 0 && spec.Height > 0 {
		// Compute the source rectangle that has the same aspect ratio
		// as the target box, centred on the source. Scaling that rect
		// into the target box produces a "fill, no letterbox" image
		// without explicit cropping in two passes.
		srcAR := float64(sw) / float64(sh)
		dstAR := float64(tw) / float64(th)
		var cropW, cropH int
		if srcAR > dstAR {
			// Source is wider than target — crop horizontally.
			cropH = sh
			cropW = int(float64(sh) * dstAR)
		} else {
			// Source is taller than target — crop vertically.
			cropW = sw
			cropH = int(float64(sw) / dstAR)
		}
		if cropW < 1 {
			cropW = 1
		}
		if cropH < 1 {
			cropH = 1
		}
		offX := b.Min.X + (sw-cropW)/2
		offY := b.Min.Y + (sh-cropH)/2
		cropRect := image.Rect(offX, offY, offX+cropW, offY+cropH)
		dst := image.NewNRGBA(image.Rect(0, 0, tw, th))
		draw.CatmullRom.Scale(dst, dst.Bounds(), src, cropRect, draw.Over, nil)
		return dst
	}

	dst := image.NewNRGBA(image.Rect(0, 0, tw, th))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)
	return dst
}

// targetDimensions computes the (width, height) after resize for a
// source of (sw, sh) pixels given spec. The caller has already
// validated that at least one of spec.Width / spec.Height is set.
//
// The rules:
//
//   - Both Width and Height set: the output is exactly (W, H). The
//     resize routine adapts to FitCover by cropping; FitContain
//     scales to fit and may produce a smaller box on one axis,
//     which targetDimensions accounts for.
//
//   - Only Width set: scale to (W, W*sh/sw) preserving aspect ratio.
//
//   - Only Height set: symmetric — (H*sw/sh, H).
//
// No upscaling — if the source is already smaller than the requested
// box, the source dimensions are returned unchanged. Upscaling adds
// no information and would just waste cache space.
func targetDimensions(sw, sh int, spec Spec) (int, int) {
	w, h := spec.Width, spec.Height
	switch {
	case w > 0 && h > 0:
		if spec.Fit == FitContain {
			// Pick the larger scale factor that still fits the box.
			srcAR := float64(sw) / float64(sh)
			dstAR := float64(w) / float64(h)
			if srcAR > dstAR {
				// Width-bound.
				newW := w
				newH := int(float64(w) / srcAR)
				if newH < 1 {
					newH = 1
				}
				if newW > sw {
					return sw, sh
				}
				return newW, newH
			}
			// Height-bound.
			newH := h
			newW := int(float64(h) * srcAR)
			if newW < 1 {
				newW = 1
			}
			if newH > sh {
				return sw, sh
			}
			return newW, newH
		}
		// FitCover: target box is exactly (W, H). No upscale.
		if w > sw && h > sh {
			return sw, sh
		}
		return w, h
	case w > 0:
		if w > sw {
			return sw, sh
		}
		newH := int(float64(w) * float64(sh) / float64(sw))
		if newH < 1 {
			newH = 1
		}
		return w, newH
	default:
		// h > 0
		if h > sh {
			return sw, sh
		}
		newW := int(float64(h) * float64(sw) / float64(sh))
		if newW < 1 {
			newW = 1
		}
		return newW, h
	}
}

// encodeStdlib runs the format-specific stdlib encoder. JPEG honours
// spec.Quality directly; PNG ignores quality (the format is lossless)
// and uses default compression; WebP goes through nativewebp which
// does not accept a quality knob — the encoder is fixed at ~85%.
// In all three cases the effective format equals the requested
// format (no fallback for this backend).
func encodeStdlib(img image.Image, spec Spec) ([]byte, Format, error) {
	switch spec.Format {
	case FormatJPEG:
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: spec.Quality}); err != nil {
			return nil, FormatJPEG, err
		}
		return buf.Bytes(), FormatJPEG, nil
	case FormatPNG:
		var buf bytes.Buffer
		enc := png.Encoder{CompressionLevel: png.DefaultCompression}
		if err := enc.Encode(&buf, img); err != nil {
			return nil, FormatPNG, err
		}
		return buf.Bytes(), FormatPNG, nil
	default:
		var buf bytes.Buffer
		if err := nativewebp.Encode(&buf, img, nil); err != nil {
			return nil, FormatWebP, err
		}
		return buf.Bytes(), FormatWebP, nil
	}
}

// defaultTransformer is the package-level transformer selected via
// the env var at first use. Lazy-initialised so test binaries that
// don't touch image processing don't pay for the env probe.
var (
	defaultOnce sync.Once
	defaultImpl Transformer
	defaultBack Backend
	defaultLog  = slog.Default
)

// Default returns the package-level Transformer. The first call
// reads GONEXT_IMAGEPROC and selects between the govips and stdlib
// backends; subsequent calls return the same instance.
//
// If GONEXT_IMAGEPROC=govips and the build was tagged with `vips`,
// the govips backend is selected and Startup is called. If Startup
// fails (libvips missing at runtime), the package falls back to
// stdlib and logs a warning. If GONEXT_IMAGEPROC is unset, the
// default is govips when the build supports it, otherwise stdlib.
//
// Exported so the HTTP handler can wire the transformer once at
// boot and avoid the per-request env probe.
func Default() Transformer {
	defaultOnce.Do(func() {
		choice := selectBackend(os.Getenv(envBackend))
		if choice == BackendGovips {
			t, err := newGovipsTransformer()
			if err == nil {
				defaultImpl = t
				defaultBack = BackendGovips
				return
			}
			defaultLog().Warn(
				"imgproxy: govips backend requested but startup failed; falling back to stdlib",
				slog.String("err", err.Error()),
			)
		}
		defaultImpl = NewStdlibTransformer()
		defaultBack = BackendStdlib
	})
	return defaultImpl
}

// DefaultBackend returns the Backend selected by the first Default
// call. Returns "" if Default has not been invoked yet — callers
// that want the value at boot should call Default first, even if
// they discard the transformer.
func DefaultBackend() Backend {
	return defaultBack
}

// selectBackend maps the raw env value to a Backend choice. Unknown
// values fall back to govips (the preferred default) — the call
// site logs the actual choice after Startup.
func selectBackend(raw string) Backend {
	switch raw {
	case string(BackendStdlib):
		return BackendStdlib
	case string(BackendGovips), "":
		return BackendGovips
	default:
		// Operator typo'd the env var. Fall back to stdlib so the
		// service still boots; the warning in Default tells them
		// why their requested backend wasn't picked.
		defaultLog().Warn(
			"imgproxy: unknown GONEXT_IMAGEPROC value; falling back to stdlib",
			slog.String("value", raw),
		)
		return BackendStdlib
	}
}
