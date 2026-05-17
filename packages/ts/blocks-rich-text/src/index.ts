/**
 * `@gonext/blocks-rich-text` — public entry point.
 *
 * Ships the canonical `<RichText/>` Edit primitive plus the inline-run
 * model (`InlineRun`, builders, type guards) and the HTML round-trip
 * helpers (`serializeInline`, `deserializeInline`).
 *
 * Consumers (core blocks, plugin blocks, future paste pipeline):
 *
 *   import { RichText } from '@gonext/blocks-rich-text';
 *
 *   <RichText
 *     value={attributes.content}
 *     onChange={(html) => setAttributes({ content: html })}
 *     placeholder="Paragraph placeholder"
 *   />
 *
 * The package sits *below* `@gonext/blocks-core` in the dependency graph
 * so the core blocks can pull it in without circular references.
 */

export { RichText, type RichTextProps, type RichTextFormat } from './RichText.tsx';

export {
  type InlineRun,
  type InlineMarks,
  type InlineTextRun,
  type InlineLinkRun,
  type InlineLineBreakRun,
  text,
  isLinkRun,
  isTextRun,
  isLineBreakRun,
} from './inline.ts';

export { serializeInline, flattenInlineToText } from './serialize.ts';
export { deserializeInline, stringToInline } from './deserialize.ts';
