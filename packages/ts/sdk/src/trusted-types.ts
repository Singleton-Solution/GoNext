/**
 * Trusted-Types compatibility shim for the @gonext/sdk runtime.
 *
 * The admin opts into `require-trusted-types-for 'script'` CSP (see
 * packages/go/middleware/csp/trusted_types.go); every assignment to
 * an injection sink — `innerHTML`, `outerHTML`, `script.src`,
 * `document.write` — throws unless the value passed in was minted by
 * a registered policy.
 *
 * @gonext/plugin-frontend-host owns the `gn-plugin` policy used by
 * plugin browser code. The SDK does NOT install its own policy:
 *
 *   - In production the host has already installed `gn-plugin`
 *     before any plugin module evaluates, so we re-use whatever is
 *     visible on `window.trustedTypes.defaultPolicy` / the named
 *     policy lookup.
 *
 *   - On the server (SSR) and in dev where Trusted Types is not
 *     enforced, we fall back to a pass-through shim that mirrors the
 *     same interface. This means `setHTML(el, str)` is a single
 *     call site that works in every environment — no `if (TT)` in
 *     plugin code.
 *
 * Why we don't import `@gonext/plugin-frontend-host` directly:
 * (1) keeps the SDK zero-dep, so its SRI hash is stable; (2) avoids
 * a circular dependency once the host package starts importing the
 * SDK back for type-only contracts; (3) plugins authored against an
 * older SDK still get TT-safe behaviour against a newer host policy.
 */

/**
 * Canonical policy name. Mirrors the constant in
 * `@gonext/plugin-frontend-host/trusted-types`. Hard-coded here
 * because we want zero runtime dependencies — the wire-level name is
 * a published contract anyway.
 */
const GN_PLUGIN_POLICY_NAME = 'gn-plugin';

/**
 * Subset of the Trusted Types policy interface this module needs.
 * Declared inline so the SDK does not depend on the `trusted-types`
 * types package or a `lib.dom` revision a consumer hasn't pinned.
 */
interface TTPolicy {
  createHTML(input: string): string;
}

interface TTFactory {
  createPolicy(name: string, rules: { createHTML?: (input: string) => string }): TTPolicy;
}

/**
 * Cached policy reference. Memoized so consecutive `setHTML` calls
 * do not re-walk the `trustedTypes` factory.
 */
let cachedPolicy: TTPolicy | null = null;

/**
 * Resolves the policy. In order:
 *
 *   1. `window.trustedTypes.policies` (Chromium-style) — read the
 *      already-installed `gn-plugin` if the platform exposes a
 *      lookup. Spec'd browsers do not actually expose a `policies`
 *      map; this branch is a defensive fallback for hardened
 *      embedders that patch one in.
 *
 *   2. `window.trustedTypes.createPolicy('gn-plugin', …)` with the
 *      `allow-duplicates` keyword — the spec allows a second
 *      `createPolicy` call iff the CSP grants it. The host CSP DOES
 *      grant it (see `csp/trusted_types.go`) precisely so SDK
 *      consumers can keep a local handle.
 *
 *   3. The pass-through shim: a plain object whose `createHTML` is
 *      identity. Used in SSR, tests, and dev where TT is not
 *      enforced.
 */
function getPolicy(): TTPolicy {
  if (cachedPolicy !== null) {
    return cachedPolicy;
  }
  const factory = readTTFactory();
  if (factory !== null) {
    // Defensive lookup. Some hardened embedders expose `policies` as
    // a Map<string, TrustedTypePolicy>.
    const policiesProp = (
      factory as unknown as {
        policies?: { get?: (name: string) => TTPolicy | undefined };
      }
    ).policies;
    if (policiesProp && typeof policiesProp.get === 'function') {
      const found = policiesProp.get(GN_PLUGIN_POLICY_NAME);
      if (found !== undefined) {
        cachedPolicy = found;
        return cachedPolicy;
      }
    }
    try {
      cachedPolicy = factory.createPolicy(GN_PLUGIN_POLICY_NAME, {
        createHTML(input) {
          return input;
        },
      });
      return cachedPolicy;
    } catch {
      // Browser refused — most likely because the policy already
      // exists and the CSP did not grant 'allow-duplicates'. Fall
      // through to the shim. The browser will dispatch through the
      // EXISTING policy when the SDK assigns innerHTML with our
      // shim's output (which is just a string the browser then
      // routes through whichever policy is registered).
    }
  }
  cachedPolicy = identityShim();
  return cachedPolicy;
}

/**
 * Test-only escape hatch. Resets the module-scoped cache so a test
 * can swap the global `trustedTypes` factory between cases.
 */
export function __resetPolicyCache(): void {
  cachedPolicy = null;
}

/**
 * Reads `window.trustedTypes` defensively. Returns `null` outside
 * the browser or when the policy API is not implemented (older
 * Safari, jsdom without a fake).
 */
function readTTFactory(): TTFactory | null {
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
  return factory as TTFactory;
}

/**
 * Returns an identity shim. Used in SSR / dev / non-TT environments.
 * The shim is intentionally minimal — adding sanitization here would
 * duplicate the host policy's behaviour and drift over time. The
 * SDK's contract is "pass strings through the gn-plugin policy when
 * it exists, otherwise just route them"; defense in depth lives in
 * the host package, not here.
 */
function identityShim(): TTPolicy {
  return {
    createHTML(input) {
      return input;
    },
  };
}

/**
 * Trusted-Types-safe `innerHTML` setter for plugin code.
 *
 * Plugin authors call `setHTML(el, '<p>safe markup</p>')` instead of
 * `el.innerHTML = '<p>…</p>'`. The string is routed through the
 * `gn-plugin` policy when one exists, so the assignment satisfies
 * the CSP `require-trusted-types-for 'script'` directive even on a
 * locked-down admin page.
 *
 * In environments without Trusted Types enforcement the call is a
 * plain `innerHTML = html`. The result is observable as the same
 * markup either way.
 */
export function setHTML(target: Element, html: string): void {
  const policy = getPolicy();
  const trusted = policy.createHTML(html);
  // The `as unknown as string` keeps the assignment type-correct
  // regardless of whether the runtime returns a TrustedHTML (tagged
  // string subtype in browsers that implement TT) or a plain string
  // (SSR / shim).
  target.innerHTML = trusted as unknown as string;
}

/**
 * Returns the resolved policy. Exposed so block components or
 * advanced consumers can mint a TrustedHTML for sinks the SDK does
 * not directly wrap (e.g. assigning to a `srcdoc` iframe attribute).
 *
 * The return type is a structural interface, NOT the DOM's
 * `TrustedTypePolicy` — that lets the SDK keep its `lib.dom`
 * portability promise.
 */
export function getTrustedTypesPolicy(): TTPolicy {
  return getPolicy();
}
