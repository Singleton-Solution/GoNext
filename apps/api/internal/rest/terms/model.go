package terms

import (
	"context"
	"errors"
	"time"
)

const (
	DefaultListLimit = 50
	MaxListLimit     = 200
)

// Taxonomy is the public wire shape for a taxonomy registry row.
type Taxonomy struct {
	Slug         string    `json:"slug"`
	Name         string    `json:"name"`
	NamePlural   string    `json:"name_plural"`
	Hierarchical bool      `json:"hierarchical"`
	CreatedAt    time.Time `json:"created_at"`
}

// Term is the public wire shape for a term row. The `count` field
// (denormalised post count from the term_relationships trigger) is
// included because every frontend that renders a tag cloud or
// category list needs it; computing it client-side would force a
// fan-out across the relationship table.
type Term struct {
	ID       string    `json:"id"`
	Slug     string    `json:"slug"`
	Name     string    `json:"name"`
	Taxonomy string    `json:"taxonomy"`
	ParentID *string   `json:"parent_id,omitempty"`
	Path     string    `json:"path"`
	Depth    int       `json:"depth"`
	Count    int       `json:"count"`
	CreatedAt time.Time `json:"created_at"`
}

// TermListFilter narrows the term list query.
type TermListFilter struct {
	// Taxonomy is the taxonomy slug; empty means "all".
	Taxonomy string

	// ParentID restricts to direct children of the given parent.
	// The empty string + ParentPresent=true means "top-level
	// only"; ParentPresent=false means "any depth".
	ParentID      string
	ParentPresent bool

	// Search is a prefix on term.name (case-insensitive). Empty
	// means no name filter.
	Search string

	Limit int
	After string
}

// Store is the public read-only persistence boundary for terms +
// taxonomies. Combined into one interface because the wire-side
// pagination cursor encoding is the same for both, and a single
// pool/connection serves both lookups.
type Store interface {
	ListTaxonomies(ctx context.Context) ([]Taxonomy, error)
	GetTaxonomy(ctx context.Context, slug string) (Taxonomy, error)
	ListTerms(ctx context.Context, f TermListFilter) ([]Term, error)
	GetTerm(ctx context.Context, id string) (Term, error)
}

// ErrNotFound is the sentinel returned by store reads when the row
// is missing. The handler maps to HTTP 404.
var ErrNotFound = errors.New("rest/terms: not found")
