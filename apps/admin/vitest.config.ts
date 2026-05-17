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
      // Coverage thresholds are intentionally low for the scaffold — most
      // surfaces are inert placeholders that ship their behavior in
      // subsequent issues. Raise these once the first real feature lands.
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
    // Vitest uses esbuild for TS/TSX. Switch JSX to the automatic runtime so
    // React 19 components don't need `import React` at the top of every file
    // (the runtime injects `react/jsx-runtime` for us). Matches Next's
    // jsx: "preserve" + automatic transform behavior.
    esbuild: {
      jsx: 'automatic',
    },
  }),
);
