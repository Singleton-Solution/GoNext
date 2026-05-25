# gn-sdk-ts-hello

Worked example of a GoNext plugin written in TypeScript via the
[`@gonext/sdk-plugin`](../../../packages/ts/sdk-plugin) SDK and compiled
to WebAssembly through [Javy](https://github.com/bytecodealliance/javy).

## What it does

- Subscribes to one action (`save_post`): records a KV timestamp and
  emits an audit row.
- Subscribes to one filter (`the_content`): wraps the value in a marker
  `<div>` so the plugin is visible in rendered output.

Read [`src/index.ts`](src/index.ts) — the whole plugin is ~30 lines
including comments.

## Build

```bash
pnpm install
pnpm build
```

The pipeline runs `tsc` then `javy compile` and writes:

```
dist/
  plugin.wasm       # WASM module loaded by the host
  manifest.json     # validated against gonext.io/v1
```

Javy must be on `$PATH` (or pass `--javy <path>` to
`gonext-sdk-build`). Install it from the
[Javy releases page](https://github.com/bytecodealliance/javy/releases).

## Sign and install

```bash
gonext plugin sign dist/
gonext plugin test dist/   # contract checks
```

See [`docs/02-plugin-system.md`](../../../docs/02-plugin-system.md) for
the full install + activation flow.
