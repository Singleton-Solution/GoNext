/**
 * Vitest configuration for @gonext/blocks-core.
 *
 * Extends the shared base from `@gonext/test-config`. The core blocks ship
 * React `edit` components and DOM-serialising `save` outputs, so we keep
 * the jsdom environment, wire jest-dom matchers via the setup file, and let
 * esbuild handle JSX automatically. Each block lives under `src/<block>/`
 * with co-located `*.test.tsx` files — the include glob already covers them.
 */
import { defineConfig, mergeConfig } from 'vitest/config';
import { baseConfig } from '@gonext/test-config';

export default mergeConfig(
  baseConfig,
  defineConfig({
    test: {
      setupFiles: ['./vitest.setup.ts'],
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
    esbuild: {
      jsx: 'automatic',
    },
  }),
);
