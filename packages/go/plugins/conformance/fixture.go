package conformance

import (
	"encoding/json"
	"fmt"
	"os"
)

// readFixture loads a per-scenario fixture file (the same shape
// writeFixtures emits). Returns the parsed Report.
//
// Errors:
//   - the file cannot be opened — surfaces as a scenario fail with
//     the OS error message wrapped.
//   - the JSON is malformed — surfaces with the json error message.
//
// This helper is the inverse of [Report.writeFixtures] and lives in
// its own file so the runner stays focused on orchestration.
func readFixture(path string) (Report, error) {
	f, err := os.Open(path)
	if err != nil {
		return Report{}, fmt.Errorf("open fixture: %w", err)
	}
	defer f.Close()
	var r Report
	if err := json.NewDecoder(f).Decode(&r); err != nil {
		return Report{}, fmt.Errorf("decode fixture %s: %w", path, err)
	}
	return r, nil
}
