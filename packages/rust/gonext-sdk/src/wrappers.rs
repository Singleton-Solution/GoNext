//! Safe typed wrappers over every `gn_*` host ABI.
//!
//! Each wrapper:
//!
//!  1. Serializes its typed request as JSON.
//!  2. Hands the bytes to the matching `extern "C"` import.
//!  3. Reads the packed `(ptr, len)` (or `i32`) return.
//!  4. Deserializes the response shape and returns it.
//!
//! Failures surface as [`crate::host::HostError::HostStatus`] carrying
//! the raw `i32` sentinel — callers that need the named sentinel can
//! match against the per-subsystem constants ([`NetStatus`],
//! [`DataStatus`], etc.).

use std::collections::BTreeMap;
use std::string::String;
use std::vec::Vec;
use serde::{Deserialize, Serialize};

use crate::host::{call_i32, call_json, Host, HostError, HostResult};

// ===========================================================================
// Network ABIs — env_net.{gn_http_fetch, gn_media_read, gn_users_read}
// ===========================================================================

/// Status sentinels returned by the network ABIs. Mirror
/// `NetResultStatus` in `host_network.go`. Callers usually match on
/// these instead of comparing to bare integers.
#[repr(i32)]
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum NetStatus {
    /// Bad request payload (empty URL, illegal method, etc.).
    BadRequest = -1,
    /// Capability denied — plugin lacks `http.fetch` / `media.read` /
    /// `users.read`.
    Denied = -2,
    /// Allowlist or SSRF guard rejection.
    Blocked = -3,
    /// Rate limit fired for this plugin.
    RateLimited = -4,
    /// Transport-level error talking to the upstream.
    Upstream = -5,
    /// Requested id did not exist (media.read / users.read).
    NotFound = -6,
    /// Host-side plumbing failure.
    Internal = -7,
}

/// Outbound HTTP request envelope. Mirrors `httpFetchRequest` in
/// `host_network.go`.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct HttpFetchRequest {
    /// HTTP method (GET, POST, PUT, DELETE, PATCH, HEAD, OPTIONS).
    pub method: String,
    /// Absolute URL. Must match an entry in the plugin's manifest
    /// `allow_hosts` list.
    pub url: String,
    /// Optional request headers.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub headers: Option<BTreeMap<String, String>>,
    /// Optional request body.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub body: Option<Vec<u8>>,
}

/// HTTP response envelope returned by [`Host::http_fetch`].
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct HttpFetchResponse {
    /// HTTP status code. A negative value means the host detected a
    /// failure before the round-trip completed; `error` will be set.
    pub status: i32,
    /// Response headers. Header names are lowercased by the host.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub headers: Option<BTreeMap<String, String>>,
    /// Response body (up to 10 MiB; truncated otherwise).
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub body: Option<Vec<u8>>,
    /// Set on host-detected failure. Empty when the upstream returned
    /// a real status, even a 4xx/5xx.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub error: Option<String>,
}

/// Wire envelope for `gn_media_read`. The id is the platform's media
/// row identifier.
#[derive(Debug, Clone, Serialize)]
pub struct MediaReadRequest<'a> {
    /// Media id — typically a UUID-like string.
    pub id: &'a str,
}

/// Response envelope for `gn_media_read`. Mirrors the host's
/// canonicalised media row.
#[derive(Debug, Clone, Deserialize)]
pub struct MediaReadResponse {
    /// Media id.
    pub id: String,
    /// Original filename.
    #[serde(default)]
    pub filename: String,
    /// MIME type.
    #[serde(default)]
    pub mime: String,
    /// Public URL.
    #[serde(default)]
    pub url: String,
    /// Free-form metadata bag (alt text, dimensions, etc.).
    #[serde(default)]
    pub meta: BTreeMap<String, serde_json::Value>,
}

/// Wire envelope for `gn_users_read`.
#[derive(Debug, Clone, Serialize)]
pub struct UserReadRequest<'a> {
    /// User id.
    pub id: &'a str,
}

/// Response envelope for `gn_users_read`. The host scrubs the password
/// hash and any session-related field before returning.
#[derive(Debug, Clone, Deserialize)]
pub struct UserReadResponse {
    /// User id.
    pub id: String,
    /// Username slug.
    #[serde(default)]
    pub username: String,
    /// Display name.
    #[serde(default)]
    pub display_name: String,
    /// Email address.
    #[serde(default)]
    pub email: String,
    /// Roles assigned to the user (admin, editor, ...).
    #[serde(default)]
    pub roles: Vec<String>,
}

impl Host {
    /// Issue an outbound HTTP request through the host's SSRF-guarded
    /// client. The plugin manifest must declare `http.fetch` in its
    /// capabilities and `allow_hosts` for any host being contacted.
    ///
    /// Returns the full response envelope. Application-level failures
    /// (4xx/5xx) come back as a populated `HttpFetchResponse`; only
    /// transport-level failures surface as [`HostError::HostStatus`].
    pub fn http_fetch(req: &HttpFetchRequest) -> HostResult<HttpFetchResponse> {
        let bytes = call_json(req, |p, l| unsafe { crate::host::raw::gn_http_fetch(p, l) })?;
        if bytes.is_empty() {
            return Err(HostError::Marshal("empty response".into()));
        }
        Ok(serde_json::from_slice(&bytes)?)
    }

    /// Look up a media row by id. Requires the `media.read` capability.
    pub fn media_read(id: &str) -> HostResult<MediaReadResponse> {
        let req = MediaReadRequest { id };
        let bytes = call_json(&req, |p, l| unsafe { crate::host::raw::gn_media_read(p, l) })?;
        Ok(serde_json::from_slice(&bytes)?)
    }

    /// Look up a user row by id. Requires the `users.read` capability.
    /// The host scrubs password hashes and session tokens before
    /// returning.
    pub fn users_read(id: &str) -> HostResult<UserReadResponse> {
        let req = UserReadRequest { id };
        let bytes = call_json(&req, |p, l| unsafe { crate::host::raw::gn_users_read(p, l) })?;
        Ok(serde_json::from_slice(&bytes)?)
    }
}

// ===========================================================================
// Data ABIs — gonext_data.{gn_db_read, gn_db_write, gn_kv_*, gn_cache_invalidate}
// ===========================================================================

/// Wire envelope for `gn_db_read` / `gn_db_write`. The host validates
/// the query against the per-plugin grants (table allowlist, mutation
/// scope) before executing.
#[derive(Debug, Clone, Serialize)]
pub struct DbRequest<'a> {
    /// SQL query string.
    pub query: &'a str,
    /// Positional bind parameters. Each entry is a free-form JSON
    /// value the host converts to the matching pg type.
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub args: Vec<serde_json::Value>,
}

/// Response envelope for `gn_db_read`.
#[derive(Debug, Clone, Deserialize)]
pub struct DbReadResponse {
    /// Column names in row-position order.
    #[serde(default)]
    pub columns: Vec<String>,
    /// Rows. Each row is an array of values, indexed by `columns`.
    #[serde(default)]
    pub rows: Vec<Vec<serde_json::Value>>,
}

/// Response envelope for `gn_db_write`. `rows_affected` follows
/// `database/sql.Result.RowsAffected()` semantics.
#[derive(Debug, Clone, Deserialize)]
pub struct DbWriteResponse {
    /// How many rows the mutation affected.
    #[serde(default)]
    pub rows_affected: i64,
}

impl Host {
    /// Read rows from the per-plugin Postgres pool. The query must be a
    /// SELECT; the host rejects anything else with a host status.
    pub fn db_read(query: &str, args: Vec<serde_json::Value>) -> HostResult<DbReadResponse> {
        let req = DbRequest { query, args };
        let bytes = call_json(&req, |p, l| unsafe {
            // The query and args are nested inside the single JSON
            // envelope — we don't split them across two pointer pairs.
            // The host decodes the envelope into (query, args).
            crate::host::raw::gn_db_read(p, l, 0, 0)
        })?;
        Ok(serde_json::from_slice(&bytes)?)
    }

    /// Execute a mutating SQL statement (INSERT / UPDATE / DELETE).
    pub fn db_write(query: &str, args: Vec<serde_json::Value>) -> HostResult<DbWriteResponse> {
        let req = DbRequest { query, args };
        let bytes = call_json(&req, |p, l| unsafe {
            crate::host::raw::gn_db_write(p, l, 0, 0)
        })?;
        Ok(serde_json::from_slice(&bytes)?)
    }

    /// Read a value from the per-plugin KV namespace. Returns
    /// `Ok(None)` when the key is unset; `Ok(Some(bytes))` otherwise.
    pub fn kv_get(key: &str) -> HostResult<Option<Vec<u8>>> {
        // KV uses raw key/value bytes, not a JSON envelope, so we
        // bypass call_json. The key bytes go directly to the host.
        let kb = key.as_bytes();
        let packed = unsafe { crate::host::raw::gn_kv_get(kb.as_ptr() as u32, kb.len() as u32) };
        let (ptr, len) = crate::host::unpack_result(packed);
        if len == -6 {
            // NetStatusNotFound is reused across data ABIs for the
            // "key absent" path. The host docs name it explicitly.
            return Ok(None);
        }
        if len < 0 {
            return Err(HostError::HostStatus(len));
        }
        if len == 0 {
            return Ok(Some(Vec::new()));
        }
        // SAFETY: host upheld the alloc contract.
        let bytes = unsafe { crate::host::read_host_result(ptr, len) }?;
        Ok(Some(bytes))
    }

    /// Set a key/value pair in the per-plugin KV namespace.
    pub fn kv_set(key: &str, value: &[u8]) -> HostResult<()> {
        let kb = key.as_bytes();
        let packed = unsafe {
            crate::host::raw::gn_kv_set(
                kb.as_ptr() as u32,
                kb.len() as u32,
                value.as_ptr() as u32,
                value.len() as u32,
            )
        };
        let (_, len) = crate::host::unpack_result(packed);
        if len < 0 {
            return Err(HostError::HostStatus(len));
        }
        Ok(())
    }

    /// Delete a key from the per-plugin KV namespace. No-op if the
    /// key was already absent.
    pub fn kv_del(key: &str) -> HostResult<()> {
        let kb = key.as_bytes();
        let packed = unsafe { crate::host::raw::gn_kv_del(kb.as_ptr() as u32, kb.len() as u32) };
        let (_, len) = crate::host::unpack_result(packed);
        if len < 0 {
            return Err(HostError::HostStatus(len));
        }
        Ok(())
    }

    /// Atomically increment a counter and return the new value. Used
    /// for counters, sequences, and rate-limit-style bookkeeping.
    pub fn kv_incr(key: &str, delta: i64) -> HostResult<i64> {
        let kb = key.as_bytes();
        let packed =
            unsafe { crate::host::raw::gn_kv_incr(kb.as_ptr() as u32, kb.len() as u32, delta) };
        let (_, len) = crate::host::unpack_result(packed);
        if len < 0 {
            return Err(HostError::HostStatus(len));
        }
        // The new value is encoded as the low 32 bits — kv_incr's
        // return doesn't follow the (ptr, len) shape; the host packs
        // the value into the same slot.
        Ok(len as i64)
    }

    /// Invalidate cache entries by tag. The tags list is passed as a
    /// JSON array.
    pub fn cache_invalidate(tags: &[&str]) -> HostResult<()> {
        let bytes = serde_json::to_vec(tags)?;
        let packed = unsafe {
            crate::host::raw::gn_cache_invalidate(bytes.as_ptr() as u32, bytes.len() as u32)
        };
        let (_, len) = crate::host::unpack_result(packed);
        if len < 0 {
            return Err(HostError::HostStatus(len));
        }
        Ok(())
    }
}

// ===========================================================================
// Observability ABIs — env.{gn_i18n_translate, gn_metric_observe,
//                          gn_event_emit, gn_span_event}
// ===========================================================================

/// Wire envelope for `gn_metric_observe`'s tags argument. The host
/// expects a flat string→string map.
pub type MetricTags = BTreeMap<String, String>;

impl Host {
    /// Translate a key for the supplied locale. Returns the host's
    /// translated string, or the key itself when no translation is
    /// available (the host signals that with (0, 0) which we surface
    /// as an empty string — callers can detect by comparing to key).
    pub fn i18n_translate(key: &str, locale: &str) -> HostResult<String> {
        let kb = key.as_bytes();
        let lb = locale.as_bytes();
        let packed = unsafe {
            crate::host::raw::gn_i18n_translate(
                kb.as_ptr() as u32,
                kb.len() as u32,
                lb.as_ptr() as u32,
                lb.len() as u32,
            )
        };
        let (ptr, len) = crate::host::unpack_result(packed);
        if len < 0 {
            return Err(HostError::HostStatus(len));
        }
        if len == 0 {
            // "no translation found" — fall back locally
            return Ok(String::new());
        }
        // SAFETY: host upheld alloc contract.
        let bytes = unsafe { crate::host::read_host_result(ptr, len) }?;
        Ok(String::from_utf8(bytes).map_err(|e| HostError::Marshal(std::format!("{}", e)))?)
    }

    /// Record one observation against the named metric. The tags map
    /// is serialized as a JSON object; the host enforces cardinality
    /// caps before persisting.
    pub fn metric_observe(name: &str, value: f64, tags: &MetricTags) -> HostResult<i32> {
        let nb = name.as_bytes();
        let tb = serde_json::to_vec(tags)?;
        let status = unsafe {
            crate::host::raw::gn_metric_observe(
                nb.as_ptr() as u32,
                nb.len() as u32,
                value,
                tb.as_ptr() as u32,
                tb.len() as u32,
            )
        };
        if status != 0 {
            return Err(HostError::HostStatus(status));
        }
        Ok(status)
    }

    /// Publish a structured event to the host event bus. Subscribers
    /// (other plugins, the admin UI, etc.) see it via the bus.
    pub fn event_emit<T: Serialize>(name: &str, data: &T) -> HostResult<i32> {
        let nb = name.as_bytes();
        let db = serde_json::to_vec(data)?;
        let status = unsafe {
            crate::host::raw::gn_event_emit(
                nb.as_ptr() as u32,
                nb.len() as u32,
                db.as_ptr() as u32,
                db.len() as u32,
            )
        };
        if status != 0 {
            return Err(HostError::HostStatus(status));
        }
        Ok(status)
    }

    /// Add an OpenTelemetry span event to the currently-active span.
    /// `attrs` is the standard OTel attribute bag (string keys, scalar
    /// values).
    pub fn span_event(
        name: &str,
        attrs: &BTreeMap<String, serde_json::Value>,
    ) -> HostResult<i32> {
        let nb = name.as_bytes();
        let ab = serde_json::to_vec(attrs)?;
        let status = unsafe {
            crate::host::raw::gn_span_event(
                nb.as_ptr() as u32,
                nb.len() as u32,
                ab.as_ptr() as u32,
                ab.len() as u32,
            )
        };
        if status != 0 {
            return Err(HostError::HostStatus(status));
        }
        Ok(status)
    }
}

// ===========================================================================
// Platform ABIs — env_platform.{gn_secrets_get, gn_audit_emit, gn_cron_register}
//
// PR #456 lands the host side; the SDK ships the imports ready to go.
// ===========================================================================

/// Wire envelope for `gn_audit_emit`. Mirrors the host's audit row
/// minus fields the host populates itself (timestamp, actor, plugin
/// slug).
#[derive(Debug, Clone, Serialize)]
pub struct AuditEvent<'a> {
    /// Action verb (e.g. "post.published", "user.invited").
    pub action: &'a str,
    /// Target object id, if any.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub target: Option<&'a str>,
    /// Free-form metadata.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub meta: Option<serde_json::Value>,
}

/// Wire envelope for `gn_cron_register`. Mirrors the host's CronSpec.
#[derive(Debug, Clone, Serialize)]
pub struct CronSpec<'a> {
    /// Cron expression in the standard 5-field format.
    pub schedule: &'a str,
    /// Job id from the plugin's manifest `jobs` list.
    pub job: &'a str,
    /// Optional JSON payload passed to the job each time it fires.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub payload: Option<serde_json::Value>,
}

impl Host {
    /// Read a secret from the host's encrypted store. Requires the
    /// `secrets.read` capability and the secret name to be in the
    /// plugin's `allow_secrets` manifest list.
    pub fn secrets_get(name: &str) -> HostResult<String> {
        let nb = name.as_bytes();
        let packed =
            unsafe { crate::host::raw::gn_secrets_get(nb.as_ptr() as u32, nb.len() as u32) };
        let (ptr, len) = crate::host::unpack_result(packed);
        if len < 0 {
            return Err(HostError::HostStatus(len));
        }
        if len == 0 {
            return Ok(String::new());
        }
        // SAFETY: host upheld alloc contract.
        let bytes = unsafe { crate::host::read_host_result(ptr, len) }?;
        Ok(String::from_utf8(bytes).map_err(|e| HostError::Marshal(std::format!("{}", e)))?)
    }

    /// Write an audit row. Requires the `audit.emit` capability.
    pub fn audit_emit(event: &AuditEvent<'_>) -> HostResult<i32> {
        call_i32(event, |p, l| unsafe { crate::host::raw::gn_audit_emit(p, l) })
    }

    /// Register a recurring job from a cron expression. Requires the
    /// `cron.register` capability and the job id to be declared in the
    /// plugin's manifest `jobs` list.
    pub fn cron_register(spec: &CronSpec<'_>) -> HostResult<i32> {
        call_i32(spec, |p, l| unsafe { crate::host::raw::gn_cron_register(p, l) })
    }
}
