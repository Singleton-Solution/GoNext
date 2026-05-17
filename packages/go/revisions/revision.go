package revisions

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// RevisionKind classifies a revision. The three values map to the
// revision_kind enum on the SQL side (see docs/01-core-cms.md §10.6).
//
// Autosave revisions are written by the editor's debounced 10s timer
// (doc 04 §9). Each (post, author) pair holds at most one active
// autosave; older autosaves with the same key are pruned on the next
// Save or Prune.
//
// Manual revisions are written when the author clicks Save (doc 01
// §4.2) and on the "Restored from revision X" path (doc 01 §4.4).
//
// Publish revisions are written when status flips to published.
// They're treated as load-bearing artifacts — pruning the latest
// publish revision of a post would erase the audit trail of what the
// public saw, so the default RetentionPolicy never touches them.
type RevisionKind string

const (
	Autosave RevisionKind = "autosave"
	Manual   RevisionKind = "manual"
	Publish  RevisionKind = "publish"
)

// Valid reports whether k is one of the defined kinds. Used by stores
// to reject malformed input before it lands on disk (also caught by
// the SQL enum, but we'd rather fail fast in Go).
func (k RevisionKind) Valid() bool {
	switch k {
	case Autosave, Manual, Publish:
		return true
	default:
		return false
	}
}

// Revision is one row in the post_revisions table.
//
// Exactly one of Snapshot or Delta must be non-nil. The store and the
// SQL CHECK constraint both enforce this. If Delta is non-nil,
// DeltaFrom must point to the parent revision the patch applies
// against.
//
// ContentBlocks is the editable JSON at the moment Save was called.
// The store does NOT persist ContentBlocks directly — it's the input
// to the snapshot-vs-delta decision. After Save returns, the
// persisted form is in Snapshot or Delta. Callers reading a revision
// back via Get receive only the persisted form; use Materialize to
// reconstruct the full content from a delta-stored revision.
type Revision struct {
	// ID is the row identifier. Empty at Save time; the store assigns
	// a UUIDv7 (matching the gen_uuid_v7() default on the SQL column).
	ID uuid.UUID

	// PostID is the post this revision belongs to. Required at Save.
	PostID uuid.UUID

	// AuthorID is the user who created the revision. May be zero
	// (uuid.Nil) for system-triggered revisions; the column is
	// NULL-able on the SQL side via ON DELETE SET NULL.
	AuthorID uuid.UUID

	// Kind is autosave / manual / publish. Required at Save.
	Kind RevisionKind

	// CreatedAt is the moment the revision was created. If zero at
	// Save time, the store sets it to time.Now().UTC().
	CreatedAt time.Time

	// Title is denormalized from posts.title so the revisions-list UI
	// doesn't need a JOIN to render the row label.
	Title string

	// Excerpt is denormalized for the same reason — small, cheap to
	// store, saves a JSONB walk in the list UI.
	Excerpt string

	// ContentBlocks is the input to Save: the full editable JSON at
	// save time. On a Get/List read, ContentBlocks is left zero —
	// the persisted form lives in Snapshot or Delta. Use Materialize
	// to walk the chain.
	ContentBlocks json.RawMessage

	// ContentBlocksHash is the BLAKE2b/SHA-256-style content hash of
	// ContentBlocks at save time. Stored so callers can detect "no
	// material change since the last save" without re-parsing the
	// JSON. The package does not enforce a specific algorithm; pass
	// in whatever the post layer used.
	ContentBlocksHash []byte

	// DeltaFrom is the parent revision ID this revision's Delta is
	// based on. Non-zero iff Delta is non-nil. Always nil for
	// snapshot revisions.
	DeltaFrom uuid.UUID

	// Delta is the RFC 6902 JSON Patch from DeltaFrom's materialized
	// content to this revision's content. Nil for snapshot revisions.
	Delta json.RawMessage

	// Snapshot is the full editable JSON for this revision. Nil for
	// delta revisions.
	Snapshot json.RawMessage

	// Comment is a human note attached to the revision — e.g. the
	// "Restored from revision X" tag the restore flow writes (doc 01
	// §4.4) or an editor-supplied "renamed section" annotation.
	Comment string

	// IsPermanent pins a revision so the retention pruner
	// (packages/go/revisions/pruner.go, issue #169) never deletes it.
	// Operators flip this on legal-hold revisions, "first published"
	// milestones, or anything the editor lets a user mark as
	// permanent. Default false so the bulk of revisions remain
	// eligible for the normal retention sweep.
	IsPermanent bool
}

// isSnapshot reports whether r is a snapshot revision (vs a delta).
// Treated as an unexported invariant helper rather than a public API
// because callers should never need to ask — Materialize handles it.
func (r Revision) isSnapshot() bool {
	return len(r.Snapshot) > 0
}

// isDelta is the mirror of isSnapshot. Provided for readability at
// call sites where "is delta" reads more naturally than "!isSnapshot".
func (r Revision) isDelta() bool {
	return len(r.Delta) > 0
}

// Filter narrows a Store.List query for the editor's revision-browse
// UI (doc 04 §9). All fields are optional; zero values are wildcards.
type Filter struct {
	// Start, End bound CreatedAt inclusively. If both are zero, the
	// time range is unbounded.
	Start time.Time
	End   time.Time

	// AuthorID, when non-zero, restricts results to revisions written
	// by that author. The editor's "my revisions" tab uses this.
	AuthorID uuid.UUID

	// Kind, when non-empty, restricts results to that revision kind.
	Kind RevisionKind

	// Limit caps the result set. Zero means "store default" (100).
	// Implementations may impose a hard maximum.
	Limit int
}

// RetentionPolicy describes how Prune trims revisions for one post.
//
// The defaults are conservative — keep enough autosaves and manuals
// to handle "I closed the tab and want my work back" without leaving
// the table unbounded. Doc 01 §4.3 specifies tighter production
// defaults; tune via site setting at the call site.
type RetentionPolicy struct {
	// MaxAutosavesPerAuthor keeps only the latest N autosaves per
	// (post, author). Default 5. Set to 1 to match the doc-01 §4.3
	// production target.
	MaxAutosavesPerAuthor int

	// MaxManual keeps only the latest N manual revisions per post.
	// Default 20. Doc 01 §4.3 calls for 30 in production.
	MaxManual int

	// MaxPublish keeps only the latest N publish revisions per post.
	// Zero disables the cap (publish revisions are load-bearing
	// audit artifacts — the default never prunes them).
	MaxPublish int

	// MaxAgeAutosave discards autosaves older than this duration,
	// regardless of count. Default 24h.
	MaxAgeAutosave time.Duration

	// MinKeepAll keeps all revisions newer than this duration even if
	// they'd otherwise exceed the count caps. Doc 01 §4.3: "Keep all
	// from the last 7 days." Default 7 days.
	MinKeepAll time.Duration
}

// DefaultRetentionPolicy returns the package-default retention policy.
// Callers should treat this as a starting point and adjust per the
// site's configured retention setting.
func DefaultRetentionPolicy() RetentionPolicy {
	return RetentionPolicy{
		MaxAutosavesPerAuthor: 5,
		MaxManual:             20,
		MaxPublish:            0, // never prune publishes by default
		MaxAgeAutosave:        24 * time.Hour,
		MinKeepAll:            7 * 24 * time.Hour,
	}
}

// normalize fills in zero-valued fields with defaults. The store
// applies this before deciding what to delete so a partially-set
// RetentionPolicy doesn't silently drop revisions the caller didn't
// mean to lose.
func (p RetentionPolicy) normalize() RetentionPolicy {
	out := p
	if out.MaxAutosavesPerAuthor < 0 {
		out.MaxAutosavesPerAuthor = 0
	}
	if out.MaxManual < 0 {
		out.MaxManual = 0
	}
	if out.MaxPublish < 0 {
		out.MaxPublish = 0
	}
	if out.MaxAgeAutosave < 0 {
		out.MaxAgeAutosave = 0
	}
	if out.MinKeepAll < 0 {
		out.MinKeepAll = 0
	}
	return out
}
