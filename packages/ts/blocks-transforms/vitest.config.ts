/**
 * Vitest configuration for @gonext/blocks-transforms.
 *
 * Extends the shared base from `@gonext/test-config`. Transforms are pure
 * functions over `Block` / `BlockTree`, so a Node environment would
 * suffice — but the base config keeps `jsdom` for parity with the rest
 * of the block packages (and so an editor-side consumer test that
 * mounts a transform-dependent component doesn't need an override).
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
