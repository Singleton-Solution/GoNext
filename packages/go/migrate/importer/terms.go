package importer

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Singleton-Solution/GoNext/packages/go/migrate/wxr"
)

// upsertCategory writes a wxr.Category into terms. The taxonomy
// slug is the literal "category" — the taxonomies table is
// pre-seeded with this row by migration 000005. Parents are
// resolved by looking up the parent nicename in runState; if the
// parent hasn't been processed yet (WXR doesn't guarantee
// topological ordering) the row goes in flat and any future
// import or admin action can re-parent.
func (imp *Importer) upsertCategory(
	ctx context.Context,
	tx pgx.Tx,
	opts Options,
	state *runState,
	c *wxr.Category,
) error {
	slug := strings.TrimSpace(c.Nicename)
	if slug == "" {
		return errors.New("category has no nicename")
	}
	name := c.Name
	if name == "" {
		name = slug
	}
	var parentID *uuid.UUID
	if c.Parent != "" {
		if pid, ok := state.lookupTerm("category", c.Parent); ok {
			pidCopy := pid
			parentID = &pidCopy
		}
	}
	return imp.upsertTerm(ctx, tx, opts, state, "category", slug, name, c.Description, parentID, c.TermID)
}

// upsertTag writes a wxr.Tag into terms with taxonomy "tag".
// Tags are flat per the taxonomies seed (hierarchical=false), so
// parent_id stays nil regardless of source.
func (imp *Importer) upsertTag(
	ctx context.Context,
	tx pgx.Tx,
	opts Options,
	state *runState,
	t *wxr.Tag,
) error {
	slug := strings.TrimSpace(t.Slug)
	if slug == "" {
		return errors.New("tag has no slug")
	}
	name := t.Name
	if name == "" {
		name = slug
	}
	// The taxonomies table seeds "tag" (singular). Item-level
	// <category domain="post_tag"> uses "post_tag"; the WXR
	// taxonomy registry is the same row regardless of which name
	// the export tool wrote. We normalise to "tag" here and to
	// "post_tag" in the term-relationship lookup so the in-memory
	// map stays consistent.
	return imp.upsertTerm(ctx, tx, opts, state, "tag", slug, name, t.Description, nil, t.TermID)
}

// upsertTerm is the shared upsert path used by category and tag.
// The (taxonomy, slug, parent_id) tuple is the natural key (see
// migration 000005's terms_taxonomy_slug_parent_uq partial unique
// index).
func (imp *Importer) upsertTerm(
	ctx context.Context,
	tx pgx.Tx,
	opts Options,
	state *runState,
	taxonomy, slug, name, description string,
	parentID *uuid.UUID,
	wpTermID string,
) error {
	// Probe for an existing row. We can't rely on ON CONFLICT
	// here because the unique index is partial (uses
	// COALESCE(parent_id, sentinel)), which Postgres's ON
	// CONFLICT inference doesn't navigate cleanly across pgx
	// versions. A two-step probe is portable and the overhead is
	// trivial for an import volume.
	var existingID uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT id FROM terms
		 WHERE taxonomy = $1
		   AND slug = $2::citext
		   AND COALESCE(parent_id, '00000000-0000-0000-0000-000000000000'::uuid)
		     = COALESCE($3::uuid, '00000000-0000-0000-0000-000000000000'::uuid)
		 LIMIT 1
	`, taxonomy, slug, parentID).Scan(&existingID)
	switch {
	case err == nil:
		// Cache both the canonical taxonomy slug and (when this is
		// a tag) the WP-side "post_tag" domain so item-level
		// <category domain="post_tag" nicename="…"> lookups in
		// posts.go find the row without a DB hit.
		state.recordTerm(taxonomy, slug, existingID)
		if taxonomy == "tag" {
			state.recordTerm("post_tag", slug, existingID)
		}
		switch opts.OnConflict {
		case ConflictSkip:
			return nil
		case ConflictUpdate:
			if _, uErr := tx.Exec(ctx, `
				UPDATE terms
				   SET name = $1, description = $2
				 WHERE id = $3
			`, name, description, existingID); uErr != nil {
				return fmt.Errorf("update term: %w", uErr)
			}
			return nil
		case ConflictFail:
			return fmt.Errorf("term already exists: taxonomy=%q slug=%q: %w",
				taxonomy, slug, ErrAborted)
		}
	case errors.Is(err, pgx.ErrNoRows):
		// Insert below.
	default:
		return fmt.Errorf("probe term: %w", err)
	}

	var newID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO terms (taxonomy, parent_id, slug, name, description)
		VALUES ($1, $2, $3::citext, $4, $5)
		RETURNING id
	`, taxonomy, parentID, slug, name, description).Scan(&newID); err != nil {
		var pgErr interface{ SQLState() string }
		if errors.As(err, &pgErr) && pgErr.SQLState() == "23505" {
			// Conflict raced ahead of us. Re-probe.
			if rErr := tx.QueryRow(ctx, `
				SELECT id FROM terms
				 WHERE taxonomy = $1 AND slug = $2::citext
				   AND COALESCE(parent_id, '00000000-0000-0000-0000-000000000000'::uuid)
				     = COALESCE($3::uuid, '00000000-0000-0000-0000-000000000000'::uuid)
				 LIMIT 1
			`, taxonomy, slug, parentID).Scan(&existingID); rErr == nil {
				state.recordTerm(taxonomy, slug, existingID)
				if taxonomy == "tag" {
					state.recordTerm("post_tag", slug, existingID)
				}
				return nil
			}
		}
		return fmt.Errorf("insert term: %w", err)
	}

	state.recordTerm(taxonomy, slug, newID)
	if taxonomy == "tag" {
		state.recordTerm("post_tag", slug, newID)
	}
	_ = wpTermID // reserved for the durable migration_map once #147 lands
	return nil
}

// resolveTermRef returns the GoNext term id for an item-level
// <category> reference. The lookup is in-memory only because the
// preamble already populated state.terms for every term referenced
// by name; if it misses, the importer creates an on-the-fly term
// row so the post→term link can still be written (the same
// behaviour WP itself shows when an item references an undeclared
// term — surfacing as auto-created terms after import).
func (imp *Importer) resolveTermRef(
	ctx context.Context,
	tx pgx.Tx,
	opts Options,
	state *runState,
	ref wxr.TermRef,
) (uuid.UUID, error) {
	if id, ok := state.lookupTerm(ref.Domain, ref.Nicename); ok {
		return id, nil
	}
	// Map the item-level domain to the canonical taxonomy slug.
	// "post_tag" is WP's domain attribute for tags; the taxonomies
	// table uses "tag".
	taxonomy := ref.Domain
	if taxonomy == "post_tag" {
		taxonomy = "tag"
	}
	if taxonomy == "" {
		taxonomy = "category"
	}
	name := ref.Name
	if name == "" {
		name = ref.Nicename
	}
	if err := imp.upsertTerm(ctx, tx, opts, state, taxonomy, ref.Nicename, name, "", nil, ""); err != nil {
		return uuid.UUID{}, err
	}
	id, _ := state.lookupTerm(ref.Domain, ref.Nicename)
	if id == (uuid.UUID{}) {
		// upsertTerm only records under (taxonomy, slug). When
		// ref.Domain differs from taxonomy ("post_tag" vs "tag"),
		// fall back to the canonical lookup.
		id, _ = state.lookupTerm(taxonomy, ref.Nicename)
	}
	return id, nil
}
