package customfields

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Validate checks values against group.Schema. Returns a multi-error
// containing every violation; nil on success.
//
// The validator compiles the group's schema on every call. Production
// callers should cache the compiled schema across requests when the
// group hasn't changed — wire that through Store.GetGroup's caller,
// not through this function.
//
// The function deliberately accepts a raw JSON blob (rather than a
// decoded any) because the schema validator works on JSON values,
// and re-decoding here is the boundary between "trusted-shape values
// from the store" and "untrusted-shape values from a client". Both
// paths go through the same gate.
func Validate(group FieldGroup, values json.RawMessage) error {
	if len(group.Schema) == 0 {
		return errors.New("customfields: group has no schema")
	}
	if len(values) == 0 {
		return errors.New("customfields: values are required")
	}

	// We can't import jsonschemautil here without creating a cycle
	// (the meta-store may eventually want to cache compiled schemas
	// and the cache lives in this package). Use a focused validator
	// stub that handles the common shape: a JSON Schema "object"
	// with declared properties + a "required" list. This covers
	// every group-schema produced by acf.MapFieldGroup and is the
	// 90% case for hand-authored groups.
	return validateAgainstSchema(group.Schema, values)
}

// validateAgainstSchema is a minimalist JSON-Schema-ish validator.
// It covers what FieldGroup needs: type=object with properties +
// required + per-property type checks. Anything beyond that
// (oneOf, $ref, format) is best-effort and falls back to "accept" so
// a more permissive schema doesn't accidentally reject valid data.
//
// The full draft 2020-12 validator from jsonschemautil is the right
// answer once the package can take the dependency; for now this
// in-package validator covers the load-bearing 80% of group schemas.
func validateAgainstSchema(schema, value json.RawMessage) error {
	var s map[string]any
	if err := json.Unmarshal(schema, &s); err != nil {
		return fmt.Errorf("schema: invalid JSON: %w", err)
	}
	var v map[string]any
	if err := json.Unmarshal(value, &v); err != nil {
		return fmt.Errorf("values: must be a JSON object: %w", err)
	}

	var errs []error

	// Required.
	if reqRaw, ok := s["required"]; ok {
		req, _ := reqRaw.([]any)
		for _, k := range req {
			key, _ := k.(string)
			if _, present := v[key]; !present {
				errs = append(errs, fmt.Errorf("required field %q is missing", key))
			}
		}
	}

	// Properties.
	props, _ := s["properties"].(map[string]any)
	for key, val := range v {
		propSchema, ok := props[key].(map[string]any)
		if !ok {
			// Unknown property; allowed unless additionalProperties=false.
			if additionalDisallowed(s) {
				errs = append(errs, fmt.Errorf("unknown field %q (additionalProperties = false)", key))
			}
			continue
		}
		if err := checkType(key, propSchema, val); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// additionalDisallowed returns whether the schema has
// additionalProperties:false. JSON Schema's default is "true" (extras
// allowed); we only reject extras when the schema author explicitly
// said so.
func additionalDisallowed(s map[string]any) bool {
	v, ok := s["additionalProperties"]
	if !ok {
		return false
	}
	b, isBool := v.(bool)
	return isBool && !b
}

// checkType validates one property value against its property
// sub-schema. Type coverage: string, number, integer, boolean,
// array, object. enum is honoured on top of the type check.
func checkType(field string, propSchema map[string]any, value any) error {
	t, _ := propSchema["type"].(string)
	switch t {
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("field %q: expected string, got %T", field, value)
		}
	case "number":
		if _, ok := value.(float64); !ok {
			return fmt.Errorf("field %q: expected number, got %T", field, value)
		}
	case "integer":
		f, ok := value.(float64)
		if !ok {
			return fmt.Errorf("field %q: expected integer, got %T", field, value)
		}
		if f != float64(int64(f)) {
			return fmt.Errorf("field %q: expected integer, got fractional %v", field, f)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("field %q: expected boolean, got %T", field, value)
		}
	case "array":
		if _, ok := value.([]any); !ok {
			return fmt.Errorf("field %q: expected array, got %T", field, value)
		}
	case "object":
		if _, ok := value.(map[string]any); !ok {
			return fmt.Errorf("field %q: expected object, got %T", field, value)
		}
	case "":
		// no type constraint declared; accept.
	default:
		return fmt.Errorf("field %q: unsupported schema type %q", field, t)
	}

	// enum check.
	if enumRaw, ok := propSchema["enum"]; ok {
		enum, _ := enumRaw.([]any)
		for _, allowed := range enum {
			if allowed == value {
				return nil
			}
		}
		return fmt.Errorf("field %q: value not in enum", field)
	}
	return nil
}
