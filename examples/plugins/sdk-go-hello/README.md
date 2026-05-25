# sdk-go-hello

Worked example for the Go plugin SDK at `packages/go/sdk`. Compiles
to a WASM module via TinyGo and exercises:

- An action handler for `posts.publish`
- A filter handler for `the_content`
- One `gn_kv_set` call
- One `gn_audit_emit` call

## Build

```bash
make            # produces plugin.wasm
make bundle     # produces sdk-go-hello.gnplugin
```

Requires `tinygo >= 0.31.0` on PATH.

## Install

Once the host is running:

```bash
gonext plugin install ./sdk-go-hello.gnplugin
gonext plugin activate gonext-sdk-go-hello
```

## Test

```bash
make test   # stock-Go tests against handler logic
```

The SDK stubs out the host-call layer under the stock toolchain so
the test target validates handler semantics without TinyGo. To
verify the full WASM build, `make check` runs both.
