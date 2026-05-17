/**
 * Vitest configuration for @gonext/blocks-rich-text.
 *
 * Extends the shared base from `@gonext/test-config`. The Lexical editor
 * relies on DOM APIs (Selection, Range, MutationObserver) so we keep the
 * jsdom environment and wire jest-dom matchers through the setup file.
 * Co-located `*.test.tsx` files live next to their subject under `src/`;
 * the include glob already covers them.
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
          lines: 70,
          functions: 70,
          branches: 70,
          statements: 70,
        },
      },
    },
    esbuild: {
      jsx: 'automatic',
    },
  }),
);
