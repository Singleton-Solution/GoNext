package reusable

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
)

// BlockNode is the on-wire shape of a single block tree node. Mirrors
// packages/ts/blocks-sdk/src/types.ts:Block — we keep the Go struct
// minimal here because the resolver only needs to look at Type and a
// "ref" attribute and copy everything else through verbatim.
//
// Attributes is a raw JSON object so the resolver doesn't unmarshal
// each attribute schema; it pulls "ref" out only when Type matches
// the reusable sentinel. InnerBlocks recurses.
type BlockNode struct {
	Type        string          `json:"type"`
	Attributes  json.RawMessage `json:"attributes,omitempty"`
	InnerBlocks []BlockNode     `json:"innerBlocks,omitempty"`
	// ClientID is editor-only but we deserialise it for round-trip
	// fidelity so callers re-encoding the resolved tree don't strip
	// nodes their editor state expects.
	ClientID string `json:"clientId,omitempty"`
}

// maxResolveDepth caps the resolver's recursion to defend against
// pathologically deep trees and any A → B → A cycles that survive the
// visited-set check (a cycle ought to short-circuit, but we still cap
// for safety). 64 levels matches Gutenberg's practical ceiling.
const maxResolveDepth = 64

// ResolveRefs walks tree and substitutes every node whose Type is
// "core/block" with the referenced entry's content (decoded from
// Entry.Content). A missing ref turns into a "core/missing" sentinel;
// a loop is broken by leaving the second visit as the same sentinel.
//
// The store is consulted via a single GetMany round-trip per call:
// the function first collects every referenced UUID it can see, then
// fetches them in bulk. Nested resolution (an entry that itself
// references another entry) recurses with an additional GetMany.
//
// The function is pure on its inputs — it never writes back. The
// renderer is expected to call it once at read time and pass the
// resolved tree to the block walker.
func ResolveRefs(ctx context.Context, store Store, tree []BlockNode) ([]BlockNode, error) {
	if len(tree) == 0 {
		return tree, nil
	}
	visited := make(map[uuid.UUID]struct{})
	return resolveAt(ctx, store, tree, visited, 0)
}

func resolveAt(
	ctx context.Context,
	store Store,
	tree []BlockNode,
	visited map[uuid.UUID]struct{},
	depth int,
) ([]BlockNode, error) {
	if depth >= maxResolveDepth {
		return tree, nil
	}

	// Collect the refs in this level so we can fan out one GetMany
	// per recursion frame. The same ref can appear twice in one
	// frame; we de-dupe inside the visited set.
	refs := make([]uuid.UUID, 0)
	seen := make(map[uuid.UUID]struct{})
	for i := range tree {
		if tree[i].Type != RefBlockType {
			continue
		}
		id, ok := extractRef(tree[i].Attributes)
		if !ok {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		if _, cycle := visited[id]; cycle {
			continue
		}
		refs = append(refs, id)
	}

	var fetched map[uuid.UUID]Entry
	if len(refs) > 0 {
		entries, err := store.GetMany(ctx, refs)
		if err != nil {
			return nil, fmt.Errorf("reusable: resolve get_many: %w", err)
		}
		fetched = make(map[uuid.UUID]Entry, len(entries))
		for _, e := range entries {
			fetched[e.ID] = e
		}
	}

	out := make([]BlockNode, 0, len(tree))
	for i := range tree {
		node := tree[i]
		if node.Type == RefBlockType {
			id, ok := extractRef(node.Attributes)
			if !ok {
				out = append(out, missingNode("invalid_ref"))
				continue
			}
			if _, cycle := visited[id]; cycle {
				out = append(out, missingNode("cycle"))
				continue
			}
			entry, found := fetched[id]
			if !found {
				out = append(out, missingNode("not_found"))
				continue
			}
			var inner []BlockNode
			if err := json.Unmarshal(entry.Content, &inner); err != nil {
				out = append(out, missingNode("invalid_content"))
				continue
			}
			// Mark this entry as visited for the recursion below so
			// a self-reference (entry pointing at itself) breaks
			// rather than running until maxResolveDepth.
			visited[id] = struct{}{}
			resolved, err := resolveAt(ctx, store, inner, visited, depth+1)
			delete(visited, id)
			if err != nil {
				return nil, err
			}
			out = append(out, resolved...)
			continue
		}
		// Non-ref node: recurse into innerBlocks.
		if len(node.InnerBlocks) > 0 {
			resolved, err := resolveAt(ctx, store, node.InnerBlocks, visited, depth+1)
			if err != nil {
				return nil, err
			}
			node.InnerBlocks = resolved
		}
		out = append(out, node)
	}
	return out, nil
}

// extractRef pulls the `ref` field out of a node's attributes JSON
// object. Returns ok=false when the value is missing, not a string,
// or not a parseable UUID.
func extractRef(attrs json.RawMessage) (uuid.UUID, bool) {
	if len(attrs) == 0 {
		return uuid.Nil, false
	}
	var holder struct {
		Ref string `json:"ref"`
	}
	if err := json.Unmarshal(attrs, &holder); err != nil {
		return uuid.Nil, false
	}
	if holder.Ref == "" {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(holder.Ref)
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

// missingNode builds a `core/missing` placeholder used in the
// resolved tree whenever a ref can't be substituted. The renderer is
// expected to surface this as an inert "this reusable block has been
// deleted" chip.
func missingNode(reason string) BlockNode {
	attrs, _ := json.Marshal(map[string]string{"reason": reason})
	return BlockNode{
		Type:       MissingBlockType,
		Attributes: attrs,
	}
}
