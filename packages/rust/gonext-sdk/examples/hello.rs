//! Manifest-builder demo. Runs on the host architecture so the SDK
//! ships a self-contained `cargo run --example hello` smoke test that
//! exercises the public API without needing a wasm32-wasi toolchain.
//!
//! For a full guest-side example (WASM exports + hook handlers), see
//! `examples/plugins/sdk-rust-hello/` at the repository root.

use gonext_sdk::manifest::{Capability, ManifestBuilder};

fn main() {
    let manifest = ManifestBuilder::new("hello", "0.1.0", "hello.wasm")
        .capability(Capability::KvRead)
        .capability(Capability::KvWrite)
        .capability(Capability::AuditEmit)
        .action("save_post")
        .filter("the_content")
        .requires_host(">=0.1.0")
        .build();

    let json = manifest.to_json().expect("serialize manifest");
    println!("{json}");
}
