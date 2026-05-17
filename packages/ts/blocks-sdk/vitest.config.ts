/**
 * Vitest configuration for @gonext/blocks-sdk.
 *
 * Extends the shared base from `@gonext/test-config`. We override the
 * environment to `node` because this is a pure data/types package — no DOM
 * is involved — and we tighten coverage thresholds to the ≥85% the issue
 * mandates.
 */
import { defineConfig, mergeConfig } from 'vitest/config';
import { baseConfig } from '@gonext/test-config';

export default mergeConfig(
  baseConfig,
  defineConfig({
    test: {
      environment: 'node',
      include: ['src/**/*.{test,spec}.ts'],
      coverage: {
        thresholds: {
          lines: 85,
          functions: 85,
          branches: 85,
          statements: 85,
        },
      },
    },
  }),
);
