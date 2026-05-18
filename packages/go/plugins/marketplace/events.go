package marketplace

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Events is the store for the plugin_install_events table.
//
// The shape of this store is intentionally narrow: write-side is
// RecordInstallEvent (one append per host action), read-side surfaces
// two aggregate queries that the marketplace UI needs (popularity
// over a time window, error rate per version).
//
// There is no Update or Delete entry point — install events are
// append-only by contract. Operators wanting to scrub specific events
// must use a moderation pipeline that issues raw DELETEs through a
// separate audit-logged path.
type Events struct {
	db PgxQuerier

	// NowFunc, if set, replaces time.Now for the CreatedAt default
	// RecordInstallEvent assigns when the column is left to default.
	NowFunc nowFunc
}

func NewEvents(db PgxQuerier) *Events {
	if db == nil {
		panic("marketplace.NewEvents: db is required")
	}
	return &Events{db: db}
}

// RecordInstallEvent appends one row to plugin_install_events and
// returns it with the BIGSERIAL id read back. Callers correlate the
// id against logs.
//
// HostID is required; the application layer is expected to hand in
// the privacy-respecting hashed signature, not a raw identifier.
//
// Returns ErrInvalidInput on missing required fields. FK NULLs are
// allowed for listing_id / version_id because the historical event
// may outlive the parent row (ON DELETE SET NULL on both FKs).
func (e *Events) RecordInstallEvent(ctx context.Context, in InstallEvent) (InstallEvent, error) {
	if in.HostID == "" {
		return InstallEvent{}, fmt.Errorf("%w: host_id is required", ErrInvalidInput)
	}
	if !in.EventType.Valid() {
		return InstallEvent{}, fmt.Errorf("%w: invalid event_type %q", ErrInvalidInput, in.EventType)
	}

	var listingArg any
	if in.ListingID != uuid.Nil {
		listingArg = in.ListingID
	}
	var versionArg any
	if in.VersionID != uuid.Nil {
		versionArg = in.VersionID
	}
	var createdAtArg any
	if !in.CreatedAt.IsZero() {
		createdAtArg = in.CreatedAt.UTC()
	} else if e.NowFunc != nil {
		createdAtArg = e.NowFunc().UTC()
	}

	const sql = `
		INSERT INTO plugin_install_events
			(listing_id, version_id, host_id, event_type, created_at)
		VALUES
			($1, $2, $3, $4, COALESCE($5, now()))
		RETURNING id, COALESCE(listing_id, '00000000-0000-0000-0000-000000000000'::uuid),
		          COALESCE(version_id, '00000000-0000-0000-0000-000000000000'::uuid),
		          host_id, event_type, created_at
	`
	row := e.db.QueryRow(ctx, sql,
		listingArg, versionArg, in.HostID, string(in.EventType), createdAtArg,
	)
	var (
		out         InstallEvent
		listingID   uuid.UUID
		versionID   uuid.UUID
		eventType   string
		createdAtTs time.Time
	)
	if err := row.Scan(
		&out.ID, &listingID, &versionID, &out.HostID, &eventType, &createdAtTs,
	); err != nil {
		return InstallEvent{}, fmt.Errorf("marketplace.Events.RecordInstallEvent: %w", err)
	}
	if listingID != uuid.Nil {
		out.ListingID = listingID
	}
	if versionID != uuid.Nil {
		out.VersionID = versionID
	}
	out.EventType = InstallEventType(eventType)
	out.CreatedAt = createdAtTs
	return out, nil
}

// CountByListing returns the total event count for a listing over the
// supplied window. Zero duration is treated as "all time" — handy for
// the simple "lifetime installs" badge on the catalogue card.
//
// Feeds the marketplace popularity carousel.
func (e *Events) CountByListing(ctx context.Context, listingID uuid.UUID, window time.Duration) (int64, error) {
	if listingID == uuid.Nil {
		return 0, fmt.Errorf("%w: listing_id is required", ErrInvalidInput)
	}
	var (
		count int64
		err   error
	)
	if window <= 0 {
		err = e.db.QueryRow(ctx,
			`SELECT COUNT(*) FROM plugin_install_events WHERE listing_id = $1`,
			listingID,
		).Scan(&count)
	} else {
		cutoff := resolveNow(e.NowFunc).Add(-window)
		err = e.db.QueryRow(ctx, `
			SELECT COUNT(*)
			  FROM plugin_install_events
			 WHERE listing_id = $1
			   AND created_at >= $2
		`, listingID, cutoff).Scan(&count)
	}
	if err != nil {
		return 0, fmt.Errorf("marketplace.Events.CountByListing: %w", err)
	}
	return count, nil
}
