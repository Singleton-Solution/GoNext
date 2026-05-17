package theme

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Parse decodes a theme.json byte slice into a ThemeJSON value. The
// decoder is strict: unknown top-level keys produce an error (this is
// the "rejects unknown top-level keys" acceptance criterion in issue
// #5). Type mismatches (e.g. "version": "1") also produce an error.
//
// Parse does NOT call Validate. The caller decides whether to follow
// up with a Validate() pass — the install flow always does; quick
// diagnostic tools may want to inspect the raw structure first.
func Parse(data []byte) (*ThemeJSON, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("theme: empty input")
	}

	// Whitespace-trimmed, empty-after-trim is still empty. We catch
	// this here rather than letting json.Decoder report a confusing
	// "EOF" because the admin error message is shown to humans.
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("theme: input contains only whitespace")
	}

	var out ThemeJSON
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("theme: parse: %w", err)
	}

	// json.Decoder happily accepts trailing JSON values after the
	// first one ("garbage after legitimate top-level object"). We
	// catch that here so two manifests concatenated by mistake fail
	// loudly instead of silently dropping the second one.
	if dec.More() {
		return nil, fmt.Errorf("theme: parse: trailing data after JSON value")
	}

	return &out, nil
}
