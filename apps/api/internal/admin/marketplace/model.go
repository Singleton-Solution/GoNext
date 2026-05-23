package marketplace

import (
	"encoding/hex"
	"time"

	"github.com/google/uuid"

	mp "github.com/Singleton-Solution/GoNext/packages/go/plugins/marketplace"
)

// ListingCard is the compact projection returned by GET /listings.
// Mirrors what the admin grid renders — name, summary, sort signals.
type ListingCard struct {
	ID              uuid.UUID `json:"id"`
	Slug            string    `json:"slug"`
	Name            string    `json:"name"`
	Summary         string    `json:"summary"`
	HomepageURL     string    `json:"homepage_url,omitempty"`
	LicenseSPDX     string    `json:"license_spdx,omitempty"`
	PrimaryCategory string    `json:"primary_category,omitempty"`
	Stars           float64   `json:"stars"`
	RatingCount     int64     `json:"rating_count"`
	InstallCount    int64     `json:"install_count"`
	CreatedAt       time.Time `json:"created_at"`
}

// ListingDetail extends ListingCard with the full description payload
// the detail page needs. The versions + compat matrix + ratings
// aggregates are served separately so the detail endpoint stays cheap.
type ListingDetail struct {
	ListingCard

	AuthorID     uuid.UUID `json:"author_id,omitempty"`
	Status       string    `json:"status"`
	UpdatedAt    time.Time `json:"updated_at"`
	LatestVersion *VersionRow `json:"latest_version,omitempty"`
}

// VersionRow is the JSON shape of one plugin_versions row. The wasm
// digest is exposed as a hex string so operators can compare it
// against object-storage hashes; the bytes themselves are NOT
// surfaced here.
type VersionRow struct {
	ID            uuid.UUID `json:"id"`
	Version       string    `json:"version"`
	WasmSHA256Hex string    `json:"wasm_sha256_hex"`
	SignatureHex  string    `json:"signature_hex,omitempty"`
	PublishedAt   time.Time `json:"published_at"`
	Deprecated    bool      `json:"deprecated"`
	DeprecatedAt  time.Time `json:"deprecated_at,omitempty"`
	Manifest      string    `json:"manifest"`
}

// CompatRow mirrors plugin_compat_matrix on the wire.
type CompatRow struct {
	HostMin string `json:"host_min"`
	HostMax string `json:"host_max"`
	Tested  bool   `json:"tested"`
}

// RatingRow is the wire shape of a single rating + review.
type RatingRow struct {
	UserID     uuid.UUID `json:"user_id"`
	Stars      int16     `json:"stars"`
	ReviewText string    `json:"review_text,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// RatingsResponse bundles the aggregate + per-rating list.
type RatingsResponse struct {
	Aggregate struct {
		Average float64 `json:"average"`
		Count   int64   `json:"count"`
	} `json:"aggregate"`
	Ratings []RatingRow `json:"ratings"`
}

// InstallResponse is returned by POST /listings/{slug}/install.
type InstallResponse struct {
	Slug    string `json:"slug"`
	Version string `json:"version"`
	// PluginSlug is what the host's lifecycle.Install returned — it
	// may differ from the marketplace slug when the manifest's
	// internal slug doesn't match the marketplace handle.
	PluginSlug string `json:"plugin_slug"`
}

// SubmitRatingRequest is the body for POST /listings/{slug}/ratings.
type SubmitRatingRequest struct {
	VersionID  uuid.UUID `json:"version_id"`
	Stars      int16     `json:"stars"`
	ReviewText string    `json:"review_text,omitempty"`
}

// toCard projects a (Listing + signals) tuple onto the wire card.
func toCard(l mp.Listing, stars float64, count int64, installs int64) ListingCard {
	return ListingCard{
		ID:              l.ID,
		Slug:            l.Slug,
		Name:            l.Name,
		Summary:         l.Summary,
		HomepageURL:     l.HomepageURL,
		LicenseSPDX:     l.LicenseSPDX,
		PrimaryCategory: l.PrimaryCategory,
		Stars:           stars,
		RatingCount:     count,
		InstallCount:    installs,
		CreatedAt:       l.CreatedAt,
	}
}

// toVersionRow projects an mp.Version onto the wire shape.
func toVersionRow(v mp.Version) VersionRow {
	row := VersionRow{
		ID:            v.ID,
		Version:       v.Version,
		WasmSHA256Hex: hex.EncodeToString(v.WasmSHA256),
		SignatureHex:  v.SignatureHex,
		PublishedAt:   v.PublishedAt,
		Manifest:      string(v.Manifest),
	}
	if !v.DeprecatedAt.IsZero() {
		row.Deprecated = true
		row.DeprecatedAt = v.DeprecatedAt
	}
	return row
}

// toCompatRow projects an mp.CompatRange onto the wire shape.
func toCompatRow(c mp.CompatRange) CompatRow {
	return CompatRow{HostMin: c.HostMin, HostMax: c.HostMax, Tested: c.Tested}
}

// toRatingRow projects an mp.Rating onto the wire shape.
func toRatingRow(r mp.Rating) RatingRow {
	return RatingRow{
		UserID:     r.UserID,
		Stars:      r.Stars,
		ReviewText: r.ReviewText,
		CreatedAt:  r.CreatedAt,
	}
}
