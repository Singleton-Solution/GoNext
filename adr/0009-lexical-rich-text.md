# ADR 0009: Lexical is the in-block rich-text engine

- **Status**: proposed
- **Date**: 2026-05-17
- **Deciders**: @tayebmokni
- **Consulted**: doc 04 §4 (rich text editing), proposal Q04-2
- **Informed**: block editor authors, plugin authors who ship blocks with rich text

## Context

The block editor (doc 04, ADR 0008) arranges blocks. Inside a text-bearing block (paragraph, heading, list item, table cell, quote) the user expects **inline formatting** — bold, italic, code, link, mention, inline image — with cursor and selection semantics that match a content-editable region. The rich-text engine that handles this is the highest-frequency code in the editor: every keystroke, every selection change, every paste runs through it. Choosing it wrong is a hard-to-reverse decision because every text-bearing block is built on its API.

Three serious candidates, all production-grade, all React-compatible:

- **Lexical** (Meta). Tree of `LexicalNode`s, intentionally light. React-first API. Small core bundle (~22 KB min+gz). Looser schema — you enforce validity yourself. First-class Yjs binding for collab. Designed for FB-scale typing, so performance on huge documents is excellent.
- **TipTap / ProseMirror.** TipTap is a React wrapper on the rigorous, decade-mature ProseMirror core. Strict schema — can reject invalid transitions outright. Bigger bundle (~70 KB+ for PM + TipTap). Huge extension ecosystem. Mature Yjs binding. The underlying ProseMirror view is imperative — debugging React + an imperative view is the worst combination.
- **Slate.js.** React-first like Lexical, but documented performance problems on large documents and a less stable API. Strong creative-tools community; weaker for long-form CMS content.

The design-doc decision in doc 04 §4.1 lays out the dimensions in detail and recommends Lexical. The reasoning specific to this project:

1. **Composes with the block model.** ADR 0008 puts block-level structure in our JSONB tree. We do not want the rich-text engine to *also* think about block structure. Lexical's "shallow" attitude (a doc is text + inline marks + maybe some custom nodes) is the right level. ProseMirror's schema wants to model the whole document — running ProseMirror per-block (fine) means we use 30% of what it offers, paying its weight without its leverage.
2. **React-first.** The editor is a React app. Lexical's React bindings are the primary API, not a wrapper. TipTap's React layer is well done, but the underlying view stays imperative, which makes debugging interactions between React state and the editor view harder.
3. **Bundle size matters when we instantiate per block.** Doc 04 §1 instantiates one rich-text editor per text-bearing block in a document. A 50-block post with 30 text blocks means 30 Lexical instances; Lexical's smaller footprint matters at that multiplier.
4. **Yjs story is symmetric.** Both Lexical and TipTap have first-class Yjs bindings. The v2 collab plan (doc 04 §10) is not constrained by this choice.
5. **API velocity has stabilized.** Lexical is past its "everything is new every minute" phase. TipTap's API surface is more battle-tested across more years, but Lexical is no longer green.

What we give up by picking Lexical: TipTap's enormous extension marketplace and ProseMirror's stricter schema validation. We compensate by validating at the block layer (per-block JSON Schemas, doc 04 §2.1) and by keeping the inline format set deliberately small (doc 04 §4.2).

Proposal Q04-2 confirms Lexical with "high confidence" after the comparative analysis in doc 04 §4.1.

## Decision

The in-block rich-text editor is **Lexical** (Meta, Apache 2.0). One Lexical editor instance per text-bearing block. The block's stored attribute is our `RichText` shape (a Quill-Delta-ish `ops: InlineRun[]` per doc 04 §4.2), not Lexical's internal `EditorState`; a small adapter converts between them on mount and serialize. The inline mark vocabulary is small and explicit (bold, italic, underline, strike, code, link, color, highlight, kbd, sub, sup). Things that "feel inline" but break flow (inline images, footnotes) stay as separate blocks.

## Consequences

### Positive

- Smaller per-instance bundle than TipTap. For a typical text-heavy post (~30 text blocks), the savings compound.
- Idiomatic React API. Hooks, refs, and component composition work the way React developers expect. No imperative view layer to debug through.
- Lexical's headless renderer ships in-repo, which we use for SSR previews and (later) for server-side validation that matches client behavior.
- Our `RichText` shape stays the source of truth in the block tree. Lexical's `EditorState` is editor-runtime only — never persisted, never canonical. Swapping to a different rich-text engine in the future is constrained to the adapter, not the data.
- The small mark vocabulary keeps the design surface small. Plugin authors who want to extend it write a custom block, not a custom inline mark.

### Negative

- Smaller extension ecosystem than TipTap's. We will hand-roll the inline marks we need; we have a small, fixed set so this is bounded work, but it is work.
- Lexical is Meta-controlled. Apache 2.0 protects against license changes but does not guarantee maintenance pace. We accept the risk; adoption is healthy and the project is past its initial volatile phase.
- The looser schema means we enforce inline validity ourselves at the adapter boundary (`lexicalToRichText` rejects unknown marks, drops disallowed nodes). TipTap's schema would do this for us. We trade a more rigorous schema for a smaller, more React-shaped engine.
- Per-block instantiation means many Lexical instances on a long post. Lexical is engineered for this case, but it is a real cost; the editor performance plan (doc 04 §16) covers it.

### Neutral / accepted tradeoffs

- We do not pursue Notion-style "everything is a block." Inline images stay as Image blocks; inline footnotes are a v2 feature with a `footnote-ref` mark plus a sibling Footnotes block.
- The block-layer slash-command UX (doc 04 §3.2, §4.3) is implemented at the block layer, not as a Lexical plugin. Typing `/` at the start of an empty block opens the inserter; inside non-empty text, `/` is just `/`.
- We commit to staying on Lexical's stable channel. Major version bumps go through their own design review.

## Alternatives considered

### Option A: TipTap (ProseMirror)
- Rejected. Strict schema is overkill once block-layer JSON Schemas (doc 04 §2.1) already constrain validity. ~70 KB bundle vs Lexical's ~22 KB matters at per-block instantiation. Underlying imperative view layer fights React-debugging ergonomics. Ecosystem extensions are great but we use a small fixed set of inline marks.

### Option B: ProseMirror directly (no TipTap)
- Rejected. Avoids the React-wrapper indirection but inherits ProseMirror's view layer plus all of TipTap's schema weight without the convenience. The whole reason to use ProseMirror is the schema, which we do not need at this level.

### Option C: Slate.js
- Rejected. Documented performance problems on large documents (multi-thousand-node trees slow noticeably). API has been less stable across versions. Has its place in creative-tool editors; less defensible for CMS long-form content where document size grows unbounded.

### Option D: Quill
- Rejected. Older architecture, no native React story (community wrappers only), no first-class CRDT story for our v2 collab plan. Mature and battle-tested, but on the wrong side of every dimension we care about.

### Option E: A custom rich-text engine
- Rejected. The list of edge cases (selection across composition, IME, bidirectional text, RTL, accessible cursor announcement, paste sanitization) is enormous. Every CMS that has tried has either spent years on it or shipped broken. Reuse a maintained engine.

### Option F: Plain contentEditable + a small abstraction
- Rejected for the same reasons as E. contentEditable is famous for browser inconsistencies. Lexical and TipTap both exist to abstract over those.

## References

- Design doc: `docs/04-block-editor.md` §4.1 (Lexical vs TipTap comparison), §4.2 (inline format vocabulary)
- Design doc: `docs/04-block-editor.md` §17.2 (rejected: Slate.js)
- Proposal: `docs/proposals/14-proposals-content.md` Q04-2 (Lexical vs TipTap final call)
- Lexical: https://lexical.dev/
- Related ADRs: ADR 0008 (JSON block tree), ADR 0007 (admin app houses the editor)
