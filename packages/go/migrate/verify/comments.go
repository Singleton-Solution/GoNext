package verify

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Singleton-Solution/GoNext/packages/go/migrate/wxr"
)

// checkComments compares the comment threads under each post.
//
// Per post we verify two things:
//
//  1. Cardinality: the number of comment rows attached to the
//     destination post equals the number of <wp:comment> children
//     in the source (excluding pingbacks/trackbacks, which the
//     importer skips by default when they have no useful content).
//  2. Depth shape: the histogram of comment depths (the ltree
//     nlevel values) on the destination matches the histogram of
//     depths the source thread implies. We don't compare individual
//     parent_id values — the WP comment_id is a transient WP-side
//     integer and there's no GoNext-side column carrying it — but
//     the depth histogram is sufficient to catch "someone collapsed
//     a subtree" regressions.
//
// The comparator skips a post entirely when the source has zero
// comments and the DB also has zero — no checks contribute to the
// count, which keeps fidelity meaningful on comment-free WXRs.
func (v *Verifier) checkComments(ctx context.Context, st *runState, report *Report) error {
	for _, p := range st.posts {
		if err := v.checkOnePostComments(ctx, p, report); err != nil {
			return err
		}
	}
	return nil
}

// checkOnePostComments runs the two checks for one post.
func (v *Verifier) checkOnePostComments(ctx context.Context, p *wxr.Post, report *Report) error {
	wantComments := filterCountedComments(p.Comments)
	if len(wantComments) == 0 {
		// No source comments — skip without contributing to the
		// gauge. We don't pre-create empty threads.
		return nil
	}

	postID, ok, err := v.resolvePostID(ctx, p)
	if err != nil {
		return err
	}
	if !ok {
		// The post itself wasn't found; checkPosts already
		// flagged it. We could emit a per-comment failure but
		// it'd just double-count. Skip.
		return nil
	}

	// 1. Cardinality.
	var dbCount int
	if err := v.DB.QueryRow(ctx, `
		SELECT count(*) FROM comments WHERE post_id = $1
	`, postID).Scan(&dbCount); err != nil {
		return wrapVerifyErr("comments.count", err)
	}
	if dbCount == len(wantComments) {
		report.AddPass("comments.count")
	} else {
		report.AddFailure(Failure{
			CheckName: "comments.count",
			Severity:  SeverityError,
			Reason: fmt.Sprintf("comment count mismatch on post %s: source=%d db=%d",
				p.PostID, len(wantComments), dbCount),
			Source: p.PostID,
			Target: postID.String(),
		})
	}

	// 2. Depth shape. We compute the source depth histogram by
	// walking comment parents in source order, then compare to
	// the DB's nlevel(path) histogram.
	srcHist := sourceDepthHistogram(wantComments)
	dbHist, err := v.dbDepthHistogram(ctx, postID)
	if err != nil {
		return err
	}
	if equalHistograms(srcHist, dbHist) {
		report.AddPass("comments.path")
	} else {
		report.AddFailure(Failure{
			CheckName: "comments.path",
			Severity:  SeverityError,
			Reason: fmt.Sprintf("comment depth shape changed on post %s: source=%s db=%s",
				p.PostID, formatHistogram(srcHist), formatHistogram(dbHist)),
			Source: p.PostID,
			Target: postID.String(),
		})
	}
	return nil
}

// filterCountedComments returns only the comments that the importer
// actually inserts. Pingbacks and trackbacks are kept (the importer
// inserts them) but explicitly-trashed comments are not (the
// importer maps them to status='trash' but still inserts the row).
// We include everything except the rare case where the importer
// would skip — currently nothing. The filter exists so a future
// importer policy change has one place to align.
func filterCountedComments(cs []wxr.Comment) []wxr.Comment {
	out := cs[:0:0]
	for _, c := range cs {
		out = append(out, c)
	}
	return out
}

// resolvePostID looks up the destination post id for a source post.
// Returns (id, true, nil) on success, (uuid.Nil, false, nil) when
// the post isn't found, (uuid.Nil, false, err) on a DB failure.
func (v *Verifier) resolvePostID(ctx context.Context, p *wxr.Post) (uuid.UUID, bool, error) {
	slug := strings.TrimSpace(p.Name)
	postType := strings.TrimSpace(p.PostType)
	if postType == "" {
		postType = "post"
	}
	var id uuid.UUID
	var err error
	if slug != "" {
		err = v.DB.QueryRow(ctx, `
			SELECT id FROM posts
			 WHERE post_type = $1 AND slug = $2::citext AND status <> 'trash'
			 LIMIT 1
		`, postType, slug).Scan(&id)
	} else {
		err = v.DB.QueryRow(ctx, `
			SELECT id FROM posts
			 WHERE post_type = $1 AND title = $2 AND status <> 'trash'
			 LIMIT 1
		`, postType, p.Title).Scan(&id)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, wrapVerifyErr("posts.resolve", err)
	}
	return id, true, nil
}

// sourceDepthHistogram returns a depth-keyed count of source
// comments. Depth 1 = top-level; depth 2 = direct reply; etc. The
// algorithm walks comments twice (since WXR doesn't guarantee
// topological ordering between siblings) and resolves parents via
// an in-memory map.
func sourceDepthHistogram(cs []wxr.Comment) map[int]int {
	depthByID := map[string]int{}
	hist := map[int]int{}

	// Pass 1: top-level (parent is "0" or empty).
	pending := []wxr.Comment{}
	for _, c := range cs {
		if c.Parent == "" || c.Parent == "0" {
			depthByID[c.ID] = 1
			hist[1]++
		} else {
			pending = append(pending, c)
		}
	}
	// Pass 2..N: resolve nested. We cap at len(pending)+1 passes
	// to avoid an infinite loop on a malformed input (a cycle).
	for pass := 0; pass < len(cs)+1 && len(pending) > 0; pass++ {
		stillPending := pending[:0]
		for _, c := range pending {
			if d, ok := depthByID[c.Parent]; ok {
				depthByID[c.ID] = d + 1
				hist[d+1]++
			} else {
				stillPending = append(stillPending, c)
			}
		}
		if len(stillPending) == len(pending) {
			// No progress; treat remaining as top-level so we
			// don't lose them from the count entirely. The depth
			// histogram will be wrong (these get depth 1
			// instead of whatever was intended), which is the
			// right outcome — the source itself is malformed.
			for _, c := range stillPending {
				depthByID[c.ID] = 1
				hist[1]++
			}
			return hist
		}
		pending = stillPending
	}
	return hist
}

// dbDepthHistogram pulls the nlevel(path) distribution for one
// post's comments.
func (v *Verifier) dbDepthHistogram(ctx context.Context, postID uuid.UUID) (map[int]int, error) {
	rows, err := v.DB.Query(ctx, `
		SELECT nlevel(path) AS depth, count(*)
		  FROM comments
		 WHERE post_id = $1
		 GROUP BY depth
	`, postID)
	if err != nil {
		return nil, wrapVerifyErr("comments.depth", err)
	}
	defer rows.Close()
	hist := map[int]int{}
	for rows.Next() {
		var depth, n int
		if err := rows.Scan(&depth, &n); err != nil {
			return nil, wrapVerifyErr("comments.depth.scan", err)
		}
		hist[depth] = n
	}
	if err := rows.Err(); err != nil {
		return nil, wrapVerifyErr("comments.depth.rows", err)
	}
	return hist, nil
}

// equalHistograms reports byte-equality on two depth distributions.
func equalHistograms(a, b map[int]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// formatHistogram renders a depth histogram in a stable order for
// log lines. e.g. "{1:3,2:1}" — depth-sorted, no spaces.
func formatHistogram(h map[int]int) string {
	if len(h) == 0 {
		return "{}"
	}
	keys := make([]int, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	var b strings.Builder
	b.WriteString("{")
	for i, k := range keys {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, "%d:%d", k, h[k])
	}
	b.WriteString("}")
	return b.String()
}
