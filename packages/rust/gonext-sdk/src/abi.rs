//! Shared ABI constants and packed-result helpers.
//!
//! Mirrors `packages/go/plugins/abi/hooks` on the host side. Keep this
//! file in lock-step with that package — any constant added there MUST
//! be reflected here, and any new sentinel MUST be added at the end of
//! [`ResultStatus`] (existing values are frozen).
//!
//! See [`crate`] docs for the wire-format overview.

/// Maximum bytes the host will transmit for a hook name. The manifest
/// schema's hook-name regex caps single names at 200 chars; this is the
/// runtime backstop. Plugin authors typically don't need to look at it.
pub const MAX_HOOK_NAME_LEN: usize = 256;

/// Maximum bytes the host will write into guest memory in a single
/// payload — and the maximum bytes the host will read back from a
/// result pointer. 1 MiB.
pub const MAX_PAYLOAD_BYTES: usize = 1 << 20;

/// Log levels accepted by [`crate::host::Host::log`]. The numbering
/// matches `packages/go/plugins/runtime/host.go`: 0=debug, 1=info,
/// 2=warn, 3=error. Unknown values route to info on the host side.
#[repr(i32)]
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum LogLevel {
    /// Verbose tracing — typically filtered out in production.
    Debug = 0,
    /// Informational events — the default.
    Info = 1,
    /// Recoverable irregularities the operator should know about.
    Warn = 2,
    /// Failures the plugin couldn't handle on its own.
    Error = 3,
}

/// Status sentinels the guest returns from `gn_handle_hook` to signal a
/// typed failure. They occupy the low 32 bits of the packed i64 return
/// when the high 32 bits (the pointer) are zero, so the host can tell a
/// failure apart from a normal `(ptr, len)` body.
///
/// Sentinels are negative `i32` values; lengths returned on the success
/// path are always non-negative, so the two never collide.
#[repr(i32)]
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ResultStatus {
    /// Success with no body. Normal return for an action handler.
    Ok = 0,
    /// Generic guest-reported error. Prefer a more specific sentinel.
    Error = -1,
    /// Guest allocator failed. Returned when `gn_alloc` would (or did)
    /// return 0 inside the handler.
    OutOfMemory = -2,
    /// Guest could not decode the payload the host sent. Almost always
    /// an ABI version mismatch.
    BadPayload = -3,
    /// Guest's dispatch table has no handler for the requested hook.
    /// Surfaces as a stale-plugin warning on the host.
    UnknownHook = -4,
    /// Reserved for the host's synthetic "the guest trapped" status —
    /// the guest never returns this. Listed for completeness.
    Trap = -5,
}

/// Pack a `(ptr, len)` into the i64 the host expects from
/// `gn_handle_hook`. The high 32 bits carry the pointer; the low 32
/// bits carry the length (interpreted as i32 so a negative sentinel
/// survives the round-trip).
///
/// This is the inverse of the host's `unpackResult`. Plugin authors
/// rarely call it directly — [`crate::plugin_init`] uses it under the
/// hood.
#[inline]
pub fn pack_result(ptr: u32, length: i32) -> u64 {
    (u64::from(ptr) << 32) | u64::from(length as u32)
}

/// Helper for the common "OK, no body" success return — encodes the
/// (0, 0) pair the host treats as a successful action call.
#[inline]
pub fn pack_ok() -> u64 {
    pack_result(0, ResultStatus::Ok as i32)
}

/// Helper for the common "failed with status N" return — encodes the
/// (0, status) pair the host decodes as the matching error.
#[inline]
pub fn pack_status(status: ResultStatus) -> u64 {
    pack_result(0, status as i32)
}
