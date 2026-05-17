/**
 * Vitest configuration for @gonext/admin.
 *
 * Extends the shared base from `@gonext/test-config` with admin-specific
 * aliases (`@/...` → `./src/...`) and points at the local setup file.
 *
 * The base config supplies jsdom env, globals, coverage thresholds, and
 * include/exclude globs — keep this file minimal so the shared config
 * remains the single source of truth.
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
      // Tests live next to the code they cover.
      include: ['src/**/*.{test,spec}.{ts,tsx}'],
    },
    resolve: {
      alias: {
        '@': srcPath,
      },
    },
  }),
);
