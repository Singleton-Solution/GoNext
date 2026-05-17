/**
 * Canonical inline-run model.
 *
 * The block-tree on disk persists a plain string for paragraph/heading/list/
 * quote *content* attributes today (see `@gonext/blocks-core`'s save outputs).
 * Once rich inline runs land in the schema the content will widen to
 * `string | InlineRun[]`, but until that schema change ships every save()
 * must still flatten to a string so round-trip tests pass byte-for-byte.
 *
 * `InlineRun` is the in-memory shape `<RichText/>` works with internally
 * and the shape `serialize` / `deserialize` use as their pivot point.
 * Three run kinds are enough for the first pass:
 *
 *  - **text** — plain run with optional `bold` / `italic` / `code` marks.
 *  - **link** — `<a href>` wrapping nested runs (no nesting beyond depth 1
 *    in practice today, but the shape allows it).
 *  - **linebreak** — explicit `<br>` between runs inside the same block.
 *
 * The serializer collapses adjacent text runs that share the same marks so
 * the canonical HTML stays minimal — a paragraph like `"Hello world"`
 * survives a round-trip as a single text run, not three.
 */

/** Inline formatting marks that can stack on a text run. */
export interface InlineMarks {
  bold?: boolean;
  italic?: boolean;
  /** Inline `<code>` styling — distinct from the block-level `core/code`. */
  code?: boolean;
}

/** Plain text content, possibly with stacked formatting marks. */
export interface InlineTextRun {
  type: 'text';
  text: string;
  marks?: InlineMarks;
}

/** `<a href>` wrapping a sequence of nested runs. */
export interface InlineLinkRun {
  type: 'link';
  href: string;
  /** Optional rel attribute — `noopener noreferrer` for external links. */
  rel?: string;
  /** Optional target attribute — `_blank` for new-tab links. */
  target?: string;
  children: InlineRun[];
}

/** Hard line break — rendered as `<br>` in the canonical HTML output. */
export interface InlineLineBreakRun {
  type: 'linebreak';
}

export type InlineRun = InlineTextRun | InlineLinkRun | InlineLineBreakRun;

/**
 * Convenience constructor — wraps the discriminated-union noise so spec
 * fixtures can call `text('hello', { bold: true })` instead of spelling
 * the whole literal.
 */
export function text(value: string, marks?: InlineMarks): InlineTextRun {
  if (marks && Object.keys(marks).length > 0) {
    return { type: 'text', text: value, marks };
  }
  return { type: 'text', text: value };
}

/** Type guard for the link variant — keeps the serializer readable. */
export function isLinkRun(run: InlineRun): run is InlineLinkRun {
  return run.type === 'link';
}

/** Type guard for the text variant — keeps the serializer readable. */
export function isTextRun(run: InlineRun): run is InlineTextRun {
  return run.type === 'text';
}

/** Type guard for the linebreak variant. */
export function isLineBreakRun(run: InlineRun): run is InlineLineBreakRun {
  return run.type === 'linebreak';
}
