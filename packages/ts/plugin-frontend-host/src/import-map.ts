/**
 * Import-map sandbox for plugin-contributed JavaScript.
 *
 * Modern browsers honor a JSON import-map embedded in the document
 * (`<script type="importmap">`) that maps bare ES module specifiers to
 * concrete URLs. When the admin emits an import map that lists ONLY
 * the host-blessed modules, plugin code physically cannot import
 * anything else: a `import "fetch-helper"` in a plugin bundle resolves
 * to whatever URL the host maps it to — or fails with a TypeError if
 * the specifier is unmapped.
 *
 * The sandboxing properties we get from this:
 *
 *   - Plugins cannot reach into private internals (no entry in the map
 *     ⇒ no resolution ⇒ no execution).
 *   - The host controls which version of each module plugins observe;
 *     security fixes to e.g. `@gonext/sdk` propagate without the plugin
 *     needing a rebuild.
 *   - Combined with the SRI-checked script tag and the gn-plugin
 *     Trusted Types policy, a plugin runs in a narrow environment
 *     where every external dependency is identified, hashed, and
 *     audited at registration time.
 *
 * This module owns two responsibilities:
 *
 *   1. Build the JSON import-map document the admin renders. Callers
 *      pass a strongly-typed `PluginImportMapAllowlist`; we serialize
 *      it into the spec shape and pin the directive order so tests can
 *      diff exact bytes.
 *
 *   2. Validate plugin manifests. Given a plugin's declared `imports`
 *      list, verify each specifier is in the allowlist BEFORE the host
 *      emits the script tag. Validation failures translate into
 *      manifest rejection in the host (issue #261).
 *
 * Spec reference: https://html.spec.whatwg.org/multipage/webappapis.html#import-maps
 */

/**
 * A single mapping in the import map: `<bare specifier>` →
 * `<absolute URL>`. The host's allowlist is a frozen set of these.
 */
export interface ImportMapEntry {
  /** Bare module specifier (e.g. `'@gonext/sdk'`). */
  specifier: string;
  /** Absolute URL the specifier resolves to. */
  url: string;
}

/**
 * Allowlist passed into `generateImportMap`. Order is significant for
 * the serialized output; pass entries in the order the host wants them
 * inspected (stable serialization makes the SRI of the import-map
 * script itself reproducible).
 */
export interface PluginImportMapAllowlist {
  /** Top-level imports — visible to all plugin code. */
  imports: ReadonlyArray<ImportMapEntry>;
  /**
   * Scoped imports: `scope URL` → `imports`. The browser uses the
   * scope as a prefix match on the URL of the module that issued the
   * `import` call. Use sparingly — most admin allowlists only need
   * `imports`.
   */
  scopes?: ReadonlyMap<string, ReadonlyArray<ImportMapEntry>>;
}

/**
 * Serialized output of `generateImportMap`. The `json` field is the
 * exact string the admin should embed in `<script type="importmap">`;
 * the `parsed` field is the same data as a plain JS object so callers
 * that need to do further inspection don't have to re-parse.
 */
export interface ImportMapDocument {
  json: string;
  parsed: ImportMapJSON;
}

/**
 * Browser-shape import map. Exported so tests can pin the wire format.
 */
export interface ImportMapJSON {
  imports: Record<string, string>;
  scopes?: Record<string, Record<string, string>>;
}

/**
 * Serializes an allowlist into the spec-shaped JSON document. Specifiers
 * are emitted in caller-supplied order; scope URLs are sorted
 * alphabetically so two equivalent allowlists produce byte-identical
 * output regardless of Map iteration order.
 *
 * Throws when the allowlist contains a duplicate specifier — silently
 * dropping duplicates would create surprising precedence rules.
 */
export function generateImportMap(allowlist: PluginImportMapAllowlist): ImportMapDocument {
  const importsObj = entriesToRecord(allowlist.imports, 'imports');
  const result: ImportMapJSON = { imports: importsObj };

  if (allowlist.scopes !== undefined && allowlist.scopes.size > 0) {
    const sortedScopes = Array.from(allowlist.scopes.keys()).sort();
    const scopesObj: Record<string, Record<string, string>> = {};
    for (const scope of sortedScopes) {
      const entries = allowlist.scopes.get(scope) ?? [];
      scopesObj[scope] = entriesToRecord(entries, `scopes[${JSON.stringify(scope)}]`);
    }
    result.scopes = scopesObj;
  }

  // Stable, two-space-indented JSON so the script tag's SRI is
  // reproducible across builds. Encoding includes a trailing newline so
  // the SHA matches what most editors save (and what the SRI verifier
  // computes over the same bytes if it reads the file from disk).
  const json = JSON.stringify(result, null, 2) + '\n';
  return { json, parsed: result };
}

/**
 * Validates a plugin manifest's declared `imports` list against the
 * allowlist. Returns the validation report; callers reject the plugin
 * when `report.ok === false`.
 *
 * The validator is intentionally pure — it doesn't read the network,
 * doesn't mutate the manifest, and never throws. Returning a structured
 * report lets the install path render a user-friendly error in the
 * admin's plugin-review screen instead of a stack trace.
 */
export function validatePluginManifest(
  allowlist: PluginImportMapAllowlist,
  manifest: { imports?: ReadonlyArray<string> | undefined },
): ManifestValidationReport {
  const declared = manifest.imports ?? [];
  const allowed = new Set(allowlist.imports.map((e) => e.specifier));
  const offending: string[] = [];

  for (const specifier of declared) {
    if (typeof specifier !== 'string' || specifier === '') {
      offending.push(String(specifier));
      continue;
    }
    if (!allowed.has(specifier)) {
      // Probe scoped imports too: if any scope grants the specifier,
      // we accept it. (Scopes' more specific match semantics are
      // enforced at runtime by the browser.)
      if (!isAllowedByScope(allowlist, specifier)) {
        offending.push(specifier);
      }
    }
  }

  if (offending.length === 0) {
    return { ok: true, offending: [] };
  }
  return {
    ok: false,
    offending,
    reason: `plugin manifest declared imports not in the admin allowlist: ${offending
      .map((s) => JSON.stringify(s))
      .join(', ')}`,
  };
}

/**
 * Result of a manifest validation. `ok=true` ⇒ safe to install;
 * `ok=false` ⇒ the host must reject the manifest and surface
 * `reason` to the operator.
 */
export type ManifestValidationReport =
  | { ok: true; offending: ReadonlyArray<string> }
  | { ok: false; offending: ReadonlyArray<string>; reason: string };

/**
 * Default host allowlist used by the admin. Other call sites may pass a
 * customized allowlist (e.g. for a sandbox preview iframe with a wider
 * surface), but this constant is the production shape.
 *
 * Entries:
 *   - `@gonext/sdk` — the plugin SDK (capabilities, hooks, fetch helpers)
 *   - `react`, `react-dom/client` — UI primitives plugins compose with
 *   - `@gonext/blocks-sdk` — block-tree types and helpers
 *   - `@gonext/plugin-frontend-host/trusted-types` — the policy installer
 *
 * The URLs are RELATIVE to the admin origin (`/_/runtime/...`) so the
 * host can serve them through its own static handler with a Cache-Control
 * appropriate to each module.
 */
export function defaultAdminAllowlist(): PluginImportMapAllowlist {
  return {
    imports: [
      { specifier: '@gonext/sdk', url: '/_/runtime/sdk.mjs' },
      { specifier: 'react', url: '/_/runtime/react.mjs' },
      { specifier: 'react-dom/client', url: '/_/runtime/react-dom-client.mjs' },
      { specifier: '@gonext/blocks-sdk', url: '/_/runtime/blocks-sdk.mjs' },
      {
        specifier: '@gonext/plugin-frontend-host/trusted-types',
        url: '/_/runtime/plugin-trusted-types.mjs',
      },
    ],
  };
}

/**
 * Converts a list of `ImportMapEntry` values to the `Record<spec, url>`
 * shape required by the spec. Throws on duplicate specifiers and on
 * empty / non-string URLs.
 */
function entriesToRecord(
  entries: ReadonlyArray<ImportMapEntry>,
  contextLabel: string,
): Record<string, string> {
  const out: Record<string, string> = {};
  for (const entry of entries) {
    if (typeof entry.specifier !== 'string' || entry.specifier === '') {
      throw new TypeError(
        `[plugin-frontend-host] import-map entry in ${contextLabel} missing specifier`,
      );
    }
    if (typeof entry.url !== 'string' || entry.url === '') {
      throw new TypeError(
        `[plugin-frontend-host] import-map entry ${JSON.stringify(
          entry.specifier,
        )} in ${contextLabel} missing url`,
      );
    }
    if (Object.prototype.hasOwnProperty.call(out, entry.specifier)) {
      throw new TypeError(
        `[plugin-frontend-host] duplicate import-map specifier ${JSON.stringify(
          entry.specifier,
        )} in ${contextLabel}`,
      );
    }
    out[entry.specifier] = entry.url;
  }
  return out;
}

/**
 * Returns true when a specifier is granted by any scope in the
 * allowlist. The browser would resolve scoped imports based on the
 * caller's URL; for manifest validation we accept ANY scope match
 * because the host fully controls which scopes it emits.
 */
function isAllowedByScope(
  allowlist: PluginImportMapAllowlist,
  specifier: string,
): boolean {
  if (allowlist.scopes === undefined) {
    return false;
  }
  for (const entries of allowlist.scopes.values()) {
    for (const entry of entries) {
      if (entry.specifier === specifier) {
        return true;
      }
    }
  }
  return false;
}
