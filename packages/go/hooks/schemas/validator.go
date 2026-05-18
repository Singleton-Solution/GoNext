package schemas

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ValidatePayload checks payload against the schema registered for
// hookName. The validation is the public contract; bus call sites use
// this to gate Apply/Do invocations, and consumer code uses it to gate
// values produced by a filter chain before persisting them.
//
// Returns:
//
//   - nil if the payload validates, or if hookName has no registered
//     schema (loose mode is the default — strict mode is opt-in via
//     [Enforce]).
//   - A wrapped [ErrInvalidPayload] when the payload fails validation.
//     The inner *jsonschema.ValidationError is reachable via errors.As
//     so callers can extract the per-instance-path detail.
//
// Conversion model
//
// The bus passes Go values (any, including typed structs) into Do and
// ApplyFilters. JSON Schema operates on the JSON-shaped equivalent of
// those values. We bridge by round-tripping through encoding/json:
// MarshalIndent the payload, Unmarshal into an interface{}, validate.
//
// This is the same approach packages/go/settings and packages/go/theme
// use, and it has one important consequence: a payload that cannot be
// round-tripped (e.g. a chan, a func, or a type with an UnmarshalJSON
// that rejects its own MarshalJSON) is reported as an invalid payload.
// We surface the underlying json.Marshaler error so the operator can see
// which field tripped the conversion.
//
// Safe for concurrent use. Compiled schemas inside the registry are
// reused across calls — there is no per-call compilation.
func (r *SchemaRegistry) ValidatePayload(hookName string, payload any) error {
	sch := r.lookup(hookName)
	if sch == nil {
		// Loose mode: no contract declared, no validation. Strict
		// mode is enforced one layer up by [Enforce] / the bus
		// middleware so this primitive stays composable.
		return nil
	}

	// Round-trip through JSON to get a value the validator can walk.
	// The compiled schema expects map[string]any / []any / string /
	// float64 / bool / nil per the jsonschema library's contract.
	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("%w: encoding %q payload: %v", ErrInvalidPayload, hookName, err)
	}
	var generic any
	if err := json.Unmarshal(jsonBytes, &generic); err != nil {
		return fmt.Errorf("%w: decoding %q payload: %v", ErrInvalidPayload, hookName, err)
	}

	if err := sch.Validate(generic); err != nil {
		// errors.As can recover the *jsonschema.ValidationError for
		// callers that want the per-instance-path detail.
		return fmt.Errorf("%w: %q: %w", ErrInvalidPayload, hookName, err)
	}
	return nil
}

// ValidateStrict is the strict-mode form of [ValidatePayload]: an
// unregistered hookName returns [ErrUnregisteredHook] rather than nil.
//
// Hosts that want belt-and-braces — every hook MUST have a contract —
// call this directly. The bus middleware [Enforce] accepts a Mode flag
// that selects between the two; this method is also exported for direct
// use by consumer code.
func (r *SchemaRegistry) ValidateStrict(hookName string, payload any) error {
	if !r.Has(hookName) {
		return fmt.Errorf("%w: %q", ErrUnregisteredHook, hookName)
	}
	return r.ValidatePayload(hookName, payload)
}

// AsValidationError unwraps a payload-validation error into the underlying
// jsonschema error type, if present. Callers that want the per-instance-
// path message tree can use this to skip the errors.As boilerplate.
//
// Returns nil if err does not carry a jsonschema validation cause (e.g.
// the error was a marshal failure or a sentinel like ErrUnregisteredHook).
func AsValidationError(err error) error {
	if err == nil {
		return nil
	}
	// The wrapped chain is:
	//   ErrInvalidPayload -> %w jsonschema.ValidationError
	// errors.Unwrap walks the wrappers one at a time; the jsonschema
	// type implements Error() but is not exported through our package,
	// so callers want the inner cause directly.
	var cur error = err
	for cur != nil {
		if e := errors.Unwrap(cur); e != nil && e != ErrInvalidPayload {
			cur = e
			continue
		}
		break
	}
	if cur == err {
		return nil
	}
	return cur
}
