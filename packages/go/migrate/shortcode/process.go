package shortcode

import (
	"fmt"
	"strings"

	"github.com/Singleton-Solution/GoNext/packages/go/migrate/html2blocks"
)

// Mode selects how the processor handles each shortcode it
// encounters. See doc.go for a high-level description; the table
// below summarises the per-mode semantics:
//
//	mode      | translator registered?       | translator missing?
//	----------|------------------------------|----------------------------
//	ModeMap   | translator output            | preserve as core/html
//	ModePreserve | preserve as core/html     | preserve as core/html
//	ModeStrip | inner content as plain text  | inner content as plain text
//
// "Preserve as core/html" emits the shortcode's verbatim Raw text
// in a core/html block. That keeps the source visible to operators
// who choose to re-process content later; nothing is silently lost.
type Mode uint8

const (
	// ModeMap routes registered shortcodes through their Translator
	// and falls back to ModePreserve for unregistered names. This is
	// the default for migrations that want maximum fidelity.
	ModeMap Mode = iota

	// ModePreserve keeps every shortcode as raw text inside a
	// core/html block. Useful when a downstream plugin will pick the
	// shortcode up and re-render at view time.
	ModePreserve

	// ModeStrip discards the shortcode tags but keeps the inner
	// content (the part between [name]…[/name]) as plain text. Self-
	// closing shortcodes vanish entirely. Lossy by design.
	ModeStrip
)

// String returns the CLI-friendly form of Mode.
func (m Mode) String() string {
	switch m {
	case ModeMap:
		return "map"
	case ModePreserve:
		return "preserve"
	case ModeStrip:
		return "strip"
	}
	return "unknown"
}

// ParseMode parses a CLI-supplied mode string. Empty input maps to
// ModeMap so callers can pass a flag through without a branch.
func ParseMode(s string) (Mode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "map":
		return ModeMap, nil
	case "preserve":
		return ModePreserve, nil
	case "strip":
		return ModeStrip, nil
	default:
		return ModeMap, fmt.Errorf("shortcode: unknown mode %q (want map|preserve|strip)", s)
	}
}

// Options control a single Process invocation.
type Options struct {
	// Mode selects map | preserve | strip. See the Mode docstring
	// for the table of behaviours.
	Mode Mode

	// Registry is the translator lookup for ModeMap. May be nil in
	// ModePreserve / ModeStrip. In ModeMap a nil registry causes
	// every shortcode to fall back to preserve-as-core/html.
	Registry *Registry
}

// Result is what Process returns. Blocks is the translated content
// suitable for storage; ProcessedCount and FellBackCount let the
// caller surface a "X shortcodes mapped, Y preserved" line in the
// migration report.
type Result struct {
	Blocks []html2blocks.Block

	// ProcessedCount is the number of shortcodes that were handled
	// (mapped, preserved, or stripped) — i.e. the total parsed count.
	ProcessedCount int

	// MappedCount is the subset of ProcessedCount that had a
	// registered translator and produced blocks. Always 0 outside
	// ModeMap.
	MappedCount int

	// FellBackCount is the subset of ProcessedCount that fell back
	// to preserve-as-core/html because no translator was registered.
	// Always 0 outside ModeMap.
	FellBackCount int

	// StrippedCount is the subset removed in ModeStrip.
	StrippedCount int
}

// ProcessString is a string-flavoured wrapper for ergonomics.
func ProcessString(src string, opts Options) (Result, error) {
	return Process([]byte(src), opts)
}

// Process walks src once, parsing shortcodes and emitting Blocks
// according to opts.Mode. Plain text between shortcodes is wrapped
// in a single core/paragraph block per literal run so the result is
// a valid block tree — callers that already render text outside this
// package (e.g. the WXR importer's html2blocks step) typically run
// shortcode replacement first, then feed the resulting HTML back
// into html2blocks.
//
// The function never errors today; the error return is reserved for
// future strict-mode behaviours (e.g. fail on unknown shortcode).
func Process(src []byte, opts Options) (Result, error) {
	scan := scanShortcodes(string(src))
	var (
		out    []html2blocks.Block
		result Result
	)
	for _, tok := range scan.Tokens {
		switch t := tok.(type) {
		case *literalToken:
			if t.Text != "" {
				out = append(out, html2blocks.Block{
					Name:  html2blocks.BlockParagraph,
					Attrs: map[string]any{"content": t.Text},
				})
			}
		case *shortcodeToken:
			result.ProcessedCount++
			blocks, kind := processOne(t.Code, opts)
			switch kind {
			case processedMapped:
				result.MappedCount++
			case processedFellBack:
				result.FellBackCount++
			case processedStripped:
				result.StrippedCount++
			}
			out = append(out, blocks...)
		}
	}
	result.Blocks = out
	return result, nil
}

// processedKind is the classification used to update Result counters.
type processedKind uint8

const (
	processedMapped processedKind = iota
	processedFellBack
	processedStripped
)

// processOne dispatches a single shortcode through the configured
// mode. Returns the emitted blocks and the kind so Process can
// update its counters.
func processOne(sc Shortcode, opts Options) ([]html2blocks.Block, processedKind) {
	switch opts.Mode {
	case ModeStrip:
		if sc.SelfClosing || strings.TrimSpace(sc.Inner) == "" {
			return nil, processedStripped
		}
		return []html2blocks.Block{{
			Name:  html2blocks.BlockParagraph,
			Attrs: map[string]any{"content": sc.Inner},
		}}, processedStripped

	case ModePreserve:
		return []html2blocks.Block{{
			Name:  "core/html",
			Attrs: map[string]any{"content": sc.Raw},
		}}, processedFellBack

	case ModeMap:
		if opts.Registry != nil {
			if t, ok := opts.Registry.Lookup(sc.Name); ok {
				if blocks := t(sc); len(blocks) > 0 {
					return blocks, processedMapped
				}
				// Translator chose to elide — count as mapped.
				return nil, processedMapped
			}
		}
		// No translator: preserve verbatim.
		return []html2blocks.Block{{
			Name:  "core/html",
			Attrs: map[string]any{"content": sc.Raw},
		}}, processedFellBack
	}
	// Unreachable in practice; treat as preserve.
	return []html2blocks.Block{{
		Name:  "core/html",
		Attrs: map[string]any{"content": sc.Raw},
	}}, processedFellBack
}
