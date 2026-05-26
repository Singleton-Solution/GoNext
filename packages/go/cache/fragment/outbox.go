package fragment

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PgOutboxWriter writes tag invalidations into the cache_invalidations
// table backed by a pgx connection pool. It is the production-default
// implementation of OutboxWriter; tests typically use a recording
// fake instead of standing up a real Postgres.
//
// The writer is intentionally tiny — one INSERT … SELECT — because
// the invalidation pipeline's contract is already nailed down by the
// invalidator worker (packages/go/cache/invalidator). All that remains
// here is "row-into-table"; everything interesting (pub/sub publish,
// at-least-once delivery, namespacing) happens downstream.
type PgOutboxWriter struct {
	pool *pgxpool.Pool
}

// NewPgOutboxWriter constructs an OutboxWriter backed by pool. Passing
// nil panics — a misconfigured writer that silently drops rows would
// be a real cache-correctness bug (a Purge that "succeeds" but never
// invalidates anything is worse than a noisy failure).
func NewPgOutboxWriter(pool *pgxpool.Pool) *PgOutboxWriter {
	if pool == nil {
		panic("fragment.NewPgOutboxWriter: pool is required")
	}
	return &PgOutboxWriter{pool: pool}
}

// WriteInvalidations appends one row per tag into cache_invalidations.
// Uses INSERT … SELECT FROM unnest to do the batch in a single
// round-trip; even at MaxTagsPerEntry the batch fits in one packet.
//
// The slug column is the namespace the invalidator worker re-prefixes
// when it publishes. For fragment-cache traffic the slug is always
// "gnf" so a subscriber that wants to route on internal traffic vs.
// plugin traffic only needs to match a single prefix.
func (w *PgOutboxWriter) WriteInvalidations(ctx context.Context, slug string, tags []string) error {
	if slug == "" {
		return errors.New("fragment: WriteInvalidations: slug is required")
	}
	if len(tags) == 0 {
		return nil
	}
	if _, err := w.pool.Exec(ctx, `
		INSERT INTO cache_invalidations (plugin_slug, tag)
		SELECT $1, unnest($2::text[])`,
		slug, tags); err != nil {
		return fmt.Errorf("fragment: WriteInvalidations: %w", err)
	}
	return nil
}
