/**
 * Vitest setup for @gonext/plugin-frontend-host.
 *
 * Re-exports the shared setup module from @gonext/test-config. The shared
 * setup wires jest-dom matchers, the loud fetch stub, and per-test RTL
 * cleanup — everything this package's component tests need.
 */
import '@gonext/test-config/setup';
