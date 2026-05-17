package revisions

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRevisionKind_Valid(t *testing.T) {
	cases := map[RevisionKind]bool{
		Autosave:                true,
		Manual:                  true,
		Publish:                 true,
		RevisionKind(""):        false,
		RevisionKind("draft"):   false,
		RevisionKind("garbage"): false,
	}
	for k, want := range cases {
		if got := k.Valid(); got != want {
			t.Errorf("%q.Valid() = %v, want %v", k, got, want)
		}
	}
}

func TestRetentionPolicy_DefaultsAndNormalize(t *testing.T) {
	def := DefaultRetentionPolicy()
	if def.MaxAutosavesPerAuthor != 5 {
		t.Errorf("MaxAutosavesPerAuthor default: got %d", def.MaxAutosavesPerAuthor)
	}
	if def.MaxManual != 20 {
		t.Errorf("MaxManual default: got %d", def.MaxManual)
	}
	if def.MaxAgeAutosave != 24*time.Hour {
		t.Errorf("MaxAgeAutosave default: got %v", def.MaxAgeAutosave)
	}

	// Negative values clamp to zero.
	neg := RetentionPolicy{
		MaxAutosavesPerAuthor: -1,
		MaxManual:             -1,
		MaxPublish:            -1,
		MaxAgeAutosave:        -1,
		MinKeepAll:            -1,
	}.normalize()
	if neg.MaxAutosavesPerAuthor != 0 || neg.MaxManual != 0 || neg.MaxPublish != 0 ||
		neg.MaxAgeAutosave != 0 || neg.MinKeepAll != 0 {
		t.Errorf("normalize did not clamp negatives: %+v", neg)
	}
}

func TestSaveOptions_ResolveAndDefaults(t *testing.T) {
	// Zero options: snapshotEveryN defaults to 20, maxSnapshotAgeSec to 24h.
	o := resolveSaveOptions(nil)
	if o.snapshotEveryN() != 20 {
		t.Errorf("default snapshotEveryN: got %d", o.snapshotEveryN())
	}
	if o.maxSnapshotAgeSec() != int64(24*60*60) {
		t.Errorf("default maxSnapshotAgeSec: got %d", o.maxSnapshotAgeSec())
	}

	// With explicit options, the overrides win.
	o = resolveSaveOptions([]SaveOption{
		WithSnapshotEveryN(50),
		WithMaxSnapshotAge(60),
		WithForceSnapshot(),
	})
	if o.snapshotEveryN() != 50 {
		t.Errorf("override snapshotEveryN: got %d", o.snapshotEveryN())
	}
	if o.maxSnapshotAgeSec() != 60 {
		t.Errorf("override maxSnapshotAgeSec: got %d", o.maxSnapshotAgeSec())
	}
	if !o.ForceSnapshot {
		t.Error("ForceSnapshot override did not stick")
	}
}

func TestValidateForSave(t *testing.T) {
	good := Revision{
		PostID:        uuid.New(),
		Kind:          Manual,
		ContentBlocks: json.RawMessage(`{}`),
	}
	if err := validateForSave(good); err != nil {
		t.Errorf("good revision rejected: %v", err)
	}

	bad := good
	bad.PostID = uuid.Nil
	if err := validateForSave(bad); !errors.Is(err, ErrInvalidRevision) {
		t.Errorf("expected ErrInvalidRevision for nil PostID, got %v", err)
	}
}

func TestRevision_IsSnapshotIsDelta(t *testing.T) {
	s := Revision{Snapshot: json.RawMessage(`{}`)}
	if !s.isSnapshot() {
		t.Error("snapshot should report isSnapshot")
	}
	if s.isDelta() {
		t.Error("snapshot should not report isDelta")
	}
	d := Revision{Delta: json.RawMessage(`[]`)}
	if !d.isDelta() {
		t.Error("delta should report isDelta")
	}
	if d.isSnapshot() {
		t.Error("delta should not report isSnapshot")
	}
}

// TestMemoryStore_Save_DefaultIDGeneration verifies the default
// uuid.NewV7 path runs when NewIDFunc isn't overridden. The seeded
// helper sets NowFunc but leaves NewIDFunc alone, so this path is
// already exercised by the other tests — we just assert ID != Nil.
func TestMemoryStore_Save_DefaultIDIsAssigned(t *testing.T) {
	store := NewMemoryStore()
	id, err := store.Save(context.Background(), Revision{
		PostID:        uuid.New(),
		Kind:          Manual,
		ContentBlocks: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if id == uuid.Nil {
		t.Error("Save returned uuid.Nil")
	}
}

// TestMemoryStore_Save_HonorsExplicitIDAndCreatedAt verifies the
// store does not overwrite caller-supplied ID and CreatedAt.
func TestMemoryStore_Save_HonorsExplicitIDAndCreatedAt(t *testing.T) {
	store := NewMemoryStore()
	wantID := uuid.New()
	wantTime := time.Date(2025, 12, 31, 23, 59, 59, 0, time.UTC)
	id, err := store.Save(context.Background(), Revision{
		ID:            wantID,
		PostID:        uuid.New(),
		Kind:          Manual,
		ContentBlocks: json.RawMessage(`{}`),
		CreatedAt:     wantTime,
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if id != wantID {
		t.Errorf("ID: got %s want %s", id, wantID)
	}
	r, _ := store.Get(context.Background(), id)
	if !r.CreatedAt.Equal(wantTime) {
		t.Errorf("CreatedAt: got %v want %v", r.CreatedAt, wantTime)
	}
}

// TestMemoryStore_NewIDFunc_Injection ensures the test-injection seam
// is wired correctly.
func TestMemoryStore_NewIDFunc_Injection(t *testing.T) {
	store := NewMemoryStore()
	custom := uuid.New()
	store.NewIDFunc = func() uuid.UUID { return custom }
	id, err := store.Save(context.Background(), Revision{
		PostID:        uuid.New(),
		Kind:          Manual,
		ContentBlocks: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if id != custom {
		t.Errorf("NewIDFunc not honored: got %s want %s", id, custom)
	}
}

// TestMemoryStore_Materialize_DeepChain stresses the chain walk with
// many delta hops. With default N=20 we get one snapshot followed by
// 19 deltas; the 20th hop is itself a snapshot. We materialize the
// last delta (hop 19) and confirm we got the latest content.
func TestMemoryStore_Materialize_DeepChain(t *testing.T) {
	store := newSeededMemoryStore(t)
	post := newPostID(t)

	var lastID uuid.UUID
	for i := 0; i < 19; i++ {
		content := json.RawMessage(`{"i":` + itoaTest(i) + `}`)
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
	got, err := store.Materialize(context.Background(), lastID)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if string(got) != `{"i":18}` {
		t.Errorf("got %s want {\"i\":18}", got)
	}
}

func itoaTest(i int) string {
	if i == 0 {
		return "0"
	}
	var d []byte
	for i > 0 {
		d = append([]byte{byte('0' + i%10)}, d...)
		i /= 10
	}
	return string(d)
}

// TestNewPostgresStore_BuildsStruct verifies the constructor path is
// callable. We pass a nil pool — calling methods would panic, but
// the constructor itself doesn't touch the pool.
func TestNewPostgresStore_BuildsStruct(t *testing.T) {
	s := NewPostgresStore(nil)
	if s == nil {
		t.Fatal("NewPostgresStore returned nil")
	}
	if s.db == nil {
		t.Error("db field should reference the (nil) pool, not be unset")
	}
}

// TestPostgresStore_DefaultNowFunc verifies the package-default time
// source is used when NowFunc isn't set.
func TestPostgresStore_DefaultNowFunc(t *testing.T) {
	s := NewPostgresStoreWithQuerier(nil)
	before := time.Now().Add(-time.Second)
	got := s.now()
	after := time.Now().Add(time.Second)
	if got.Before(before) || got.After(after) {
		t.Errorf("default now: got %v, outside [%v,%v]", got, before, after)
	}
}

// TestMemoryStore_DefaultNowFunc mirrors the postgres test.
func TestMemoryStore_DefaultNowFunc(t *testing.T) {
	store := NewMemoryStore()
	before := time.Now().Add(-time.Second)
	got := store.now()
	after := time.Now().Add(time.Second)
	if got.Before(before) || got.After(after) {
		t.Errorf("default now: got %v, outside [%v,%v]", got, before, after)
	}
}
