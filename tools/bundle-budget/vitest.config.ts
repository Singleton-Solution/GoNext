/**
 * Vitest configuration for @gonext/bundle-budget.
 *
 * This package is Node-only (no React, no DOM), so we override the shared
 * jsdom default with the much faster `node` environment. Coverage thresholds
 * are tightened — this is a small, isolated tool and we want regressions
 * caught immediately.
 */
import { defineConfig, mergeConfig } from 'vitest/config';
import { baseConfig } from '@gonext/test-config';

export default mergeConfig(
  baseConfig,
  defineConfig({
    test: {
      environment: 'node',
      include: ['test/**/*.{test,spec}.ts', 'src/**/*.{test,spec}.ts'],
      coverage: {
        thresholds: {
          lines: 80,
          functions: 80,
          branches: 75,
          statements: 80,
        },
      },
    },
  }),
);
