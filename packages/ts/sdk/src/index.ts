/**
 * @gonext/sdk — browser-side SDK plugin authors import.
 *
 * This is the canonical bundle the host's import map (see
 * packages/ts/plugin-frontend-host/src/import-map.ts) resolves
 * `@gonext/sdk` to. A plugin's browser code writes:
 *
 *   import { host, defineBlock, getSlug, i18n, setHTML } from '@gonext/sdk';
 *
 * and the import map points that specifier at
 * `/_/runtime/sdk.mjs` (the ESM emit from this package's tsup
 * build). The host serves the file with an SRI hash pinned in
 * the import map, so a tampered SDK bundle is rejected by the
 * browser before any plugin code runs.
 *
 * Public surface:
 *
 *   - `getSlug()` / `setSlug()` / `SlugRequiredError` — plugin
 *     identity, auto-detected from the import-map URL.
 *
 *   - `host.{posts, users, media, cache}` — REST shims over the
 *     existing WP-compat + plugin-scoped surfaces.
 *
 *   - `HostFetchError` — typed error for the REST shims.
 *
 *   - `defineBlock(spec)` — register a client-side block;
 *     forwards to the editor's `BLOCK_REGISTRY` when present.
 *
 *   - `i18n.t(key, args)` / `i18n.load(locale)` — translation
 *     lookup against the host's per-plugin catalogue endpoint.
 *
 *   - `setHTML(el, html)` / `getTrustedTypesPolicy()` — Trusted
 *     Types-safe DOM injection that re-uses the host's
 *     `gn-plugin` policy.
 *
 * Stability: post-1.0 we promise backwards compatibility on every
 * named export. Pre-1.0 (current) we may rename internals; the
 * public surface listed above is intentionally narrow so most
 * plugin code survives a 0.x → 1.0 bump untouched.
 */

export { getSlug, setSlug, SlugRequiredError } from './slug';
export { host, HostFetchError } from './host';
export type { Post, User, Media, ListOptions, HostCallOptions } from './host';
export { defineBlock } from './blocks';
export type { BlockSpec, BlockProps } from './blocks';
export type { ComponentType } from './react-types';
export { i18n } from './i18n';
export type { Catalogue } from './i18n';
export { setHTML, getTrustedTypesPolicy } from './trusted-types';
