package acf

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
)

// Parse reads an ACF JSON export from r and returns the contained
// field groups. The input may be a single field-group object or a
// JSON array — see [FieldGroupExport.UnmarshalJSON].
//
// Parse returns an error if the input is empty or syntactically
// invalid JSON. A successful parse with zero groups is allowed and
// returns an empty []FieldGroup.
func Parse(r io.Reader) (*FieldGroupExport, error) {
	if r == nil {
		return nil, fmt.Errorf("acf: nil reader")
	}
	body, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("acf: read: %w", err)
	}
	var exp FieldGroupExport
	if err := json.Unmarshal(body, &exp); err != nil {
		return nil, err
	}
	return &exp, nil
}

// sortStrings sorts s in place. Indirection through this helper
// keeps the package free of stdlib import noise across files.
func sortStrings(s []string) { sort.Strings(s) }
