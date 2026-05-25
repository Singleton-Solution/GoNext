package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"

	"github.com/Singleton-Solution/GoNext/packages/go/audit"
	"github.com/Singleton-Solution/GoNext/packages/go/plugins/capabilities"
)

// Data ABI exports for the WASM plugin host.
//
// This file is the implementation half of issues #118 (db.read /
// db.write), #146 (kv.*), and #175 (cache.invalidate). The schema half
// lives in migrations/000030_plugin_data_abi.up.sql; the cache-outbox
// worker lives in packages/go/cache/invalidator. See
// docs/02-plugin-system.md §4 (forthcoming) for the catalog narrative.
//
// # Module layout
//
// The functions are registered in a SEPARATE host module called
// `gonext_data`. Plugins import them as:
//
//	(import "gonext_data" "gn_db_read"  (func ...))
//	(import "gonext_data" "gn_kv_set"   (func ...))
//	(import "gonext_data" "gn_cache_invalidate" (func ...))
//
// The split keeps host.go's "env" module untouched — which is
// load-bearing for two reasons: (a) concurrent ABI work
// (capabilities, jobs, observability) extends "env" in parallel, and
// (b) plugins built before this ABI landed must continue importing
// only what they declared.
//
// # Capability + audit pattern
//
// Every entry point follows the same template:
//
//   1. Resolve the calling plugin's slug from mod.Name().
//   2. Look up the slug's *PluginData binding. No binding = host
//      not configured for this plugin (e.g., test runtime). Return
//      dataResultInternal — the lifecycle Manager is responsible for
//      Bind() at activation.
//   3. Check the relevant capability via the bound Checker.
//      Denial returns dataResultDenied through the result pointer
//      (NOT a trap); plugins can recover gracefully.
//   4. Execute. On success, materialize the result through the
//      guest allocator and pack (ptr, len) into the i64 return.
//   5. Emit one audit row on WRITE operations (db.write, kv.set,
//      kv.del, kv.incr, cache.invalidate). Reads are NOT audited
//      individually because they would flood the audit log on
//      every page render; the cap-grant decision is the audited
//      surface for reads.
//
// All payloads cross the boundary as JSON — same encoding as the
// hook ABI in packages/go/plugins/abi/hooks. A future abi/v2 can
// swap encodings without changing this file's host contract.

// dataHostModuleName is the namespace wazero registers the data
// ABI functions under. Plugins import from this exact name; the
// import name MUST stay stable for the v1 ABI lifetime.
const dataHostModuleName = "gonext_data"

// Result-packing sentinels for the data ABI. We follow the same
// (high32=ptr, low32=int32) convention the hook ABI uses, so
// plugin SDKs can share decoding logic. Sentinels are negative
// int32s, never collide with a real length.
const (
	// dataResultOK is the no-body success indicator. Reserved for
	// gn_kv_del-style "operation succeeded, nothing to return".
	dataResultOK int32 = 0

	// dataResultDenied is returned when the capability check fails.
	// Plugins SHOULD treat this as a non-recoverable contract
	// violation and not retry; the host has already audited.
	dataResultDenied int32 = -1

	// dataResultInternal covers host-side failures (DB down, Redis
	// down, marshalling glitches). Plugins MAY retry with backoff.
	dataResultInternal int32 = -2

	// dataResultBadArgs is returned for guest-side mistakes: a
	// non-allowlisted query keyword, an empty key, a malformed
	// JSON payload.
	dataResultBadArgs int32 = -3

	// dataResultNotFound is the gn_kv_get-specific "key missing"
	// signal.
	dataResultNotFound int32 = -4

	// dataResultQuota signals a write was rejected because the
	// plugin's storage quota is exhausted AND eviction couldn't
	// free enough space.
	dataResultQuota int32 = -5
)

// maxDBPayloadBytes caps the JSON payload size for
// gn_db_read / gn_db_write. Same rationale as
// hooks.MaxPayloadBytes: a sanity guard, not a security boundary.
const maxDBPayloadBytes = 1 << 20 // 1 MiB

// maxKVValueBytes is the per-write value-size ceiling for
// gn_kv_set. Larger writes return dataResultBadArgs immediately;
// this is independent of the per-plugin quota (which is about
// CUMULATIVE bytes).
const maxKVValueBytes = 1 << 18 // 256 KiB

// maxKVKeyLen caps how long a kv key can be. The Redis-side prefix
// (`plugin:<slug>:`) is added on top.
const maxKVKeyLen = 256

// maxCacheTagLen caps the length of one tag in gn_cache_invalidate.
const maxCacheTagLen = 256

// maxCacheTagsPerCall is the limit on how many tags one
// gn_cache_invalidate call may carry. A runaway plugin shouldn't be
// able to fill the table with one call.
const maxCacheTagsPerCall = 64

// PluginData binds a single plugin slug to the runtime resources
// the data ABI needs: a capability Checker (cap grants), an audit
// emitter (pre-bound to the plugin slug), and policy knobs.
//
// One PluginData per active plugin. The lifecycle Manager
// constructs the binding at activation and tears it down at
// deactivation. Tests may construct one directly.
type PluginData struct {
	// Slug is the plugin slug ("gn-seo"). Used to derive the
	// Redis key prefix (`plugin:<slug>:`), the audit emitter's
	// actor metadata, and the lookup key in DataHost.bindings.
	Slug string

	// Checker enforces capability grants. nil is treated as
	// deny-all — the binding still resolves, but every entry
	// point returns dataResultDenied.
	Checker *capabilities.Checker

	// AuditEmitter is pre-bound to the plugin slug via
	// audit.Emitter.WithPlugin(slug). The data ABI uses it to
	// emit write events; nil disables audit emission (test mode).
	AuditEmitter DataAuditEmitter

	// KVQuotaBytes / KVQuotaKeys mirror the manifest's
	// storage.kv block. Zero means "unlimited"; the binder
	// rejects negative values.
	KVQuotaBytes int64
	KVQuotaKeys  int

	// DBRoleName is the Postgres role the host sets via
	// `SET LOCAL ROLE` before each db.read / db.write query.
	// Empty means "no role-switching" — useful for tests with
	// a single all-powerful test connection.
	DBRoleName string

	// DBAllowedViews is the per-binding allowlist of relation
	// names the plugin's queries may reference. nil means
	// "no host-side check beyond the role's GRANTs"; a non-nil
	// empty slice means "no relations at all" (effectively
	// disabling the DB ABI for this plugin).
	DBAllowedViews []string
}

// DataAuditEmitter is the narrow subset of audit.Emitter we need.
// Defined as an interface so tests can plug in a fake without
// constructing a real Store.
type DataAuditEmitter interface {
	Emit(ctx context.Context, eventType string, opts ...audit.EmitOption) error
}

// DataHost is the long-lived data-ABI plumbing for a Runtime. One
// per process. It owns the pgxpool.Pool and redis.Client and the
// slug-binding table.
//
// Construction:
//
//	dh := runtime.NewDataHost(pool, redisClient)
//	rt, _ := runtime.New(ctx, runtime.WithDataHost(dh), ...)
//	dh.Bind(&runtime.PluginData{Slug: "gn-seo", Checker: chk, ...})
//
// Bind is goroutine-safe; callers may rebind a plugin (e.g., after
// a manifest reload) and the new binding takes effect on the next
// host call.
type DataHost struct {
	pool   *pgxpool.Pool
	redis  *redis.Client
	logger *slog.Logger

	mu       sync.RWMutex
	bindings map[string]*PluginData
}

// NewDataHost constructs a DataHost. Both arguments are required;
// passing nil panics — a misconfigured DataHost would mean every
// db / kv / cache call traps, which is worse than a startup crash.
func NewDataHost(pool *pgxpool.Pool, rdb *redis.Client) *DataHost {
	if pool == nil {
		panic("runtime.NewDataHost: pool is required")
	}
	if rdb == nil {
		panic("runtime.NewDataHost: redis client is required")
	}
	return &DataHost{
		pool:     pool,
		redis:    rdb,
		logger:   slog.Default(),
		bindings: map[string]*PluginData{},
	}
}

// WithLogger swaps the structured logger. Optional; tests use this
// to capture host-side warnings.
func (d *DataHost) WithLogger(l *slog.Logger) *DataHost {
	if l != nil {
		d.logger = l
	}
	return d
}

// Bind registers a per-plugin binding. An empty slug returns an
// error rather than panicking so a misconfigured lifecycle path
// surfaces cleanly. Re-binding the same slug replaces the prior
// entry.
func (d *DataHost) Bind(pd *PluginData) error {
	if pd == nil {
		return errors.New("runtime.DataHost.Bind: nil PluginData")
	}
	if pd.Slug == "" {
		return errors.New("runtime.DataHost.Bind: Slug is required")
	}
	if pd.KVQuotaBytes < 0 || pd.KVQuotaKeys < 0 {
		return fmt.Errorf("runtime.DataHost.Bind: %q: negative KV quota", pd.Slug)
	}
	d.mu.Lock()
	d.bindings[pd.Slug] = pd
	d.mu.Unlock()
	return nil
}

// Unbind removes a plugin binding. Safe to call for an unknown slug
// (no-op).
func (d *DataHost) Unbind(slug string) {
	d.mu.Lock()
	delete(d.bindings, slug)
	d.mu.Unlock()
}

// lookup returns the binding for slug, or nil if none is registered.
func (d *DataHost) lookup(slug string) *PluginData {
	d.mu.RLock()
	pd := d.bindings[slug]
	d.mu.RUnlock()
	return pd
}

// WithDataHost wires the data ABI into a Runtime constructor. The
// option appends a HostModuleBuilder that instantiates the
// `gonext_data` module against the runtime. The builder closes
// over dh so per-call dispatch can reach the pool/redis/audit
// plumbing.
//
// Without WithDataHost, the runtime instantiates with no data ABI
// at all — modules importing gonext_data.* fail with a linker
// error. That's the correct behavior: the data ABI is opt-in at
// the host level so tests that don't need it can run without a
// database.
func WithDataHost(dh *DataHost) Option {
	if dh == nil {
		return func(c *runtimeConfig) {
			c.extraHosts = append(c.extraHosts, func(_ context.Context, _ wazeroRuntime) error {
				return errors.New("runtime.WithDataHost: DataHost is nil")
			})
		}
	}
	return func(c *runtimeConfig) {
		c.extraHosts = append(c.extraHosts, dh.build)
	}
}

// build is the HostModuleBuilder seam: it gets called once during
// runtime.New() to register the gonext_data module against the
// underlying wazero runtime.
func (d *DataHost) build(ctx context.Context, wRT wazeroRuntime) error {
	b := wRT.NewHostModuleBuilder(dataHostModuleName)

	// gn_db_read(query_ptr, query_len, args_ptr, args_len) -> i64
	registerDataFn(b, "gn_db_read", d.hostDBRead,
		[]api.ValueType{
			api.ValueTypeI32, api.ValueTypeI32,
			api.ValueTypeI32, api.ValueTypeI32,
		},
		[]api.ValueType{api.ValueTypeI64})

	// gn_db_write(query_ptr, query_len, args_ptr, args_len) -> i64
	registerDataFn(b, "gn_db_write", d.hostDBWrite,
		[]api.ValueType{
			api.ValueTypeI32, api.ValueTypeI32,
			api.ValueTypeI32, api.ValueTypeI32,
		},
		[]api.ValueType{api.ValueTypeI64})

	// gn_kv_get(key_ptr, key_len) -> i64
	registerDataFn(b, "gn_kv_get", d.hostKVGet,
		[]api.ValueType{api.ValueTypeI32, api.ValueTypeI32},
		[]api.ValueType{api.ValueTypeI64})

	// gn_kv_set(key_ptr, key_len, val_ptr, val_len) -> i64
	registerDataFn(b, "gn_kv_set", d.hostKVSet,
		[]api.ValueType{
			api.ValueTypeI32, api.ValueTypeI32,
			api.ValueTypeI32, api.ValueTypeI32,
		},
		[]api.ValueType{api.ValueTypeI64})

	// gn_kv_del(key_ptr, key_len) -> i64
	registerDataFn(b, "gn_kv_del", d.hostKVDel,
		[]api.ValueType{api.ValueTypeI32, api.ValueTypeI32},
		[]api.ValueType{api.ValueTypeI64})

	// gn_kv_incr(key_ptr, key_len, delta) -> i64
	registerDataFn(b, "gn_kv_incr", d.hostKVIncr,
		[]api.ValueType{
			api.ValueTypeI32, api.ValueTypeI32,
			api.ValueTypeI64,
		},
		[]api.ValueType{api.ValueTypeI64})

	// gn_cache_invalidate(tags_ptr, tags_len) -> i64
	registerDataFn(b, "gn_cache_invalidate", d.hostCacheInvalidate,
		[]api.ValueType{api.ValueTypeI32, api.ValueTypeI32},
		[]api.ValueType{api.ValueTypeI64})

	if _, err := b.Instantiate(ctx); err != nil {
		return fmt.Errorf("runtime: instantiate %q host module: %w",
			dataHostModuleName, err)
	}
	return nil
}

// registerDataFn is a small helper that wires one entry point onto
// the host module builder. The function-shape registration is
// identical for every export.
func registerDataFn(
	b wazero.HostModuleBuilder,
	name string,
	fn api.GoModuleFunc,
	params []api.ValueType,
	results []api.ValueType,
) {
	b.NewFunctionBuilder().
		WithGoModuleFunction(fn, params, results).
		Export(name)
}

// packDataResult composes the i64 return for any data ABI entry
// point. Mirrors hooks.packResult exactly so plugin SDKs can share
// decoding helpers.
func packDataResult(ptr uint32, length int32) uint64 {
	return uint64(ptr)<<32 | uint64(uint32(length))
}

// hostBindingForCall resolves the per-plugin binding for the
// calling module. Returns (nil, true) if the binding is missing.
func (d *DataHost) hostBindingForCall(mod api.Module) (*PluginData, bool) {
	pd := d.lookup(mod.Name())
	return pd, pd == nil
}

// readDataString is the data-ABI counterpart to host.go's
// readHostString. We don't reuse the env-module helper because (a)
// the data ABI's failure mode is different (return sentinel, not
// trap) and (b) duplicating 20 lines keeps the two files
// independently evolvable.
func readDataString(mod api.Module, ptr, length uint32) ([]byte, bool) {
	if length == 0 {
		return nil, true
	}
	if length > maxHostStringLen {
		return nil, false
	}
	mem := mod.Memory()
	if mem == nil {
		return nil, false
	}
	buf, ok := mem.Read(ptr, length)
	if !ok {
		return nil, false
	}
	return buf, true
}

// writeToGuest allocates `size` bytes in the guest via its
// exported gn_alloc(i32) i32 function, writes data into the
// allocation, and returns the guest-side pointer. Returns
// (0, false) if the guest has no gn_alloc or the allocation
// failed.
func (d *DataHost) writeToGuest(ctx context.Context, mod api.Module, data []byte) (uint32, bool) {
	if len(data) == 0 {
		return 0, true
	}
	alloc := mod.ExportedFunction("gn_alloc")
	if alloc == nil {
		d.logger.Warn("runtime/data: guest missing gn_alloc export",
			slog.String("plugin", mod.Name()))
		return 0, false
	}
	res, err := alloc.Call(ctx, api.EncodeU32(uint32(len(data))))
	if err != nil || len(res) != 1 {
		d.logger.Warn("runtime/data: gn_alloc call failed",
			slog.String("plugin", mod.Name()),
			slog.Any("err", err))
		return 0, false
	}
	ptr := api.DecodeU32(res[0])
	if ptr == 0 {
		return 0, false
	}
	mem := mod.Memory()
	if mem == nil || !mem.Write(ptr, data) {
		return 0, false
	}
	return ptr, true
}

// ──────────────────────────────────────────────────────────────────
// gn_db_read / gn_db_write
// ──────────────────────────────────────────────────────────────────

// dbAllowedReadKeywords is the set of leading tokens we accept for
// gn_db_read. SELECT covers projection; WITH is allowed because
// recursive / non-recursive CTEs are common in read queries.
var dbAllowedReadKeywords = map[string]bool{
	"SELECT": true,
	"WITH":   true,
}

// dbAllowedWriteKeywords is the set of leading tokens we accept for
// gn_db_write.
var dbAllowedWriteKeywords = map[string]bool{
	"INSERT": true,
	"UPDATE": true,
	"DELETE": true,
}

// dbBannedKeywords is the deny list checked across the whole query
// body. These are the DDL and meta-statement keywords no plugin
// should be running through this ABI.
var dbBannedKeywords = []string{
	"DROP", "CREATE", "ALTER", "TRUNCATE",
	"GRANT", "REVOKE", "VACUUM", "ANALYZE",
	"COPY", "EXECUTE", "PREPARE", "DEALLOCATE",
	"LISTEN", "NOTIFY", "UNLISTEN",
	"SET", "RESET", "SHOW",
}

// dbVerifyQuery applies the statement gate. Returns nil on accept,
// or an error describing the rejection.
//
// The verification is intentionally LEXICAL — no SQL parsing. A real
// parser would be more robust but pulls in a heavy dependency
// (libpg_query / pg_query_go); we trade that for a conservative
// keyword scan plus the role's GRANT enforcement. Bugs in the
// scanner default to over-rejecting, not under-rejecting.
func dbVerifyQuery(query string, allowedLeading map[string]bool) error {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return errors.New("empty query")
	}
	if strings.Contains(trimmed, ";") {
		return errors.New("query contains ';' (multi-statement not allowed)")
	}
	first := firstSQLToken(trimmed)
	if !allowedLeading[strings.ToUpper(first)] {
		return fmt.Errorf("leading keyword %q not in allowlist", first)
	}
	upper := strings.ToUpper(trimmed)
	for _, kw := range dbBannedKeywords {
		if containsWord(upper, kw) {
			return fmt.Errorf("banned keyword %q present", kw)
		}
	}
	return nil
}

// firstSQLToken returns the first contiguous run of alphabetic
// characters in s.
func firstSQLToken(s string) string {
	start := -1
	for i, r := range s {
		alpha := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		if start < 0 {
			if alpha {
				start = i
			}
			continue
		}
		if !alpha {
			return s[start:i]
		}
	}
	if start < 0 {
		return ""
	}
	return s[start:]
}

// containsWord reports whether s contains kw as a whole word
// (delimited by non-alphanumeric on both sides). Both inputs are
// expected to be already uppercased.
func containsWord(s, kw string) bool {
	idx := 0
	for idx < len(s) {
		i := strings.Index(s[idx:], kw)
		if i < 0 {
			return false
		}
		i += idx
		leftOK := i == 0 || !isWordChar(s[i-1])
		rightOK := i+len(kw) == len(s) || !isWordChar(s[i+len(kw)])
		if leftOK && rightOK {
			return true
		}
		idx = i + 1
	}
	return false
}

// isWordChar reports whether b is part of a SQL identifier word.
func isWordChar(b byte) bool {
	return (b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9') ||
		b == '_'
}

// dbVerifyRelations checks that every quoted/unquoted relation
// reference in the query appears in allowed. Returns nil on accept,
// or an error naming the offending reference. If allowed is nil
// (binding didn't supply a view list), the check is skipped.
func dbVerifyRelations(query string, allowed []string) error {
	if allowed == nil {
		return nil
	}
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, a := range allowed {
		allowedSet[strings.ToLower(a)] = struct{}{}
	}
	upper := strings.ToUpper(query)
	keywords := []string{"FROM", "JOIN", "INTO", "UPDATE"}
	for _, kw := range keywords {
		idx := 0
		for idx < len(upper) {
			i := strings.Index(upper[idx:], kw)
			if i < 0 {
				break
			}
			i += idx
			if i > 0 && isWordChar(upper[i-1]) {
				idx = i + 1
				continue
			}
			j := i + len(kw)
			for j < len(query) && (query[j] == ' ' || query[j] == '\t' || query[j] == '\n') {
				j++
			}
			rel, end := readRelationToken(query, j)
			if rel != "" {
				if _, ok := allowedSet[strings.ToLower(rel)]; !ok {
					return fmt.Errorf("relation %q not in allowlist", rel)
				}
			}
			idx = end
			if idx <= i {
				idx = i + 1
			}
		}
	}
	return nil
}

// readRelationToken reads one possibly-quoted SQL identifier
// starting at position `start` in s.
func readRelationToken(s string, start int) (string, int) {
	if start >= len(s) {
		return "", start
	}
	if s[start] == '"' {
		end := strings.IndexByte(s[start+1:], '"')
		if end < 0 {
			return "", len(s)
		}
		return s[start+1 : start+1+end], start + end + 2
	}
	i := start
	for i < len(s) && (isWordChar(s[i]) || s[i] == '.') {
		i++
	}
	return s[start:i], i
}

// hostDBRead implements gonext_data.gn_db_read. Wire-format:
//
//	params: query_ptr, query_len, args_ptr, args_len
//	result: i64 (ptr<<32 | len)  — JSON rowset on success
//	                                (0, sentinel) on failure
func (d *DataHost) hostDBRead(ctx context.Context, mod api.Module, stack []uint64) {
	queryPtr := api.DecodeU32(stack[0])
	queryLen := api.DecodeU32(stack[1])
	argsPtr := api.DecodeU32(stack[2])
	argsLen := api.DecodeU32(stack[3])

	pd, missing := d.hostBindingForCall(mod)
	if missing {
		stack[0] = packDataResult(0, dataResultInternal)
		return
	}
	if pd.Checker == nil || pd.Checker.MustAllow(ctx, "db.read") != nil {
		stack[0] = packDataResult(0, dataResultDenied)
		return
	}

	query, ok := readDataString(mod, queryPtr, queryLen)
	if !ok || len(query) == 0 {
		stack[0] = packDataResult(0, dataResultBadArgs)
		return
	}
	if uint32(len(query))+argsLen > maxDBPayloadBytes {
		stack[0] = packDataResult(0, dataResultBadArgs)
		return
	}
	if err := dbVerifyQuery(string(query), dbAllowedReadKeywords); err != nil {
		d.logger.Warn("runtime/db: gn_db_read rejected query",
			slog.String("plugin", pd.Slug), slog.String("err", err.Error()))
		stack[0] = packDataResult(0, dataResultBadArgs)
		return
	}
	if err := dbVerifyRelations(string(query), pd.DBAllowedViews); err != nil {
		d.logger.Warn("runtime/db: gn_db_read rejected relation",
			slog.String("plugin", pd.Slug), slog.String("err", err.Error()))
		stack[0] = packDataResult(0, dataResultBadArgs)
		return
	}

	args, parsed := parseDBArgs(mod, argsPtr, argsLen)
	if !parsed {
		stack[0] = packDataResult(0, dataResultBadArgs)
		return
	}

	rows, err := d.executeRead(ctx, pd, string(query), args)
	if err != nil {
		d.logger.Warn("runtime/db: gn_db_read exec failed",
			slog.String("plugin", pd.Slug), slog.Any("err", err))
		stack[0] = packDataResult(0, dataResultInternal)
		return
	}

	encoded, err := json.Marshal(rows)
	if err != nil {
		stack[0] = packDataResult(0, dataResultInternal)
		return
	}
	ptr, ok := d.writeToGuest(ctx, mod, encoded)
	if !ok {
		stack[0] = packDataResult(0, dataResultInternal)
		return
	}
	stack[0] = packDataResult(ptr, int32(len(encoded)))
}

// hostDBWrite implements gonext_data.gn_db_write.
//
//	params: query_ptr, query_len, args_ptr, args_len
//	result: i64 (0 << 32 | affected_rows)  — count on success
//	                                          sentinel on failure
func (d *DataHost) hostDBWrite(ctx context.Context, mod api.Module, stack []uint64) {
	queryPtr := api.DecodeU32(stack[0])
	queryLen := api.DecodeU32(stack[1])
	argsPtr := api.DecodeU32(stack[2])
	argsLen := api.DecodeU32(stack[3])

	pd, missing := d.hostBindingForCall(mod)
	if missing {
		stack[0] = packDataResult(0, dataResultInternal)
		return
	}
	if pd.Checker == nil || pd.Checker.MustAllow(ctx, "db.write") != nil {
		stack[0] = packDataResult(0, dataResultDenied)
		return
	}

	query, ok := readDataString(mod, queryPtr, queryLen)
	if !ok || len(query) == 0 {
		stack[0] = packDataResult(0, dataResultBadArgs)
		return
	}
	if uint32(len(query))+argsLen > maxDBPayloadBytes {
		stack[0] = packDataResult(0, dataResultBadArgs)
		return
	}
	if err := dbVerifyQuery(string(query), dbAllowedWriteKeywords); err != nil {
		d.logger.Warn("runtime/db: gn_db_write rejected query",
			slog.String("plugin", pd.Slug), slog.String("err", err.Error()))
		stack[0] = packDataResult(0, dataResultBadArgs)
		return
	}
	if err := dbVerifyRelations(string(query), pd.DBAllowedViews); err != nil {
		stack[0] = packDataResult(0, dataResultBadArgs)
		return
	}

	args, parsed := parseDBArgs(mod, argsPtr, argsLen)
	if !parsed {
		stack[0] = packDataResult(0, dataResultBadArgs)
		return
	}

	affected, err := d.executeWrite(ctx, pd, string(query), args)
	if err != nil {
		d.logger.Warn("runtime/db: gn_db_write exec failed",
			slog.String("plugin", pd.Slug), slog.Any("err", err))
		stack[0] = packDataResult(0, dataResultInternal)
		return
	}

	d.emitAudit(ctx, pd, "plugin.db.write", map[string]any{
		"plugin":   pd.Slug,
		"affected": affected,
	})

	if affected > int64(int32(0x7fffffff)) {
		affected = int64(int32(0x7fffffff))
	}
	stack[0] = packDataResult(0, int32(affected))
}

// parseDBArgs decodes the args buffer into a slice of any[].
// argsLen == 0 yields a nil slice; otherwise the buffer is parsed
// as a JSON array.
func parseDBArgs(mod api.Module, ptr, length uint32) ([]any, bool) {
	if length == 0 {
		return nil, true
	}
	buf, ok := readDataString(mod, ptr, length)
	if !ok {
		return nil, false
	}
	var args []any
	if err := json.Unmarshal(buf, &args); err != nil {
		return nil, false
	}
	return args, true
}

// executeRead runs the verified SELECT under the plugin's role and
// returns the result rows as a slice of column-named maps.
//
// We use BEGIN/COMMIT (rather than a single-statement
// pool.QueryRow) so SET LOCAL ROLE can take effect — it scopes to
// the transaction and resets on COMMIT/ROLLBACK.
func (d *DataHost) executeRead(ctx context.Context, pd *PluginData, query string, args []any) ([]map[string]any, error) {
	tx, err := d.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if pd.DBRoleName != "" {
		if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL ROLE %q", pd.DBRoleName)); err != nil {
			return nil, fmt.Errorf("set role: %w", err)
		}
	}

	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	out := make([]map[string]any, 0, 8)
	fields := rows.FieldDescriptions()
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		row := make(map[string]any, len(fields))
		for i, f := range fields {
			row[string(f.Name)] = vals[i]
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows: %w", err)
	}
	return out, tx.Commit(ctx)
}

// executeWrite runs the verified INSERT/UPDATE/DELETE under the
// plugin's role and returns the number of affected rows.
func (d *DataHost) executeWrite(ctx context.Context, pd *PluginData, query string, args []any) (int64, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if pd.DBRoleName != "" {
		if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL ROLE %q", pd.DBRoleName)); err != nil {
			return 0, fmt.Errorf("set role: %w", err)
		}
	}

	tag, err := tx.Exec(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("exec: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ──────────────────────────────────────────────────────────────────
// gn_kv_*
// ──────────────────────────────────────────────────────────────────

// kvRedisKey returns the namespaced Redis key the host should read
// or write. Plugins never see this string — they pass their bare
// key and the host re-prefixes here.
func kvRedisKey(slug, key string) string {
	return "plugin:" + slug + ":" + key
}

// hostKVGet implements gonext_data.gn_kv_get.
func (d *DataHost) hostKVGet(ctx context.Context, mod api.Module, stack []uint64) {
	keyPtr := api.DecodeU32(stack[0])
	keyLen := api.DecodeU32(stack[1])

	pd, missing := d.hostBindingForCall(mod)
	if missing {
		stack[0] = packDataResult(0, dataResultInternal)
		return
	}
	if pd.Checker == nil || pd.Checker.MustAllow(ctx, "kv.read") != nil {
		stack[0] = packDataResult(0, dataResultDenied)
		return
	}

	keyBytes, ok := readDataString(mod, keyPtr, keyLen)
	if !ok || len(keyBytes) == 0 || len(keyBytes) > maxKVKeyLen {
		stack[0] = packDataResult(0, dataResultBadArgs)
		return
	}

	val, err := d.redis.Get(ctx, kvRedisKey(pd.Slug, string(keyBytes))).Bytes()
	if errors.Is(err, redis.Nil) {
		stack[0] = packDataResult(0, dataResultNotFound)
		return
	}
	if err != nil {
		stack[0] = packDataResult(0, dataResultInternal)
		return
	}
	if len(val) == 0 {
		stack[0] = packDataResult(0, dataResultOK)
		return
	}
	ptr, ok := d.writeToGuest(ctx, mod, val)
	if !ok {
		stack[0] = packDataResult(0, dataResultInternal)
		return
	}
	stack[0] = packDataResult(ptr, int32(len(val)))
}

// hostKVSet implements gonext_data.gn_kv_set. The quota path:
//
//   1. Read the plugin's quota counters with FOR UPDATE.
//   2. If the write would exceed max_bytes or max_keys, evict the
//      oldest keys until the new write fits or there's nothing
//      left to evict.
//   3. If eviction couldn't free enough room (the value itself is
//      bigger than max_bytes), return dataResultQuota.
//   4. SET the Redis key, update the index + quota counters, commit.
func (d *DataHost) hostKVSet(ctx context.Context, mod api.Module, stack []uint64) {
	keyPtr := api.DecodeU32(stack[0])
	keyLen := api.DecodeU32(stack[1])
	valPtr := api.DecodeU32(stack[2])
	valLen := api.DecodeU32(stack[3])

	pd, missing := d.hostBindingForCall(mod)
	if missing {
		stack[0] = packDataResult(0, dataResultInternal)
		return
	}
	if pd.Checker == nil || pd.Checker.MustAllow(ctx, "kv.write") != nil {
		stack[0] = packDataResult(0, dataResultDenied)
		return
	}

	keyBytes, ok := readDataString(mod, keyPtr, keyLen)
	if !ok || len(keyBytes) == 0 || len(keyBytes) > maxKVKeyLen {
		stack[0] = packDataResult(0, dataResultBadArgs)
		return
	}
	if valLen > maxKVValueBytes {
		stack[0] = packDataResult(0, dataResultBadArgs)
		return
	}
	val, ok := readDataString(mod, valPtr, valLen)
	if !ok {
		stack[0] = packDataResult(0, dataResultBadArgs)
		return
	}
	key := string(keyBytes)

	if err := d.kvWriteWithQuota(ctx, pd, key, val); err != nil {
		if errors.Is(err, errKVQuotaExceeded) {
			stack[0] = packDataResult(0, dataResultQuota)
			return
		}
		d.logger.Warn("runtime/kv: set failed",
			slog.String("plugin", pd.Slug), slog.Any("err", err))
		stack[0] = packDataResult(0, dataResultInternal)
		return
	}

	d.emitAudit(ctx, pd, "plugin.kv.set", map[string]any{
		"plugin": pd.Slug,
		"key":    key,
		"size":   len(val),
	})

	stack[0] = packDataResult(0, dataResultOK)
}

// errKVQuotaExceeded is the sentinel returned by kvWriteWithQuota
// when even after evicting every existing key, the new write
// doesn't fit.
var errKVQuotaExceeded = errors.New("kv: quota exceeded")

// kvWriteWithQuota is the meaty version of the quota-aware write.
func (d *DataHost) kvWriteWithQuota(ctx context.Context, pd *PluginData, key string, val []byte) error {
	// Ensure the quota row exists; first-write-for-plugin upserts
	// it. We do this lazily rather than at Bind so a quota change
	// between activation and first write picks up.
	if _, err := d.pool.Exec(ctx, `
		INSERT INTO plugin_kv_quotas (plugin_slug, max_bytes, max_keys)
		VALUES ($1, NULLIF($2, 0), NULLIF($3, 0))
		ON CONFLICT (plugin_slug) DO UPDATE SET
		    max_bytes  = EXCLUDED.max_bytes,
		    max_keys   = EXCLUDED.max_keys,
		    updated_at = now()`,
		pd.Slug, pd.KVQuotaBytes, pd.KVQuotaKeys); err != nil {
		return fmt.Errorf("upsert quota row: %w", err)
	}

	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		maxBytes  *int64
		maxKeys   *int
		usedBytes int64
		usedKeys  int
	)
	if err := tx.QueryRow(ctx, `
		SELECT max_bytes, max_keys, used_bytes, used_keys
		FROM plugin_kv_quotas
		WHERE plugin_slug = $1
		FOR UPDATE`,
		pd.Slug).Scan(&maxBytes, &maxKeys, &usedBytes, &usedKeys); err != nil {
		return fmt.Errorf("lock quota: %w", err)
	}

	var existingSize int
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(size_bytes, 0)
		FROM plugin_kv_index
		WHERE plugin_slug = $1 AND key = $2`,
		pd.Slug, key).Scan(&existingSize); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("read existing size: %w", err)
	}

	deltaBytes := int64(len(val) - existingSize)
	deltaKeys := 1
	if existingSize > 0 {
		deltaKeys = 0
	}

	// Hard rejection: the value itself is bigger than the byte cap.
	if maxBytes != nil && *maxBytes > 0 && int64(len(val)) > *maxBytes {
		return errKVQuotaExceeded
	}

	// Soft eviction loop.
	projBytes := usedBytes + deltaBytes
	projKeys := usedKeys + deltaKeys
	for (maxBytes != nil && *maxBytes > 0 && projBytes > *maxBytes) ||
		(maxKeys != nil && *maxKeys > 0 && projKeys > *maxKeys) {

		var oldestKey string
		var oldestSize int
		err := tx.QueryRow(ctx, `
			SELECT key, size_bytes FROM plugin_kv_index
			WHERE plugin_slug = $1 AND key <> $2
			ORDER BY created_at ASC
			LIMIT 1
			FOR UPDATE SKIP LOCKED`,
			pd.Slug, key).Scan(&oldestKey, &oldestSize)
		if errors.Is(err, pgx.ErrNoRows) {
			return errKVQuotaExceeded
		}
		if err != nil {
			return fmt.Errorf("find eviction candidate: %w", err)
		}

		if _, err := tx.Exec(ctx, `
			DELETE FROM plugin_kv_index
			WHERE plugin_slug = $1 AND key = $2`,
			pd.Slug, oldestKey); err != nil {
			return fmt.Errorf("delete eviction row: %w", err)
		}
		if err := d.redis.Del(ctx, kvRedisKey(pd.Slug, oldestKey)).Err(); err != nil {
			d.logger.Warn("runtime/kv: evict-side Redis del failed",
				slog.String("plugin", pd.Slug),
				slog.String("key", oldestKey),
				slog.Any("err", err))
		}
		projBytes -= int64(oldestSize)
		projKeys--
	}

	// Commit the Redis write BEFORE the index/quota update so a
	// crash between the two leaves a recoverable inconsistency.
	if err := d.redis.Set(ctx, kvRedisKey(pd.Slug, key), val, 0).Err(); err != nil {
		return fmt.Errorf("redis set: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO plugin_kv_index (plugin_slug, key, size_bytes)
		VALUES ($1, $2, $3)
		ON CONFLICT (plugin_slug, key) DO UPDATE SET
		    size_bytes = EXCLUDED.size_bytes,
		    created_at = plugin_kv_index.created_at`,
		pd.Slug, key, len(val)); err != nil {
		return fmt.Errorf("upsert index: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE plugin_kv_quotas
		SET used_bytes = $1, used_keys = $2, updated_at = now()
		WHERE plugin_slug = $3`,
		projBytes, projKeys, pd.Slug); err != nil {
		return fmt.Errorf("update quota: %w", err)
	}

	return tx.Commit(ctx)
}

// hostKVDel implements gonext_data.gn_kv_del. Idempotent.
func (d *DataHost) hostKVDel(ctx context.Context, mod api.Module, stack []uint64) {
	keyPtr := api.DecodeU32(stack[0])
	keyLen := api.DecodeU32(stack[1])

	pd, missing := d.hostBindingForCall(mod)
	if missing {
		stack[0] = packDataResult(0, dataResultInternal)
		return
	}
	if pd.Checker == nil || pd.Checker.MustAllow(ctx, "kv.write") != nil {
		stack[0] = packDataResult(0, dataResultDenied)
		return
	}

	keyBytes, ok := readDataString(mod, keyPtr, keyLen)
	if !ok || len(keyBytes) == 0 || len(keyBytes) > maxKVKeyLen {
		stack[0] = packDataResult(0, dataResultBadArgs)
		return
	}
	key := string(keyBytes)

	tx, err := d.pool.Begin(ctx)
	if err != nil {
		stack[0] = packDataResult(0, dataResultInternal)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var size int
	if err := tx.QueryRow(ctx, `
		DELETE FROM plugin_kv_index
		WHERE plugin_slug = $1 AND key = $2
		RETURNING size_bytes`,
		pd.Slug, key).Scan(&size); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			_ = tx.Commit(ctx)
			_ = d.redis.Del(ctx, kvRedisKey(pd.Slug, key)).Err()
			stack[0] = packDataResult(0, dataResultOK)
			return
		}
		stack[0] = packDataResult(0, dataResultInternal)
		return
	}

	if _, err := tx.Exec(ctx, `
		UPDATE plugin_kv_quotas
		SET used_bytes = GREATEST(0, used_bytes - $1),
		    used_keys  = GREATEST(0, used_keys  - 1),
		    updated_at = now()
		WHERE plugin_slug = $2`,
		size, pd.Slug); err != nil {
		stack[0] = packDataResult(0, dataResultInternal)
		return
	}

	if err := tx.Commit(ctx); err != nil {
		stack[0] = packDataResult(0, dataResultInternal)
		return
	}
	_ = d.redis.Del(ctx, kvRedisKey(pd.Slug, key)).Err()

	d.emitAudit(ctx, pd, "plugin.kv.del", map[string]any{
		"plugin": pd.Slug,
		"key":    key,
	})

	stack[0] = packDataResult(0, dataResultOK)
}

// hostKVIncr implements gonext_data.gn_kv_incr. Counter values are
// not subject to the byte quota (they're small ints), but they DO
// count toward the key quota. A first INCR on a new key creates a
// quota row entry; subsequent INCRs leave it.
func (d *DataHost) hostKVIncr(ctx context.Context, mod api.Module, stack []uint64) {
	keyPtr := api.DecodeU32(stack[0])
	keyLen := api.DecodeU32(stack[1])
	// Wazero stores i64 directly in the stack slot — there's no
	// dedicated DecodeI64. A simple cast from uint64 preserves the
	// two's-complement bit pattern.
	delta := int64(stack[2])

	pd, missing := d.hostBindingForCall(mod)
	if missing {
		stack[0] = packDataResult(0, dataResultInternal)
		return
	}
	if pd.Checker == nil || pd.Checker.MustAllow(ctx, "kv.write") != nil {
		stack[0] = packDataResult(0, dataResultDenied)
		return
	}

	keyBytes, ok := readDataString(mod, keyPtr, keyLen)
	if !ok || len(keyBytes) == 0 || len(keyBytes) > maxKVKeyLen {
		stack[0] = packDataResult(0, dataResultBadArgs)
		return
	}
	key := string(keyBytes)

	newVal, err := d.redis.IncrBy(ctx, kvRedisKey(pd.Slug, key), delta).Result()
	if err != nil {
		stack[0] = packDataResult(0, dataResultInternal)
		return
	}
	if newVal > int64(int32(0x7fffffff)) || newVal < int64(int32(-0x80000000)) {
		_, _ = d.redis.IncrBy(ctx, kvRedisKey(pd.Slug, key), -delta).Result()
		stack[0] = packDataResult(0, dataResultInternal)
		return
	}

	// First-time INCR creates an index row so the counter still
	// counts toward max_keys.
	if _, err := d.pool.Exec(ctx, `
		INSERT INTO plugin_kv_quotas (plugin_slug)
		VALUES ($1)
		ON CONFLICT (plugin_slug) DO NOTHING`, pd.Slug); err == nil {
		_, _ = d.pool.Exec(ctx, `
			INSERT INTO plugin_kv_index (plugin_slug, key, size_bytes)
			VALUES ($1, $2, 8)
			ON CONFLICT (plugin_slug, key) DO NOTHING`, pd.Slug, key)
	}

	d.emitAudit(ctx, pd, "plugin.kv.incr", map[string]any{
		"plugin": pd.Slug,
		"key":    key,
		"delta":  delta,
	})

	stack[0] = packDataResult(0, int32(newVal))
}

// ──────────────────────────────────────────────────────────────────
// gn_cache_invalidate
// ──────────────────────────────────────────────────────────────────

// hostCacheInvalidate implements gonext_data.gn_cache_invalidate.
// Inserts one row per tag into the cache_invalidations outbox; a
// worker (packages/go/cache/invalidator) drains the table into
// Redis pub/sub.
//
// Tags are persisted UN-prefixed; the worker adds the
// `plugin:<slug>:` prefix when publishing.
func (d *DataHost) hostCacheInvalidate(ctx context.Context, mod api.Module, stack []uint64) {
	tagsPtr := api.DecodeU32(stack[0])
	tagsLen := api.DecodeU32(stack[1])

	pd, missing := d.hostBindingForCall(mod)
	if missing {
		stack[0] = packDataResult(0, dataResultInternal)
		return
	}
	if pd.Checker == nil || pd.Checker.MustAllow(ctx, "cache.invalidate") != nil {
		stack[0] = packDataResult(0, dataResultDenied)
		return
	}

	tagsBuf, ok := readDataString(mod, tagsPtr, tagsLen)
	if !ok || len(tagsBuf) == 0 {
		stack[0] = packDataResult(0, dataResultBadArgs)
		return
	}
	var tags []string
	if err := json.Unmarshal(tagsBuf, &tags); err != nil {
		stack[0] = packDataResult(0, dataResultBadArgs)
		return
	}
	if len(tags) == 0 || len(tags) > maxCacheTagsPerCall {
		stack[0] = packDataResult(0, dataResultBadArgs)
		return
	}
	for _, t := range tags {
		if t == "" || len(t) > maxCacheTagLen {
			stack[0] = packDataResult(0, dataResultBadArgs)
			return
		}
	}

	tx, err := d.pool.Begin(ctx)
	if err != nil {
		stack[0] = packDataResult(0, dataResultInternal)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	batch := &pgx.Batch{}
	for _, t := range tags {
		batch.Queue(`INSERT INTO cache_invalidations (plugin_slug, tag) VALUES ($1, $2)`, pd.Slug, t)
	}
	br := tx.SendBatch(ctx, batch)
	for range tags {
		if _, err := br.Exec(); err != nil {
			_ = br.Close()
			stack[0] = packDataResult(0, dataResultInternal)
			return
		}
	}
	if err := br.Close(); err != nil {
		stack[0] = packDataResult(0, dataResultInternal)
		return
	}

	if err := tx.Commit(ctx); err != nil {
		stack[0] = packDataResult(0, dataResultInternal)
		return
	}

	d.emitAudit(ctx, pd, "plugin.cache.invalidate", map[string]any{
		"plugin": pd.Slug,
		"tags":   tags,
	})

	stack[0] = packDataResult(0, dataResultOK)
}

// ──────────────────────────────────────────────────────────────────
// audit helper
// ──────────────────────────────────────────────────────────────────

// emitAudit writes one audit row for a data-ABI write. We swallow
// emission errors: the audit row is best-effort, and a missing
// emitter shouldn't fail the user's gn_kv_set call.
func (d *DataHost) emitAudit(ctx context.Context, pd *PluginData, event string, md map[string]any) {
	if pd.AuditEmitter == nil {
		return
	}
	_ = pd.AuditEmitter.Emit(ctx, event,
		audit.WithSeverity(audit.SeverityInfo),
		audit.WithMetadata(md),
		audit.WithTarget("plugin", pd.Slug),
	)
}

// Ensure the data-ABI host functions satisfy api.GoModuleFunc —
// same build-time guard host.go uses. Catches a wazero signature
// change at compile time.
var (
	_ api.GoModuleFunc = (*DataHost)(nil).hostDBRead
	_ api.GoModuleFunc = (*DataHost)(nil).hostDBWrite
	_ api.GoModuleFunc = (*DataHost)(nil).hostKVGet
	_ api.GoModuleFunc = (*DataHost)(nil).hostKVSet
	_ api.GoModuleFunc = (*DataHost)(nil).hostKVDel
	_ api.GoModuleFunc = (*DataHost)(nil).hostKVIncr
	_ api.GoModuleFunc = (*DataHost)(nil).hostCacheInvalidate
)
