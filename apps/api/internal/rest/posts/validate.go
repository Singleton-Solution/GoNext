package posts

import (
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"
)

// validation returns the user-readable problem detail and the
// machine-readable code in one value, so handlers can map it onto a
// 400 ProblemDetails in a single call.
type validation struct {
	Code   string
	Detail string
}

func (v validation) Error() string { return v.Detail }

// asValidation extracts a validation error from err, if any. Returns
// (v, true) when err is a validation; (zero, false) otherwise.
func asValidation(err error) (validation, bool) {
	var v validation
	if errors.As(err, &v) {
		return v, true
	}
	return validation{}, false
}

// validStatuses is the closed set the API accepts on create/update.
// Matches the post_status enum in 000001_init.up.sql (draft, pending,
// published, scheduled, private, trash). The set is a literal here
// because the runtime registry of statuses doesn't exist yet — when
// it lands, this set moves to a config-time lookup.
var validStatuses = map[string]struct{}{
	"draft":     {},
	"pending":   {},
	"published": {},
	"scheduled": {},
	"private":   {},
	"trash":     {},
}

var validCommentStatuses = map[string]struct{}{
	"open":   {},
	"closed": {},
}

// titleMaxBytes / slugMaxBytes / excerptMaxBytes bound the storable
// fields. These match what a sane CMS surface accepts; the database
// itself has TEXT columns so it's the API's job to refuse abuse-size
// inputs before they hit the row.
const (
	titleMaxBytes   = 4 * 1024
	slugMaxBytes    = 200
	excerptMaxBytes = 32 * 1024
)

// validateCreate runs structural + value checks on a create body.
// Returns a [validation] on the first failure so the handler can
// surface a 400 with a usable detail field.
func validateCreate(in CreateInput) error {
	if in.Status != nil {
		if _, ok := validStatuses[*in.Status]; !ok {
			return validation{Code: "invalid_status", Detail: fmt.Sprintf("unknown status %q", *in.Status)}
		}
	}
	if in.Title != nil && len(*in.Title) > titleMaxBytes {
		return validation{Code: "title_too_long", Detail: "title exceeds maximum length"}
	}
	if in.Slug != nil {
		if err := validateSlug(*in.Slug); err != nil {
			return err
		}
	}
	if in.Excerpt != nil && len(*in.Excerpt) > excerptMaxBytes {
		return validation{Code: "excerpt_too_long", Detail: "excerpt exceeds maximum length"}
	}
	if in.CommentStatus != nil {
		if _, ok := validCommentStatuses[*in.CommentStatus]; !ok {
			return validation{Code: "invalid_comment_status", Detail: "comment_status must be open or closed"}
		}
	}
	if in.PingStatus != nil {
		if _, ok := validCommentStatuses[*in.PingStatus]; !ok {
			return validation{Code: "invalid_ping_status", Detail: "ping_status must be open or closed"}
		}
	}
	if len(in.ContentBlocks) > 0 {
		if err := validateContentBlocks(in.ContentBlocks); err != nil {
			return err
		}
	}
	if len(in.Meta) > 0 {
		if err := validateJSONObject(in.Meta, "meta"); err != nil {
			return err
		}
	}
	return nil
}

// validateUpdate is the patch-shape equivalent of validateCreate. The
// same checks apply with the same nil-as-omitted semantics.
func validateUpdate(in UpdateInput) error {
	// UpdateInput shares its field set with CreateInput — translate so
	// we don't duplicate the rule list.
	return validateCreate(CreateInput{
		ParentID:      in.ParentID,
		Status:        in.Status,
		Title:         in.Title,
		Slug:          in.Slug,
		Excerpt:       in.Excerpt,
		ContentBlocks: in.ContentBlocks,
		Password:      in.Password,
		CommentStatus: in.CommentStatus,
		PingStatus:    in.PingStatus,
		MenuOrder:     in.MenuOrder,
		Meta:          in.Meta,
		PublishedAt:   in.PublishedAt,
		ScheduledFor:  in.ScheduledFor,
	})
}

// validateSlug enforces a conservative slug rule: ASCII letters, digits,
// hyphen, underscore. Empty slug is allowed at validation time (the
// store may auto-derive it; this issue does not implement auto-slug
// derivation, but it leaves room for a future issue to do so).
func validateSlug(slug string) error {
	if len(slug) == 0 {
		return nil
	}
	if len(slug) > slugMaxBytes {
		return validation{Code: "slug_too_long", Detail: "slug exceeds maximum length"}
	}
	if !utf8.ValidString(slug) {
		return validation{Code: "invalid_slug", Detail: "slug must be valid UTF-8"}
	}
	for _, r := range slug {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_':
		default:
			return validation{Code: "invalid_slug", Detail: "slug must contain only ASCII letters, digits, hyphens, or underscores"}
		}
	}
	return nil
}

// validateContentBlocks ensures that the supplied JSON is a top-level
// array of objects, each having a non-empty `type` string. This is the
// minimum structural contract for ADR 0008's block tree. A full
// block-schema validation (per-type attributes) is deferred to the
// block-registry issue.
func validateContentBlocks(raw json.RawMessage) error {
	var blocks []map[string]any
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return validation{Code: "invalid_content_blocks", Detail: "content_blocks must be a JSON array of objects"}
	}
	for i, b := range blocks {
		t, ok := b["type"].(string)
		if !ok || t == "" {
			return validation{Code: "invalid_content_blocks", Detail: fmt.Sprintf("block at index %d is missing a non-empty type field", i)}
		}
	}
	return nil
}

// validateJSONObject ensures that the supplied JSON is a top-level
// object. Used for meta which has the same shape requirement as the
// posts.meta column (jsonb DEFAULT '{}').
func validateJSONObject(raw json.RawMessage, field string) error {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return validation{Code: "invalid_" + field, Detail: field + " must be a JSON object"}
	}
	return nil
}
