package revisions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// ErrInvalidRevision is returned by Store.Save when the revision is
// structurally invalid — empty PostID, unknown Kind, or empty
// ContentBlocks. Distinct from a transport error so callers can
// decide whether to retry.
var ErrInvalidRevision = errors.New("revisions: invalid revision")

// ErrNotFound is returned by Get, Latest, and Materialize when the
// requested revision (or, for Latest, the requested (post, kind)
// pair) does not exist.
var ErrNotFound = errors.New("revisions: not found")

// ErrCorruptChain is returned by Materialize when the delta chain
// walk hits a cycle, references a missing revision, or exceeds
// maxChainDepth. All three indicate the store is corrupt — there
// is no in-band recovery, the caller should escalate.
var ErrCorruptChain = errors.New("revisions: corrupt delta chain")

// maxChainDepth caps how many delta hops Materialize will walk before
// giving up. With the default snapshot-every-20 strategy the real
// chain length is bounded at 20; 1024 leaves enough headroom for an
// operator who has cranked SnapshotEveryN up while still rejecting
// pathological cycles in finite time.
const maxChainDepth = 1024

// SaveOptions tunes the snapshot-vs-delta decision on a per-call
// basis. Most callers use the zero value (defaults from doc 01 §4.1).
type SaveOptions struct {
	// SnapshotEveryN forces a full snapshot every Nth revision per
	// post (counting all kinds together). Default 20. Set to 1 to
	// always snapshot (delta storage disabled). Set to 0 to use the
	// default.
	SnapshotEveryN int

	// MaxSnapshotAge forces a full snapshot if no snapshot has been
	// written for this post in the given duration. Default 24h. Set
	// to 0 to use the default; set to a negative value to disable
	// the age cap.
	MaxSnapshotAge int64 // seconds — int64 to keep SaveOptions trivially comparable

	// ForceSnapshot, when true, bypasses the decision and writes a
	// full snapshot regardless of count or age. Used by the post
	// layer when it knows a snapshot is wanted (e.g. on publish, or
	// on the first revision of a new post).
	ForceSnapshot bool
}

// defaultSnapshotEveryN matches docs/01-core-cms.md §4.1: "We force a
// full snapshot every 20 revisions or every 24h, whichever comes first."
const defaultSnapshotEveryN = 20

// defaultMaxSnapshotAgeSec is 24h in seconds.
const defaultMaxSnapshotAgeSec = int64(24 * 60 * 60)

func (o SaveOptions) snapshotEveryN() int {
	if o.SnapshotEveryN <= 0 {
		return defaultSnapshotEveryN
	}
	return o.SnapshotEveryN
}

func (o SaveOptions) maxSnapshotAgeSec() int64 {
	if o.MaxSnapshotAge == 0 {
		return defaultMaxSnapshotAgeSec
	}
	return o.MaxSnapshotAge
}

// Store persists block-editor revisions.
//
// Save MUST be safe to call from many goroutines. Implementations
// decide automatically whether to store r as a snapshot or a delta
// (see SaveOptions); callers should leave Snapshot / Delta / DeltaFrom
// zero on the input and read the assigned ID off the return value.
//
// Get returns ErrNotFound for unknown IDs.
//
// List returns revisions for one post, most recent first
// (created_at DESC, id DESC tiebreak), capped at Filter.Limit (or
// the store default if zero). Unsupported Filter fields are ignored
// rather than failing the request.
//
// Latest returns the most recent revision of the given kind for the
// post. Returns ErrNotFound if no revision of that kind exists.
//
// Materialize reconstructs the full editable JSON for a revision.
// For a snapshot revision it returns Snapshot directly; for a delta
// revision it walks DeltaFrom back to the nearest snapshot, applies
// each patch in order, and returns the result. Returns ErrCorruptChain
// if the walk hits a cycle, a missing parent, or exceeds maxChainDepth.
//
// Prune applies retention to one post's revisions. Returns the count
// of rows deleted. The default policy never touches publish
// revisions; see RetentionPolicy for the knobs.
type Store interface {
	Save(ctx context.Context, r Revision, opts ...SaveOption) (uuid.UUID, error)
	Get(ctx context.Context, id uuid.UUID) (Revision, error)
	List(ctx context.Context, postID uuid.UUID, filter Filter) ([]Revision, error)
	Latest(ctx context.Context, postID uuid.UUID, kind RevisionKind) (Revision, error)
	Materialize(ctx context.Context, id uuid.UUID) (json.RawMessage, error)
	Prune(ctx context.Context, postID uuid.UUID, retention RetentionPolicy) (deleted int, err error)
}

// SaveOption is the functional-option carrier for Save. Implemented
// only by the package's With* helpers; the unexported method keeps
// callers from rolling their own.
type SaveOption func(*SaveOptions)

// WithSnapshotEveryN overrides RetentionPolicy.SnapshotEveryN for one Save call.
func WithSnapshotEveryN(n int) SaveOption {
	return func(o *SaveOptions) { o.SnapshotEveryN = n }
}

// WithMaxSnapshotAge overrides MaxSnapshotAge (in seconds) for one Save call.
func WithMaxSnapshotAge(seconds int64) SaveOption {
	return func(o *SaveOptions) { o.MaxSnapshotAge = seconds }
}

// WithForceSnapshot makes Save store a full snapshot regardless of
// count or age. Used by publish handlers and by the first save of a
// new post (no prior revision to delta against).
func WithForceSnapshot() SaveOption {
	return func(o *SaveOptions) { o.ForceSnapshot = true }
}

// resolveSaveOptions folds the options into a final SaveOptions.
func resolveSaveOptions(opts []SaveOption) SaveOptions {
	var out SaveOptions
	for _, o := range opts {
		o(&out)
	}
	return out
}

// validateForSave checks the cheap structural constraints every
// store applies before persisting. Returns ErrInvalidRevision wrapped
// with a description so callers see exactly what's wrong.
func validateForSave(r Revision) error {
	if r.PostID == uuid.Nil {
		return errors.Join(ErrInvalidRevision, errors.New("PostID is required"))
	}
	if !r.Kind.Valid() {
		return errors.Join(ErrInvalidRevision, fmt.Errorf("unknown Kind: %q", string(r.Kind)))
	}
	if len(r.ContentBlocks) == 0 {
		return errors.Join(ErrInvalidRevision, errors.New("ContentBlocks is required"))
	}
	// Reject invalid JSON early — otherwise the snapshot store
	// would silently round-trip a malformed payload and corrupt
	// future delta chains rooted at this revision.
	if !json.Valid(r.ContentBlocks) {
		return errors.Join(ErrInvalidRevision, errors.New("ContentBlocks is not valid JSON"))
	}
	return nil
}
