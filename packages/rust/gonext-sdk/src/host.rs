//! Raw host imports and a safe wrapper façade.
//!
//! The `extern "C"` block at the bottom of this file lists every `gn_*`
//! function the host runtime exposes. These names are resolved by the
//! wazero linker at load time against the host modules registered in
//! `packages/go/plugins/runtime/host*.go`.
//!
//! Plugin code should NOT call the raw imports directly — the [`Host`]
//! struct wraps each one with bounds-checked memory marshaling, typed
//! errors, and serde-derived envelopes. The raw block is `pub` only so
//! advanced consumers (e.g. test harnesses, code-generators) can wire
//! something custom on top without re-exporting through a private API.

use std::string::{String, ToString};
use std::vec::Vec;
use core::slice;

use crate::abi::{pack_result, LogLevel, MAX_PAYLOAD_BYTES};

/// `Host` is a unit-style namespace for the safe wrappers — every
/// method is associated and stateless. We model it as a struct rather
/// than free functions so plugin code reads as `Host::log(...)`,
/// matching the WordPress-style API plugin authors expect.
pub struct Host;

/// Top-level error type returned by every safe wrapper. The unit-struct
/// variants are intentionally lightweight; callers usually only need to
/// know whether a call failed, not exactly which boundary tripped.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum HostError {
    /// The host returned a negative-length sentinel signalling that the
    /// call failed. The wrapper does not attempt to interpret which
    /// sentinel because the catalogs differ between subsystems (network
    /// vs hooks); the raw `i32` is preserved for callers that care.
    HostStatus(i32),
    /// Guest-side JSON marshaling failed (rare — typically only happens
    /// on cycles).
    Marshal(String),
    /// Guest-side allocator returned 0 — out of memory.
    AllocFailed,
    /// The host returned a result pointer claiming a length greater
    /// than [`MAX_PAYLOAD_BYTES`]. The wrapper refuses to read it.
    ResultTooLarge,
}

impl core::fmt::Display for HostError {
    fn fmt(&self, f: &mut core::fmt::Formatter<'_>) -> core::fmt::Result {
        match self {
            HostError::HostStatus(s) => write!(f, "gonext_sdk: host returned status {}", s),
            HostError::Marshal(s) => write!(f, "gonext_sdk: marshal error: {}", s),
            HostError::AllocFailed => write!(f, "gonext_sdk: guest allocator failed"),
            HostError::ResultTooLarge => write!(f, "gonext_sdk: result exceeds host cap"),
        }
    }
}

impl From<serde_json::Error> for HostError {
    fn from(e: serde_json::Error) -> Self {
        HostError::Marshal(e.to_string())
    }
}

/// Result alias used throughout the safe wrappers.
pub type HostResult<T> = Result<T, HostError>;

impl Host {
    /// Emit a host-side structured log line at `level`. Best-effort:
    /// errors from the host are swallowed so plugin code stays linear.
    /// Mirrors `env.gn_log` (level i32, ptr i32, len i32).
    pub fn log(level: LogLevel, message: &str) {
        let bytes = message.as_bytes();
        // The host caps log strings at 64 KiB; anything larger is
        // truncated rather than dropped so the operator still sees
        // something useful. The check is here rather than in the host
        // so misbehaving plugins surface their own issue.
        let len = core::cmp::min(bytes.len(), 64 * 1024);
        unsafe {
            raw::gn_log(level as i32, bytes.as_ptr() as u32, len as u32);
        }
    }

    /// Trap immediately with the supplied reason. The host closes the
    /// calling module with exit code 1, surfacing as a host-side
    /// `TrapError`. Use sparingly — a panic is almost always a bug
    /// rather than a control-flow primitive.
    ///
    /// This function does not return; callers should follow it with
    /// `unreachable!()` or use `!` semantics via [`panic_now`].
    pub fn panic_now(reason: &str) -> ! {
        let bytes = reason.as_bytes();
        let len = core::cmp::min(bytes.len(), 64 * 1024);
        unsafe {
            raw::gn_panic(bytes.as_ptr() as u32, len as u32);
        }
        // gn_panic doesn't return on the happy path, but the compiler
        // doesn't know that — keep the !-return by looping. The host
        // has already closed the module so the loop is unreachable in
        // practice.
        loop {}
    }

    /// Returns the host's wall-clock time in Unix milliseconds. Mirrors
    /// `env.gn_time_ms`. NOTE: this is wall-clock and can jump
    /// backwards on NTP corrections — use it for stamping events, not
    /// for measuring durations.
    pub fn time_ms() -> i64 {
        unsafe { raw::gn_time_ms() }
    }
}

/// Decode a packed `(ptr, len)` returned by a host call. The high 32
/// bits are the pointer; the low 32 bits are the length, interpreted as
/// i32 so negative sentinels survive.
///
/// We take `i64` because Rust's extern blocks that return packed
/// pointers conventionally model them as signed — WASM has only one
/// 64-bit type and Rust picks `i64` by default. The bit-level shape is
/// identical to a `u64` cast.
#[inline]
pub fn unpack_result(packed: i64) -> (u32, i32) {
    let p = packed as u64;
    let ptr = (p >> 32) as u32;
    let len = (p & 0xFFFF_FFFF) as u32 as i32;
    (ptr, len)
}

/// Marshal `value` as JSON, hand it to the host via a `(ptr, len)` pair
/// pointing into this guest's linear memory. The pair is the address of
/// the bytes we just allocated; the host reads them in-place and never
/// retains the pointer past its return.
///
/// Returns a `Vec<u8>` whose ownership is moved into the caller — the
/// caller MUST keep it alive for the duration of the host call. The
/// most common pattern is to leak it via `Box::leak` or to bind it to a
/// local variable in the wrapper that outlives the call.
pub(crate) fn marshal_request<T: serde::Serialize>(value: &T) -> HostResult<Vec<u8>> {
    let bytes = serde_json::to_vec(value)?;
    if bytes.len() > MAX_PAYLOAD_BYTES {
        return Err(HostError::ResultTooLarge);
    }
    Ok(bytes)
}

/// Read `length` bytes from this module's linear memory starting at
/// `ptr` and copy them into a freshly-allocated `Vec<u8>`. The host
/// returned `(ptr, length)` from a call after writing the bytes via the
/// guest's `gn_alloc`; the guest owns them. We copy because most
/// callers want a 'static buffer, and the additional malloc is cheap
/// compared to the host hop.
///
/// # Safety
///
/// The caller must ensure `ptr` and `length` point at a valid
/// allocation produced by `gn_alloc`. The wrapper functions in this
/// crate enforce that contract.
pub(crate) unsafe fn read_host_result(ptr: u32, length: i32) -> HostResult<Vec<u8>> {
    if length < 0 {
        return Err(HostError::HostStatus(length));
    }
    let len = length as usize;
    if len == 0 {
        return Ok(Vec::new());
    }
    if len > MAX_PAYLOAD_BYTES {
        return Err(HostError::ResultTooLarge);
    }
    // SAFETY: caller upheld the contract; ptr/length identify a valid
    // allocation we just received from the host (and which the host got
    // from our own gn_alloc).
    let bytes = unsafe { slice::from_raw_parts(ptr as *const u8, len) }.to_vec();
    // The host docs say guests SHOULD `gn_free` the result buffer once
    // copied. Our `gn_free` is a no-op (see crate::hooks) so the call
    // is purely advisory, but emitting it keeps the contract explicit
    // for future allocator implementations.
    unsafe {
        raw::gn_free_self(ptr, len as u32);
    }
    Ok(bytes)
}

/// Issue a `(req_ptr, req_len) -> i64` style host call: serialize
/// `request` as JSON, pin it in this module's memory, invoke `f`, and
/// decode the packed `(ptr, len)` return into a `Vec<u8>`.
///
/// Used by every JSON-envelope host wrapper (http_fetch, media_read,
/// users_read, db_read, db_write, kv_get/set/del, cache_invalidate,
/// metric_observe, event_emit, span_event, i18n_translate, audit_emit,
/// cron_register, secrets_get). The wrapper-specific layer on top
/// deserializes the bytes into the expected response shape.
pub(crate) fn call_json<R, F>(request: &R, f: F) -> HostResult<Vec<u8>>
where
    R: serde::Serialize,
    F: FnOnce(u32, u32) -> i64,
{
    let bytes = marshal_request(request)?;
    let packed = f(bytes.as_ptr() as u32, bytes.len() as u32);
    let (ptr, len) = unpack_result(packed);
    // SAFETY: the host upheld its half of the contract — ptr is either
    // 0 (signalling status in low 32 bits) or points at a buffer the
    // host allocated through our gn_alloc.
    unsafe { read_host_result(ptr, len) }
}

/// Same as [`call_json`] but for host functions whose return type is
/// `i32` (a status code, not a packed pointer). Used by
/// `gn_metric_observe`, `gn_event_emit`, `gn_span_event`. A 0 return is
/// success; non-zero is a host-side status the caller can inspect.
pub(crate) fn call_i32<R, F>(request: &R, f: F) -> HostResult<i32>
where
    R: serde::Serialize,
    F: FnOnce(u32, u32) -> i32,
{
    let bytes = marshal_request(request)?;
    let status = f(bytes.as_ptr() as u32, bytes.len() as u32);
    Ok(status)
}

// ===========================================================================
// Raw host imports. Plugin code should NOT call into here — use Host::* or
// the wrappers in [`crate::wrappers`].
//
// Names match the host exports in:
//   - packages/go/plugins/runtime/host.go           (env)
//   - packages/go/plugins/runtime/host_data.go      (gonext_data)
//   - packages/go/plugins/runtime/host_network.go   (env_net)
//   - packages/go/plugins/runtime/host_observability.go (env)
//
// Module names are encoded with `#[link(wasm_import_module = "...")]`.
// ===========================================================================

/// Raw extern declarations. The `raw` module is `pub` so advanced
/// consumers (test harnesses, custom dispatchers) can wire something
/// directly on top, but every method here is `unsafe` and operates on
/// guest pointers.
pub mod raw {
    // env module — baseline trio + observability extras
    #[link(wasm_import_module = "env")]
    extern "C" {
        /// `gn_log(level i32, ptr i32, len i32)` — best-effort log line.
        pub fn gn_log(level: i32, ptr: u32, len: u32);
        /// `gn_panic(ptr i32, len i32)` — abort the call, surface reason.
        pub fn gn_panic(ptr: u32, len: u32);
        /// `gn_time_ms() -> i64` — wall-clock milliseconds.
        pub fn gn_time_ms() -> i64;


        /// `gn_i18n_translate(key_ptr, key_len, locale_ptr, locale_len) -> i64`
        /// — translate a key for a locale; packed (ptr, len) return,
        /// (0, 0) means "no translation, fall back to key".
        pub fn gn_i18n_translate(
            key_ptr: u32,
            key_len: u32,
            locale_ptr: u32,
            locale_len: u32,
        ) -> i64;

        /// `gn_metric_observe(name_ptr, name_len, value, tags_ptr, tags_len) -> i32`
        /// — record one observation; 0=ok, non-zero=host status.
        pub fn gn_metric_observe(
            name_ptr: u32,
            name_len: u32,
            value: f64,
            tags_ptr: u32,
            tags_len: u32,
        ) -> i32;

        /// `gn_event_emit(name_ptr, name_len, data_ptr, data_len) -> i32`
        /// — publish a structured event to the host event bus.
        pub fn gn_event_emit(name_ptr: u32, name_len: u32, data_ptr: u32, data_len: u32) -> i32;

        /// `gn_span_event(name_ptr, name_len, attrs_ptr, attrs_len) -> i32`
        /// — add an OpenTelemetry span event to the active span.
        pub fn gn_span_event(name_ptr: u32, name_len: u32, attrs_ptr: u32, attrs_len: u32) -> i32;
    }

    // env_net module — network ABIs (http.fetch, media.read, users.read)
    #[link(wasm_import_module = "env_net")]
    extern "C" {
        /// `gn_http_fetch(req_ptr, req_len) -> i64` — outbound HTTP via
        /// the host's SSRF-guarded client. Packed (ptr, len) return.
        pub fn gn_http_fetch(req_ptr: u32, req_len: u32) -> i64;

        /// `gn_media_read(req_ptr, req_len) -> i64` — fetch a media row.
        pub fn gn_media_read(req_ptr: u32, req_len: u32) -> i64;

        /// `gn_users_read(req_ptr, req_len) -> i64` — fetch a user row.
        pub fn gn_users_read(req_ptr: u32, req_len: u32) -> i64;
    }

    // gonext_data module — db.read/write, KV, cache.invalidate
    #[link(wasm_import_module = "gonext_data")]
    extern "C" {
        /// `gn_db_read(query_ptr, query_len, args_ptr, args_len) -> i64`
        /// — read-only SQL through the per-plugin pool.
        pub fn gn_db_read(query_ptr: u32, query_len: u32, args_ptr: u32, args_len: u32) -> i64;

        /// `gn_db_write(query_ptr, query_len, args_ptr, args_len) -> i64`
        /// — mutating SQL through the per-plugin pool.
        pub fn gn_db_write(query_ptr: u32, query_len: u32, args_ptr: u32, args_len: u32) -> i64;

        /// `gn_kv_get(key_ptr, key_len) -> i64` — read a KV value.
        pub fn gn_kv_get(key_ptr: u32, key_len: u32) -> i64;

        /// `gn_kv_set(key_ptr, key_len, val_ptr, val_len) -> i64` — set a KV value.
        pub fn gn_kv_set(key_ptr: u32, key_len: u32, val_ptr: u32, val_len: u32) -> i64;

        /// `gn_kv_del(key_ptr, key_len) -> i64` — delete a KV value.
        pub fn gn_kv_del(key_ptr: u32, key_len: u32) -> i64;

        /// `gn_kv_incr(key_ptr, key_len, delta i64) -> i64` — atomic counter.
        pub fn gn_kv_incr(key_ptr: u32, key_len: u32, delta: i64) -> i64;

        /// `gn_cache_invalidate(tags_ptr, tags_len) -> i64` — purge by tag.
        pub fn gn_cache_invalidate(tags_ptr: u32, tags_len: u32) -> i64;
    }

    // env_platform module — secrets.get, audit.emit, cron.register
    // (pending PR #456; the imports are declared so the SDK is ready
    // the moment the host lands. A plugin that calls these against a
    // runtime built without the platform host module will fail at link
    // time, which is the correct behavior — capability denials happen
    // at the host, not silently here.)
    #[link(wasm_import_module = "env_platform")]
    extern "C" {
        /// `gn_secrets_get(name_ptr, name_len) -> i64` — read a secret
        /// from the host's encrypted store.
        pub fn gn_secrets_get(name_ptr: u32, name_len: u32) -> i64;

        /// `gn_audit_emit(event_ptr, event_len) -> i32` — write an
        /// audit row.
        pub fn gn_audit_emit(event_ptr: u32, event_len: u32) -> i32;

        /// `gn_cron_register(spec_ptr, spec_len) -> i32` — register a
        /// recurring job from a cron expression.
        pub fn gn_cron_register(spec_ptr: u32, spec_len: u32) -> i32;
    }

    /// Self-references the guest's own `gn_free` export. The host
    /// allocates result buffers through the guest's `gn_alloc`, so the
    /// guest is the only party that knows how to free them. Calling our
    /// own `gn_free` after a copy keeps the contract explicit.
    ///
    /// The plugin_init! macro defines a real `gn_free`; this thunk
    /// dispatches to whichever symbol is in scope at link time.
    ///
    /// # Safety
    ///
    /// Only safe to call with a `(ptr, size)` pair the host produced
    /// from a result envelope. Wrappers enforce this.
    #[inline]
    pub unsafe fn gn_free_self(ptr: u32, size: u32) {
        extern "C" {
            fn gn_free(ptr: u32, size: u32);
        }
        // SAFETY: caller upheld the contract.
        unsafe {
            gn_free(ptr, size);
        }
    }
}

// pack_result is re-exported here so the plugin_init! macro can call
// `gonext_sdk::host::pack_result` without depending on the abi module
// path resolution.
#[doc(hidden)]
pub fn pack_result_for_macro(ptr: u32, length: i32) -> u64 {
    pack_result(ptr, length)
}

// Conversion helpers between the u64 packed result the SDK uses
// internally (the bit pattern is unsigned-friendly for tests and
// comparisons) and the i64 the WASM extern returns.
#[doc(hidden)]
#[inline]
pub fn packed_u64_to_i64(p: u64) -> i64 {
    p as i64
}
