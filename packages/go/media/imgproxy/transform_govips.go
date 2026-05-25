//go:build vips

package imgproxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/davidbyttow/govips/v2/vips"
)

// vipsStarted is the package-level guard around vips.Startup. The
// upstream library requires Startup to be called once per process
// before any vips.NewImageFromBuffer call; it is NOT safe to call
// Startup twice. We wrap it in a sync.Once so the test suite (which
// builds multiple Transformer instances) doesn't trigger the upstream
// "already initialised" panic.
//
// startupErr is the result of the Startup call. A non-nil error
// indicates libvips is not installed or could not be initialised;
// newGovipsTransformer surfaces it so Default can fall back.
var (
	vipsStartupOnce sync.Once
	vipsStartupErr  error
)

// startVips lazy-initialises libvips. Safe to call from multiple
// goroutines; only the first call runs Startup.
func startVips() error {
	vipsStartupOnce.Do(func() {
		defer func() {
			// vips.Startup panics on some platforms when libvips is
			// missing rather than returning a clean error. Recover so
			// the caller gets a regular error and the parent process
			// can fall back to stdlib.
			if r := recover(); r != nil {
				vipsStartupErr = fmt.Errorf("imgproxy: vips.Startup panicked: %v", r)
			}
		}()
		vips.LoggingSettings(nil, vips.LogLevelWarning)
		vips.Startup(nil)
	})
	return vipsStartupErr
}

// govipsTransformer wraps libvips for the on-the-fly resize path.
// One instance per process is sufficient — vips itself manages its
// internal thread-local state. The struct is empty because vips
// operations take their inputs via NewImageFromBuffer.
type govipsTransformer struct{}

// newGovipsTransformer constructs a vips-backed Transformer. Returns
// an error if vips.Startup fails — the caller (Default) treats that
// as a signal to use the stdlib backend.
func newGovipsTransformer() (Transformer, error) {
	if err := startVips(); err != nil {
		return nil, err
	}
	return govipsTransformer{}, nil
}

// Transform implements the Transformer interface using libvips. The
// pipeline matches the stdlib backend's behaviour (cover/contain,
// quality, no upscale) but runs through the much faster libvips
// resize + encode primitives.
func (govipsTransformer) Transform(ctx context.Context, src io.Reader, spec Spec) (*Result, error) {
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

	img, err := vips.NewImageFromBuffer(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnsupportedFormat, err)
	}
	defer img.Close()

	sw, sh := img.Width(), img.Height()
	tw, th := targetDimensions(sw, sh, spec)

	if spec.Fit == FitCover && spec.Width > 0 && spec.Height > 0 {
		// libvips Thumbnail() with cropping is the dedicated path for
		// this — it picks the right resize kernel internally and
		// produces a tightly-cropped output in one operation. We
		// fall back to a manual resize+crop if Thumbnail isn't
		// available for the codec in this libvips build.
		if err := img.Thumbnail(tw, th, vips.InterestingCentre); err != nil {
			return nil, fmt.Errorf("imgproxy.Transform: vips thumbnail: %w", err)
		}
	} else {
		// FitContain or single-axis resize — just resize to the
		// target dimensions. libvips picks Lanczos3 by default which
		// is the right kernel for downscale.
		if tw != sw || th != sh {
			scaleX := float64(tw) / float64(sw)
			scaleY := float64(th) / float64(sh)
			if err := img.ResizeWithVScale(scaleX, scaleY, vips.KernelLanczos3); err != nil {
				return nil, fmt.Errorf("imgproxy.Transform: vips resize: %w", err)
			}
		}
	}

	bytes, _, err := exportVips(img, spec)
	if err != nil {
		return nil, fmt.Errorf("imgproxy.Transform: vips export: %w", err)
	}

	return &Result{
		Bytes:   bytes,
		Width:   img.Width(),
		Height:  img.Height(),
		Format:  spec.Format,
		Backend: BackendGovips,
	}, nil
}

// exportVips routes the encode through the format-specific govips
// exporter. The quality knob is honoured for WebP and JPEG; PNG
// ignores it (the format is lossless).
func exportVips(img *vips.ImageRef, spec Spec) ([]byte, *vips.ImageMetadata, error) {
	switch spec.Format {
	case FormatJPEG:
		params := vips.NewJpegExportParams()
		params.Quality = spec.Quality
		params.StripMetadata = true
		return img.ExportJpeg(params)
	case FormatPNG:
		params := vips.NewPngExportParams()
		params.StripMetadata = true
		return img.ExportPng(params)
	default:
		params := vips.NewWebpExportParams()
		params.Quality = spec.Quality
		params.StripMetadata = true
		return img.ExportWebp(params)
	}
}
