/**
 * Vitest configuration for @gonext/docs.
 *
 * Extends `@gonext/test-config`'s base. The docs site does not have any
 * runtime-fetch components, but we still use jsdom (the base) so React
 * component tests can mount Next-flavoured Link components.
 */
import { defineConfig, mergeConfig } from 'vitest/config';
import { fileURLToPath } from 'node:url';
import { baseConfig } from '@gonext/test-config';

const root = fileURLToPath(new URL('./', import.meta.url));

export default mergeConfig(
  baseConfig,
  defineConfig({
    test: {
      setupFiles: ['./vitest.setup.ts'],
      include: ['{lib,components,app,src}/**/*.{test,spec}.{ts,tsx}'],
      coverage: {
        thresholds: {
          lines: 0,
          functions: 0,
          branches: 0,
          statements: 0,
        },
      },
    },
    resolve: {
      alias: {
        '@': root.replace(/\/$/, ''),
      },
    },
    esbuild: {
      jsx: 'automatic',
    },
  }),
);
