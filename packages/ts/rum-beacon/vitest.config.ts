/**
 * Vitest configuration for @gonext/rum-beacon.
 *
 * Pulls the shared base from `@gonext/test-config` and overrides only
 * what is package-specific: a single setup file (no JSX, no jest-dom
 * matchers — this package has no React surface), and the include glob
 * that picks up co-located `*.test.ts` files under src/.
 */
import { defineConfig, mergeConfig } from 'vitest/config';
import { baseConfig } from '@gonext/test-config';

export default mergeConfig(
  baseConfig,
  defineConfig({
    test: {
      include: ['src/**/*.{test,spec}.ts'],
      coverage: {
        thresholds: {
          lines: 80,
          functions: 80,
          branches: 70,
          statements: 80,
        },
      },
    },
  }),
);
