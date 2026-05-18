package verify

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// checkTerms compares every source Category / Tag against its
// destination row. Per term we verify:
//
//   - existence in terms keyed by (taxonomy, slug)
//   - name preserved
//   - taxonomy correct (category vs. tag; importer maps WP's
//     "post_tag" domain to "tag")
//   - parent_id resolves (only for category, which is hierarchical;
//     the tag taxonomy is flat and we skip the parent check)
//
// Plus two cardinality checks: source count of categories matches
// the destination count where taxonomy='category', same for tags.
func (v *Verifier) checkTerms(ctx context.Context, st *runState, report *Report) error {
	if err := v.checkTermsByTaxonomy(ctx, "category", len(st.categories), report); err != nil {
		return err
	}
	if err := v.checkTermsByTaxonomy(ctx, "tag", len(st.tags), report); err != nil {
		return err
	}

	for _, c := range st.categories {
		if err := v.checkOneCategory(ctx, c.Nicename, c.Name, c.Parent, report); err != nil {
			return err
		}
	}
	for _, tg := range st.tags {
		if err := v.checkOneTag(ctx, tg.Slug, tg.Name, report); err != nil {
			return err
		}
	}
	return nil
}

// checkTermsByTaxonomy runs the count check for one taxonomy. The
// importer never deletes rows on re-import (the conflict policy
// only decides between skip / update / fail), so we expect at least
// `want` rows but tolerate extras.
func (v *Verifier) checkTermsByTaxonomy(ctx context.Context, taxonomy string, want int, report *Report) error {
	var got int
	row := v.DB.QueryRow(ctx, `SELECT count(*) FROM terms WHERE taxonomy = $1`, taxonomy)
	if err := row.Scan(&got); err != nil {
		return wrapVerifyErr("terms.count", err)
	}
	if got >= want {
		report.AddPass("terms.count")
	} else {
		report.AddFailure(Failure{
			CheckName: "terms.count",
			Severity:  SeverityError,
			Reason:    fmt.Sprintf("term count too low for taxonomy=%q: source=%d db=%d", taxonomy, want, got),
			Source:    taxonomy,
			Target:    fmt.Sprintf("%d", got),
		})
	}
	return nil
}

// checkOneCategory probes one category row and verifies name and
// parent linkage.
func (v *Verifier) checkOneCategory(ctx context.Context, slug, name, parentNicename string, report *Report) error {
	if strings.TrimSpace(slug) == "" {
		return nil
	}
	var (
		id       uuid.UUID
		dbName   string
		dbParent *uuid.UUID
	)
	err := v.DB.QueryRow(ctx, `
		SELECT id, name, parent_id
		  FROM terms
		 WHERE taxonomy = 'category' AND slug = $1::citext
		 LIMIT 1
	`, slug).Scan(&id, &dbName, &dbParent)
	if errors.Is(err, pgx.ErrNoRows) {
		report.AddFailure(Failure{
			CheckName: "terms.name",
			Severity:  SeverityError,
			Reason:    fmt.Sprintf("category not found: slug=%q", slug),
			Source:    slug,
		})
		return nil
	}
	if err != nil {
		return wrapVerifyErr("terms.probe", err)
	}

	if name == "" {
		// The importer falls back to the slug when name is
		// empty; verify against that to avoid flagging a false
		// positive.
		name = slug
	}
	if dbName == name {
		report.AddPass("terms.name")
	} else {
		report.AddFailure(Failure{
			CheckName: "terms.name",
			Severity:  SeverityError,
			Reason:    fmt.Sprintf("category name mismatch: source=%q db=%q", name, dbName),
			Source:    slug,
			Target:    id.String(),
		})
	}

	// Parent linkage. The importer resolves the parent only if
	// the WXR declared it before the child (rare to violate, but
	// legal); we treat an unresolved parent as a warn-level miss
	// because the row still exists, just flat.
	if strings.TrimSpace(parentNicename) == "" {
		// No declared parent — pass if the DB also has none, warn
		// otherwise (the importer would have to invent a parent).
		if dbParent == nil {
			report.AddPass("terms.parent")
		} else {
			report.AddFailure(Failure{
				CheckName: "terms.parent",
				Severity:  SeverityWarn,
				Reason:    "category has parent_id in DB but none in source",
				Source:    slug,
				Target:    id.String(),
			})
		}
		return nil
	}

	// Resolve the parent slug to a terms.id and compare.
	var parentID uuid.UUID
	err = v.DB.QueryRow(ctx, `
		SELECT id FROM terms WHERE taxonomy = 'category' AND slug = $1::citext LIMIT 1
	`, parentNicename).Scan(&parentID)
	if errors.Is(err, pgx.ErrNoRows) {
		report.AddFailure(Failure{
			CheckName: "terms.parent",
			Severity:  SeverityError,
			Reason:    fmt.Sprintf("parent category not found: slug=%q", parentNicename),
			Source:    slug,
			Target:    id.String(),
		})
		return nil
	}
	if err != nil {
		return wrapVerifyErr("terms.parent", err)
	}
	if dbParent != nil && *dbParent == parentID {
		report.AddPass("terms.parent")
	} else {
		report.AddFailure(Failure{
			CheckName: "terms.parent",
			Severity:  SeverityWarn,
			Reason:    fmt.Sprintf("category parent_id unresolved: want=%s", parentID),
			Source:    slug,
			Target:    id.String(),
		})
	}
	return nil
}

// checkOneTag probes one tag row and verifies name.
func (v *Verifier) checkOneTag(ctx context.Context, slug, name string, report *Report) error {
	if strings.TrimSpace(slug) == "" {
		return nil
	}
	var (
		id     uuid.UUID
		dbName string
	)
	err := v.DB.QueryRow(ctx, `
		SELECT id, name
		  FROM terms
		 WHERE taxonomy = 'tag' AND slug = $1::citext
		 LIMIT 1
	`, slug).Scan(&id, &dbName)
	if errors.Is(err, pgx.ErrNoRows) {
		report.AddFailure(Failure{
			CheckName: "terms.name",
			Severity:  SeverityError,
			Reason:    fmt.Sprintf("tag not found: slug=%q", slug),
			Source:    slug,
		})
		return nil
	}
	if err != nil {
		return wrapVerifyErr("terms.probe", err)
	}
	if name == "" {
		name = slug
	}
	if dbName == name {
		report.AddPass("terms.name")
	} else {
		report.AddFailure(Failure{
			CheckName: "terms.name",
			Severity:  SeverityError,
			Reason:    fmt.Sprintf("tag name mismatch: source=%q db=%q", name, dbName),
			Source:    slug,
			Target:    id.String(),
		})
	}
	return nil
}
