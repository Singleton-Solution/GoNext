/**
 * @gonext/blocks-sdk — public entry point.
 *
 * The SDK provides:
 *
 *  - **Types** for the block tree (`Block`, `BlockTree`, `BlockAttributes`,
 *    `BlockTypeDefinition`, `BlockSupports`, etc.).
 *  - **JSON Schema documents** for structural validation
 *    (`BlockJSONSchema`, `BlockTreeJSONSchema`).
 *  - **A validator** that checks both shape AND per-block attribute
 *    schemas (`BlockValidator`, `validateBlockTree`).
 *  - **A registry** that plugins and core call into to register their
 *    block types (`BlockRegistry`, `DuplicateBlockTypeError`).
 *  - **Migration helpers** for the deprecation pipeline (`migrateBlock`,
 *    `migrateBlockTree`).
 *
 * Designed for tree-shaking: every symbol is a named export, no default,
 * no side-effects at module evaluation time.
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
} from './types.ts';

export {
  BLOCK_SCHEMA_ID,
  BLOCK_TREE_SCHEMA_ID,
  BlockJSONSchema,
  BlockTreeJSONSchema,
  SCHEMA_DIALECT,
  isPinnedDialect,
} from './schema.ts';

export {
  assertPinnedDialect,
  BlockValidator,
  UnsupportedDialectError,
  validateBlockTree,
  type BlockTypeLookup,
} from './validator.ts';

export {
  BlockRegistry,
  DuplicateBlockTypeError,
  type RegisterOptions,
} from './registry.ts';

export { migrateBlock, migrateBlockTree } from './migrate.ts';
