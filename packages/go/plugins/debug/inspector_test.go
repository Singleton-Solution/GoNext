package debug

import (
	"strings"
	"testing"

	"github.com/Singleton-Solution/GoNext/packages/go/plugins/runtime"
)

// TestInspector_NoSourceMap is the "graceful degrade" check. Without a
// map, every Symbolicate call must produce a stack with one frame whose
// File is the "<no source map>" placeholder. The dev tools rely on
// this — they print whatever string comes back without re-checking.
func TestInspector_NoSourceMap(t *testing.T) {
	in := NewInspector()
	trap := &runtime.TrapError{
		Module: "blog",
		Reason: "guest panicked at 0x12C: division by zero",
	}

	got := in.Symbolicate(trap)

	if got.Module != "blog" || got.Reason != trap.Reason {
		t.Fatalf("trap fields: got %+v", got)
	}
	if len(got.Stack) != 1 {
		t.Fatalf("stack length: got %d want 1", len(got.Stack))
	}
	if want := "<no source map>"; got.Stack[0].File != want {
		t.Errorf("Stack[0].File: got %q want %q", got.Stack[0].File, want)
	}
	if got.Stack[0].Offset != 0x12C {
		t.Errorf("Stack[0].Offset: got 0x%X want 0x12C", got.Stack[0].Offset)
	}
}

// TestInspector_WithMap_ResolvesFrame walks the full happy path: trap
// reason carries "0x12C", the loaded map has a segment at offset 0x100
// that resolves to a known (file, line, col), and the result matches.
//
// We pin both the file and the symbol name to exercise the name table
// pathway — the dev UI prints both, so a regression in either is
// caught here.
func TestInspector_WithMap_ResolvesFrame(t *testing.T) {
	// Three segments: offset 0, 0x100, 0x200. The middle one is the
	// nearest-preceding match for 0x12C.
	//   AAAA   → [0,0,0,0]              offset=0 file=0 line=0 col=0
	//   "g8DUFA"... actually let's build deliberately.
	// We'll use offsets 0, 256, 512 and lines 0, 7, 14.
	// VLQ deltas:
	//   seg0: [0, 0, 0, 0]      = "AAAA"
	//   seg1: [256, 0, 7, 0, 0] (256 in VLQ: see below)
	//   seg2: [256, 0, 7, 0, 0]
	//
	// VLQ of 256: unsigned 512 = 0b1000000000. Need 2 sextets of 5
	// bits each with continuation: low 5 bits 0b00000, next 5 bits
	// 0b10000 = 16. With continuation: first byte = 0b100000 = 32 ='g',
	// second byte = 0b010000 = 16 = 'Q'. Total: "gQ".
	//
	// VLQ of 7: unsigned 14 = 'O'.
	//
	// Hand-built: "AAAA,gQAOAA,gQAOAA"
	doc := `{
		"version": 3,
		"sources": ["src/lib.go"],
		"names": ["doWork"],
		"mappings": "AAAA,gQAOAA,gQAOAA"
	}`
	sm, err := ParseSourceMap(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("ParseSourceMap: %v", err)
	}

	in := NewInspectorWithMap(sm)
	trap := &runtime.TrapError{
		Module: "blog",
		Reason: "wasm trap: unreachable at 0x12C",
	}
	got := in.Symbolicate(trap)

	if len(got.Stack) != 1 {
		t.Fatalf("stack length: got %d want 1\n%s", len(got.Stack), got.String())
	}
	fr := got.Stack[0]
	if fr.File != "src/lib.go" {
		t.Errorf("File: got %q want %q", fr.File, "src/lib.go")
	}
	// Segment offset 0x100=256 maps to seg with line=7 (zero-based) →
	// one-based 8.
	if fr.Line != 8 {
		t.Errorf("Line: got %d want 8", fr.Line)
	}
	if fr.Col != 1 {
		t.Errorf("Col: got %d want 1", fr.Col)
	}
	if fr.Function != "doWork" {
		t.Errorf("Function: got %q want %q", fr.Function, "doWork")
	}
}

// TestInspector_OffsetBeforeFirstSegment is the unresolved-but-mapped
// case: a map is loaded but the trap's offset (0x10) falls before the
// first segment (which starts at 0x100). The inspector emits an
// "<unmapped>" frame rather than silently fabricating coordinates.
func TestInspector_OffsetBeforeFirstSegment(t *testing.T) {
	doc := `{"version":3,"sources":["a.go"],"names":[],"mappings":"gQAOA"}`
	sm, err := ParseSourceMap(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	in := NewInspectorWithMap(sm)
	trap := &runtime.TrapError{Module: "x", Reason: "panic at 0x10"}
	got := in.Symbolicate(trap)
	if len(got.Stack) != 1 {
		t.Fatalf("stack length: got %d", len(got.Stack))
	}
	if got.Stack[0].File != "<unmapped>" {
		t.Errorf("File: got %q want <unmapped>", got.Stack[0].File)
	}
}

// TestInspector_NoOffsetInReason exercises the "map present, trap text
// unhelpful" branch. The reason carries no hex, no offset=, no '@';
// the inspector should still produce a printable frame.
func TestInspector_NoOffsetInReason(t *testing.T) {
	doc := `{"version":3,"sources":["a.go"],"names":[],"mappings":"AAAA"}`
	sm, err := ParseSourceMap(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	in := NewInspectorWithMap(sm)
	trap := &runtime.TrapError{Module: "x", Reason: "module exited"}
	got := in.Symbolicate(trap)
	if len(got.Stack) != 1 {
		t.Fatalf("stack length: got %d", len(got.Stack))
	}
	if got.Stack[0].File != "<unmapped>" {
		t.Errorf("File: got %q want <unmapped>", got.Stack[0].File)
	}
}

// TestExtractOffsets verifies the offset-extraction helper handles
// every shape the inspector advertises support for.
func TestExtractOffsets(t *testing.T) {
	cases := []struct {
		in   string
		want []uint32
	}{
		{"panic at 0x12C", []uint32{0x12C}},
		{"trap: offset=300", []uint32{300}},
		{"emscripten @42", []uint32{42}},
		{"two: 0x10 and 0x20", []uint32{0x10, 0x20}},
		{"plain text with no offset", nil},
	}
	for _, tc := range cases {
		got := extractOffsets(tc.in)
		if len(got) != len(tc.want) {
			t.Errorf("extractOffsets(%q): got %v want %v", tc.in, got, tc.want)
			continue
		}
		for i := range tc.want {
			if got[i] != tc.want[i] {
				t.Errorf("extractOffsets(%q)[%d]: got %d want %d", tc.in, i, got[i], tc.want[i])
			}
		}
	}
}

// TestSymbolicatedTrap_String verifies the canonical multi-line render.
// The dev CLI consumes this string verbatim, so a format drift would
// break operator-visible output.
func TestSymbolicatedTrap_String(t *testing.T) {
	st := &SymbolicatedTrap{
		Module: "blog",
		Reason: "panic: bad input",
		Stack: []Frame{
			{File: "src/main.go", Line: 12, Col: 4, Function: "handle"},
		},
	}
	out := st.String()
	for _, want := range []string{`trap in "blog": panic: bad input`, "src/main.go:12:4", "handle"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// TestSymbolicate_NilPanics catches the documented panic on nil input.
// The dev CLI's caller always type-asserts before invoking; if that
// contract slips, the panic is much louder than a quiet wrong answer.
func TestSymbolicate_NilPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on nil trap, got none")
		}
	}()
	NewInspector().Symbolicate(nil)
}
