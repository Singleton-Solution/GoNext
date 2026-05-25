package sdk

import (
	"encoding/json"
	"fmt"
)

// This file owns the typed methods that hang off the Host.* surface.
// Every method follows the same shape:
//
//	1. JSON-marshal the typed input into the wire envelope the host
//	   expects (or skip the marshal if the ABI takes raw bytes).
//	2. Call the low-level hostCall<Name> primitive (defined in
//	   host_wasm.go for the wasm32-wasi target, host_stub.go elsewhere).
//	3. Decode the host's response — either a JSON envelope or a raw
//	   byte buffer — and map it onto the typed return shape.
//
// Splitting "typed method" from "raw host call" keeps the wasmimport
// signatures small (i32/i64 only) and the test surface independent of
// TinyGo.

// ============================================================================
// HTTP
// ============================================================================

// Fetch issues a single outbound HTTP request through gn_http_fetch.
// The plugin must declare http.fetch in its manifest and list the
// target hostname under http.fetch.allow_hosts; the host rejects
// anything else with a denied / blocked status.
//
// Returns a typed HTTPResponse on the success path. On any host-
// detected failure (denied, blocked, rate-limited, upstream error)
// the returned error wraps ErrHostFailure and carries the specific
// negative status in a HostError.
func (HTTPAPI) Fetch(req HTTPRequest) (*HTTPResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("sdk: marshal http request: %w", err)
	}
	respBytes, status := hostCallHTTPFetch(body)
	if status < 0 {
		return nil, &HostError{Function: "gn_http_fetch", Status: status}
	}
	var resp HTTPResponse
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		return nil, fmt.Errorf("sdk: decode http response: %w", err)
	}
	return &resp, nil
}

// ============================================================================
// DB
// ============================================================================

// Read runs a SELECT (or WITH ...) query through gn_db_read. The host
// validates the query lexically (no DDL, no multi-statement) and
// applies the plugin's bound role before execution.
//
// Returns the rowset as a slice of column-name -> value maps. Empty
// rowsets come back as a non-nil zero-length slice.
func (DBAPI) Read(query string, args []any) ([]map[string]any, error) {
	argsBuf, err := marshalDBArgs(args)
	if err != nil {
		return nil, err
	}
	respBytes, status := hostCallDBRead([]byte(query), argsBuf)
	if status < 0 {
		return nil, &HostError{Function: "gn_db_read", Status: status}
	}
	if len(respBytes) == 0 {
		return []map[string]any{}, nil
	}
	var rows []map[string]any
	if err := json.Unmarshal(respBytes, &rows); err != nil {
		return nil, fmt.Errorf("sdk: decode db rows: %w", err)
	}
	if rows == nil {
		rows = []map[string]any{}
	}
	return rows, nil
}

// Write runs an INSERT/UPDATE/DELETE query through gn_db_write.
// Returns the number of affected rows; the host caps the return at
// math.MaxInt32 if the underlying tag claims more.
func (DBAPI) Write(query string, args []any) (int32, error) {
	argsBuf, err := marshalDBArgs(args)
	if err != nil {
		return 0, err
	}
	_, status := hostCallDBWrite([]byte(query), argsBuf)
	if status < 0 {
		return 0, &HostError{Function: "gn_db_write", Status: status}
	}
	// Successful db.write packs the affected-row count into the low
	// 32 bits with ptr=0. The status return IS the affected count
	// when non-negative; non-positive means "exactly zero rows" or
	// a sentinel.
	return status, nil
}

// marshalDBArgs encodes a []any into a JSON array, returning nil for
// nil/empty input so the host sees argsLen=0.
func marshalDBArgs(args []any) ([]byte, error) {
	if len(args) == 0 {
		return nil, nil
	}
	out, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("sdk: marshal db args: %w", err)
	}
	return out, nil
}

// ============================================================================
// KV
// ============================================================================

// Get retrieves the value for key under the plugin's namespace. Returns
// (nil, nil) when the key is not present — the absence is not an error.
func (KVAPI) Get(key string) ([]byte, error) {
	respBytes, status := hostCallKVGet([]byte(key))
	if status == kvStatusNotFound {
		return nil, nil
	}
	if status < 0 && status != int32(StatusOK) {
		return nil, &HostError{Function: "gn_kv_get", Status: status}
	}
	if len(respBytes) == 0 {
		return nil, nil
	}
	return respBytes, nil
}

// Set stores value under key in the plugin's namespace. Returns nil
// on success.
//
// The host caps individual values at 256 KiB and enforces the plugin's
// declared storage.kv.max_bytes / max_keys quotas. When the quota would
// be exceeded the host evicts the oldest keys; only an unrecoverable
// quota overflow (the new value alone is larger than max_bytes) surfaces
// as a dataQuotaExceeded error.
func (KVAPI) Set(key string, value []byte) error {
	_, status := hostCallKVSet([]byte(key), value)
	if status < 0 && status != int32(StatusOK) {
		return &HostError{Function: "gn_kv_set", Status: status}
	}
	return nil
}

// Del removes the value for key. Idempotent — deleting a missing key
// is a no-op success.
func (KVAPI) Del(key string) error {
	_, status := hostCallKVDel([]byte(key))
	if status < 0 && status != int32(StatusOK) {
		return &HostError{Function: "gn_kv_del", Status: status}
	}
	return nil
}

// Incr atomically increments the counter at key by delta. delta may
// be negative. Returns the new value.
func (KVAPI) Incr(key string, delta int64) (int32, error) {
	_, status := hostCallKVIncr([]byte(key), delta)
	if status < 0 && status != int32(StatusOK) {
		return 0, &HostError{Function: "gn_kv_incr", Status: status}
	}
	return status, nil
}

// kvStatusNotFound mirrors the host's dataResultNotFound. Spelled out
// in this file so the high-level Get can distinguish "missing" from a
// generic failure without pulling the host package's constants over.
const kvStatusNotFound int32 = -4

// ============================================================================
// Cache
// ============================================================================

// Invalidate enqueues invalidation for one or more cache tags. The
// host persists them into the cache_invalidations outbox; a worker
// drains the table into Redis pub/sub.
//
// Tags are persisted UN-prefixed; the worker namespaces them as
// plugin:<slug>:<tag> on publish. Pass them however the plugin sees
// them.
func (CacheAPI) Invalidate(tags ...string) error {
	if len(tags) == 0 {
		return nil
	}
	buf, err := json.Marshal(tags)
	if err != nil {
		return fmt.Errorf("sdk: marshal cache tags: %w", err)
	}
	_, status := hostCallCacheInvalidate(buf)
	if status < 0 && status != int32(StatusOK) {
		return &HostError{Function: "gn_cache_invalidate", Status: status}
	}
	return nil
}

// ============================================================================
// Media
// ============================================================================

// Read returns metadata + a signed URL for the media asset identified
// by id. Returns (nil, nil) for an unknown id.
func (MediaAPI) Read(id string) (*MediaAsset, error) {
	req, err := json.Marshal(struct {
		ID string `json:"id"`
	}{ID: id})
	if err != nil {
		return nil, fmt.Errorf("sdk: marshal media request: %w", err)
	}
	respBytes, status := hostCallMediaRead(req)
	if status == kvStatusNotFound {
		return nil, nil
	}
	if status < 0 {
		return nil, &HostError{Function: "gn_media_read", Status: status}
	}
	var asset MediaAsset
	if err := json.Unmarshal(respBytes, &asset); err != nil {
		return nil, fmt.Errorf("sdk: decode media asset: %w", err)
	}
	return &asset, nil
}

// ============================================================================
// Users
// ============================================================================

// Read returns the user row for id, filtered against the manifest's
// users.read field allowlist by the host. Returns (nil, nil) when no
// user with that id exists.
func (UsersAPI) Read(id string) (map[string]any, error) {
	req, err := json.Marshal(struct {
		ID string `json:"id"`
	}{ID: id})
	if err != nil {
		return nil, fmt.Errorf("sdk: marshal users request: %w", err)
	}
	respBytes, status := hostCallUsersRead(req)
	if status == kvStatusNotFound {
		return nil, nil
	}
	if status < 0 {
		return nil, &HostError{Function: "gn_users_read", Status: status}
	}
	var row map[string]any
	if err := json.Unmarshal(respBytes, &row); err != nil {
		return nil, fmt.Errorf("sdk: decode users row: %w", err)
	}
	return row, nil
}

// ============================================================================
// Secrets
// ============================================================================

// Get returns the value for one secret key the manifest declared
// under secrets.read. Returns (nil, nil) when the key is not present
// in the host's vault.
func (SecretsAPI) Get(key string) ([]byte, error) {
	respBytes, status := hostCallSecretsGet([]byte(key))
	if status == kvStatusNotFound {
		return nil, nil
	}
	if status < 0 && status != int32(StatusOK) {
		return nil, &HostError{Function: "gn_secrets_get", Status: status}
	}
	if len(respBytes) == 0 {
		return nil, nil
	}
	return respBytes, nil
}

// ============================================================================
// Audit
// ============================================================================

// Emit records one audit row tagged with the plugin slug. eventType
// is the dotted event name (e.g. "post.indexed", "user.notified").
// metadata is the structured payload — every value must be a type
// encoding/json can render (string, bool, number, nested map/slice).
//
// Returns an error if the metadata blob fails to marshal or the host
// reports a failure status. The audit ABI is best-effort host-side;
// most failures are visible to the operator via host logs.
func (AuditAPI) Emit(eventType string, metadata map[string]any) error {
	payload, err := json.Marshal(struct {
		Event    string         `json:"event"`
		Metadata map[string]any `json:"metadata,omitempty"`
	}{Event: eventType, Metadata: metadata})
	if err != nil {
		return fmt.Errorf("sdk: marshal audit payload: %w", err)
	}
	_, status := hostCallAuditEmit(payload)
	if status < 0 && status != int32(StatusOK) {
		return &HostError{Function: "gn_audit_emit", Status: status}
	}
	return nil
}

// ============================================================================
// Cron
// ============================================================================

// Register declares a cron schedule that triggers jobID on the host's
// scheduler. spec is a standard cron expression ("0 */1 * * *",
// "@hourly"). jobID must be one of the ids declared in the manifest's
// jobs[] list.
//
// Register is idempotent — calling it twice for the same (spec, jobID)
// pair is a no-op on the second call.
func (CronAPI) Register(spec, jobID string) error {
	payload, err := json.Marshal(struct {
		Spec  string `json:"spec"`
		JobID string `json:"job_id"`
	}{Spec: spec, JobID: jobID})
	if err != nil {
		return fmt.Errorf("sdk: marshal cron payload: %w", err)
	}
	_, status := hostCallCronRegister(payload)
	if status < 0 && status != int32(StatusOK) {
		return &HostError{Function: "gn_cron_register", Status: status}
	}
	return nil
}

// ============================================================================
// Metrics, Events, Spans
// ============================================================================

// Observe records one metric observation. The (slug, name, tags)
// tuple is checked against the host's cardinality dam — a noisy
// plugin emitting unbounded tag values gets its excess observations
// dropped with a `plugin.metric_cardinality_exceeded` audit warning.
//
// Tag keys and values MUST be strings; richer types should be
// stringified at the SDK boundary. The host's tag encoding is a
// minimal msgpack subset (string-keyed string-valued maps).
func (MetricAPI) Observe(name string, value float64, tags map[string]string) error {
	tagsBuf := encodeStringMap(tags)
	status := hostCallMetricObserve([]byte(name), value, tagsBuf)
	if status < 0 {
		return &HostError{Function: "gn_metric_observe", Status: status}
	}
	return nil
}

// Emit records a structured event row into the audit log at info
// severity. data is the structured payload; keys and values must be
// strings (same encoding as MetricAPI.Observe tags). Use AuditAPI.Emit
// instead when the payload needs richer types or a non-info severity.
func (EventAPI) Emit(name string, data map[string]string) error {
	dataBuf := encodeStringMap(data)
	status := hostCallEventEmit([]byte(name), dataBuf)
	if status < 0 {
		return &HostError{Function: "gn_event_emit", Status: status}
	}
	return nil
}

// AddEvent attaches a named event to the currently-active OTel span
// for this plugin. attrs is the event's attribute set; keys and values
// must be strings. When no span is active, the host logs the event at
// debug level so authors developing without an OTel pipeline still
// see breadcrumbs.
func (SpanAPI) AddEvent(name string, attrs map[string]string) error {
	attrsBuf := encodeStringMap(attrs)
	status := hostCallSpanEvent([]byte(name), attrsBuf)
	if status < 0 {
		return &HostError{Function: "gn_span_event", Status: status}
	}
	return nil
}

// ============================================================================
// I18n
// ============================================================================

// Translate returns the localised string for (key, locale). Falls back
// to the key itself when no translation exists — matching the host's
// behaviour. Plugin code can rely on Translate always returning a
// non-empty string.
func (I18nAPI) Translate(key, locale string) string {
	out, _ := hostCallI18nTranslate([]byte(key), []byte(locale))
	if len(out) == 0 {
		return key
	}
	return string(out)
}

// ============================================================================
// Log + Time
// ============================================================================

// Log emits a host-side structured log line at the given level. The
// host prefixes the entry with the plugin slug. This is a fire-and-
// forget call — the host best-effort-publishes to slog and to any
// dev-CLI log streamer.
func (LogAPI) Log(level LogLevel, message string) {
	hostCallLog(int32(level), []byte(message))
}

// Debug is shorthand for Log(LogDebug, message).
func (l LogAPI) Debug(message string) { l.Log(LogDebug, message) }

// Info is shorthand for Log(LogInfo, message).
func (l LogAPI) Info(message string) { l.Log(LogInfo, message) }

// Warn is shorthand for Log(LogWarn, message).
func (l LogAPI) Warn(message string) { l.Log(LogWarn, message) }

// Error is shorthand for Log(LogError, message).
func (l LogAPI) Error(message string) { l.Log(LogError, message) }

// NowMs returns the host's idea of current wall-clock milliseconds
// (the same value time.Now().UnixMilli() would produce host-side).
// Plugins running under WithTimeSource see whatever the host injects;
// production builds see real wall-clock.
func (TimeAPI) NowMs() int64 {
	return hostCallTimeMs()
}

// ============================================================================
// String-map encoder (matches the host's minimal msgpack subset).
// ============================================================================

// encodeStringMap encodes a map[string]string into the minimal-msgpack
// shape the host's readHostTags expects: map16 header + str16-keyed
// entries. Plugins that want richer tag types should stringify at the
// SDK boundary.
//
// We deliberately emit the always-2-byte header forms (map16, str16)
// even for small maps so the encoder is straight-line — no branching
// on count. The host's decoder accepts both fixmap and map16 forms.
//
// nil/empty input returns nil so the host sees tags_len=0 and treats
// the call as untagged.
func encodeStringMap(m map[string]string) []byte {
	if len(m) == 0 {
		return nil
	}
	out := make([]byte, 0, 32+8*len(m))
	out = append(out, 0xde, byte(len(m)>>8), byte(len(m)))
	for k, v := range m {
		out = append(out, 0xda, byte(len(k)>>8), byte(len(k)))
		out = append(out, k...)
		out = append(out, 0xda, byte(len(v)>>8), byte(len(v)))
		out = append(out, v...)
	}
	return out
}
