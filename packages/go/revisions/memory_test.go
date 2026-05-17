package revisions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

// newSeededMemoryStore returns a MemoryStore whose NowFunc steps
// forward by one second on every call. Tests use this to get
// deterministic ordering without sleeping.
func newSeededMemoryStore(t *testing.T) *MemoryStore {
	t.Helper()
	store := NewMemoryStore()
	var counter atomic.Int64
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	store.NowFunc = func() time.Time {
		// Each call gets a unique second so List ordering is stable.
		return base.Add(time.Duration(counter.Add(1)) * time.Second)
	}
	return store
}

func newPostID(t *testing.T) uuid.UUID {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid: %v", err)
	}
	return id
}

func TestMemoryStore_Save_FirstRevisionIsSnapshot(t *testing.T) {
	store := newSeededMemoryStore(t)
	post := newPostID(t)

	id, err := store.Save(context.Background(), Revision{
		PostID:        post,
		Kind:          Manual,
		ContentBlocks: json.RawMessage(`{"title":"hello"}`),
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	r, err := store.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !r.isSnapshot() {
		t.Errorf("first revision should be a snapshot, got delta=%s", r.Delta)
	}
	if r.DeltaFrom != uuid.Nil {
		t.Errorf("first revision DeltaFrom should be Nil, got %s", r.DeltaFrom)
	}
}

func TestMemoryStore_Save_RejectsInvalid(t *testing.T) {
	store := NewMemoryStore()

	t.Run("missing PostID", func(t *testing.T) {
		_, err := store.Save(context.Background(), Revision{
			Kind:          Manual,
			ContentBlocks: json.RawMessage(`{}`),
		})
		if !errors.Is(err, ErrInvalidRevision) {
			t.Errorf("expected ErrInvalidRevision, got %v", err)
		}
	})

	t.Run("missing Kind", func(t *testing.T) {
		_, err := store.Save(context.Background(), Revision{
			PostID:        newPostID(t),
			ContentBlocks: json.RawMessage(`{}`),
		})
		if !errors.Is(err, ErrInvalidRevision) {
			t.Errorf("expected ErrInvalidRevision, got %v", err)
		}
	})

	t.Run("missing ContentBlocks", func(t *testing.T) {
		_, err := store.Save(context.Background(), Revision{
			PostID: newPostID(t),
			Kind:   Manual,
		})
		if !errors.Is(err, ErrInvalidRevision) {
			t.Errorf("expected ErrInvalidRevision, got %v", err)
		}
	})

	t.Run("invalid JSON in ContentBlocks", func(t *testing.T) {
		_, err := store.Save(context.Background(), Revision{
			PostID:        newPostID(t),
			Kind:          Manual,
			ContentBlocks: json.RawMessage(`{not json`),
		})
		if !errors.Is(err, ErrInvalidRevision) {
			t.Errorf("expected ErrInvalidRevision, got %v", err)
		}
	})

	t.Run("unknown Kind", func(t *testing.T) {
		_, err := store.Save(context.Background(), Revision{
			PostID:        newPostID(t),
			Kind:          "draft",
			ContentBlocks: json.RawMessage(`{}`),
		})
		if !errors.Is(err, ErrInvalidRevision) {
			t.Errorf("expected ErrInvalidRevision, got %v", err)
		}
	})
}

func TestMemoryStore_Save_Get_RoundTrip(t *testing.T) {
	store := newSeededMemoryStore(t)
	post := newPostID(t)
	author := newPostID(t)

	content := json.RawMessage(`{"blocks":[{"id":"a","type":"p","text":"hi"}]}`)
	id, err := store.Save(context.Background(), Revision{
		PostID:            post,
		AuthorID:          author,
		Kind:              Manual,
		Title:             "Hello",
		Excerpt:           "Hi there.",
		ContentBlocks:     content,
		ContentBlocksHash: []byte{0xde, 0xad, 0xbe, 0xef},
		Comment:           "first save",
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	r, err := store.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if r.PostID != post {
		t.Errorf("PostID mismatch: got %s want %s", r.PostID, post)
	}
	if r.AuthorID != author {
		t.Errorf("AuthorID mismatch: got %s want %s", r.AuthorID, author)
	}
	if r.Kind != Manual {
		t.Errorf("Kind mismatch: got %s want %s", r.Kind, Manual)
	}
	if r.Title != "Hello" {
		t.Errorf("Title mismatch: got %q", r.Title)
	}
	if r.Excerpt != "Hi there." {
		t.Errorf("Excerpt mismatch: got %q", r.Excerpt)
	}
	if !bytes.Equal(r.ContentBlocksHash, []byte{0xde, 0xad, 0xbe, 0xef}) {
		t.Errorf("Hash mismatch: got %x", r.ContentBlocksHash)
	}
	if r.Comment != "first save" {
		t.Errorf("Comment mismatch: got %q", r.Comment)
	}
	// The first save is a snapshot — the persisted form is in Snapshot.
	if !bytes.Equal(canonicalize(t, r.Snapshot), canonicalize(t, content)) {
		t.Errorf("Snapshot mismatch:\n got=%s\nwant=%s", r.Snapshot, content)
	}
	// ContentBlocks is the input-only field — not persisted.
	if len(r.ContentBlocks) != 0 {
		t.Errorf("ContentBlocks should not be persisted, got %s", r.ContentBlocks)
	}
}

func TestMemoryStore_Get_NotFound(t *testing.T) {
	store := NewMemoryStore()
	_, err := store.Get(context.Background(), uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestMemoryStore_Save_SecondRevisionIsDelta(t *testing.T) {
	store := newSeededMemoryStore(t)
	post := newPostID(t)

	_, err := store.Save(context.Background(), Revision{
		PostID:        post,
		Kind:          Manual,
		ContentBlocks: json.RawMessage(`{"k":"v1"}`),
	})
	if err != nil {
		t.Fatalf("first Save: %v", err)
	}
	id2, err := store.Save(context.Background(), Revision{
		PostID:        post,
		Kind:          Manual,
		ContentBlocks: json.RawMessage(`{"k":"v2"}`),
	})
	if err != nil {
		t.Fatalf("second Save: %v", err)
	}
	r2, err := store.Get(context.Background(), id2)
	if err != nil {
		t.Fatalf("Get second: %v", err)
	}
	if !r2.isDelta() {
		t.Errorf("second revision should be a delta, got snapshot=%s", r2.Snapshot)
	}
	if r2.DeltaFrom == uuid.Nil {
		t.Error("second revision should have a non-nil DeltaFrom")
	}
}

func TestMemoryStore_Save_SnapshotEveryN(t *testing.T) {
	// The doc-01 §4.1 rule: every 20th revision is a snapshot,
	// regardless of how small the delta would be.
	store := newSeededMemoryStore(t)
	post := newPostID(t)

	for i := 0; i < 25; i++ {
		content := fmt.Sprintf(`{"counter":%d}`, i)
		_, err := store.Save(context.Background(), Revision{
			PostID:        post,
			Kind:          Manual,
			ContentBlocks: json.RawMessage(content),
		})
		if err != nil {
			t.Fatalf("Save %d: %v", i, err)
		}
	}

	// Pull the list (oldest-first by re-sorting) and count snapshots.
	revs, err := store.List(context.Background(), post, Filter{Limit: 100})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(revs) != 25 {
		t.Fatalf("len(revs): got %d want 25", len(revs))
	}

	// Find which positions are snapshots. The list is newest-first,
	// so position [24] is the first revision. With default N=20, the
	// snapshot-vs-delta decision triggers `since >= N` where `since`
	// counts revisions since the last snapshot (including the new
	// one). So with N=20, revisions 1 and 21 are snapshots — list
	// positions [24] and [4].
	snapshotPositions := make(map[int]bool)
	for i, r := range revs {
		if r.isSnapshot() {
			snapshotPositions[i] = true
		}
	}
	// Position 24 = first revision (always snapshot).
	if !snapshotPositions[24] {
		t.Errorf("first revision (list[24]) should be snapshot")
	}
	// Position 4 = 21st revision = forced snapshot (every Nth).
	if !snapshotPositions[4] {
		t.Errorf("21st revision (list[4]) should be snapshot, got delta")
	}
	// Exactly 2 snapshots in 25 saves with default N=20.
	if len(snapshotPositions) != 2 {
		t.Errorf("expected exactly 2 snapshots in 25 saves, got %d at %v",
			len(snapshotPositions), snapshotPositions)
	}
}

func TestMemoryStore_Save_ForceSnapshot(t *testing.T) {
	store := newSeededMemoryStore(t)
	post := newPostID(t)

	_, err := store.Save(context.Background(), Revision{
		PostID:        post,
		Kind:          Manual,
		ContentBlocks: json.RawMessage(`{"k":1}`),
	})
	if err != nil {
		t.Fatalf("first Save: %v", err)
	}
	id2, err := store.Save(context.Background(), Revision{
		PostID:        post,
		Kind:          Publish,
		ContentBlocks: json.RawMessage(`{"k":2}`),
	}, WithForceSnapshot())
	if err != nil {
		t.Fatalf("forced Save: %v", err)
	}
	r2, _ := store.Get(context.Background(), id2)
	if !r2.isSnapshot() {
		t.Error("WithForceSnapshot should yield a snapshot")
	}
}

func TestMemoryStore_Save_MaxSnapshotAge(t *testing.T) {
	// If MaxSnapshotAge has passed since the last snapshot, the next
	// Save is a snapshot regardless of count.
	store := NewMemoryStore()
	post := newPostID(t)

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var t1, t2 time.Time
	t1 = base
	t2 = base.Add(25 * time.Hour) // >24h later
	store.NowFunc = func() time.Time { return t1 }

	_, err := store.Save(context.Background(), Revision{
		PostID:        post,
		Kind:          Manual,
		ContentBlocks: json.RawMessage(`{"k":"v1"}`),
	})
	if err != nil {
		t.Fatalf("first Save: %v", err)
	}

	store.NowFunc = func() time.Time { return t2 }
	id2, err := store.Save(context.Background(), Revision{
		PostID:        post,
		Kind:          Manual,
		ContentBlocks: json.RawMessage(`{"k":"v2"}`),
	})
	if err != nil {
		t.Fatalf("second Save: %v", err)
	}
	r2, _ := store.Get(context.Background(), id2)
	if !r2.isSnapshot() {
		t.Errorf("revision >24h after last snapshot should be snapshot, got delta")
	}
}

func TestMemoryStore_List_FilterCombinations(t *testing.T) {
	store := newSeededMemoryStore(t)
	post := newPostID(t)
	otherPost := newPostID(t)
	authorA := newPostID(t)
	authorB := newPostID(t)

	// Seed: 3 manuals by A, 2 autosaves by B, 1 publish by A. Plus
	// one revision against a different post (must not surface).
	seed := []Revision{
		{PostID: post, AuthorID: authorA, Kind: Manual, ContentBlocks: json.RawMessage(`{"i":1}`)},
		{PostID: post, AuthorID: authorB, Kind: Autosave, ContentBlocks: json.RawMessage(`{"i":2}`)},
		{PostID: post, AuthorID: authorA, Kind: Manual, ContentBlocks: json.RawMessage(`{"i":3}`)},
		{PostID: post, AuthorID: authorB, Kind: Autosave, ContentBlocks: json.RawMessage(`{"i":4}`)},
		{PostID: post, AuthorID: authorA, Kind: Manual, ContentBlocks: json.RawMessage(`{"i":5}`)},
		{PostID: post, AuthorID: authorA, Kind: Publish, ContentBlocks: json.RawMessage(`{"i":6}`)},
		{PostID: otherPost, AuthorID: authorA, Kind: Manual, ContentBlocks: json.RawMessage(`{"i":99}`)},
	}
	for i, r := range seed {
		if _, err := store.Save(context.Background(), r); err != nil {
			t.Fatalf("seed[%d]: %v", i, err)
		}
	}

	t.Run("all for post", func(t *testing.T) {
		out, err := store.List(context.Background(), post, Filter{})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(out) != 6 {
			t.Errorf("got %d revisions, want 6", len(out))
		}
	})

	t.Run("filter by kind=manual", func(t *testing.T) {
		out, _ := store.List(context.Background(), post, Filter{Kind: Manual})
		if len(out) != 3 {
			t.Errorf("got %d manuals, want 3", len(out))
		}
		for _, r := range out {
			if r.Kind != Manual {
				t.Errorf("non-manual in filter result: %s", r.Kind)
			}
		}
	})

	t.Run("filter by author=B", func(t *testing.T) {
		out, _ := store.List(context.Background(), post, Filter{AuthorID: authorB})
		if len(out) != 2 {
			t.Errorf("got %d for B, want 2", len(out))
		}
	})

	t.Run("filter by kind=autosave and author=A", func(t *testing.T) {
		out, _ := store.List(context.Background(), post, Filter{Kind: Autosave, AuthorID: authorA})
		if len(out) != 0 {
			t.Errorf("expected no rows, got %d", len(out))
		}
	})

	t.Run("limit", func(t *testing.T) {
		out, _ := store.List(context.Background(), post, Filter{Limit: 2})
		if len(out) != 2 {
			t.Errorf("limited list: got %d want 2", len(out))
		}
		// Newest-first.
		if !out[0].CreatedAt.After(out[1].CreatedAt) {
			t.Error("list not newest-first")
		}
	})

	t.Run("time range", func(t *testing.T) {
		// Pull all, then filter by middle of the range.
		all, _ := store.List(context.Background(), post, Filter{})
		if len(all) < 4 {
			t.Fatalf("seed too small")
		}
		mid := all[len(all)/2].CreatedAt
		out, _ := store.List(context.Background(), post, Filter{Start: mid})
		for _, r := range out {
			if r.CreatedAt.Before(mid) {
				t.Errorf("revision before Start in result: %v", r.CreatedAt)
			}
		}
	})

	t.Run("unknown post returns empty", func(t *testing.T) {
		out, err := store.List(context.Background(), uuid.New(), Filter{})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(out) != 0 {
			t.Errorf("expected empty, got %d", len(out))
		}
	})
}

func TestMemoryStore_Latest(t *testing.T) {
	store := newSeededMemoryStore(t)
	post := newPostID(t)
	author := newPostID(t)

	for i, k := range []RevisionKind{Manual, Autosave, Manual, Publish, Manual} {
		_, err := store.Save(context.Background(), Revision{
			PostID:        post,
			AuthorID:      author,
			Kind:          k,
			ContentBlocks: json.RawMessage(fmt.Sprintf(`{"i":%d}`, i)),
		})
		if err != nil {
			t.Fatalf("seed[%d]: %v", i, err)
		}
	}

	t.Run("latest manual", func(t *testing.T) {
		r, err := store.Latest(context.Background(), post, Manual)
		if err != nil {
			t.Fatalf("Latest: %v", err)
		}
		if r.Kind != Manual {
			t.Errorf("kind: got %s want %s", r.Kind, Manual)
		}
	})

	t.Run("latest publish", func(t *testing.T) {
		r, err := store.Latest(context.Background(), post, Publish)
		if err != nil {
			t.Fatalf("Latest: %v", err)
		}
		if r.Kind != Publish {
			t.Errorf("kind: got %s want %s", r.Kind, Publish)
		}
	})

	t.Run("no autosave for unknown post", func(t *testing.T) {
		_, err := store.Latest(context.Background(), uuid.New(), Autosave)
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("expected ErrNotFound, got %v", err)
		}
	})

	t.Run("invalid kind", func(t *testing.T) {
		_, err := store.Latest(context.Background(), post, RevisionKind("draft"))
		if !errors.Is(err, ErrInvalidRevision) {
			t.Errorf("expected ErrInvalidRevision, got %v", err)
		}
	})
}

func TestMemoryStore_Materialize_Snapshot(t *testing.T) {
	store := newSeededMemoryStore(t)
	post := newPostID(t)
	content := json.RawMessage(`{"k":"snapshot"}`)
	id, err := store.Save(context.Background(), Revision{
		PostID:        post,
		Kind:          Manual,
		ContentBlocks: content,
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := store.Materialize(context.Background(), id)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if !bytes.Equal(canonicalize(t, got), canonicalize(t, content)) {
		t.Errorf("materialized snapshot mismatch:\n got=%s\nwant=%s", got, content)
	}
}

func TestMemoryStore_Materialize_DeltaChain(t *testing.T) {
	store := newSeededMemoryStore(t)
	post := newPostID(t)

	want := json.RawMessage(`{"k":"v5","extra":42}`)
	steps := []json.RawMessage{
		json.RawMessage(`{"k":"v1"}`),
		json.RawMessage(`{"k":"v2"}`),
		json.RawMessage(`{"k":"v3"}`),
		json.RawMessage(`{"k":"v4"}`),
		want,
	}
	var lastID uuid.UUID
	for i, content := range steps {
		id, err := store.Save(context.Background(), Revision{
			PostID:        post,
			Kind:          Manual,
			ContentBlocks: content,
		})
		if err != nil {
			t.Fatalf("Save[%d]: %v", i, err)
		}
		lastID = id
	}

	r, _ := store.Get(context.Background(), lastID)
	if !r.isDelta() {
		t.Fatal("expected last revision to be a delta — test setup is wrong")
	}

	got, err := store.Materialize(context.Background(), lastID)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if !bytes.Equal(canonicalize(t, got), canonicalize(t, want)) {
		t.Errorf("materialized delta-chain mismatch:\n got=%s\nwant=%s", got, want)
	}
}

func TestMemoryStore_Materialize_NotFound(t *testing.T) {
	store := NewMemoryStore()
	_, err := store.Materialize(context.Background(), uuid.New())
	if !errors.Is(err, ErrCorruptChain) {
		// Materialize hides ErrNotFound behind ErrCorruptChain for
		// mid-chain lookups; the top-level lookup also returns
		// ErrCorruptChain because the visited-set/depth check
		// doesn't distinguish.
		t.Errorf("expected ErrCorruptChain, got %v", err)
	}
}

func TestMemoryStore_Prune_KeepsLatestManuals(t *testing.T) {
	store := newSeededMemoryStore(t)
	post := newPostID(t)
	author := newPostID(t)

	// Seed 5 manuals + 3 autosaves. Default policy keeps 20 manuals
	// (so none drop) and 5 autosaves per author (none drop).
	for i := 0; i < 5; i++ {
		_, _ = store.Save(context.Background(), Revision{
			PostID:        post,
			AuthorID:      author,
			Kind:          Manual,
			ContentBlocks: json.RawMessage(fmt.Sprintf(`{"m":%d}`, i)),
		})
	}
	for i := 0; i < 3; i++ {
		_, _ = store.Save(context.Background(), Revision{
			PostID:        post,
			AuthorID:      author,
			Kind:          Autosave,
			ContentBlocks: json.RawMessage(fmt.Sprintf(`{"a":%d}`, i)),
		})
	}

	deleted, err := store.Prune(context.Background(), post, DefaultRetentionPolicy())
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if deleted != 0 {
		t.Errorf("default policy deleted %d, expected 0 for small seed", deleted)
	}
}

func TestMemoryStore_Prune_ManualCap(t *testing.T) {
	store := newSeededMemoryStore(t)
	post := newPostID(t)
	author := newPostID(t)

	for i := 0; i < 30; i++ {
		_, _ = store.Save(context.Background(), Revision{
			PostID:        post,
			AuthorID:      author,
			Kind:          Manual,
			ContentBlocks: json.RawMessage(fmt.Sprintf(`{"m":%d}`, i)),
		})
	}

	// Disable the MinKeepAll exemption so the count cap actually
	// fires on the seed (otherwise the 7d-rule keeps everything).
	policy := DefaultRetentionPolicy()
	policy.MinKeepAll = 0
	policy.MaxManual = 5

	deleted, err := store.Prune(context.Background(), post, policy)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	// Reachability sweep keeps snapshots referenced by un-pruned
	// deltas. With 30 manuals at N=20, snapshots land at positions
	// 1 and 21 (1-indexed); the latest 5 manuals are positions
	// 26-30, all of which delta back through 21. Snapshot at 21
	// must survive; snapshot at 1 may be dropped.
	if deleted == 0 {
		t.Error("expected some rows pruned, got 0")
	}
	remaining, _ := store.List(context.Background(), post, Filter{Limit: 100})
	// Must include the latest 5 manuals.
	manuals := 0
	for _, r := range remaining {
		if r.Kind == Manual {
			manuals++
		}
	}
	if manuals < 5 {
		t.Errorf("expected at least 5 manuals after prune, got %d", manuals)
	}
}

func TestMemoryStore_Prune_MaxAgeAutosave(t *testing.T) {
	store := NewMemoryStore()
	post := newPostID(t)
	author := newPostID(t)

	// Fixed clock; we'll move it for the "now" of Prune.
	var now time.Time
	store.NowFunc = func() time.Time { return now }

	now = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Save initial manual (force snapshot — first revision).
	if _, err := store.Save(context.Background(), Revision{
		PostID:        post,
		AuthorID:      author,
		Kind:          Manual,
		ContentBlocks: json.RawMessage(`{"k":"v"}`),
	}); err != nil {
		t.Fatalf("seed manual: %v", err)
	}

	// Save an old autosave.
	now = time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)
	if _, err := store.Save(context.Background(), Revision{
		PostID:        post,
		AuthorID:      author,
		Kind:          Autosave,
		ContentBlocks: json.RawMessage(`{"k":"old"}`),
	}); err != nil {
		t.Fatalf("seed autosave: %v", err)
	}

	// Save a recent autosave.
	now = time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)
	if _, err := store.Save(context.Background(), Revision{
		PostID:        post,
		AuthorID:      author,
		Kind:          Autosave,
		ContentBlocks: json.RawMessage(`{"k":"new"}`),
	}); err != nil {
		t.Fatalf("seed recent autosave: %v", err)
	}

	// Prune "now" is 2026-01-03 12:00 — the first autosave is ~35h
	// old, second is ~12h. MaxAgeAutosave=24h drops the first.
	// MinKeepAll set short so the time check actually fires.
	now = time.Date(2026, 1, 3, 12, 0, 0, 0, time.UTC)
	policy := RetentionPolicy{
		MaxAutosavesPerAuthor: 10,
		MaxManual:             10,
		MaxAgeAutosave:        24 * time.Hour,
		MinKeepAll:            time.Hour, // shorter than 12h
	}
	deleted, err := store.Prune(context.Background(), post, policy)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if deleted != 1 {
		t.Errorf("expected 1 deleted, got %d", deleted)
	}
}

func TestMemoryStore_Prune_PublishDefaultNeverDeleted(t *testing.T) {
	store := newSeededMemoryStore(t)
	post := newPostID(t)
	author := newPostID(t)

	// Seed 5 publishes. Default policy MaxPublish=0 means "no cap".
	for i := 0; i < 5; i++ {
		if _, err := store.Save(context.Background(), Revision{
			PostID:        post,
			AuthorID:      author,
			Kind:          Publish,
			ContentBlocks: json.RawMessage(fmt.Sprintf(`{"v":%d}`, i)),
		}); err != nil {
			t.Fatalf("seed[%d]: %v", i, err)
		}
	}

	policy := DefaultRetentionPolicy()
	policy.MinKeepAll = 0 // so the count caps actually fire
	deleted, err := store.Prune(context.Background(), post, policy)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if deleted != 0 {
		t.Errorf("default policy should not touch publishes, deleted=%d", deleted)
	}
}

func TestMemoryStore_Prune_ReachabilityKeepsAnchorSnapshot(t *testing.T) {
	// Save 3 revisions: snapshot, delta, delta. Then prune with a
	// retention policy that would normally drop the oldest. The
	// snapshot must survive because the un-pruned deltas reference it.
	store := newSeededMemoryStore(t)
	post := newPostID(t)
	author := newPostID(t)

	contents := []json.RawMessage{
		json.RawMessage(`{"k":"v1"}`),
		json.RawMessage(`{"k":"v2"}`),
		json.RawMessage(`{"k":"v3"}`),
	}
	var ids []uuid.UUID
	for _, c := range contents {
		id, err := store.Save(context.Background(), Revision{
			PostID:        post,
			AuthorID:      author,
			Kind:          Manual,
			ContentBlocks: c,
		})
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
		ids = append(ids, id)
	}

	policy := RetentionPolicy{
		MaxManual:  2,
		MinKeepAll: 0, // disable exemption
	}
	deleted, err := store.Prune(context.Background(), post, policy)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	// Marked for drop: id[0] (oldest, exceeds MaxManual=2). But
	// id[0] is the anchor snapshot for the remaining deltas, so
	// reachability sweep saves it.
	if deleted != 0 {
		t.Errorf("expected 0 deleted (reachability protects anchor), got %d", deleted)
	}

	// Verify the chain still materializes.
	got, err := store.Materialize(context.Background(), ids[2])
	if err != nil {
		t.Fatalf("Materialize after prune: %v", err)
	}
	if !bytes.Equal(canonicalize(t, got), canonicalize(t, contents[2])) {
		t.Errorf("materialized after prune:\n got=%s\nwant=%s", got, contents[2])
	}
}

func TestMemoryStore_Prune_AutosavePerAuthorCap(t *testing.T) {
	// Force-snapshot every save so autosaves don't form delta chains
	// that the reachability sweep would protect. This isolates the
	// per-author count cap behavior from the delta-chain protection
	// tested elsewhere.
	store := newSeededMemoryStore(t)
	post := newPostID(t)
	authorA := newPostID(t)
	authorB := newPostID(t)

	// 4 autosaves by A, 2 autosaves by B — all as snapshots so the
	// reachability sweep has nothing to rescue.
	for i := 0; i < 4; i++ {
		_, _ = store.Save(context.Background(), Revision{
			PostID:        post,
			AuthorID:      authorA,
			Kind:          Autosave,
			ContentBlocks: json.RawMessage(fmt.Sprintf(`{"A":%d}`, i)),
		}, WithForceSnapshot())
	}
	for i := 0; i < 2; i++ {
		_, _ = store.Save(context.Background(), Revision{
			PostID:        post,
			AuthorID:      authorB,
			Kind:          Autosave,
			ContentBlocks: json.RawMessage(fmt.Sprintf(`{"B":%d}`, i)),
		}, WithForceSnapshot())
	}

	policy := RetentionPolicy{
		MaxAutosavesPerAuthor: 2,
		MinKeepAll:            0,
	}
	deleted, _ := store.Prune(context.Background(), post, policy)
	// Should mark 2 autosaves of A for drop (4 - 2 = 2). B has only
	// 2, none to drop. All saves are snapshots, so nothing rescues
	// the dropped autosaves.
	if deleted != 2 {
		t.Errorf("expected 2 deleted, got %d", deleted)
	}
	// Verify the latest 2 of A and both of B remain.
	remaining, _ := store.List(context.Background(), post, Filter{Limit: 100})
	byAuthor := map[uuid.UUID]int{}
	for _, r := range remaining {
		byAuthor[r.AuthorID]++
	}
	if byAuthor[authorA] != 2 {
		t.Errorf("authorA should have 2 left, got %d", byAuthor[authorA])
	}
	if byAuthor[authorB] != 2 {
		t.Errorf("authorB should have 2 left, got %d", byAuthor[authorB])
	}
}

func TestMemoryStore_Concurrency_SaveAndList(t *testing.T) {
	// Smoke test: concurrent Save / List / Get from multiple
	// goroutines should not race or deadlock.
	store := NewMemoryStore()
	post := newPostID(t)
	author := newPostID(t)

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				_, err := store.Save(context.Background(), Revision{
					PostID:        post,
					AuthorID:      author,
					Kind:          Autosave,
					ContentBlocks: json.RawMessage(fmt.Sprintf(`{"w":%d,"i":%d}`, id, i)),
				})
				if err != nil {
					t.Errorf("Save: %v", err)
					return
				}
				_, _ = store.List(context.Background(), post, Filter{Limit: 10})
			}
		}(w)
	}
	wg.Wait()

	out, _ := store.List(context.Background(), post, Filter{Limit: 500})
	if len(out) != 200 {
		t.Errorf("expected 200 revisions, got %d", len(out))
	}
}
