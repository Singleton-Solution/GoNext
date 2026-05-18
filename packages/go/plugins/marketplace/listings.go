package marketplace

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Listings is the store for the plugin_listings table.
//
// The slug column carries the application-level UNIQUE constraint;
// Create translates the resulting SQLSTATE 23505 into ErrAlreadyExists
// so callers can errors.Is on the sentinel without parsing pgx errors.
type Listings struct {
	db PgxQuerier

	// NowFunc, if set, replaces time.Now for the CreatedAt/UpdatedAt
	// defaults Create assigns when the caller leaves them zero.
	NowFunc nowFunc
}

// NewListings wraps a PgxQuerier. The querier's lifecycle is the
// caller's responsibility — Listings does not own it.
func NewListings(db PgxQuerier) *Listings {
	if db == nil {
		panic("marketplace.NewListings: db is required")
	}
	return &Listings{db: db}
}

const listingsSelectColumns = `
    id, slug, name,
    COALESCE(summary, ''),
    COALESCE(author_id, '00000000-0000-0000-0000-000000000000'::uuid),
    COALESCE(homepage_url, ''),
    COALESCE(license_spdx, ''),
    COALESCE(primary_category, ''),
    status, created_at, updated_at
`

// Create inserts a new listing. The id is minted by gen_uuid_v7() on
// the database side; the returned Listing carries the canonical id +
// timestamps so callers don't have to re-Get.
//
// Slug uniqueness is enforced by the column constraint. A duplicate
// slug returns ErrAlreadyExists wrapped with the slug.
func (l *Listings) Create(ctx context.Context, in Listing) (Listing, error) {
	if in.Slug == "" {
		return Listing{}, fmt.Errorf("%w: slug is required", ErrInvalidInput)
	}
	if in.Name == "" {
		return Listing{}, fmt.Errorf("%w: name is required", ErrInvalidInput)
	}
	if in.Status == "" {
		in.Status = ListingDraft
	}
	if !in.Status.Valid() {
		return Listing{}, fmt.Errorf("%w: invalid status %q", ErrInvalidInput, in.Status)
	}

	// Pass NULL for optional columns when the caller left them empty;
	// the database side defaults to NULL which COALESCE'd back to "" on
	// read. This keeps the round-trip lossless for the empty case.
	var authorID any
	if in.AuthorID != uuid.Nil {
		authorID = in.AuthorID
	}
	var summary any
	if in.Summary != "" {
		summary = in.Summary
	}
	var homepage any
	if in.HomepageURL != "" {
		homepage = in.HomepageURL
	}
	var license any
	if in.LicenseSPDX != "" {
		license = in.LicenseSPDX
	}
	var category any
	if in.PrimaryCategory != "" {
		category = in.PrimaryCategory
	}

	const sql = `
		INSERT INTO plugin_listings
			(slug, name, summary, author_id, homepage_url, license_spdx, primary_category, status)
		VALUES
			($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING ` + listingsSelectColumns

	row := l.db.QueryRow(ctx, sql,
		in.Slug, in.Name, summary, authorID, homepage, license, category, string(in.Status),
	)
	out, err := scanListing(row)
	if err != nil {
		if isUniqueViolation(err) {
			return Listing{}, fmt.Errorf("%w: slug %q", ErrAlreadyExists, in.Slug)
		}
		return Listing{}, fmt.Errorf("marketplace.Listings.Create: %w", err)
	}
	return out, nil
}

// Get returns the listing identified by id, or ErrNotFound.
func (l *Listings) Get(ctx context.Context, id uuid.UUID) (Listing, error) {
	row := l.db.QueryRow(ctx,
		`SELECT `+listingsSelectColumns+` FROM plugin_listings WHERE id = $1`,
		id,
	)
	out, err := scanListing(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Listing{}, fmt.Errorf("%w: listing id %s", ErrNotFound, id)
		}
		return Listing{}, fmt.Errorf("marketplace.Listings.Get: %w", err)
	}
	return out, nil
}

// GetBySlug returns the listing identified by slug, or ErrNotFound.
// Slug is the natural lookup key for URL-driven catalogue queries.
func (l *Listings) GetBySlug(ctx context.Context, slug string) (Listing, error) {
	if slug == "" {
		return Listing{}, fmt.Errorf("%w: slug is required", ErrInvalidInput)
	}
	row := l.db.QueryRow(ctx,
		`SELECT `+listingsSelectColumns+` FROM plugin_listings WHERE slug = $1`,
		slug,
	)
	out, err := scanListing(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Listing{}, fmt.Errorf("%w: slug %q", ErrNotFound, slug)
		}
		return Listing{}, fmt.Errorf("marketplace.Listings.GetBySlug: %w", err)
	}
	return out, nil
}

// List returns every listing, ordered by created_at DESC so the most
// recent draft/listing surfaces first. No pagination — the catalogue
// is expected to hold hundreds, not millions, of rows.
func (l *Listings) List(ctx context.Context) ([]Listing, error) {
	rows, err := l.db.Query(ctx,
		`SELECT `+listingsSelectColumns+` FROM plugin_listings ORDER BY created_at DESC, id ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("marketplace.Listings.List: %w", err)
	}
	defer rows.Close()

	out := []Listing{}
	for rows.Next() {
		lst, scanErr := scanListing(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("marketplace.Listings.List: scan: %w", scanErr)
		}
		out = append(out, lst)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("marketplace.Listings.List: rows: %w", err)
	}
	return out, nil
}

// ListByCategory returns every Listed (i.e. visible) listing whose
// primary_category matches the supplied value. Empty category is
// rejected with ErrInvalidInput — call List for "everything".
//
// Only Listed rows are returned because draft/delisted/banned have no
// place in catalogue browsing. The partial index on
// (primary_category) WHERE status = 'listed' serves this query.
func (l *Listings) ListByCategory(ctx context.Context, category string) ([]Listing, error) {
	if category == "" {
		return nil, fmt.Errorf("%w: category is required", ErrInvalidInput)
	}
	rows, err := l.db.Query(ctx,
		`SELECT `+listingsSelectColumns+`
		   FROM plugin_listings
		  WHERE primary_category = $1
		    AND status = 'listed'
		  ORDER BY created_at DESC, id ASC`,
		category,
	)
	if err != nil {
		return nil, fmt.Errorf("marketplace.Listings.ListByCategory: %w", err)
	}
	defer rows.Close()

	out := []Listing{}
	for rows.Next() {
		lst, scanErr := scanListing(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("marketplace.Listings.ListByCategory: scan: %w", scanErr)
		}
		out = append(out, lst)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("marketplace.Listings.ListByCategory: rows: %w", err)
	}
	return out, nil
}

// Update applies a partial update.
//
// The caller passes the fields they want to change via a ListingUpdate
// struct; nil fields are left untouched. Returns the updated row.
//
// The status transition rules (e.g. "banned" should not move back to
// "listed" without moderator action) are policy-layer concerns and are
// NOT enforced here — Update accepts any valid status. The CHECK
// constraint covers the typo class; everything else is the caller's
// responsibility.
func (l *Listings) Update(ctx context.Context, id uuid.UUID, in ListingUpdate) (Listing, error) {
	// Validate enums before SQL so a bogus status comes back as
	// ErrInvalidInput rather than as a CHECK violation.
	if in.Status != nil && !in.Status.Valid() {
		return Listing{}, fmt.Errorf("%w: invalid status %q", ErrInvalidInput, *in.Status)
	}

	// COALESCE pattern: for each updatable column, write either the
	// caller's value (when the pointer is non-nil) or fall back to the
	// current value. The pointer-of-string trick keeps the SQL
	// statement shape constant — no dynamic SET-list assembly.
	const sql = `
		UPDATE plugin_listings SET
			name             = COALESCE($2, name),
			summary          = COALESCE($3, summary),
			homepage_url     = COALESCE($4, homepage_url),
			license_spdx     = COALESCE($5, license_spdx),
			primary_category = COALESCE($6, primary_category),
			status           = COALESCE($7, status)
		 WHERE id = $1
		 RETURNING ` + listingsSelectColumns

	var statusArg any
	if in.Status != nil {
		statusArg = string(*in.Status)
	}

	row := l.db.QueryRow(ctx, sql,
		id, in.Name, in.Summary, in.HomepageURL, in.LicenseSPDX, in.PrimaryCategory, statusArg,
	)
	out, err := scanListing(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Listing{}, fmt.Errorf("%w: listing id %s", ErrNotFound, id)
		}
		return Listing{}, fmt.Errorf("marketplace.Listings.Update: %w", err)
	}
	return out, nil
}

// Delete hard-deletes the listing row. Versions, ratings, and compat
// rows cascade via FK; install_events are NULL'd (FK ON DELETE SET
// NULL) so the historical telemetry survives.
//
// Returns ErrNotFound when no row was deleted.
func (l *Listings) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := l.db.Exec(ctx, `DELETE FROM plugin_listings WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("marketplace.Listings.Delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: listing id %s", ErrNotFound, id)
	}
	return nil
}

// ListingUpdate is the partial-update payload for Listings.Update.
// nil-valued fields are left unchanged.
type ListingUpdate struct {
	Name            *string
	Summary         *string
	HomepageURL     *string
	LicenseSPDX     *string
	PrimaryCategory *string
	Status          *ListingStatus
}

// scanListing reads one row into a Listing. Shared between Get, List,
// Create-with-RETURNING.
type pgxScannable interface {
	Scan(dest ...any) error
}

func scanListing(s pgxScannable) (Listing, error) {
	var (
		l         Listing
		statusStr string
		authorID  uuid.UUID
		createdAt time.Time
		updatedAt time.Time
	)
	if err := s.Scan(
		&l.ID, &l.Slug, &l.Name,
		&l.Summary, &authorID, &l.HomepageURL, &l.LicenseSPDX, &l.PrimaryCategory,
		&statusStr, &createdAt, &updatedAt,
	); err != nil {
		return Listing{}, err
	}
	l.Status = ListingStatus(statusStr)
	// Nil UUID = no author (FK was NULL or SET NULL'd).
	if authorID != uuid.Nil {
		l.AuthorID = authorID
	}
	l.CreatedAt = createdAt
	l.UpdatedAt = updatedAt
	return l, nil
}
