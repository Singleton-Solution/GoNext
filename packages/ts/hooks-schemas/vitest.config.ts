/**
 * Vitest configuration for @gonext/hooks-schemas.
 *
 * Mirrors the @gonext/blocks-sdk config: pure data/types package, no DOM
 * involvement, coverage held to the project-wide ≥85% bar.
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
