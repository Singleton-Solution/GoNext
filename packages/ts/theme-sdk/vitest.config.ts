/**
 * Vitest configuration for `@gonext/theme-sdk`.
 *
 * Extends the shared base from `@gonext/test-config`. Like `blocks-sdk`,
 * we run in the `node` environment because this is a pure data/types
 * package — there's no DOM involved — and we tighten coverage thresholds
 * to ≥85% per the project standard.
 */
import { defineConfig, mergeConfig } from 'vitest/config';
import { baseConfig } from '@gonext/test-config';

export default mergeConfig(
  baseConfig,
  defineConfig({
    test: {
      environment: 'node',
      include: ['src/**/*.{test,spec}.ts'],
      coverage: {
        thresholds: {
          lines: 85,
          functions: 85,
          branches: 85,
          statements: 85,
        },
      },
    },
  }),
);
