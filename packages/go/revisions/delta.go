package revisions

import (
	"encoding/json"
	"errors"
	"fmt"

	jsonpatch "github.com/evanphx/json-patch/v5"
	"github.com/wI2L/jsondiff"
)

// ErrInvalidDelta is returned by Apply when the supplied delta is not
// a valid RFC 6902 JSON Patch, when base is not valid JSON, or when
// the patch fails to apply (e.g. a "test" operation that doesn't
// match, or a path that doesn't exist).
var ErrInvalidDelta = errors.New("revisions: invalid delta")

// ComputeDelta produces an RFC 6902 JSON Patch transforming a into b.
//
// The patch is the minimal sequence of operations (add / remove /
// replace / move / copy / test) that, applied to a, yields b.
// jsondiff is used under the hood — it's allocation-friendly and
// produces patches that round-trip cleanly through Apply.
//
// Returns ErrInvalidDelta if either input is not valid JSON.
func ComputeDelta(a, b json.RawMessage) (json.RawMessage, error) {
	if !json.Valid(a) {
		return nil, errors.Join(ErrInvalidDelta, errors.New("a is not valid JSON"))
	}
	if !json.Valid(b) {
		return nil, errors.Join(ErrInvalidDelta, errors.New("b is not valid JSON"))
	}

	patch, err := jsondiff.CompareJSON(a, b)
	if err != nil {
		return nil, fmt.Errorf("revisions: jsondiff: %w", err)
	}
	if len(patch) == 0 {
		// An empty patch is the canonical "no-op". We emit "[]" rather
		// than nil so the persisted JSONB column is never NULL when
		// the row claims to be a delta.
		return json.RawMessage("[]"), nil
	}

	encoded, err := json.Marshal(patch)
	if err != nil {
		return nil, fmt.Errorf("revisions: marshal patch: %w", err)
	}
	return json.RawMessage(encoded), nil
}

// ApplyDelta applies an RFC 6902 JSON Patch to a base document and
// returns the result.
//
// Returns ErrInvalidDelta (wrapped) if base is invalid JSON, if delta
// is not a valid JSON Patch, or if applying the patch fails (e.g. a
// "test" op that doesn't match, or a "remove" on a non-existent path).
func ApplyDelta(base, delta json.RawMessage) (json.RawMessage, error) {
	if !json.Valid(base) {
		return nil, errors.Join(ErrInvalidDelta, errors.New("base is not valid JSON"))
	}
	// A nil or empty delta is treated as "no-op" — returning base
	// unchanged is friendlier to callers than erroring on a
	// well-meaning Materialize against a snapshot that the caller
	// happened to route through ApplyDelta.
	if len(delta) == 0 {
		out := make(json.RawMessage, len(base))
		copy(out, base)
		return out, nil
	}

	patch, err := jsonpatch.DecodePatch(delta)
	if err != nil {
		return nil, errors.Join(ErrInvalidDelta, fmt.Errorf("decode patch: %w", err))
	}

	applied, err := patch.Apply(base)
	if err != nil {
		return nil, errors.Join(ErrInvalidDelta, fmt.Errorf("apply patch: %w", err))
	}
	return json.RawMessage(applied), nil
}
