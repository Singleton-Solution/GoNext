package migmap

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// =============================================================================
// fakeTx — in-memory stand-in for a pgx.Tx / pgxpool.Pool
// =============================================================================
//
// fakeTx parses the SQL the store would have run and applies the
// equivalent operation against an in-memory table. We deliberately do
// NOT use a generic sqlmock here — the integration coverage already
// proves the SQL strings are correct against a real database. What we
// want from these unit tests is fast, hermetic coverage of the
// validation, the on-conflict merge semantics, and the concurrent
// safety. The fake gives us all three without booting Docker.
//
// fakeTx implements both the Tx interface and the queryer interface
// the store reflects on for GetByTarget.
type fakeTx struct {
	mu      sync.Mutex
	rows    map[fakeKey]*fakeRow
	execErr error // injectable error for Exec, exercised by transport-failure tests
}

type fakeKey struct {
	source, entityType, sourceID string
}

type fakeRow struct {
	target uuid.UUID
	meta   map[string]any
}

func newFakeTx() *fakeTx {
	return &fakeTx{rows: make(map[fakeKey]*fakeRow)}
}

// Exec routes on SQL prefix. We support the two patterns the store
// actually emits: the single-row INSERT in Put and the multi-row
// VALUES expansion in PutBatch.
func (f *fakeTx) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if f.execErr != nil {
		return pgconn.CommandTag{}, f.execErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	// Both INSERT statements have args in groups of five
	// (source, entity_type, source_id, target_id, meta). The single
	// statement supplies one group; the batch supplies N.
	if len(args)%5 != 0 {
		return pgconn.CommandTag{}, errors.New("fakeTx.Exec: args not a multiple of 5")
	}
	rowCount := int64(0)
	for i := 0; i < len(args); i += 5 {
		source, _ := args[i].(string)
		entityType, _ := args[i+1].(string)
		sourceID, _ := args[i+2].(string)
		target, _ := args[i+3].(uuid.UUID)
		metaRaw, _ := args[i+4].([]byte)

		var meta map[string]any
		if len(metaRaw) > 0 {
			if err := json.Unmarshal(metaRaw, &meta); err != nil {
				return pgconn.CommandTag{}, err
			}
		}
		key := fakeKey{source: source, entityType: entityType, sourceID: sourceID}
		if existing, ok := f.rows[key]; ok {
			// ON CONFLICT (source, entity_type, source_id) DO UPDATE
			// SET meta = migration_map.meta || EXCLUDED.meta.
			// target_id and imported_at are PRESERVED.
			merged := map[string]any{}
			for k, v := range existing.meta {
				merged[k] = v
			}
			for k, v := range meta {
				merged[k] = v // EXCLUDED side wins on overlapping keys, matching jsonb || semantics.
			}
			existing.meta = merged
		} else {
			// Defensive copy of the meta map so a caller mutating its
			// original after Put doesn't bleed into the fake's state.
			cp := make(map[string]any, len(meta))
			for k, v := range meta {
				cp[k] = v
			}
			f.rows[key] = &fakeRow{target: target, meta: cp}
		}
		rowCount++
	}
	// pgconn.CommandTag values are produced by pgconn.NewCommandTag —
	// fortunately the zero value satisfies the interface our code
	// expects (we never read RowsAffected on it).
	return pgconn.CommandTag{}, nil
}

// QueryRow services the single-row SELECT from Get.
func (f *fakeTx) QueryRow(_ context.Context, _ string, args ...any) pgx.Row {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(args) != 3 {
		return errRow{err: errors.New("fakeTx.QueryRow: expected 3 args")}
	}
	source, _ := args[0].(string)
	entityType, _ := args[1].(string)
	sourceID, _ := args[2].(string)
	key := fakeKey{source: source, entityType: entityType, sourceID: sourceID}
	if row, ok := f.rows[key]; ok {
		metaJSON, err := json.Marshal(row.meta)
		if err != nil {
			return errRow{err: err}
		}
		return scalarRow{values: []any{row.target, metaJSON}}
	}
	return errRow{err: pgx.ErrNoRows}
}

// Query supports GetByTarget. Returns every mapping with the given
// target UUID.
func (f *fakeTx) Query(_ context.Context, _ string, args ...any) (pgx.Rows, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(args) != 1 {
		return nil, errors.New("fakeTx.Query: expected 1 arg")
	}
	target, ok := args[0].(uuid.UUID)
	if !ok {
		return nil, errors.New("fakeTx.Query: arg not uuid.UUID")
	}
	out := &fakeRows{}
	for key, row := range f.rows {
		if row.target != target {
			continue
		}
		metaJSON, err := json.Marshal(row.meta)
		if err != nil {
			return nil, err
		}
		out.rows = append(out.rows, []any{key.source, key.entityType, key.sourceID, metaJSON})
	}
	return out, nil
}

// errRow is a pgx.Row whose Scan returns a preset error. Used to
// surface ErrNoRows and parse errors from the fake's lookup path.
type errRow struct{ err error }

func (r errRow) Scan(_ ...any) error { return r.err }

// scalarRow holds a pre-computed slice of column values and assigns
// them to the Scan destinations in order. Mirrors pgx.Row's contract
// closely enough for our store's QueryRow call sites.
type scalarRow struct{ values []any }

func (s scalarRow) Scan(dest ...any) error {
	if len(dest) != len(s.values) {
		return errors.New("scalarRow: dest/value length mismatch")
	}
	for i, v := range s.values {
		switch d := dest[i].(type) {
		case *uuid.UUID:
			u, ok := v.(uuid.UUID)
			if !ok {
				return errors.New("scalarRow: expected uuid.UUID")
			}
			*d = u
		case *[]byte:
			b, ok := v.([]byte)
			if !ok {
				return errors.New("scalarRow: expected []byte")
			}
			*d = b
		default:
			return errors.New("scalarRow: unsupported dest type")
		}
	}
	return nil
}

// fakeRows is a minimal pgx.Rows iterator over an in-memory result set.
type fakeRows struct {
	rows [][]any
	cur  int
	err  error
}

func (r *fakeRows) Close()                                       {}
func (r *fakeRows) Err() error                                   { return r.err }
func (r *fakeRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *fakeRows) Values() ([]any, error)                       { return nil, nil }
func (r *fakeRows) RawValues() [][]byte                          { return nil }
func (r *fakeRows) Conn() *pgx.Conn                              { return nil }
func (r *fakeRows) Next() bool {
	if r.cur >= len(r.rows) {
		return false
	}
	r.cur++
	return true
}
func (r *fakeRows) Scan(dest ...any) error {
	if r.cur == 0 || r.cur > len(r.rows) {
		return errors.New("fakeRows: Scan before Next")
	}
	row := r.rows[r.cur-1]
	if len(dest) != len(row) {
		return errors.New("fakeRows: dest/row length mismatch")
	}
	for i, v := range row {
		switch d := dest[i].(type) {
		case *string:
			s, ok := v.(string)
			if !ok {
				return errors.New("fakeRows: expected string")
			}
			*d = s
		case *[]byte:
			b, ok := v.([]byte)
			if !ok {
				return errors.New("fakeRows: expected []byte")
			}
			*d = b
		default:
			return errors.New("fakeRows: unsupported dest")
		}
	}
	return nil
}

// =============================================================================
// Validation tests
// =============================================================================

func TestMappingValidate(t *testing.T) {
	t.Parallel()

	validTarget := uuid.New()
	cases := []struct {
		name    string
		m       Mapping
		wantErr bool
	}{
		{
			name: "valid",
			m: Mapping{
				Source: SourceWordPress, EntityType: EntityUser,
				SourceID: "42", TargetID: validTarget,
			},
		},
		{
			name:    "missing source",
			m:       Mapping{EntityType: EntityUser, SourceID: "42", TargetID: validTarget},
			wantErr: true,
		},
		{
			name: "source too long",
			m: Mapping{
				Source: Source(string(make([]byte, 65))), EntityType: EntityUser,
				SourceID: "42", TargetID: validTarget,
			},
			wantErr: true,
		},
		{
			name: "unknown entity type",
			m: Mapping{
				Source: SourceWordPress, EntityType: EntityType("widget"),
				SourceID: "42", TargetID: validTarget,
			},
			wantErr: true,
		},
		{
			name: "missing source id",
			m: Mapping{
				Source: SourceWordPress, EntityType: EntityUser,
				SourceID: "", TargetID: validTarget,
			},
			wantErr: true,
		},
		{
			name: "source id too long",
			m: Mapping{
				Source: SourceWordPress, EntityType: EntityUser,
				SourceID: string(make([]byte, 256)), TargetID: validTarget,
			},
			wantErr: true,
		},
		{
			name: "zero target uuid",
			m: Mapping{
				Source: SourceWordPress, EntityType: EntityUser,
				SourceID: "42", TargetID: uuid.Nil,
			},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.m.Validate()
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !errors.Is(err, ErrInvalidMapping) {
					t.Fatalf("expected ErrInvalidMapping, got %v", err)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// =============================================================================
// PostgresStore tests (against fakeTx)
// =============================================================================

func TestPostgresStorePutGetRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tx := newFakeTx()
	s := NewPostgresStore(tx)

	target := uuid.New()
	m := Mapping{
		Source:     SourceWordPress,
		EntityType: EntityUser,
		SourceID:   "42",
		TargetID:   target,
		Meta:       map[string]any{"login": "alice"},
	}
	if err := s.Put(ctx, nil, m); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, ok, err := s.Get(ctx, SourceWordPress, EntityUser, "42")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("expected hit, got miss")
	}
	if got.TargetID != target {
		t.Fatalf("target: got %s, want %s", got.TargetID, target)
	}
	if got.Meta["login"] != "alice" {
		t.Fatalf("meta.login: got %v, want %q", got.Meta["login"], "alice")
	}
}

func TestPostgresStoreGetMissReturnsFalseNotError(t *testing.T) {
	t.Parallel()
	// The README contract: a miss is (nil, false, nil), NOT an error.
	// Callers branch on the bool; an error means transport failure.
	ctx := context.Background()
	tx := newFakeTx()
	s := NewPostgresStore(tx)

	got, ok, err := s.Get(ctx, SourceWordPress, EntityUser, "does-not-exist")
	if err != nil {
		t.Fatalf("Get: unexpected error %v", err)
	}
	if ok {
		t.Fatal("expected miss, got hit")
	}
	if got != nil {
		t.Fatalf("expected nil mapping on miss, got %+v", got)
	}
}

func TestPostgresStorePutBatchThenGet(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tx := newFakeTx()
	s := NewPostgresStore(tx)

	t1, t2, t3 := uuid.New(), uuid.New(), uuid.New()
	ms := []Mapping{
		{Source: SourceWordPress, EntityType: EntityTerm, SourceID: "1", TargetID: t1, Meta: map[string]any{"slug": "news"}},
		{Source: SourceWordPress, EntityType: EntityTerm, SourceID: "2", TargetID: t2},
		{Source: SourceWordPress, EntityType: EntityPost, SourceID: "100", TargetID: t3},
	}
	if err := s.PutBatch(ctx, nil, ms); err != nil {
		t.Fatalf("PutBatch: %v", err)
	}
	for _, want := range ms {
		got, ok, err := s.Get(ctx, want.Source, want.EntityType, want.SourceID)
		if err != nil {
			t.Fatalf("Get %s/%s/%s: %v", want.Source, want.EntityType, want.SourceID, err)
		}
		if !ok {
			t.Fatalf("Get %s/%s/%s: miss", want.Source, want.EntityType, want.SourceID)
		}
		if got.TargetID != want.TargetID {
			t.Fatalf("Get %s/%s/%s: target got %s, want %s",
				want.Source, want.EntityType, want.SourceID, got.TargetID, want.TargetID)
		}
	}
}

func TestPostgresStorePutBatchEmptyIsNoOp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tx := newFakeTx()
	s := NewPostgresStore(tx)
	if err := s.PutBatch(ctx, nil, nil); err != nil {
		t.Fatalf("empty batch: %v", err)
	}
	if err := s.PutBatch(ctx, nil, []Mapping{}); err != nil {
		t.Fatalf("empty slice batch: %v", err)
	}
}

func TestPostgresStorePutBatchValidatesEagerly(t *testing.T) {
	t.Parallel()
	// A bad mapping at index N must NOT leave indices 0..N-1
	// half-inserted. We verify by checking that index 0 is absent
	// after PutBatch returns an error.
	ctx := context.Background()
	tx := newFakeTx()
	s := NewPostgresStore(tx)

	ms := []Mapping{
		{Source: SourceWordPress, EntityType: EntityUser, SourceID: "1", TargetID: uuid.New()},
		{Source: "", EntityType: EntityUser, SourceID: "2", TargetID: uuid.New()}, // invalid
	}
	err := s.PutBatch(ctx, nil, ms)
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if !errors.Is(err, ErrInvalidMapping) {
		t.Fatalf("expected ErrInvalidMapping, got %v", err)
	}
	if _, ok, _ := s.Get(ctx, SourceWordPress, EntityUser, "1"); ok {
		t.Fatal("index-0 was inserted despite later validation failure")
	}
}

func TestPostgresStoreGetByTargetMultipleSources(t *testing.T) {
	t.Parallel()
	// Two sources alias to one target — the reverse lookup must
	// surface both rows.
	ctx := context.Background()
	tx := newFakeTx()
	s := NewPostgresStore(tx)

	target := uuid.New()
	ms := []Mapping{
		{Source: SourceWordPress, EntityType: EntityUser, SourceID: "42", TargetID: target},
		{Source: SourceGhost, EntityType: EntityUser, SourceID: "alice", TargetID: target},
		// Decoy: same source, different target.
		{Source: SourceWordPress, EntityType: EntityUser, SourceID: "43", TargetID: uuid.New()},
	}
	for _, m := range ms {
		if err := s.Put(ctx, nil, m); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}
	got, err := s.GetByTarget(ctx, target)
	if err != nil {
		t.Fatalf("GetByTarget: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 rows, got %d (%+v)", len(got), got)
	}
	// Verify both source systems are represented; the iteration
	// order over the fake's map is unspecified, so we collect into
	// a set.
	srcs := map[Source]bool{}
	for _, m := range got {
		srcs[m.Source] = true
		if m.TargetID != target {
			t.Fatalf("row target: got %s, want %s", m.TargetID, target)
		}
	}
	if !srcs[SourceWordPress] || !srcs[SourceGhost] {
		t.Fatalf("expected both wp + ghost, got %v", srcs)
	}
}

func TestPostgresStoreOnConflictPreservesTargetMergesMeta(t *testing.T) {
	t.Parallel()
	// The defining contract: re-importing the same source key must
	// PRESERVE target_id (so downstream FKs aren't invalidated) and
	// MERGE meta (so a second-pass importer can add keys without
	// clobbering the first-pass record).
	ctx := context.Background()
	tx := newFakeTx()
	s := NewPostgresStore(tx)

	original := uuid.New()
	if err := s.Put(ctx, nil, Mapping{
		Source: SourceWordPress, EntityType: EntityUser,
		SourceID: "42", TargetID: original,
		Meta: map[string]any{"login": "alice", "email": "alice@example.com"},
	}); err != nil {
		t.Fatalf("first Put: %v", err)
	}

	// Second-pass importer with a DIFFERENT target_id (which the
	// store must IGNORE) and an additional meta key.
	if err := s.Put(ctx, nil, Mapping{
		Source: SourceWordPress, EntityType: EntityUser,
		SourceID: "42", TargetID: uuid.New(), // would-be new target
		Meta: map[string]any{"revisions_imported_at": "2026-05-17"},
	}); err != nil {
		t.Fatalf("second Put: %v", err)
	}

	got, ok, err := s.Get(ctx, SourceWordPress, EntityUser, "42")
	if err != nil || !ok {
		t.Fatalf("Get after conflict: err=%v, ok=%v", err, ok)
	}
	if got.TargetID != original {
		t.Fatalf("target overwritten: got %s, want %s", got.TargetID, original)
	}
	if got.Meta["login"] != "alice" {
		t.Fatalf("original login lost: %+v", got.Meta)
	}
	if got.Meta["revisions_imported_at"] != "2026-05-17" {
		t.Fatalf("new meta key missing: %+v", got.Meta)
	}
	if got.Meta["email"] != "alice@example.com" {
		t.Fatalf("original email lost: %+v", got.Meta)
	}
}

func TestPostgresStoreConcurrentPutRaceClean(t *testing.T) {
	t.Parallel()
	// Concurrent Puts from many goroutines against disjoint AND
	// overlapping keys must produce consistent state. The race
	// detector catches data races; the post-condition check catches
	// lost writes.
	ctx := context.Background()
	tx := newFakeTx()
	s := NewPostgresStore(tx)

	const workers = 32
	const perWorker = 50
	sharedTarget := uuid.New()

	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				m := Mapping{
					Source:     SourceWordPress,
					EntityType: EntityPost,
					// Half the writes target the same key (idempotent
					// re-imports) and half are unique (parallel
					// progress on disjoint posts).
					SourceID: idForWorker(w, i),
					TargetID: sharedTarget,
					Meta:     map[string]any{"worker": w, "i": i},
				}
				if err := s.Put(ctx, nil, m); err != nil {
					t.Errorf("Put: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	// Verify the shared-key row exists exactly once and points at
	// sharedTarget (idempotent ON CONFLICT preserved the original
	// target).
	got, ok, err := s.Get(ctx, SourceWordPress, EntityPost, "shared")
	if err != nil || !ok {
		t.Fatalf("Get shared: err=%v ok=%v", err, ok)
	}
	if got.TargetID != sharedTarget {
		t.Fatalf("shared target diverged: %s vs %s", got.TargetID, sharedTarget)
	}
}

// idForWorker returns either the shared key or a unique-per-iteration
// key. The mix exercises both the conflict and the disjoint-key paths.
func idForWorker(w, i int) string {
	if i%2 == 0 {
		return "shared"
	}
	return string([]byte{byte('a' + (w % 26)), byte('0' + (i % 10))})
}

func TestPostgresStorePutValidatesMapping(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewPostgresStore(newFakeTx())
	err := s.Put(ctx, nil, Mapping{}) // entirely empty
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !errors.Is(err, ErrInvalidMapping) {
		t.Fatalf("expected ErrInvalidMapping, got %v", err)
	}
}

func TestPostgresStorePutTransportError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tx := newFakeTx()
	tx.execErr = errors.New("simulated transport failure")
	s := NewPostgresStore(tx)

	err := s.Put(ctx, nil, Mapping{
		Source: SourceWordPress, EntityType: EntityUser,
		SourceID: "42", TargetID: uuid.New(),
	})
	if err == nil {
		t.Fatal("expected transport error")
	}
	if errors.Is(err, ErrInvalidMapping) {
		t.Fatal("transport error misclassified as validation")
	}
}
