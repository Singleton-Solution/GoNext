package marketplace

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

// =============================================================================
// Errors
// =============================================================================

// ErrNotFound is returned by Get-shaped methods when no row matches.
// Callers errors.Is against this to distinguish "missing" from "bad
// database".
var ErrNotFound = errors.New("marketplace: not found")

// ErrAlreadyExists is returned when a UNIQUE constraint is violated by
// an Insert. Currently triggered by:
//
//   - Listings.Create when the slug is taken,
//   - Versions.Publish when (listing_id, version) is taken,
//   - Ratings.Submit is exempt — it UPSERTs.
var ErrAlreadyExists = errors.New("marketplace: already exists")

// ErrInvalidInput signals that the caller-supplied arguments are
// malformed in a way that doesn't need to reach the database (empty
// slug, stars out of range, etc.). The wrapping format includes the
// offending field name.
var ErrInvalidInput = errors.New("marketplace: invalid input")

// =============================================================================
// Listing
// =============================================================================

// ListingStatus enumerates the lifecycle column on plugin_listings.
// Values mirror the CHECK constraint in 000018_plugin_listings.up.sql;
// callers should use the constants rather than string literals so a
// typo is caught at compile time.
type ListingStatus string

const (
	// ListingDraft — the publisher is still preparing the listing.
	// Not surfaced in the catalogue's main view; reachable only via
	// the owner's dashboard.
	ListingDraft ListingStatus = "draft"

	// ListingListed — visible in the catalogue, installable.
	ListingListed ListingStatus = "listed"

	// ListingDelisted — temporarily hidden by the owner or platform.
	// Existing installs continue to work; new discovery is paused.
	ListingDelisted ListingStatus = "delisted"

	// ListingBanned — permanent moderation action. Distinct from
	// delisted so the audit trail records intent.
	ListingBanned ListingStatus = "banned"
)

// Valid reports whether s is one of the defined ListingStatus values.
// Store methods call this before INSERT/UPDATE to refuse writes that
// would corrupt the column.
func (s ListingStatus) Valid() bool {
	switch s {
	case ListingDraft, ListingListed, ListingDelisted, ListingBanned:
		return true
	default:
		return false
	}
}

// Listing is one row of the plugin_listings table.
//
// Fields are populated by Store methods; callers receive Listing values
// from Get/List and should treat them as read-only snapshots. The PK is
// minted by the database via gen_uuid_v7() — callers should leave ID
// zero on Create and read it back from the returned value.
type Listing struct {
	// ID is the time-sortable UUID v7 minted by the database.
	ID uuid.UUID

	// Slug is the public-facing handle that appears in URLs. Must be
	// non-empty; uniqueness is enforced at the database.
	Slug string

	// Name is the human-facing display label. Required.
	Name string

	// Summary is the one-line description shown on catalogue cards.
	// Optional; empty string means "no summary yet".
	Summary string

	// AuthorID is the publishing user. Zero UUID means "unowned"
	// (e.g. after the owner's account was deleted, the FK was nulled).
	AuthorID uuid.UUID

	// HomepageURL is an optional project page. Plain text; no URL
	// validation at this layer.
	HomepageURL string

	// LicenseSPDX is the SPDX licence identifier ("MIT", "Apache-2.0").
	// Opaque text — we don't validate against the SPDX catalogue.
	LicenseSPDX string

	// PrimaryCategory is the free-form category string used for
	// catalogue browsing. Optional.
	PrimaryCategory string

	// Status is the lifecycle position. Always one of the ListingStatus
	// constants; defaults to ListingDraft on Create when zero.
	Status ListingStatus

	// CreatedAt / UpdatedAt are the row-scoped audit timestamps.
	// CreatedAt is set once at Insert; UpdatedAt is bumped by the
	// marketplace_touch_updated_at trigger on every UPDATE.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// =============================================================================
// Version
// =============================================================================

// Version is one row of the plugin_versions table.
//
// The wasm artefact itself is NOT carried on this struct — only its
// SHA-256 digest. Callers are expected to push the bytes to object
// storage keyed by WasmSHA256; the digest is the content-address.
type Version struct {
	ID uuid.UUID

	// ListingID is the parent plugin_listings.id. Required.
	ListingID uuid.UUID

	// SemVer string verbatim from the manifest ("1.4.2", "2.0.0-beta.1").
	// Comparison is done at the application layer using semver libs.
	Version string

	// WasmSHA256 is 32 raw bytes — the artefact's content digest.
	// Publish computes this from the supplied wasm bytes; callers
	// reading Version values can compare against the bytes they have
	// in object storage.
	WasmSHA256 []byte

	// Manifest is the raw manifest.json blob captured at publish time.
	// Stored as json.RawMessage so this package doesn't lock a Go
	// struct shape into the marketplace contract.
	Manifest json.RawMessage

	// SignatureHex is the optional detached signature, hex-encoded.
	// Empty string means "unsigned" (allowed in v1).
	SignatureHex string

	// PublishedAt is the moment Publish succeeded.
	PublishedAt time.Time

	// DeprecatedAt is non-zero when the publisher or platform has
	// flagged this version as deprecated. Deprecated versions remain
	// installable but the marketplace UI surfaces a banner.
	DeprecatedAt time.Time
}

// =============================================================================
// Compat range
// =============================================================================

// CompatRange is one row of the plugin_compat_matrix table — a single
// host ABI range that a Version claims compatibility with.
type CompatRange struct {
	PluginVersionID uuid.UUID

	// HostMin / HostMax bound the supported host ABI inclusively.
	// Strings rather than ints because the host versioning scheme is
	// semver-shaped.
	HostMin string
	HostMax string

	// Tested is true when the publisher actually exercised the plugin
	// against this range under CI. False = declared compatible but
	// unverified.
	Tested bool
}

// =============================================================================
// Rating
// =============================================================================

// Rating is one row of the plugin_ratings table.
//
// One row per (plugin_version_id, user_id). Re-submitting overwrites
// the previous rating — see Ratings.Submit which uses ON CONFLICT DO
// UPDATE.
type Rating struct {
	PluginVersionID uuid.UUID
	UserID          uuid.UUID

	// Stars is 1..5 inclusive. Submit validates before SQL.
	Stars int16

	// ReviewText is an optional written review. Empty string means
	// "no text", not "review with a literal empty body".
	ReviewText string

	CreatedAt time.Time
}

// Aggregate is the avg+count summary computed by Ratings.Aggregate.
// Returned for one Version at a time — callers wanting a per-listing
// aggregate join across versions in their own SQL.
type Aggregate struct {
	// PluginVersionID is the version this aggregate describes. Zeroed
	// when the version has zero ratings.
	PluginVersionID uuid.UUID

	// Average is the arithmetic mean of stars. Zero when Count == 0.
	Average float64

	// Count is the number of ratings included in the average.
	Count int64
}

// =============================================================================
// Install event
// =============================================================================

// InstallEventType enumerates the event_type column on
// plugin_install_events. The CHECK constraint in
// 000022_plugin_install_events.up.sql is the source of truth; these
// constants mirror it so callers don't pass raw strings.
type InstallEventType string

const (
	EventInstalled   InstallEventType = "installed"
	EventActivated   InstallEventType = "activated"
	EventUninstalled InstallEventType = "uninstalled"
	EventErrored     InstallEventType = "errored"
)

// Valid reports whether t is one of the defined InstallEventType
// values. RecordInstallEvent calls this before INSERT.
func (t InstallEventType) Valid() bool {
	switch t {
	case EventInstalled, EventActivated, EventUninstalled, EventErrored:
		return true
	default:
		return false
	}
}

// InstallEvent is one row of the plugin_install_events table.
//
// Append-only — there is no Update or Delete method on Events. The
// auto-increment BIGSERIAL id is read back by RecordInstallEvent so
// callers can correlate against logs.
type InstallEvent struct {
	// ID is the monotonic event id minted by BIGSERIAL.
	ID int64

	// ListingID / VersionID may be zero when the event references a
	// listing/version that has since been hard-deleted (FK is set to
	// NULL on parent delete).
	ListingID uuid.UUID
	VersionID uuid.UUID

	// HostID is the opaque host identifier — hashed signature, not a
	// directly identifying value. Required (non-empty).
	HostID string

	// EventType is one of the InstallEventType constants.
	EventType InstallEventType

	CreatedAt time.Time
}
