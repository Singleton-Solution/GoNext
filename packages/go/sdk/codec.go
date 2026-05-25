// Package sdk is the GoNext Go plugin SDK, TinyGo-targeted.
//
// A plugin author writes:
//
//	package main
//
//	import "github.com/Singleton-Solution/GoNext/packages/go/sdk"
//
//	func main() {
//	    sdk.RegisterAction("posts.publish", func(args []any) error {
//	        sdk.Host.KV.Set("last-published", []byte(args[0].(string)))
//	        sdk.Host.Audit.Emit("post.indexed", map[string]any{"id": args[0]})
//	        return nil
//	    })
//	    sdk.RegisterFilter("the_content", func(value []byte, args []any) ([]byte, error) {
//	        return append(value, []byte("\n<!-- enhanced -->")...), nil
//	    })
//	    sdk.PluginInit(sdk.NewManifest("hello", "0.1.0").
//	        WithCapability("kv.write").
//	        WithCapability("audit.emit").
//	        WithAction("posts.publish").
//	        WithFilter("the_content").
//	        Build())
//	}
//
// `tinygo build -target=wasi -o plugin.wasm .` produces a module that
// satisfies the v1 hook ABI in packages/go/plugins/abi/hooks — same
// gn_handle_hook / gn_alloc / gn_free / gn_panic exports the host
// expects.
//
// # Wire format
//
// The SDK encodes/decodes payloads on the same JSON envelope shape the
// host uses in packages/go/plugins/abi/hooks/marshal.go. We mirror the
// shapes here rather than depending on the host package so the SDK has
// no transitive imports the TinyGo toolchain can't handle.
//
// # Build modes
//
// codec.go (this file) is pure Go and compiles with both the stock
// toolchain AND TinyGo. Codec unit tests run on the stock toolchain so
// CI can validate marshalling without requiring TinyGo on PATH.
//
// hooks.go, host.go, host_*.go, and runtime_wasm.go contain the
// guest-side runtime that only makes sense inside a wasm32-wasi build.
// They use build tags to switch between a "real" implementation (under
// the wasi tag) and a no-op stub (everywhere else) — so a plugin
// author writing tests against their own handler functions can build
// the package under the stock toolchain too.
package sdk

import (
	"encoding/json"
	"errors"
	"fmt"
)

// PayloadKind tags whether a payload carries action arguments (one
// list) or filter arguments (a value + extras list). Mirrors
// abi/hooks.PayloadKind byte-for-byte; the constant strings are part
// of the v1 wire format.
type PayloadKind string

const (
	// PayloadKindAction is the action-call shape: a list of args.
	PayloadKindAction PayloadKind = "action"

	// PayloadKindFilter is the filter-call shape: a transformable
	// value plus the per-call extras.
	PayloadKindFilter PayloadKind = "filter"
)

// ActionPayload mirrors abi/hooks.ActionPayload — the wire form of an
// action-call payload. Kept here so the SDK has no host-package
// imports (TinyGo can't compile the host module).
type ActionPayload struct {
	Kind PayloadKind   `json:"kind"`
	Args []interface{} `json:"args"`
}

// FilterPayload mirrors abi/hooks.FilterPayload.
type FilterPayload struct {
	Kind  PayloadKind     `json:"kind"`
	Value json.RawMessage `json:"value"`
	Args  []interface{}   `json:"args"`
}

// FilterResult mirrors abi/hooks.FilterResult — the envelope a filter
// handler returns. The host decodes this shape on the way out.
type FilterResult struct {
	Value json.RawMessage `json:"value"`
}

// ResultStatus mirrors abi/hooks.ResultStatus. Sentinels are negative
// int32s the SDK packs into the low half of the i64 result when no
// body is being returned.
type ResultStatus int32

const (
	// StatusOK signals success-with-no-body. Returned by every action
	// handler that completes without producing a result envelope.
	StatusOK ResultStatus = 0

	// StatusError is the generic guest-reported failure. The SDK
	// returns this when a handler returns a non-nil error that isn't
	// one of the more specific sentinels below.
	StatusError ResultStatus = -1

	// StatusOutOfMemory signals an allocator failure inside the
	// handler. The SDK returns this when gn_alloc would fail.
	StatusOutOfMemory ResultStatus = -2

	// StatusBadPayload signals the SDK couldn't decode the inbound
	// payload — a host/guest ABI mismatch.
	StatusBadPayload ResultStatus = -3

	// StatusUnknownHook signals no handler is registered for the
	// requested name. The host typically treats this as a stale
	// plugin binary.
	StatusUnknownHook ResultStatus = -4
)

// PackResult composes the i64 the SDK returns from gn_handle_hook
// from a (ptr, len) pair. Pointer occupies the high 32 bits; length
// occupies the low 32 bits as a signed int32 so negative sentinels
// survive the round trip. Mirrors hooks.packResult.
func PackResult(ptr uint32, length int32) uint64 {
	return uint64(ptr)<<32 | uint64(uint32(length))
}

// UnpackResult is the inverse of PackResult.
func UnpackResult(packed uint64) (ptr uint32, length int32) {
	ptr = uint32(packed >> 32)
	length = int32(packed & 0xFFFFFFFF)
	return ptr, length
}

// MarshalActionPayload returns the JSON wire bytes for an action call.
// Mirrors hooks.MarshalActionPayload.
func MarshalActionPayload(args []interface{}) ([]byte, error) {
	if args == nil {
		args = []interface{}{}
	}
	return json.Marshal(ActionPayload{Kind: PayloadKindAction, Args: args})
}

// UnmarshalActionPayload decodes an action-call envelope. Returns an
// error wrapped around ErrBadPayload on a shape mismatch so handlers
// can errors.Is against the sentinel without type-asserting.
func UnmarshalActionPayload(buf []byte) (*ActionPayload, error) {
	if len(buf) == 0 {
		// The bridge always emits a complete envelope, even for
		// zero-arg actions ({"kind":"action","args":[]}). An empty
		// buffer is a contract violation; treat as bad payload.
		return nil, fmt.Errorf("%w: empty payload", ErrBadPayload)
	}
	var p ActionPayload
	if err := json.Unmarshal(buf, &p); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadPayload, err)
	}
	if p.Kind != PayloadKindAction {
		return nil, fmt.Errorf("%w: expected %q kind, got %q", ErrBadPayload, PayloadKindAction, p.Kind)
	}
	if p.Args == nil {
		p.Args = []interface{}{}
	}
	return &p, nil
}

// MarshalFilterPayload returns the JSON wire bytes for a filter call.
// value MAY be nil — in that case the envelope carries "value":null.
// Mirrors hooks.MarshalFilterPayload.
func MarshalFilterPayload(value json.RawMessage, args []interface{}) ([]byte, error) {
	if args == nil {
		args = []interface{}{}
	}
	if value == nil {
		value = json.RawMessage("null")
	}
	return json.Marshal(FilterPayload{Kind: PayloadKindFilter, Value: value, Args: args})
}

// UnmarshalFilterPayload decodes a filter-call envelope. Returns an
// error wrapped around ErrBadPayload on a shape mismatch.
func UnmarshalFilterPayload(buf []byte) (*FilterPayload, error) {
	if len(buf) == 0 {
		return nil, fmt.Errorf("%w: empty payload", ErrBadPayload)
	}
	var p FilterPayload
	if err := json.Unmarshal(buf, &p); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadPayload, err)
	}
	if p.Kind != PayloadKindFilter {
		return nil, fmt.Errorf("%w: expected %q kind, got %q", ErrBadPayload, PayloadKindFilter, p.Kind)
	}
	if p.Args == nil {
		p.Args = []interface{}{}
	}
	if len(p.Value) == 0 {
		p.Value = json.RawMessage("null")
	}
	return &p, nil
}

// MarshalFilterResult produces the wire bytes the host expects from a
// filter handler return — a JSON object with a single "value" key.
func MarshalFilterResult(value json.RawMessage) ([]byte, error) {
	if value == nil {
		value = json.RawMessage("null")
	}
	return json.Marshal(FilterResult{Value: value})
}

// UnmarshalFilterResult is the inverse: extracts the value bytes from
// a FilterResult envelope.
func UnmarshalFilterResult(buf []byte) (json.RawMessage, error) {
	if len(buf) == 0 {
		return nil, nil
	}
	var r FilterResult
	if err := json.Unmarshal(buf, &r); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadPayload, err)
	}
	if len(r.Value) == 0 {
		return json.RawMessage("null"), nil
	}
	return r.Value, nil
}

// Sentinel errors callers can errors.Is against. The SDK never wraps
// arbitrary host errors — the failure modes are categorical, and the
// status code carried in the i64 return value is the canonical signal.
//
// Plugin authors that return errors from their handlers get them
// projected onto one of these statuses by the dispatcher in hooks.go.
var (
	// ErrBadPayload is returned from the Unmarshal* helpers when the
	// inbound envelope can't be decoded as the expected shape.
	ErrBadPayload = errors.New("sdk: bad payload")

	// ErrUnknownHook is returned by the dispatcher when no handler
	// matches the inbound name.
	ErrUnknownHook = errors.New("sdk: unknown hook")

	// ErrOutOfMemory is returned when the SDK's allocator runs out of
	// room. The host translates this to ResultStatusOutOfMemory.
	ErrOutOfMemory = errors.New("sdk: out of memory")

	// ErrHostFailure is returned by the typed Host.* wrappers when a
	// host ABI call returns a negative status (denied, blocked, rate-
	// limited, etc). Inspect the wrapping HostError for the specific
	// status.
	ErrHostFailure = errors.New("sdk: host ABI returned failure status")
)

// HostError wraps a host-ABI failure. Status carries the negative
// sentinel the host returned; Function names which host export was
// called. Implements errors.Is against ErrHostFailure.
type HostError struct {
	Function string
	Status   int32
}

// Error returns a one-line summary.
func (e *HostError) Error() string {
	return fmt.Sprintf("sdk: host call %q failed (status=%d)", e.Function, e.Status)
}

// Is supports errors.Is(err, ErrHostFailure) for typed matching.
func (e *HostError) Is(target error) bool { return target == ErrHostFailure }
