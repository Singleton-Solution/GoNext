/**
 * `<PluginScript>` — React component that renders an SRI-checked,
 * import-map-sandboxed script tag for plugin-contributed JavaScript.
 *
 * Why a dedicated component:
 *
 *   - Every plugin script tag MUST carry both `integrity=` (so the
 *     browser refuses to execute mismatched bytes) and `crossorigin=`
 *     (so the integrity check actually runs; without crossorigin the
 *     browser skips integrity on opaque responses).
 *   - The tag MUST be `type="module"` so it resolves through the import
 *     map the admin emits earlier in the document.
 *   - Plugin scripts MUST NOT inherit the admin's nonce — the nonce is
 *     for first-party scripts; plugin code should depend on its
 *     integrity hash for trust. Forgetting to omit the nonce would
 *     downgrade SRI enforcement to "either nonce OR integrity matches",
 *     a strictly weaker check.
 *
 * Bundling these requirements into one component lets call sites read
 * naturally — `<PluginScript src=... hash=...>` — and centralizes
 * regressions in a single place that tests can pin.
 *
 * The component is server-component-safe: it renders a single
 * `<script>` element with no client-only hooks. The actual policy
 * installation happens inside the loaded module via
 * `installGnPluginPolicy` from this package.
 */
import type { CSSProperties, ReactElement } from 'react';
import { GN_PLUGIN_POLICY_NAME } from './trusted-types';
import { isValidSRIAttribute } from './sri';

export interface PluginScriptProps {
  /**
   * URL of the plugin script. Should resolve through the host's
   * import-map allowlist (relative paths are fine; absolute URLs are
   * validated against the same allowlist the admin emits).
   */
  src: string;
  /**
   * Spec-shaped SRI value, e.g. `'sha384-AbC123…'`. The browser refuses
   * to execute the script if the fetched bytes hash to a different
   * value. Multiple comma-separated hashes are accepted by the
   * platform but the host emits exactly one for clarity.
   */
  hash: string;
  /**
   * Optional `nonce`. AVOID supplying this on plugin scripts — see the
   * file header. The prop exists only so test fixtures can verify the
   * absence-of-nonce path; production call sites should omit it.
   */
  nonce?: string;
  /**
   * Whether the script should defer execution. Defaults to false; ES
   * modules already defer by default, but explicit `defer` is allowed
   * by the spec for documentation purposes.
   */
  defer?: boolean;
  /**
   * Whether to fire the script asynchronously. Pass true for analytics
   * / telemetry scripts that don't need to block the parser; pass
   * false for plugin UI scripts that need a defined ordering.
   */
  async?: boolean;
  /**
   * Optional element id. Useful when the host needs to reference the
   * script tag later (e.g. for hot-reload during plugin dev).
   */
  id?: string;
  /**
   * Optional `data-*` attributes. The keys are passed through
   * verbatim; React handles the dash-conversion.
   */
  dataAttributes?: Record<string, string>;
  /**
   * Optional style. Reserved for testing — script tags never render,
   * but JSDOM allows the attribute and tests want to inspect it.
   */
  style?: CSSProperties;
}

/**
 * Renders the SRI-checked script tag. Throws synchronously if the
 * `hash` prop is malformed — call sites should validate at plugin
 * registration time so a bad manifest is rejected before the React
 * tree is built.
 */
export function PluginScript(props: PluginScriptProps): ReactElement {
  if (!isValidSRIAttribute(props.hash)) {
    throw new TypeError(
      `[plugin-frontend-host] <PluginScript> received an invalid integrity hash ${JSON.stringify(
        props.hash,
      )}.`,
    );
  }
  if (typeof props.src !== 'string' || props.src === '') {
    throw new TypeError(
      `[plugin-frontend-host] <PluginScript> requires a non-empty src.`,
    );
  }

  const dataAttrs: Record<string, string> = props.dataAttributes ?? {};
  const dataProps: Record<string, string> = {};
  for (const [k, v] of Object.entries(dataAttrs)) {
    // Pass the raw key — React forwards `data-*` verbatim.
    dataProps[k.startsWith('data-') ? k : `data-${k}`] = String(v);
  }

  return (
    <script
      // type="module" is REQUIRED so the script resolves through the
      // import map the admin emits earlier in the document. Without
      // this, bare specifiers in the plugin would throw at runtime.
      type="module"
      src={props.src}
      // Integrity + crossorigin form one logical unit per the SRI spec.
      integrity={props.hash}
      // anonymous request mode keeps cookies out of the bundle fetch;
      // plugin bundles MUST be served with appropriate
      // Access-Control-Allow-Origin or this attribute would block
      // them entirely. Tradeoff is acceptable: the admin owns the
      // serving infrastructure.
      crossOrigin="anonymous"
      // Hint to the browser that the policy mints the script source —
      // the data-* attribute is for debugging only; the actual policy
      // enforcement happens via the CSP header.
      data-trusted-types-policy={GN_PLUGIN_POLICY_NAME}
      // Optional behaviors.
      {...(props.nonce !== undefined ? { nonce: props.nonce } : {})}
      {...(props.defer === true ? { defer: true } : {})}
      {...(props.async === true ? { async: true } : {})}
      {...(props.id !== undefined ? { id: props.id } : {})}
      {...(props.style !== undefined ? { style: props.style } : {})}
      {...dataProps}
    />
  );
}
