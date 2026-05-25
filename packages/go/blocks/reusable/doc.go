// Package reusable is the read/write path for GoNext's "reusable" (synced)
// blocks — named block-tree snippets that any post can reference via the
// `core/block` ref.
//
// See migrations/000032_reusable_blocks.up.sql for the table; see issue
// #193 for the product motivation. The admin CRUD lives in
// apps/api/internal/admin/blocks (it depends on this package's Store).
//
// # Wire shape
//
// Inserting a reusable block in the editor materialises as a placeholder
// node of type "core/block" carrying the entry's UUID:
//
//	{
//	  "type": "core/block",
//	  "attributes": { "ref": "<uuid>" }
//	}
//
// The renderer resolves the ref at read time, splicing the referenced
// row's content tree in place of the placeholder. An edit on the
// inlined surface writes back to the row, propagating to every other
// instance of the same UUID — that's the "synced" half of the contract.
//
// # Stores
//
// Two implementations:
//
//   - MemoryStore is an in-process map backing tests and the no-DB
//     development fall-through. No persistence, no concurrency tricks
//     beyond a sync.RWMutex.
//
//   - PgxStore writes parameterised SQL against reusable_blocks. The
//     admin handler wires this in production.
//
// Both satisfy the same Store interface — the admin handler doesn't
// know which it's holding.
//
// # Resolving refs on the read path
//
// ResolveRefs walks a block tree and replaces every node whose Type is
// "core/block" with the referenced entry's Content (a BlockTree). A
// missing ref leaves a sentinel "core/missing" node so the renderer
// can surface "this reusable block has been deleted" without crashing
// the page. A loop (entry A references entry B references A) is
// detected by a visited-set; subsequent visits become the missing
// sentinel.
//
// The function is pure on its inputs — it never writes back. The
// renderer is expected to call it once at read time and pass the
// resolved tree to the block walker.
package reusable
