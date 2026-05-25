/**
 * Vitest configuration for @gonext/sdk.
 *
 * Extends the shared base from `@gonext/test-config`. We run on
 * jsdom because the SDK exercises `document`, `fetch`, and a
 * fake `trustedTypes` shim in unit tests — a Node-only env would
 * fail any test that touches the DOM-side branches.
 *
 * Coverage thresholds are set to 80% line / 80% branch. The SDK
 * is the API contract every plugin in the ecosystem depends on;
 * lower coverage here translates directly into breakages plugin
 * authors will struggle to debug.
 */
import { defineConfig, mergeConfig } from 'vitest/config';
import { baseConfig } from '@gonext/test-config';

export default mergeConfig(
  baseConfig,
  defineConfig({
    test: {
      setupFiles: ['./vitest.setup.ts'],
      include: ['src/**/*.{test,spec}.ts'],
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
