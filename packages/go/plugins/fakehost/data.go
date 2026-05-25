package fakehost

import (
	"fmt"
	"strings"
)

// Data ABI surface (gn_db_read, gn_db_write, gn_kv_*, gn_cache_invalidate).
//
// Each method on the Host represents one ABI entry point. Bodies look
// the same: lock, capability check, record event, mutate the in-memory
// store. The signatures mirror what the real wazero host does AFTER
// decoding the JSON payload — i.e. a plugin author who is testing the
// behaviour of their plugin's data layer can call these methods in
// place of writing a host-trace fixture.

// KVGet reads the value for key. Returns ErrNotFound if the key is
// unbound. Records an EventKVGet event with {"key": key} and (on hit)
// Result = the value bytes.
//
// Capability gating: requires the "kv" cap. If disabled via
// DisableCapability, returns ErrDenied without touching the store.
func (h *Host) KVGet(key string) ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	args := map[string]any{"key": key}
	if err := h.requireCapLocked(EventKVGet, "kv", args); err != nil {
		return nil, err
	}
	v, ok := h.kv[key]
	if !ok {
		h.recordLocked(EventKVGet, args, nil)
		return nil, fmt.Errorf("%w: %s", ErrNotFound, key)
	}
	out := make([]byte, len(v))
	copy(out, v)
	h.recordLocked(EventKVGet, args, out)
	return out, nil
}

// KVSet stores value under key. Returns ErrQuota if installing the
// new value would push total stored bytes past the configured
// kvQuotaBytes; the store is NOT mutated in that case. Records the
// attempt regardless.
//
// Capability gating: requires the "kv" cap.
func (h *Host) KVSet(key string, value []byte) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	args := map[string]any{"key": key, "value_bytes": len(value)}
	if err := h.requireCapLocked(EventKVSet, "kv", args); err != nil {
		return err
	}
	if h.kvQuotaBytes > 0 {
		delta := int64(len(value))
		if existing, ok := h.kv[key]; ok {
			delta -= int64(len(existing))
		}
		if h.kvBytesUsedLocked()+delta > h.kvQuotaBytes {
			h.recordLocked(EventKVSet, args, map[string]any{"err": "quota"})
			return fmt.Errorf("%w: would exceed %d bytes", ErrQuota, h.kvQuotaBytes)
		}
	}
	stored := make([]byte, len(value))
	copy(stored, value)
	h.kv[key] = stored
	h.recordLocked(EventKVSet, args, nil)
	return nil
}

// KVDel removes key. Returns nil whether or not key existed —
// matches the real host's idempotent delete semantics.
func (h *Host) KVDel(key string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	args := map[string]any{"key": key}
	if err := h.requireCapLocked(EventKVDel, "kv", args); err != nil {
		return err
	}
	delete(h.kv, key)
	h.recordLocked(EventKVDel, args, nil)
	return nil
}

// KVIncr atomically adds delta to the int64 stored at key. If the
// key does not exist, it is initialised to delta. If the existing
// value is not a valid int64 string, returns an error and does NOT
// mutate. Returns the new value.
//
// Capability gating: requires the "kv" cap.
func (h *Host) KVIncr(key string, delta int64) (int64, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	args := map[string]any{"key": key, "delta": delta}
	if err := h.requireCapLocked(EventKVIncr, "kv", args); err != nil {
		return 0, err
	}
	var current int64
	if existing, ok := h.kv[key]; ok {
		s := strings.TrimSpace(string(existing))
		var parsed int64
		if _, err := fmt.Sscanf(s, "%d", &parsed); err != nil {
			h.recordLocked(EventKVIncr, args, map[string]any{"err": "non-int"})
			return 0, fmt.Errorf("fakehost: KVIncr: existing value %q is not int64", s)
		}
		current = parsed
	}
	next := current + delta
	h.kv[key] = []byte(fmt.Sprintf("%d", next))
	h.recordLocked(EventKVIncr, args, next)
	return next, nil
}

// CacheInvalidate emits a cache-invalidation request for the given
// tag set. The fake host does not maintain a cache — it only records
// the request so scenarios can assert "the plugin invalidated tags
// X and Y after writing post Z".
//
// Capability gating: requires the "cache.invalidate" cap.
func (h *Host) CacheInvalidate(tags []string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	args := map[string]any{"tags": append([]string(nil), tags...)}
	if err := h.requireCapLocked(EventCacheInval, "cache.invalidate", args); err != nil {
		return err
	}
	h.recordLocked(EventCacheInval, args, nil)
	return nil
}

// DBRead simulates a read-only query through the data ABI. The fake
// host does NOT execute SQL — it returns whatever rows the scenario
// pre-seeded via SeedPost/SeedUser/SeedMedia, scoped by the relation
// argument:
//
//   - relation == "posts" — returns the seeded post fields by ID.
//   - relation == "users" — returns the seeded user fields.
//   - relation == "media" — returns the seeded media fields.
//   - anything else       — returns ErrNotFound.
//
// The query string and bindings are recorded verbatim in the event so
// authors can assert "the plugin issued a query containing X".
//
// Capability gating: requires the "db" cap.
func (h *Host) DBRead(relation, query string, args ...any) ([]map[string]any, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ev := map[string]any{
		"relation": relation,
		"query":    query,
		"args":     append([]any(nil), args...),
	}
	if err := h.requireCapLocked(EventDBRead, "db", ev); err != nil {
		return nil, err
	}
	var rows []map[string]any
	switch relation {
	case "posts":
		for _, k := range sortedInt64Keys(h.posts) {
			rows = append(rows, cloneFields(h.posts[k]))
		}
	case "users":
		for _, k := range sortedInt64Keys(h.users) {
			rows = append(rows, cloneFields(h.users[k]))
		}
	case "media":
		for _, k := range sortedInt64Keys(h.media) {
			rows = append(rows, cloneFields(h.media[k]))
		}
	default:
		h.recordLocked(EventDBRead, ev, map[string]any{"err": "unknown relation"})
		return nil, fmt.Errorf("%w: relation %s", ErrNotFound, relation)
	}
	h.recordLocked(EventDBRead, ev, rows)
	return rows, nil
}

// DBWrite simulates a row insert/update on the named relation. The
// fake host stores the row under a freshly minted ID (returned to the
// caller) and records the event. Update semantics are
// dumb-replace-by-ID: if the row contains "id", that ID is used to
// upsert; otherwise a new one is minted.
//
// Capability gating: requires the "db" cap.
func (h *Host) DBWrite(relation string, row map[string]any) (int64, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	args := map[string]any{"relation": relation, "row": cloneFields(row)}
	if err := h.requireCapLocked(EventDBWrite, "db", args); err != nil {
		return 0, err
	}
	id := int64(0)
	if raw, ok := row["id"]; ok {
		switch n := raw.(type) {
		case int:
			id = int64(n)
		case int64:
			id = n
		case float64:
			id = int64(n)
		}
	}
	if id == 0 {
		h.nextID++
		id = h.nextID
	}
	row = cloneFields(row)
	row["id"] = id
	switch relation {
	case "posts":
		h.posts[id] = row
	case "users":
		h.users[id] = row
	case "media":
		h.media[id] = row
	default:
		h.recordLocked(EventDBWrite, args, map[string]any{"err": "unknown relation"})
		return 0, fmt.Errorf("%w: relation %s", ErrNotFound, relation)
	}
	h.recordLocked(EventDBWrite, args, id)
	return id, nil
}

// sortedInt64Keys returns the int64 keys of m sorted ascending. We
// use sorted iteration so the recorded trace is stable across runs
// (Go map iteration is randomised).
func sortedInt64Keys(m map[int64]map[string]any) []int64 {
	keys := make([]int64, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// hand-rolled to avoid pulling sort.Slice; small N
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	return keys
}
