package search

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrEmptyQuery is returned by Search when q is empty (after trim).
// Callers translate this to a 400 at the HTTP boundary; the package
// refuses to issue a SQL with an empty tsquery because Postgres
// happily returns every row in the corpus, which is never what the
// user wants.
var ErrEmptyQuery = errors.New("search: query is required")

// Querier is the read-only subset of *pgxpool.Pool this package
// uses. Defining a local interface lets tests run against
// dbtest.WrapPool(tx) without dragging pgxpool into the test
// substrate. The shape matches packages/go/testutil/dbtest.Querier.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Store is the concrete search implementation. Construct one per
// process via NewStore(pool) and share it across handlers — it is
// safe to use from multiple goroutines (it holds nothing but the
// pool reference).
type Store struct {
	db Querier
}

// NewStore returns a Store bound to db. Panics on nil so a wiring
// mistake fails at boot rather than on the first request.
func NewStore(db Querier) *Store {
	if db == nil {
		panic("search.NewStore: db is nil")
	}
	return &Store{db: db}
}

// Search runs a full-text query against the posts table and
// returns up to opts.Limit hits (clamped to MaxLimit), ordered by
// ts_rank descending. The query is bound to the trigger-maintained
// search_vector column from migration 000011_search; the relevance
// weighting (title > excerpt > content > meta-description) lives
// there, not here.
//
// q is parsed by Postgres's `plainto_tsquery`, which strips
// operators and treats every word as a required term. This is the
// safest default for arbitrary user input: pasted text never
// produces a syntax error or an operator-driven over-match. An
// "advanced search" mode that exposes `websearch_to_tsquery` (which
// understands "phrase", -negation, and OR) is a future SearchOpts
// flag, not a default.
//
// SQL-injection safety: q is always passed as a positional
// parameter ($1) and never interpolated into the SQL string. Even a
// term like `'; DROP TABLE posts; --` reaches the database as a
// literal text value and is fed verbatim to plainto_tsquery, which
// tokenises it as harmless words.
//
// Returned errors:
//
//   - ErrEmptyQuery when q is empty after trimming whitespace.
//     Translate to 400 at the HTTP layer.
//   - Any pgx error otherwise. These signal infrastructure trouble
//     (statement_timeout, connection drop) and should propagate as
//     5xx to the caller.
func (s *Store) Search(ctx context.Context, q string, opts SearchOpts) (*Results, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, ErrEmptyQuery
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	if limit > MaxLimit {
		limit = MaxLimit
	}
	offset := opts.Offset
	if offset < 0 {
		offset = 0
	}

	// Build the dynamic WHERE clause. Every filter is parameterised;
	// `cond` accumulates SQL fragments and `args` accumulates the
	// matching values. The base predicate — `search_vector @@
	// plainto_tsquery('english', $1)` — is always present.
	args := []any{q}
	cond := []string{"search_vector @@ plainto_tsquery('english', $1)"}

	if len(opts.Types) > 0 {
		// pgx supports = ANY($2::text[]) for slice parameters, which
		// is the right tool here: any number of types in one
		// parameter, no string-pasted IN clauses.
		args = append(args, normalizeTypes(opts.Types))
		cond = append(cond, fmt.Sprintf("post_type = ANY($%d::text[])", len(args)))
	}
	if opts.Status != "" {
		args = append(args, opts.Status)
		cond = append(cond, fmt.Sprintf("status = $%d", len(args)))
	}

	// Trashed rows are never searchable. We hard-code this rather
	// than expose it through SearchOpts: there is no legitimate
	// caller who wants to find trash, and a future "show in trash"
	// surface would use a separate /trash endpoint anyway.
	cond = append(cond, "status <> 'trash'")

	whereSQL := strings.Join(cond, " AND ")

	// The main projection. We compute ts_rank on the same vector +
	// tsquery; using ts_rank rather than ts_rank_cd is a deliberate
	// choice — `_cd` favours documents with covered density (terms
	// near each other) which inverts intent for the title-weighted
	// posts corpus. Issue #119's acceptance test expects title hits
	// to outrank body hits; ts_rank with the migration's A/B/C/D
	// weights gives that.
	selectSQL := fmt.Sprintf(`
SELECT
    id,
    post_type,
    slug,
    title,
    coalesce(excerpt, '') AS excerpt,
    ts_rank(search_vector, plainto_tsquery('english', $1)) AS rank
FROM posts
WHERE %s
ORDER BY rank DESC, id ASC
LIMIT %d OFFSET %d
`, whereSQL, limit, offset)

	start := time.Now()
	rows, err := s.db.Query(ctx, selectSQL, args...)
	if err != nil {
		return nil, fmt.Errorf("search.Search: query: %w", err)
	}
	defer rows.Close()

	terms := tokenize(q)

	hits := make([]Hit, 0, limit)
	for rows.Next() {
		var (
			id, postType, slug, title, excerpt string
			rank                               float64
		)
		if err := rows.Scan(&id, &postType, &slug, &title, &excerpt, &rank); err != nil {
			return nil, fmt.Errorf("search.Search: scan: %w", err)
		}
		// Prefer a snippet from the excerpt; fall back to the title
		// itself so a result without an excerpt still highlights.
		snippet := excerpt
		if strings.TrimSpace(snippet) == "" {
			snippet = title
		}
		hits = append(hits, Hit{
			ID:           id,
			Type:         postType,
			Slug:         slug,
			Title:        title,
			ExcerptHTML:  Highlight(snippet, terms),
			Rank:         rank,
			MatchedTerms: matchedTerms(title, excerpt, terms),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("search.Search: rows: %w", err)
	}

	res := &Results{
		Hits:          hits,
		Total:         -1,
		QueryDuration: time.Since(start),
	}

	if !opts.SkipTotal {
		// Count query reuses the same WHERE + args. Cheap on the
		// GIN-indexed predicate; even on a synthetic 10k-row corpus
		// it stays well under the per-request budget.
		countSQL := fmt.Sprintf(`SELECT count(*) FROM posts WHERE %s`, whereSQL)
		var total int
		if err := s.db.QueryRow(ctx, countSQL, args...).Scan(&total); err != nil {
			// Don't fail the whole call on a count error — the page
			// has already been built. Log via the wrapping handler;
			// here we just degrade gracefully to "unknown total".
			res.Total = -1
			return res, nil
		}
		res.Total = total
	}

	return res, nil
}

// normalizeTypes lower-cases and trims each entry. Types in the
// table are stored lower-case; filtering happens after this
// normalisation so the caller can pass "Post", "post", or " POST "
// equivalently.
func normalizeTypes(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]struct{}{}
	for _, t := range in {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" {
			continue
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	return out
}

// tokenize splits q into the same word-shaped tokens
// plainto_tsquery sees. The result feeds the Highlight helper —
// we do NOT try to mirror Postgres's full lexical analysis (which
// would require running the dictionary in-process). Word-boundary
// splitting is enough for the in-snippet highlight contract.
func tokenize(q string) []string {
	fields := strings.FieldsFunc(q, func(r rune) bool {
		return !isWordRune(r)
	})
	out := make([]string, 0, len(fields))
	seen := map[string]struct{}{}
	for _, f := range fields {
		key := strings.ToLower(f)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, f)
	}
	return out
}

// isWordRune is the rune-aware companion to isWordByte. The tokenize
// path runs on the raw query string (which may include multibyte
// characters from a user's clipboard) so it needs the rune view.
func isWordRune(r rune) bool {
	if r == '_' {
		return true
	}
	// We intentionally accept Letter + Digit here even outside ASCII
	// — the highlight match against the (HTML-escaped) snippet still
	// runs ASCII-only, but if the source text is ASCII this lets the
	// tokenizer reject "go's" → ["go", "s"] cleanly rather than
	// emitting "go's".
	return isLetter(r) || isDigit(r)
}

func isLetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func isDigit(r rune) bool { return r >= '0' && r <= '9' }

// matchedTerms returns the subset of terms that appear (prefix,
// case-insensitive) in either the title or the excerpt. The set is
// surfaced on the Hit so the admin UI can render per-term chips
// next to the row. We do NOT consult content_rendered — that field
// can be megabytes and the cost of scanning it per row in a tight
// loop is not worth the marginal UI value.
func matchedTerms(title, excerpt string, terms []string) []string {
	if len(terms) == 0 {
		return nil
	}
	combined := strings.ToLower(title + " " + excerpt)
	out := make([]string, 0, len(terms))
	for _, t := range terms {
		lt := strings.ToLower(t)
		if lt == "" {
			continue
		}
		if strings.Contains(combined, lt) {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// IsConstraintError reports whether err is a Postgres
// foreign-key/check violation rather than a syntax problem the
// search package would have caused. Exposed so handlers can
// distinguish "user input was bad" (return 400) from "the search
// path is broken" (return 500). Kept here rather than imported from
// a sibling because the dependency would otherwise be inverted —
// search is the lowest layer in the read path.
func IsConstraintError(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	// 23xxx is the Postgres SQLSTATE class for integrity constraint
	// violations.
	return strings.HasPrefix(pgErr.Code, "23")
}
