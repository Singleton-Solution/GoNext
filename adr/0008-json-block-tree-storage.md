# ADR 0008: Content is stored as a JSONB block tree, not HTML with comment delimiters

- **Status**: proposed
- **Date**: 2026-05-17
- **Deciders**: @tayebmokni
- **Consulted**: doc 04 §1 (block data model), doc 01 §10.5 (`posts` table)
- **Informed**: block editor authors, theme authors, plugin authors

## Context

A block-based editor has to store its content somewhere. The two viable storage shapes are:

1. **Tree-as-data.** The block tree is the source of truth, stored as a structured value (JSONB, here). Rendered HTML is a derived artifact cached alongside.
2. **HTML-with-delimiters.** The rendered HTML is the source of truth, with the block structure encoded as specially-formatted comments. This is what WordPress Gutenberg does — a paragraph block is stored as `<!-- wp:paragraph --><p>text</p><!-- /wp:paragraph -->`. The HTML is parsed at the boundary (read or write) to recover the tree.

WordPress picked HTML-with-delimiters for a backward-compatibility reason: the `posts.post_content` column was a `TEXT` field of HTML for two decades before Gutenberg landed. Storing blocks as comment-delimited HTML kept the schema unchanged. It is a brilliant compatibility hack, and it is also the source of a long list of practical problems:

- **Unqueryable.** "Find me all posts that use the `gn-shop/product-card` block" requires parsing every post's HTML. There is no index that can answer this question in less than O(N).
- **Slow to parse.** Every save round-trips through a tokenizer that has to recover the tree from the HTML. Plugins that edit content via the database (perfectly common in WP) routinely produce malformed block markup that the parser then has to defend against.
- **Plugin-unsafe edits.** If a plugin does a substring replace on the HTML (very common — think "find/replace across all posts"), the block delimiters can be silently corrupted.
- **Round-trip fidelity is fragile.** Edit a block in the JS editor, the editor serializes to HTML, the server stores HTML, the editor reads HTML back, parses to tree, displays. Any mismatch between the serializer and the parser (different attribute escaping, different whitespace) shows up as "the editor says the post has changed."
- **Validation is post-hoc.** You can only check whether the block structure is valid after parsing the HTML.

The doc 04 §1.3 design explicitly rejects this shape. Concrete decisions in the design that depend on tree-as-data:

- Doc 01 §10.5 stores `posts.content_blocks JSONB`. A GIN index on `content_blocks` lets us answer "which posts contain block X" in milliseconds.
- Doc 04 §1.4 caches `posts.content_rendered TEXT` as a denormalized projection. The cache is invalidated and regenerated; it is never the source of truth.
- Doc 04 §1.4 stores `posts.content_blocks_hash BYTEA` so the pre-render cache can key on tree content. Hashing HTML is fragile (whitespace, attribute order); hashing canonical JSON is not.
- The cache invalidation outbox (doc 07 §16, ADR 0011) carries block-render-keyed invalidations; that depends on a stable identity for the tree.
- The migration importer (doc 08) converts WP's HTML-with-comments to our JSON tree on import. The reverse-direction export is a deliberate non-goal — we are migrating *from* WP, not maintaining round-trip compatibility *to* it.

The doubled storage (block tree JSON + rendered HTML) is real. Doc 04 §1.4 accepts it: Postgres TOAST compression handles the redundancy well, and for an average post (5KB blocks JSON + 8KB rendered HTML) the doubling is fine. The denormalization buys us read-path latency that we would otherwise pay every page render.

## Decision

`posts.content_blocks` is a **JSONB** column holding the canonical block tree. `posts.content_rendered` is a **TEXT** column holding the pre-rendered HTML — a derived cache, not the source of truth — refreshed on save and on dependency-driven invalidation. `posts.content_blocks_hash BYTEA` and `posts.content_rendered_at TIMESTAMPTZ` track the cache's keying and freshness. A GIN index on `content_blocks jsonb_path_ops` supports "which posts use block X" queries. We do not store the block tree as comment-delimited HTML anywhere.

## Consequences

### Positive

- Block lookups are indexable. Common queries the design depends on ("which posts use this block," "which posts use this plugin's blocks before uninstalling") run in milliseconds.
- Save validation is structural. Per-block JSON Schemas (doc 04 §2.1) validate every block on save. Invalid trees are rejected with precise error messages.
- Plugin edits are safe. A plugin doing `db.write` on the `posts` table works against typed JSON, not HTML strings. Find-and-replace across posts cannot corrupt block delimiters.
- Round-trip fidelity is trivial. The tree the editor saves is the tree it reads back. No serializer/parser mismatch.
- Block migrations (doc 04 §8.2) operate on the JSON, not on HTML. The migration runner walks the tree, applies version-bump transforms per block, and rewrites the tree.
- Cache keying is honest. The hash of the canonical JSON is stable across reads; hashing HTML would not be.
- Plain-text projection (`posts.content_text`) is derived from the tree at save time and feeds full-text search (doc 01 §8).

### Negative

- Storage doubling. The same content lives twice: once as a tree, once as rendered HTML. Average post: ~5KB + ~8KB. TOAST compression mitigates most of it. We accept the cost.
- Direct-SQL inspection is less ergonomic. `SELECT post_content FROM wp_posts` shows readable HTML in WP; `SELECT content_blocks FROM posts` shows JSON the human has to decode. Mitigation: admin tooling shows rendered preview, not raw column dumps.
- Migration into WP-shaped tools (e.g., themes designed to read `post_content` HTML) is awkward. We supply `content_rendered`; tools that need it can read that column.
- Plugins that want to do regex-on-HTML for some legitimate reason (a search-replace plugin, a content-rewriting middleware) have to walk the JSON instead. This is the right tradeoff but is a learning curve for WP-trained plugin authors.

### Neutral / accepted tradeoffs

- We rejected a fully normalized blocks table (separate `blocks` table joined to `posts`) — doc 04 §17.6. The read-time JOIN cost on every page render outweighs the gains, and the JSON tree is the natural shape for a recursive `innerBlocks` structure.
- We rejected storing only the tree and rendering on every request — doc 04 §17.5. The render cost is real (5-50ms per post for a 50-block document), and caching it in the same row as the tree is the simplest answer.
- We never expose the rendered HTML as canonical to plugins. Plugins read the tree.

## Alternatives considered

### Option A: WordPress-style HTML with `<!-- wp:paragraph -->` comment delimiters
- Rejected. Unqueryable without parsing every post. Slow to parse. Plugin-unsafe under substring edits. Round-trip-fragile between editor and server. The whole list of doc 04 §1.3 reasons applies. WP keeps this shape for back-compat with their two-decade-old TEXT column; we have no such legacy.

### Option B: Fully normalized blocks table (one row per block, joined to posts)
- Rejected per doc 04 §17.6. Read-time JOIN cost on every render (a 50-block post becomes a 50-row read), recursive `innerBlocks` modeled with parent_id is awkward, and JSONB gives us the same indexability via GIN without the join cost.

### Option C: Tree stored in JSONB, no pre-rendered HTML cache
- Rejected per doc 04 §17.5. Rendering on every request adds 5-50ms of latency per page for content the editor knows is static. The pre-render cache is the right answer; the doubled storage is acceptable.

### Option D: Tree as MessagePack or another binary format in `BYTEA`
- Rejected. JSONB gives us native Postgres operators (`@>`, `->>`, `jsonb_path_query`), GIN indexing, and human readability for debugging. MessagePack would shave bytes but loses every Postgres-side affordance.

### Option E: Tree in a separate `post_blocks` table (one row per post, JSONB column)
- Rejected. Adds a join on every read for no schema benefit. The JSONB column belongs on `posts` where it is queried with the rest of the post's data.

## References

- Design doc: `docs/01-core-cms.md` §10.5 (`posts` table DDL), §13.5 (rejected: HTML with delimiters)
- Design doc: `docs/04-block-editor.md` §1 (block data model), §1.3 (why a JSON tree), §1.4 (storage)
- Design doc: `docs/04-block-editor.md` §17.5–§17.6 (rejected alternatives)
- Related ADRs: ADR 0003 (UUID v7), ADR 0004 (Postgres + JSONB), ADR 0009 (Lexical for in-block rich text), ADR 0011 (cache invalidation)
