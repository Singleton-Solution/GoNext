/**
 * Vitest configuration for @gonext/plugin-frontend-host.
 *
 * Extends the shared base from `@gonext/test-config`. The script-tag
 * component and Trusted Types policy both touch the DOM (the policy
 * wraps DOMPurify which inspects the global window), so we stay on
 * jsdom and wire up jest-dom matchers via the setup file. The SRI and
 * import-map modules are pure data — they happily run in the same DOM
 * environment, so there's no need to split into a second project.
 *
 * Coverage thresholds are set at the security-critical 90% line: this
 * package is the host-side enforcement point for plugin XSS isolation,
 * and uncovered branches here translate directly into exploitable
 * surface area.
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
          lines: 90,
          functions: 90,
          branches: 85,
          statements: 90,
        },
      },
    },
    esbuild: {
      jsx: 'automatic',
    },
  }),
);
