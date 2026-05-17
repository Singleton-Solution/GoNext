package revisions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// memoryDefaultLimit caps List results when Filter.Limit is zero.
const memoryDefaultLimit = 100

// MemoryStore is an in-process Store backed by a map. Designed for
// unit tests and short-lived development — no persistence, no
// eviction other than explicit Prune calls.
//
// Concurrency: safe for concurrent Save / Get / List / Latest /
// Materialize / Prune. Guarded by a single sync.RWMutex; contention
// is acceptable at test scale.
//
// Time injection: NowFunc lets tests pin CreatedAt without sleeping.
// If nil, time.Now is used.
//
// NewIDFunc lets tests inject deterministic UUIDs. If nil,
// uuid.Must(uuid.NewV7()) is used so generated IDs sort
// chronologically and the persisted order survives a sort by id.
type MemoryStore struct {
	mu        sync.RWMutex
	revisions map[uuid.UUID]Revision
	// byPost holds, per post, the IDs in insertion order. The index
	// is rebuilt on every Save; with the default snapshot-every-20
	// strategy the per-post list is short enough that this is cheap.
	byPost map[uuid.UUID][]uuid.UUID

	NowFunc   func() time.Time
	NewIDFunc func() uuid.UUID
}

// NewMemoryStore returns an empty in-memory store ready for use.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		revisions: make(map[uuid.UUID]Revision),
		byPost:    make(map[uuid.UUID][]uuid.UUID),
	}
}

func (s *MemoryStore) now() time.Time {
	if s.NowFunc != nil {
		return s.NowFunc().UTC()
	}
	return time.Now().UTC()
}

func (s *MemoryStore) newID() uuid.UUID {
	if s.NewIDFunc != nil {
		return s.NewIDFunc()
	}
	// UUIDv7 sorts chronologically and is the production default
	// (gen_uuid_v7() on the SQL column). Falling back to v4 is fine
	// for tests but loses sort stability.
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.New()
	}
	return id
}

// Save appends r to the store, choosing snapshot vs delta storage
// automatically (see SaveOptions / docs/01-core-cms.md §4.1).
//
// Returns ErrInvalidRevision if r fails structural validation.
func (s *MemoryStore) Save(_ context.Context, r Revision, opts ...SaveOption) (uuid.UUID, error) {
	if err := validateForSave(r); err != nil {
		return uuid.Nil, err
	}
	options := resolveSaveOptions(opts)

	s.mu.Lock()
	defer s.mu.Unlock()

	// Assign id / created_at defaults.
	if r.ID == uuid.Nil {
		r.ID = s.newID()
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = s.now()
	} else {
		r.CreatedAt = r.CreatedAt.UTC()
	}

	// Decide snapshot vs delta. The decision needs to read prior
	// revisions for this post; we already hold the write lock, so
	// the chain walk is consistent with the upcoming insert.
	priorIDs := append([]uuid.UUID(nil), s.byPost[r.PostID]...)
	persisted, err := s.persistedFromInput(r, priorIDs, options)
	if err != nil {
		return uuid.Nil, err
	}

	// Clear the input-only ContentBlocks slot — the persisted form
	// is what readers see, not the raw input.
	persisted.ContentBlocks = nil

	s.revisions[persisted.ID] = persisted
	s.byPost[r.PostID] = append(s.byPost[r.PostID], persisted.ID)
	return persisted.ID, nil
}

// persistedFromInput is the snapshot-vs-delta decision and the
// matching field population. Caller must hold s.mu (read or write —
// the function does not mutate s).
func (s *MemoryStore) persistedFromInput(r Revision, priorIDs []uuid.UUID, opts SaveOptions) (Revision, error) {
	out := r

	// Force-snapshot path: no decision to make.
	if opts.ForceSnapshot {
		out.Snapshot = cloneJSON(r.ContentBlocks)
		out.Delta = nil
		out.DeltaFrom = uuid.Nil
		return out, nil
	}

	// First revision of a post is always a snapshot — there's no
	// parent to delta against.
	if len(priorIDs) == 0 {
		out.Snapshot = cloneJSON(r.ContentBlocks)
		out.Delta = nil
		out.DeltaFrom = uuid.Nil
		return out, nil
	}

	// Find the most recent snapshot in this post's chain. If none
	// exists (corrupt state — the first revision is always a
	// snapshot), fall back to snapshotting this one too.
	lastSnapshotIdx := -1
	for i := len(priorIDs) - 1; i >= 0; i-- {
		if s.revisions[priorIDs[i]].isSnapshot() {
			lastSnapshotIdx = i
			break
		}
	}
	if lastSnapshotIdx < 0 {
		out.Snapshot = cloneJSON(r.ContentBlocks)
		return out, nil
	}

	// Snapshot-every-N: count revisions since the last snapshot
	// (inclusive of the snapshot itself, so the 20th revision is a
	// snapshot — matching doc 01 §4.1).
	since := len(priorIDs) - lastSnapshotIdx
	if since >= opts.snapshotEveryN() {
		out.Snapshot = cloneJSON(r.ContentBlocks)
		return out, nil
	}

	// Snapshot-age cap. A negative maxAge disables the cap (caller
	// who really wants it off can pass -1).
	maxAgeSec := opts.maxSnapshotAgeSec()
	if maxAgeSec > 0 {
		lastSnapshot := s.revisions[priorIDs[lastSnapshotIdx]]
		age := r.CreatedAt.Sub(lastSnapshot.CreatedAt)
		if age >= time.Duration(maxAgeSec)*time.Second {
			out.Snapshot = cloneJSON(r.ContentBlocks)
			return out, nil
		}
	}

	// Delta path: diff against the most recent prior revision (which
	// may itself be a delta — Materialize walks the chain).
	parentID := priorIDs[len(priorIDs)-1]
	parentMaterialized, err := s.materializeLocked(parentID, 0)
	if err != nil {
		// Shouldn't happen under a non-corrupt store; fall back to
		// snapshotting so the new row is at least usable.
		out.Snapshot = cloneJSON(r.ContentBlocks)
		out.Delta = nil
		out.DeltaFrom = uuid.Nil
		return out, nil
	}
	delta, err := ComputeDelta(parentMaterialized, r.ContentBlocks)
	if err != nil {
		return Revision{}, fmt.Errorf("revisions: compute delta: %w", err)
	}
	out.Snapshot = nil
	out.Delta = delta
	out.DeltaFrom = parentID
	return out, nil
}

// Get returns the revision with the given id.
func (s *MemoryStore) Get(_ context.Context, id uuid.UUID) (Revision, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.revisions[id]
	if !ok {
		return Revision{}, ErrNotFound
	}
	return cloneRevision(r), nil
}

// List returns revisions for postID matching f, most recent first.
func (s *MemoryStore) List(_ context.Context, postID uuid.UUID, f Filter) ([]Revision, error) {
	s.mu.RLock()
	ids := append([]uuid.UUID(nil), s.byPost[postID]...)
	out := make([]Revision, 0, len(ids))
	for _, id := range ids {
		r := s.revisions[id]
		if !matchFilter(r, f) {
			continue
		}
		out = append(out, cloneRevision(r))
	}
	s.mu.RUnlock()

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			// Lexicographic ID tiebreak keeps the order stable when
			// tests pin NowFunc and emit multiple revisions at the
			// same instant.
			return out[i].ID.String() > out[j].ID.String()
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})

	limit := f.Limit
	if limit <= 0 {
		limit = memoryDefaultLimit
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Latest returns the most recent revision of the given kind for postID.
func (s *MemoryStore) Latest(_ context.Context, postID uuid.UUID, kind RevisionKind) (Revision, error) {
	if !kind.Valid() {
		return Revision{}, errors.Join(ErrInvalidRevision, fmt.Errorf("unknown Kind: %q", string(kind)))
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	ids := s.byPost[postID]
	var best *Revision
	for i := len(ids) - 1; i >= 0; i-- {
		r := s.revisions[ids[i]]
		if r.Kind != kind {
			continue
		}
		if best == nil || r.CreatedAt.After(best.CreatedAt) {
			rc := r
			best = &rc
		}
	}
	if best == nil {
		return Revision{}, ErrNotFound
	}
	return cloneRevision(*best), nil
}

// Materialize walks the delta chain back to the nearest snapshot and
// returns the reconstructed JSON. Returns ErrCorruptChain on cycles
// or missing parents.
func (s *MemoryStore) Materialize(_ context.Context, id uuid.UUID) (json.RawMessage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.materializeLocked(id, 0)
}

// materializeLocked is the locked-by-caller variant of Materialize.
// depth is the recursion counter; it doubles as a visited-set proxy
// because the chain is a single parent pointer, so any cycle inflates
// depth past maxChainDepth.
func (s *MemoryStore) materializeLocked(id uuid.UUID, depth int) (json.RawMessage, error) {
	if depth >= maxChainDepth {
		return nil, ErrCorruptChain
	}
	r, ok := s.revisions[id]
	if !ok {
		return nil, ErrCorruptChain
	}
	if r.isSnapshot() {
		// Return a copy so a downstream mutator can't corrupt the
		// stored snapshot. ContentBlocks isn't kept; snapshot is.
		return cloneJSON(r.Snapshot), nil
	}
	// Delta path: materialize parent, then apply.
	if r.DeltaFrom == uuid.Nil {
		return nil, ErrCorruptChain
	}
	parent, err := s.materializeLocked(r.DeltaFrom, depth+1)
	if err != nil {
		return nil, err
	}
	return ApplyDelta(parent, r.Delta)
}

// Prune applies retention to one post. Returns the number of rows
// deleted. Publish revisions are never deleted under the default
// policy (MaxPublish == 0).
func (s *MemoryStore) Prune(_ context.Context, postID uuid.UUID, retention RetentionPolicy) (int, error) {
	policy := retention.normalize()
	now := s.now()

	s.mu.Lock()
	defer s.mu.Unlock()

	ids := s.byPost[postID]
	if len(ids) == 0 {
		return 0, nil
	}

	// First pass: classify, computing a candidate set of IDs to drop.
	// We respect MinKeepAll (recent revisions are exempt) and never
	// drop a snapshot that's still being referenced by an un-dropped
	// delta — that would break the chain walk.
	toDelete := make(map[uuid.UUID]bool)

	// Group autosaves by author so we can apply MaxAutosavesPerAuthor
	// per-author rather than per-post.
	autosavesByAuthor := make(map[uuid.UUID][]uuid.UUID)
	var manuals, publishes []uuid.UUID

	for _, id := range ids {
		r := s.revisions[id]
		if policy.MinKeepAll > 0 && now.Sub(r.CreatedAt) < policy.MinKeepAll {
			continue // exempt from count caps; still subject to MaxAgeAutosave below
		}
		switch r.Kind {
		case Autosave:
			autosavesByAuthor[r.AuthorID] = append(autosavesByAuthor[r.AuthorID], id)
		case Manual:
			manuals = append(manuals, id)
		case Publish:
			publishes = append(publishes, id)
		}
	}

	// MaxAutosavesPerAuthor: keep latest N per author.
	if policy.MaxAutosavesPerAuthor > 0 {
		for author, list := range autosavesByAuthor {
			s.classifyDrop(list, policy.MaxAutosavesPerAuthor, toDelete)
			_ = author
		}
	}
	// MaxManual / MaxPublish.
	if policy.MaxManual > 0 {
		s.classifyDrop(manuals, policy.MaxManual, toDelete)
	}
	if policy.MaxPublish > 0 {
		s.classifyDrop(publishes, policy.MaxPublish, toDelete)
	}

	// MaxAgeAutosave: separate sweep that ignores MinKeepAll's
	// exemption only for autosaves OLDER than MaxAgeAutosave. The
	// rationale is in the godoc: autosave history is throwaway, the
	// MinKeepAll exemption is mainly for manuals.
	if policy.MaxAgeAutosave > 0 {
		for _, id := range ids {
			r := s.revisions[id]
			if r.Kind != Autosave {
				continue
			}
			if now.Sub(r.CreatedAt) >= policy.MaxAgeAutosave {
				toDelete[id] = true
			}
		}
	}

	// Reachability sweep: any delta we're keeping must have its
	// parent (transitively) preserved. Walk every kept delta's
	// DeltaFrom chain and un-mark referenced snapshots / deltas.
	for _, id := range ids {
		if toDelete[id] {
			continue
		}
		cur := s.revisions[id]
		for cur.isDelta() && cur.DeltaFrom != uuid.Nil {
			if toDelete[cur.DeltaFrom] {
				delete(toDelete, cur.DeltaFrom)
			}
			parent, ok := s.revisions[cur.DeltaFrom]
			if !ok {
				break
			}
			cur = parent
		}
	}

	// Apply deletions.
	deleted := 0
	if len(toDelete) > 0 {
		newList := ids[:0:len(ids)]
		for _, id := range ids {
			if toDelete[id] {
				delete(s.revisions, id)
				deleted++
				continue
			}
			newList = append(newList, id)
		}
		s.byPost[postID] = newList
	}
	return deleted, nil
}

// classifyDrop marks the older entries of ids for deletion, keeping
// the most recent keep count. ids must be in insertion order
// (oldest-first); the byPost index satisfies this invariant.
func (s *MemoryStore) classifyDrop(ids []uuid.UUID, keep int, out map[uuid.UUID]bool) {
	if len(ids) <= keep {
		return
	}
	drop := len(ids) - keep
	for i := 0; i < drop; i++ {
		out[ids[i]] = true
	}
}

// matchFilter reports whether r satisfies every non-zero field of f.
// PostID is not consulted here — List has already keyed on it.
func matchFilter(r Revision, f Filter) bool {
	if !f.Start.IsZero() && r.CreatedAt.Before(f.Start) {
		return false
	}
	if !f.End.IsZero() && r.CreatedAt.After(f.End) {
		return false
	}
	if f.AuthorID != uuid.Nil && r.AuthorID != f.AuthorID {
		return false
	}
	if f.Kind != "" && r.Kind != f.Kind {
		return false
	}
	return true
}

// cloneJSON returns a defensive copy so a caller mutating the returned
// slice can't corrupt the stored bytes.
func cloneJSON(b json.RawMessage) json.RawMessage {
	if b == nil {
		return nil
	}
	out := make(json.RawMessage, len(b))
	copy(out, b)
	return out
}

// cloneRevision returns a deep-enough copy of r that mutations on the
// returned value's byte-slice fields don't affect the store. Other
// fields (UUIDs, strings, times) are value-copies already.
func cloneRevision(r Revision) Revision {
	out := r
	out.ContentBlocks = cloneJSON(r.ContentBlocks)
	out.ContentBlocksHash = append([]byte(nil), r.ContentBlocksHash...)
	out.Delta = cloneJSON(r.Delta)
	out.Snapshot = cloneJSON(r.Snapshot)
	return out
}

// Ensure MemoryStore satisfies Store at compile time.
var _ Store = (*MemoryStore)(nil)
