package sdk

// Host is the top-level handle plugin authors reach through to call
// host ABIs:
//
//	resp, err := sdk.Host.HTTP.Fetch(sdk.HTTPRequest{URL: "https://api.example.com/x"})
//	err := sdk.Host.KV.Set("counter", []byte("42"))
//	rows, err := sdk.Host.DB.Read("SELECT id FROM posts WHERE published = true", nil)
//
// Each field is a typed surface over one ABI domain. The implementations
// live in host_*.go files split by domain; this file is the assembly
// point so plugin authors have a single value to chain from.
//
// The struct is a package-level singleton because every host export is
// a global wasm import — there's no concept of "two hosts" inside one
// guest module. Constructing a Host yourself is a no-op; use the
// package-level Host variable.
var Host = HostAPI{
	HTTP:    HTTPAPI{},
	DB:      DBAPI{},
	KV:      KVAPI{},
	Cache:   CacheAPI{},
	Media:   MediaAPI{},
	Users:   UsersAPI{},
	Secrets: SecretsAPI{},
	Audit:   AuditAPI{},
	Cron:    CronAPI{},
	Metric:  MetricAPI{},
	Event:   EventAPI{},
	Span:    SpanAPI{},
	I18n:    I18nAPI{},
	Log:     LogAPI{},
	Time:    TimeAPI{},
}

// HostAPI is the typed root. Plugin authors don't construct this
// directly — they reach it via the package-level Host singleton.
type HostAPI struct {
	// HTTP wraps gn_http_fetch. The plugin's manifest must declare
	// the http.fetch capability and list allowed hosts in
	// http.fetch.allow_hosts; the host SSRF-guards every call.
	HTTP HTTPAPI

	// DB wraps gn_db_read and gn_db_write. The plugin's manifest
	// must declare db.read / db.write capabilities; the host
	// enforces a SELECT/WITH allowlist on reads and an INSERT/
	// UPDATE/DELETE allowlist on writes.
	DB DBAPI

	// KV wraps gn_kv_get, gn_kv_set, gn_kv_del, gn_kv_incr. Keys
	// are namespaced per-plugin by the host; the plugin sees its
	// own bare keys.
	KV KVAPI

	// Cache wraps gn_cache_invalidate. Tags are persisted into the
	// cache_invalidations outbox; a worker drains them.
	Cache CacheAPI

	// Media wraps gn_media_read — returns metadata plus a short-TTL
	// signed URL for the asset.
	Media MediaAPI

	// Users wraps gn_users_read — returns the user row with fields
	// filtered against the manifest's users.read scope.
	Users UsersAPI

	// Secrets wraps gn_secrets_get — returns the value for one
	// secret key the manifest declared in secrets.read.
	Secrets SecretsAPI

	// Audit wraps gn_audit_emit — emits one audit row tagged with
	// the plugin slug.
	Audit AuditAPI

	// Cron wraps gn_cron_register — declares a cron schedule for
	// a job id the plugin owns.
	Cron CronAPI

	// Metric wraps gn_metric_observe — records one observation
	// against a (slug, metric, tags) tuple. Tags are
	// cardinality-bounded per plugin.
	Metric MetricAPI

	// Event wraps gn_event_emit — emits one structured event row
	// into the audit log at info severity.
	Event EventAPI

	// Span wraps gn_span_event — attaches a named event to the
	// currently-active OTel span.
	Span SpanAPI

	// I18n wraps gn_i18n_translate — returns the localised string
	// for (key, locale), or the key if no translation exists.
	I18n I18nAPI

	// Log wraps gn_log — emits a host-side structured log line.
	Log LogAPI

	// Time wraps gn_time_ms — returns the host's idea of current
	// wall-clock milliseconds.
	Time TimeAPI
}

// ============================================================================
// Wire envelopes shared across the domain wrappers.
// ============================================================================

// HTTPRequest is the input shape for Host.HTTP.Fetch. Mirrors the
// host's httpFetchRequest exactly so the SDK marshals straight into
// the wire format.
type HTTPRequest struct {
	// Method is the HTTP verb (GET, POST, PUT, PATCH, DELETE).
	// Empty means GET.
	Method string `json:"method,omitempty"`

	// URL is the full target URL. Must be https-or-http, and the
	// hostname must appear in the manifest's http.fetch.allow_hosts
	// list — the host rejects anything else.
	URL string `json:"url"`

	// Headers is the set of request headers. The host strips
	// Host, Cookie, Authorization, Content-Length, and
	// Transfer-Encoding so a plugin can't impersonate the host's
	// identity to upstreams.
	Headers map[string]string `json:"headers,omitempty"`

	// Body is the request body. Nil for verbs that don't carry one.
	Body []byte `json:"body,omitempty"`
}

// HTTPResponse is the result shape from Host.HTTP.Fetch.
type HTTPResponse struct {
	// Status is the HTTP status code. 0 on transport-level failure.
	Status int `json:"status"`

	// Headers is the response header map. Multi-value headers are
	// joined with ", " on the wire.
	Headers map[string]string `json:"headers,omitempty"`

	// Body is the response body, capped at the host's
	// MaxHTTPFetchResponseBytes (10 MiB).
	Body []byte `json:"body,omitempty"`

	// Error is set when the host detected a transport-level
	// failure (DNS, TLS, redirect loop). Empty when Status carries
	// a real upstream code, even for 4xx/5xx.
	Error string `json:"error,omitempty"`
}

// MediaAsset is the result shape from Host.Media.Read. Mirrors the
// host's MediaAsset exactly.
type MediaAsset struct {
	ID        string         `json:"id"`
	MimeType  string         `json:"mime_type,omitempty"`
	SizeBytes int64          `json:"size_bytes,omitempty"`
	SignedURL string         `json:"signed_url"`
	ExpiresAt string         `json:"expires_at,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// LogLevel mirrors the host's log levels (matched to slog).
type LogLevel int32

const (
	// LogDebug is the most verbose level.
	LogDebug LogLevel = 0

	// LogInfo is the default level.
	LogInfo LogLevel = 1

	// LogWarn is for non-fatal anomalies.
	LogWarn LogLevel = 2

	// LogError is for failures the operator should see.
	LogError LogLevel = 3
)

// ============================================================================
// Per-domain wrappers. The methods themselves are declared in this file
// for the API surface; the actual host-call plumbing is in the
// host_wasm.go / host_stub.go pair (build-tag-gated).
// ============================================================================

// HTTPAPI is the typed wrapper over gn_http_fetch.
type HTTPAPI struct{}

// DBAPI is the typed wrapper over gn_db_read / gn_db_write.
type DBAPI struct{}

// KVAPI is the typed wrapper over gn_kv_get / gn_kv_set / gn_kv_del / gn_kv_incr.
type KVAPI struct{}

// CacheAPI is the typed wrapper over gn_cache_invalidate.
type CacheAPI struct{}

// MediaAPI is the typed wrapper over gn_media_read.
type MediaAPI struct{}

// UsersAPI is the typed wrapper over gn_users_read.
type UsersAPI struct{}

// SecretsAPI is the typed wrapper over gn_secrets_get.
type SecretsAPI struct{}

// AuditAPI is the typed wrapper over gn_audit_emit.
type AuditAPI struct{}

// CronAPI is the typed wrapper over gn_cron_register.
type CronAPI struct{}

// MetricAPI is the typed wrapper over gn_metric_observe.
type MetricAPI struct{}

// EventAPI is the typed wrapper over gn_event_emit.
type EventAPI struct{}

// SpanAPI is the typed wrapper over gn_span_event.
type SpanAPI struct{}

// I18nAPI is the typed wrapper over gn_i18n_translate.
type I18nAPI struct{}

// LogAPI is the typed wrapper over gn_log.
type LogAPI struct{}

// TimeAPI is the typed wrapper over gn_time_ms.
type TimeAPI struct{}
