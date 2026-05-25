# sdk-rust-hello

Worked example of a GoNext plugin written in Rust using [`gonext-sdk`](../../../packages/rust/gonext-sdk).

This is the Rust counterpart to [`examples/plugins/seo`](../seo) (which is Go/TinyGo).

## What it does

| Hook | Type   | Side effect |
|------|--------|-------------|
| `save_post` | action | Logs, bumps a counter in KV (`save_count`), writes an audit row. |
| `the_content` | filter | Appends `<!-- sdk-rust-hello @ ...ms -->` to the post body. |

## Build

```sh
make build
# or, directly:
cargo build --target wasm32-wasip1 --release
```

The output WASM is at `target/wasm32-wasip1/release/sdk_rust_hello.wasm`.
The bundled `manifest.json` is in the directory root.

## Install

Build, then upload the bundle to a running GoNext host:

```sh
make bundle
gonext plugin dev --host http://localhost:8080 .
```

## See also

- [`gonext-sdk`](../../../packages/rust/gonext-sdk) — the crate this plugin uses.
- [`examples/plugins/seo`](../seo) — the Go/TinyGo equivalent.
- [`docs/02-plugin-system.md`](../../../docs/02-plugin-system.md) — plugin reference.
