// Package queryparse holds tiny, dependency-free helpers for parsing
// URL query parameters that several handlers share.
//
// The package exists to keep the same alias rules ("status=any" means
// "no filter", empty string also means "no filter") from drifting
// across endpoints. Three list handlers — REST posts, admin comments,
// and admin search — used to hand-roll the same shape; every new list
// endpoint should call into here instead so the contract stays
// authoritative in one file.
package queryparse

import "errors"

// ErrInvalidStatus is returned by ParseStatus when the raw value is
// non-empty, not the "any" alias, and not present in the caller's
// valid set. Handlers map this to a 400 with an error code of their
// choosing — typically "invalid_status".
var ErrInvalidStatus = errors.New("queryparse: invalid status")

// ParseStatus parses a "status" query param against the caller's set
// of valid values.
//
// Both "" and the literal "any" mean "no filter": the caller wants
// every status. Both return ("", nil) so the handler can drop the
// filter from its downstream call without a separate branch.
//
// Any other value must be in valid; otherwise ParseStatus returns
// ErrInvalidStatus. The caller supplies its own set rather than
// importing a shared one because the valid statuses differ by
// resource (posts use the WordPress post_status enum, comments use a
// moderation-state enum, etc.).
func ParseStatus(raw string, valid map[string]struct{}) (string, error) {
	if raw == "" || raw == "any" {
		return "", nil
	}
	if _, ok := valid[raw]; !ok {
		return "", ErrInvalidStatus
	}
	return raw, nil
}
