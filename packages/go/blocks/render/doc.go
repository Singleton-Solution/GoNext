// Package render implements the server-side block render walker for
// GoNext. It mirrors the editor-side render pipeline in
// packages/ts/blocks-editor: take a block tree (JSON-decoded into
// []Block), walk the tree, and emit safe HTML for the public web.
//
// The shape of the package is intentionally small:
//
//   - Block is the in-memory representation of a block node, identical
//     in shape to the on-wire JSON.
//   - Context is the resolved "block context" map (postId, postType,
//     queryId, …) that a parent block exposes to its descendants. It
//     mirrors the React Context the editor's BlockContextProvider
//     threads through the tree.
//   - Renderer is the per-block-type function the walker dispatches
//     to. Renderers receive the block, the already-rendered inner
//     HTML, and the (filtered) inherited context.
//   - Registry maps a block type ("core/paragraph", "wp-query/query")
//     to its renderer. The core block renderers (heading, paragraph,
//     list, image, columns, group, …) are pre-populated by
//     RegisterCoreBlocks; plugins register their own via Register.
//   - Walker walks a tree of Blocks against a Registry and produces a
//     template.HTML payload safe to drop into a Go template.
//
// The renderer uses html/template's safety helpers for any
// user-controlled string interpolation. The walker itself never
// blesses output: every renderer is responsible for escaping its
// own inputs before assembling the returned HTML.
//
// Block context — a parent block can declare ProvidesContext keys
// that are read off its attributes; those values flow down through
// the walker into descendant renderers' Context argument. Descendants
// opt in via UsesContext on their BlockSpec — the walker filters the
// inherited map down to those keys before invoking the renderer, so
// a bystander block never sees state it didn't ask for. This pairs
// with the TS-side block-context.tsx contract.
//
// The render pipeline is single-pass and depth-first, mirroring the
// TS canvas's recursion order. The walker has no implicit caching
// behaviour; callers wanting per-render memoisation wrap the result.
package render
