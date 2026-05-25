/**
 * Trusted Types policies for the GoNext admin app (issues #59, #90).
 *
 * The admin emits a strict CSP from `apps/admin/middleware.ts`:
 *
 *   require-trusted-types-for 'script';
 *   trusted-types gn-admin gn-editor 'allow-duplicates'
 *
 * Once that header is in force, every assignment to a DOM XSS sink
 * (`innerHTML`, `outerHTML`, `document.write`, `iframe.srcdoc`, …)
 * THROWS at runtime unless the value was minted by a registered
 * Trusted Types policy. This module:
 *
 *   1. Declares the `gn-admin` and `gn-editor` policies. Both run
 *      DOMPurify's strict-but-pragmatic sanitization profile so the
 *      input HTML is scrubbed of script vectors before it reaches the
 *      DOM.
 *   2. Exposes two ergonomic helpers — `setHTML(el, str)` and
 *      `setURL(el, attr, str)` — that every first-party admin
 *      component should use INSTEAD OF `dangerouslySetInnerHTML` or
 *      direct `el.innerHTML =` writes. The helpers internally route
 *      through the appropriate policy.
 *
 * Why two policies — `gn-admin` covers the global admin chrome (search
 * excerpts, comment moderation previews, etc.). `gn-editor` covers
 * the block editor surface, which has wider needs (block icons that
 * are arbitrary inline SVG, embed previews, theme markup). Keeping
 * the policies separate means a CSP audit can tell at a glance which
 * surface a violation came from.
 *
 * Why DOMPurify (not isomorphic-dompurify) — the admin's
 * `dangerouslySetInnerHTML` call sites all run on the client (React
 * server components do not call `innerHTML` sinks). DOMPurify pulls
 * in only the browser implementation, which is ~30% the bundle size
 * of the isomorphic wrapper. The SSR path is a no-op anyway because
 * `window.trustedTypes` doesn't exist on the server.
 *
 * Wire shape:
 *
 *     import { setHTML } from '@/lib/trusted-types';
 *
 *     useEffect(() => {
 *       setHTML(ref.current, props.html);
 *     }, [props.html]);
 *
 * For React render-side use, the helpers can also produce a value
 * suitable for `dangerouslySetInnerHTML` via `sanitizedHTML(str)` —
 * but PREFER the imperative form so the new helper is the only sink
 * touching the DOM directly.
 */
import DOMPurify, { type Config as DOMPurifyConfig } from 'dompurify';

/**
 * Canonical name of the admin Trusted Types policy. Mirror this value
 * into the CSP `trusted-types` directive emitted by the Next.js
 * middleware (apps/admin/middleware.ts) and the Go preset
 * (packages/go/middleware/csp/preset.go::AdminStrictPolicy).
 */
export const GN_ADMIN_POLICY_NAME = 'gn-admin';

/**
 * Canonical name of the block editor Trusted Types policy. Same
 * mirroring rules as GN_ADMIN_POLICY_NAME.
 */
export const GN_EDITOR_POLICY_NAME = 'gn-editor';

/**
 * Subset of the Trusted Types policy interface this module uses.
 * Declared locally so the package type-checks even when the lib.dom
 * Trusted Types declarations aren't yet present (older TS).
 */
export interface AdminTrustedTypesPolicy {
  /** Mints a TrustedHTML from a sanitized string. */
  createHTML(input: string): string;
  /** Mints a TrustedScriptURL after URL allowlist check. */
  createScriptURL(input: string): string;
}

/**
 * Browser's global TrustedTypes namespace. Modeled defensively so the
 * module loads under SSR (where `window.trustedTypes` is undefined).
 */
interface TrustedTypesFactory {
  createPolicy(
    name: string,
    rules: {
      createHTML?: (input: string) => string;
      createScriptURL?: (input: string) => string;
    },
  ): AdminTrustedTypesPolicy;
}

/**
 * Module-scoped policy cache. Trusted Types disallows installing two
 * policies with the same name unless `'allow-duplicates'` is in the
 * CSP — and even then we'd rather not pay the cost twice. Caching
 * keeps the installer idempotent regardless of how many call sites
 * import the module.
 */
let cachedAdmin: AdminTrustedTypesPolicy | null = null;
let cachedEditor: AdminTrustedTypesPolicy | null = null;

/**
 * DOMPurify sanitization profile for first-party admin HTML.
 *
 * The defaults are already strict; we additionally:
 *
 *   - FORBID_TAGS forces `<style>`, `<iframe>`, `<form>` removal so a
 *     sanitized excerpt cannot smuggle policy bypasses through layout
 *     side-effects.
 *   - FORBID_ATTR removes inline event handlers and `style` to defend
 *     against the click-jacking-via-CSS variant.
 *
 * Search excerpts and comment moderation strings are the main intake.
 * The Go server already escapes content before highlighting (only
 * `<mark>` slips through); DOMPurify here is defense-in-depth.
 */
const ADMIN_SANITIZE_CONFIG: DOMPurifyConfig = {
  FORBID_TAGS: ['style', 'iframe', 'form', 'object', 'embed'],
  FORBID_ATTR: ['style', 'onerror', 'onload', 'onclick', 'onmouseover'],
  ALLOW_DATA_ATTR: false,
  USE_PROFILES: { html: true },
};

/**
 * Sanitization profile for the block editor. Wider than the admin
 * profile because the editor legitimately needs to render block icons
 * (inline SVG), block previews (themed markup), and embed cards.
 *
 * The widening is bounded: SVG is allowed (icons), but with
 * USE_PROFILES.svg only — no `<script>` inside SVG, no
 * <foreignObject> with arbitrary HTML.
 */
const EDITOR_SANITIZE_CONFIG: DOMPurifyConfig = {
  FORBID_TAGS: ['iframe', 'form', 'object', 'embed'],
  FORBID_ATTR: ['onerror', 'onload', 'onclick', 'onmouseover'],
  ALLOW_DATA_ATTR: true,
  USE_PROFILES: { html: true, svg: true, svgFilters: true },
};

/**
 * Allowlist for `createScriptURL`. Only `'self'` is accepted by
 * default — script URLs that don't resolve to the document origin
 * are rejected with a TypeError. Tighter than what `gn-plugin`
 * accepts because the admin app itself should never load scripts
 * from third-party origins.
 */
const SCRIPT_URL_ALLOWLIST: ReadonlyArray<string> = ['self'];

/**
 * Reads `window.trustedTypes` defensively. Returns null outside the
 * browser or when the API is not implemented (Firefox stable still
 * gates this behind a flag at the time of writing).
 */
function getTrustedTypesFactory(): TrustedTypesFactory | null {
  if (typeof globalThis === 'undefined') return null;
  const win = globalThis as { trustedTypes?: unknown };
  const factory = win.trustedTypes;
  if (factory === undefined || factory === null) return null;
  if (typeof (factory as { createPolicy?: unknown }).createPolicy !== 'function') {
    return null;
  }
  return factory as TrustedTypesFactory;
}

/**
 * Builds the rules object passed to `trustedTypes.createPolicy`.
 * Extracted so the real-policy and shim paths share the exact same
 * sanitization code.
 */
function buildRules(config: DOMPurifyConfig): {
  createHTML: (input: string) => string;
  createScriptURL: (input: string) => string;
} {
  return {
    createHTML(input: string): string {
      // DOMPurify returns a string in node builds and a TrustedHTML
      // when invoked in a Trusted-Types-aware browser; either way the
      // policy's createHTML accepts the result.
      return DOMPurify.sanitize(input, config) as unknown as string;
    },
    createScriptURL(input: string): string {
      if (!isAllowedScriptURL(input)) {
        throw new TypeError(
          `[gn-admin] createScriptURL rejected ${JSON.stringify(input)}: ` +
            'URL is not in the origin allowlist',
        );
      }
      return input;
    },
  };
}

/**
 * Wraps the rules object in the policy surface the helpers consume.
 * Used in SSR and when `createPolicy` throws (e.g. duplicate
 * registration without 'allow-duplicates').
 */
function buildShim(rules: ReturnType<typeof buildRules>): AdminTrustedTypesPolicy {
  return {
    createHTML: rules.createHTML,
    createScriptURL: rules.createScriptURL,
  };
}

/**
 * Installs and returns the `gn-admin` policy. Idempotent — repeat
 * calls return the cached instance. Safe to call from any module.
 */
export function installAdminPolicy(): AdminTrustedTypesPolicy {
  if (cachedAdmin !== null) return cachedAdmin;
  const rules = buildRules(ADMIN_SANITIZE_CONFIG);
  const factory = getTrustedTypesFactory();
  if (factory !== null) {
    try {
      cachedAdmin = factory.createPolicy(GN_ADMIN_POLICY_NAME, rules);
      return cachedAdmin;
    } catch (err) {
      // Most likely already-installed (without 'allow-duplicates')
      // or the CSP rejected the policy name. Fall through to a shim
      // so the helpers still sanitize.
      if (typeof console !== 'undefined') {
        // eslint-disable-next-line no-console
        console.warn(
          `[gn-admin] could not register Trusted Types policy: ${
            err instanceof Error ? err.message : String(err)
          }`,
        );
      }
    }
  }
  cachedAdmin = buildShim(rules);
  return cachedAdmin;
}

/**
 * Installs and returns the `gn-editor` policy. Idempotent.
 */
export function installEditorPolicy(): AdminTrustedTypesPolicy {
  if (cachedEditor !== null) return cachedEditor;
  const rules = buildRules(EDITOR_SANITIZE_CONFIG);
  const factory = getTrustedTypesFactory();
  if (factory !== null) {
    try {
      cachedEditor = factory.createPolicy(GN_EDITOR_POLICY_NAME, rules);
      return cachedEditor;
    } catch (err) {
      if (typeof console !== 'undefined') {
        // eslint-disable-next-line no-console
        console.warn(
          `[gn-editor] could not register Trusted Types policy: ${
            err instanceof Error ? err.message : String(err)
          }`,
        );
      }
    }
  }
  cachedEditor = buildShim(rules);
  return cachedEditor;
}

/**
 * Test-only hook. Resets the cached policies so subsequent
 * `installAdminPolicy` / `installEditorPolicy` calls can re-run the
 * registration flow with a different mock factory.
 *
 * Production code MUST NOT call this — flipping an active policy at
 * runtime defeats the integrity guarantee Trusted Types provides.
 */
export function __resetPoliciesForTests(): void {
  cachedAdmin = null;
  cachedEditor = null;
}

/**
 * Surface that decides which policy a given HTML string is sanitized
 * through. `admin` for the global chrome (default). `editor` for the
 * block editor (wider allowlist; permits inline SVG).
 */
export type PolicySurface = 'admin' | 'editor';

/**
 * Setter for HTML strings. Imperatively assigns sanitized HTML to the
 * target element's `innerHTML`. Returns silently when `el` is null so
 * call sites can guard on a `useRef` without an explicit null check.
 *
 *     setHTML(ref.current, props.excerptHtml);            // gn-admin
 *     setHTML(iconRef.current, def.icon, 'editor');       // gn-editor
 *
 * Why imperative — `dangerouslySetInnerHTML` (React's API) passes the
 * raw string through React's reconciler, which then assigns it to
 * `innerHTML`. With `require-trusted-types-for 'script'` in force,
 * that React-driven assignment THROWS unless React itself uses a
 * Trusted Types policy. React 19 supports policy registration via
 * the `trustedTypes` factory, but the safer migration path is to
 * route admin HTML through OUR policy and stop using
 * `dangerouslySetInnerHTML` for first-party content entirely.
 */
export function setHTML(
  el: Element | null,
  html: string,
  surface: PolicySurface = 'admin',
): void {
  if (el === null) return;
  const policy = surface === 'editor' ? installEditorPolicy() : installAdminPolicy();
  // The policy's createHTML returns a TrustedHTML in browsers that
  // support it; in SSR / older browsers it returns a plain string.
  // Either is a valid right-hand side for `innerHTML`.
  el.innerHTML = policy.createHTML(html) as unknown as string;
}

/**
 * Setter for URL-valued attributes (`src`, `href`, etc.). Used by the
 * admin chrome when surfacing user-supplied URLs (e.g. user avatar
 * `src`, comment-author `href`).
 *
 *     setURL(linkRef.current, 'href', userProfileURL);
 *
 * Mirrors `setHTML` ergonomics so call sites are easy to grep for.
 * The `createScriptURL` policy ONLY applies to script-loading
 * attributes (`<script src>`, `<link rel="preload" as="script">`);
 * other attributes are not Trusted Types sinks and the helper just
 * does a setAttribute. The unified surface still gives us one place
 * to add URL validation in the future.
 */
export function setURL(
  el: Element | null,
  attr: string,
  url: string,
  surface: PolicySurface = 'admin',
): void {
  if (el === null) return;
  // setAttribute is a Trusted Types sink only for script-URL-bearing
  // attributes (src on <script>, href on <link rel=preload as=script>).
  // For those, mint a TrustedScriptURL via the policy. Otherwise we
  // can call setAttribute directly with the string.
  const isScriptURLSink =
    (el.tagName === 'SCRIPT' && attr.toLowerCase() === 'src') ||
    (el.tagName === 'LINK' && attr.toLowerCase() === 'href');
  if (isScriptURLSink) {
    const policy = surface === 'editor' ? installEditorPolicy() : installAdminPolicy();
    el.setAttribute(attr, policy.createScriptURL(url) as unknown as string);
    return;
  }
  el.setAttribute(attr, url);
}

/**
 * Returns a value suitable for React's `dangerouslySetInnerHTML` prop
 * that has already been routed through the named policy. The escape
 * hatch exists for the small number of legacy components that cannot
 * yet be converted to the imperative `setHTML` form (e.g. SSR-only
 * markup). New code should use `setHTML` directly.
 *
 * Example:
 *
 *     <span dangerouslySetInnerHTML={sanitizedHTML(hit.excerpt_html)} />
 *
 * The ESLint rule that bans `dangerouslySetInnerHTML` allows the
 * prop when its value is the return of `sanitizedHTML(…)` — see
 * apps/admin/.eslintrc.json.
 *
 * @internal Prefer `setHTML` for new code.
 */
export function sanitizedHTML(
  html: string,
  surface: PolicySurface = 'admin',
): { __html: string } {
  const policy = surface === 'editor' ? installEditorPolicy() : installAdminPolicy();
  return { __html: policy.createHTML(html) as unknown as string };
}

/**
 * Internal: applies sanitization eagerly so SSR markup is also safe.
 * Returns the cleaned string suitable for direct interpolation into a
 * server-rendered HTML attribute or text node.
 *
 * Exported because the React `<SafeHTML>` component uses it during
 * SSR (where `useEffect` doesn't fire); first-party callers should
 * prefer `setHTML` (imperative) or `<SafeHTML>` (declarative) which
 * route here internally.
 */
export function sanitizeForRender(
  html: string,
  surface: PolicySurface = 'admin',
): string {
  // Use the install path so the *exact same* DOMPurify config used by
  // the Trusted Types policy is applied here. createHTML on the SSR
  // shim is a thin wrapper around DOMPurify.sanitize.
  const policy = surface === 'editor' ? installEditorPolicy() : installAdminPolicy();
  return policy.createHTML(html) as unknown as string;
}

/**
 * Returns true when `input` resolves to an allowed script URL.
 * Currently the only allowed origin is `'self'` (the document
 * origin); third-party script loading is forbidden.
 *
 * Pseudo-schemes (`javascript:`, `data:`, `vbscript:`) are rejected
 * outright — those bypass origin checks and are pure XSS sinks.
 */
function isAllowedScriptURL(input: string): boolean {
  const trimmed = input.trim();
  if (trimmed === '') return false;

  const lower = trimmed.toLowerCase();
  if (
    lower.startsWith('javascript:') ||
    lower.startsWith('data:') ||
    lower.startsWith('vbscript:')
  ) {
    return false;
  }

  // Same-origin relative path: works in SSR where `location` may not
  // be present. Reject protocol-relative URLs (`//host/…`) because
  // those allow cross-origin loads.
  if (trimmed.startsWith('/') && !trimmed.startsWith('//')) {
    return SCRIPT_URL_ALLOWLIST.includes('self');
  }

  let parsed: URL;
  try {
    parsed = new URL(trimmed);
  } catch {
    return false;
  }

  for (const token of SCRIPT_URL_ALLOWLIST) {
    if (token === 'self') {
      const selfOrigin = getSelfOrigin();
      if (selfOrigin !== null && parsed.origin === selfOrigin) {
        return true;
      }
    }
  }
  return false;
}

/**
 * Reads the document origin defensively. Returns null in SSR or
 * tests where no meaningful origin exists; callers treat null as
 * "no self match", which is the safe default.
 */
function getSelfOrigin(): string | null {
  if (typeof globalThis === 'undefined') return null;
  const loc = (globalThis as { location?: { origin?: string } }).location;
  if (!loc || typeof loc.origin !== 'string' || loc.origin === '') return null;
  return loc.origin;
}
