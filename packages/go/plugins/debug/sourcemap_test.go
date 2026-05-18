package debug

import (
	"errors"
	"strings"
	"testing"
)

// TestParseSourceMap_KnownInput exercises the V3 parser on a hand-rolled
// document with VLQ values we can verify by hand. The three segments
// span offsets 0, 10, and 20; lines 0, 5, 10; one carries a symbol
// name. Lookups at the segment boundaries and in the gaps must resolve
// to the nearest preceding segment, never overshoot.
func TestParseSourceMap_KnownInput(t *testing.T) {
	// VLQ derivation:
	//   "AAAA"   → [ 0, 0, 0, 0]  → offset=0, src=0, line=0, col=0
	//   "UAKA"   → [10, 0, 5, 0]  → offset=10, src=0, line=5, col=0
	//   "UAKAA"  → [10, 0, 5, 0, 0] → offset=20, src=0, line=10, col=0, name=0
	doc := `{
		"version": 3,
		"file": "plugin.wasm",
		"sources": ["src/main.go", "src/util.go"],
		"sourceRoot": "",
		"names": ["mainHandler"],
		"mappings": "AAAA,UAKA,UAKAA"
	}`

	sm, err := ParseSourceMap(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("ParseSourceMap: %v", err)
	}
	if got, want := len(sm.Segments), 3; got != want {
		t.Fatalf("segments: got %d want %d", got, want)
	}

	expectedOffsets := []uint32{0, 10, 20}
	for i, s := range sm.Segments {
		if s.Offset != expectedOffsets[i] {
			t.Errorf("seg[%d].Offset: got %d want %d", i, s.Offset, expectedOffsets[i])
		}
	}
	if sm.Segments[2].NameIdx != 0 {
		t.Errorf("seg[2].NameIdx: got %d want 0", sm.Segments[2].NameIdx)
	}
	if got := sm.SymbolName(sm.Segments[2]); got != "mainHandler" {
		t.Errorf("SymbolName: got %q want %q", got, "mainHandler")
	}

	// Boundary: offset 0 hits segment 0.
	seg, ok := sm.LookupOffset(0)
	if !ok {
		t.Fatalf("LookupOffset(0): not found")
	}
	if seg.Line != 0 || seg.Column != 0 {
		t.Errorf("LookupOffset(0): got %d:%d want 0:0", seg.Line, seg.Column)
	}

	// In-gap: offset 5 falls between 0 and 10; resolves to segment 0.
	seg, ok = sm.LookupOffset(5)
	if !ok || seg.Offset != 0 {
		t.Errorf("LookupOffset(5): got offset %d ok=%v, want 0/true", seg.Offset, ok)
	}

	// Mid: offset 15 between 10 and 20 → segment at offset 10.
	seg, ok = sm.LookupOffset(15)
	if !ok || seg.Offset != 10 {
		t.Errorf("LookupOffset(15): got offset %d ok=%v, want 10/true", seg.Offset, ok)
	}

	// Beyond last segment: offset 999 still resolves to segment 20.
	seg, ok = sm.LookupOffset(999)
	if !ok || seg.Offset != 20 {
		t.Errorf("LookupOffset(999): got offset %d ok=%v, want 20/true", seg.Offset, ok)
	}
}

// TestParseSourceMap_EmptyReturnsErr verifies the "no map present"
// signal callers depend on for graceful degradation.
func TestParseSourceMap_EmptyReturnsErr(t *testing.T) {
	_, err := ParseSourceMap(strings.NewReader(""))
	if !errors.Is(err, ErrEmptySourceMap) {
		t.Fatalf("ParseSourceMap(empty): got %v want ErrEmptySourceMap", err)
	}
}

// TestParseSourceMap_BadVersion rejects v2/v1 maps explicitly so a
// stale toolchain doesn't silently produce wrong frames.
func TestParseSourceMap_BadVersion(t *testing.T) {
	_, err := ParseSourceMap(strings.NewReader(`{"version":2,"mappings":"AAAA","sources":["a"]}`))
	if err == nil {
		t.Fatal("ParseSourceMap(v2): expected error, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported version") {
		t.Errorf("error message: %q does not mention version", err.Error())
	}
}

// TestSourceRootPrepended confirms the sourceRoot prefix is applied at
// parse time, so callers don't have to reconstruct paths themselves.
func TestSourceRootPrepended(t *testing.T) {
	doc := `{"version":3,"mappings":"AAAA","sources":["main.go"],"sourceRoot":"src/","names":[]}`
	sm, err := ParseSourceMap(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	if got := sm.Sources[0]; got != "src/main.go" {
		t.Errorf("Sources[0]: got %q want %q", got, "src/main.go")
	}
}

// TestLookupNilSafeOnEmptyMap ensures a SourceMap pointer with no
// segments and a nil SourceMap pointer both return (zero, false). The
// Inspector relies on this — it calls LookupOffset before checking
// HasSourceMap in some branches.
func TestLookupNilSafeOnEmptyMap(t *testing.T) {
	if _, ok := (*SourceMap)(nil).LookupOffset(123); ok {
		t.Error("nil.LookupOffset: expected !ok")
	}
	sm := &SourceMap{}
	if _, ok := sm.LookupOffset(0); ok {
		t.Error("empty.LookupOffset: expected !ok")
	}
}

// TestVLQRoundTrip verifies the base64-VLQ decoder against the canonical
// values the V3 spec spells out: 0='A', 1='C', -1='D', 16='gB'.
func TestVLQRoundTrip(t *testing.T) {
	cases := []struct {
		in   string
		want int32
	}{
		{"A", 0},
		{"C", 1},
		{"D", -1},
		{"E", 2},
		{"F", -2},
		{"gB", 16},
		{"hB", -16},
	}
	for _, tc := range cases {
		got, _, err := decodeVLQ(tc.in)
		if err != nil {
			t.Errorf("decodeVLQ(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("decodeVLQ(%q): got %d want %d", tc.in, got, tc.want)
		}
	}
}

// TestDecodeVLQList_Consumes verifies that decodeVLQList correctly
// advances the input pointer across multi-byte continuations. The
// segment "gBAA" decodes to [16, 0, 0] when 3 fields are expected.
func TestDecodeVLQList_Consumes(t *testing.T) {
	got, err := decodeVLQList("gBAA")
	if err != nil {
		t.Fatal(err)
	}
	want := []int32{16, 0, 0}
	if len(got) != len(want) {
		t.Fatalf("len: got %d want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d]: got %d want %d", i, got[i], want[i])
		}
	}
}
