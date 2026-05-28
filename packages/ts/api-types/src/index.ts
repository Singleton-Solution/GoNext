/**
 * @gonext/api-types — entry point.
 *
 * Re-exports everything from the generated module so consumers can write:
 *
 *   import type { paths, components } from '@gonext/api-types';
 *
 * The generated file is committed (see `src/generated.ts`) so consumers
 * don't need to run a codegen step on `pnpm install`. Whenever
 * `apps/api/openapi/openapi.yaml` changes, run `pnpm generate` here and
 * commit the diff alongside the spec change.
 */
export * from './generated';
