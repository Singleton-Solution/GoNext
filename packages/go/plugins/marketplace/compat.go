package marketplace

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// CompatStore is the store for the plugin_compat_matrix table.
//
// A version may declare multiple disjoint ranges; the composite PK
// (plugin_version_id, host_min, host_max) is the lookup key. Re-
// publishing a matrix UPSERTs on the same tuple — the publisher's
// intent is "this range is now (un)tested", not "create a duplicate".
type CompatStore struct {
	db PgxQuerier
}

func NewCompatStore(db PgxQuerier) *CompatStore {
	if db == nil {
		panic("marketplace.NewCompatStore: db is required")
	}
	return &CompatStore{db: db}
}

// Upsert writes a compat range. On conflict (same PK tuple) the
// `tested` column is updated to the supplied value — that's the only
// field that meaningfully changes between re-publishes; the (min, max)
// pair *is* the key, so changing them produces a new row.
//
// The sanity CHECK host_min <= host_max is enforced by the column
// constraint; a violation surfaces as the underlying pgx error.
func (c *CompatStore) Upsert(ctx context.Context, in CompatRange) error {
	if in.PluginVersionID == uuid.Nil {
		return fmt.Errorf("%w: plugin_version_id is required", ErrInvalidInput)
	}
	if in.HostMin == "" {
		return fmt.Errorf("%w: host_min is required", ErrInvalidInput)
	}
	if in.HostMax == "" {
		return fmt.Errorf("%w: host_max is required", ErrInvalidInput)
	}
	if in.HostMin > in.HostMax {
		// Lexicographic check is intentionally coarse — see the
		// CHECK comment in 000020. Catches the typo class without
		// claiming to validate semver.
		return fmt.Errorf("%w: host_min %q > host_max %q", ErrInvalidInput, in.HostMin, in.HostMax)
	}

	const sql = `
		INSERT INTO plugin_compat_matrix
			(plugin_version_id, host_min, host_max, tested)
		VALUES
			($1, $2, $3, $4)
		ON CONFLICT (plugin_version_id, host_min, host_max)
		DO UPDATE SET tested = EXCLUDED.tested
	`
	_, err := c.db.Exec(ctx, sql,
		in.PluginVersionID, in.HostMin, in.HostMax, in.Tested,
	)
	if err != nil {
		return fmt.Errorf("marketplace.Compat.Upsert: %w", err)
	}
	return nil
}

// ListByVersion returns every compat range declared by a version,
// ordered by (host_min, host_max). Empty result is not an error.
func (c *CompatStore) ListByVersion(ctx context.Context, versionID uuid.UUID) ([]CompatRange, error) {
	if versionID == uuid.Nil {
		return nil, fmt.Errorf("%w: version_id is required", ErrInvalidInput)
	}
	rows, err := c.db.Query(ctx, `
		SELECT plugin_version_id, host_min, host_max, tested
		  FROM plugin_compat_matrix
		 WHERE plugin_version_id = $1
		 ORDER BY host_min, host_max
	`, versionID)
	if err != nil {
		return nil, fmt.Errorf("marketplace.Compat.ListByVersion: %w", err)
	}
	defer rows.Close()

	out := []CompatRange{}
	for rows.Next() {
		var r CompatRange
		if err := rows.Scan(&r.PluginVersionID, &r.HostMin, &r.HostMax, &r.Tested); err != nil {
			return nil, fmt.Errorf("marketplace.Compat.ListByVersion: scan: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("marketplace.Compat.ListByVersion: rows: %w", err)
	}
	return out, nil
}
