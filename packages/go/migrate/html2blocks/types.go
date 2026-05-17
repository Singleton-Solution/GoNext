package html2blocks

// Block mirrors the canonical GoNext Block Tree node shape. The JSON
// tags match the editor's serialized form so the converter's output can
// be stored directly via the same API the editor uses.
//
// At the time of writing there is no canonical Go-side `Block` struct
// in `packages/go/blocks` (the TS-side @gonext/blocks-core owns the
// authoritative definitions); when that package lands we will switch
// to import its type and drop this one. Field shapes are intentionally
// permissive: `Attrs` is `map[string]any` so any block's attribute
// schema serialises round-trip without a sealed enum here.
//
// TODO(blocks): swap to packages/go/blocks.Block once it exists (#352).
type Block struct {
	// ID is a stable client-generated identifier. The HTML converter
	// leaves this empty — IDs are assigned by the persistence layer
	// at write time so we don't burn UUID entropy in a CPU-bound walk
	// that may be re-run during import retries.
	ID string `json:"id,omitempty"`

	// Name is the registered block name, e.g. `core/paragraph`.
	Name string `json:"name"`

	// Attrs holds the typed attribute payload for this block. The
	// shape varies per block (see @gonext/blocks-core for each
	// block's attribute schema). We intentionally do not type these
	// here — the converter and renderer agree on string keys.
	Attrs map[string]any `json:"attrs,omitempty"`

	// InnerBlocks holds nested children for container blocks such as
	// quote, columns, and group. Leaf blocks (paragraph, heading,
	// image, separator, code, list) always have a nil/empty slice.
	InnerBlocks []Block `json:"innerBlocks,omitempty"`
}

// Canonical block-name constants. Grouped here so a future refactor that
// imports the names from packages/go/blocks only has to touch one file.
const (
	BlockParagraph = "core/paragraph"
	BlockHeading   = "core/heading"
	BlockList      = "core/list"
	BlockImage     = "core/image"
	BlockQuote     = "core/quote"
	BlockCode      = "core/code"
	BlockSeparator = "core/separator"
)
