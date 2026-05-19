/**
 * Vitest setup for @gonext/docs.
 *
 * Re-exports the shared setup from @gonext/test-config: jest-dom matchers,
 * fetch stub, and per-test cleanup. Add docs-specific globals (e.g. an
 * MSW server when we wire one in) below the import.
 */
import '@gonext/test-config/setup';
