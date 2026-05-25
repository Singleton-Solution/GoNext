/**
 * Vitest configuration for @gonext/sdk-plugin.
 *
 * Pure logic package — Node environment, no DOM. Coverage threshold
 * matches the project-wide ≥85% bar.
 */
import { defineConfig, mergeConfig } from 'vitest/config';
import { baseConfig } from '@gonext/test-config';

export default mergeConfig(
  baseConfig,
  defineConfig({
    test: {
      environment: 'node',
      include: ['test/**/*.{test,spec}.ts'],
      coverage: {
        include: ['src/**/*.ts'],
        exclude: ['src/**/*.d.ts'],
        thresholds: {
          lines: 85,
          functions: 85,
          branches: 80,
          statements: 85,
        },
      },
    },
  }),
);
