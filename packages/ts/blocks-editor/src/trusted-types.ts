/**
 * Trusted Types support for `@gonext/blocks-editor` (issue #90).
 *
 * The block editor lives inside the GoNext admin, which emits the strict
 * CSP from `apps/admin/middleware.ts`:
 *
 *   require-trusted-types-for 'script';
 *   trusted-types gn-admin gn-editor 'allow-duplicates'
 *
 * Once that header is in force, any `innerHTML` assignment (including
 * React's `dangerouslySetInnerHTML`) THROWS unless the value was minted
 * by a registered policy. This module registers the `gn-editor` policy
 * and exposes `sanitizeBlockIcon(svg)` so call sites can route their
 * raw HTML through DOMPurify + the policy without each having to
 * re-implement the integration.
 *
 * Mirroring with the admin host — `apps/admin/src/lib/trusted-types.ts`
 * also registers `gn-editor`. The CSP's `'allow-duplicates'` keyword
 * means the second registration is a no-op rather than a throw.
 * Whichever module loads first wins; both apply the same DOMPurify
 * profile so the rendered output is identical regardless.
 */
import DOMPurify from 'isomorphic-dompurify';

/**
 * Canonical name of the block editor Trusted Types policy. Must
 * match the value emitted in the CSP `trusted-types` directive.
 */
export const GN_EDITOR_POLICY_NAME = 'gn-editor';

/**
 * Subset of the Trusted Types policy interface we use. Modeled
 * locally so the package type-checks across TS versions without the
 * Trusted Types lib.dom additions.
 */
export interface EditorTrustedTypesPolicy {
  createHTML(input: string): string;
}

interface TrustedTypesFactory {
  createPolicy(
    name: string,
    rules: { createHTML?: (input: string) => string },
  ): EditorTrustedTypesPolicy;
}

/**
 * Module-scoped policy cache. Trusted Types disallows installing two
 * policies with the same name unless `'allow-duplicates'` is in the
 * CSP, so we register on first use and reuse forever.
 */
let cachedPolicy: EditorTrustedTypesPolicy | null = null;

/**
 * DOMPurify profile for block-editor markup (icons + previews).
 * Wider than the strict admin profile because the editor legitimately
 * needs to render inline SVG (block icons). USE_PROFILES.svg blocks
 * `<script>` inside SVG and `<foreignObject>` with HTML payloads,
 * which is the only practical XSS vector left in inline SVG.
 */
const EDITOR_SANITIZE_CONFIG = {
  FORBID_TAGS: ['iframe', 'form', 'object', 'embed'],
  FORBID_ATTR: ['onerror', 'onload', 'onclick', 'onmouseover'],
  ALLOW_DATA_ATTR: true,
  USE_PROFILES: { html: true, svg: true, svgFilters: true },
} as const;

/**
 * Reads `window.trustedTypes` defensively. Returns null outside the
 * browser or when the API is not implemented.
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
 * Installs and returns the `gn-editor` policy. Idempotent — repeat
 * calls return the cached instance.
 */
export function installEditorPolicy(): EditorTrustedTypesPolicy {
  if (cachedPolicy !== null) return cachedPolicy;

  const rules = {
    createHTML(input: string): string {
      return DOMPurify.sanitize(input, EDITOR_SANITIZE_CONFIG) as unknown as string;
    },
  };
  const factory = getTrustedTypesFactory();
  if (factory !== null) {
    try {
      cachedPolicy = factory.createPolicy(GN_EDITOR_POLICY_NAME, rules);
      return cachedPolicy;
    } catch (err) {
      // Most likely the admin host already registered gn-editor.
      // Fall through to the shim so the helpers still sanitize.
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
  cachedPolicy = { createHTML: rules.createHTML };
  return cachedPolicy;
}

/**
 * Test-only escape hatch. Resets the cached policy so consecutive
 * tests can re-install with a different mock factory.
 */
export function __resetEditorPolicyForTests(): void {
  cachedPolicy = null;
}

/**
 * Sanitizes a block icon (inline SVG) through the gn-editor policy.
 * Returns a value suitable for direct assignment to `innerHTML` or
 * use inside React's `dangerouslySetInnerHTML` prop.
 *
 * Why this signature — the existing call site
 * (`BlockTile` in block-inserter.tsx) passes the sanitized string
 * straight into React. Returning a `{ __html }` object keeps the
 * migration mechanical (just wrap the existing value).
 */
export function sanitizeBlockIcon(svg: string): { __html: string } {
  return { __html: installEditorPolicy().createHTML(svg) };
}
