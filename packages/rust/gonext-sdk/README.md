# gonext-sdk

Rust SDK for building [GoNext](https://github.com/Singleton-Solution/GoNext) plugins as `wasm32-wasi` modules.

The host loads plugins via [wazero](https://wazero.io) and exposes a set of
`gn_*` ABIs (logging, time, HTTP fetch, DB read/write, KV, cache, audit,
secrets, cron, metrics, events, OpenTelemetry spans, i18n). This crate hides
the raw ABIs behind safe typed wrappers, generates the WebAssembly exports
the host expects, and ships a builder for the plugin `manifest.json`.

## Install

```toml
[dependencies]
gonext-sdk = "0.1"

[lib]
crate-type = ["cdylib"]
```

Build target:

```sh
cargo build --target wasm32-wasi --release
```

The output WASM is at `target/wasm32-wasi/release/<crate>.wasm`. Ship that
plus a `manifest.json` in the `.gnplugin` bundle.

## Hello, plugin

```rust
use gonext_sdk::prelude::*;

fn init(ctx: &mut PluginContext) -> Result<(), SdkError> {
    ctx.register_action("save_post", |args| {
        Host::log(LogLevel::Info, "save_post fired");
        Host::kv_set("last_save_ts", Host::time_ms().to_string().as_bytes())?;
        Ok(())
    });

    ctx.register_filter("the_content", |value, _args| {
        let html: String = serde_json::from_value(value.clone())?;
        let out = format!("{}\n<!-- enhanced by my-plugin -->", html);
        Ok(serde_json::to_vec(&out)?)
    });

    Ok(())
}

gonext_sdk::plugin_init!(init);
```

The `plugin_init!` macro generates `gn_alloc`, `gn_free`, `gn_handle_hook`,
and `_start` for you. You only write the typed handlers.

## API surface

| Module            | What it covers                                                  |
|-------------------|------------------------------------------------------------------|
| `host`            | Raw `extern "C"` declarations for every `gn_*` host import.      |
| `wrappers`        | Safe wrappers — `Host::http_fetch`, `Host::db_read`, etc.        |
| `hooks`           | `PluginContext`, `register_action`, `register_filter`.           |
| `codec`           | JSON envelope decode/encode mirroring the host's `marshal.go`.   |
| `manifest`        | `Manifest` + `ManifestBuilder` for writing `manifest.json`.      |
| `abi`             | Shared constants (`LogLevel`, `ResultStatus`, packed-i64 helpers).|

## Host ABIs covered

- **env** (built-in): `gn_log`, `gn_panic`, `gn_time_ms`, `gn_i18n_translate`,
  `gn_metric_observe`, `gn_event_emit`, `gn_span_event`
- **env_net** (with `WithNetworkContext`): `gn_http_fetch`, `gn_media_read`,
  `gn_users_read`
- **gonext_data** (with `WithDataHost`): `gn_db_read`, `gn_db_write`,
  `gn_kv_get/set/del/incr`, `gn_cache_invalidate`
- **env_platform** (with `WithPlatformHost`, pending PR #456): `gn_secrets_get`,
  `gn_audit_emit`, `gn_cron_register`

If your plugin imports a host module the runtime wasn't built with, the host
fails at link time with a clear error — capability denials happen there, not
silently in the SDK.

## Manifest

The builder makes a host-compatible `manifest.json`:

```rust
use gonext_sdk::manifest::{Capability, ManifestBuilder};

let m = ManifestBuilder::new("my-plugin", "0.1.0", "plugin.wasm")
    .capability(Capability::KvRead)
    .capability(Capability::KvWrite)
    .capability(Capability::HttpFetch)
    .action("save_post")
    .filter("the_content")
    .requires_host(">=0.1.0")
    .build();

std::fs::write("manifest.json", m.to_json()?)?;
```

## Testing

```sh
# Unit tests run on the host architecture (codec, manifest builder)
cargo test

# Build the example plugin for wasm32-wasi
cargo build --target wasm32-wasi --release --examples
```

## License

Apache-2.0. Part of the GoNext project.
