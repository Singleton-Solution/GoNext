package render

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"strings"
)

// Block is the persisted shape of a single block node, in lock-step
// with the TypeScript Block type in packages/ts/blocks-sdk/src/types.ts.
//
// On the wire, the editor stores this as JSONB in
// `posts.content_blocks`. The renderer decodes that column straight
// into []Block (the BlockTree alias) and feeds it to a Walker.
//
// Attributes are deliberately typed as map[string]any rather than a
// generic — each renderer cracks the keys it cares about. The
// validator package (packages/ts/blocks-sdk) is what guarantees the
// keys are well-typed before the row hits the database; the Go
// renderer trusts that contract and defensively type-asserts at
// renderer time. A failed assertion is degraded to an empty string,
// not a panic, so a malformed block in one corner of a post does not
// take the whole page down.
type Block struct {
	// Type is the registry key, e.g. "core/paragraph" or
	// "wp-pricing/pricing-table". The walker dispatches on this.
	Type string `json:"type"`

	// Attributes is the per-type bag of options. Each renderer
	// narrows the keys it expects.
	Attributes map[string]any `json:"attributes"`

	// InnerBlocks are the recursive children. Walker renders them
	// depth-first and hands the resulting HTML to the parent's
	// renderer as the `inner` argument.
	InnerBlocks []Block `json:"innerBlocks,omitempty"`

	// ClientID is editor-only. The TS save pipeline strips it before
	// persisting; we model it here so the renderer can ingest live
	// editor previews unchanged.
	ClientID string `json:"clientId,omitempty"`
}

// BlockTree is the root of a render request — a flat slice of root
// blocks, matching the TS BlockTree type.
type BlockTree = []Block

// Context is the resolved block-context map, mirroring the
// BlockContextMap on the TS side. Keys are free-form strings ("postId",
// "postType", "queryId", …); values are unknown until the consumer
// type-asserts.
//
// A nil Context is equivalent to an empty one — renderers must
// tolerate either. Context is never mutated by the walker or by
// downstream renderers; the walker layers new context by allocating
// a fresh map on each provider boundary.
type Context map[string]any

// Renderer is a per-block-type render function. The walker invokes it
// with:
//
//   - the block being rendered (so the renderer can read its
//     Attributes and InnerBlocks count without re-walking the tree),
//   - the already-rendered inner HTML (depth-first), produced by
//     recursively walking InnerBlocks,
//   - the filtered Context map — only the keys the block's spec
//     declared as UsesContext are present.
//
// Returning a non-nil error short-circuits the walk: the parent's
// inner HTML for this child becomes an HTML comment carrying the
// error (so authors see a placeholder rather than a missing block),
// and the error is also reported on the walker's Errors slice. This
// matches the TS canvas's "loud but non-fatal" UnknownBlock contract.
type Renderer func(block Block, inner template.HTML, ctx Context) (template.HTML, error)

// BlockSpec is the per-block-type record the Walker dispatches on.
// It pairs a Renderer with the block's context plumbing — the same
// shape the TS BlockTypeDefinition uses for ProvidesContext /
// UsesContext.
//
// Renderer is required; both context slices are optional and default
// to "none", matching the WordPress Gutenberg opt-in contract.
type BlockSpec struct {
	// Render is the per-block-type rendering function. Required.
	Render Renderer

	// ProvidesContext lists attribute keys this block exposes to
	// descendants. The walker reads each key off the block's
	// Attributes (skipping missing entries silently) and merges
	// them into the inherited context before walking InnerBlocks.
	ProvidesContext []string

	// UsesContext lists context keys this block consumes. The
	// walker filters the inherited map down to these keys before
	// invoking Render. Listing a key that no ancestor provides is
	// non-fatal — the resolved value is simply absent.
	UsesContext []string
}

// ErrUnknownBlockType is returned by the walker (via WalkResult.Errors)
// when a block whose type is not registered is encountered. The walk
// continues — the unknown block surfaces as an HTML comment in its
// place. Use errors.Is to test for it.
var ErrUnknownBlockType = errors.New("render: unknown block type")

// WalkResult is the output of Walk. HTML is the rendered tree, safe
// to drop into a Go html/template; Errors collects every per-block
// failure the walker encountered (unknown types, renderer errors,
// malformed attributes) so callers can decide whether to surface a
// 5xx, log a warning, or keep serving the degraded HTML.
type WalkResult struct {
	HTML   template.HTML
	Errors []WalkError
}

// WalkError captures one per-block failure encountered during a Walk.
// The Path is the JSON-pointer-style location of the failing block in
// the input tree ("/0", "/0/innerBlocks/2"), matching the TS
// validator's ValidationError shape so editor toasts can reuse the
// same pointer.
type WalkError struct {
	// Path is the JSON-pointer-like location of the failing block.
	Path string
	// BlockType is the failing block's Type, when known.
	BlockType string
	// Err is the underlying error.
	Err error
}

// Error implements the error interface, producing a stable string for
// logging — "render: /0/innerBlocks/2: core/foo: …".
func (e WalkError) Error() string {
	var b strings.Builder
	b.WriteString("render: ")
	b.WriteString(e.Path)
	b.WriteString(": ")
	if e.BlockType != "" {
		b.WriteString(e.BlockType)
		b.WriteString(": ")
	}
	b.WriteString(e.Err.Error())
	return b.String()
}

// Unwrap exposes the underlying error for errors.Is / errors.As.
func (e WalkError) Unwrap() error { return e.Err }

// Walker renders a BlockTree against a Registry.
//
// Walker is stateless besides its Registry pointer — Walk may be
// called concurrently for distinct trees. The Registry itself is
// concurrency-safe for reads (see registry.go); callers should avoid
// mutating it while a walk is in flight, but doing so is not a data
// race in the Go memory-model sense.
type Walker struct {
	registry *Registry
}

// New constructs a Walker bound to the given Registry. The registry
// must not be nil; New panics on a nil registry because a render
// without a dispatch table can never produce output and a typed
// error here costs every caller a redundant check.
func New(reg *Registry) *Walker {
	if reg == nil {
		panic("render.New: registry is nil")
	}
	return &Walker{registry: reg}
}

// Walk renders the given tree with the supplied root context.
//
// The walk is depth-first: each block's InnerBlocks are rendered
// first, then the resulting concatenated HTML is handed to the
// parent's Renderer as the `inner` argument. This mirrors the
// TS canvas's recursion order.
//
// A nil ctx is treated as an empty map.
//
// The returned WalkResult.HTML is the concatenation of every root
// block's render output, in tree order. Errors encountered along the
// way (unknown block types, renderer failures, malformed attributes)
// are collected into WalkResult.Errors; the walk does not stop on
// them — see WalkError for the per-failure shape.
func (w *Walker) Walk(tree BlockTree, ctx Context) WalkResult {
	res := WalkResult{}
	if ctx == nil {
		ctx = Context{}
	}
	var html strings.Builder
	for i, block := range tree {
		path := fmt.Sprintf("/%d", i)
		out, errs := w.walkBlock(block, ctx, path)
		html.WriteString(string(out))
		res.Errors = append(res.Errors, errs...)
	}
	res.HTML = template.HTML(html.String())
	return res
}

// walkBlock renders a single block, recursing into its InnerBlocks
// first so the parent renderer receives already-rendered children
// as its `inner` argument.
//
// The depth-first traversal matches the TS canvas. The Context
// argument is the *inherited* context — the walker layers the
// block's own ProvidesContext on top before recursing into children,
// and filters down to UsesContext before invoking the block's
// renderer. This mirrors the TS canvas's filterConsumedContext /
// resolveProvidedContext flow.
func (w *Walker) walkBlock(block Block, inherited Context, path string) (template.HTML, []WalkError) {
	spec, ok := w.registry.Get(block.Type)
	if !ok {
		err := WalkError{
			Path:      path,
			BlockType: block.Type,
			Err:       fmt.Errorf("%w: %q", ErrUnknownBlockType, block.Type),
		}
		return unknownBlockHTML(block.Type), []WalkError{err}
	}

	// Layer the block's provided context for descendants.
	childCtx := mergeProvidedContext(inherited, block, spec.ProvidesContext)

	// Render inner blocks first (depth-first).
	var innerHTML template.HTML
	var errs []WalkError
	if len(block.InnerBlocks) > 0 {
		var inner strings.Builder
		for i, child := range block.InnerBlocks {
			childPath := fmt.Sprintf("%s/innerBlocks/%d", path, i)
			out, childErrs := w.walkBlock(child, childCtx, childPath)
			inner.WriteString(string(out))
			errs = append(errs, childErrs...)
		}
		innerHTML = template.HTML(inner.String())
	}

	// Filter the inherited context down to the block's UsesContext.
	// The block reads only what its spec declared.
	consumed := filterConsumedContext(inherited, spec.UsesContext)

	out, err := spec.Render(block, innerHTML, consumed)
	if err != nil {
		errs = append(errs, WalkError{
			Path:      path,
			BlockType: block.Type,
			Err:       err,
		})
		return renderErrorHTML(block.Type, err), errs
	}
	return out, errs
}

// mergeProvidedContext layers the block's ProvidesContext values on
// top of the inherited map, allocating a fresh map only when the
// block actually contributes new values. Otherwise the inherited map
// is returned unchanged so descendants keep sharing the parent's
// identity (mirrors the TS provider's empty-values fast path).
func mergeProvidedContext(inherited Context, block Block, keys []string) Context {
	if len(keys) == 0 {
		return inherited
	}
	merged := make(Context, len(inherited)+len(keys))
	for k, v := range inherited {
		merged[k] = v
	}
	added := 0
	for _, k := range keys {
		if v, ok := block.Attributes[k]; ok {
			merged[k] = v
			added++
		}
	}
	if added == 0 {
		// Nothing new to provide; preserve the inherited identity.
		return inherited
	}
	return merged
}

// filterConsumedContext narrows the inherited map to the keys a
// block opted into via its UsesContext list. Missing keys are
// silently dropped — descendants of a moved block see "no value"
// rather than an error.
func filterConsumedContext(inherited Context, keys []string) Context {
	if len(keys) == 0 || len(inherited) == 0 {
		return Context{}
	}
	out := make(Context, len(keys))
	for _, k := range keys {
		if v, ok := inherited[k]; ok {
			out[k] = v
		}
	}
	return out
}

// unknownBlockHTML renders the placeholder substituted in for a
// block whose type is not registered. The class name lets a theme
// surface the placeholder visibly in dev; the HTML comment carries
// the type so a "view source" inspection points at the missing
// registration.
func unknownBlockHTML(blockType string) template.HTML {
	safe := template.HTMLEscapeString(blockType)
	return template.HTML(fmt.Sprintf(
		`<div class="gn-block-unknown" role="alert" data-block-type=%q>`+
			`<!-- gn:unknown-block %s -->Unknown block: <code>%s</code></div>`,
		blockType, safe, safe,
	))
}

// renderErrorHTML is the placeholder substituted in for a block
// whose registered renderer returned an error. Same shape as
// unknownBlockHTML so themes can style both with one selector.
func renderErrorHTML(blockType string, err error) template.HTML {
	safeType := template.HTMLEscapeString(blockType)
	safeErr := template.HTMLEscapeString(err.Error())
	return template.HTML(fmt.Sprintf(
		`<div class="gn-block-error" role="alert" data-block-type=%q>`+
			`<!-- gn:render-error %s: %s -->Render error in <code>%s</code></div>`,
		blockType, safeType, safeErr, safeType,
	))
}

// DecodeTree decodes a JSONB-shaped byte payload (the column body of
// `posts.content_blocks`, or the request body of the /api/v1/render/
// preview handler) into a BlockTree. Empty input returns a nil tree
// — an empty post still renders as no HTML rather than an error.
//
// Decoding errors include the offset reported by encoding/json so a
// malformed editor save can be traced back to its bad byte without
// re-parsing the source twice.
func DecodeTree(body []byte) (BlockTree, error) {
	body = []byte(strings.TrimSpace(string(body)))
	if len(body) == 0 {
		return nil, nil
	}
	var tree BlockTree
	if err := json.Unmarshal(body, &tree); err != nil {
		return nil, fmt.Errorf("render: decode tree: %w", err)
	}
	return tree, nil
}
