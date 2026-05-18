// Package importer orchestrates a WordPress WXR import into GoNext.
//
// The package wires three pieces that already exist independently:
//
//   - packages/go/migrate/wxr — the streaming WXR parser. Hands us
//     typed records (authors, categories, tags, posts) one at a time
//     so a multi-hundred-MB export never has to be resident in
//     memory.
//   - packages/go/migrate/html2blocks — the HTML→Block-Tree
//     converter. For every Post the importer feeds its content:encoded
//     bytes into this converter and stores the resulting JSON block
//     tree in posts.content_blocks.
//   - The database tables defined by migrations/000002 (users),
//     /000004 (posts), /000005 (taxonomies + terms), /000006
//     (comments).
//
// The orchestrator owns three concerns the constituent packages
// deliberately don't: per-record upsert SQL, conflict-policy
// enforcement, and report accumulation. Everything else is delegated.
//
// Typical usage:
//
//	imp := importer.New(pool, importer.Options{
//	    BatchSize:  100,
//	    OnConflict: importer.ConflictSkip,
//	})
//	report, err := imp.Run(ctx, file)
//	if err != nil {
//	    return fmt.Errorf("wp import: %w", err)
//	}
//	fmt.Printf("imported: %+v\n", report)
//
// The Importer is single-pass and not safe for concurrent invocation.
// A *pgxpool.Pool is required because the import opens short-lived
// transactions of size Options.BatchSize per commit, releasing locks
// between batches so a long import doesn't pin a single connection.
//
// See issue #144 and the P5 migration plan.
package importer
