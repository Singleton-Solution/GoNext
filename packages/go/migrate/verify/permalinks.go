package verify

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Singleton-Solution/GoNext/packages/go/migrate/wxr"
)

// checkPermalinks verifies that every WP `<link>` resolves on the
// GoNext side to the same post the importer landed it in.
//
// The check is intentionally tolerant of the two implementations
// of permalink lookup that GoNext supports:
//
//   - If a row exists in the `permalinks` table (issue #77 / 000007
//     emits one per live post), we look up the WP link's path
//     there. This is the production-style lookup the request
//     middleware uses.
//   - If the importer hasn't written permalinks yet (still the case
//     at the time of #218; see the importer doc.go which doesn't
//     wire permalinks), we fall back to the post slug embedded in
//     the WP link. The WP link is typically
//     `https://site.example.com/<slug>/`, and the slug is the most
//     stable bit of routing information across implementations.
//
// In either case we resolve the link to a slug and confirm that
// slug is the same one the importer wrote to posts.slug. A
// mismatch reads as "the WP URL would 404 on the new site", which
// is exactly what the gate is designed to catch.
func (v *Verifier) checkPermalinks(ctx context.Context, st *runState, report *Report) error {
	for _, p := range st.posts {
		if err := v.checkOnePermalink(ctx, p, report); err != nil {
			return err
		}
	}
	return nil
}

// checkOnePermalink runs the lookup-and-compare for one post.
func (v *Verifier) checkOnePermalink(ctx context.Context, p *wxr.Post, report *Report) error {
	wpLink := strings.TrimSpace(p.Link)
	if wpLink == "" {
		// Some custom post types (nav menu items, etc.) have no
		// link. Skip rather than ding fidelity.
		return nil
	}

	wpPath := extractPath(wpLink)
	wpSlug := slugFromPath(wpPath)
	if wpSlug == "" {
		// We couldn't find a slug in the WP link. Warn rather
		// than error so a malformed export doesn't bottom the
		// fidelity score.
		report.AddFailure(Failure{
			CheckName: "permalinks.resolve",
			Severity:  SeverityWarn,
			Reason:    fmt.Sprintf("could not extract slug from WP link: %s", wpLink),
			Source:    p.PostID,
		})
		return nil
	}

	// First try the permalinks table. A nil row → fall through
	// to the slug-direct lookup. We never treat the absence of
	// a permalinks row as a hard failure since the importer
	// doesn't currently write it.
	var postIDViaPermalinks uuid.UUID
	permalinksHit := false
	err := v.DB.QueryRow(ctx, `
		SELECT post_id FROM permalinks WHERE path = $1
	`, "/"+strings.Trim(wpPath, "/")).Scan(&postIDViaPermalinks)
	switch {
	case err == nil:
		permalinksHit = true
	case errors.Is(err, pgx.ErrNoRows):
		// Fall through to the slug lookup.
	default:
		return wrapVerifyErr("permalinks.probe", err)
	}

	// Look up the post by slug to validate.
	postType := strings.TrimSpace(p.PostType)
	if postType == "" {
		postType = "post"
	}
	expectedSlug := strings.TrimSpace(p.Name)
	if expectedSlug == "" {
		expectedSlug = wpSlug
	}
	var (
		id      uuid.UUID
		dbSlug  string
		dbType  string
		dbState string
	)
	err = v.DB.QueryRow(ctx, `
		SELECT id, slug::text, post_type, status::text
		  FROM posts
		 WHERE post_type = $1 AND slug = $2::citext AND status <> 'trash'
		 LIMIT 1
	`, postType, expectedSlug).Scan(&id, &dbSlug, &dbType, &dbState)
	if errors.Is(err, pgx.ErrNoRows) {
		report.AddFailure(Failure{
			CheckName: "permalinks.resolve",
			Severity:  SeverityError,
			Reason:    fmt.Sprintf("WP link %s does not resolve: no post with type=%q slug=%q", wpLink, postType, expectedSlug),
			Source:    p.PostID,
			Target:    expectedSlug,
		})
		return nil
	}
	if err != nil {
		return wrapVerifyErr("permalinks.slug", err)
	}

	// If we hit the permalinks table earlier, the post_id must
	// agree with the slug-based lookup. A mismatch is a routing
	// regression — the WP path leads somewhere other than the
	// imported post.
	if permalinksHit && postIDViaPermalinks != id {
		report.AddFailure(Failure{
			CheckName: "permalinks.resolve",
			Severity:  SeverityError,
			Reason:    fmt.Sprintf("WP link %s resolves to a different post: via permalinks=%s via slug=%s", wpLink, postIDViaPermalinks, id),
			Source:    p.PostID,
			Target:    id.String(),
		})
		return nil
	}

	// All good: the WP URL leads to the imported post via at
	// least one of the supported routes.
	if dbSlug == expectedSlug || strings.EqualFold(dbSlug, expectedSlug) {
		report.AddPass("permalinks.resolve")
	} else {
		// The slug column resolved via citext but the byte form
		// drifted. We pass-fail at the strict-equal level so
		// callers can see the drift, but downgrade to warn
		// because routing works (the column is citext).
		report.AddFailure(Failure{
			CheckName: "permalinks.resolve",
			Severity:  SeverityWarn,
			Reason:    fmt.Sprintf("slug case drift: source=%q db=%q (citext matches)", expectedSlug, dbSlug),
			Source:    p.PostID,
			Target:    id.String(),
		})
	}
	return nil
}

// extractPath returns the path component of a URL string. An input
// that doesn't parse as a URL is returned unchanged — relative
// paths are common in WXR exports from sites with overridden
// permalink templates.
func extractPath(s string) string {
	if u, err := url.Parse(s); err == nil && u.Path != "" {
		return u.Path
	}
	return s
}

// slugFromPath extracts the last non-empty path segment. For WP's
// canonical `/<slug>/` it returns `<slug>`; for `/category/foo/<slug>/`
// it still returns `<slug>`. We don't try to parse `?p=N` queries
// because those forms don't carry slug information by definition;
// the caller skips those at the slug-empty check.
func slugFromPath(p string) string {
	p = strings.TrimSpace(p)
	p = strings.Trim(p, "/")
	if p == "" {
		return ""
	}
	parts := strings.Split(p, "/")
	return parts[len(parts)-1]
}
