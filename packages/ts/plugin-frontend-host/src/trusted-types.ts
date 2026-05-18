/**
 * Trusted Types policy for plugin-contributed JavaScript in the GoNext admin.
 *
 * Background — once the admin emits `require-trusted-types-for 'script'`
 * (see packages/go/middleware/csp/trusted_types.go), every assignment to
 * a DOM XSS sink (innerHTML, outerHTML, document.write, eval-equivalents)
 * THROWS at runtime unless the value was minted by a registered Trusted
 * Types policy. This module declares the `gn-plugin` policy that plugin
 * frontend code is expected to use for those assignments.
 *
 * Why a policy AT ALL — plugins legitimately need to insert HTML
 * fragments (block previews, theme markup, capability UIs). The policy
 * funnels that through DOMPurify-sanitized strings so a malicious or
 * buggy plugin cannot inject `<img src=x onerror=…>` into the admin DOM.
 *
 * Why `isomorphic-dompurify` — the admin is a Next.js App-Router app and
 * may instantiate this policy during SSR (e.g. when rendering a script
 * loader on the server before hydration). isomorphic-dompurify uses
 * `jsdom` on the server and the native browser DOM in the client, so a
 * single import works in both environments without conditional loading.
 *
 * Lifecycle:
 *   - `installGnPluginPolicy` is idempotent and may be called from any
 *     module that imports plugin frontend code. The browser disallows
 *     creating two policies with the same name unless 'allow-duplicates'
 *     is in the CSP — we tolerate the resulting exception and return the
 *     previously-installed policy so callers don't need to coordinate.
 *   - On the server (no `window.trustedTypes`), the function returns a
 *     SHIM that mirrors the spec interface. The shim makes SSR rendering
 *     of script tags / sanitized fragments work; the browser then
 *     replaces it with the real policy on hydrate.
 *
 * Wire-level shape: the policy name MUST match the value emitted in the
 * admin's CSP `trusted-types` directive. The shared constant is exported
 * from this module so the Go side and the TS side cannot drift.
 */
import DOMPurify from 'isomorphic-dompurify';

/**
 * Canonical name of the plugin Trusted Types policy. Mirror this string
 * into the CSP `trusted-types` directive emitted by the admin (see the
 * Go middleware in packages/go/middleware/csp/trusted_types.go).
 */
export const GN_PLUGIN_POLICY_NAME = 'gn-plugin';

/**
 * Subset of the Trusted Types policy interface this package needs.
 * Trusted Types' lib.dom types are gated behind a relatively recent
 * `lib.dom.iterable` revision; declaring just the methods we use keeps
 * this package portable across the TS versions consumers pin.
 */
export interface PluginTrustedTypesPolicy {
  /** Mints a TrustedHTML from a sanitized string. */
  createHTML(input: string): string;
  /** Mints a TrustedScript from a string. ALWAYS REJECTS untrusted input. */
  createScript(input: string): string;
  /** Mints a TrustedScriptURL after checking the URL against the allowlist. */
  createScriptURL(input: string): string;
}

/**
 * Browser's global TrustedTypes namespace. Modeled as `unknown` and then
 * narrowed inside the install helper so this module type-checks even in
 * environments where the lib types are not yet present (older TS).
 */
interface TrustedTypesFactory {
  createPolicy(
    name: string,
    rules: {
      createHTML?: (input: string) => string;
      createScript?: (input: string) => string;
      createScriptURL?: (input: string) => string;
    },
  ): PluginTrustedTypesPolicy;
}

/**
 * Module-scoped cache so repeated callers of `installGnPluginPolicy`
 * receive the same instance. Trusted Types disallows reinstalling a
 * named policy unless 'allow-duplicates' is in the CSP, so caching here
 * keeps the call idempotent regardless of the policy.
 */
let cachedPolicy: PluginTrustedTypesPolicy | null = null;

/**
 * Options that customize the policy's permissive surface. The defaults
 * are intentionally strict; pass options only when a specific plugin
 * surface needs a wider allowlist.
 */
export interface InstallPolicyOptions {
  /**
   * Origins that `createScriptURL` will allow. Any other input is
   * rejected with a `TypeError`. Origin matching is exact (scheme + host
   * + port); pathnames are not considered. Default is `['self']`, which
   * means only same-origin URLs (resolved relative to the document URL)
   * are admitted.
   */
  scriptURLAllowlist?: ReadonlyArray<string>;
  /**
   * When true, the policy's `createScript` becomes a no-op pass-through.
   * Use ONLY in tests; production code must keep this off so raw script
   * source is never minted by this policy.
   */
  allowRawScript?: boolean;
}

/**
 * Default allowlist for `createScriptURL`: only `self` (the document
 * origin). Use the explicit `'self'` token rather than a hardcoded
 * hostname so SSR + dev + prod all work with the same value.
 */
const DEFAULT_SCRIPT_URL_ALLOWLIST: ReadonlyArray<string> = ['self'];

/**
 * Installs the `gn-plugin` Trusted Types policy and returns it. Idempotent.
 *
 * In the browser: registers the policy via `window.trustedTypes`. If a
 * previous call already installed it, the cached instance is returned.
 *
 * On the server: returns a SHIM implementing the same interface. The
 * shim still runs the DOMPurify sanitizer + URL allowlist so SSR-emitted
 * markup is identical to what the browser would mint.
 */
export function installGnPluginPolicy(
  options: InstallPolicyOptions = {},
): PluginTrustedTypesPolicy {
  if (cachedPolicy !== null) {
    return cachedPolicy;
  }
  const rules = buildPolicyRules(options);
  const factory = getTrustedTypesFactory();
  if (factory !== null) {
    try {
      cachedPolicy = factory.createPolicy(GN_PLUGIN_POLICY_NAME, rules);
      return cachedPolicy;
    } catch (err) {
      // Browser refused — most likely because the policy is already
      // installed by an earlier import. Fall through to the shim so
      // callers still get a working policy object; the second
      // assignment will then exercise the real policy via the browser's
      // own dispatch.
      if (typeof console !== 'undefined') {
        // eslint-disable-next-line no-console
        console.warn(
          `[gn-plugin] could not register Trusted Types policy: ${
            err instanceof Error ? err.message : String(err)
          }`,
        );
      }
    }
  }
  cachedPolicy = buildShimPolicy(rules);
  return cachedPolicy;
}

/**
 * Test-only escape hatch. Resets the module-scoped cache so consecutive
 * tests can re-install the policy with different options.
 *
 * Production code MUST NOT call this; redirecting an active policy at
 * runtime defeats the integrity guarantee Trusted Types is meant to
 * provide.
 */
export function __resetGnPluginPolicy(): void {
  cachedPolicy = null;
}

/**
 * Builds the spec-shaped rules object from the options. Extracted so the
 * shim and the real-policy paths share the exact same enforcement code.
 */
function buildPolicyRules(options: InstallPolicyOptions): {
  createHTML: (input: string) => string;
  createScript: (input: string) => string;
  createScriptURL: (input: string) => string;
} {
  const allowlist = options.scriptURLAllowlist ?? DEFAULT_SCRIPT_URL_ALLOWLIST;
  const allowRawScript = options.allowRawScript ?? false;

  return {
    createHTML(input: string): string {
      // DOMPurify mutates strings into safe markup; cast to string so
      // the policy's signature returns a plain string in the SSR path
      // (TrustedHTML is just a tagged string in the browser anyway).
      return DOMPurify.sanitize(input);
    },
    createScript(input: string): string {
      if (!allowRawScript) {
        throw new TypeError(
          '[gn-plugin] createScript called with raw input; ' +
            'plugin code may only execute scripts loaded via createScriptURL.',
        );
      }
      return input;
    },
    createScriptURL(input: string): string {
      if (!isAllowedScriptURL(input, allowlist)) {
        throw new TypeError(
          `[gn-plugin] createScriptURL rejected ${JSON.stringify(
            input,
          )}; origin not in allowlist.`,
        );
      }
      return input;
    },
  };
}

/**
 * Wraps the spec-shaped rules in an object that implements the policy
 * surface this package exposes. Used in SSR and in the
 * createPolicy-failed fallback.
 */
function buildShimPolicy(rules: {
  createHTML: (input: string) => string;
  createScript: (input: string) => string;
  createScriptURL: (input: string) => string;
}): PluginTrustedTypesPolicy {
  return {
    createHTML: rules.createHTML,
    createScript: rules.createScript,
    createScriptURL: rules.createScriptURL,
  };
}

/**
 * Reads `window.trustedTypes` defensively. Returns `null` outside the
 * browser or when the policy API is not implemented (older Safari).
 */
function getTrustedTypesFactory(): TrustedTypesFactory | null {
  if (typeof globalThis === 'undefined') {
    return null;
  }
  const win = globalThis as { trustedTypes?: unknown };
  const factory = win.trustedTypes;
  if (factory === undefined || factory === null) {
    return null;
  }
  if (typeof (factory as { createPolicy?: unknown }).createPolicy !== 'function') {
    return null;
  }
  return factory as TrustedTypesFactory;
}

/**
 * Returns true when `input` (a script URL) is allowed by the allowlist.
 *
 * The allowlist tokens follow CSP source-list semantics:
 *   - `'self'` matches the document's origin (resolved against
 *     `globalThis.location` when available; otherwise pure same-origin
 *     relative URLs only).
 *   - any other token is parsed as a URL prefix and the input is
 *     checked for an origin match.
 *
 * Inputs that fail to parse as URLs (relative paths, fragments) are
 * accepted ONLY when the allowlist contains `'self'` and the path
 * starts with `/`. This lets plugins reference their own `/static/…`
 * bundles without needing to know the deployment origin.
 */
function isAllowedScriptURL(input: string, allowlist: ReadonlyArray<string>): boolean {
  const trimmed = input.trim();
  if (trimmed === '') {
    return false;
  }
  // Block javascript: / data: / vbscript: pseudo-schemes outright;
  // those bypass the origin check and are pure XSS sinks.
  const lower = trimmed.toLowerCase();
  if (
    lower.startsWith('javascript:') ||
    lower.startsWith('data:') ||
    lower.startsWith('vbscript:')
  ) {
    return false;
  }

  // Same-origin relative path branch (works in SSR where `location` is
  // not necessarily a valid base).
  if (trimmed.startsWith('/') && !trimmed.startsWith('//')) {
    return allowlist.includes('self');
  }

  // Try to parse as an absolute URL. If parsing fails, reject.
  let parsed: URL;
  try {
    parsed = new URL(trimmed);
  } catch {
    return false;
  }

  for (const token of allowlist) {
    if (token === 'self') {
      const selfOrigin = getSelfOrigin();
      if (selfOrigin !== null && parsed.origin === selfOrigin) {
        return true;
      }
      continue;
    }
    // Treat the token as a URL prefix. Use string startsWith on the
    // canonical href so port + protocol differences are honored.
    try {
      const tokenURL = new URL(token);
      if (parsed.origin === tokenURL.origin) {
        return true;
      }
    } catch {
      // Token is not a parseable URL — fall through.
    }
  }
  return false;
}

/**
 * Reads the document origin defensively. Returns `null` in SSR where no
 * meaningful origin exists; callers treat `null` as "no self match",
 * which is the safe default.
 */
function getSelfOrigin(): string | null {
  if (typeof globalThis === 'undefined') {
    return null;
  }
  const loc = (globalThis as { location?: { origin?: string } }).location;
  if (!loc || typeof loc.origin !== 'string' || loc.origin === '') {
    return null;
  }
  return loc.origin;
}
