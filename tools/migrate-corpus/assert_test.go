package main

import (
	"strings"
	"testing"
)

// TestParseReport_HappyPath pins the dry-run output format we depend
// on. If the CLI's report format changes, this test catches it before
// the workflow fails on every fixture.
func TestParseReport_HappyPath(t *testing.T) {
	t.Parallel()
	in := `[dry-run] import summary
  authors:     5
  categories:  3
  tags:        7
  posts:       42
  attachments: 4
  comments:    11
  errors:      0
  took:        12.3ms
`
	got, err := parseReport(in)
	if err != nil {
		t.Fatalf("parseReport: %v", err)
	}
	want := Actual{
		Authors:     5,
		Categories:  3,
		Tags:        7,
		Posts:       42,
		Attachments: 4,
		Comments:    11,
		Errors:      0,
	}
	if got != want {
		t.Fatalf("\ngot:  %+v\nwant: %+v", got, want)
	}
}

func TestParseReport_IgnoresNoise(t *testing.T) {
	t.Parallel()
	in := `gonext migrate wp: starting...
some other line
  authors:     1
nothing on this line
  posts: 9
=== end ===
`
	got, err := parseReport(in)
	if err != nil {
		t.Fatalf("parseReport: %v", err)
	}
	if got.Authors != 1 {
		t.Errorf("authors: got %d want 1", got.Authors)
	}
	if got.Posts != 9 {
		t.Errorf("posts: got %d want 9", got.Posts)
	}
}

func TestDiffActual_Match(t *testing.T) {
	t.Parallel()
	e := Expected{
		Authors: 1, Categories: 0, Tags: 0,
		Posts: 1, Attachments: 0, Comments: 0, ErrorsMax: 0,
	}
	a := Actual{
		Authors: 1, Posts: 1,
	}
	if got := diffActual(e, a); got != "" {
		t.Fatalf("expected empty diff, got %q", got)
	}
}

func TestDiffActual_Mismatch(t *testing.T) {
	t.Parallel()
	e := Expected{Authors: 1, Posts: 5}
	a := Actual{Authors: 1, Posts: 3}
	got := diffActual(e, a)
	if !strings.Contains(got, "posts") {
		t.Fatalf("diff should mention posts, got %q", got)
	}
	if !strings.Contains(got, "5") || !strings.Contains(got, "3") {
		t.Fatalf("diff should include counts, got %q", got)
	}
}

func TestDiffActual_ErrorsCeiling(t *testing.T) {
	t.Parallel()
	e := Expected{ErrorsMax: 2}
	if d := diffActual(e, Actual{Errors: 2}); d != "" {
		t.Errorf("at-ceiling should pass, got %q", d)
	}
	if d := diffActual(e, Actual{Errors: 3}); !strings.Contains(d, "errors") {
		t.Errorf("over-ceiling should fail with errors mention, got %q", d)
	}
}

func TestLoadExpected_Roundtrip(t *testing.T) {
	t.Parallel()
	// One of the real fixture files we shipped.
	got, err := loadExpected("fixtures/expected/01-tiny-blog.json")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Authors != 1 {
		t.Errorf("authors: got %d want 1", got.Authors)
	}
	if got.Posts != 1 {
		t.Errorf("posts: got %d want 1", got.Posts)
	}
}
