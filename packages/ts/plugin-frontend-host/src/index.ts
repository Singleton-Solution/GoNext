/**
 * @gonext/plugin-frontend-host — public entry point.
 *
 * Re-exports the four hardening primitives that plugin-contributed
 * frontend JS in the admin runs through:
 *
 *   - `trusted-types`: the `gn-plugin` Trusted Types policy.
 *   - `sri`: subresource-integrity helpers (`computeSRI`, `verifySRI`).
 *   - `import-map`: allowlisted import-map generator + manifest
 *     validator.
 *   - `script-tag`: the `<PluginScript>` React component that wires
 *     all of the above into a single tag.
 *
 * Consumers may import the named sub-paths
 * (`@gonext/plugin-frontend-host/sri`, etc.) for tree-shaking; this
 * barrel exists for the common "import everything" call site.
 *
 * Pairs with packages/go/middleware/csp on the host side: the Go
 * middleware emits `require-trusted-types-for 'script'` and a
 * `trusted-types gn-plugin …` directive; this package provides the
 * matching client-side policy.
 */
export * from './trusted-types';
export * from './sri';
export * from './import-map';
export * from './script-tag';
