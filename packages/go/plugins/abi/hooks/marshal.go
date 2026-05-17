package hooks

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Payload kinds carried over the ABI. The wire format is JSON because:
//
//   - Every plugin SDK we target (Rust, AssemblyScript, TinyGo, Go) has
//     a stdlib-grade JSON implementation. That keeps the per-language
//     SDK surface a single dependency.
//
//   - The host's hook bus (packages/go/hooks) accepts arbitrary
//     `any`-typed args. Marshaling to JSON is the lowest-common-denominator
//     way to express that across the language boundary without
//     committing to a richer schema (protobuf, msgpack) the SDKs would
//     have to mirror.
//
//   - The performance cost is amortized — plugin hooks run on the
//     control plane (post-publish, user-created, settings-changed), not
//     in the data path of every request. The benchmark target for the
//     bridge is "≤ 100 µs round-trip for a typical payload", and JSON
//     comfortably fits within that.
//
// If we later need a faster encoding for hot-path hooks, the ABI is
// versioned (see EntryPoint comment in abi.go); v2 can switch to
// msgpack without breaking v1 consumers.

// PayloadKind tags whether a payload carries action arguments (one
// list) or filter arguments (a value + extras list). The guest reads
// kind to know which shape to expect; the host writes it from the call
// site (InvokeAction vs InvokeFilter).
type PayloadKind string

const (
	// PayloadKindAction is the action-call shape: a list of args, no
	// transformable value.
	PayloadKindAction PayloadKind = "action"

	// PayloadKindFilter is the filter-call shape: a value to transform
	// plus the action-like extras.
	PayloadKindFilter PayloadKind = "filter"
)

// ActionPayload is the wire form of an action-call payload.
//
// Fields:
//
//   - Kind is always PayloadKindAction.
//
//   - Args carries the bus-level `args ...any` from hooks.Bus.Do. We
//     ship it as a JSON array; nested types become whatever
//     encoding/json renders them as. The guest is responsible for
//     decoding the array into whatever per-hook shape the plugin
//     expects.
//
// A nil or empty Args is the "action with no payload" case — the bridge
// emits {"kind":"action","args":[]} for it, NOT a 0-byte payload, so
// the guest's decoder works uniformly.
type ActionPayload struct {
	Kind PayloadKind   `json:"kind"`
	Args []interface{} `json:"args"`
}

// FilterPayload is the wire form of a filter-call payload.
//
//   - Kind is always PayloadKindFilter.
//
//   - Value is the transformable value threaded through the filter
//     chain. Marshaled as the raw json.RawMessage so callers that
//     already have a serialized blob (e.g. a post body) don't pay the
//     unmarshal+remarshal cost. Callers passing a typed Go value must
//     marshal it themselves and store the bytes here.
//
//   - Args carries the per-call extras (the `args ...any` in
//     hooks.Bus.ApplyFilters), same semantics as ActionPayload.Args.
type FilterPayload struct {
	Kind  PayloadKind     `json:"kind"`
	Value json.RawMessage `json:"value"`
	Args  []interface{}   `json:"args"`
}

// FilterResult is the wire form of a filter handler's return value.
// The guest produces this; the host decodes it.
//
//   - Value is the transformed value, as raw JSON bytes. The bridge
//     hands these bytes back to the caller (hooks.Bus.ApplyFilters
//     receives them as []byte) without unmarshalling further — the
//     host bus is already untyped, so the bytes are the canonical
//     representation.
//
// We deliberately don't include error/status here; failures use the
// ResultStatus return path. Mixing them would create two competing
// failure surfaces.
type FilterResult struct {
	Value json.RawMessage `json:"value"`
}

// MarshalActionPayload returns the JSON wire bytes for an action call.
//
// args may be nil — the bridge passes the bus's variadic args through
// unchanged, including the zero-arg "empty action" case (e.g. an
// `on_shutdown` hook). The encoded form is always a complete envelope,
// never a 0-byte payload, so the guest's decoder has a single shape to
// handle.
//
// Error: returns a non-nil error only if encoding/json itself fails,
// which is rare in practice (cycles in the input). Plugin authors that
// want stricter guarantees can pre-marshal their args.
func MarshalActionPayload(args []interface{}) ([]byte, error) {
	if args == nil {
		// nil → []interface{}{} so the encoded form is "args":[]
		// instead of "args":null. Guests prefer the empty-array shape.
		args = []interface{}{}
	}
	p := ActionPayload{Kind: PayloadKindAction, Args: args}
	return json.Marshal(p)
}

// MarshalFilterPayload returns the JSON wire bytes for a filter call.
//
// value is the raw JSON bytes representing the value to transform. If
// nil, the encoded form carries "value":null — the guest may treat
// that as a sentinel "no value" if its hook semantics allow it.
//
// args carries the per-call extras (see FilterPayload).
//
// We require pre-marshaled value bytes rather than an `any` parameter
// so callers that already have JSON (the common case, e.g. forwarding
// a request body) don't pay a decode+re-encode round trip.
func MarshalFilterPayload(value json.RawMessage, args []interface{}) ([]byte, error) {
	if args == nil {
		args = []interface{}{}
	}
	if value == nil {
		// Explicit "null" so the field is always present.
		value = json.RawMessage("null")
	}
	p := FilterPayload{Kind: PayloadKindFilter, Value: value, Args: args}
	return json.Marshal(p)
}

// UnmarshalFilterResult decodes the bytes the guest produced from a
// filter call. The guest's gn_handle_hook is expected to write the
// transformed value into a FilterResult envelope; UnmarshalFilterResult
// pulls the Value field out and returns it as raw bytes for the host
// bus.
//
// Errors:
//
//   - Returns ErrBadPayload-wrapped error if the bytes don't decode as
//     a FilterResult. This is a guest-side contract violation; the
//     bridge surfaces it like the guest had returned
//     ResultStatusBadPayload.
//
//   - Returns nil, nil for empty input — the "no body" success path
//     (which actions use but filters generally don't; included for
//     symmetry).
func UnmarshalFilterResult(buf []byte) (json.RawMessage, error) {
	if len(buf) == 0 {
		return nil, nil
	}
	var r FilterResult
	if err := json.Unmarshal(buf, &r); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadPayload, err)
	}
	if len(r.Value) == 0 {
		// A FilterResult with no value is treated as JSON null so the
		// caller has a well-defined shape to feed forward.
		return json.RawMessage("null"), nil
	}
	return r.Value, nil
}

// hookNameValid is a defense-in-depth check on the name the host is
// about to send to the guest. The manifest schema's regex already gates
// most cases at install time; this catches values that came in from a
// different path (e.g. a test calling InvokeAction directly).
//
// Returns nil for an acceptable name; a wrapped ErrHookNameTooLong or a
// "bad name" error otherwise.
func hookNameValid(name string) error {
	if name == "" {
		return errors.New("abi/hooks: hook name is empty")
	}
	if len(name) > MaxHookNameLen {
		return fmt.Errorf("%w: %d > %d", ErrHookNameTooLong, len(name), MaxHookNameLen)
	}
	return nil
}
