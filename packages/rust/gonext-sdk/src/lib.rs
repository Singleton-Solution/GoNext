//! # gonext-sdk
//!
//! Rust SDK for building [GoNext](https://github.com/Singleton-Solution/GoNext)
//! plugins targeting `wasm32-wasi`. The crate hides the raw host ABI
//! ([`host`]) behind safe typed wrappers ([`Host`]), provides a JSON
//! envelope codec ([`codec`]), a [`Manifest`] builder, and a
//! [`plugin_init!`] macro that wires the WASM exports the host
//! runtime expects.
//!
//! ## Quickstart
//!
//! ```ignore
//! use gonext_sdk::prelude::*;
//!
//! fn init(ctx: &mut PluginContext) -> Result<(), SdkError> {
//!     ctx.register_action("save_post", |args| {
//!         Host::log(LogLevel::Info, "save_post fired");
//!         Ok(())
//!     });
//!     ctx.register_filter("the_content", |value, _args| {
//!         let html: String = serde_json::from_slice(&value)?;
//!         let out = format!("{}\n<!-- enhanced by my-plugin -->", html);
//!         Ok(serde_json::to_vec(&out)?)
//!     });
//!     Ok(())
//! }
//!
//! gonext_sdk::plugin_init!(init);
//! ```
//!
//! ## ABI surface
//!
//! Every plugin built against `gonext.io/v1` must export these symbols.
//! The [`plugin_init!`] macro generates them; if you'd rather wire them
//! by hand, see [`abi`] for the underlying raw entry points.
//!
//! ```text
//! (func (export "gn_alloc")        (param i32) (result i32))
//! (func (export "gn_free")         (param i32) (param i32))
//! (func (export "gn_handle_hook")  (param i32 i32 i32 i32) (result i64))
//! ```
//!
//! The packed-i64 return convention is: high 32 bits = pointer into our
//! linear memory; low 32 bits = length, interpreted as `i32` so negative
//! sentinels can encode typed failures. See [`abi::ResultStatus`].
//!
//! ## std on wasi
//!
//! The crate links against `std` because every `wasm32-wasi` toolchain
//! ships a wasi-libc that supplies it. We avoid the *parts* of `std`
//! that don't work in a wazero sandbox — `std::net`, `std::process`,
//! `std::thread` — but `std::collections`, `std::format`, `std::string`,
//! and the like are all fine. The host provides everything that
//! crosses the sandbox boundary via the `gn_*` ABIs (see [`host`] and
//! [`wrappers`]).

// The crate is intentionally `std`-linked, not `no_std`, because every
// wasm32-wasi toolchain ships a wasi-libc that provides std for free.
// Going `no_std` would mean asking plugin authors to bring their own
// `#[global_allocator]`, which adds friction with no upside on WASI —
// the std heap is wasi-libc's heap.
//
// The crate avoids the *parts* of std that don't work in a wazero
// sandbox: no `std::net`, no `std::process`, no `std::thread`. The
// host provides everything that crosses the sandbox boundary via the
// `gn_*` ABIs (see `host` and `wrappers`).

#![deny(missing_docs)]
#![deny(unsafe_op_in_unsafe_fn)]
#![warn(rust_2018_idioms)]

pub mod abi;
pub mod codec;
pub mod hooks;
pub mod host;
pub mod manifest;
pub mod wrappers;

#[cfg(test)]
mod tests;

// Re-export the proc-macro at the crate root so users can write
// `gonext_sdk::plugin_init!(...)` without depending on the companion
// crate directly.
#[cfg(feature = "macros")]
pub use gonext_sdk_macros::plugin_init;

/// Internal re-exports the [`plugin_init!`] macro expands against.
/// Plugin authors should not name these directly — they are subject to
/// change without notice. Routing through this module keeps the macro
/// independent of whether the caller's crate is `no_std` or not.
#[doc(hidden)]
pub mod __macro_support {
    pub use std::format;
    pub use std::vec::Vec;
}

/// The prelude is the single import that gives plugin authors every
/// type they need to write a plugin. Bring it into scope with
/// `use gonext_sdk::prelude::*;`.
pub mod prelude {
    pub use crate::abi::{LogLevel, ResultStatus};
    pub use crate::codec::{ActionPayload, FilterPayload, FilterResult};
    pub use crate::hooks::{PluginContext, SdkError};
    pub use crate::host::Host;
    pub use crate::manifest::{
        Capability, Dependency, Hooks, KVStorage, Manifest, ManifestBuilder, Requires, Storage,
    };
    pub use crate::wrappers::{
        HttpFetchRequest, HttpFetchResponse, MediaReadResponse, NetStatus, UserReadResponse,
    };
}
