//! Integration test: build the worked example plugin
//! (`examples/plugins/sdk-rust-hello`) for `wasm32-wasip1`. Verifies
//! that the SDK actually produces a valid guest module — the proc-macro
//! expansion, the host imports, and the export shape all have to be
//! right for cargo to finish.
//!
//! The test is skipped when:
//!
//!  - the wasm32-wasip1 target isn't installed (CI may not have it).
//!  - the cargo home is read-only (sandboxed environments).
//!
//! This means the test is informative locally but isn't a hard gate
//! in CI without the right toolchain — that's by design; the unit
//! tests in src/tests.rs are the always-on gate.

use std::path::PathBuf;
use std::process::Command;

#[test]
fn build_sdk_rust_hello_for_wasm32_wasi() {
    // Skip if wasm32-wasip1 isn't installed. `rustc --print sysroot`
    // is cheap; the target dir under it tells us if std is available.
    let sysroot = match Command::new("rustc").args(["--print", "sysroot"]).output() {
        Ok(out) if out.status.success() => {
            String::from_utf8_lossy(&out.stdout).trim().to_string()
        }
        _ => {
            eprintln!("skipping: rustc unavailable");
            return;
        }
    };
    let target_dir = PathBuf::from(&sysroot)
        .join("lib")
        .join("rustlib")
        .join("wasm32-wasip1");
    if !target_dir.exists() {
        eprintln!("skipping: wasm32-wasip1 target not installed");
        return;
    }

    // The example plugin sits at examples/plugins/sdk-rust-hello from
    // the repo root. CARGO_MANIFEST_DIR points at the SDK crate root
    // (packages/rust/gonext-sdk), so we go three up.
    let manifest = PathBuf::from(env!("CARGO_MANIFEST_DIR"))
        .join("..")
        .join("..")
        .join("..")
        .join("examples")
        .join("plugins")
        .join("sdk-rust-hello")
        .join("Cargo.toml");
    if !manifest.exists() {
        // In some embedded layouts (vendored copy, packaged crate) the
        // example isn't shipped alongside. Skip rather than fail.
        eprintln!("skipping: example crate not found at {}", manifest.display());
        return;
    }

    let status = Command::new("cargo")
        .args([
            "build",
            "--manifest-path",
        ])
        .arg(&manifest)
        .args(["--target", "wasm32-wasip1", "--release"])
        .status();

    let status = match status {
        Ok(s) => s,
        Err(e) => {
            eprintln!("skipping: cargo unavailable: {}", e);
            return;
        }
    };
    assert!(
        status.success(),
        "cargo build for wasm32-wasip1 failed: {}",
        status
    );

    // Verify the output wasm exists and has a non-zero size.
    let wasm = manifest
        .parent()
        .unwrap()
        .join("target")
        .join("wasm32-wasip1")
        .join("release")
        .join("sdk_rust_hello.wasm");
    let md = std::fs::metadata(&wasm)
        .expect("expected wasm output to exist after a successful build");
    assert!(md.len() > 0, "wasm output is empty");
}
