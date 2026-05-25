//! Unit tests for the codec + manifest builder. These run on the host
//! architecture (not wasm32-wasi) so we can validate the JSON shapes
//! against fixtures without a wazero round-trip.

use std::string::ToString;
use std::vec;

#[test]
fn action_payload_decodes_envelope_shape() {
    // The wire shape must match what the host's MarshalActionPayload
    // emits: {"kind":"action","args":[...]}.
    let raw = br#"{"kind":"action","args":[1,"two",{"x":3}]}"#;
    let ap = crate::codec::decode_action_payload(raw).expect("decode");
    assert!(matches!(ap.kind, crate::codec::PayloadKind::Action));
    assert_eq!(ap.args.len(), 3);
    assert_eq!(ap.args[0], serde_json::json!(1));
    assert_eq!(ap.args[1], serde_json::json!("two"));
    assert_eq!(ap.args[2], serde_json::json!({"x": 3}));
}

#[test]
fn action_payload_handles_empty_args() {
    // The host emits "args":[] for the zero-arg action case; nil is
    // never on the wire.
    let raw = br#"{"kind":"action","args":[]}"#;
    let ap = crate::codec::decode_action_payload(raw).expect("decode");
    assert_eq!(ap.args.len(), 0);
}

#[test]
fn filter_payload_decodes_envelope_shape() {
    let raw = br#"{"kind":"filter","value":"<p>hi</p>","args":[42]}"#;
    let fp = crate::codec::decode_filter_payload(raw).expect("decode");
    assert!(matches!(fp.kind, crate::codec::PayloadKind::Filter));
    assert_eq!(fp.value, serde_json::json!("<p>hi</p>"));
    assert_eq!(fp.args, vec![serde_json::json!(42)]);
}

#[test]
fn filter_payload_handles_null_value() {
    // The host emits "value":null when ApplyFilters was called with
    // no value — the wire shape MUST keep the field present.
    let raw = br#"{"kind":"filter","value":null,"args":[]}"#;
    let fp = crate::codec::decode_filter_payload(raw).expect("decode");
    assert_eq!(fp.value, serde_json::Value::Null);
}

#[test]
fn filter_result_round_trips() {
    let result_bytes =
        crate::codec::encode_filter_result(&"transformed".to_string()).expect("encode");
    let s = core::str::from_utf8(&result_bytes).expect("utf8");
    // Expected shape: {"value":"transformed"} — the wrapper field.
    assert_eq!(s, r#"{"value":"transformed"}"#);
}

#[test]
fn filter_result_accepts_complex_value() {
    let v = serde_json::json!({"score": 42, "tags": ["a", "b"]});
    let result_bytes = crate::codec::encode_filter_result_value(v.clone()).expect("encode");
    let decoded: crate::codec::FilterResult = serde_json::from_slice(&result_bytes).expect("decode");
    assert_eq!(decoded.value, v);
}

#[test]
fn manifest_builder_produces_minimal_shape() {
    let m = crate::manifest::ManifestBuilder::new("my-plugin", "0.1.0", "plugin.wasm").build();
    assert_eq!(m.api_version, crate::manifest::API_VERSION);
    assert_eq!(m.name, "my-plugin");
    assert_eq!(m.version, "0.1.0");
    assert_eq!(m.entry, "plugin.wasm");
    assert!(m.capabilities.is_empty());
    assert!(m.hooks.is_none());
}

#[test]
fn manifest_builder_collects_hooks_and_caps() {
    let m = crate::manifest::ManifestBuilder::new("p", "1.0.0", "p.wasm")
        .capability(crate::manifest::Capability::KvRead)
        .capability(crate::manifest::Capability::KvWrite)
        .action("save_post")
        .filter("the_content")
        .job("p.recompute")
        .requires_host(">=0.1.0")
        .depends_on("other", "^1.0.0")
        .kv_storage(Some(1024), Some(100))
        .build();

    assert_eq!(m.capabilities, vec!["kv.read", "kv.write"]);
    let hooks = m.hooks.expect("hooks present");
    assert_eq!(hooks.actions, vec!["save_post"]);
    assert_eq!(hooks.filters, vec!["the_content"]);
    assert_eq!(m.jobs, vec!["p.recompute"]);
    assert_eq!(m.requires.expect("requires").host, ">=0.1.0");
    assert_eq!(m.depends.len(), 1);
    assert_eq!(m.depends[0].name, "other");
    let storage = m.storage.expect("storage");
    let kv = storage.kv.expect("kv");
    assert_eq!(kv.max_bytes, Some(1024));
    assert_eq!(kv.max_keys, Some(100));
}

#[test]
fn manifest_serializes_to_host_compatible_json() {
    // The serialized form MUST match the shape the host's JSON-Schema
    // accepts. We don't run the host validator here (no Go in the
    // test harness) but we check the field names match.
    let m = crate::manifest::ManifestBuilder::new("test-plugin", "0.1.0", "out.wasm")
        .capability("posts.read")
        .filter("the_content")
        .requires_host(">=0.1.0")
        .build();

    let json = m.to_json().expect("serialize");
    // apiVersion must be camelCase (the host's schema requires it).
    assert!(json.contains("\"apiVersion\""));
    assert!(json.contains("\"gonext.io/v1\""));
    assert!(json.contains("\"name\": \"test-plugin\""));
    assert!(json.contains("\"entry\": \"out.wasm\""));
    assert!(json.contains("\"posts.read\""));
    assert!(json.contains("\"the_content\""));
    // Empty collections should be omitted (skip_serializing_if).
    assert!(!json.contains("\"depends\""));
    assert!(!json.contains("\"jobs\""));
}

#[test]
fn manifest_round_trip_through_json() {
    // The Manifest type must be self-consistent: serialize and
    // deserialize and you get the same fields back.
    let original = crate::manifest::ManifestBuilder::new("rt", "1.2.3", "rt.wasm")
        .capability(crate::manifest::Capability::HttpFetch)
        .action("on_install")
        .action("on_activate")
        .requires_host(">=0.5.0")
        .build();

    let json = original.to_json_bytes().expect("ser");
    let parsed: crate::manifest::Manifest = serde_json::from_slice(&json).expect("de");
    assert_eq!(parsed.name, "rt");
    assert_eq!(parsed.version, "1.2.3");
    assert_eq!(parsed.capabilities, vec!["http.fetch"]);
    let hooks = parsed.hooks.expect("hooks");
    assert_eq!(hooks.actions, vec!["on_install", "on_activate"]);
}

#[test]
fn pack_result_round_trips() {
    let packed = crate::abi::pack_result(0x1234_5678, 42);
    let (ptr, len) = crate::host::unpack_result(packed as i64);
    assert_eq!(ptr, 0x1234_5678);
    assert_eq!(len, 42);
}

#[test]
fn pack_result_preserves_negative_sentinel() {
    let packed = crate::abi::pack_result(0, -3);
    let (ptr, len) = crate::host::unpack_result(packed as i64);
    assert_eq!(ptr, 0);
    assert_eq!(len, -3);
}

#[test]
fn pack_status_matches_host_layout() {
    use crate::abi::{pack_status, ResultStatus};
    // (ptr=0, len=ResultStatusBadPayload) — must equal the host's
    // packResult(0, -3) = 0x00000000_FFFFFFFD.
    let packed = pack_status(ResultStatus::BadPayload);
    assert_eq!(packed, 0x00000000_FFFFFFFD);
}

#[test]
fn capability_string_table_matches_host() {
    use crate::manifest::Capability;
    // The capability catalog is shared with the host. We hard-code
    // the strings here so a typo in the enum surfaces immediately.
    assert_eq!(Capability::HttpFetch.as_str(), "http.fetch");
    assert_eq!(Capability::HooksSubscribe.as_str(), "hooks.subscribe");
    assert_eq!(Capability::AuditEmit.as_str(), "audit.emit");
    assert_eq!(Capability::CronRegister.as_str(), "cron.register");
}

#[test]
fn plugin_context_dispatches_to_registered_action() {
    use crate::hooks::PluginContext;
    let mut ctx = PluginContext::new();
    ctx.register_action("hello", |_args| Ok(()));
    assert!(ctx.find_action("hello").is_some());
    assert!(ctx.find_action("missing").is_none());
}

#[test]
fn plugin_context_dispatches_to_registered_filter() {
    use crate::hooks::PluginContext;
    let mut ctx = PluginContext::new();
    ctx.register_filter("the_content", |v, _| Ok(serde_json::to_vec(v).unwrap()));
    assert!(ctx.find_filter("the_content").is_some());
    assert!(ctx.find_filter("missing").is_none());
}

#[test]
fn dispatch_unknown_hook_returns_sentinel() {
    use crate::abi::{pack_status, ResultStatus};
    use crate::hooks::{dispatch, PluginContext};
    let ctx = PluginContext::new();
    let payload = br#"{"kind":"action","args":[]}"#;
    let packed = dispatch(&ctx, "no.such.hook", payload);
    assert_eq!(packed, pack_status(ResultStatus::UnknownHook));
}

#[test]
fn dispatch_bad_payload_returns_sentinel() {
    use crate::abi::{pack_status, ResultStatus};
    use crate::hooks::{dispatch, PluginContext};
    let ctx = PluginContext::new();
    let packed = dispatch(&ctx, "x", b"not-json");
    assert_eq!(packed, pack_status(ResultStatus::BadPayload));
}

#[test]
fn dispatch_action_returns_ok_on_success() {
    use crate::abi::pack_ok;
    use crate::hooks::{dispatch, PluginContext};
    let mut ctx = PluginContext::new();
    ctx.register_action("hello", |_| Ok(()));
    let packed = dispatch(&ctx, "hello", br#"{"kind":"action","args":[]}"#);
    assert_eq!(packed, pack_ok());
}

#[test]
fn dispatch_action_propagates_generic_error() {
    use crate::abi::{pack_status, ResultStatus};
    use crate::hooks::{dispatch, PluginContext, SdkError};
    let mut ctx = PluginContext::new();
    ctx.register_action("bang", |_| Err(SdkError::Generic("boom".into())));
    let packed = dispatch(&ctx, "bang", br#"{"kind":"action","args":[]}"#);
    assert_eq!(packed, pack_status(ResultStatus::Error));
}
