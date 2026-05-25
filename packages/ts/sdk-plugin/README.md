# @gonext/sdk-plugin

TypeScript plugin SDK for [GoNext](https://github.com/Singleton-Solution/GoNext).
Plugin authors write TypeScript against typed wrappers over the host's
`gn_*` ABIs; `gonext-sdk-build` compiles the source via `tsc` + Javy
(Shopify's JS→WASM compiler) into a `plugin.wasm` ready to bundle.

This SDK exists alongside the Go and Rust SDKs for the same plugin
runtime: WordPress-style action / filter hooks dispatched into a
sandboxed wasm guest. Choose TypeScript when you want the npm
ecosystem, JSON-native ergonomics, and a familiar editor experience;
choose Go or Rust when you care most about cold-start time and binary
size.

## Quick start

```bash
pnpm add -D @gonext/sdk-plugin typescript
```

```ts
// src/index.ts
import {
  pluginInit,
  registerAction,
  registerFilter,
  host,
} from '@gonext/sdk-plugin';

registerAction('save_post', async (args) => {
  host.log.info('post saved: ' + JSON.stringify(args));
  host.kv.set('last-save', String(host.nowMs()));
  host.audit.emit('plugin.save_post.observed', { args });
});

registerFilter('the_content', async (value) => {
  return `<div data-plugin="hello">${value}</div>`;
});

pluginInit();
```

```json
// manifest.json
{
  "name": "gn-hello",
  "version": "0.1.0",
  "entry": "plugin.wasm",
  "capabilities": ["kv.write", "audit.emit"],
  "hooks": {
    "actions": ["save_post"],
    "filters": ["the_content"]
  },
  "requires": { "host": ">=0.1.0" }
}
```

```bash
npx gonext-sdk-build
# -> dist/plugin.wasm + dist/manifest.json
```

## What's in the package

- `index` — `pluginInit`, `registerAction`, `registerFilter`, and the
  dispatcher Javy wires onto `gn_handle_hook`.
- `host` — typed wrappers for every host ABI:
  - **Data**: `host.db.read/write`, `host.kv.get/set/del/incr`,
    `host.cache.invalidate`
  - **Network**: `host.http.fetch`, `host.media.read`, `host.users.read`
  - **Platform**: `host.secrets.get`, `host.audit.emit`, `host.cron.register`
  - **Observability**: `host.log.{debug,info,warn,error}`, `host.nowMs`,
    `host.i18n.translate`, `host.observe.{metric,event,spanEvent}`
- `manifest` — `buildManifest`, `manifestToJSON`, types matching
  `gonext.io/v1`.
- `codec` — JSON envelope helpers (mostly internal; useful if you
  bypass the dispatcher).

## Build pipeline

`gonext-sdk-build` runs:

1. Manifest validation (`buildManifest`) → `dist/manifest.json`.
2. esbuild bundles `src/index.ts` (plus its `@gonext/sdk-plugin`
   imports from `node_modules`) into a single ES2020 file at
   `dist/plugin.js`.
3. Javy compiles `dist/plugin.js` → `dist/plugin.wasm`.

Javy must be on `$PATH` or passed via `--javy <path>`. Install it from
the [Javy releases page](https://github.com/bytecodealliance/javy/releases).

The CLI skips the Javy step when `--skip-wasm` is set, which is useful
in CI lanes that only need the manifest + bundle gate (esbuild surfaces
syntax errors; `pnpm typecheck` is the type-level gate).

## Signing

Signing is decoupled from `gonext-sdk-build`. After producing the
bundle, run:

```bash
gonext plugin sign dist/
```

See `gonext plugin sign --help` and the
[plugin-system docs](../../docs/02-plugin-system.md) for details.

## Why a separate package from `@gonext/sdk`

`@gonext/sdk` (sibling package, frontend SDK) is loaded inside the host's
admin / runtime browser bundle — it ships React helpers and hook
schemas for the frontend host. `@gonext/sdk-plugin` is the *guest*-side
counterpart, intended only for the JavaScript that runs inside a
plugin's wasm sandbox. Keeping the two separate means a plugin
author's bundle doesn't pull in React, and a frontend dev doesn't
ship Javy runtime stubs.

## Testing

```bash
pnpm --filter @gonext/sdk-plugin test
```

The test suite exercises the codec and manifest builder under Node;
the Javy compile step is skipped (no Javy is installed in CI). The
dispatcher tests prove the registration API + JSON wire format
without standing up a wasm runtime.
