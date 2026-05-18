/**
 * @gonext/hooks-schemas — public entry.
 *
 * Re-exports the validator surface and the built-in schema catalog.
 * Consumers import from this single module:
 *
 *     import { createBuiltinRegistry } from '@gonext/hooks-schemas';
 *     const reg = createBuiltinRegistry();
 *     reg.validate('the_content', someString);
 *
 * The package is source-only; no build step. See package.json scripts
 * for the schema sync utility (`pnpm sync-schemas`) used to mirror the
 * Go-side canonical files into this package.
 */
export {
  SchemaRegistry,
  createBuiltinRegistry,
  builtinHookNames,
  HooksValidationError,
  HooksUnregisteredError,
  HooksUnsupportedDialectError,
  SCHEMA_DIALECT,
} from './validator.ts';
export type { EnforcementMode } from './validator.ts';
export { BUILTIN_SCHEMAS } from './schemas/index.ts';
export type { BuiltinHookName } from './schemas/index.ts';
