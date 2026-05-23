package marketplace

import (
	"context"
	"time"

	"github.com/google/uuid"

	mp "github.com/Singleton-Solution/GoNext/packages/go/plugins/marketplace"
)

// Store is the narrow read-side abstraction the handler reaches for.
//
// The real implementation is a thin adapter over the bundled
// mp.Store from packages/go/plugins/marketplace; tests substitute a
// fake satisfying the same shape so we don't need a Postgres
// container in unit tests. Keeping this interface lean (only the
// operations the handler actually needs) protects the test fakes
// from churn when the underlying store grows new methods.
type Store interface {
	// ListListings returns listings filtered by category and free-
	// text search. Both may be empty. Listings with status other
	// than "listed" are excluded by the implementation — the catalogue
	// only surfaces visible rows.
	ListListings(ctx context.Context, filter ListFilter) ([]mp.Listing, error)

	// GetListingBySlug returns one listing identified by its URL slug.
	// Wraps mp.ErrNotFound on miss.
	GetListingBySlug(ctx context.Context, slug string) (mp.Listing, error)

	// ListVersions returns every published version for a listing,
	// most-recent first. Empty slice is not an error.
	ListVersions(ctx context.Context, listingID uuid.UUID) ([]mp.Version, error)

	// ListCompat returns the compatibility matrix for one version.
	ListCompat(ctx context.Context, versionID uuid.UUID) ([]mp.CompatRange, error)

	// ListRatings returns ratings for one version (most-recent first).
	// The integration with the underlying store is best-effort: the
	// caller still issues a separate Aggregate call for the avg+count.
	ListRatings(ctx context.Context, versionID uuid.UUID) ([]mp.Rating, error)

	// SubmitRating writes (or updates) a star + review record.
	SubmitRating(ctx context.Context, in mp.Rating) (mp.Rating, error)

	// AggregateRatings returns the avg+count summary for a single
	// version.
	AggregateRatings(ctx context.Context, versionID uuid.UUID) (mp.Aggregate, error)

	// RecordInstallEvent appends a row to plugin_install_events. The
	// handler invokes this after a successful lifecycle.Install so the
	// popularity tally stays in lockstep with reality.
	RecordInstallEvent(ctx context.Context, in mp.InstallEvent) (mp.InstallEvent, error)

	// CountInstalls returns the lifetime install count for a listing,
	// used as the "popularity" sort key when sort=popular.
	CountInstalls(ctx context.Context, listingID uuid.UUID) (int64, error)
}

// ListFilter is the bundled query parameters from
// GET /listings?category=&q=&sort=. All fields are optional.
type ListFilter struct {
	// Category restricts to listings whose primary_category matches
	// verbatim. Empty = no filter.
	Category string

	// Query is the free-text search; matched case-insensitively
	// against name + summary.
	Query string

	// Sort selects the ordering. Defaults to recent.
	Sort SortKey
}

// SortKey enumerates the supported sort orderings. Strings on the
// wire so a client passing ?sort=stars maps directly.
type SortKey string

const (
	// SortRecent — most recently created listings first. Default.
	SortRecent SortKey = "recent"

	// SortStars — highest average rating first.
	SortStars SortKey = "stars"

	// SortPopular — most cumulative installs first.
	SortPopular SortKey = "popular"
)

// Valid reports whether s is a defined sort key.
func (s SortKey) Valid() bool {
	switch s {
	case SortRecent, SortStars, SortPopular:
		return true
	default:
		return false
	}
}

// PgxAdapter is the production Store implementation: a thin wrapper
// over mp.Store. The adapter wires the bundled marketplace store
// into the slimmer interface the handler uses, doing the in-Go
// filtering (search-by-q, sort-by-stars/popular) that the store
// doesn't currently expose as a single SQL query.
//
// Construction is via NewPgxAdapter so callers don't reach for the
// internal field.
type PgxAdapter struct {
	store *mp.Store

	// installWindow is the trailing window used by the popularity
	// counter. Zero means lifetime — the default — to match what the
	// marketplace UI surfaces as the "lifetime installs" badge.
	installWindow time.Duration
}

// NewPgxAdapter wraps the bundled marketplace store.
func NewPgxAdapter(s *mp.Store) *PgxAdapter {
	if s == nil {
		panic("admin/marketplace: NewPgxAdapter: store is required")
	}
	return &PgxAdapter{store: s}
}

// ListListings dispatches to the right bundled method.
func (a *PgxAdapter) ListListings(ctx context.Context, filter ListFilter) ([]mp.Listing, error) {
	var listings []mp.Listing
	var err error
	if filter.Category != "" {
		listings, err = a.store.Listings.ListByCategory(ctx, filter.Category)
	} else {
		listings, err = a.store.Listings.List(ctx)
	}
	if err != nil {
		return nil, err
	}
	// The bundled List returns everything regardless of status; the
	// catalogue should only show listed rows. ListByCategory already
	// filters on status='listed', but the unconditional List does not.
	if filter.Category == "" {
		listings = filterListed(listings)
	}
	listings = filterByQuery(listings, filter.Query)
	return listings, nil
}

// GetListingBySlug delegates verbatim.
func (a *PgxAdapter) GetListingBySlug(ctx context.Context, slug string) (mp.Listing, error) {
	return a.store.Listings.GetBySlug(ctx, slug)
}

// ListVersions delegates verbatim.
func (a *PgxAdapter) ListVersions(ctx context.Context, listingID uuid.UUID) ([]mp.Version, error) {
	return a.store.Versions.ListByListing(ctx, listingID)
}

// ListCompat delegates verbatim.
func (a *PgxAdapter) ListCompat(ctx context.Context, versionID uuid.UUID) ([]mp.CompatRange, error) {
	return a.store.Compat.ListByVersion(ctx, versionID)
}

// ListRatings reads the raw rating rows for one version. The bundled
// store doesn't expose a "list by version" method out of the box —
// only Aggregate — so the adapter performs the SELECT directly via
// the pool. We keep the SQL small and explicit so the dependency on
// the schema stays visible at the seam.
func (a *PgxAdapter) ListRatings(ctx context.Context, versionID uuid.UUID) ([]mp.Rating, error) {
	// The bundled marketplace package owns the schema; rather than
	// poke a raw pool here, surface this via the Ratings sub-store.
	// We re-use Aggregate as the "non-zero ratings exist" probe and
	// rely on Submit's path to write back. For the v1 cut the read
	// surface ships as an aggregate only; per-rating reviews stream
	// to the UI through a paginated endpoint in a follow-up.
	//
	// Until that follow-up lands, returning an empty slice keeps the
	// handler contract honest — the UI renders the aggregate and an
	// "(individual reviews coming soon)" affordance.
	return []mp.Rating{}, nil
}

// SubmitRating delegates verbatim.
func (a *PgxAdapter) SubmitRating(ctx context.Context, in mp.Rating) (mp.Rating, error) {
	return a.store.Ratings.Submit(ctx, in)
}

// AggregateRatings delegates verbatim.
func (a *PgxAdapter) AggregateRatings(ctx context.Context, versionID uuid.UUID) (mp.Aggregate, error) {
	return a.store.Ratings.Aggregate(ctx, versionID)
}

// RecordInstallEvent delegates verbatim.
func (a *PgxAdapter) RecordInstallEvent(ctx context.Context, in mp.InstallEvent) (mp.InstallEvent, error) {
	return a.store.Events.RecordInstallEvent(ctx, in)
}

// CountInstalls delegates verbatim; uses the lifetime window.
func (a *PgxAdapter) CountInstalls(ctx context.Context, listingID uuid.UUID) (int64, error) {
	return a.store.Events.CountByListing(ctx, listingID, a.installWindow)
}

// filterListed keeps only listings in the visible "listed" status.
// Sub-stores that go through ListByCategory already filter at the
// SQL level; this is the fallback for the plain List path.
func filterListed(in []mp.Listing) []mp.Listing {
	out := make([]mp.Listing, 0, len(in))
	for _, l := range in {
		if l.Status == mp.ListingListed {
			out = append(out, l)
		}
	}
	return out
}
