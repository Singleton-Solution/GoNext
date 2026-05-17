/**
 * Block-tree re-export surface for theme authors.
 *
 * Theme authors render block content with the same types the editor
 * persists; rather than ask them to import `@gonext/blocks-sdk`
 * separately and remember which package owns which type, we re-export
 * the block surface from this single SDK entry point. The contract:
 *
 *   import type { Block, BlockTree } from '@gonext/theme-sdk';
 *
 * is exactly equivalent to importing those names from
 * `@gonext/blocks-sdk` — there is no wrapping, no narrowing, no
 * "theme-flavored" subset. The block SDK remains the single source of
 * truth for the block tree's shape.
 *
 * Listed names match what `@gonext/blocks-sdk/src/index.ts` already
 * exports today. New surface added there should also land here so the
 * one-import promise holds; an `extract`-style audit on the upstream
 * package will catch the drift.
 */

export type {
  AttributesSchema,
  Block,
  BlockAttributes,
  BlockCategory,
  BlockDeprecation,
  BlockEditProps,
  BlockSaveProps,
  BlockSupports,
  BlockTree,
  BlockTypeDefinition,
  EditComponent,
  SaveComponent,
  ValidationError,
  ValidationResult,
} from '@gonext/blocks-sdk';
