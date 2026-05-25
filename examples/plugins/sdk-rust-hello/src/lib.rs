//! sdk-rust-hello: minimal Rust plugin demonstrating the GoNext SDK.
//!
//! This is the Rust counterpart to `examples/plugins/seo` (Go/TinyGo).
//! It exercises:
//!
//!   - one action handler (`save_post`) — logs and writes a counter.
//!   - one filter handler (`the_content`) — appends a HTML comment.
//!   - `gn_kv_set` — bumps a per-plugin counter.
//!   - `gn_audit_emit` — records the action in the host audit log.
//!
//! The host runtime locates plugins via the manifest. See
//! `manifest.json` in this directory.
//!
//! ## Build
//!
//! ```sh
//! cargo build --target wasm32-wasip1 --release
//! # Output: target/wasm32-wasip1/release/sdk_rust_hello.wasm
//! ```
//!
//! Or, from the repo root:
//!
//! ```sh
//! make -C examples/plugins/sdk-rust-hello
//! ```

use std::collections::BTreeMap;
use gonext_sdk::prelude::*;
use gonext_sdk::wrappers::AuditEvent;

fn init(ctx: &mut PluginContext) -> Result<(), SdkError> {
    // -----------------------------------------------------------------
    // Action: save_post
    //
    // Side-effects:
    //   1. Log at info level.
    //   2. Bump a per-plugin counter in KV (proves gn_kv_set works
    //      end-to-end).
    //   3. Emit an audit row (proves gn_audit_emit works).
    // -----------------------------------------------------------------
    ctx.register_action("save_post", |args| {
        Host::log(LogLevel::Info, "sdk-rust-hello: save_post fired");

        // Increment a counter; if it doesn't exist yet, store "1".
        let current = match Host::kv_get("save_count")? {
            Some(bytes) => {
                let s = core::str::from_utf8(&bytes).unwrap_or("0");
                s.parse::<i64>().unwrap_or(0)
            }
            None => 0,
        };
        let next = current + 1;
        Host::kv_set("save_count", next.to_string().as_bytes())?;

        // Audit-emit, with the post id (if any) as the target.
        let target = args
            .first()
            .and_then(|v| v.get("id"))
            .and_then(|v| v.as_str());
        let mut meta = BTreeMap::new();
        meta.insert(
            "save_count".into(),
            serde_json::Value::from(next),
        );
        let _ = Host::audit_emit(&AuditEvent {
            action: "sdk-rust-hello.save_post",
            target,
            meta: Some(serde_json::Value::Object(
                meta.into_iter()
                    .map(|(k, v)| (k, v))
                    .collect::<serde_json::Map<_, _>>(),
            )),
        });

        Ok(())
    });

    // -----------------------------------------------------------------
    // Filter: the_content
    //
    // Takes the post HTML (the filter `value`) and appends a one-line
    // HTML comment so the operator can see the plugin actually ran.
    // -----------------------------------------------------------------
    ctx.register_filter("the_content", |value, _args| {
        let html: String = serde_json::from_value(value.clone()).unwrap_or_default();
        let stamped = format!(
            "{}\n<!-- sdk-rust-hello @ {}ms -->",
            html,
            Host::time_ms()
        );
        Ok(serde_json::to_vec(&stamped)?)
    });

    Ok(())
}

gonext_sdk::plugin_init!(init);

