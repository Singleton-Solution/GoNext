// Package revisions is the snapshot-and-delta storage for the GoNext
// block editor. Every save (autosave, manual, or publish) appends one
// row to post_revisions; rows are stored either as a full JSON snapshot
// of the editable fields or as an RFC 6902 JSON Patch against an earlier
// snapshot. The package owns the read path (Get / List / Latest /
// Materialize), the write path (Save with auto snapshot-vs-delta
// decision), and the retention sweep (Prune).
//
// See docs/01-core-cms.md §4 for the product semantics (autosave UPSERT,
// retention table, restoration rules) and §10.6 for the SQL DDL. The
// matching block-editor flow lives in docs/04-block-editor.md §9.
//
// # Snapshot vs delta
//
// A revision is either a snapshot (full json.RawMessage in
// Revision.Snapshot) OR a delta (RFC 6902 JSON Patch in Revision.Delta,
// pointing at a parent revision via Revision.DeltaFrom). The CHECK
// constraint on the SQL table enforces (snapshot IS NOT NULL) XOR
// (delta IS NOT NULL); the Save path enforces the same invariant
// before writing.
//
// Save chooses automatically: it stores a full snapshot every
// SnapshotEveryN revisions (default 20) OR after MaxSnapshotAge
// (default 24h) since the most recent snapshot — whichever comes
// first. Between snapshots it stores deltas against the most recent
// prior revision (snapshot or delta, doesn't matter — Materialize
// walks the chain back to the nearest snapshot regardless).
//
// This is a 5–10x space win over WordPress's full-copy revisions for
// typical posts and a 50x win for block-heavy pages where a single
// edit touches 1 KB of a 200 KB tree.
//
// # Materialization
//
// Materialize(ctx, id) reconstructs the full JSON for a revision by
// walking DeltaFrom back to the nearest snapshot, then applying the
// patches forward. It does not require any specific traversal direction
// in the caller — the chain walk is internal. Cycles are defended
// against with a depth cap; the package treats a cycle as a corrupt
// store and returns ErrCorruptChain.
//
// # Stores
//
// Two implementations:
//
//   - MemoryStore is an in-process Store backed by a map. Designed for
//     unit tests and short-lived development; no persistence, no
//     eviction (other than explicit Prune calls).
//
//   - PostgresStore writes parameterized SQL against post_revisions.
//     The CREATE TABLE migration is owned by a downstream issue (see
//     docs/01-core-cms.md §10.6); this store locks the column contract
//     so the Go side speaks SQL against the documented columns as soon
//     as the migration lands.
//
// # Expected SQL (Postgres)
//
// The PostgresStore writes to a table created by a future migration
// matching the DDL in docs/01-core-cms.md §10.6:
//
//	CREATE TYPE revision_kind AS ENUM ('autosave', 'manual', 'publish');
//
//	CREATE TABLE post_revisions (
//	    id              UUID PRIMARY KEY DEFAULT gen_uuid_v7(),
//	    post_id         UUID NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
//	    author_id       UUID REFERENCES users(id) ON DELETE SET NULL,
//	    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
//	    kind            revision_kind NOT NULL,
//	    snapshot        JSONB,
//	    delta_from      UUID REFERENCES post_revisions(id),
//	    delta           JSONB,
//	    title           TEXT,
//	    excerpt         TEXT,
//	    content_blocks_hash BYTEA,
//	    comment         TEXT,
//	    CHECK ((snapshot IS NOT NULL) <> (delta IS NOT NULL))
//	);
//
//	CREATE INDEX post_revisions_post_created_idx
//	  ON post_revisions(post_id, created_at DESC);
//	CREATE INDEX post_revisions_kind_idx
//	  ON post_revisions(post_id, kind, created_at DESC);
//
// The PostgresStore does NOT create or migrate the table. Calling Save
// on a host where the table does not yet exist will fail with the
// usual pgx UndefinedTable error.
//
// # Retention
//
// Prune applies a RetentionPolicy to one post's revisions:
//
//   - MaxAutosavesPerAuthor: only the latest N autosaves per (post, author).
//     Default 5; doc 01 §4.3 calls for 1 in production, but we leave
//     headroom for tools that need short autosave history.
//   - MaxManual: only the latest N manual revisions per post. Default 20.
//   - MaxAgeAutosave: autosaves older than this are discarded regardless
//     of count. Default 24h.
//
// Publish revisions are never pruned by the default policy (set
// MaxPublish > 0 if you want a cap). Snapshots that are still
// referenced by un-pruned deltas are retained even if older — the
// sweep does a reachability check, not a naive age delete.
//
// # Typical wiring
//
//	pool, _ := pgxpool.New(ctx, dsn)
//	store := revisions.NewPostgresStore(pool)
//
//	// On manual save in a post handler:
//	rev := revisions.Revision{
//	    PostID:        postID,
//	    AuthorID:      currentUserID,
//	    Kind:          revisions.Manual,
//	    Title:         post.Title,
//	    Excerpt:       post.Excerpt,
//	    ContentBlocks: post.ContentBlocks,
//	}
//	id, err := store.Save(ctx, rev)
//
//	// Editor's revision-list panel:
//	revs, _ := store.List(ctx, postID, revisions.Filter{Limit: 50})
//
//	// User clicks a delta-stored revision in the diff view:
//	blocks, _ := store.Materialize(ctx, rev.ID)
//
// # Concurrency
//
// All Store methods are safe to call from multiple goroutines.
// PostgresStore relies on the underlying pgxpool for concurrent access;
// MemoryStore uses a sync.RWMutex internally.
package revisions
