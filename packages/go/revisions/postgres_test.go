package revisions

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// fakeRow implements pgx.Row with a configurable Scan.
type fakeRow struct {
	scan func(dest ...any) error
}

func (r *fakeRow) Scan(dest ...any) error { return r.scan(dest...) }

// fakeRows is a small Rows implementation backed by a slice of row
// scanners.
type fakeRows struct {
	rows []func(dest ...any) error
	i    int
	err  error
}

func (r *fakeRows) Close()                                       {}
func (r *fakeRows) Err() error                                   { return r.err }
func (r *fakeRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *fakeRows) Values() ([]any, error)                       { return nil, errors.New("not implemented") }
func (r *fakeRows) RawValues() [][]byte                          { return nil }
func (r *fakeRows) Conn() *pgx.Conn                              { return nil }

func (r *fakeRows) Next() bool {
	if r.i >= len(r.rows) {
		return false
	}
	return true
}

func (r *fakeRows) Scan(dest ...any) error {
	if r.i >= len(r.rows) {
		return errors.New("no more rows")
	}
	row := r.rows[r.i]
	r.i++
	return row(dest...)
}

// queryCall captures a single QueryRow/Query/Exec invocation so tests
// can match SQL against expected fragments and inject a response.
type queryCall struct {
	sql  string
	args []any
}

// scriptedQuerier returns a sequence of canned responses keyed by the
// order of calls. Each entry's matcher (substring) MUST match the
// incoming SQL — this catches accidental SQL-vs-call ordering bugs.
type scriptedQuerier struct {
	calls []queryCall
	// queryRowResp / queryResp / execResp are returned in order. The
	// matcher is checked against the incoming SQL fragment so a test
	// failure reads as "SQL #2 expected to contain X, got Y" rather
	// than as a generic scan error.
	queryRowResp []scriptedQueryRow
	queryResp    []scriptedQuery
	execResp     []scriptedExec

	rowIdx, qIdx, execIdx int
}

type scriptedQueryRow struct {
	matcher string
	scan    func(dest ...any) error
}
type scriptedQuery struct {
	matcher string
	rows    pgx.Rows
	err     error
}
type scriptedExec struct {
	matcher string
	tag     pgconn.CommandTag
	err     error
}

func (s *scriptedQuerier) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	s.calls = append(s.calls, queryCall{sql, args})
	if s.rowIdx >= len(s.queryRowResp) {
		return &fakeRow{scan: func(...any) error {
			return errors.New("scriptedQuerier: unexpected QueryRow at index " + itoa(s.rowIdx))
		}}
	}
	r := s.queryRowResp[s.rowIdx]
	s.rowIdx++
	if r.matcher != "" && !strings.Contains(sql, r.matcher) {
		got := sql
		return &fakeRow{scan: func(...any) error {
			return errors.New("expected SQL to contain " + r.matcher + ", got " + got)
		}}
	}
	return &fakeRow{scan: r.scan}
}

func (s *scriptedQuerier) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	s.calls = append(s.calls, queryCall{sql, args})
	if s.qIdx >= len(s.queryResp) {
		return nil, errors.New("scriptedQuerier: unexpected Query at index " + itoa(s.qIdx))
	}
	r := s.queryResp[s.qIdx]
	s.qIdx++
	if r.matcher != "" && !strings.Contains(sql, r.matcher) {
		return nil, errors.New("expected SQL to contain " + r.matcher + ", got " + sql)
	}
	return r.rows, r.err
}

func (s *scriptedQuerier) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	s.calls = append(s.calls, queryCall{sql, args})
	if s.execIdx >= len(s.execResp) {
		return pgconn.CommandTag{}, errors.New("scriptedQuerier: unexpected Exec")
	}
	r := s.execResp[s.execIdx]
	s.execIdx++
	if r.matcher != "" && !strings.Contains(sql, r.matcher) {
		return pgconn.CommandTag{}, errors.New("expected SQL to contain " + r.matcher + ", got " + sql)
	}
	return r.tag, r.err
}

// itoa is a tiny helper so the fakes can format counters without
// dragging in strconv into the test surface.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var digits []byte
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}

// scanRevisionResponse builds a scan callback that fills the
// scanRevision destinations from the Revision struct passed in.
//
// Layout MUST match selectByIDSQL / listSQL / latestByKindSQL:
//
//	id, post_id, author_id, created_at, kind, title, excerpt,
//	content_blocks_hash, delta_from, delta, snapshot, comment,
//	is_permanent
func scanRevisionResponse(r Revision) func(dest ...any) error {
	return func(dest ...any) error {
		if len(dest) != 13 {
			return errors.New("scanRevisionResponse: expected 13 destinations")
		}
		*dest[0].(*string) = r.ID.String()
		*dest[1].(*string) = r.PostID.String()
		if r.AuthorID != uuid.Nil {
			*dest[2].(*string) = r.AuthorID.String()
		} else {
			*dest[2].(*string) = ""
		}
		*dest[3].(*time.Time) = r.CreatedAt
		*dest[4].(*string) = string(r.Kind)
		*dest[5].(*string) = r.Title
		*dest[6].(*string) = r.Excerpt
		*dest[7].(*[]byte) = r.ContentBlocksHash
		if r.DeltaFrom != uuid.Nil {
			*dest[8].(*string) = r.DeltaFrom.String()
		} else {
			*dest[8].(*string) = ""
		}
		*dest[9].(*[]byte) = []byte(r.Delta)
		*dest[10].(*[]byte) = []byte(r.Snapshot)
		*dest[11].(*string) = r.Comment
		*dest[12].(*bool) = r.IsPermanent
		return nil
	}
}

func TestPostgresStore_Save_FirstRevisionIsSnapshot(t *testing.T) {
	q := &scriptedQuerier{}
	post := newPostID(t)
	author := newPostID(t)
	returnedID := uuid.New()

	q.queryRowResp = []scriptedQueryRow{
		{ // 1. count since last snapshot — 0 (no rows yet)
			matcher: "COUNT(*)",
			scan: func(dest ...any) error {
				*dest[0].(*int64) = 0
				return nil
			},
		},
		{ // 2. INSERT snapshot — return id
			matcher: "INSERT INTO post_revisions",
			scan: func(dest ...any) error {
				*dest[0].(*string) = returnedID.String()
				return nil
			},
		},
	}

	s := NewPostgresStoreWithQuerier(q)
	id, err := s.Save(context.Background(), Revision{
		PostID:        post,
		AuthorID:      author,
		Kind:          Manual,
		ContentBlocks: json.RawMessage(`{"k":"v"}`),
		Title:         "T",
		Excerpt:       "E",
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if id != returnedID {
		t.Errorf("returned id: got %s want %s", id, returnedID)
	}
	if len(q.calls) != 2 {
		t.Fatalf("expected 2 SQL calls, got %d", len(q.calls))
	}
	if !strings.Contains(q.calls[1].sql, "snapshot") {
		t.Errorf("second call should be snapshot INSERT, got:\n%s", q.calls[1].sql)
	}
}

func TestPostgresStore_Save_ForceSnapshot(t *testing.T) {
	q := &scriptedQuerier{}
	returnedID := uuid.New()
	q.queryRowResp = []scriptedQueryRow{
		{matcher: "INSERT INTO post_revisions", scan: func(dest ...any) error {
			*dest[0].(*string) = returnedID.String()
			return nil
		}},
	}
	s := NewPostgresStoreWithQuerier(q)
	id, err := s.Save(context.Background(), Revision{
		PostID:        newPostID(t),
		Kind:          Publish,
		ContentBlocks: json.RawMessage(`{}`),
	}, WithForceSnapshot())
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if id != returnedID {
		t.Errorf("id mismatch")
	}
	// Only one SQL call — no probes when forced.
	if len(q.calls) != 1 {
		t.Errorf("expected 1 call (no probes), got %d", len(q.calls))
	}
}

func TestPostgresStore_Save_RejectsInvalid(t *testing.T) {
	q := &scriptedQuerier{}
	s := NewPostgresStoreWithQuerier(q)
	_, err := s.Save(context.Background(), Revision{Kind: Manual, ContentBlocks: json.RawMessage(`{}`)})
	if !errors.Is(err, ErrInvalidRevision) {
		t.Errorf("expected ErrInvalidRevision for missing PostID, got %v", err)
	}
	if len(q.calls) != 0 {
		t.Errorf("no SQL should be issued for invalid input, got %d calls", len(q.calls))
	}
}

func TestPostgresStore_Get_NotFound(t *testing.T) {
	q := &scriptedQuerier{
		queryRowResp: []scriptedQueryRow{
			{matcher: "SELECT", scan: func(dest ...any) error { return pgx.ErrNoRows }},
		},
	}
	s := NewPostgresStoreWithQuerier(q)
	_, err := s.Get(context.Background(), uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestPostgresStore_Get_RoundTrip(t *testing.T) {
	post := newPostID(t)
	author := newPostID(t)
	revID := uuid.New()
	rev := Revision{
		ID:        revID,
		PostID:    post,
		AuthorID:  author,
		CreatedAt: time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC),
		Kind:      Manual,
		Title:     "Title",
		Excerpt:   "Excerpt",
		Snapshot:  json.RawMessage(`{"k":"v"}`),
		Comment:   "comment",
	}
	q := &scriptedQuerier{
		queryRowResp: []scriptedQueryRow{
			{matcher: "SELECT", scan: scanRevisionResponse(rev)},
		},
	}
	s := NewPostgresStoreWithQuerier(q)
	got, err := s.Get(context.Background(), revID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != revID {
		t.Errorf("ID: got %s want %s", got.ID, revID)
	}
	if got.PostID != post {
		t.Errorf("PostID: got %s want %s", got.PostID, post)
	}
	if got.AuthorID != author {
		t.Errorf("AuthorID: got %s want %s", got.AuthorID, author)
	}
	if got.Kind != Manual {
		t.Errorf("Kind: got %s want %s", got.Kind, Manual)
	}
	if got.Title != "Title" {
		t.Errorf("Title: got %q", got.Title)
	}
	if string(got.Snapshot) != `{"k":"v"}` {
		t.Errorf("Snapshot: got %s", got.Snapshot)
	}
}

func TestPostgresStore_List_ClampsLimit(t *testing.T) {
	q := &scriptedQuerier{
		queryResp: []scriptedQuery{
			{matcher: "FROM post_revisions", rows: &fakeRows{}},
		},
	}
	s := NewPostgresStoreWithQuerier(q)
	_, err := s.List(context.Background(), newPostID(t), Filter{Limit: 99999})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	args := q.calls[0].args
	if got := args[len(args)-1].(int); got != postgresMaxLimit {
		t.Errorf("limit not clamped: got %d want %d", got, postgresMaxLimit)
	}
}

func TestPostgresStore_List_ZeroLimitUsesDefault(t *testing.T) {
	q := &scriptedQuerier{
		queryResp: []scriptedQuery{
			{matcher: "FROM post_revisions", rows: &fakeRows{}},
		},
	}
	s := NewPostgresStoreWithQuerier(q)
	_, err := s.List(context.Background(), newPostID(t), Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	args := q.calls[0].args
	if got := args[len(args)-1].(int); got != postgresDefaultLimit {
		t.Errorf("default limit: got %d want %d", got, postgresDefaultLimit)
	}
}

func TestPostgresStore_List_PassesFilters(t *testing.T) {
	post := newPostID(t)
	author := newPostID(t)
	q := &scriptedQuerier{
		queryResp: []scriptedQuery{
			{matcher: "FROM post_revisions", rows: &fakeRows{}},
		},
	}
	s := NewPostgresStoreWithQuerier(q)
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	_, err := s.List(context.Background(), post, Filter{
		Start:    start,
		End:      end,
		AuthorID: author,
		Kind:     Publish,
		Limit:    50,
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	args := q.calls[0].args
	if args[0] != post {
		t.Errorf("postID arg: got %v want %v", args[0], post)
	}
	if args[1].(time.Time) != start {
		t.Errorf("start arg: got %v want %v", args[1], start)
	}
	if args[2].(time.Time) != end {
		t.Errorf("end arg: got %v want %v", args[2], end)
	}
	if args[3] != author.String() {
		t.Errorf("author arg: got %v", args[3])
	}
	if args[4] != string(Publish) {
		t.Errorf("kind arg: got %v", args[4])
	}
	if args[5].(int) != 50 {
		t.Errorf("limit arg: got %v", args[5])
	}
}

func TestPostgresStore_List_ScansRows(t *testing.T) {
	post := newPostID(t)
	revs := []Revision{
		{ID: uuid.New(), PostID: post, CreatedAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), Kind: Manual, Title: "A", Snapshot: json.RawMessage(`{"a":1}`)},
		{ID: uuid.New(), PostID: post, CreatedAt: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC), Kind: Manual, Title: "B", Snapshot: json.RawMessage(`{"b":1}`)},
	}
	scans := make([]func(dest ...any) error, len(revs))
	for i, r := range revs {
		scans[i] = scanRevisionResponse(r)
	}
	q := &scriptedQuerier{
		queryResp: []scriptedQuery{
			{matcher: "FROM post_revisions", rows: &fakeRows{rows: scans}},
		},
	}
	s := NewPostgresStoreWithQuerier(q)
	got, err := s.List(context.Background(), post, Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len: got %d want 2", len(got))
	}
	if got[0].Title != "A" || got[1].Title != "B" {
		t.Errorf("titles: got [%s,%s]", got[0].Title, got[1].Title)
	}
}

func TestPostgresStore_Latest_NotFound(t *testing.T) {
	q := &scriptedQuerier{
		queryRowResp: []scriptedQueryRow{
			{matcher: "FROM post_revisions", scan: func(dest ...any) error { return pgx.ErrNoRows }},
		},
	}
	s := NewPostgresStoreWithQuerier(q)
	_, err := s.Latest(context.Background(), newPostID(t), Manual)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestPostgresStore_Latest_RejectsInvalidKind(t *testing.T) {
	q := &scriptedQuerier{}
	s := NewPostgresStoreWithQuerier(q)
	_, err := s.Latest(context.Background(), newPostID(t), RevisionKind("garbage"))
	if !errors.Is(err, ErrInvalidRevision) {
		t.Errorf("expected ErrInvalidRevision, got %v", err)
	}
	if len(q.calls) != 0 {
		t.Errorf("no SQL should be issued for invalid kind")
	}
}

func TestPostgresStore_Latest_RoundTrip(t *testing.T) {
	post := newPostID(t)
	rev := Revision{
		ID:        uuid.New(),
		PostID:    post,
		CreatedAt: time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC),
		Kind:      Publish,
		Title:     "Latest Publish",
		Snapshot:  json.RawMessage(`{"published":true}`),
	}
	q := &scriptedQuerier{
		queryRowResp: []scriptedQueryRow{
			{matcher: "ORDER BY created_at DESC, id DESC", scan: scanRevisionResponse(rev)},
		},
	}
	s := NewPostgresStoreWithQuerier(q)
	got, err := s.Latest(context.Background(), post, Publish)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if got.Kind != Publish {
		t.Errorf("Kind: got %s", got.Kind)
	}
	if got.Title != "Latest Publish" {
		t.Errorf("Title: got %q", got.Title)
	}
}

func TestPostgresStore_Materialize_Snapshot(t *testing.T) {
	post := newPostID(t)
	rev := Revision{
		ID:        uuid.New(),
		PostID:    post,
		CreatedAt: time.Now(),
		Kind:      Manual,
		Snapshot:  json.RawMessage(`{"materialized":"directly"}`),
	}
	q := &scriptedQuerier{
		queryRowResp: []scriptedQueryRow{
			{matcher: "SELECT", scan: scanRevisionResponse(rev)},
		},
	}
	s := NewPostgresStoreWithQuerier(q)
	got, err := s.Materialize(context.Background(), rev.ID)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if string(got) != `{"materialized":"directly"}` {
		t.Errorf("got %s", got)
	}
}

func TestPostgresStore_Materialize_DeltaChain(t *testing.T) {
	post := newPostID(t)
	rootID := uuid.New()
	deltaID := uuid.New()

	root := Revision{
		ID:        rootID,
		PostID:    post,
		CreatedAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		Kind:      Manual,
		Snapshot:  json.RawMessage(`{"k":"v1"}`),
	}
	// Compute a real delta so ApplyDelta will round-trip correctly.
	delta, err := ComputeDelta(json.RawMessage(`{"k":"v1"}`), json.RawMessage(`{"k":"v2"}`))
	if err != nil {
		t.Fatalf("ComputeDelta: %v", err)
	}
	leaf := Revision{
		ID:        deltaID,
		PostID:    post,
		CreatedAt: time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC),
		Kind:      Manual,
		DeltaFrom: rootID,
		Delta:     delta,
	}
	q := &scriptedQuerier{
		queryRowResp: []scriptedQueryRow{
			{matcher: "SELECT", scan: scanRevisionResponse(leaf)},
			{matcher: "SELECT", scan: scanRevisionResponse(root)},
		},
	}
	s := NewPostgresStoreWithQuerier(q)
	got, err := s.Materialize(context.Background(), deltaID)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if string(got) != `{"k":"v2"}` {
		t.Errorf("materialized: got %s want {\"k\":\"v2\"}", got)
	}
}

func TestPostgresStore_Materialize_CorruptChain(t *testing.T) {
	// A delta row whose DeltaFrom column came back as uuid.Nil is
	// proof of corruption — the CHECK constraint shouldn't let that
	// land, but the Materialize path defends anyway.
	rev := Revision{
		ID:        uuid.New(),
		PostID:    newPostID(t),
		CreatedAt: time.Now(),
		Kind:      Manual,
		Delta:     json.RawMessage(`[]`),
		// DeltaFrom intentionally zero.
	}
	q := &scriptedQuerier{
		queryRowResp: []scriptedQueryRow{
			{matcher: "SELECT", scan: scanRevisionResponse(rev)},
		},
	}
	s := NewPostgresStoreWithQuerier(q)
	_, err := s.Materialize(context.Background(), rev.ID)
	if !errors.Is(err, ErrCorruptChain) {
		t.Errorf("expected ErrCorruptChain, got %v", err)
	}
}

func TestPostgresStore_Save_DeltaPath(t *testing.T) {
	post := newPostID(t)
	parentID := uuid.New()
	returnedID := uuid.New()

	q := &scriptedQuerier{}
	q.queryRowResp = []scriptedQueryRow{
		// 1. count since last snapshot — 1 (one snapshot exists)
		{matcher: "COUNT(*)", scan: func(dest ...any) error {
			*dest[0].(*int64) = 1
			return nil
		}},
		// 2. last snapshot timestamp (still recent)
		{matcher: "snapshot IS NOT NULL", scan: func(dest ...any) error {
			*dest[0].(*time.Time) = time.Now().Add(-1 * time.Hour)
			return nil
		}},
		// 3. latest revision id (for DeltaFrom)
		{matcher: "ORDER BY created_at DESC, id DESC", scan: func(dest ...any) error {
			*dest[0].(*string) = parentID.String()
			return nil
		}},
		// 4. Materialize parent — Get on parent: returns a snapshot
		{matcher: "SELECT", scan: scanRevisionResponse(Revision{
			ID:        parentID,
			PostID:    post,
			CreatedAt: time.Now().Add(-1 * time.Hour),
			Kind:      Manual,
			Snapshot:  json.RawMessage(`{"k":"v1"}`),
		})},
		// 5. INSERT delta
		{matcher: "INSERT INTO post_revisions", scan: func(dest ...any) error {
			*dest[0].(*string) = returnedID.String()
			return nil
		}},
	}
	s := NewPostgresStoreWithQuerier(q)
	id, err := s.Save(context.Background(), Revision{
		PostID:        post,
		Kind:          Manual,
		ContentBlocks: json.RawMessage(`{"k":"v2"}`),
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if id != returnedID {
		t.Errorf("returned id mismatch")
	}
	// The final SQL call should be the delta INSERT, not the snapshot one.
	last := q.calls[len(q.calls)-1].sql
	if !strings.Contains(last, "delta_from") || !strings.Contains(last, "delta,") {
		t.Errorf("expected delta INSERT, got:\n%s", last)
	}
}

func TestPostgresStore_Save_TriggersSnapshotPastN(t *testing.T) {
	post := newPostID(t)
	returnedID := uuid.New()
	q := &scriptedQuerier{}
	q.queryRowResp = []scriptedQueryRow{
		// count since last snapshot: 20 (>= default N=20)
		{matcher: "COUNT(*)", scan: func(dest ...any) error {
			*dest[0].(*int64) = 20
			return nil
		}},
		// INSERT snapshot
		{matcher: "INSERT INTO post_revisions", scan: func(dest ...any) error {
			*dest[0].(*string) = returnedID.String()
			return nil
		}},
	}
	s := NewPostgresStoreWithQuerier(q)
	_, err := s.Save(context.Background(), Revision{
		PostID:        post,
		Kind:          Manual,
		ContentBlocks: json.RawMessage(`{"k":"v"}`),
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	last := q.calls[len(q.calls)-1].sql
	if !strings.Contains(last, "snapshot") || strings.Contains(last, "delta_from") {
		t.Errorf("expected snapshot INSERT (since >= N), got:\n%s", last)
	}
}

func TestPostgresStore_Save_TriggersSnapshotPastAge(t *testing.T) {
	post := newPostID(t)
	returnedID := uuid.New()
	q := &scriptedQuerier{}
	q.queryRowResp = []scriptedQueryRow{
		// count since last snapshot: small, below N
		{matcher: "COUNT(*)", scan: func(dest ...any) error {
			*dest[0].(*int64) = 3
			return nil
		}},
		// last snapshot was 25h ago — over MaxSnapshotAge of 24h
		{matcher: "snapshot IS NOT NULL", scan: func(dest ...any) error {
			*dest[0].(*time.Time) = time.Now().Add(-25 * time.Hour)
			return nil
		}},
		// INSERT snapshot
		{matcher: "INSERT INTO post_revisions", scan: func(dest ...any) error {
			*dest[0].(*string) = returnedID.String()
			return nil
		}},
	}
	s := NewPostgresStoreWithQuerier(q)
	_, err := s.Save(context.Background(), Revision{
		PostID:        post,
		Kind:          Manual,
		ContentBlocks: json.RawMessage(`{"k":"v"}`),
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	last := q.calls[len(q.calls)-1].sql
	if !strings.Contains(last, "snapshot") || strings.Contains(last, "delta_from") {
		t.Errorf("expected snapshot INSERT (age past cap), got:\n%s", last)
	}
}

func TestPostgresStore_Prune_DeletesMarkedRows(t *testing.T) {
	post := newPostID(t)
	old := time.Now().Add(-30 * 24 * time.Hour) // 30 days old — past MinKeepAll
	revs := []pruneCandidate{
		{id: uuid.New(), createdAt: old, kind: Manual, isSnapshot: true},
		{id: uuid.New(), createdAt: old.Add(1 * time.Hour), kind: Manual, isSnapshot: true},
		{id: uuid.New(), createdAt: old.Add(2 * time.Hour), kind: Manual, isSnapshot: true},
		{id: uuid.New(), createdAt: old.Add(3 * time.Hour), kind: Manual, isSnapshot: true},
	}

	scans := make([]func(dest ...any) error, len(revs))
	for i, r := range revs {
		r := r
		scans[i] = func(dest ...any) error {
			*dest[0].(*string) = r.id.String()
			*dest[1].(*string) = ""
			*dest[2].(*time.Time) = r.createdAt
			*dest[3].(*RevisionKind) = r.kind
			*dest[4].(*bool) = r.isSnapshot
			*dest[5].(*string) = ""
			*dest[6].(*bool) = r.isPermanent
			return nil
		}
	}

	q := &scriptedQuerier{
		queryResp: []scriptedQuery{
			{matcher: "ORDER BY created_at ASC", rows: &fakeRows{rows: scans}},
		},
		execResp: []scriptedExec{
			{matcher: "DELETE FROM post_revisions", tag: pgconn.NewCommandTag("DELETE 1")},
			{matcher: "DELETE FROM post_revisions", tag: pgconn.NewCommandTag("DELETE 1")},
		},
	}
	s := NewPostgresStoreWithQuerier(q)
	deleted, err := s.Prune(context.Background(), post, RetentionPolicy{
		MaxManual:  2,
		MinKeepAll: 0,
	})
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if deleted != 2 {
		t.Errorf("deleted: got %d want 2", deleted)
	}
}

func TestPostgresStore_Prune_NoRows(t *testing.T) {
	post := newPostID(t)
	q := &scriptedQuerier{
		queryResp: []scriptedQuery{
			{matcher: "ORDER BY created_at ASC", rows: &fakeRows{}},
		},
	}
	s := NewPostgresStoreWithQuerier(q)
	deleted, err := s.Prune(context.Background(), post, DefaultRetentionPolicy())
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if deleted != 0 {
		t.Errorf("deleted: got %d want 0", deleted)
	}
}

func TestPostgresStore_Prune_PropagatesQueryError(t *testing.T) {
	post := newPostID(t)
	q := &scriptedQuerier{
		queryResp: []scriptedQuery{
			{matcher: "ORDER BY", err: errors.New("network down")},
		},
	}
	s := NewPostgresStoreWithQuerier(q)
	_, err := s.Prune(context.Background(), post, DefaultRetentionPolicy())
	if err == nil || !strings.Contains(err.Error(), "network down") {
		t.Errorf("expected wrapped network error, got %v", err)
	}
}

func TestPostgresStore_Save_PropagatesInsertError(t *testing.T) {
	post := newPostID(t)
	q := &scriptedQuerier{
		queryRowResp: []scriptedQueryRow{
			{matcher: "INSERT INTO post_revisions", scan: func(dest ...any) error {
				return errors.New("constraint violated")
			}},
		},
	}
	s := NewPostgresStoreWithQuerier(q)
	_, err := s.Save(context.Background(), Revision{
		PostID:        post,
		Kind:          Manual,
		ContentBlocks: json.RawMessage(`{}`),
	}, WithForceSnapshot())
	if err == nil || !strings.Contains(err.Error(), "constraint violated") {
		t.Errorf("expected wrapped insert error, got %v", err)
	}
}
