/**
 * Trusted-Types compatibility smoke test.
 *
 * jsdom does not implement the Trusted Types API natively, so we
 * stand up a small fake `window.trustedTypes` that mimics the
 * `createPolicy` shape. The test verifies:
 *
 *   - The SDK calls `createPolicy('gn-plugin', …)` exactly once
 *     (cache works).
 *
 *   - `setHTML(el, html)` routes through the policy's `createHTML`
 *     hook before assigning to `innerHTML`.
 *
 *   - When the global is absent, `setHTML` still works (identity
 *     fallback path).
 *
 *   - When `createPolicy` THROWS (e.g. CSP refuses a duplicate
 *     registration), `setHTML` still works by falling through to
 *     the identity shim.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  __resetPolicyCache,
  getTrustedTypesPolicy,
  setHTML,
} from './trusted-types';

type RulesShape = { createHTML?: (input: string) => string };

let createPolicySpy: ReturnType<typeof vi.fn>;
let createHTMLSpy: ReturnType<typeof vi.fn>;

beforeEach(() => {
  __resetPolicyCache();
  createHTMLSpy = vi.fn();
  createHTMLSpy.mockImplementation((input: unknown) => `TRUSTED:${String(input)}`);
  createPolicySpy = vi.fn();
  createPolicySpy.mockImplementation((name: unknown, rules: unknown) => {
    expect(name).toBe('gn-plugin');
    // The SDK passes its own createHTML hook through. We ignore it
    // in the fake (the test cares about the OUTPUT, which is the
    // string the SDK's hook returns); we exercise OUR createHTMLSpy
    // by returning a different policy that the SDK then uses.
    void (rules as RulesShape);
    return { createHTML: createHTMLSpy };
  });
  // Install the fake on the global.
  (globalThis as { trustedTypes?: unknown }).trustedTypes = {
    createPolicy: createPolicySpy,
  };
});

afterEach(() => {
  delete (globalThis as { trustedTypes?: unknown }).trustedTypes;
  __resetPolicyCache();
});

describe('setHTML with a trustedTypes factory installed', () => {
  it('routes through createHTML before assigning innerHTML', () => {
    const el = document.createElement('div');
    setHTML(el, '<span>hi</span>');
    expect(createPolicySpy).toHaveBeenCalledTimes(1);
    expect(createHTMLSpy).toHaveBeenCalledWith('<span>hi</span>');
    // The fake policy prefixes the input with `TRUSTED:`. The browser
    // then parses the result as HTML, so the trailing markup is real
    // elements and the leading prefix is text. We assert the parsed
    // shape rather than a fragile innerHTML serialization round-trip.
    expect(el.firstChild?.textContent).toBe('TRUSTED:');
    expect(el.querySelector('span')?.textContent).toBe('hi');
  });

  it('memoizes the policy across calls', () => {
    const el = document.createElement('div');
    setHTML(el, '<a>1</a>');
    setHTML(el, '<a>2</a>');
    expect(createPolicySpy).toHaveBeenCalledTimes(1);
    expect(createHTMLSpy).toHaveBeenCalledTimes(2);
  });

  it('exposes the policy via getTrustedTypesPolicy', () => {
    const policy = getTrustedTypesPolicy();
    expect(policy.createHTML('hi')).toBe('TRUSTED:hi');
  });
});

describe('setHTML with no trustedTypes factory', () => {
  beforeEach(() => {
    delete (globalThis as { trustedTypes?: unknown }).trustedTypes;
    __resetPolicyCache();
  });

  it('falls back to identity assignment (works in SSR / dev)', () => {
    const el = document.createElement('div');
    setHTML(el, '<b>x</b>');
    expect(el.innerHTML).toBe('<b>x</b>');
  });
});

describe('setHTML when createPolicy throws', () => {
  it('falls back to the identity shim', () => {
    const failingSpy = vi.fn(() => {
      throw new Error('CSP refused');
    });
    (globalThis as { trustedTypes?: unknown }).trustedTypes = {
      createPolicy: failingSpy,
    };
    __resetPolicyCache();
    const el = document.createElement('div');
    setHTML(el, '<i>safe</i>');
    expect(failingSpy).toHaveBeenCalledTimes(1);
    expect(el.innerHTML).toBe('<i>safe</i>');
  });
});

describe('getTrustedTypesPolicy', () => {
  it('returns a workable policy even when no global is present', () => {
    delete (globalThis as { trustedTypes?: unknown }).trustedTypes;
    __resetPolicyCache();
    const policy = getTrustedTypesPolicy();
    expect(policy.createHTML('hello')).toBe('hello');
  });
});
