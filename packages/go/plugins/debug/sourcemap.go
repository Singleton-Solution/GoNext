package debug

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
)

// SourceMap is a parsed Source Map V3 document, with the segments
// flattened into a slice sorted by WASM code offset (the V3 "column"
// field, interpreted per the WebAssembly source-map convention).
//
// The mainstream WASM toolchains (LLVM-based: clang/rustc/wasm-ld; and
// binaryen / wasm-opt) all emit V3 maps where:
//
//   - "version" is 3.
//   - "sources" is a list of source file paths.
//   - "names" is a list of symbol names — optional, mostly populated by
//     emscripten and used by us to label frames with a function name.
//   - "mappings" is the VLQ-encoded segment string. Each segment is a
//     5-tuple [genCol, srcIdx, srcLine, srcCol, nameIdx]. For WASM
//     maps the "generated line" is always 0 (the entire code section is
//     one logical line) and genCol is the BYTE OFFSET into the code
//     section. That's the same offset wazero surfaces in trap
//     addresses, which makes the lookup a direct binary search.
//
// We DO NOT support the "sections" form of V3 maps — none of the WASM
// toolchains emit it, and adding the recursive walk would bloat this
// file with no real-world payoff.
type SourceMap struct {
	// Version is the V3 spec version. We accept only 3.
	Version int

	// Sources is the list of source-file paths as declared by the
	// toolchain. The "sourceRoot" field, if present, is prepended onto
	// each entry at parse time so callers don't need to compose paths
	// themselves.
	Sources []string

	// Names is the optional symbol-name table. Empty if absent.
	Names []string

	// Segments is the flattened, sorted-by-Offset slice of mappings.
	// Lookups binary-search this slice.
	Segments []Segment
}

// Segment is one entry of the flattened mappings table. Offset is the
// byte offset into the WASM code section; the (SourceIdx, Line, Column)
// triple indexes into SourceMap.Sources and the source file itself.
// NameIdx is -1 when the segment carries no symbol.
//
// Lines and columns are ZERO-BASED, matching the V3 wire format. The
// Inspector translates to one-based before rendering.
type Segment struct {
	Offset    uint32
	SourceIdx int
	Line      int
	Column    int
	NameIdx   int
}

// rawSourceMap is the literal JSON shape. We unmarshal into it then
// post-process into the SourceMap proper.
type rawSourceMap struct {
	Version    int      `json:"version"`
	File       string   `json:"file"`
	SourceRoot string   `json:"sourceRoot"`
	Sources    []string `json:"sources"`
	Names      []string `json:"names"`
	Mappings   string   `json:"mappings"`
}

// ErrEmptySourceMap is returned when ParseSourceMap is handed an empty
// document. Callers (typically Inspector.WithSourceMap) treat this as
// "no map present" and degrade gracefully.
var ErrEmptySourceMap = errors.New("debug: source map is empty")

// ParseSourceMap decodes a Source Map V3 JSON document from r. The
// returned SourceMap is ready for LookupOffset.
//
// Failures fall into three buckets, each diagnostic:
//
//   - Empty/unreadable input → ErrEmptySourceMap.
//   - Wrong version or malformed JSON → a wrapped error.
//   - Malformed VLQ segments → a wrapped error pointing at the bad
//     segment index.
func ParseSourceMap(r io.Reader) (*SourceMap, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("debug: ParseSourceMap: read: %w", err)
	}
	if len(data) == 0 {
		return nil, ErrEmptySourceMap
	}

	var raw rawSourceMap
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("debug: ParseSourceMap: unmarshal: %w", err)
	}
	if raw.Version != 3 {
		return nil, fmt.Errorf("debug: ParseSourceMap: unsupported version %d (want 3)", raw.Version)
	}

	// Prefix sourceRoot onto every entry once, so callers don't have to.
	sources := make([]string, len(raw.Sources))
	for i, s := range raw.Sources {
		if raw.SourceRoot != "" {
			sources[i] = raw.SourceRoot + s
		} else {
			sources[i] = s
		}
	}

	segs, err := decodeMappings(raw.Mappings)
	if err != nil {
		return nil, fmt.Errorf("debug: ParseSourceMap: decode mappings: %w", err)
	}

	// Sort by offset so LookupOffset can binary-search. decodeMappings
	// emits in source order, which is the same as offset order for the
	// WASM convention (offsets monotonically increase), but we sort
	// defensively — a slightly malformed but otherwise-valid map should
	// still work.
	sort.Slice(segs, func(i, j int) bool { return segs[i].Offset < segs[j].Offset })

	return &SourceMap{
		Version:  raw.Version,
		Sources:  sources,
		Names:    raw.Names,
		Segments: segs,
	}, nil
}

// ParseSourceMapFile is a convenience wrapper around ParseSourceMap
// that opens path. A missing file returns os.ErrNotExist (use
// errors.Is) so the inspector can treat absent maps as "degrade
// gracefully".
func ParseSourceMapFile(path string) (*SourceMap, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ParseSourceMap(f)
}

// LookupOffset resolves a WASM code offset to the nearest preceding
// segment. The returned bool is false when the map has no segments at
// or before offset — callers should treat that as "unresolved" rather
// than an error.
//
// The search is O(log N). N is typically a few thousand for a
// real-world plugin's map.
func (m *SourceMap) LookupOffset(offset uint32) (Segment, bool) {
	if m == nil || len(m.Segments) == 0 {
		return Segment{}, false
	}
	// sort.Search returns the first index whose Offset > target; we
	// want the LAST index with Offset <= target, which is one before.
	i := sort.Search(len(m.Segments), func(i int) bool {
		return m.Segments[i].Offset > offset
	})
	if i == 0 {
		// All segments start AFTER offset. The trap fell into the
		// pre-code prologue (e.g., the function-table dispatch); no
		// useful mapping.
		return Segment{}, false
	}
	return m.Segments[i-1], true
}

// SourceName returns the source file path for seg, or "" if the index
// is out of range. Out-of-range indexes don't crash — a malformed map
// should still produce a printable frame.
func (m *SourceMap) SourceName(seg Segment) string {
	if seg.SourceIdx < 0 || seg.SourceIdx >= len(m.Sources) {
		return ""
	}
	return m.Sources[seg.SourceIdx]
}

// SymbolName returns the function/symbol name for seg, or "" if the
// segment carries no name or the index is out of range.
func (m *SourceMap) SymbolName(seg Segment) string {
	if seg.NameIdx < 0 || seg.NameIdx >= len(m.Names) {
		return ""
	}
	return m.Names[seg.NameIdx]
}

// decodeMappings parses the V3 "mappings" string into a slice of
// Segments. Per the V3 spec, the string is divided into "lines"
// separated by ';' and "segments" separated by ','. Each segment is a
// 1-, 4-, or 5-tuple of VLQ-encoded base64 integers. Values are
// delta-encoded against the previous segment, with line breaks
// resetting genCol to 0.
//
// For WASM maps the toolchain typically emits everything on a single
// generated line (no ';' at all, or just one trailing ';'). We handle
// both shapes; multi-line maps work too — we just ignore the genLine
// because WASM trap offsets carry no line info.
//
// A 1-tuple segment (genCol only) is "unmapped" and we skip it.
// A 4-tuple omits the name index; we record -1.
// A 5-tuple is the full form.
func decodeMappings(s string) ([]Segment, error) {
	if s == "" {
		return nil, nil
	}

	var (
		segs           []Segment
		genCol         int32
		srcIdx         int32
		srcLine        int32
		srcCol         int32
		nameIdx        int32
		segmentCounter int
	)

	for _, line := range splitMappingLines(s) {
		genCol = 0 // genCol resets per line per V3 spec
		if line == "" {
			continue
		}
		for _, segStr := range splitSegments(line) {
			segmentCounter++
			if segStr == "" {
				continue
			}
			vals, err := decodeVLQList(segStr)
			if err != nil {
				return nil, fmt.Errorf("segment #%d: %w", segmentCounter, err)
			}
			// Update state from the delta-encoded VLQs.
			switch len(vals) {
			case 1:
				// Unmapped: only genCol moves, no source association.
				genCol += vals[0]
				continue
			case 4:
				genCol += vals[0]
				srcIdx += vals[1]
				srcLine += vals[2]
				srcCol += vals[3]
				segs = append(segs, Segment{
					Offset:    uint32(genCol),
					SourceIdx: int(srcIdx),
					Line:      int(srcLine),
					Column:    int(srcCol),
					NameIdx:   -1,
				})
			case 5:
				genCol += vals[0]
				srcIdx += vals[1]
				srcLine += vals[2]
				srcCol += vals[3]
				nameIdx += vals[4]
				segs = append(segs, Segment{
					Offset:    uint32(genCol),
					SourceIdx: int(srcIdx),
					Line:      int(srcLine),
					Column:    int(srcCol),
					NameIdx:   int(nameIdx),
				})
			default:
				return nil, fmt.Errorf("segment #%d: unexpected field count %d", segmentCounter, len(vals))
			}
		}
	}

	return segs, nil
}

// splitMappingLines splits the V3 mappings string at ';'. We avoid
// strings.Split here purely to keep the dependency surface small —
// this file already pulls encoding/json and io, and adding one more
// import for a 6-line helper isn't worth the noise.
func splitMappingLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ';' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

// splitSegments splits a line at ','.
func splitSegments(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

// decodeVLQList decodes a concatenation of base64-VLQ integers into a
// slice. The V3 spec uses a single-byte-per-character base64 alphabet
// (RFC 4648 §4) with sign packed into the low bit of the first sextet
// and a continuation bit in the high sextet of each follow-on byte.
func decodeVLQList(s string) ([]int32, error) {
	var out []int32
	i := 0
	for i < len(s) {
		v, consumed, err := decodeVLQ(s[i:])
		if err != nil {
			return nil, err
		}
		out = append(out, v)
		i += consumed
	}
	return out, nil
}

// decodeVLQ decodes ONE base64-VLQ integer from the front of s. Returns
// the value, the number of bytes consumed, and any error.
//
// The encoding packs the absolute value's sign bit into the lowest bit
// of the first sextet, then continues 5-bit chunks with the high (6th)
// bit set on every non-terminal byte.
func decodeVLQ(s string) (int32, int, error) {
	const (
		vlqBaseShift     = 5
		vlqBaseMask      = (1 << vlqBaseShift) - 1
		vlqContinuation  = 1 << vlqBaseShift
	)

	var (
		result int32
		shift  uint
		i      int
	)
	for i = 0; i < len(s); i++ {
		digit, ok := base64Decode(s[i])
		if !ok {
			return 0, 0, fmt.Errorf("invalid base64 char %q", s[i])
		}
		result |= int32(digit&vlqBaseMask) << shift
		shift += vlqBaseShift
		if digit&vlqContinuation == 0 {
			i++ // consumed this byte
			break
		}
	}
	if i == 0 {
		return 0, 0, fmt.Errorf("VLQ: empty input")
	}

	// Sign bit is the LSB; the magnitude lives in the rest.
	neg := result&1 != 0
	result >>= 1
	if neg {
		// Symmetric two's-complement-like sign: 0 maps to 0, 1 maps to -0
		// (treated as 0 by the spec, but we keep -0 == 0 below).
		if result == 0 {
			return -0x80000000, i, nil
		}
		result = -result
	}
	return result, i, nil
}

// base64Decode maps a single base64 character to its 0..63 value.
// Returns (0, false) for invalid input. The alphabet is the standard
// RFC 4648 table: A-Z, a-z, 0-9, +, /.
//
// We use a switch rather than a precomputed table to keep the helper
// allocation-free and readable.
func base64Decode(c byte) (int32, bool) {
	switch {
	case c >= 'A' && c <= 'Z':
		return int32(c - 'A'), true
	case c >= 'a' && c <= 'z':
		return int32(c-'a') + 26, true
	case c >= '0' && c <= '9':
		return int32(c-'0') + 52, true
	case c == '+':
		return 62, true
	case c == '/':
		return 63, true
	default:
		return 0, false
	}
}
