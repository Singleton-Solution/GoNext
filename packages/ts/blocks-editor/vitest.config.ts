/**
 * Vitest configuration for @gonext/blocks-editor.
 *
 * Extends the shared base from `@gonext/test-config`. We need a DOM here
 * (the inserter and canvas are React components) so we stay on jsdom, and
 * we wire `react-jsx` as the esbuild JSX target so test files don't need
 * `import React` boilerplate. The setup file pulls in jest-dom matchers
 * and RTL `cleanup()` after every test.
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
