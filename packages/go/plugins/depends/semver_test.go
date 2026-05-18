package depends

import "testing"

// TestMatchRange exercises the operator vocabulary the manifest
// schema allows. The cases are picked from the npm-semver test
// fixtures plus the load-bearing edge cases the resolver depends on
// (caret-zero, compound clauses, malformed input).
func TestMatchRange(t *testing.T) {
	t.Parallel()
	type tc struct {
		name    string
		version string
		expr    string
		want    bool
		wantErr bool
	}
	cases := []tc{
		// Exact
		{"exact match", "1.2.3", "1.2.3", true, false},
		{"exact non-match", "1.2.4", "1.2.3", false, false},
		{"exact equal-prefix", "1.2.3", "=1.2.3", true, false},

		// Caret
		{"caret match same minor", "1.2.5", "^1.2.0", true, false},
		{"caret match newer minor", "1.5.0", "^1.2.0", true, false},
		{"caret reject below base", "1.1.0", "^1.2.0", false, false},
		{"caret reject next major", "2.0.0", "^1.2.0", false, false},
		{"caret-zero same minor", "0.2.5", "^0.2.0", true, false},
		{"caret-zero reject next minor", "0.3.0", "^0.2.0", false, false},

		// Tilde
		{"tilde same minor higher patch", "1.2.5", "~1.2.0", true, false},
		{"tilde reject next minor", "1.3.0", "~1.2.0", false, false},
		{"tilde reject below base", "1.1.9", "~1.2.0", false, false},

		// Range
		{"compound min and max", "1.5.0", ">=1.0.0 <2.0.0", true, false},
		{"compound exclude upper", "2.0.0", ">=1.0.0 <2.0.0", false, false},
		{"compound exclude lower", "0.9.9", ">=1.0.0 <2.0.0", false, false},
		{">= equal", "1.0.0", ">=1.0.0", true, false},
		{"> strict reject equal", "1.0.0", ">1.0.0", false, false},
		{"<= equal", "2.0.0", "<=2.0.0", true, false},
		{"< strict reject equal", "2.0.0", "<2.0.0", false, false},

		// Wildcard
		{"star accepts anything", "999.0.0", "*", true, false},

		// Whitespace forgiveness
		{"operator with space", "1.0.0", ">=  1.0.0", true, false},

		// Errors
		{"empty expr", "1.0.0", "", false, true},
		{"empty version", "", "^1.0.0", false, true},
		{"|| union unsupported", "1.0.0", "^1.0.0 || ^2.0.0", false, true},
		{"bogus base", "1.0.0", "^abc", false, true},
		{"bogus version", "junk", "1.0.0", false, true},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, err := matchRange(c.version, c.expr)
			if c.wantErr {
				if err == nil {
					t.Fatalf("want error, got nil (match=%v)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Errorf("matchRange(%q, %q) = %v, want %v", c.version, c.expr, got, c.want)
			}
		})
	}
}
