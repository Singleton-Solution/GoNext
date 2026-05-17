package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// ErrValidation is returned (wrapped) by Store.Write when the value
// fails JSON Schema validation or the optional Validator callback. It
// is the sentinel callers errors.Is against when translating store
// errors into HTTP 400 / 422 responses.
var ErrValidation = errors.New("settings: validation failed")

// ErrUnknownKey is returned by Store.Write when the key has not been
// registered. We refuse to persist values for unknown keys — an unknown
// key would skip schema validation entirely and become a write-only
// graveyard in the options table.
var ErrUnknownKey = errors.New("settings: unknown key")

// Store reads and writes the actual setting values. The registry is
// the schema (immutable, in-memory); the store is the values (mutable,
// durable).
//
// Read returns the registered Default if the key is not in the
// underlying storage — callers never need to special-case "first run".
// Write validates the value against the registered Setting's Schema
// (and the optional Validator) before persisting; an invalid value is
// rejected with an ErrValidation-wrapped error and the store is not
// modified.
//
// Concurrency: implementations MUST be safe for concurrent use from
// many goroutines. Read is the hot path; Write is rare. The Postgres
// implementation uses an L1 cache to keep Read sub-millisecond; Write
// invalidates the cache entry as part of the write path.
type Store interface {
	// Read returns the current value for key. If the key is registered
	// but not yet stored, Read returns the registered Default and a
	// nil error. If the key is NOT registered, Read returns
	// ErrUnknownKey.
	Read(ctx context.Context, key string) (any, error)

	// Write persists value for key. The value is validated against
	// the registered Setting's Schema first, then the optional
	// Validator. Returns ErrValidation (wrapped) on validation failure,
	// ErrUnknownKey if the key isn't registered, or a storage error
	// otherwise.
	Write(ctx context.Context, key string, value any) error

	// BulkRead returns the current values for the given keys, applying
	// per-key defaults for any key not yet in the underlying storage.
	// Unknown keys (not in the registry) are silently skipped — they
	// will not appear in the returned map. Callers who need to detect
	// unknown keys should compare keys to len(out).
	BulkRead(ctx context.Context, keys []string) (map[string]any, error)

	// LoadAutoload returns the values for every Setting with
	// Autoload=true. Called once at boot to prime the in-memory cache;
	// the result is also the right shape for the Redis autoload hash
	// described in docs/01-core-cms.md §10.11.
	//
	// Defaults are applied for autoload keys not yet in storage. Keys
	// missing from the registry are not included.
	LoadAutoload(ctx context.Context) (map[string]any, error)
}

// validate runs JSON Schema validation against value, then the
// optional Validator callback. Returns nil iff both pass.
//
// JSON Schema's validator expects a value that has been round-tripped
// through encoding/json — concretely, it wants:
//   - bool         -> bool
//   - integer      -> float64 (json's default) or json.Number, NOT int
//   - string       -> string
//   - array        -> []any
//   - object       -> map[string]any
//
// Callers commonly hand Write a Go int / int64 / json.RawMessage. The
// helper normalizes those into the JSON-shaped form before running
// the validator so the API is friendly without the caller needing to
// know about the marshal/unmarshal dance.
func validate(entry *registryEntry, value any) error {
	if entry == nil {
		return ErrUnknownKey
	}
	normalized, err := normalizeForValidation(value)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrValidation, err)
	}
	if err := entry.Schema.Validate(normalized); err != nil {
		return fmt.Errorf("%w: %v", ErrValidation, err)
	}
	if entry.Setting.Validator != nil {
		if err := entry.Setting.Validator(value); err != nil {
			return fmt.Errorf("%w: %v", ErrValidation, err)
		}
	}
	return nil
}

// normalizeForValidation converts a Go value into the shape the JSON
// Schema validator expects — that is, the shape `json.Unmarshal` into
// `any` would produce. The simplest correct implementation is "marshal
// then unmarshal", which is what we do. It's not free, but it runs
// only on Write (rare) and against values that are typically small.
func normalizeForValidation(v any) (any, error) {
	// json.RawMessage carries a pre-marshaled payload; unmarshal it
	// directly without an extra marshal trip.
	if raw, ok := v.(json.RawMessage); ok {
		var out any
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, fmt.Errorf("unmarshal RawMessage: %w", err)
		}
		return out, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	return out, nil
}

// ValidateDefault validates the Setting's Default against its Schema.
// It is NOT called by Register (that would force every caller to take
// the marshal/unmarshal cost on package init), but it's a useful
// belt-and-braces check for core settings tests.
func (s Setting) ValidateDefault() error {
	if len(s.Schema) == 0 {
		return ErrInvalidSchema
	}
	compiled, err := compileSchema(s.Key, s.Schema)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSchema, err)
	}
	entry := &registryEntry{Setting: s, Schema: compiled}
	return validate(entry, s.Default)
}
