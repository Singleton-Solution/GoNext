package search

import "time"

// Type discriminates the corpus an individual Hit came from. The
// values mirror the strings used in posts.post_type plus the
// taxonomy-term placeholder for future expansion. Today only "post"
// and "page" are populated; "term" is reserved so adding it later
// does not break the JSON contract.
const (
	TypePost = "post"
	TypePage = "page"
	TypeTerm = "term"
)

// Hit is one row of search results. The shape is the on-the-wire
// JSON for both /api/v1/admin/search and /api/v1/search — keeping a
// single struct guarantees the two surfaces never drift.
//
// ExcerptHTML is pre-sanitised, pre-<mark>-wrapped HTML. The
// highlight helper escapes any user-supplied HTML before wrapping
// terms, so consumers can drop the field straight into a server-side
// template that allows raw HTML. Clients that render React should
// pass it through dangerouslySetInnerHTML; the highlight helper
// guarantees the only HTML it produces is <mark>…</mark>.
//
// Rank is the raw ts_rank score from Postgres. Higher is better but
// the absolute number is meaningless across queries — only the
// relative ordering within one Results envelope matters. Clients
// should not surface it directly; it's exposed so an admin debug
// surface can display it.
//
// MatchedTerms is the set of distinct lexemes from the query that
// landed on this row. It is informational only — the relevance
// ordering already accounts for it — but the admin UI uses it to
// render per-term chips next to the result.
type Hit struct {
	ID           string   `json:"id"`
	Type         string   `json:"type"`
	Slug         string   `json:"slug"`
	Title        string   `json:"title"`
	ExcerptHTML  string   `json:"excerpt_html"`
	Rank         float64  `json:"rank"`
	MatchedTerms []string `json:"matched_terms,omitempty"`
}

// Results is the envelope returned by Search. The shape is
// deliberately small: a list of hits, a count, and a duration. The
// count is the number of matching rows after filtering but before
// the LIMIT/OFFSET window — i.e. what the UI needs to render
// pagination, not len(Hits).
//
// Total = -1 signals "uncounted". Computing an exact total requires
// a second query (Postgres has no cheap cardinality for arbitrary
// FTS predicates); callers that don't need pagination can opt out via
// SearchOpts.SkipTotal and read -1 here. The default is to compute it.
type Results struct {
	Hits          []Hit         `json:"hits"`
	Total         int           `json:"total"`
	QueryDuration time.Duration `json:"query_duration_ms,omitempty"`
}

// SearchOpts narrows a Search call. The zero value is a sensible
// default: no type filter (every searchable post_type), no status
// filter, limit = DefaultLimit, offset = 0.
//
// The Status field exists for the public-search handler, which
// always pins it to "published". Admin callers pass "" so drafts and
// scheduled posts are searchable too.
//
// SkipTotal short-circuits the COUNT query — pass true when the
// caller doesn't render pagination (the cmd+k modal, for example,
// shows the first N results and no "1 of 137" line).
type SearchOpts struct {
	Types      []string
	Status     string
	Limit      int
	Offset     int
	SkipTotal  bool
}

// DefaultLimit is the page size when SearchOpts.Limit is zero or
// negative. Chosen to fit a typical modal viewport without
// scrolling; the full-page admin /search route requests larger
// pages explicitly.
const DefaultLimit = 20

// MaxLimit is the hard upper bound on a single Search call. Higher
// values are silently clamped so a malicious client cannot ask for
// 10_000 rows in one round-trip. The full corpus is reachable via
// OFFSET pagination.
const MaxLimit = 100
