/**
 * Shared Vitest base configuration for GoNext TypeScript packages.
 *
 * Consumers extend it from their own `vitest.config.ts`:
 *
 *   import { defineConfig, mergeConfig } from 'vitest/config';
 *   import { baseConfig } from '@gonext/test-config';
 *
 *   export default mergeConfig(
 *     baseConfig,
 *     defineConfig({
 *       resolve: { alias: { '@': new URL('./src', import.meta.url).pathname } },
 *     }),
 *   );
 *
 * Or use the convenience helper:
 *
 *   import { defineExtendedConfig } from '@gonext/test-config';
 *   export default defineExtendedConfig({ test: { setupFiles: ['./vitest.setup.ts'] } });
 *
 * Decisions:
 *  - `environment: 'jsdom'` matches the package devDependency we ship today;
 *    teams can override to `'happy-dom'` (faster, see docs/11-testing-ci.md §4.1)
 *    once it is installed in the consuming app.
 *  - `globals: true` so tests can use `describe`/`it`/`expect` without imports,
 *    consistent with Jest muscle memory.
 *  - Coverage thresholds are intentionally conservative for a P0 skeleton —
 *    raise them per-app once real tests exist.
 *  - `restoreMocks` / `clearMocks` / `mockReset` are all on: each test gets a
 *    clean slate, which is the assumption the rest of the suite makes.
 */
import { defineConfig, mergeConfig, type UserConfig } from 'vitest/config';

export const baseConfig: UserConfig = defineConfig({
  test: {
    environment: 'jsdom',
    globals: true,
    css: true,
    clearMocks: true,
    mockReset: true,
    restoreMocks: true,
    include: ['src/**/*.{test,spec}.{ts,tsx}'],
    exclude: [
      '**/node_modules/**',
      '**/dist/**',
      '**/.next/**',
      '**/coverage/**',
    ],
    coverage: {
      provider: 'v8',
      reporter: ['text', 'lcov', 'html'],
      reportsDirectory: './coverage',
      include: ['src/**/*.{ts,tsx}'],
      exclude: [
        'src/**/*.{test,spec}.{ts,tsx}',
        'src/**/__mocks__/**',
        'src/test/**',
        'src/**/*.d.ts',
      ],
      thresholds: {
        lines: 60,
        functions: 60,
        branches: 60,
        statements: 60,
      },
    },
  },
});

/**
 * Convenience wrapper around `mergeConfig` so consumers don't have to import
 * both `defineConfig` and `mergeConfig` from vitest/config themselves.
 *
 * Anything passed in overrides the base. Use sparingly — most apps should only
 * need to add `resolve.alias` entries and a `setupFiles` pointer.
 */
export function defineExtendedConfig(override: UserConfig): UserConfig {
  return mergeConfig(baseConfig, defineConfig(override));
}
