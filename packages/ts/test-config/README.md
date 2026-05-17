# @gonext/test-config

Shared Vitest + React Testing Library configuration for GoNext TypeScript packages.

Centralises the boring parts (DOM env, coverage thresholds, jest-dom matchers, per-test cleanup) so every app and shared package starts from the same baseline. See [`docs/11-testing-ci.md`](../../../docs/11-testing-ci.md) §4 for the testing philosophy this implements.

## What you get

- **`baseConfig`** — a Vitest `UserConfig` with sensible defaults (jsdom env, globals enabled, v8 coverage, P0-skeleton thresholds).
- **`defineExtendedConfig(override)`** — convenience helper that merges your overrides into the base.
- **`@gonext/test-config/setup`** — a setup module that wires `@testing-library/jest-dom` matchers, a loud `fetch` stub, and per-test RTL cleanup.

## Install

Already in the workspace — just add a workspace dependency in your app's `package.json`:

```json
{
  "devDependencies": {
    "@gonext/test-config": "workspace:*",
    "vitest": "^1.6.0",
    "@vitest/coverage-v8": "^1.6.0",
    "@testing-library/react": "^14.2.0",
    "@testing-library/jest-dom": "^6.4.0",
    "jsdom": "^24.0.0"
  }
}
```

`vitest`, `@vitest/coverage-v8`, `@testing-library/react`, `@testing-library/jest-dom`, and a DOM environment (`jsdom` or `happy-dom`) are declared as peer dependencies — install them in the consuming app, not here.

## Wire it up

`apps/<your-app>/vitest.config.ts`:

```ts
import { defineConfig, mergeConfig } from 'vitest/config';
import { baseConfig } from '@gonext/test-config';

export default mergeConfig(
  baseConfig,
  defineConfig({
    test: {
      setupFiles: ['./vitest.setup.ts'],
    },
    resolve: {
      alias: {
        '@': new URL('./src', import.meta.url).pathname,
      },
    },
  }),
);
```

`apps/<your-app>/vitest.setup.ts`:

```ts
import '@gonext/test-config/setup';
```

That's it — `pnpm test` will pick up `src/**/*.test.{ts,tsx}` in your app.

## Switching to happy-dom

`docs/11-testing-ci.md` §4.1 recommends happy-dom over jsdom for our shape. The base config ships with jsdom because it matches the dev-dependency in `apps/admin/package.json` today. To opt in to happy-dom from a consuming app:

```ts
import { defineExtendedConfig } from '@gonext/test-config';

export default defineExtendedConfig({
  test: {
    environment: 'happy-dom',
    setupFiles: ['./vitest.setup.ts'],
  },
});
```

…and add `happy-dom` to the app's devDependencies.

## What's intentionally not here

- **MSW** is not pre-configured. The setup module ships a loud `fetch` stub so unmocked calls fail with a clear message; per-app setups should `import { server } from '@/test/msw'` and call `server.listen()` themselves (see `docs/11-testing-ci.md` §4.2).
- **Provider wrappers** (`renderWithAdmin`, etc.) live in each app's `src/test/render.tsx` — they're app-specific and don't belong in a shared package.
- **File-based snapshots** are deliberately not encouraged; the testing doc mandates inline snapshots only.

## Local validation

```sh
pnpm install
pnpm --filter @gonext/admin test
```
