/**
 * Vitest configuration for @gonext/web.
 *
 * Extends the shared base from `@gonext/test-config` with web-specific
 * aliases (`@/...` → `./src/...`) and points at the local setup file.
 *
 * Coverage thresholds are kept conservative for the initial renderer —
 * raise once the catch-all route grows additional branches (preview
 * mode, password-protected posts, etc.).
 */
import { defineConfig, mergeConfig } from 'vitest/config';
import { fileURLToPath } from 'node:url';
import { baseConfig } from '@gonext/test-config';

const srcPath = fileURLToPath(new URL('./src', import.meta.url));

export default mergeConfig(
  baseConfig,
  defineConfig({
    test: {
      setupFiles: ['./vitest.setup.ts'],
      include: ['src/**/*.{test,spec}.{ts,tsx}'],
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
        '@': srcPath,
      },
    },
    esbuild: {
      jsx: 'automatic',
    },
  }),
);
