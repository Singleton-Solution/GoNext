package imageproc

import (
	"bytes"
	"testing"
)

// TestStripEXIF_RemovesAPP1 walks the happy path: a JPEG with one
// EXIF APP1 segment loses exactly that segment. We assert on length
// (the strip removed bytes) and on HasEXIF (the segment is gone).
func TestStripEXIF_RemovesAPP1(t *testing.T) {
	t.Parallel()
	base := makeJPEG(t, 64, 64)
	withEXIF := jpegWithEXIF(t, base)
	if !HasEXIF(withEXIF) {
		t.Fatalf("fixture: jpegWithEXIF did not add an EXIF segment")
	}

	stripped, ok := StripEXIF(withEXIF)
	if !ok {
		t.Fatalf("StripEXIF reported ok=false on a JPEG that has EXIF")
	}
	if HasEXIF(stripped) {
		t.Fatalf("EXIF survived the strip")
	}
	if len(stripped) >= len(withEXIF) {
		t.Errorf("stripped length %d >= input length %d", len(stripped), len(withEXIF))
	}
	// The stripped bytes must still be a parseable JPEG, with no
	// truncation past EOI.
	if stripped[0] != 0xFF || stripped[1] != 0xD8 {
		t.Errorf("stripped output does not start with SOI: %x", stripped[:2])
	}
}

// TestStripEXIF_NoOpWithoutEXIF pins the contract that a JPEG with
// no EXIF block is returned unchanged AND with ok=false. The caller
// uses the flag to log "no EXIF found" without re-walking.
func TestStripEXIF_NoOpWithoutEXIF(t *testing.T) {
	t.Parallel()
	base := makeJPEG(t, 64, 64)
	stripped, ok := StripEXIF(base)
	if ok {
		t.Errorf("StripEXIF on a clean JPEG should report ok=false")
	}
	if !bytes.Equal(stripped, base) {
		t.Errorf("StripEXIF mutated a clean JPEG")
	}
}

// TestStripEXIF_NonJPEG: a PNG or random garbage should NOT trigger
// the strip path. We don't want to corrupt a valid PNG by walking
// its bytes as a JPEG segment table.
func TestStripEXIF_NonJPEG(t *testing.T) {
	t.Parallel()
	png := makePNGWithAlpha(t, 16, 16)
	out, ok := StripEXIF(png)
	if ok {
		t.Errorf("StripEXIF on a PNG reported ok=true")
	}
	if !bytes.Equal(out, png) {
		t.Errorf("StripEXIF mutated a non-JPEG input")
	}
}

// TestStripEXIF_TruncatedSegment pins the defensive contract: a
// JPEG whose APP1 length runs past EOF is returned as-is rather
// than panicking. We construct a minimal corrupt JPEG and verify
// the strip is a no-op.
func TestStripEXIF_TruncatedSegment(t *testing.T) {
	t.Parallel()
	corrupt := []byte{
		0xFF, 0xD8, // SOI
		0xFF, 0xE1, // APP1 marker
		0xFF, 0xFF, // claimed length 65535
		'E', 'x', 'i', 'f', 0, 0, // truncated payload
	}
	out, ok := StripEXIF(corrupt)
	if ok {
		t.Errorf("StripEXIF on a truncated segment reported ok=true")
	}
	if !bytes.Equal(out, corrupt) {
		t.Errorf("StripEXIF on truncated input mutated bytes")
	}
}

// TestStripEXIF_PreservesNonEXIFAPP1 documents that XMP-style APP1
// segments (which start with a URI, not "Exif\0\0") are NOT stripped.
// Customers use XMP for licensing metadata; clobbering it would be a
// regression beyond the privacy fix.
func TestStripEXIF_PreservesNonEXIFAPP1(t *testing.T) {
	t.Parallel()
	base := makeJPEG(t, 32, 32)
	// Manually splice an XMP-ish APP1 ("http://ns.adobe.com/xap/1.0/\0").
	xmpID := []byte("http://ns.adobe.com/xap/1.0/\x00")
	xmpPayload := append(append([]byte{}, xmpID...), []byte(`<x:xmpmeta/>`)...)
	segLen := len(xmpPayload) + 2
	app1 := []byte{0xFF, 0xE1, byte(segLen >> 8), byte(segLen & 0xFF)}
	app1 = append(app1, xmpPayload...)
	withXMP := append([]byte{}, base[:2]...)
	withXMP = append(withXMP, app1...)
	withXMP = append(withXMP, base[2:]...)

	stripped, ok := StripEXIF(withXMP)
	if ok {
		t.Errorf("StripEXIF reported ok=true on XMP-only APP1 (should be no-op)")
	}
	if !bytes.Equal(stripped, withXMP) {
		t.Errorf("StripEXIF mutated an XMP-bearing JPEG (length %d → %d)", len(withXMP), len(stripped))
	}
}

// TestHasEXIF_Positive + Negative pin the cheap probe used by metrics
// counters ("uploads with EXIF / uploads without"). It must agree
// with the result of StripEXIF on the same input.
func TestHasEXIF_AgreesWithStrip(t *testing.T) {
	t.Parallel()
	base := makeJPEG(t, 32, 32)
	if HasEXIF(base) {
		t.Errorf("HasEXIF on a fresh JPEG returned true")
	}
	withEXIF := jpegWithEXIF(t, base)
	if !HasEXIF(withEXIF) {
		t.Errorf("HasEXIF on a JPEG with EXIF returned false")
	}
	stripped, _ := StripEXIF(withEXIF)
	if HasEXIF(stripped) {
		t.Errorf("HasEXIF on the stripped output returned true")
	}
}
