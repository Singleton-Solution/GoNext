package imgproxy

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"sync"
	"testing"
)

// makeJPEG renders a w×h test JPEG with a colour gradient. Mirrors
// the helper in imageproc_test so the tests in the two packages
// share a recognisable sample-image story.
func makeJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 7), G: uint8(y * 11), B: 60, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("encode test JPEG: %v", err)
	}
	return buf.Bytes()
}

// makePNG builds a w×h opaque PNG for tests that want a different
// source format than JPEG.
func makePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: 100, G: uint8(x), B: uint8(y), A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode test PNG: %v", err)
	}
	return buf.Bytes()
}

// decodeJPEG returns the bounds of a JPEG buffer, asserting it
// decodes cleanly. Used by tests that need to verify the
// transformer produced the right dimensions.
func decodeJPEG(t *testing.T, raw []byte) image.Rectangle {
	t.Helper()
	img, err := jpeg.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("decode jpeg: %v", err)
	}
	return img.Bounds()
}

func TestStdlibTransformer_CoverExactDimensions(t *testing.T) {
	src := makeJPEG(t, 400, 200)
	tr := NewStdlibTransformer()
	spec := Spec{Width: 100, Height: 100, Quality: 80, Fit: FitCover, Format: FormatJPEG}

	res, err := tr.Transform(context.Background(), bytes.NewReader(src), spec)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if res.Width != 100 || res.Height != 100 {
		t.Fatalf("want 100x100, got %dx%d", res.Width, res.Height)
	}
	if res.Format != FormatJPEG {
		t.Fatalf("want jpeg, got %s", res.Format)
	}
	if res.Backend != BackendStdlib {
		t.Fatalf("want stdlib backend, got %s", res.Backend)
	}
	if b := decodeJPEG(t, res.Bytes); b.Dx() != 100 || b.Dy() != 100 {
		t.Fatalf("decoded bounds: want 100x100, got %v", b)
	}
}

func TestStdlibTransformer_ContainPreservesAspect(t *testing.T) {
	src := makeJPEG(t, 400, 200)
	tr := NewStdlibTransformer()
	spec := Spec{Width: 100, Height: 100, Quality: 80, Fit: FitContain, Format: FormatJPEG}

	res, err := tr.Transform(context.Background(), bytes.NewReader(src), spec)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	// Source is 2:1. Contain into 100x100 produces 100x50.
	if res.Width != 100 || res.Height != 50 {
		t.Fatalf("want 100x50, got %dx%d", res.Width, res.Height)
	}
}

func TestStdlibTransformer_WidthOnlyPreservesAspect(t *testing.T) {
	src := makeJPEG(t, 400, 200)
	tr := NewStdlibTransformer()
	spec := Spec{Width: 100, Quality: 80, Fit: FitCover, Format: FormatJPEG}

	res, err := tr.Transform(context.Background(), bytes.NewReader(src), spec)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if res.Width != 100 || res.Height != 50 {
		t.Fatalf("want 100x50, got %dx%d", res.Width, res.Height)
	}
}

func TestStdlibTransformer_NoUpscale(t *testing.T) {
	// Source smaller than requested box — the transformer should
	// return the source dimensions unchanged.
	src := makeJPEG(t, 100, 100)
	tr := NewStdlibTransformer()
	spec := Spec{Width: 800, Height: 800, Quality: 80, Fit: FitCover, Format: FormatJPEG}

	res, err := tr.Transform(context.Background(), bytes.NewReader(src), spec)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if res.Width != 100 || res.Height != 100 {
		t.Fatalf("want 100x100 (no upscale), got %dx%d", res.Width, res.Height)
	}
}

func TestStdlibTransformer_PNGSource(t *testing.T) {
	src := makePNG(t, 200, 200)
	tr := NewStdlibTransformer()
	spec := Spec{Width: 50, Quality: 80, Fit: FitCover, Format: FormatWebP}

	res, err := tr.Transform(context.Background(), bytes.NewReader(src), spec)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if res.Width != 50 || res.Height != 50 {
		t.Fatalf("want 50x50, got %dx%d", res.Width, res.Height)
	}
	if res.Format != FormatWebP {
		t.Fatalf("want webp output, got %s", res.Format)
	}
}

func TestStdlibTransformer_EmptySource(t *testing.T) {
	tr := NewStdlibTransformer()
	_, err := tr.Transform(context.Background(), bytes.NewReader(nil), Spec{Width: 100})
	if !errors.Is(err, ErrEmptySource) {
		t.Fatalf("want ErrEmptySource, got %v", err)
	}
}

func TestStdlibTransformer_UnsupportedFormat(t *testing.T) {
	tr := NewStdlibTransformer()
	_, err := tr.Transform(context.Background(), bytes.NewReader([]byte("not an image")), Spec{Width: 100})
	if !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("want ErrUnsupportedFormat, got %v", err)
	}
}

func TestStdlibTransformer_NilContextRejected(t *testing.T) {
	tr := NewStdlibTransformer()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before call
	_, err := tr.Transform(ctx, bytes.NewReader(makeJPEG(t, 10, 10)), Spec{Width: 5})
	if err == nil {
		t.Fatal("expected context error")
	}
}

func TestDefault_FallsBackToStdlibWithoutVips(t *testing.T) {
	// Builds without the vips tag must produce a stdlib transformer.
	// Reset the package-level once to exercise the selection.
	defaultOnce = sync.Once{}
	defaultImpl = nil
	defaultBack = ""

	t.Setenv("GONEXT_IMAGEPROC", "")
	tr := Default()
	if tr == nil {
		t.Fatal("Default returned nil")
	}
	// The package-level state should reflect a successful selection.
	if DefaultBackend() == "" {
		t.Fatal("DefaultBackend empty after Default()")
	}
}
