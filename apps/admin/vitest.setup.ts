/**
 * Vitest setup for @gonext/admin.
 *
 * Re-exports the shared setup module from @gonext/test-config, which:
 *  - registers @testing-library/jest-dom matchers
 *  - installs a loud fetch stub (unmocked network calls throw)
 *  - runs RTL `cleanup()` after every test
 *
 * Admin-specific globals (e.g. MSW server registration once `src/test/msw.ts`
 * lands per issue #240 acceptance criteria) should be added below this import.
 */
import '@gonext/test-config/setup';

// Radix UI primitives (Select, Tabs, Dialog) instantiate a
// ResizeObserver during mount. jsdom does not provide one, so we
// install a no-op shim. Without this the Select / Tabs tests fail
// with `ReferenceError: ResizeObserver is not defined` before the
// first assertion runs.
class NoopResizeObserver {
  observe(): void {}
  unobserve(): void {}
  disconnect(): void {}
}
const globalAny = globalThis as unknown as {
  ResizeObserver?: typeof NoopResizeObserver;
};
if (typeof globalAny.ResizeObserver === 'undefined') {
  globalAny.ResizeObserver = NoopResizeObserver;
}

// Radix Select also reads `hasPointerCapture`, which jsdom doesn't
// implement on HTMLElement.prototype. Default to a noop so the
// pointer-capture branch is a no-op in tests.
if (typeof Element !== 'undefined') {
  const elementProto = Element.prototype as unknown as Record<string, unknown>;
  if (!elementProto.hasPointerCapture) {
    elementProto.hasPointerCapture = () => false;
  }
  if (!elementProto.scrollIntoView) {
    elementProto.scrollIntoView = () => {};
  }
}
