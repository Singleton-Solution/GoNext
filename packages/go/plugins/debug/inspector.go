package debug

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/Singleton-Solution/GoNext/packages/go/plugins/runtime"
)

// Frame is one symbolicated entry in a SymbolicatedTrap.Stack. The
// triple (File, Line, Col) follows editor-friendly one-based indexing
// even though the underlying source map uses zero-based — humans read
// dev-tool output, not parsers.
//
// Offset is preserved alongside the resolved file/line because raw
// offsets are useful when a frame is unresolved or partially resolved
// (e.g., source map present but offset falls before the first segment).
type Frame struct {
	// Offset is the raw WASM code-section byte offset extracted from
	// the trap. Zero when the trap carried no offset information.
	Offset uint32

	// File is the source file path. "<no source map>" when no map is
	// loaded; "<unmapped>" when the offset has no entry in the loaded
	// map.
	File string

	// Line is one-based. Zero when unresolved.
	Line int

	// Col is one-based. Zero when unresolved.
	Col int

	// Function is a best-effort symbol name. Sourced from the map's
	// "names" table when available; falls back to the trap reason for
	// the leaf frame so dev-tool output never reads empty.
	Function string
}

// String renders the frame in the canonical "function (file:line:col)"
// format dev tools expect. Unresolved fields are omitted gracefully so
// the output is still readable.
func (f Frame) String() string {
	loc := f.File
	if f.Line > 0 {
		loc = fmt.Sprintf("%s:%d", loc, f.Line)
		if f.Col > 0 {
			loc = fmt.Sprintf("%s:%d", loc, f.Col)
		}
	}
	if f.Function == "" {
		return loc
	}
	return fmt.Sprintf("%s (%s)", f.Function, loc)
}

// SymbolicatedTrap is the result of Inspector.Symbolicate. It carries
// the original TrapError verbatim alongside the resolved stack, so
// callers that want to forward the raw error still can.
type SymbolicatedTrap struct {
	// Trap is the original error; never nil for traps produced by the
	// runtime, but may be nil in synthetic tests.
	Trap *runtime.TrapError

	// Module is duplicated from Trap.Module for convenience.
	Module string

	// Reason mirrors Trap.Reason for convenience and so the formatter
	// has a stable field to lead the rendering with.
	Reason string

	// Stack is the symbolicated frames, leaf-first. Always at least one
	// entry — even if no offset could be extracted, we synthesise a
	// single frame carrying the trap reason as the function name and
	// "<unknown>" as the location.
	Stack []Frame
}

// String renders the trap as a multi-line human-readable block. The
// format mirrors what `go test` prints on panic so the dev tools and
// the CLI can share rendering code.
func (s *SymbolicatedTrap) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "trap in %q: %s\n", s.Module, s.Reason)
	for i, fr := range s.Stack {
		fmt.Fprintf(&b, "  #%d %s\n", i, fr.String())
	}
	return b.String()
}

// Inspector resolves runtime.TrapError values into SymbolicatedTrap
// values. It is goroutine-safe: the underlying SourceMap is immutable
// after parse.
//
// Construct one Inspector per plugin (the source map is plugin-
// specific) and reuse it across traps.
type Inspector struct {
	// sourceMap is nil when the caller did not provide one. Symbolicate
	// degrades to a "<no source map>" frame in that case.
	sourceMap *SourceMap
}

// NewInspector returns an Inspector with no source map attached.
// Symbolicate calls will produce "<no source map>" frames. Use
// NewInspectorWithMap to attach one at construction time.
func NewInspector() *Inspector {
	return &Inspector{}
}

// NewInspectorWithMap returns an Inspector backed by sm. Passing nil is
// equivalent to NewInspector — the inspector still works, it just
// can't resolve offsets.
func NewInspectorWithMap(sm *SourceMap) *Inspector {
	return &Inspector{sourceMap: sm}
}

// WithSourceMap returns a copy of the inspector with sm attached.
// Useful for hot-replacing a map after a rebuild without churning the
// caller's plumbing.
func (i *Inspector) WithSourceMap(sm *SourceMap) *Inspector {
	return &Inspector{sourceMap: sm}
}

// HasSourceMap reports whether a map is attached. Mostly for
// diagnostics — the symbolicated output already calls out the
// missing-map case via Frame.File.
func (i *Inspector) HasSourceMap() bool {
	return i != nil && i.sourceMap != nil
}

// Symbolicate translates trap into a SymbolicatedTrap. The input must
// not be nil; passing nil panics — the inspector is invoked from the
// trap path where the caller has already type-asserted.
//
// When no source map is loaded the returned Stack carries one Frame
// with File="<no source map>" and Function=trap.Reason. Callers always
// get a printable result.
//
// When a map IS loaded but the offset can't be parsed from the trap
// reason (the wazero-shape strings vary), the inspector falls back to
// the no-source-map shape so the dev tool still has something to show.
func (i *Inspector) Symbolicate(trap *runtime.TrapError) *SymbolicatedTrap {
	if trap == nil {
		panic("debug: Inspector.Symbolicate: nil trap")
	}

	out := &SymbolicatedTrap{
		Trap:   trap,
		Module: trap.Module,
		Reason: trap.Reason,
	}

	// Try to pull an offset out of the trap text. Wazero surfaces
	// offsets in formats like "wasm error: out of bounds memory access
	// (recovered by wazerocoll)" or "panic at 0x12C". We accept a few
	// shapes; failure simply means we fall back to an unresolved frame.
	offsets := extractOffsets(trap.Reason)

	if !i.HasSourceMap() {
		out.Stack = []Frame{{
			Offset:   firstOrZero(offsets),
			File:     "<no source map>",
			Function: trap.Reason,
		}}
		return out
	}

	if len(offsets) == 0 {
		// Map present but trap shape we don't recognise. Synthesise
		// one unresolved frame so the output still renders.
		out.Stack = []Frame{{
			File:     "<unmapped>",
			Function: trap.Reason,
		}}
		return out
	}

	for _, off := range offsets {
		seg, ok := i.sourceMap.LookupOffset(off)
		if !ok {
			out.Stack = append(out.Stack, Frame{
				Offset:   off,
				File:     "<unmapped>",
				Function: trap.Reason,
			})
			continue
		}
		out.Stack = append(out.Stack, Frame{
			Offset:   off,
			File:     i.sourceMap.SourceName(seg),
			Line:     seg.Line + 1,
			Col:      seg.Column + 1,
			Function: pickFunctionName(i.sourceMap, seg, trap.Reason),
		})
	}
	return out
}

// pickFunctionName picks a label for the resolved frame. Prefers the
// source map's symbol name when present; falls back to the trap's
// reason text (which carries the panic message for gn_panic-originated
// traps, and the wazero error string otherwise).
//
// We avoid returning an empty function name — a Frame with no symbol
// AND no file is just noise in the stack listing.
func pickFunctionName(sm *SourceMap, seg Segment, fallback string) string {
	if name := sm.SymbolName(seg); name != "" {
		return name
	}
	return fallback
}

// extractOffsets scans s for byte offsets a WASM trap might encode and
// returns them in textual order (which is also stack order: wazero
// prints leaf-first). Supported shapes, in priority order:
//
//   - "0x12C"           — hex with 0x prefix (wazero's own format)
//   - "offset=300"      — decimal after a known key (some custom builds)
//   - "@300"            — decimal after '@' (emscripten-ish)
//
// We deliberately do NOT scan bare decimal numbers — every trap reason
// has them (exit codes, page counts, etc.) and most are not offsets.
// The patterns above are unambiguous.
//
// Duplicates are preserved: a real stack can have the same offset
// appear twice (recursive frames, tail calls reusing instructions).
func extractOffsets(s string) []uint32 {
	if s == "" {
		return nil
	}
	var out []uint32

	for _, m := range hexOffsetRe.FindAllString(s, -1) {
		// Strip the "0x" so strconv.ParseUint reads the digits.
		v, err := strconv.ParseUint(m[2:], 16, 32)
		if err == nil {
			out = append(out, uint32(v))
		}
	}
	for _, m := range decOffsetRe.FindAllStringSubmatch(s, -1) {
		v, err := strconv.ParseUint(m[1], 10, 32)
		if err == nil {
			out = append(out, uint32(v))
		}
	}
	for _, m := range atOffsetRe.FindAllStringSubmatch(s, -1) {
		v, err := strconv.ParseUint(m[1], 10, 32)
		if err == nil {
			out = append(out, uint32(v))
		}
	}
	return out
}

var (
	hexOffsetRe = regexp.MustCompile(`0x[0-9a-fA-F]+`)
	decOffsetRe = regexp.MustCompile(`offset=(\d+)`)
	atOffsetRe  = regexp.MustCompile(`@(\d+)`)
)

func firstOrZero(s []uint32) uint32 {
	if len(s) == 0 {
		return 0
	}
	return s[0]
}
