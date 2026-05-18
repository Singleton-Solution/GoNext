// Package migmap is the durable "WP source ID → GoNext UUID" lookup
// the migration importer (issue #144) relies on for idempotency and
// for resolving intra-export references.
//
// The contract is small. An importer that has just inserted a GoNext
// user for WP user_id=42 records the mapping
//
//	migmap.Mapping{
//	    Source:     migmap.SourceWordPress,
//	    EntityType: migmap.EntityUser,
//	    SourceID:   "42",
//	    TargetID:   uuid.MustParse("..."),
//	}
//
// On a second pass over the same export it asks
//
//	if existing, ok, _ := store.Get(ctx, "wp", "user", "42"); ok {
//	    // skip — already imported
//	}
//
// and reuses existing.TargetID when wiring post.author or
// term_relationships rows.
//
// Storage tiers, top to bottom:
//
//   - [CachedStore] — process-local LRU. Read-through and write-through
//     so the next Get against the same key skips Postgres entirely.
//     Useful during a long import where the same author appears on
//     ten thousand posts.
//
//   - [PostgresStore] — durable, survives process restarts and Redis
//     flushes. Backed by the `migration_map` table
//     (migrations/000015_migration_map.up.sql).
//
// All implementations are safe for concurrent use. Put and PutBatch
// accept a [Tx] so the importer can record the mapping in the same
// transaction as the insert that produced TargetID — if the
// transaction rolls back, the mapping disappears too, which is the
// property we want.
//
// See issue #147 for the design discussion and issue #144 for the
// importer that drives this package.
package migmap
