package templates

import (
	"errors"
	"strings"
	"testing"
)

// mapFiles is an in-memory ThemeFiles implementation for tests. The
// zero value is a usable (but empty) theme; populate via the helper
// constructors below to keep the table cases readable.
type mapFiles map[string]struct{}

// newFiles returns a mapFiles populated with the given filenames.
// Passing zero arguments yields an empty theme (Resolve should
// always return ErrNoIndex against it).
func newFiles(names ...string) mapFiles {
	m := make(mapFiles, len(names))
	for _, n := range names {
		m[n] = struct{}{}
	}
	return m
}

// Has implements ThemeFiles.
func (m mapFiles) Has(filename string) bool {
	_, ok := m[filename]
	return ok
}

// TestRequestType_String pins the stable string forms of each
// RequestType. The strings are part of the package's public surface
// (callers log them, tests fixture against them), so a rename should
// trip a review.
func TestRequestType_String(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   RequestType
		want string
	}{
		{RequestTypeUnknown, "unknown"},
		{RequestTypeSingular, "singular"},
		{RequestTypeArchive, "archive"},
		{RequestTypeTaxonomy, "taxonomy"},
		{RequestTypeAuthor, "author"},
		{RequestTypeDate, "date"},
		{RequestTypeSearch, "search"},
		{RequestTypeHome, "home"},
		{RequestTypeFrontPage, "front-page"},
		{RequestTypeNotFound, "404"},
		{RequestType(99), "unknown"},
	}
	for _, c := range cases {
		if got := c.in.String(); got != c.want {
			t.Errorf("RequestType(%d).String() = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestDefaultResolver_Resolve is the main table-driven coverage
// matrix. For every RequestType we exercise:
//
//   - the most-specific candidate wins (precedence ordering)
//   - an intermediate-fallback case (skip the most-specific)
//   - only-index fallback (every type drops to index)
//
// Plus shared edge cases at the end (no files → ErrNoIndex, unknown
// type → ErrUnknownRequestType, .html fallback, mixed .tsx beats
// .html).
func TestDefaultResolver_Resolve(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		req     Request
		files   mapFiles
		want    string
		wantErr error
	}{
		// ─── Singular ───────────────────────────────────────────────
		{
			name: "singular: most-specific single-{type}-{slug} wins",
			req: Request{
				Type:     RequestTypeSingular,
				PostType: "book",
				PostSlug: "intro-to-cooking",
			},
			files: newFiles(
				"single-book-intro-to-cooking.tsx",
				"single-book.tsx",
				"single.tsx",
				"singular.tsx",
				"index.tsx",
			),
			want: "single-book-intro-to-cooking.tsx",
		},
		{
			name: "singular: single-{type} beats single when slug template missing",
			req: Request{
				Type:     RequestTypeSingular,
				PostType: "book",
				PostSlug: "intro-to-cooking",
			},
			files: newFiles(
				"single-book.tsx",
				"single.tsx",
				"singular.tsx",
				"index.tsx",
			),
			want: "single-book.tsx",
		},
		{
			name: "singular: single beats singular when both present",
			req: Request{
				Type:     RequestTypeSingular,
				PostType: "book",
				PostSlug: "intro-to-cooking",
			},
			files: newFiles(
				"single.tsx",
				"singular.tsx",
				"index.tsx",
			),
			want: "single.tsx",
		},
		{
			name: "singular: singular fires after single is absent",
			req: Request{
				Type:     RequestTypeSingular,
				PostType: "book",
				PostSlug: "intro-to-cooking",
			},
			files: newFiles(
				"singular.tsx",
				"index.tsx",
			),
			want: "singular.tsx",
		},
		{
			name: "singular: PostID variant resolves to single-{type}-{id}",
			req: Request{
				Type:     RequestTypeSingular,
				PostType: "book",
				PostID:   "42",
			},
			files: newFiles(
				"single-book-42.tsx",
				"single-book.tsx",
				"index.tsx",
			),
			want: "single-book-42.tsx",
		},
		{
			name: "singular: slug wins over id when both are set and both files exist",
			req: Request{
				Type:     RequestTypeSingular,
				PostType: "book",
				PostSlug: "intro-to-cooking",
				PostID:   "42",
			},
			files: newFiles(
				"single-book-intro-to-cooking.tsx",
				"single-book-42.tsx",
				"single-book.tsx",
				"index.tsx",
			),
			want: "single-book-intro-to-cooking.tsx",
		},
		{
			name: "singular: drops all the way to index",
			req: Request{
				Type:     RequestTypeSingular,
				PostType: "book",
				PostSlug: "intro-to-cooking",
			},
			files: newFiles("index.tsx"),
			want:  "index.tsx",
		},

		// ─── Archive ────────────────────────────────────────────────
		{
			name: "archive: most-specific archive-{type} wins",
			req: Request{
				Type:     RequestTypeArchive,
				PostType: "book",
			},
			files: newFiles(
				"archive-book.tsx",
				"archive.tsx",
				"index.tsx",
			),
			want: "archive-book.tsx",
		},
		{
			name: "archive: archive fires when archive-{type} is absent",
			req: Request{
				Type:     RequestTypeArchive,
				PostType: "book",
			},
			files: newFiles(
				"archive.tsx",
				"index.tsx",
			),
			want: "archive.tsx",
		},
		{
			name: "archive: drops to index",
			req: Request{
				Type:     RequestTypeArchive,
				PostType: "book",
			},
			files: newFiles("index.tsx"),
			want:  "index.tsx",
		},

		// ─── Taxonomy ───────────────────────────────────────────────
		{
			name: "taxonomy: most-specific taxonomy-{tax}-{term} wins",
			req: Request{
				Type:         RequestTypeTaxonomy,
				TaxonomySlug: "genre",
				TermSlug:     "cookbooks",
			},
			files: newFiles(
				"taxonomy-genre-cookbooks.tsx",
				"taxonomy-genre.tsx",
				"taxonomy.tsx",
				"archive.tsx",
				"index.tsx",
			),
			want: "taxonomy-genre-cookbooks.tsx",
		},
		{
			name: "taxonomy: taxonomy-{tax} beats taxonomy when term file missing",
			req: Request{
				Type:         RequestTypeTaxonomy,
				TaxonomySlug: "genre",
				TermSlug:     "cookbooks",
			},
			files: newFiles(
				"taxonomy-genre.tsx",
				"taxonomy.tsx",
				"index.tsx",
			),
			want: "taxonomy-genre.tsx",
		},
		{
			name: "taxonomy: taxonomy beats archive when both present",
			req: Request{
				Type:         RequestTypeTaxonomy,
				TaxonomySlug: "genre",
				TermSlug:     "cookbooks",
			},
			files: newFiles(
				"taxonomy.tsx",
				"archive.tsx",
				"index.tsx",
			),
			want: "taxonomy.tsx",
		},
		{
			name: "taxonomy: archive fires after taxonomy chain is absent",
			req: Request{
				Type:         RequestTypeTaxonomy,
				TaxonomySlug: "genre",
				TermSlug:     "cookbooks",
			},
			files: newFiles(
				"archive.tsx",
				"index.tsx",
			),
			want: "archive.tsx",
		},
		{
			name: "taxonomy: drops to index",
			req: Request{
				Type:         RequestTypeTaxonomy,
				TaxonomySlug: "genre",
				TermSlug:     "cookbooks",
			},
			files: newFiles("index.tsx"),
			want:  "index.tsx",
		},

		// ─── Author ─────────────────────────────────────────────────
		{
			name: "author: numeric id wins over slug when both files exist",
			req: Request{
				Type:     RequestTypeAuthor,
				AuthorID: "42",
				PostSlug: "alice",
			},
			files: newFiles(
				"author-42.tsx",
				"author-alice.tsx",
				"author.tsx",
				"archive.tsx",
				"index.tsx",
			),
			want: "author-42.tsx",
		},
		{
			name: "author: handle fires when numeric file is missing",
			req: Request{
				Type:     RequestTypeAuthor,
				AuthorID: "42",
				PostSlug: "alice",
			},
			files: newFiles(
				"author-alice.tsx",
				"author.tsx",
				"index.tsx",
			),
			want: "author-alice.tsx",
		},
		{
			name: "author: bare author beats archive",
			req: Request{
				Type:     RequestTypeAuthor,
				AuthorID: "42",
			},
			files: newFiles(
				"author.tsx",
				"archive.tsx",
				"index.tsx",
			),
			want: "author.tsx",
		},
		{
			name: "author: archive fires when author chain is absent",
			req: Request{
				Type:     RequestTypeAuthor,
				AuthorID: "42",
			},
			files: newFiles(
				"archive.tsx",
				"index.tsx",
			),
			want: "archive.tsx",
		},
		{
			name: "author: drops to index",
			req: Request{
				Type:     RequestTypeAuthor,
				AuthorID: "42",
			},
			files: newFiles("index.tsx"),
			want:  "index.tsx",
		},

		// ─── Date ───────────────────────────────────────────────────
		{
			name: "date: date wins when present",
			req:  Request{Type: RequestTypeDate},
			files: newFiles(
				"date.tsx",
				"archive.tsx",
				"index.tsx",
			),
			want: "date.tsx",
		},
		{
			name: "date: archive fires when date is absent",
			req:  Request{Type: RequestTypeDate},
			files: newFiles(
				"archive.tsx",
				"index.tsx",
			),
			want: "archive.tsx",
		},
		{
			name:  "date: drops to index",
			req:   Request{Type: RequestTypeDate},
			files: newFiles("index.tsx"),
			want:  "index.tsx",
		},

		// ─── Search ─────────────────────────────────────────────────
		{
			name: "search: search wins when present",
			req:  Request{Type: RequestTypeSearch},
			files: newFiles(
				"search.tsx",
				"index.tsx",
			),
			want: "search.tsx",
		},
		{
			name:  "search: drops to index",
			req:   Request{Type: RequestTypeSearch},
			files: newFiles("index.tsx"),
			want:  "index.tsx",
		},

		// ─── Home ───────────────────────────────────────────────────
		{
			name: "home: home wins when present",
			req:  Request{Type: RequestTypeHome, IsHome: true},
			files: newFiles(
				"home.tsx",
				"index.tsx",
			),
			want: "home.tsx",
		},
		{
			name:  "home: drops to index",
			req:   Request{Type: RequestTypeHome, IsHome: true},
			files: newFiles("index.tsx"),
			want:  "index.tsx",
		},

		// ─── FrontPage ──────────────────────────────────────────────
		{
			name: "front-page: front-page wins when present",
			req:  Request{Type: RequestTypeFrontPage, IsFront: true},
			files: newFiles(
				"front-page.tsx",
				"home.tsx",
				"index.tsx",
			),
			want: "front-page.tsx",
		},
		{
			name: "front-page: home fires when front-page is absent",
			req:  Request{Type: RequestTypeFrontPage, IsFront: true},
			files: newFiles(
				"home.tsx",
				"index.tsx",
			),
			want: "home.tsx",
		},
		{
			name:  "front-page: drops to index",
			req:   Request{Type: RequestTypeFrontPage, IsFront: true},
			files: newFiles("index.tsx"),
			want:  "index.tsx",
		},

		// ─── 404 ────────────────────────────────────────────────────
		{
			name: "404: 404.tsx wins when present",
			req:  Request{Type: RequestTypeNotFound, Is404: true},
			files: newFiles(
				"404.tsx",
				"index.tsx",
			),
			want: "404.tsx",
		},
		{
			name:  "404: drops to index",
			req:   Request{Type: RequestTypeNotFound, Is404: true},
			files: newFiles("index.tsx"),
			want:  "index.tsx",
		},

		// ─── Error / fallback cases ─────────────────────────────────
		{
			name:    "error: no files at all",
			req:     Request{Type: RequestTypeSingular, PostType: "book", PostSlug: "x"},
			files:   newFiles(),
			wantErr: ErrNoIndex,
		},
		{
			name:    "error: chain is satisfied but index is missing",
			req:     Request{Type: RequestTypeSearch},
			files:   newFiles("search-only-but-no-fallthrough-impossible"),
			wantErr: ErrNoIndex,
		},
		{
			name:    "error: unknown request type",
			req:     Request{Type: RequestTypeUnknown},
			files:   newFiles("index.tsx"),
			wantErr: ErrUnknownRequestType,
		},
		{
			name:    "error: unrecognised future RequestType",
			req:     Request{Type: RequestType(123)},
			files:   newFiles("index.tsx"),
			wantErr: ErrUnknownRequestType,
		},

		// ─── .html extension fallback (classic themes) ──────────────
		{
			name: "html-only: index.html is accepted as ultimate fallback",
			req: Request{
				Type:     RequestTypeSingular,
				PostType: "book",
				PostSlug: "intro-to-cooking",
			},
			files: newFiles("index.html"),
			want:  "index.html",
		},
		{
			name: "html: single-{type}-{slug}.html wins when the .tsx variant is absent",
			req: Request{
				Type:     RequestTypeSingular,
				PostType: "book",
				PostSlug: "intro-to-cooking",
			},
			files: newFiles(
				"single-book-intro-to-cooking.html",
				"single-book.tsx",
				"index.tsx",
			),
			want: "single-book-intro-to-cooking.html",
		},
		{
			name: "mixed: .tsx of a given candidate beats .html of the same candidate",
			req: Request{
				Type:     RequestTypeSingular,
				PostType: "book",
				PostSlug: "intro-to-cooking",
			},
			files: newFiles(
				"single-book-intro-to-cooking.tsx",
				"single-book-intro-to-cooking.html",
				"index.tsx",
			),
			want: "single-book-intro-to-cooking.tsx",
		},
		{
			name: "mixed: .html of a MORE-specific candidate beats .tsx of a LESS-specific one",
			req: Request{
				Type:     RequestTypeSingular,
				PostType: "book",
				PostSlug: "intro-to-cooking",
			},
			files: newFiles(
				"single-book-intro-to-cooking.html",
				"single-book.tsx",
				"index.tsx",
			),
			want: "single-book-intro-to-cooking.html",
		},

		// ─── Archive without PostType (bare /archive) ───────────────
		{
			name:  "archive: PostType absent → falls to archive then index",
			req:   Request{Type: RequestTypeArchive},
			files: newFiles("archive.tsx", "index.tsx"),
			want:  "archive.tsx",
		},

		// ─── Taxonomy without TermSlug (term-less taxonomy view) ────
		{
			name: "taxonomy: TermSlug absent → taxonomy-{tax} is the most-specific candidate",
			req: Request{
				Type:         RequestTypeTaxonomy,
				TaxonomySlug: "genre",
			},
			files: newFiles(
				"taxonomy-genre.tsx",
				"taxonomy.tsx",
				"index.tsx",
			),
			want: "taxonomy-genre.tsx",
		},
	}

	r := NewDefaultResolver()
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, err := r.Resolve(c.req, c.files)
			if c.wantErr != nil {
				if !errors.Is(err, c.wantErr) {
					t.Fatalf("Resolve(%s) error = %v, want %v", c.name, err, c.wantErr)
				}
				if got != "" {
					t.Errorf("Resolve(%s) returned %q with an error; expected empty string", c.name, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve(%s) unexpected error: %v", c.name, err)
			}
			if got != c.want {
				t.Errorf("Resolve(%s) = %q, want %q", c.name, got, c.want)
			}
		})
	}
}

// TestDefaultResolver_NilFiles asserts that a nil ThemeFiles isn't
// silently treated as "empty theme" — it's a programmer error
// (someone forgot to pass the active theme's file index), and we
// flag it the same way we flag a malformed theme.
func TestDefaultResolver_NilFiles(t *testing.T) {
	t.Parallel()
	r := NewDefaultResolver()
	got, err := r.Resolve(Request{Type: RequestTypeSearch}, nil)
	if !errors.Is(err, ErrNoIndex) {
		t.Fatalf("Resolve(nil files) error = %v, want ErrNoIndex", err)
	}
	if got != "" {
		t.Errorf("Resolve(nil files) returned %q; expected empty", got)
	}
}

// TestResolver_InterfaceCompliance is a compile-time guard: the test
// won't build if DefaultResolver stops satisfying Resolver, or if
// mapFiles stops satisfying ThemeFiles. The body runs zero
// assertions; it exists for its declaration alone.
func TestResolver_InterfaceCompliance(t *testing.T) {
	t.Parallel()
	var _ Resolver = (*DefaultResolver)(nil)
	var _ Resolver = DefaultResolver{}
	var _ ThemeFiles = mapFiles{}
}

// TestDefaultResolver_PrecedenceProperty asserts the structural
// invariant the task spec calls out: removing any single template
// from a perfectly-stocked theme shifts the resolved name to the
// NEXT entry in the precedence list. This is the property test that
// catches accidental reorderings of buildCandidates branches.
func TestDefaultResolver_PrecedenceProperty(t *testing.T) {
	t.Parallel()

	// A canonical "every candidate present" theme for the Singular
	// case. Walking down it verifies the exact precedence the docs
	// state.
	allSingular := []string{
		"single-book-intro-to-cooking.tsx",
		"single-book.tsx",
		"single.tsx",
		"singular.tsx",
		"index.tsx",
	}
	req := Request{
		Type:     RequestTypeSingular,
		PostType: "book",
		PostSlug: "intro-to-cooking",
	}

	r := NewDefaultResolver()
	// Start with every file present; the most-specific should win.
	files := newFiles(allSingular...)
	for i, expect := range allSingular {
		got, err := r.Resolve(req, files)
		if err != nil {
			t.Fatalf("step %d: unexpected error: %v", i, err)
		}
		if got != expect {
			t.Fatalf("step %d (after removing %d files): got %q, want %q (current files: %v)",
				i, i, got, expect, sortedKeys(files))
		}
		// Knock out the file we just matched on; on the next loop
		// iteration, Resolve must drop to the next entry in the list.
		delete(files, expect)
	}
}

// TestDefaultResolver_TaxonomyPrecedenceProperty applies the same
// "walk-down-the-list" property check to the Taxonomy hierarchy,
// which is the deepest one (five entries).
func TestDefaultResolver_TaxonomyPrecedenceProperty(t *testing.T) {
	t.Parallel()

	all := []string{
		"taxonomy-genre-cookbooks.tsx",
		"taxonomy-genre.tsx",
		"taxonomy.tsx",
		"archive.tsx",
		"index.tsx",
	}
	req := Request{
		Type:         RequestTypeTaxonomy,
		TaxonomySlug: "genre",
		TermSlug:     "cookbooks",
	}

	r := NewDefaultResolver()
	files := newFiles(all...)
	for i, expect := range all {
		got, err := r.Resolve(req, files)
		if err != nil {
			t.Fatalf("step %d: unexpected error: %v", i, err)
		}
		if got != expect {
			t.Fatalf("step %d: got %q, want %q (files left: %v)",
				i, got, expect, sortedKeys(files))
		}
		delete(files, expect)
	}

	// After every file is removed, the theme is malformed → error.
	if _, err := r.Resolve(req, files); !errors.Is(err, ErrNoIndex) {
		t.Fatalf("after exhausting all files, want ErrNoIndex, got %v", err)
	}
}

// TestBuildCandidates_UnknownReturnsNil pins the contract that
// buildCandidates returns nil (not an empty slice) for unrecognised
// types. The distinction matters because Resolve uses it to choose
// between ErrUnknownRequestType and ErrNoIndex.
func TestBuildCandidates_UnknownReturnsNil(t *testing.T) {
	t.Parallel()
	if got := buildCandidates(Request{Type: RequestTypeUnknown}); got != nil {
		t.Errorf("buildCandidates(unknown) = %v, want nil", got)
	}
	if got := buildCandidates(Request{Type: RequestType(-1)}); got != nil {
		t.Errorf("buildCandidates(-1) = %v, want nil", got)
	}
}

// TestBuildCandidates_AlwaysEndsWithIndex pins the structural
// invariant that every recognised RequestType's precedence list
// terminates at "index". Themes rely on this — that's why "index"
// is mandatory in §4.1.
func TestBuildCandidates_AlwaysEndsWithIndex(t *testing.T) {
	t.Parallel()
	types := []RequestType{
		RequestTypeSingular,
		RequestTypeArchive,
		RequestTypeTaxonomy,
		RequestTypeAuthor,
		RequestTypeDate,
		RequestTypeSearch,
		RequestTypeHome,
		RequestTypeFrontPage,
		RequestTypeNotFound,
	}
	for _, rt := range types {
		got := buildCandidates(Request{Type: rt})
		if len(got) == 0 {
			t.Errorf("%s: buildCandidates returned empty", rt)
			continue
		}
		if last := got[len(got)-1]; last != "index" {
			t.Errorf("%s: precedence list ends with %q, want %q", rt, last, "index")
		}
	}
}

// sortedKeys is a tiny debug helper for the property tests so a
// failure prints the remaining files in deterministic order.
func sortedKeys(m mapFiles) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// Simple insertion sort; the slice is small (≤ ~10 entries) and
	// we don't want to drag in "sort" for one helper.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && strings.Compare(out[j-1], out[j]) > 0; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}
