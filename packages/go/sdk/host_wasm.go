//go:build (wasm || tinygo.wasm) && !sdk_disable_wasm

// host_wasm.go is the real host-call layer compiled into a TinyGo
// wasm32-wasi build. It uses //go:wasmimport to bind each host export
// to a Go function whose calling convention is i32/i64/f64 only —
// pointers are passed as uint32 offsets into linear memory and
// lengths as uint32 byte counts.
//
// The shape we use is the same the host advertises:
//
//	gn_http_fetch(req_ptr, req_len) -> i64
//	gn_db_read(query_ptr, query_len, args_ptr, args_len) -> i64
//	gn_kv_get(key_ptr, key_len) -> i64
//	...
//
// Each returned i64 packs (ptr<<32 | len). A negative length signals
// a typed sentinel; a non-negative length means the host wrote a
// result envelope at ptr that the SDK reads back via unsafe.Slice and
// frees via gn_free.
//
// For the metric / event / span exports the host returns a plain i32
// status — no body — so we use a simpler wrapper.

package sdk

import (
	"sync"
	"unsafe"
)

// ============================================================================
// Allocator.
//
// We export gn_alloc / gn_free to the host. Strategy: keep a slice of
// Go byte slices alive; gn_alloc grows the slice and returns the
// address of its first byte. gn_free is a no-op — under this plugin's
// usage pattern the per-call data is bounded by MaxPayloadBytes
// (1 MiB) and the host calls gn_free on success but we deliberately
// retain the buffer so a TinyGo GC sweep doesn't reclaim it
// concurrently with a host-side read.
//
// A real bump-arena allocator would be cheaper but the simpler
// strategy is correct and shippable. Operators with strict memory
// budgets can replace the allocator by linking against a custom
// build that defines its own gn_alloc / gn_free with the
// sdk_disable_wasm build tag and a sibling implementation file.
// ============================================================================

var (
	allocMu   sync.Mutex
	allocBufs [][]byte
)

//go:wasmexport gn_alloc
func gn_alloc(size uint32) uint32 {
	return guestAlloc(size)
}

//go:wasmexport gn_free
func gn_free(ptr uint32, size uint32) {
	guestFree(ptr, size)
}

func guestAlloc(size uint32) uint32 {
	if size == 0 {
		// 0-byte allocations are legal but pointless; return a
		// stable non-zero pointer so the host doesn't mistake the
		// result for OOM. The pointer doesn't need to be valid
		// memory — the host only reads zero bytes through it.
		return 1
	}
	allocMu.Lock()
	defer allocMu.Unlock()
	buf := make([]byte, size)
	allocBufs = append(allocBufs, buf)
	return uint32(uintptr(unsafe.Pointer(&buf[0])))
}

func guestFree(_ uint32, _ uint32) {
	// no-op; see the allocator strategy comment above.
}

// readGuest returns a byte slice that aliases the wasm linear memory
// at (ptr, length). The slice MUST NOT outlive the host call that
// passed the pointer in — TinyGo may relocate buffers across GC, and
// the host re-uses the buffer space for the next call.
func readGuest(ptr uint32, length uint32) []byte {
	if length == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(uintptr(ptr))), int(length))
}

// writeAlloc allocates a fresh guest buffer via guestAlloc and copies
// b into it. Returns the address; 0 on allocation failure.
func writeAlloc(b []byte) uint32 {
	if len(b) == 0 {
		return 0
	}
	ptr := guestAlloc(uint32(len(b)))
	if ptr == 0 {
		return 0
	}
	dst := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(ptr))), len(b))
	copy(dst, b)
	return ptr
}

// unpackHostResult is the i64-decoder counterpart to PackResult. The
// host returns (ptr<<32 | int32_len). A negative length is a status
// sentinel and ptr is 0; a non-negative length means the host wrote
// a real buffer at ptr.
func unpackHostResult(packed uint64) (data []byte, status int32) {
	ptr := uint32(packed >> 32)
	length := int32(packed & 0xFFFFFFFF)
	if length < 0 {
		return nil, length
	}
	if ptr == 0 || length == 0 {
		return nil, 0
	}
	// Copy out: the host's writeGuestPayload allocated this via our
	// gn_alloc, so the bytes live in our linear memory and survive
	// past the host call. We still copy to keep the SDK contract
	// uniform with the no-body path.
	src := readGuest(ptr, uint32(length))
	out := make([]byte, length)
	copy(out, src)
	return out, 0
}

// ============================================================================
// Host imports.
//
// Module names:
//   env         — gn_log, gn_panic, gn_time_ms, gn_i18n_translate,
//                 gn_metric_observe, gn_event_emit, gn_span_event,
//                 gn_audit_emit, gn_cron_register, gn_secrets_get.
//   env_net     — gn_http_fetch, gn_media_read, gn_users_read.
//   gonext_data — gn_db_read, gn_db_write, gn_kv_*, gn_cache_invalidate.
//
// The split matches the host's actual instantiation: env is the
// baseline host module; env_net was added in PR #454 (network ABI);
// gonext_data was added in PR #455 (data ABI).
// ============================================================================

//go:wasmimport env_net gn_http_fetch
func envNetHTTPFetch(reqPtr, reqLen uint32) uint64

//go:wasmimport env_net gn_media_read
func envNetMediaRead(reqPtr, reqLen uint32) uint64

//go:wasmimport env_net gn_users_read
func envNetUsersRead(reqPtr, reqLen uint32) uint64

//go:wasmimport gonext_data gn_db_read
func gonextDataDBRead(queryPtr, queryLen, argsPtr, argsLen uint32) uint64

//go:wasmimport gonext_data gn_db_write
func gonextDataDBWrite(queryPtr, queryLen, argsPtr, argsLen uint32) uint64

//go:wasmimport gonext_data gn_kv_get
func gonextDataKVGet(keyPtr, keyLen uint32) uint64

//go:wasmimport gonext_data gn_kv_set
func gonextDataKVSet(keyPtr, keyLen, valPtr, valLen uint32) uint64

//go:wasmimport gonext_data gn_kv_del
func gonextDataKVDel(keyPtr, keyLen uint32) uint64

//go:wasmimport gonext_data gn_kv_incr
func gonextDataKVIncr(keyPtr, keyLen uint32, delta int64) uint64

//go:wasmimport gonext_data gn_cache_invalidate
func gonextDataCacheInvalidate(tagsPtr, tagsLen uint32) uint64

//go:wasmimport env gn_log
func envLog(level int32, ptr, length uint32)

//go:wasmimport env gn_time_ms
func envTimeMs() int64

//go:wasmimport env gn_i18n_translate
func envI18nTranslate(keyPtr, keyLen, localePtr, localeLen uint32) uint64

//go:wasmimport env gn_metric_observe
func envMetricObserve(namePtr, nameLen uint32, value float64, tagsPtr, tagsLen uint32) int32

//go:wasmimport env gn_event_emit
func envEventEmit(namePtr, nameLen, dataPtr, dataLen uint32) int32

//go:wasmimport env gn_span_event
func envSpanEvent(namePtr, nameLen, attrsPtr, attrsLen uint32) int32

//go:wasmimport env gn_audit_emit
func envAuditEmit(payloadPtr, payloadLen uint32) int32

//go:wasmimport env gn_cron_register
func envCronRegister(payloadPtr, payloadLen uint32) int32

//go:wasmimport env gn_secrets_get
func envSecretsGet(keyPtr, keyLen uint32) uint64

// ============================================================================
// hostCall* — the typed shims host_methods.go consumes. Each one
// writes the input buffers into guest linear memory, calls the
// wasmimport, and unpacks the result.
// ============================================================================

// hostCallHTTPFetch issues gn_http_fetch and returns (responseBytes,
// status). A non-negative status means the host wrote a response
// envelope (responseBytes is non-nil). A negative status is one of
// the NetResultStatus sentinels (denied, blocked, rate-limited, ...).
func hostCallHTTPFetch(body []byte) ([]byte, int32) {
	ptr, length := bytesToWasm(body)
	packed := envNetHTTPFetch(ptr, length)
	return unpackHostResult(packed)
}

func hostCallMediaRead(body []byte) ([]byte, int32) {
	ptr, length := bytesToWasm(body)
	packed := envNetMediaRead(ptr, length)
	return unpackHostResult(packed)
}

func hostCallUsersRead(body []byte) ([]byte, int32) {
	ptr, length := bytesToWasm(body)
	packed := envNetUsersRead(ptr, length)
	return unpackHostResult(packed)
}

func hostCallDBRead(query, args []byte) ([]byte, int32) {
	qp, ql := bytesToWasm(query)
	ap, al := bytesToWasm(args)
	packed := gonextDataDBRead(qp, ql, ap, al)
	return unpackHostResult(packed)
}

func hostCallDBWrite(query, args []byte) ([]byte, int32) {
	qp, ql := bytesToWasm(query)
	ap, al := bytesToWasm(args)
	packed := gonextDataDBWrite(qp, ql, ap, al)
	// db.write packs the affected-row count into the low 32 bits
	// when ptr=0. unpackHostResult interprets negative length as
	// status; non-negative length is the count — which we surface
	// as the status value to keep the host_methods.go shape uniform.
	return unpackHostResult(packed)
}

func hostCallKVGet(key []byte) ([]byte, int32) {
	kp, kl := bytesToWasm(key)
	packed := gonextDataKVGet(kp, kl)
	return unpackHostResult(packed)
}

func hostCallKVSet(key, val []byte) ([]byte, int32) {
	kp, kl := bytesToWasm(key)
	vp, vl := bytesToWasm(val)
	packed := gonextDataKVSet(kp, kl, vp, vl)
	return unpackHostResult(packed)
}

func hostCallKVDel(key []byte) ([]byte, int32) {
	kp, kl := bytesToWasm(key)
	packed := gonextDataKVDel(kp, kl)
	return unpackHostResult(packed)
}

func hostCallKVIncr(key []byte, delta int64) ([]byte, int32) {
	kp, kl := bytesToWasm(key)
	packed := gonextDataKVIncr(kp, kl, delta)
	return unpackHostResult(packed)
}

func hostCallCacheInvalidate(tags []byte) ([]byte, int32) {
	tp, tl := bytesToWasm(tags)
	packed := gonextDataCacheInvalidate(tp, tl)
	return unpackHostResult(packed)
}

func hostCallSecretsGet(key []byte) ([]byte, int32) {
	kp, kl := bytesToWasm(key)
	packed := envSecretsGet(kp, kl)
	return unpackHostResult(packed)
}

func hostCallAuditEmit(payload []byte) ([]byte, int32) {
	pp, pl := bytesToWasm(payload)
	status := envAuditEmit(pp, pl)
	return nil, status
}

func hostCallCronRegister(payload []byte) ([]byte, int32) {
	pp, pl := bytesToWasm(payload)
	status := envCronRegister(pp, pl)
	return nil, status
}

func hostCallMetricObserve(name []byte, value float64, tags []byte) int32 {
	np, nl := bytesToWasm(name)
	tp, tl := bytesToWasm(tags)
	return envMetricObserve(np, nl, value, tp, tl)
}

func hostCallEventEmit(name, data []byte) int32 {
	np, nl := bytesToWasm(name)
	dp, dl := bytesToWasm(data)
	return envEventEmit(np, nl, dp, dl)
}

func hostCallSpanEvent(name, attrs []byte) int32 {
	np, nl := bytesToWasm(name)
	ap, al := bytesToWasm(attrs)
	return envSpanEvent(np, nl, ap, al)
}

func hostCallI18nTranslate(key, locale []byte) ([]byte, int32) {
	kp, kl := bytesToWasm(key)
	lp, ll := bytesToWasm(locale)
	packed := envI18nTranslate(kp, kl, lp, ll)
	return unpackHostResult(packed)
}

func hostCallLog(level int32, msg []byte) {
	mp, ml := bytesToWasm(msg)
	envLog(level, mp, ml)
}

func hostCallTimeMs() int64 {
	return envTimeMs()
}

// ============================================================================
// bytesToWasm prepares a Go byte slice for transmission to the host.
//
// Both the wasm linear memory model and TinyGo's GC complicate this:
//
//   - The host reads bytes via mod.Memory().Read(ptr, len). Those
//     bytes MUST be in linear memory, not in some hypothetical Go
//     heap.
//
//   - TinyGo's GC may relocate live allocations. We can't pass a
//     pointer to a stack-allocated slice and expect it to survive the
//     host call.
//
// The safe path is: copy the bytes into a fresh guestAlloc'd buffer
// and pass that pointer. The buffer is retained by our allocator
// (allocBufs) so the GC keeps it alive across the call.
//
// nil/empty input returns (0, 0) — the host treats that as "no
// payload" uniformly.
// ============================================================================

func bytesToWasm(b []byte) (uint32, uint32) {
	if len(b) == 0 {
		return 0, 0
	}
	ptr := writeAlloc(b)
	if ptr == 0 {
		return 0, 0
	}
	return ptr, uint32(len(b))
}

// ============================================================================
// Hook entry point — exported as gn_handle_hook.
//
// The host writes (name, payload) into our memory via gn_alloc BEFORE
// invoking gn_handle_hook. We read the bytes back, dispatch through
// the registry, marshal the response, and pack (resultPtr, len) into
// the i64 return.
// ============================================================================

//go:wasmexport gn_handle_hook
func gn_handle_hook(namePtr, nameLen, payloadPtr, payloadLen uint32) uint64 {
	name := string(readGuest(namePtr, nameLen))
	payload := readGuest(payloadPtr, payloadLen)

	result, status := DispatchHook(name, payload)
	if status != StatusOK {
		return PackResult(0, int32(status))
	}
	if len(result) == 0 {
		return PackResult(0, int32(StatusOK))
	}
	ptr := writeAlloc(result)
	if ptr == 0 {
		return PackResult(0, int32(StatusOutOfMemory))
	}
	return PackResult(ptr, int32(len(result)))
}
