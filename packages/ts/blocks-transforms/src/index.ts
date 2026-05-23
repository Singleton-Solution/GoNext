/**
 * @gonext/blocks-transforms — public entry point.
 *
 * Block transforms are the "change block type" affordance every modern
 * editor has. Given a source block (e.g. `core/paragraph`), a transform
 * produces one or more target blocks (e.g. a `core/heading` carrying
 * the same text). The package ships a `TransformRegistry`, a curated
 * set of first-party transforms covering the obvious conversions, and
 * a `registerBuiltinTransforms()` helper that wires them up in one
 * line.
 *
 * Built-in catalogue:
 *  - `core/paragraph-to-heading` / `core/heading-to-paragraph`
 *  - `core/paragraph-to-quote`   / `core/quote-to-paragraph`
 *  - `core/list-to-paragraphs`   / `core/paragraphs-to-list`
 *  - `core/image-to-gallery`
 *  - `core/heading-level-up`     / `core/heading-level-down`
 *  - `core/code-to-paragraph`    / `core/paragraph-to-code`
 *  - `core/columns-to-group`     / `core/group-to-columns`
 *
 * Designed for tree-shaking: every symbol is a named export, and the
 * module carries no side-effects at evaluation time. Call
 * `registerBuiltinTransforms(registry)` from the editor host to
 * populate the registry.
 */
export type { Transform, TransformContext, TransformResult } from './types.ts';

export {
  DuplicateTransformError,
  TransformRegistry,
  type RegisterTransformOptions,
} from './registry.ts';

export {
  CORE_TRANSFORMS,
  DEFAULT_COLUMNS,
  codeToParagraph,
  columnsToGroup,
  escapeHtml,
  groupToColumns,
  headingLevelDown,
  headingLevelUp,
  headingToParagraph,
  imageToGallery,
  listToParagraphs,
  paragraphsToList,
  paragraphToCode,
  paragraphToHeading,
  paragraphToQuote,
  quoteToParagraph,
} from './builtins.ts';

export { registerBuiltinTransforms } from './registerBuiltins.ts';
