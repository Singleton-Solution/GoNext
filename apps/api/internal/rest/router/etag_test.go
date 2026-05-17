package router

import (
	"errors"
	"net/http/httptest"
	"testing"
)

func TestFormatETag(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"abc", `"abc"`},
		{"42", `"42"`},
	}
	for _, c := range cases {
		if got := FormatETag(c.in); got != c.want {
			t.Errorf("FormatETag(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseETag(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in, want string
	}{
		{"", ""},
		{`"abc"`, "abc"},
		{`W/"abc"`, "abc"},
		{"abc", "abc"}, // tolerate unquoted
		{`  "abc"  `, "abc"},
	}
	for _, c := range cases {
		if got := ParseETag(c.in); got != c.want {
			t.Errorf("ParseETag(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHashETag(t *testing.T) {
	t.Parallel()

	if got := HashETag(nil); got != "" {
		t.Errorf("HashETag(nil) = %q, want empty", got)
	}
	if got := HashETag([]byte{0xde, 0xad, 0xbe, 0xef}); got != `"deadbeef"` {
		t.Errorf("HashETag([deadbeef]) = %q", got)
	}
}

func TestVersionETag(t *testing.T) {
	t.Parallel()

	if got, want := VersionETag(7), `"7"`; got != want {
		t.Errorf("VersionETag(7) = %q, want %q", got, want)
	}
}

func TestParseIfMatchVersion_Absent(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest("PATCH", "/x", nil)
	n, present, err := ParseIfMatchVersion(r)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if present {
		t.Errorf("present = true, want false")
	}
	if n != 0 {
		t.Errorf("n = %d, want 0", n)
	}
}

func TestParseIfMatchVersion_Present(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest("PATCH", "/x", nil)
	r.Header.Set("If-Match", `"3"`)
	n, present, err := ParseIfMatchVersion(r)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !present || n != 3 {
		t.Errorf("got (%d, %v), want (3, true)", n, present)
	}
}

func TestParseIfMatchVersion_Wildcard(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest("PATCH", "/x", nil)
	r.Header.Set("If-Match", "*")
	n, present, err := ParseIfMatchVersion(r)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !present || n != 0 {
		t.Errorf("got (%d, %v), want (0, true)", n, present)
	}
}

func TestParseIfMatchVersion_Invalid(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest("PATCH", "/x", nil)
	r.Header.Set("If-Match", `"not-an-int"`)
	_, _, err := ParseIfMatchVersion(r)
	if !errors.Is(err, ErrInvalidIfMatch) {
		t.Errorf("err = %v, want ErrInvalidIfMatch", err)
	}
}

func TestMatchesIfNoneMatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		current string
		header  string
		want    bool
	}{
		{"no header", `"abc"`, "", false},
		{"no current", "", `"abc"`, false},
		{"exact", `"abc"`, `"abc"`, true},
		{"wildcard", `"abc"`, "*", true},
		{"list match", `"abc"`, `"x", "abc", "y"`, true},
		{"list no match", `"abc"`, `"x", "y"`, false},
		{"unquoted current vs quoted header", `"abc"`, "abc", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/x", nil)
			if tc.header != "" {
				r.Header.Set("If-None-Match", tc.header)
			}
			if got := MatchesIfNoneMatch(r, tc.current); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSetETag(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	SetETag(rec, "")
	if rec.Header().Get("ETag") != "" {
		t.Errorf("empty SetETag set the header")
	}
	SetETag(rec, `"abc"`)
	if rec.Header().Get("ETag") != `"abc"` {
		t.Errorf("SetETag did not set the header")
	}
}
