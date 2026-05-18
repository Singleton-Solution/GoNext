package verify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Singleton-Solution/GoNext/packages/go/migrate/html2blocks"
	"github.com/Singleton-Solution/GoNext/packages/go/migrate/importer"
	"github.com/Singleton-Solution/GoNext/packages/go/migrate/wxr"
)

// checkPosts compares every source Post against its destination
// row. The checks performed per post:
//
//   - existence + slug lookup
//   - title matches verbatim
//   - content round-trips through html2blocks then re-serialises to
//     the same canonical hash
//   - status preserved (after the importer's normaliseStatus())
//   - author resolved to a users.id (and that users.id matches the
//     login from the WXR)
//
// Plus a top-level cardinality check (source count == destination
// count of non-trash, non-revision posts).
//
// The lookup strategy keys off (post_type, slug) — the same natural
// key the importer's probe uses. If a post can't be found at all
// the row is reported once as an error and no per-field checks are
// run (they'd all fail trivially and bloat the report).
func (v *Verifier) checkPosts(ctx context.Context, st *runState, report *Report) error {
	// 1. Cardinality. We restrict the DB-side count to the
	// post_types the WXR carries — a freshly-imported DB will only
	// have those rows anyway, but a re-import or a separately
	// authored DB could legitimately have additional rows that
	// shouldn't penalise fidelity.
	wantByType := map[string]int{}
	for _, p := range st.posts {
		t := strings.TrimSpace(p.PostType)
		if t == "" {
			t = "post"
		}
		wantByType[t]++
	}

	// We compare totals across the post types the WXR actually
	// contributed. The DB query filters by the same set so an
	// admin-created page after import doesn't move the gauge.
	types := make([]string, 0, len(wantByType))
	for t := range wantByType {
		types = append(types, t)
	}
	sort.Strings(types)

	var dbTotal int
	if len(types) > 0 {
		row := v.DB.QueryRow(ctx, `
			SELECT count(*)
			  FROM posts
			 WHERE post_type = ANY($1::text[])
			   AND status <> 'trash'
		`, types)
		if err := row.Scan(&dbTotal); err != nil {
			return wrapVerifyErr("posts.count", err)
		}
	}
	wantTotal := 0
	for _, n := range wantByType {
		wantTotal += n
	}
	if dbTotal == wantTotal {
		report.AddPass("posts.count")
	} else {
		report.AddFailure(Failure{
			CheckName: "posts.count",
			Severity:  SeverityError,
			Reason:    fmt.Sprintf("post count mismatch: source=%d db=%d", wantTotal, dbTotal),
			Source:    fmt.Sprintf("%d", wantTotal),
			Target:    fmt.Sprintf("%d", dbTotal),
		})
	}

	// 2. Per-post checks. We probe by (post_type, slug); the
	// importer keeps slug stable so this is the natural lookup.
	for _, p := range st.posts {
		if err := v.checkOnePost(ctx, p, report); err != nil {
			return err
		}
	}
	return nil
}

// checkOnePost performs the per-record post comparisons. Returns a
// non-nil error only for fatal DB issues; field-level mismatches
// land on Report.Failures.
func (v *Verifier) checkOnePost(ctx context.Context, p *wxr.Post, report *Report) error {
	slug := strings.TrimSpace(p.Name)
	if slug == "" {
		// The importer mints a slug from the title in this case;
		// we don't try to replicate that here because the helper
		// is private to the importer package. Instead, fall back
		// to a title-keyed lookup — less precise, still useful.
		slug = ""
	}
	postType := strings.TrimSpace(p.PostType)
	if postType == "" {
		postType = "post"
	}

	var (
		id           uuid.UUID
		title        string
		dbStatus     string
		blocksJSON   []byte
		authorID     uuid.UUID
		dbPostTypeOK bool
	)
	// Probe by (post_type, slug). When slug is empty, fall back to
	// (post_type, title) — also a probabilistic lookup but enough
	// for the round-trip fidelity gate. Either probe is read-only.
	var probeErr error
	if slug != "" {
		probeErr = v.DB.QueryRow(ctx, `
			SELECT id, title, status::text, content_blocks::text, author_id
			  FROM posts
			 WHERE post_type = $1
			   AND slug = $2::citext
			   AND status <> 'trash'
			 LIMIT 1
		`, postType, slug).Scan(&id, &title, &dbStatus, &blocksJSON, &authorID)
	} else {
		probeErr = v.DB.QueryRow(ctx, `
			SELECT id, title, status::text, content_blocks::text, author_id
			  FROM posts
			 WHERE post_type = $1
			   AND title = $2
			   AND status <> 'trash'
			 LIMIT 1
		`, postType, p.Title).Scan(&id, &title, &dbStatus, &blocksJSON, &authorID)
	}

	if errors.Is(probeErr, pgx.ErrNoRows) {
		report.AddFailure(Failure{
			CheckName: "posts.exists",
			Severity:  SeverityError,
			Reason:    fmt.Sprintf("post not found in DB: type=%q slug=%q title=%q", postType, slug, p.Title),
			Source:    p.PostID,
		})
		return nil
	}
	if probeErr != nil {
		return wrapVerifyErr("posts.probe", probeErr)
	}
	dbPostTypeOK = true
	_ = dbPostTypeOK

	// Title verbatim.
	if title == p.Title {
		report.AddPass("posts.title")
	} else {
		report.AddFailure(Failure{
			CheckName: "posts.title",
			Severity:  SeverityError,
			Reason:    fmt.Sprintf("title mismatch: source=%q db=%q", p.Title, title),
			Source:    p.PostID,
			Target:    id.String(),
		})
	}

	// Status preserved (after importer normalisation).
	wantStatus := normaliseStatus(p.Status)
	if dbStatus == wantStatus {
		report.AddPass("posts.status")
	} else {
		report.AddFailure(Failure{
			CheckName: "posts.status",
			Severity:  SeverityError,
			Reason:    fmt.Sprintf("status mismatch: source=%q db=%q", wantStatus, dbStatus),
			Source:    p.PostID,
			Target:    id.String(),
		})
	}

	// Content round-trip: convert the source HTML again and hash
	// the resulting canonical block tree. Then hash the DB row's
	// stored block tree. The hashes should match — the importer
	// ran the same converter, the DB stored the JSON verbatim, and
	// we canonicalise both ends through json.Marshal of a
	// type-sorted structure so map ordering doesn't desync.
	srcHash, err := canonicalBlocksHash([]byte(p.Content))
	if err != nil {
		report.AddFailure(Failure{
			CheckName: "posts.content",
			Severity:  SeverityWarn,
			Reason:    fmt.Sprintf("source convert: %v", err),
			Source:    p.PostID,
			Target:    id.String(),
		})
	} else {
		dbHash, dbErr := canonicalBlocksHashFromJSON(blocksJSON)
		switch {
		case dbErr != nil:
			report.AddFailure(Failure{
				CheckName: "posts.content",
				Severity:  SeverityWarn,
				Reason:    fmt.Sprintf("db blocks parse: %v", dbErr),
				Source:    p.PostID,
				Target:    id.String(),
			})
		case dbHash == srcHash:
			report.AddPass("posts.content")
		default:
			report.AddFailure(Failure{
				CheckName: "posts.content",
				Severity:  SeverityError,
				Reason:    fmt.Sprintf("content round-trip hash mismatch: source=%s db=%s", srcHash[:12], dbHash[:12]),
				Source:    p.PostID,
				Target:    id.String(),
			})
		}
	}

	// Author resolved: there must be a users row, and its handle
	// (or email) must align with the WXR's Creator login. The
	// importer falls back to a synthetic "migrated" user when the
	// login isn't declared — we warn rather than error in that
	// case so a partial WXR doesn't trash the fidelity.
	if err := v.checkPostAuthor(ctx, p, authorID, id, report); err != nil {
		return err
	}
	return nil
}

// checkPostAuthor verifies the resolved author row aligns with the
// source post's Creator login. Emits one pass or one failure per
// post.
func (v *Verifier) checkPostAuthor(
	ctx context.Context,
	p *wxr.Post,
	authorID uuid.UUID,
	postID uuid.UUID,
	report *Report,
) error {
	var handle string
	err := v.DB.QueryRow(ctx, `SELECT handle::text FROM users WHERE id = $1`, authorID).Scan(&handle)
	if errors.Is(err, pgx.ErrNoRows) {
		report.AddFailure(Failure{
			CheckName: "posts.author",
			Severity:  SeverityError,
			Reason:    fmt.Sprintf("author row not found: author_id=%s", authorID),
			Source:    p.PostID,
			Target:    postID.String(),
		})
		return nil
	}
	if err != nil {
		return wrapVerifyErr("posts.author", err)
	}

	wantLogin := strings.TrimSpace(p.Creator)
	switch {
	case wantLogin == "" && handle == "migrated":
		// No creator declared, synthetic user resolved. The
		// importer guarantees this fallback; pass.
		report.AddPass("posts.author")
	case wantLogin != "" && strings.EqualFold(handle, wantLogin):
		report.AddPass("posts.author")
	case wantLogin != "" && handle == "migrated":
		// The creator was declared but the importer fell back to
		// the synthetic user — usually because the author wasn't
		// in the preamble. Warn rather than error: the row is
		// addressable, just not faithfully attributed.
		report.AddFailure(Failure{
			CheckName: "posts.author",
			Severity:  SeverityWarn,
			Reason:    fmt.Sprintf("author resolved to synthetic 'migrated' user: source login=%q", wantLogin),
			Source:    p.PostID,
			Target:    postID.String(),
		})
	default:
		report.AddFailure(Failure{
			CheckName: "posts.author",
			Severity:  SeverityError,
			Reason:    fmt.Sprintf("author mismatch: source login=%q db handle=%q", wantLogin, handle),
			Source:    p.PostID,
			Target:    postID.String(),
		})
	}
	return nil
}

// canonicalBlocksHash converts htmlSrc through html2blocks and
// returns a hex SHA-256 over the canonical JSON form. The canonical
// form is what json.Marshal produces on the typed Block slice —
// the marshaller orders struct fields by declaration order and the
// map[string]any fields are walked in sorted key order, so two
// equal block trees serialise to byte-equal bytes.
func canonicalBlocksHash(htmlSrc []byte) (string, error) {
	blocks, err := html2blocks.Convert(htmlSrc)
	if err != nil {
		return "", err
	}
	return canonicalBlocksHashOver(blocks)
}

// canonicalBlocksHashFromJSON unmarshals a stored content_blocks
// JSON document and re-hashes it via canonicalBlocksHashOver. The
// extra hop is needed because Postgres may have re-formatted the
// JSON (whitespace, ordering) and a byte-equality check on the raw
// document would false-positive trivially-modified rows.
func canonicalBlocksHashFromJSON(b []byte) (string, error) {
	var blocks []html2blocks.Block
	if len(b) == 0 || string(b) == "null" {
		return canonicalBlocksHashOver(nil)
	}
	if err := json.Unmarshal(b, &blocks); err != nil {
		return "", err
	}
	return canonicalBlocksHashOver(blocks)
}

// canonicalBlocksHashOver hashes the block slice via a deterministic
// JSON serialisation. Map keys are sorted by stringifying-then-
// re-marshalling so the per-block Attrs (map[string]any) doesn't
// rely on Go's nondeterministic map iteration.
func canonicalBlocksHashOver(blocks []html2blocks.Block) (string, error) {
	canon := make([]map[string]any, 0, len(blocks))
	for _, blk := range blocks {
		canon = append(canon, canonicaliseBlock(blk))
	}
	b, err := json.Marshal(canon)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// canonicaliseBlock normalises one block into an ordered
// map[string]any whose keys are themselves sorted by JSON marshal
// (Go's encoding/json sorts map[string]any keys lexically). The
// recursive call covers innerBlocks.
func canonicaliseBlock(b html2blocks.Block) map[string]any {
	m := map[string]any{
		"name": b.Name,
	}
	if b.ID != "" {
		m["id"] = b.ID
	}
	if len(b.Attrs) > 0 {
		// Marshal+unmarshal forces consistent map ordering and
		// strips any non-JSON-stable types (functions, channels)
		// that could otherwise sneak in. Encoding/json sorts
		// map[string]any keys alphabetically on marshal, so the
		// output is deterministic.
		raw, err := json.Marshal(b.Attrs)
		if err == nil {
			var attrs map[string]any
			_ = json.Unmarshal(raw, &attrs)
			m["attrs"] = attrs
		}
	}
	if len(b.InnerBlocks) > 0 {
		inner := make([]map[string]any, 0, len(b.InnerBlocks))
		for _, ib := range b.InnerBlocks {
			inner = append(inner, canonicaliseBlock(ib))
		}
		m["innerBlocks"] = inner
	}
	return m
}

// normaliseStatus mirrors importer.normaliseStatus exactly. We
// duplicate rather than import-and-export from the importer package
// because the importer's helper is unexported and the cost of a
// small switch statement is well below the cost of a broader API
// change for verify-only use. If the importer ever exports its
// helper this file should switch.
func normaliseStatus(s string) string {
	switch strings.TrimSpace(s) {
	case "publish", "published":
		return "published"
	case "draft":
		return "draft"
	case "pending":
		return "pending"
	case "future", "scheduled":
		return "scheduled"
	case "private":
		return "private"
	case "trash":
		return "trash"
	case "inherit":
		return "published"
	default:
		return "draft"
	}
}

// reference shim so unused-import linters keep their hands off. The
// importer is intentionally pulled in so future checks can call its
// public helpers without a re-import; for now only the package's
// shape is used.
var _ = importer.ConflictSkip
