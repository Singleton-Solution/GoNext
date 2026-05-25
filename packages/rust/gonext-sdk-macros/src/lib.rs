//! Proc-macro companion crate for [`gonext-sdk`](https://docs.rs/gonext-sdk).
//!
//! Plugin authors normally depend on `gonext-sdk` and use the re-export
//! at `gonext_sdk::plugin_init!`. This crate exists because procedural
//! macros must live in their own crate marked `proc-macro = true`; we
//! split the SDK in two so plugin authors don't have to think about it.

use proc_macro::TokenStream;
use proc_macro2::TokenStream as TokenStream2;
use quote::quote;
use syn::{parse_macro_input, Ident};

/// `plugin_init!(your_init_fn)` generates the wasm exports the host
/// expects, including:
///
///  - `gn_alloc(size: i32) -> i32`
///  - `gn_free(ptr: i32, size: i32)`
///  - `gn_handle_hook(name_ptr, name_len, payload_ptr, payload_len) -> i64`
///  - `_start()` — calls `your_init_fn(&mut PluginContext)` once and
///    parks the context in a thread-local for the dispatcher.
///
/// The user's init function must have signature:
///
/// ```ignore
/// fn init(ctx: &mut gonext_sdk::hooks::PluginContext) -> Result<(), gonext_sdk::hooks::SdkError>;
/// ```
///
/// ## Example
///
/// ```ignore
/// use gonext_sdk::prelude::*;
///
/// fn init(ctx: &mut PluginContext) -> Result<(), SdkError> {
///     ctx.register_action("save_post", |_args| {
///         Host::log(LogLevel::Info, "save_post fired");
///         Ok(())
///     });
///     Ok(())
/// }
///
/// gonext_sdk::plugin_init!(init);
/// ```
///
/// ## Allocator strategy
///
/// The generated `gn_alloc` leaks `Vec<u8>` allocations into a global
/// list, mirroring the example plugin in `examples/plugins/seo/main.go`.
/// This is fine for the typical per-hook size (a few KiB to a few MiB)
/// and bounded by the host's payload caps. A future version may swap
/// to a per-invocation bump allocator without changing the user-facing
/// API.
#[proc_macro]
pub fn plugin_init(input: TokenStream) -> TokenStream {
    let init_fn = parse_macro_input!(input as Ident);
    let expanded: TokenStream2 = quote! {
        // -----------------------------------------------------------
        // Allocator exports.
        //
        // Strategy: keep a global Vec<Vec<u8>> so the GC (well, the
        // allocator) doesn't free our buffers under us. gn_free is a
        // no-op — the host calls it once per result buffer on the
        // success path, but we don't need to actually reclaim because
        // the next allocation will overwrite or extend the global.
        //
        // The 1-byte stub for size==0 returns a stable non-zero
        // pointer so the host can tell "you gave me OOM" apart from
        // "you gave me a zero-length buffer".
        // -----------------------------------------------------------

        #[doc(hidden)]
        static mut __GONEXT_ALLOCATIONS: ::core::option::Option<
            ::gonext_sdk::__macro_support::Vec<::gonext_sdk::__macro_support::Vec<u8>>,
        > = ::core::option::Option::None;

        #[doc(hidden)]
        #[no_mangle]
        pub extern "C" fn gn_alloc(size: u32) -> u32 {
            if size == 0 {
                return 1;
            }
            unsafe {
                if __GONEXT_ALLOCATIONS.is_none() {
                    __GONEXT_ALLOCATIONS = ::core::option::Option::Some(
                        ::gonext_sdk::__macro_support::Vec::new(),
                    );
                }
                let allocations = __GONEXT_ALLOCATIONS.as_mut().unwrap();
                let mut buf: ::gonext_sdk::__macro_support::Vec<u8> =
                    ::gonext_sdk::__macro_support::Vec::with_capacity(size as usize);
                buf.resize(size as usize, 0u8);
                let ptr = buf.as_mut_ptr() as u32;
                allocations.push(buf);
                ptr
            }
        }

        #[doc(hidden)]
        #[no_mangle]
        pub extern "C" fn gn_free(ptr: u32, size: u32) {
            // no-op; see allocator strategy above.
            let _ = ptr;
            let _ = size;
        }

        // -----------------------------------------------------------
        // _start — the WASI entry point. Called exactly once when the
        // host instantiates the module, before any hook fires. We use
        // it to build the PluginContext and stash it in a static so
        // gn_handle_hook can find it again.
        //
        // Errors from the user's init function are logged but don't
        // abort the module: the host will still resolve gn_handle_hook
        // and any hook call will see an empty context and return
        // UnknownHook. That gives the operator a way to diagnose the
        // failure without the plugin disappearing from the registry.
        // -----------------------------------------------------------

        #[doc(hidden)]
        static mut __GONEXT_CTX: ::core::option::Option<
            ::gonext_sdk::hooks::PluginContext,
        > = ::core::option::Option::None;

        #[doc(hidden)]
        #[no_mangle]
        pub extern "C" fn _start() {
            let mut ctx = ::gonext_sdk::hooks::PluginContext::new();
            if let ::core::result::Result::Err(e) = #init_fn(&mut ctx) {
                let msg = ::gonext_sdk::__macro_support::format!(
                    "plugin init failed: {}",
                    e
                );
                ::gonext_sdk::host::Host::log(
                    ::gonext_sdk::abi::LogLevel::Error,
                    &msg,
                );
            }
            unsafe {
                __GONEXT_CTX = ::core::option::Option::Some(ctx);
            }
        }

        // -----------------------------------------------------------
        // gn_handle_hook — the host-facing dispatcher. Reads the name
        // and payload out of our own linear memory, looks up the
        // matching closure on the cached context, and packs the
        // result.
        //
        // The dispatch itself lives in gonext_sdk::hooks::dispatch so
        // we can unit-test it on the host architecture without going
        // through the macro.
        // -----------------------------------------------------------

        #[doc(hidden)]
        #[no_mangle]
        pub extern "C" fn gn_handle_hook(
            name_ptr: u32,
            name_len: u32,
            payload_ptr: u32,
            payload_len: u32,
        ) -> u64 {
            // SAFETY: ptrs come from the host, which got them from our
            // own gn_alloc above. The lifetimes are bounded by this
            // call.
            let name_bytes = unsafe {
                if name_len == 0 {
                    &[][..]
                } else {
                    ::core::slice::from_raw_parts(name_ptr as *const u8, name_len as usize)
                }
            };
            let payload_bytes = unsafe {
                if payload_len == 0 {
                    &[][..]
                } else {
                    ::core::slice::from_raw_parts(payload_ptr as *const u8, payload_len as usize)
                }
            };
            let name = match ::core::str::from_utf8(name_bytes) {
                ::core::result::Result::Ok(s) => s,
                ::core::result::Result::Err(_) => {
                    return ::gonext_sdk::abi::pack_status(
                        ::gonext_sdk::abi::ResultStatus::BadPayload,
                    );
                }
            };
            let ctx = unsafe { __GONEXT_CTX.as_ref() };
            let ctx = match ctx {
                ::core::option::Option::Some(c) => c,
                ::core::option::Option::None => {
                    return ::gonext_sdk::abi::pack_status(
                        ::gonext_sdk::abi::ResultStatus::UnknownHook,
                    );
                }
            };
            ::gonext_sdk::hooks::dispatch(ctx, name, payload_bytes)
        }
    };
    TokenStream::from(expanded)
}
