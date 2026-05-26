package menus

import "regexp"

// Package-level regexes compiled once at init. Matching the column
// CHECK rules from migration 000035.
var (
	slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	pathRe = regexp.MustCompile(`^[0-9]{3}(\.[0-9]{3})*$`)
)
