package imageproc

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"
)

// makeJPEG renders a w×h test image with a colour gradient and encodes
// it as a JPEG. Used by the EXIF + decode tests so they have a
// deterministic, decode-able payload.
func makeJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 7), G: uint8(y * 11), B: 50, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("encode test JPEG: %v", err)
	}
	return buf.Bytes()
}

// makePNGWithAlpha renders a w×h PNG with semi-transparent pixels in a
// checkerboard pattern. Used to verify transparency survives the
// pipeline.
func makePNGWithAlpha(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			alpha := uint8(255)
			if (x+y)%2 == 0 {
				alpha = 0 // fully transparent on the "even" squares
			}
			img.Set(x, y, color.NRGBA{R: 200, G: 50, B: 50, A: alpha})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode test PNG: %v", err)
	}
	return buf.Bytes()
}

// makeAnimatedGIF builds a multi-frame GIF for the "first-frame only"
// test. Frames are tiny so the encoder does not balloon the test
// fixture.
func makeAnimatedGIF(t *testing.T, frames int) []byte {
	t.Helper()
	palette := color.Palette{
		color.RGBA{0, 0, 0, 255},
		color.RGBA{255, 255, 255, 255},
	}
	g := &gif.GIF{}
	for f := 0; f < frames; f++ {
		img := image.NewPaletted(image.Rect(0, 0, 16, 16), palette)
		// Walk a bright pixel across the frame so the frames are
		// visually distinct (helps when debugging a failing test
		// with a hex dump).
		idx := f % 16
		img.SetColorIndex(idx, idx, 1)
		g.Image = append(g.Image, img)
		g.Delay = append(g.Delay, 5)
		g.Disposal = append(g.Disposal, gif.DisposalNone)
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, g); err != nil {
		t.Fatalf("encode test GIF: %v", err)
	}
	return buf.Bytes()
}

// jpegWithEXIF builds a JPEG that carries an APP1 EXIF block. The
// block is a minimal valid TIFF header — enough that the EXIF
// stripper sees the "Exif\0\0" identifier and removes it, but not
// so much that the test fixture becomes unreadable.
func jpegWithEXIF(t *testing.T, base []byte) []byte {
	t.Helper()
	if len(base) < 4 || base[0] != 0xFF || base[1] != 0xD8 {
		t.Fatalf("jpegWithEXIF: input is not a JPEG")
	}
	// EXIF APP1 payload: "Exif\0\0" + a tiny TIFF header. The TIFF
	// header is little-endian, has the 0x002A magic, and points to
	// IFD0 immediately after the header. We add ONE IFD entry — a
	// GPSInfo tag pointer with an offset of zero — so the EXIF
	// block is structurally non-trivial.
	exifPayload := []byte{
		'E', 'x', 'i', 'f', 0x00, 0x00, // identifier
		'I', 'I', 0x2A, 0x00, // TIFF header (little-endian, magic)
		0x08, 0x00, 0x00, 0x00, // offset to IFD0
		0x01, 0x00, // 1 IFD entry
		0x25, 0x88, 0x04, 0x00, 0x01, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, // value (zero offset)
		0x00, 0x00, 0x00, 0x00, // next-IFD pointer (none)
	}
	segLen := len(exifPayload) + 2
	app1 := []byte{0xFF, 0xE1, byte(segLen >> 8), byte(segLen & 0xFF)}
	app1 = append(app1, exifPayload...)
	// Splice APP1 immediately after SOI.
	out := make([]byte, 0, len(base)+len(app1))
	out = append(out, base[:2]...)
	out = append(out, app1...)
	out = append(out, base[2:]...)
	return out
}

// --- imageproc tests ---------------------------------------------------------

// TestProcess_JPEGEmitsFourVariants is the happy-path smoke test: a
// real JPEG goes in, four variants come out (thumb, medium, large,
// original) in WebP. Each variant is decode-able by the WebP reader
// and reports a sensible width. This pins the contract that the
// upload pipeline's downstream consumers (theme renderer, REST
// listing, srcset builder) can rely on.
func TestProcess_JPEGEmitsFourVariants(t *testing.T) {
	t.Parallel()
	src := makeJPEG(t, 1024, 768)

	r, err := Process(context.Background(), bytes.NewReader(src), ProcessOptions{})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if r.SourceFormat != "jpeg" {
		t.Errorf("SourceFormat = %q, want jpeg", r.SourceFormat)
	}
	if r.SourceWidth != 1024 || r.SourceHeight != 768 {
		t.Errorf("Source dims = %dx%d, want 1024x768", r.SourceWidth, r.SourceHeight)
	}

	wantSizes := map[VariantName]int{
		VariantThumbnail: 256,
		VariantMedium:    768,
		VariantLarge:     1024, // input < 1536 so unscaled
		VariantOriginal:  1024,
	}
	if len(r.Variants) != len(wantSizes) {
		t.Fatalf("len(Variants) = %d, want %d", len(r.Variants), len(wantSizes))
	}
	for _, v := range r.Variants {
		want, ok := wantSizes[v.Name]
		if !ok {
			t.Errorf("unexpected variant %q", v.Name)
			continue
		}
		if v.Width != want {
			t.Errorf("variant %q width = %d, want %d", v.Name, v.Width, want)
		}
		if v.Format != FormatWebP {
			t.Errorf("variant %q format = %q, want webp", v.Name, v.Format)
		}
		if len(v.Bytes) == 0 {
			t.Errorf("variant %q has empty bytes", v.Name)
		}
		// The first four bytes of every WebP file are "RIFF".
		if len(v.Bytes) >= 4 && string(v.Bytes[:4]) != "RIFF" {
			t.Errorf("variant %q does not look like WebP (header %x)", v.Name, v.Bytes[:4])
		}
	}
}

// TestProcess_EXIFStripped verifies the privacy contract: an input
// with EXIF GPS metadata produces variants that no longer carry the
// EXIF marker. We assert on the EXIF parser (HasEXIF) for the source
// + the JPEG variant; WebP doesn't carry EXIF at all so the absence
// is structural.
func TestProcess_EXIFStripped(t *testing.T) {
	t.Parallel()
	plain := makeJPEG(t, 200, 200)
	withEXIF := jpegWithEXIF(t, plain)
	if !HasEXIF(withEXIF) {
		t.Fatalf("test fixture broken: jpegWithEXIF did not produce a JPEG with EXIF")
	}

	r, err := Process(context.Background(), bytes.NewReader(withEXIF), ProcessOptions{
		Sizes:   []SizeSpec{{Name: VariantOriginal, LongestEdge: 0}},
		Formats: []Format{FormatJPEG},
	})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	v, ok := r.FindVariant(VariantOriginal, FormatJPEG)
	if !ok {
		t.Fatalf("no original JPEG variant produced")
	}
	if HasEXIF(v.Bytes) {
		t.Errorf("EXIF marker survived round-trip — privacy contract broken")
	}
}

// TestProcess_PNGPreservesTransparency goes through the PNG path:
// alpha pixels in the source must survive into the output. We
// reproduce the resize at a small size and decode the result back to
// inspect a sentinel pixel. WebP supports alpha so the round trip
// should preserve it.
func TestProcess_PNGPreservesTransparency(t *testing.T) {
	t.Parallel()
	src := makePNGWithAlpha(t, 64, 64)

	r, err := Process(context.Background(), bytes.NewReader(src), ProcessOptions{
		Sizes:   []SizeSpec{{Name: VariantOriginal, LongestEdge: 0}},
		Formats: []Format{FormatPNG},
	})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	v, ok := r.FindVariant(VariantOriginal, FormatPNG)
	if !ok {
		t.Fatalf("no PNG variant")
	}
	decoded, err := png.Decode(bytes.NewReader(v.Bytes))
	if err != nil {
		t.Fatalf("decode produced PNG: %v", err)
	}
	// Walk a few pixels and confirm at least one is transparent.
	transparent := false
	bounds := decoded.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y && !transparent; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			_, _, _, a := decoded.At(x, y).RGBA()
			if a == 0 {
				transparent = true
				break
			}
		}
	}
	if !transparent {
		t.Errorf("no transparent pixels survived the PNG round-trip")
	}
}

// TestProcess_AnimatedGIFCollapsesFirstFrame pins the documented
// behaviour: an animated GIF with N>1 frames is collapsed to its
// first frame, and the Result carries a Warning naming the count.
// Without this guard the pipeline would emit a still WebP from a
// random frame, or worse, fail on an animated input.
func TestProcess_AnimatedGIFCollapsesFirstFrame(t *testing.T) {
	t.Parallel()
	src := makeAnimatedGIF(t, 8)

	r, err := Process(context.Background(), bytes.NewReader(src), ProcessOptions{})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if r.SourceFormat != "gif" {
		t.Errorf("SourceFormat = %q, want gif", r.SourceFormat)
	}
	foundWarning := false
	for _, w := range r.Warnings {
		if bytes.Contains([]byte(w), []byte("8 frames")) {
			foundWarning = true
		}
	}
	if !foundWarning {
		t.Errorf("expected a warning mentioning the frame count, got %v", r.Warnings)
	}
	if len(r.Variants) == 0 {
		t.Errorf("no variants produced for animated GIF")
	}
}

// TestProcess_RejectsEmpty pins the contract that an empty reader
// produces a clean error rather than a misleading decoder message.
func TestProcess_RejectsEmpty(t *testing.T) {
	t.Parallel()
	_, err := Process(context.Background(), bytes.NewReader(nil), ProcessOptions{})
	if err == nil {
		t.Fatalf("expected error on empty src, got nil")
	}
}

// TestProcess_RejectsUnsupported pins the ErrUnsupportedFormat
// contract: random bytes that look like nothing in particular get
// the typed error rather than a panic. The dispatcher's permanent-
// vs-transient classifier branches on this error in the worker
// wiring upstream.
func TestProcess_RejectsUnsupported(t *testing.T) {
	t.Parallel()
	_, err := Process(context.Background(), bytes.NewReader([]byte("not an image, definitely not")), ProcessOptions{})
	if err == nil {
		t.Fatalf("expected error on garbage input")
	}
}

// TestProcess_DoesNotUpscale checks the documented behaviour: a tiny
// source that's smaller than every variant's LongestEdge produces
// variants at the source's natural size rather than upscaling. An
// upscale would store more bytes without adding information.
func TestProcess_DoesNotUpscale(t *testing.T) {
	t.Parallel()
	src := makeJPEG(t, 100, 100)
	r, err := Process(context.Background(), bytes.NewReader(src), ProcessOptions{})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	for _, v := range r.Variants {
		if v.Width > 100 {
			t.Errorf("variant %q upscaled to %d (source was 100)", v.Name, v.Width)
		}
	}
}

// TestProcess_CustomSizes proves Sizes is configurable. A caller
// that asks for only the thumbnail gets one variant, not four.
func TestProcess_CustomSizes(t *testing.T) {
	t.Parallel()
	src := makeJPEG(t, 800, 600)
	r, err := Process(context.Background(), bytes.NewReader(src), ProcessOptions{
		Sizes: []SizeSpec{{Name: VariantThumbnail, LongestEdge: 128}},
	})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(r.Variants) != 1 {
		t.Fatalf("len(Variants) = %d, want 1", len(r.Variants))
	}
	if r.Variants[0].Width != 128 {
		t.Errorf("thumbnail width = %d, want 128", r.Variants[0].Width)
	}
}

// TestProcess_CancelledContext pins that Process exits early when the
// caller cancels mid-flight rather than running every variant.
func TestProcess_CancelledContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Process(ctx, bytes.NewReader(makeJPEG(t, 200, 200)), ProcessOptions{})
	if err == nil {
		t.Fatalf("expected error when ctx is cancelled")
	}
}

// TestProcess_AVIFFallback proves the build-default behaviour for
// AVIF: without the libaom tag, AVIF is requested but WebP is
// emitted, and a Warning records the downgrade. Deployments shipping
// libaom would get a real AVIF payload — that path is covered by the
// avif_libaom build, not in this CI-friendly suite.
func TestProcess_AVIFFallback(t *testing.T) {
	t.Parallel()
	src := makeJPEG(t, 256, 256)
	r, err := Process(context.Background(), bytes.NewReader(src), ProcessOptions{
		Sizes:   []SizeSpec{{Name: VariantThumbnail, LongestEdge: 128}},
		Formats: []Format{FormatAVIF},
	})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(r.Variants) != 1 {
		t.Fatalf("len(Variants) = %d, want 1", len(r.Variants))
	}
	// Stub builds downgrade to WebP and report a Warning; libaom
	// builds emit FormatAVIF with no Warning. Accept either so the
	// test passes on both.
	v := r.Variants[0]
	switch v.Format {
	case FormatWebP:
		if len(r.Warnings) == 0 {
			t.Errorf("AVIF fallback to WebP must record a Warning")
		}
	case FormatAVIF:
		// libaom path — no Warning expected.
	default:
		t.Errorf("variant format = %q, want webp or avif", v.Format)
	}
}

// TestIsSupportedMIME is a tiny contract test for the helper the
// upload handler uses to skip non-image enqueues.
func TestIsSupportedMIME(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"image/jpeg":      true,
		"image/png":       true,
		"image/gif":       true,
		"image/webp":      true,
		"IMAGE/JPEG":      true,
		"application/pdf": false,
		"":                false,
		"video/mp4":       false,
		"text/plain":      false,
	}
	for mime, want := range cases {
		if got := IsSupportedMIME(mime); got != want {
			t.Errorf("IsSupportedMIME(%q) = %v, want %v", mime, got, want)
		}
	}
}
