package queryparse_test

import (
	"errors"
	"testing"

	"github.com/Singleton-Solution/GoNext/packages/go/util/queryparse"
)

// validPosts mirrors the post_status enum the REST posts handler uses.
// Kept as a fixture rather than imported so this test file has zero
// dependencies outside the package under test.
var validPosts = map[string]struct{}{
	"draft":     {},
	"pending":   {},
	"published": {},
	"scheduled": {},
	"private":   {},
	"trash":     {},
}

// TestParseStatus_EmptyMeansNoFilter documents the contract for the
// empty-string case: the handler treats it as "all statuses".
func TestParseStatus_EmptyMeansNoFilter(t *testing.T) {
	got, err := queryparse.ParseStatus("", validPosts)
	if err != nil {
		t.Fatalf("ParseStatus(\"\"): unexpected error %v", err)
	}
	if got != "" {
		t.Errorf("ParseStatus(\"\") = %q, want \"\"", got)
	}
}

// TestParseStatus_AnyIsAlias is the regression test for issue #516 —
// the literal "any" must map to the empty filter, identically to "".
func TestParseStatus_AnyIsAlias(t *testing.T) {
	got, err := queryparse.ParseStatus("any", validPosts)
	if err != nil {
		t.Fatalf("ParseStatus(\"any\"): unexpected error %v", err)
	}
	if got != "" {
		t.Errorf("ParseStatus(\"any\") = %q, want \"\" (alias for no filter)", got)
	}
}

// TestParseStatus_ValidPassesThrough asserts that a value present in
// the valid set is returned verbatim.
func TestParseStatus_ValidPassesThrough(t *testing.T) {
	for _, raw := range []string{"draft", "pending", "published", "scheduled", "private", "trash"} {
		t.Run(raw, func(t *testing.T) {
			got, err := queryparse.ParseStatus(raw, validPosts)
			if err != nil {
				t.Fatalf("ParseStatus(%q): unexpected error %v", raw, err)
			}
			if got != raw {
				t.Errorf("ParseStatus(%q) = %q, want %q", raw, got, raw)
			}
		})
	}
}

// TestParseStatus_UnknownReturnsErr covers the rejection path: an
// unknown value yields ErrInvalidStatus and an empty string.
func TestParseStatus_UnknownReturnsErr(t *testing.T) {
	got, err := queryparse.ParseStatus("approve", validPosts) // typo of "approved"; not in set
	if !errors.Is(err, queryparse.ErrInvalidStatus) {
		t.Fatalf("ParseStatus(\"approve\") err = %v, want ErrInvalidStatus", err)
	}
	if got != "" {
		t.Errorf("ParseStatus(\"approve\") = %q, want \"\" on error", got)
	}
}

// TestParseStatus_NilValidMapRejectsNonAlias documents that a caller
// passing a nil valid set still gets the "" / "any" aliases for free
// but rejects everything else. The implementation reads valid only
// inside the map-lookup branch, so a nil map is safe.
func TestParseStatus_NilValidMapRejectsNonAlias(t *testing.T) {
	// empty + alias should still pass with a nil map
	if got, err := queryparse.ParseStatus("", nil); err != nil || got != "" {
		t.Errorf("ParseStatus(\"\", nil) = (%q, %v), want (\"\", nil)", got, err)
	}
	if got, err := queryparse.ParseStatus("any", nil); err != nil || got != "" {
		t.Errorf("ParseStatus(\"any\", nil) = (%q, %v), want (\"\", nil)", got, err)
	}
	// anything else must error — the lookup against a nil map yields
	// (zero, false) just like an empty map.
	if got, err := queryparse.ParseStatus("draft", nil); !errors.Is(err, queryparse.ErrInvalidStatus) || got != "" {
		t.Errorf("ParseStatus(\"draft\", nil) = (%q, %v), want (\"\", ErrInvalidStatus)", got, err)
	}
}

// TestParseStatus_CaseSensitive guards the design decision that
// status values are matched byte-exactly. The handlers that call in
// emit lowercase enums, so "Draft" must be rejected — relying on
// case-insensitive matching here would diverge from the underlying
// CHECK constraints in the schema migrations.
func TestParseStatus_CaseSensitive(t *testing.T) {
	if _, err := queryparse.ParseStatus("Draft", validPosts); !errors.Is(err, queryparse.ErrInvalidStatus) {
		t.Errorf("ParseStatus(\"Draft\"): err = %v, want ErrInvalidStatus (case-sensitive match)", err)
	}
}

// TestParseStatus_AnyCaseSensitive guards a subtle edge: only the
// lowercase "any" is the alias. "ANY", "Any", etc. fall through to
// the valid-set check and (assuming they're not in the set) are
// rejected. Documents the design rather than the implementation.
func TestParseStatus_AnyCaseSensitive(t *testing.T) {
	if _, err := queryparse.ParseStatus("ANY", validPosts); !errors.Is(err, queryparse.ErrInvalidStatus) {
		t.Errorf("ParseStatus(\"ANY\"): err = %v, want ErrInvalidStatus", err)
	}
}
