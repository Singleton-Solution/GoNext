/**
 * Tests for the gn-plugin Trusted Types policy.
 *
 * The policy runs in two environments — real browsers (where
 * `window.trustedTypes` exists) and SSR (where the policy returns a
 * pure-JS shim). Both code paths must be exercised; we simulate the
 * browser path by injecting a fake `trustedTypes` factory onto
 * `globalThis` before each test.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  GN_PLUGIN_POLICY_NAME,
  __resetGnPluginPolicy,
  installGnPluginPolicy,
} from './trusted-types';

/**
 * Shape of the fake trustedTypes factory installed by tests. We keep
 * `createPolicy` typed as a plain function (not `vi.Mock`) so the
 * Vitest type signature doesn't impose its own generic shape onto the
 * fake — call-site assertions reach into `(factory.createPolicy as
 * MockInstance).mock` when they want the spy surface.
 */
interface FakeTrustedTypes {
  createPolicy: (
    name: string,
    rules: Record<string, (input: string) => string>,
  ) => {
    createHTML: (input: string) => string;
    createScript: (input: string) => string;
    createScriptURL: (input: string) => string;
  };
}

/**
 * Installs a fake `trustedTypes` factory on the global so the policy
 * thinks it's running in a browser. The rules object is captured so
 * tests can drive the policy through it directly.
 */
function installFakeTrustedTypes(): {
  factory: FakeTrustedTypes;
  capturedRules: () => Record<string, (input: string) => string>;
  createPolicySpy: () => ReturnType<typeof vi.fn>;
} {
  let captured: Record<string, (input: string) => string> = {};
  const spy = vi.fn(
    (_name: string, rules: Record<string, (input: string) => string>) => {
      captured = rules;
      return {
        createHTML: rules.createHTML!,
        createScript: rules.createScript!,
        createScriptURL: rules.createScriptURL!,
      };
    },
  );
  const factory: FakeTrustedTypes = { createPolicy: spy };
  (globalThis as { trustedTypes?: FakeTrustedTypes }).trustedTypes = factory;
  return {
    factory,
    capturedRules: () => captured,
    createPolicySpy: () => spy as unknown as ReturnType<typeof vi.fn>,
  };
}

describe('installGnPluginPolicy — browser path', () => {
  beforeEach(() => {
    __resetGnPluginPolicy();
    delete (globalThis as { trustedTypes?: unknown }).trustedTypes;
  });
  afterEach(() => {
    delete (globalThis as { trustedTypes?: unknown }).trustedTypes;
  });

  it('registers the policy under the canonical gn-plugin name', () => {
    const { createPolicySpy } = installFakeTrustedTypes();
    installGnPluginPolicy();
    const spy = createPolicySpy();
    expect(spy).toHaveBeenCalledOnce();
    expect(spy.mock.calls[0]![0]).toBe(GN_PLUGIN_POLICY_NAME);
  });

  it('returns the cached policy on a second call (idempotent)', () => {
    const { createPolicySpy } = installFakeTrustedTypes();
    const first = installGnPluginPolicy();
    const second = installGnPluginPolicy();
    expect(first).toBe(second);
    expect(createPolicySpy()).toHaveBeenCalledOnce();
  });

  it('falls back to the SSR shim when createPolicy throws', () => {
    const factory: FakeTrustedTypes = {
      createPolicy: ((): never => {
        throw new Error('boom');
      }) as unknown as FakeTrustedTypes['createPolicy'],
    };
    (globalThis as { trustedTypes?: FakeTrustedTypes }).trustedTypes = factory;
    const consoleWarn = vi.spyOn(console, 'warn').mockImplementation(() => undefined);
    const policy = installGnPluginPolicy();
    expect(policy.createHTML('<b>safe</b>')).toBe('<b>safe</b>');
    expect(consoleWarn).toHaveBeenCalledOnce();
  });

  it('createHTML routes input through DOMPurify (strips event handlers)', () => {
    installFakeTrustedTypes();
    const policy = installGnPluginPolicy();
    const sanitized = policy.createHTML('<img src=x onerror=alert(1)>');
    expect(sanitized).not.toContain('onerror');
  });

  it('createScript REJECTS raw input by default', () => {
    installFakeTrustedTypes();
    const policy = installGnPluginPolicy();
    expect(() => policy.createScript('const a = 1')).toThrowError(
      /createScript called with raw input/,
    );
  });

  it('createScript allows raw input when allowRawScript=true (test escape hatch)', () => {
    installFakeTrustedTypes();
    const policy = installGnPluginPolicy({ allowRawScript: true });
    expect(policy.createScript('const a = 1')).toBe('const a = 1');
  });

  it('createScriptURL rejects javascript: pseudo-scheme', () => {
    installFakeTrustedTypes();
    const policy = installGnPluginPolicy();
    expect(() => policy.createScriptURL('javascript:alert(1)')).toThrowError(
      /not in allowlist/,
    );
  });

  it('createScriptURL rejects data: pseudo-scheme', () => {
    installFakeTrustedTypes();
    const policy = installGnPluginPolicy();
    expect(() => policy.createScriptURL('data:text/javascript,alert(1)')).toThrowError(
      /not in allowlist/,
    );
  });

  it('createScriptURL accepts same-origin absolute paths when allowlist contains self', () => {
    installFakeTrustedTypes();
    const policy = installGnPluginPolicy();
    expect(policy.createScriptURL('/static/plugin.mjs')).toBe('/static/plugin.mjs');
  });

  it('createScriptURL rejects protocol-relative URLs (//foo/bar)', () => {
    installFakeTrustedTypes();
    const policy = installGnPluginPolicy();
    expect(() => policy.createScriptURL('//evil.example.com/x.js')).toThrowError(
      /not in allowlist/,
    );
  });

  it('createScriptURL accepts URLs whose origin matches an allowlist token', () => {
    installFakeTrustedTypes();
    const policy = installGnPluginPolicy({
      scriptURLAllowlist: ['https://cdn.gonext.example.com'],
    });
    expect(
      policy.createScriptURL('https://cdn.gonext.example.com/plugin/script.mjs'),
    ).toBe('https://cdn.gonext.example.com/plugin/script.mjs');
  });

  it('createScriptURL rejects empty input', () => {
    installFakeTrustedTypes();
    const policy = installGnPluginPolicy();
    expect(() => policy.createScriptURL('   ')).toThrowError(/not in allowlist/);
  });

  it('createScriptURL rejects unparseable URLs', () => {
    installFakeTrustedTypes();
    const policy = installGnPluginPolicy({
      scriptURLAllowlist: ['https://cdn.example.com'],
    });
    expect(() => policy.createScriptURL(':::garbage:::')).toThrowError(
      /not in allowlist/,
    );
  });
});

describe('installGnPluginPolicy — SSR shim path', () => {
  beforeEach(() => {
    __resetGnPluginPolicy();
    delete (globalThis as { trustedTypes?: unknown }).trustedTypes;
  });

  it('returns a working shim when trustedTypes is undefined', () => {
    const policy = installGnPluginPolicy();
    expect(policy.createHTML('<b>x</b>')).toBe('<b>x</b>');
  });

  it('shim still sanitizes HTML (DOMPurify runs on the server)', () => {
    const policy = installGnPluginPolicy();
    const sanitized = policy.createHTML('<img src=x onerror=alert(1)>');
    expect(sanitized).not.toContain('onerror');
  });

  it('shim createScript still rejects raw input', () => {
    const policy = installGnPluginPolicy();
    expect(() => policy.createScript('alert(1)')).toThrow();
  });

  it('shim falls back to null self origin (rejects self-relative absolute URLs)', () => {
    // Drop location so getSelfOrigin returns null.
    const original = globalThis.location;
    Object.defineProperty(globalThis, 'location', {
      value: undefined,
      configurable: true,
    });
    try {
      const policy = installGnPluginPolicy();
      expect(() =>
        policy.createScriptURL('https://other.example.com/x.mjs'),
      ).toThrow();
    } finally {
      Object.defineProperty(globalThis, 'location', {
        value: original,
        configurable: true,
      });
    }
  });
});

describe('installGnPluginPolicy — defensive globals', () => {
  beforeEach(() => {
    __resetGnPluginPolicy();
  });

  it('ignores a trustedTypes value that is not a factory', () => {
    (globalThis as { trustedTypes?: unknown }).trustedTypes = { createPolicy: 'not-a-fn' };
    const policy = installGnPluginPolicy();
    // Falls back to shim — createHTML still works.
    expect(policy.createHTML('<b>x</b>')).toBe('<b>x</b>');
    delete (globalThis as { trustedTypes?: unknown }).trustedTypes;
  });

  it('ignores a null trustedTypes value', () => {
    (globalThis as { trustedTypes?: unknown }).trustedTypes = null;
    const policy = installGnPluginPolicy();
    expect(policy.createHTML('<b>x</b>')).toBe('<b>x</b>');
    delete (globalThis as { trustedTypes?: unknown }).trustedTypes;
  });
});
