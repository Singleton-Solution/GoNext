//go:build tinygo

// Package main is the TinyGo entry point for the gonext-seo example
// plugin. It is compiled to WebAssembly (wasi target) and loaded by the
// host's wazero runtime.
//
// The plugin proves the whole plugin runtime end-to-end:
//
//   - It subscribes to a filter (the_content), an action (wp_head), and
//     a second action (save_post) — all three exercise the hook ABI
//     (#364, packages/go/plugins/abi/hooks).
//
//   - It owns one background job (seo.recompute-scores) — exercises the
//     job ABI (#395, packages/go/plugins/abi/jobs).
//
//   - It requests posts.read + posts.write + hooks.subscribe +
//     jobs.enqueue — exercises the capability gate (#344,
//     packages/go/plugins/capabilities).
//
//   - It declares a manifest the lifecycle Manager (#339) installs and
//     activates through the standard path.
//
// # ABI surface
//
// Every plugin built against gonext.io/v1 must export the following
// symbols. We re-state them here verbatim because the SDK that hides
// them does not yet exist in the public tree, and a self-contained
// example is far more useful to operators than one that depends on an
// in-house package.
//
//	(func (export "gn_alloc")        (param i32) (result i32))
//	(func (export "gn_free")         (param i32) (param i32))
//	(func (export "gn_handle_hook")  (param i32 i32 i32 i32) (result i64))
//	(func (export "gn_handle_job")   (param i32 i32 i32 i32) (result i64))
//
// The packed-i64 return convention is: high 32 bits = pointer into our
// own linear memory, low 32 bits = length. Sentinel negative lengths
// (with ptr==0) signal typed failures — see the status* constants.
//
// # Why no SDK
//
// The repository's Go SDK is a planned package (see
// docs/02-plugin-system.md §9.1); shipping the worked example before
// the SDK lands means the example demonstrates the raw ABI. When the
// SDK ships, this file will be rewritten as a thin layer over its
// hook.AddFilter/AddAction helpers — but the wire-format part of the
// contract will stay the same.
package main

import (
	"encoding/json"
	"unsafe"
)

// main is the entry point TinyGo requires. We do nothing here:
// dispatch is via the gn_handle_hook / gn_handle_job exports, not via
// main.
func main() {}

// -----------------------------------------------------------------------
// Result-packing helpers — mirror the host's packResult.
// -----------------------------------------------------------------------

// packResult composes the i64 the host expects from gn_handle_hook and
// gn_handle_job. Pointer occupies the high 32 bits; length occupies the
// low 32 bits. Length is signed so we can encode sentinel ResultStatus
// values (which are negative).
func packResult(ptr uint32, length int32) uint64 {
	return uint64(ptr)<<32 | uint64(uint32(length))
}

// Status sentinels — mirror packages/go/plugins/abi/hooks (and the jobs
// sibling). Sentinels are negative int32s so the host distinguishes
// them from a real (non-negative) length.
const (
	statusOK          int32 = 0
	statusError       int32 = -1
	statusOutOfMemory int32 = -2
	statusBadPayload  int32 = -3
	statusUnknownHook int32 = -4
)

// -----------------------------------------------------------------------
// Allocator — exported as gn_alloc / gn_free.
//
// Strategy: keep a global slice of []byte allocations. The retained
// reference is what keeps TinyGo's GC from freeing the backing storage
// under our feet.
//
// gn_free is a no-op: the example never reuses memory across calls and
// the per-call data is bounded by MaxPayloadBytes (1 MiB). A real
// plugin SDK would back this with a bump arena per invocation.
// -----------------------------------------------------------------------

var allocations [][]byte

//export gn_alloc
func gnAlloc(size uint32) uint32 {
	if size == 0 {
		// A 0-length allocation is legal but pointless; hand back a
		// stable non-zero pointer so callers don't mistake the result
		// for OOM.
		return 1
	}
	buf := make([]byte, size)
	allocations = append(allocations, buf)
	return uint32(uintptr(unsafe.Pointer(&buf[0])))
}

//export gn_free
func gnFree(ptr uint32, size uint32) {
	// no-op; see allocator strategy above.
	_ = ptr
	_ = size
}

// readGuestBytes turns a (ptr, len) into a Go []byte that aliases the
// same memory. unsafe.Slice is the TinyGo-supported way; reflect's
// SliceHeader is deprecated in modern Go and TinyGo follows suit.
func readGuestBytes(ptr uint32, length uint32) []byte {
	if length == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(uintptr(ptr))), int(length))
}

// writeResultBytes allocates fresh guest memory via gn_alloc and copies
// b into it. Returns (ptr, len) suitable for packResult.
func writeResultBytes(b []byte) (uint32, int32) {
	if len(b) == 0 {
		return 0, 0
	}
	ptr := gnAlloc(uint32(len(b)))
	if ptr == 0 {
		return 0, statusOutOfMemory
	}
	dst := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(ptr))), len(b))
	copy(dst, b)
	return ptr, int32(len(b))
}

// -----------------------------------------------------------------------
// Wire-format mirror types — these mirror the host-side payload structs
// in packages/go/plugins/abi/hooks/marshal.go. Keeping them inline (not
// behind an SDK package) means the example is self-contained.
// -----------------------------------------------------------------------

type filterPayload struct {
	Kind  string          `json:"kind"`
	Value json.RawMessage `json:"value"`
	Args  []interface{}   `json:"args"`
}

type filterResult struct {
	Value json.RawMessage `json:"value"`
}

type actionPayload struct {
	Kind string        `json:"kind"`
	Args []interface{} `json:"args"`
}

// -----------------------------------------------------------------------
// Hook dispatch — exported as gn_handle_hook.
// -----------------------------------------------------------------------

//export gn_handle_hook
func gnHandleHook(namePtr, nameLen, payloadPtr, payloadLen uint32) uint64 {
	name := string(readGuestBytes(namePtr, nameLen))
	payload := readGuestBytes(payloadPtr, payloadLen)

	switch name {
	case "the_content":
		return invokeContentFilter(payload)
	case "wp_head":
		return invokeWPHead(payload)
	case "save_post":
		return invokeSavePost(payload)
	default:
		return packResult(0, statusUnknownHook)
	}
}

// invokeContentFilter is the filter handler. It receives the post HTML
// in payload.value and returns the same HTML augmented with a
// schema.org Article JSON-LD <script> block at the end.
func invokeContentFilter(payload []byte) uint64 {
	var fp filterPayload
	if err := json.Unmarshal(payload, &fp); err != nil {
		return packResult(0, statusBadPayload)
	}
	var inputHTML string
	if err := json.Unmarshal(fp.Value, &inputHTML); err != nil {
		return packResult(0, statusBadPayload)
	}

	post := postFromArgs(fp.Args)
	jsonld := BuildJSONLD(post)
	out := inputHTML + "\n" + jsonld

	encodedValue, err := json.Marshal(out)
	if err != nil {
		return packResult(0, statusError)
	}
	resultBytes, err := json.Marshal(filterResult{Value: encodedValue})
	if err != nil {
		return packResult(0, statusError)
	}
	ptr, length := writeResultBytes(resultBytes)
	if length < 0 {
		return packResult(0, length)
	}
	return packResult(ptr, length)
}

// invokeWPHead is the action handler for wp_head. Actions return no
// body — the side effect is what matters. In a real plugin we'd call
// the host's "emit_head_html" capability here; in the example, we just
// validate that the payload decodes and the side-effect computation
// runs (a panic in BuildHeadHTML would surface as a trap).
func invokeWPHead(payload []byte) uint64 {
	var ap actionPayload
	if err := json.Unmarshal(payload, &ap); err != nil {
		return packResult(0, statusBadPayload)
	}
	post := postFromArgs(ap.Args)
	_ = BuildHeadHTML(post)
	return packResult(0, statusOK)
}

// invokeSavePost is the action handler for save_post. We compute the
// SEO score and (in a real plugin) call posts.write to persist it to
// post meta. The example skips the host call because the host-side
// posts.write ABI lands separately; the score computation is the part
// we test.
func invokeSavePost(payload []byte) uint64 {
	var ap actionPayload
	if err := json.Unmarshal(payload, &ap); err != nil {
		return packResult(0, statusBadPayload)
	}
	post := postFromArgs(ap.Args)
	_ = ComputeSEOScore(post)
	return packResult(0, statusOK)
}

// -----------------------------------------------------------------------
// Job dispatch — exported as gn_handle_job.
// -----------------------------------------------------------------------

//export gn_handle_job
func gnHandleJob(namePtr, nameLen, payloadPtr, payloadLen uint32) uint64 {
	name := string(readGuestBytes(namePtr, nameLen))
	payload := readGuestBytes(payloadPtr, payloadLen)

	switch name {
	case "seo.recompute-scores":
		return invokeRecomputeScoresJob(payload)
	default:
		return packResult(0, statusError) // unknown job — mirrors the hooks UnknownHook path
	}
}

// invokeRecomputeScoresJob processes the batch recompute job. Payload
// is expected to be the asynq Task payload — for this example we accept
// any JSON object and return OK. A real implementation would fan out
// over posts.read, compute ComputeSEOScore for each, and save via
// posts.write.
func invokeRecomputeScoresJob(payload []byte) uint64 {
	if len(payload) > 0 {
		var probe map[string]interface{}
		if err := json.Unmarshal(payload, &probe); err != nil {
			return packResult(0, statusBadPayload)
		}
	}
	return packResult(0, statusOK)
}
