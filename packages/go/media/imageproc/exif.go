package imageproc

import "bytes"

// JPEG marker constants. We only handle the markers we actually need
// to walk; the rest are skipped wholesale via their declared length.
//
// JPEG bytestreams are a sequence of "segments": each starts with
// 0xFF followed by a one-byte marker code, optionally followed by a
// big-endian length-prefixed payload. The structure is documented in
// ISO/IEC 10918-1 §B.1.1.3; for our purposes the relevant facts are:
//
//   - The stream begins with SOI (0xFFD8) and ends with EOI (0xFFD9).
//   - Markers 0xD0..0xD7 (RSTn) and 0x01 carry no length payload.
//   - All other markers are immediately followed by a 2-byte big-
//     endian length INCLUDING those length bytes themselves.
//   - The payload after SOS (0xFFDA) is the actual compressed data;
//     once we see SOS we stop walking.
//
// EXIF lives inside an APP1 (0xFFE1) segment whose payload starts
// with the six bytes "Exif\x00\x00". Other APP1 segments are XMP or
// vendor-specific and we leave those alone — XMP is plain XML and is
// not the privacy hazard EXIF is (cameras don't write GPS coordinates
// to XMP by convention).
const (
	jpegMarkerStart = 0xFF
	jpegMarkerSOI   = 0xD8
	jpegMarkerSOS   = 0xDA
	jpegMarkerEOI   = 0xD9
	jpegMarkerAPP1  = 0xE1
)

// exifIdentifier is the six-byte prefix that distinguishes an EXIF
// APP1 from a plain APP1 (e.g. XMP, which starts with the URI
// "http://ns.adobe.com/xap/1.0/"). The trailing NUL pad is part of the
// EXIF spec — see CIPA DC-008-2019 §4.7.2.
var exifIdentifier = []byte{'E', 'x', 'i', 'f', 0, 0}

// StripEXIF returns a copy of jpeg with every Exif-flavoured APP1
// segment removed. If the input has no EXIF, it is returned as-is
// (ok=false). If the input does not look like a JPEG (no SOI), it is
// returned as-is (ok=false) — the caller decides whether that's an
// error.
//
// StripEXIF is conservative: it walks the segment table and removes
// ONLY APP1 segments whose payload begins with the Exif identifier.
// Other APP segments (XMP in APP1, ICC profiles in APP2, JFIF in
// APP0, photoshop metadata in APP13) are preserved. The conservative
// stance is deliberate: an ICC profile carries colour-management
// information the rendering surface needs; stripping it would shift
// colours on every variant. EXIF, in contrast, carries no rendering
// info — only metadata.
//
// The function returns the new bytes and ok=true on a successful
// strip. ok=false means "nothing was changed"; the caller can use the
// flag to log "this upload had no EXIF" rather than re-running the
// strip later.
//
// StripEXIF does not panic on malformed input; it returns
// (input, false) on any structural error. A defective JPEG will still
// decode through the stdlib decoder (which tolerates a lot), so a
// strip failure should not block the pipeline.
func StripEXIF(jpegBytes []byte) ([]byte, bool) {
	if len(jpegBytes) < 4 {
		return jpegBytes, false
	}
	if jpegBytes[0] != jpegMarkerStart || jpegBytes[1] != jpegMarkerSOI {
		return jpegBytes, false
	}

	// Two passes: first pass identifies the byte ranges to drop;
	// second pass copies the surviving ranges into a fresh buffer.
	// We could do it in one pass with an in-place compaction, but
	// the input is bounded by the upload size cap and the two-pass
	// version is easier to reason about (and to unit test).
	type dropRange struct{ start, end int }
	var drops []dropRange
	i := 2 // past SOI

	for i+1 < len(jpegBytes) {
		if jpegBytes[i] != jpegMarkerStart {
			// Not a marker — this happens inside segment payloads of
			// some malformed JPEGs. Bail out; we'd rather skip the
			// strip than corrupt the file.
			return jpegBytes, false
		}
		marker := jpegBytes[i+1]
		// Marker padding: a sequence of 0xFF bytes is allowed as
		// inter-segment padding. Skip them.
		for marker == 0xFF && i+2 < len(jpegBytes) {
			i++
			marker = jpegBytes[i+1]
		}
		// Stand-alone markers (no length payload).
		if marker == jpegMarkerSOI || marker == jpegMarkerEOI || marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7) {
			i += 2
			if marker == jpegMarkerEOI {
				break
			}
			continue
		}
		// Once we hit SOS, the compressed data follows. We do not
		// scan into it (Huffman bytestreams contain 0xFF as escaped
		// pairs). EXIF only ever lives BEFORE SOS so the remainder
		// of the file is one big "preserve as-is" run.
		if marker == jpegMarkerSOS {
			break
		}
		if i+4 > len(jpegBytes) {
			return jpegBytes, false
		}
		segLen := int(jpegBytes[i+2])<<8 | int(jpegBytes[i+3])
		if segLen < 2 || i+2+segLen > len(jpegBytes) {
			return jpegBytes, false
		}
		segEnd := i + 2 + segLen
		if marker == jpegMarkerAPP1 && segLen >= 2+len(exifIdentifier) {
			payloadStart := i + 4
			if payloadStart+len(exifIdentifier) <= segEnd &&
				bytes.Equal(jpegBytes[payloadStart:payloadStart+len(exifIdentifier)], exifIdentifier) {
				drops = append(drops, dropRange{start: i, end: segEnd})
			}
		}
		i = segEnd
	}

	if len(drops) == 0 {
		return jpegBytes, false
	}

	// Build the output. Pre-compute the size so the buffer is sized
	// exactly: avoids a copy on the bytes.Buffer growth path.
	dropped := 0
	for _, d := range drops {
		dropped += d.end - d.start
	}
	out := make([]byte, 0, len(jpegBytes)-dropped)
	cursor := 0
	for _, d := range drops {
		out = append(out, jpegBytes[cursor:d.start]...)
		cursor = d.end
	}
	out = append(out, jpegBytes[cursor:]...)
	return out, true
}

// HasEXIF reports whether jpegBytes contains an EXIF APP1 segment.
// Cheaper than a full StripEXIF when the caller only wants to log a
// counter ("uploads with EXIF / uploads without"). Returns false on
// any non-JPEG input or on a malformed segment table.
func HasEXIF(jpegBytes []byte) bool {
	if len(jpegBytes) < 4 || jpegBytes[0] != jpegMarkerStart || jpegBytes[1] != jpegMarkerSOI {
		return false
	}
	i := 2
	for i+1 < len(jpegBytes) {
		if jpegBytes[i] != jpegMarkerStart {
			return false
		}
		marker := jpegBytes[i+1]
		for marker == 0xFF && i+2 < len(jpegBytes) {
			i++
			marker = jpegBytes[i+1]
		}
		if marker == jpegMarkerSOI || marker == jpegMarkerEOI || marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7) {
			i += 2
			if marker == jpegMarkerEOI {
				return false
			}
			continue
		}
		if marker == jpegMarkerSOS {
			return false
		}
		if i+4 > len(jpegBytes) {
			return false
		}
		segLen := int(jpegBytes[i+2])<<8 | int(jpegBytes[i+3])
		if segLen < 2 || i+2+segLen > len(jpegBytes) {
			return false
		}
		if marker == jpegMarkerAPP1 && segLen >= 2+len(exifIdentifier) {
			payloadStart := i + 4
			if payloadStart+len(exifIdentifier) <= i+2+segLen &&
				bytes.Equal(jpegBytes[payloadStart:payloadStart+len(exifIdentifier)], exifIdentifier) {
				return true
			}
		}
		i += 2 + segLen
	}
	return false
}
