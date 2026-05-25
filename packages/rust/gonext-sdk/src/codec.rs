//! JSON envelope codec — mirrors `packages/go/plugins/abi/hooks/marshal.go`.
//!
//! Every hook invocation crosses the wasm boundary as JSON. We use
//! `serde_json` because it is the lowest-common-denominator codec every
//! plugin SDK has stdlib-grade support for, and the hot path of hook
//! dispatch is the host round-trip, not the codec.
//!
//! ## Wire shapes
//!
//! Action payload:
//!
//! ```json
//! { "kind": "action", "args": [...] }
//! ```
//!
//! Filter payload:
//!
//! ```json
//! { "kind": "filter", "value": <json>, "args": [...] }
//! ```
//!
//! Filter result (what the guest writes back):
//!
//! ```json
//! { "value": <json> }
//! ```
//!
//! Actions return no body — the side effect is the point.

use std::vec::Vec;
use serde::{Deserialize, Serialize};

/// Payload kinds carried over the ABI. Matches `PayloadKind` in
/// `marshal.go`.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum PayloadKind {
    /// Action call: a list of args, no transformable value.
    Action,
    /// Filter call: a value to transform plus action-like extras.
    Filter,
}

/// Wire form of an action-call payload. `Args` is the bus-level
/// variadic `args ...any` rendered as a JSON array.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ActionPayload {
    /// Always [`PayloadKind::Action`] when decoded.
    pub kind: PayloadKind,
    /// Action args. Each entry is a free-form JSON value because the
    /// host bus is untyped.
    #[serde(default)]
    pub args: Vec<serde_json::Value>,
}

/// Wire form of a filter-call payload.
///
/// `value` is the transformable value threaded through the filter
/// chain. We keep it as `serde_json::Value` so plugin authors can pull
/// it out and re-serialize into whatever typed shape their hook expects
/// without paying a decode+re-encode round-trip on every call.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct FilterPayload {
    /// Always [`PayloadKind::Filter`] when decoded.
    pub kind: PayloadKind,
    /// The value the filter is asked to transform. Defaults to `null`
    /// if the host omitted it.
    #[serde(default)]
    pub value: serde_json::Value,
    /// Per-call extras (the variadic args in `ApplyFilters`).
    #[serde(default)]
    pub args: Vec<serde_json::Value>,
}

/// Wire form of a filter handler's return value. Just the transformed
/// `value`; failures use the [`crate::abi::ResultStatus`] return path
/// instead of a sibling error field.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct FilterResult {
    /// The transformed value. The host hands these bytes straight back
    /// to `ApplyFilters` callers as `[]byte`, untouched.
    pub value: serde_json::Value,
}

/// Decode a host-supplied action payload from the bytes the host wrote
/// into our linear memory. Returns `serde_json::Error` if the bytes
/// aren't valid JSON or the shape doesn't match — callers should
/// translate that into [`crate::abi::ResultStatus::BadPayload`].
pub fn decode_action_payload(buf: &[u8]) -> Result<ActionPayload, serde_json::Error> {
    serde_json::from_slice(buf)
}

/// Decode a host-supplied filter payload.
pub fn decode_filter_payload(buf: &[u8]) -> Result<FilterPayload, serde_json::Error> {
    serde_json::from_slice(buf)
}

/// Encode a filter handler's transformed value into the bytes the host
/// expects to read back from `gn_handle_hook`. The wrapper around
/// [`serde_json::to_vec`] exists so the SDK can change the codec
/// (msgpack, postcard) in a future v2 without rewriting every caller.
pub fn encode_filter_result<T: Serialize>(value: &T) -> Result<Vec<u8>, serde_json::Error> {
    let v = serde_json::to_value(value)?;
    serde_json::to_vec(&FilterResult { value: v })
}

/// Encode a raw `serde_json::Value` as a filter result. Useful when the
/// handler already constructed the JSON shape itself (e.g. by mutating
/// the incoming `FilterPayload::value`).
pub fn encode_filter_result_value(value: serde_json::Value) -> Result<Vec<u8>, serde_json::Error> {
    serde_json::to_vec(&FilterResult { value })
}
