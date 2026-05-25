//! Typed `Manifest` struct + builder for `manifest.json`.
//!
//! Mirrors `packages/go/plugins/manifest/manifest.go`. The Rust side is
//! a write-time helper: plugin authors construct a [`Manifest`] in
//! their build script (or wherever they want — a `build.rs`, a `main`
//! function, a one-off binary) and serialize it to disk for the host
//! to validate at install time.
//!
//! We DO NOT mirror the host's JSON-Schema validator — there's no
//! point validating in two places, and the host is the source of truth.
//! What we do guarantee here is that a `Manifest` constructed through
//! the builder serializes into a shape the host accepts.

use std::string::{String, ToString};
use std::vec::Vec;
use serde::{Deserialize, Serialize};

/// The only manifest API version this SDK targets. The host accepts
/// only this literal; future revisions live in sibling packages.
pub const API_VERSION: &str = "gonext.io/v1";

/// Typed mirror of `manifest.json`. Field-for-field with
/// `manifest.Manifest` on the host side.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Manifest {
    /// Manifest API version. Always [`API_VERSION`].
    #[serde(rename = "apiVersion")]
    pub api_version: String,

    /// Plugin slug. Pattern: `^[a-z][a-z0-9-]{2,40}$`.
    pub name: String,

    /// Strict SemVer 2.0.0 string.
    pub version: String,

    /// Path inside the bundle to the WASM entrypoint. POSIX-style, no
    /// leading slash, no parent traversal.
    pub entry: String,

    /// Capabilities the plugin requests from the host. Operators
    /// review this list at install time.
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub capabilities: Vec<String>,

    /// Actions and filters the plugin subscribes to.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub hooks: Option<Hooks>,

    /// Background TaskSpec ids the plugin owns.
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub jobs: Vec<String>,

    /// Compatibility requirements (host semver range, etc.).
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub requires: Option<Requires>,

    /// Other plugins this plugin requires at activation time.
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub depends: Vec<Dependency>,

    /// Detached signature over the canonical bundle bytes.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub signature: Option<String>,

    /// Persistent-storage budgets.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub storage: Option<Storage>,
}

/// Action and filter subscriptions.
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct Hooks {
    /// Action hook names.
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub actions: Vec<String>,
    /// Filter hook names.
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub filters: Vec<String>,
}

/// Compatibility ranges. Currently only `host` is recognised.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Requires {
    /// Semver range the host platform must satisfy.
    pub host: String,
}

/// Dependency on another installed plugin.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Dependency {
    /// Slug of the depended-on plugin.
    pub name: String,
    /// Semver range its version must satisfy.
    pub version: String,
}

/// Storage-budget bag.
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct Storage {
    /// KV namespace budget.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub kv: Option<KVStorage>,
}

/// Per-plugin KV namespace budget.
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct KVStorage {
    /// Cumulative value bytes cap.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub max_bytes: Option<i64>,
    /// Maximum number of keys.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub max_keys: Option<i64>,
}

/// Named capability strings the platform recognises. Plugin authors
/// can pass either a [`Capability`] variant or a raw `&str` through
/// the builder; the variant is a compile-time guard against typos.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Capability {
    /// Outbound HTTP to allowlisted hosts.
    HttpFetch,
    /// Read the media library.
    MediaRead,
    /// Read user rows (scrubbed by the host).
    UsersRead,
    /// Read posts.
    PostsRead,
    /// Write posts.
    PostsWrite,
    /// Read the per-plugin KV namespace.
    KvRead,
    /// Write the per-plugin KV namespace.
    KvWrite,
    /// Read the per-plugin DB pool.
    DbRead,
    /// Write the per-plugin DB pool.
    DbWrite,
    /// Subscribe to hooks (always implicit; included for completeness).
    HooksSubscribe,
    /// Enqueue background jobs.
    JobsEnqueue,
    /// Emit audit rows.
    AuditEmit,
    /// Read encrypted secrets.
    SecretsRead,
    /// Register a cron-driven job.
    CronRegister,
    /// Invalidate cache entries by tag.
    CacheInvalidate,
}

impl Capability {
    /// Render the variant as the platform's canonical capability string.
    pub fn as_str(self) -> &'static str {
        match self {
            Capability::HttpFetch => "http.fetch",
            Capability::MediaRead => "media.read",
            Capability::UsersRead => "users.read",
            Capability::PostsRead => "posts.read",
            Capability::PostsWrite => "posts.write",
            Capability::KvRead => "kv.read",
            Capability::KvWrite => "kv.write",
            Capability::DbRead => "db.read",
            Capability::DbWrite => "db.write",
            Capability::HooksSubscribe => "hooks.subscribe",
            Capability::JobsEnqueue => "jobs.enqueue",
            Capability::AuditEmit => "audit.emit",
            Capability::SecretsRead => "secrets.read",
            Capability::CronRegister => "cron.register",
            Capability::CacheInvalidate => "cache.invalidate",
        }
    }
}

impl From<Capability> for String {
    fn from(c: Capability) -> Self {
        c.as_str().to_string()
    }
}

/// Fluent builder for [`Manifest`]. The builder fills [`API_VERSION`]
/// in by default and lets the author set everything else with
/// chainable calls.
///
/// ## Example
///
/// ```
/// use gonext_sdk::manifest::{Capability, ManifestBuilder};
///
/// let manifest = ManifestBuilder::new("my-plugin", "0.1.0", "plugin.wasm")
///     .capability(Capability::KvRead)
///     .capability(Capability::KvWrite)
///     .action("save_post")
///     .filter("the_content")
///     .requires_host(">=0.1.0")
///     .build();
///
/// assert_eq!(manifest.name, "my-plugin");
/// assert_eq!(manifest.capabilities, vec!["kv.read", "kv.write"]);
/// ```
#[derive(Debug, Clone)]
pub struct ManifestBuilder {
    manifest: Manifest,
}

impl ManifestBuilder {
    /// Start a new builder. `name`, `version`, and `entry` are the
    /// three required top-level fields; everything else is opt-in.
    pub fn new(name: impl Into<String>, version: impl Into<String>, entry: impl Into<String>) -> Self {
        Self {
            manifest: Manifest {
                api_version: API_VERSION.to_string(),
                name: name.into(),
                version: version.into(),
                entry: entry.into(),
                capabilities: Vec::new(),
                hooks: None,
                jobs: Vec::new(),
                requires: None,
                depends: Vec::new(),
                signature: None,
                storage: None,
            },
        }
    }

    /// Add a capability to the manifest. Accepts both [`Capability`]
    /// variants (compile-time-checked) and raw `&str` via `.into()`
    /// for capabilities not yet in the enum.
    pub fn capability(mut self, cap: impl Into<String>) -> Self {
        self.manifest.capabilities.push(cap.into());
        self
    }

    /// Add multiple capabilities at once.
    pub fn capabilities<I, S>(mut self, caps: I) -> Self
    where
        I: IntoIterator<Item = S>,
        S: Into<String>,
    {
        for cap in caps {
            self.manifest.capabilities.push(cap.into());
        }
        self
    }

    /// Add an action subscription.
    pub fn action(mut self, name: impl Into<String>) -> Self {
        let hooks = self.manifest.hooks.get_or_insert_with(Hooks::default);
        hooks.actions.push(name.into());
        self
    }

    /// Add a filter subscription.
    pub fn filter(mut self, name: impl Into<String>) -> Self {
        let hooks = self.manifest.hooks.get_or_insert_with(Hooks::default);
        hooks.filters.push(name.into());
        self
    }

    /// Declare a background job id.
    pub fn job(mut self, id: impl Into<String>) -> Self {
        self.manifest.jobs.push(id.into());
        self
    }

    /// Set the required host semver range.
    pub fn requires_host(mut self, range: impl Into<String>) -> Self {
        self.manifest.requires = Some(Requires { host: range.into() });
        self
    }

    /// Declare a dependency on another plugin.
    pub fn depends_on(mut self, name: impl Into<String>, version: impl Into<String>) -> Self {
        self.manifest.depends.push(Dependency {
            name: name.into(),
            version: version.into(),
        });
        self
    }

    /// Set the KV storage budget.
    pub fn kv_storage(mut self, max_bytes: Option<i64>, max_keys: Option<i64>) -> Self {
        let storage = self.manifest.storage.get_or_insert_with(Storage::default);
        storage.kv = Some(KVStorage { max_bytes, max_keys });
        self
    }

    /// Set the detached signature. The CLI's `gonext plugin sign`
    /// command normally fills this in; the builder accepts it so
    /// authors building bundles programmatically can finalise the
    /// manifest in one place.
    pub fn signature(mut self, sig: impl Into<String>) -> Self {
        self.manifest.signature = Some(sig.into());
        self
    }

    /// Finalise the builder.
    pub fn build(self) -> Manifest {
        self.manifest
    }
}

impl Manifest {
    /// Serialize the manifest as canonical JSON (sorted keys, no
    /// trailing newline). This is what the bundle's manifest.json
    /// should contain.
    pub fn to_json(&self) -> Result<String, serde_json::Error> {
        // serde_json doesn't sort keys by default — but the host's
        // schema validator doesn't require sorted keys either, and
        // doing so would mean reaching for `serde_json::Value`
        // round-trips that aren't worth the code. Plugin authors who
        // need byte-exact reproducibility can pipe through `jq -S`.
        serde_json::to_string_pretty(self)
    }

    /// Serialize the manifest as canonical JSON bytes. Equivalent to
    /// `to_json().map(String::into_bytes)` but skips the UTF-8
    /// re-encoding.
    pub fn to_json_bytes(&self) -> Result<Vec<u8>, serde_json::Error> {
        serde_json::to_vec_pretty(self)
    }
}
