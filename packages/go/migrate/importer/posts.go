package importer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Singleton-Solution/GoNext/packages/go/migrate/html2blocks"
	"github.com/Singleton-Solution/GoNext/packages/go/migrate/wxr"
)

// upsertPost converts the post's HTML to blocks and writes the
// posts row, term relationships, and comments. The author is
// resolved through runState; if the post's Creator login was
// never declared in the WXR preamble we fall through to the
// synthetic "migrated" user (see findOrCreateMigratedUser).
//
// On a conflict (slug+post_type collision):
//   - ConflictSkip leaves the existing row alone, but still
//     records the WP→GoNext id mapping so downstream comments
//     and term relationships attach to the right row.
//   - ConflictUpdate replaces the content/blocks/excerpt.
//   - ConflictFail returns ErrAborted; the caller rolls the
//     batch back.
func (imp *Importer) upsertPost(
	ctx context.Context,
	tx pgx.Tx,
	opts Options,
	state *runState,
	p *wxr.Post,
	report *Report,
) error {
	if p == nil {
		return errors.New("nil post")
	}
	slug := strings.TrimSpace(p.Name)
	if slug == "" {
		slug = slugFromTitle(p.Title)
	}
	if slug == "" {
		return errors.New("post has no slug and no title")
	}

	postType := strings.TrimSpace(p.PostType)
	if postType == "" {
		postType = "post"
	}

	// 1. Convert HTML→blocks. The converter is tolerant of empty
	// input and never panics; an error here is genuinely a parser
	// failure and we surface it as a per-record error.
	blocks, err := html2blocks.Convert([]byte(p.Content))
	if err != nil {
		return fmt.Errorf("convert html: %w", err)
	}
	if blocks == nil {
		blocks = []html2blocks.Block{}
	}
	blocksJSON, err := json.Marshal(blocks)
	if err != nil {
		return fmt.Errorf("marshal blocks: %w", err)
	}

	// 2. Resolve the author. Falls back to the synthetic
	// "migrated" user if the WXR is missing or references an
	// undeclared login.
	authorID, ok := state.lookupAuthorByLogin(p.Creator)
	if !ok {
		var fErr error
		authorID, fErr = imp.findOrCreateMigratedUser(ctx, tx, opts, state)
		if fErr != nil {
			return fmt.Errorf("resolve author: %w", fErr)
		}
	}

	// 3. Resolve the parent (hierarchical types only).
	var parentID *uuid.UUID
	if p.Parent != "" && p.Parent != "0" {
		if pid, ok := state.lookupPost(p.Parent); ok {
			pidCopy := pid
			parentID = &pidCopy
		}
	}

	// 4. Map the WP status string to the post_status enum used
	// by the schema. Unknown values degrade to 'draft' rather
	// than failing — operators can fix the row afterwards.
	status := normaliseStatus(p.Status)

	// 5. Build the meta JSON. We carry the wp_post_id so
	// downstream tooling can round-trip references.
	metaMap := map[string]any{
		"wp_post_id":       p.PostID,
		"wp_post_type":     p.PostType,
		"wp_link":          p.Link,
		"wp_post_date":     p.PostDate,
		"wp_post_date_gmt": p.PostDateGMT,
	}
	if p.IsSticky == "1" {
		metaMap["sticky"] = true
	}
	if len(p.Meta) > 0 {
		// Preserve the original postmeta keys under a namespaced
		// bucket. Plugin-readable post.meta queries can lift them
		// out by key once the import is complete.
		metaMap["wp_meta"] = p.Meta
	}
	metaJSON, err := json.Marshal(metaMap)
	if err != nil {
		return fmt.Errorf("marshal post meta: %w", err)
	}

	commentStatus := normaliseCommentStatus(p.CommentStatus)
	pingStatus := normaliseCommentStatus(p.PingStatus)

	excerpt := p.Excerpt

	// 6. Existence probe. The schema's slug uniqueness is partial
	// (excludes status='trash'), so we look directly at the live
	// rows that match the (type, parent, slug) tuple.
	var existingID uuid.UUID
	probe := `
		SELECT id FROM posts
		 WHERE post_type = $1
		   AND slug = $2::citext
		   AND COALESCE(parent_id, '00000000-0000-0000-0000-000000000000'::uuid)
		     = COALESCE($3::uuid, '00000000-0000-0000-0000-000000000000'::uuid)
		   AND status <> 'trash'
		 LIMIT 1
	`
	err = tx.QueryRow(ctx, probe, postType, slug, parentID).Scan(&existingID)
	switch {
	case err == nil:
		state.recordPost(p.PostID, existingID)
		switch opts.OnConflict {
		case ConflictSkip:
			// Still write term relationships and comments — the
			// existing row may belong to a previous partial
			// import that never got past the post row. The
			// upserts below are idempotent.
			if err := imp.writeTermRelationships(ctx, tx, opts, state, existingID, p); err != nil {
				return err
			}
			if !opts.SkipComments {
				return imp.writeComments(ctx, tx, existingID, p, state, report)
			}
			return nil
		case ConflictUpdate:
			if _, uErr := tx.Exec(ctx, `
				UPDATE posts
				   SET title = $1,
				       excerpt = $2,
				       content_blocks = $3::jsonb,
				       status = $4::post_status,
				       comment_status = $5,
				       ping_status = $6,
				       meta = meta || $7::jsonb,
				       author_id = $8
				 WHERE id = $9
			`, p.Title, excerpt, string(blocksJSON), status, commentStatus, pingStatus,
				string(metaJSON), authorID, existingID); uErr != nil {
				return fmt.Errorf("update post: %w", uErr)
			}
			if err := imp.writeTermRelationships(ctx, tx, opts, state, existingID, p); err != nil {
				return err
			}
			if !opts.SkipComments {
				return imp.writeComments(ctx, tx, existingID, p, state, report)
			}
			return nil
		case ConflictFail:
			return fmt.Errorf("post already exists: type=%q slug=%q: %w",
				postType, slug, ErrAborted)
		}
	case errors.Is(err, pgx.ErrNoRows):
		// Fall through to insert.
	default:
		return fmt.Errorf("probe post: %w", err)
	}

	// 7. Insert.
	var newID uuid.UUID
	insertSQL := `
		INSERT INTO posts (
			post_type, parent_id, author_id, status,
			title, slug, excerpt, content_blocks,
			comment_status, ping_status, meta,
			published_at
		)
		VALUES (
			$1, $2, $3, $4::post_status,
			$5, $6::citext, $7, $8::jsonb,
			$9, $10, $11::jsonb,
			CASE WHEN $4 = 'published' THEN now() ELSE NULL END
		)
		RETURNING id
	`
	if err := tx.QueryRow(ctx, insertSQL,
		postType, parentID, authorID, status,
		p.Title, slug, excerpt, string(blocksJSON),
		commentStatus, pingStatus, string(metaJSON),
	).Scan(&newID); err != nil {
		var pgErr interface{ SQLState() string }
		if errors.As(err, &pgErr) && pgErr.SQLState() == "23505" {
			// Lost-race on the unique index. Re-probe and degrade
			// to a skip (the alternative — re-running the conflict
			// branch above — would be a re-entry hazard).
			if rErr := tx.QueryRow(ctx, probe, postType, slug, parentID).Scan(&existingID); rErr == nil {
				state.recordPost(p.PostID, existingID)
				return nil
			}
		}
		return fmt.Errorf("insert post: %w", err)
	}

	state.recordPost(p.PostID, newID)

	if err := imp.writeTermRelationships(ctx, tx, opts, state, newID, p); err != nil {
		return err
	}
	if !opts.SkipComments {
		return imp.writeComments(ctx, tx, newID, p, state, report)
	}
	return nil
}

// writeTermRelationships attaches every TermRef on the post to
// the term row recorded in runState. Missing terms are created on
// the fly so the FK lookup never fails.
func (imp *Importer) writeTermRelationships(
	ctx context.Context,
	tx pgx.Tx,
	opts Options,
	state *runState,
	postID uuid.UUID,
	p *wxr.Post,
) error {
	for position, ref := range p.Terms {
		termID, err := imp.resolveTermRef(ctx, tx, opts, state, ref)
		if err != nil {
			return fmt.Errorf("resolve term %q: %w", ref.Nicename, err)
		}
		if termID == (uuid.UUID{}) {
			continue
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO term_relationships (post_id, term_id, position)
			VALUES ($1, $2, $3)
			ON CONFLICT (post_id, term_id) DO UPDATE
			    SET position = EXCLUDED.position
		`, postID, termID, position); err != nil {
			return fmt.Errorf("insert term_relationship: %w", err)
		}
	}
	return nil
}

// writeComments inserts each <wp:comment> child as a comments row.
// Threading via comment_parent is resolved through a per-post
// in-memory map of (wp:comment_id → comments.id). WXR emits
// parents before children in source order in every export we've
// seen; we don't rely on that — the map handles out-of-order
// references by reparenting on a second pass below.
func (imp *Importer) writeComments(
	ctx context.Context,
	tx pgx.Tx,
	postID uuid.UUID,
	p *wxr.Post,
	state *runState,
	report *Report,
) error {
	if len(p.Comments) == 0 {
		return nil
	}
	wpToID := map[string]uuid.UUID{}
	pending := make([]int, 0, len(p.Comments)) // indexes whose parent wasn't ready

	for i, c := range p.Comments {
		var parentID *uuid.UUID
		if c.Parent != "" && c.Parent != "0" {
			if pid, ok := wpToID[c.Parent]; ok {
				pidCopy := pid
				parentID = &pidCopy
			} else {
				pending = append(pending, i)
				continue
			}
		}
		newID, err := imp.insertComment(ctx, tx, postID, parentID, c)
		if err != nil {
			report.Errors = append(report.Errors, *newImportError("comment", c.ID, "", err))
			continue
		}
		wpToID[c.ID] = newID
		report.Comments++
	}

	// Second pass for comments whose parent landed later in the
	// source order. We just retry once — deeper out-of-order
	// nesting in real-world exports is essentially unheard of.
	for _, i := range pending {
		c := p.Comments[i]
		var parentID *uuid.UUID
		if pid, ok := wpToID[c.Parent]; ok {
			pidCopy := pid
			parentID = &pidCopy
		}
		newID, err := imp.insertComment(ctx, tx, postID, parentID, c)
		if err != nil {
			report.Errors = append(report.Errors, *newImportError("comment", c.ID, "", err))
			continue
		}
		wpToID[c.ID] = newID
		report.Comments++
	}
	_ = state
	return nil
}

// insertComment writes one row into comments. Returns the
// freshly generated id.
func (imp *Importer) insertComment(
	ctx context.Context,
	tx pgx.Tx,
	postID uuid.UUID,
	parentID *uuid.UUID,
	c wxr.Comment,
) (uuid.UUID, error) {
	status := commentStatusFromApproval(c.Approved)
	content := c.Content
	if content == "" {
		// comments.content is NOT NULL — synthesize a sentinel so
		// the row survives. Operators can trash it after import.
		content = "[empty comment imported from WordPress]"
	}
	var id uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO comments (
			post_id, parent_id, author_user_id,
			author_name, author_email, author_url, author_ip,
			content, content_format, status
		)
		VALUES ($1, $2, NULL, $3, NULLIF($4,'')::citext, $5, NULLIF($6,'')::inet, $7, 'html', $8)
		RETURNING id
	`, postID, parentID, c.Author, c.AuthorEmail, c.AuthorURL, c.AuthorIP,
		content, status).Scan(&id); err != nil {
		return uuid.UUID{}, fmt.Errorf("insert comment: %w", err)
	}
	return id, nil
}

// slugFromTitle is a last-resort fallback: WP posts always have
// post_name set in practice, but a hand-edited export can omit
// it. We do the minimum normalisation — lowercase, hyphenate
// whitespace, strip non-URL-safe characters — and let the DB's
// citext slug column handle uniqueness collisions.
func slugFromTitle(title string) string {
	t := strings.ToLower(strings.TrimSpace(title))
	var b strings.Builder
	prevDash := false
	for _, r := range t {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		case r == ' ' || r == '-' || r == '_' || r == '\t':
			if !prevDash {
				b.WriteRune('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// normaliseStatus maps a WP status string to a value the
// post_status enum recognises. Unknown values land in 'draft' so
// the row is queryable but invisible until an admin sets it.
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
		// WP's attachments/revisions inherit the parent's status.
		// We don't carry "inherit" in the enum; route attachments
		// to 'published' and let downstream tooling re-derive the
		// effective state.
		return "published"
	default:
		return "draft"
	}
}

// normaliseCommentStatus is the comment/ping status mapper. The
// posts table's CHECK constraint only accepts 'open' or 'closed'.
func normaliseCommentStatus(s string) string {
	switch strings.TrimSpace(strings.ToLower(s)) {
	case "open":
		return "open"
	default:
		return "closed"
	}
}

// commentStatusFromApproval maps WP's <wp:comment_approved>
// values to the comments.status check-constrained enum.
func commentStatusFromApproval(a string) string {
	switch strings.TrimSpace(a) {
	case "1", "approve":
		return "approved"
	case "0", "hold":
		return "pending"
	case "spam":
		return "spam"
	case "trash":
		return "trash"
	default:
		return "pending"
	}
}
