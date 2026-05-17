/**
 * Vitest setup for @gonext/blocks-rich-text.
 *
 * Re-exports the shared setup from @gonext/test-config (jest-dom matchers,
 * RTL cleanup, loud fetch stub). Lexical's headless code paths need a few
 * jsdom shims that aren't on by default — we patch them in below so the
 * editor mounts cleanly in tests without each spec having to do it.
 */
import '@gonext/test-config/setup';

// jsdom doesn't ship `getBoundingClientRect` results that satisfy Lexical's
// selection probing; the polyfill returns a zero-rect, which is what every
// Lexical test fixture upstream uses too.
if (typeof window !== 'undefined') {
  // Some Lexical helpers query the layout to position the toolbar / placeholder.
  // jsdom returns a stub, so we mirror Lexical's own test setup and stub
  // `scrollIntoView` to a no-op rather than letting it crash the test.
  if (!('scrollIntoView' in Element.prototype)) {
    Object.defineProperty(Element.prototype, 'scrollIntoView', {
      value: () => undefined,
      writable: true,
    });
  }
}
