/**
 * Vitest setup for @gonext/sdk.
 *
 * Re-exports the shared setup module from @gonext/test-config:
 * jest-dom-style matchers (the SDK doesn't ship React but the
 * matchers are harmless), the loud-fetch stub (so any test that
 * forgets to mock fetch fails noisily), and per-test cleanup.
 *
 * Individual tests opt into a real `fetch` mock by setting
 * `globalThis.fetch = vi.fn(...)` in their own `beforeEach`.
 */
import '@gonext/test-config/setup';
