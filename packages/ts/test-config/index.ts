/**
 * @gonext/test-config — public entry point.
 *
 * Re-exports the shared Vitest base config and setup helpers so consumers can
 * write a one-line import in their per-app `vitest.config.ts`:
 *
 *   import { baseConfig } from '@gonext/test-config';
 *
 * And reference the setup file from their `vitest.setup.ts`:
 *
 *   import '@gonext/test-config/setup';
 *
 * See README.md for the full integration guide.
 *
 * Note: we re-export with an explicit `.ts` extension so the entry point can
 * be loaded by Node's native ESM resolver (vitest's config loader uses it
 * when the consuming app sets `"type": "module"`).
 */
export { baseConfig, defineExtendedConfig } from './vitest.base.ts';
