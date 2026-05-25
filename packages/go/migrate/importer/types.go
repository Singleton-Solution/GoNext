package importer

import "time"

// ConflictPolicy decides how the importer reacts when a destination
// row already exists for a record's natural key.
//
// The natural keys are:
//   - users: email (citext UNIQUE) or handle (citext UNIQUE)
//   - terms: (taxonomy, slug, parent_id)
//   - posts: (post_type, slug) for top-level rows or
//     (post_type, parent_id, slug) for hierarchical rows
//
// Every conflict policy is applied per-record, never globally — a
// single import may skip one duplicate, update another, and complete
// successfully. Fail is the exception: it aborts the import on the
// first collision (current batch rolls back, subsequent batches are
// not attempted). Callers select the policy via Options.OnConflict.
type ConflictPolicy uint8

const (
	// ConflictSkip leaves the existing row untouched and increments
	// the per-kind counter on the Report so operators can see how
	// many rows were already present. This is the default — re-
	// importing the same WXR is then idempotent.
	ConflictSkip ConflictPolicy = iota

	// ConflictUpdate overwrites the existing row with the values
	// from the WXR record. Useful for "the canonical source is over
	// there; re-sync everything" workflows.
	ConflictUpdate

	// ConflictFail aborts the import on the first conflict. The
	// partial state inside the current batch is rolled back; rows
	// committed by earlier batches stay. Pick this when you expect
	// a clean target database and want a hard failure if it isn't.
	ConflictFail
)

// String returns the canonical CLI form ("skip", "update", "fail")
// so flag parsing and log lines stay symmetric.
func (c ConflictPolicy) String() string {
	switch c {
	case ConflictSkip:
		return "skip"
	case ConflictUpdate:
		return "update"
	case ConflictFail:
		return "fail"
	default:
		return "unknown"
	}
}

// ParseConflictPolicy turns a CLI string into a ConflictPolicy. The
// empty string maps to ConflictSkip so callers can pass the flag
// untouched without a separate "was it set?" branch. Any unknown
// value returns a non-nil error.
func ParseConflictPolicy(s string) (ConflictPolicy, error) {
	switch s {
	case "", "skip":
		return ConflictSkip, nil
	case "update":
		return ConflictUpdate, nil
	case "fail":
		return ConflictFail, nil
	default:
		return ConflictSkip, &ImportError{
			Stage:  "options",
			Reason: "unknown conflict policy: " + s,
		}
	}
}

// Options configures a single Importer.Run invocation.
//
// Zero-value Options is safe to use — see field comments for the
// per-field defaults. The Importer applies defaults at Run time, not
// at construction, so callers can mutate fields freely on the value
// they passed to New.
type Options struct {
	// Dryrun, when true, walks the WXR but writes no rows. The
	// Report still accumulates as if the writes had happened so
	// callers can preview the outcome ("how many posts would I get?").
	Dryrun bool

	// OnConflict selects the conflict-handling policy. See the
	// ConflictPolicy doc comment for semantics. Default: ConflictSkip.
	OnConflict ConflictPolicy

	// BatchSize is the number of post records committed per
	// transaction. Larger batches reduce per-commit overhead but
	// pin a connection and grow the rollback window on error.
	// Authors, categories, and tags are committed in their own
	// preamble transactions regardless of BatchSize. Default: 100.
	BatchSize int

	// SkipComments turns off comment ingestion. Useful when an
	// operator wants the posts but not the (often-spammy) comment
	// thread. Default: false.
	SkipComments bool

	// PlaceholderPasswordHash is the argon2id PHC string written to
	// every migrated user's user_passwords row. Migrated users carry
	// the meta key `must_reset_password=true` so the login flow
	// forces a reset. The default is a fixed, well-known invalid
	// argon2id string so verification will always fail until the
	// user resets — but callers can override it for tests or to
	// pin a specific hash format.
	PlaceholderPasswordHash string

	// MediaMigrator, when non-nil, is the per-asset ingestion
	// orchestrator the importer hands every WXR attachment URL.
	// Wired up by the CLI or the migration wizard UI from the
	// operator's selection of "copy" vs "proxy" mode (#187, #234).
	//
	// Nil disables media migration entirely — the importer still
	// records attachment posts but does not download or proxy the
	// underlying bytes; the post bodies retain their original
	// source URLs and the imported site falls back to hot-linking.
	MediaMigrator *MediaMigrator

	// MediaUploaderID is the GoNext users.id the MediaMigrator
	// attributes every migrated media row to. Required when
	// MediaMigrator is non-nil; empty triggers an error at Run-
	// validation time. Typically the operator who triggered the
	// migration.
	MediaUploaderID string
}

// resolved returns a copy of Options with defaults applied. Internal.
func (o Options) resolved() Options {
	if o.BatchSize <= 0 {
		o.BatchSize = 100
	}
	if o.PlaceholderPasswordHash == "" {
		// A deliberately invalid argon2id PHC string. argon2id
		// verifiers reject any plaintext against this value because
		// the salt is empty and the params are nonsense — exactly
		// what we want for a "must reset before you can log in" row.
		o.PlaceholderPasswordHash = "$argon2id$v=19$m=1,t=1,p=1$bWlncmF0ZWQ$bWlncmF0ZWQ"
	}
	return o
}

// Report summarises the outcome of a single Run. The counters are
// per-record-kind tallies of rows the importer either created,
// updated, or (when OnConflict == ConflictSkip) found already
// present and left alone.
//
// Errors contains every record-scoped failure that did not abort the
// import — a single malformed post yields one ImportError; the rest
// of the import continues. Use len(Errors) > 0 as the "did anything
// go wrong?" signal. The first fatal error (the one that stopped the
// import) is returned by Run as its second return value, not
// recorded here.
type Report struct {
	// Authors is the number of *Author records the importer
	// processed. Equals the number of rows the import attempted to
	// upsert into users — not the number of new rows: an existing
	// user counted under ConflictSkip still contributes here.
	Authors int

	// Categories is the number of *Category records processed.
	Categories int

	// Tags is the number of *Tag records processed.
	Tags int

	// Posts is the number of *Post records processed (every value
	// of PostType — post, page, attachment, revision, etc.).
	Posts int

	// Comments is the total number of <wp:comment> children
	// emitted across every post.
	Comments int

	// Attachments is the number of posts whose PostType ==
	// "attachment". They contribute to Posts as well; this is a
	// secondary breakdown for the CLI report.
	Attachments int

	// MediaCopied is the number of attachment URLs the MediaMigrator
	// successfully downloaded and stored locally. Always zero when
	// the migration ran in proxy mode (or when no MediaMigrator was
	// wired). Issue #187.
	MediaCopied int

	// MediaProxied is the number of attachment URLs the MediaMigrator
	// registered as proxied rows. Always zero in copy mode.
	MediaProxied int

	// MediaSkipped is the number of attachment URLs the
	// MediaMigrator did not ingest because a row already existed
	// (idempotency hit on FindBySourceURL). Both modes can produce
	// skips on a re-run.
	MediaSkipped int

	// MediaBytesFetched is the total number of source bytes the
	// MediaMigrator downloaded in copy mode. Always zero in proxy
	// mode. Useful for the CLI report to surface "we transferred
	// X MB during the migration".
	MediaBytesFetched int64

	// Errors collects per-record failures. Never nil-checked by
	// callers — an empty slice means "no errors" and an unset slice
	// means the same thing. Re-allocated to nil if the user trims
	// it externally.
	Errors []ImportError

	// Took is the wall-clock duration of the Run. Zero on Dryrun
	// for callers that print the duration unconditionally.
	Took time.Duration
}

// HasErrors reports whether the Report carries any per-record
// errors. Equivalent to len(r.Errors) > 0 but reads more naturally
// at call sites that branch on success.
func (r *Report) HasErrors() bool { return len(r.Errors) > 0 }
