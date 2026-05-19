/**
 * Vitest setup for @gonext/web.
 *
 * Re-exports the shared setup module from @gonext/test-config (jest-dom
 * matchers, loud fetch stub, RTL cleanup). Per-test files override the
 * fetch stub via `vi.spyOn(global, 'fetch')` to return route-specific
 * fixture responses.
 */
import '@gonext/test-config/setup';
