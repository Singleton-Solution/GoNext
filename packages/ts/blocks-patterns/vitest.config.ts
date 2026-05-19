/**
 * Vitest configuration for @gonext/blocks-patterns.
 *
 * Extends the shared base from `@gonext/test-config`. Patterns are pure
 * data fixtures plus a registry, so a Node environment is fine — but we
 * keep jsdom so future tests that mount the inserter's "Patterns" tab
 * (importing from `@gonext/blocks-editor`) work without overrides.
 */
import { defineConfig, mergeConfig } from 'vitest/config';
import { baseConfig } from '@gonext/test-config';

export default mergeConfig(
  baseConfig,
  defineConfig({
    test: {
      include: ['src/**/*.{test,spec}.{ts,tsx}'],
      coverage: {
        thresholds: {
          lines: 80,
          functions: 80,
          branches: 80,
          statements: 80,
        },
      },
    },
  }),
);
