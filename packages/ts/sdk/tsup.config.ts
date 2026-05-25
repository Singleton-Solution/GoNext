/**
 * tsup build configuration for @gonext/sdk.
 *
 * Outputs three artifacts under ./dist:
 *
 *   - index.mjs  — ESM. This is the canonical bundle the admin's
 *                  import map points at (`/_/runtime/sdk.mjs`). It is
 *                  what plugin browser code actually resolves to when
 *                  it writes `import { host } from '@gonext/sdk'`.
 *
 *   - index.cjs  — CJS. Test harnesses, Node-based codemods, and the
 *                  occasional plugin SSR step want a `require()`-able
 *                  form. Same source, different module emit.
 *
 *   - index.d.ts — Single rolled-up type declaration. Plugin authors
 *                  get full IntelliSense without us shipping every
 *                  internal `.d.ts` to npm.
 *
 * `clean: true` deletes ./dist on every build so stale outputs from a
 * removed entry point never leak through. `sourcemap: true` keeps the
 * SDK debuggable in the user's browser — the package is small enough
 * that the extra bytes are worth the developer experience.
 *
 * `treeshake: true` + `splitting: false` keeps the ESM output a single
 * file. The import map references one URL; splitting would force the
 * host to learn about chunk filenames.
 *
 * No external dependencies — the SDK is intentionally zero-dep so it
 * can be served with a fixed SRI hash that does not drift when an
 * upstream patch lands. If a future feature pulls in a runtime dep,
 * mark it `external` here and add it to the host's import-map
 * allowlist (packages/ts/plugin-frontend-host/src/import-map.ts).
 */
import { defineConfig } from 'tsup';

export default defineConfig({
  entry: { index: 'src/index.ts' },
  format: ['esm', 'cjs'],
  outExtension({ format }) {
    return { js: format === 'esm' ? '.mjs' : '.cjs' };
  },
  dts: true,
  clean: true,
  sourcemap: true,
  treeshake: true,
  splitting: false,
  target: 'es2022',
  platform: 'neutral',
  // Empty external array is on purpose — the SDK has zero runtime deps
  // and we want every helper folded into the single emitted bundle so
  // the import-map URL resolves to one self-contained module.
  external: [],
});
