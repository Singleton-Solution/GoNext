package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// rowsAffectedTag is a tiny constructor: pgconn.CommandTag is a struct
// whose only field is the string form ("UPDATE n"). We use the public
// NewCommandTag helper so test fixtures don't have to know the wire
// format — "UPDATE 1" yields RowsAffected() == 1.
func rowsAffectedTag(n int64) pgconn.CommandTag {
	if n == 0 {
		return pgconn.NewCommandTag("UPDATE 0")
	}
	return pgconn.NewCommandTag(fmt.Sprintf("UPDATE %d", n))
}

// fakeRow implements pgx.Row by replaying canned Scan args.
type fakeRow struct {
	values []any
	err    error
}

func (r *fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		return errors.New("fakeRow: dest count mismatch")
	}
	for i, v := range r.values {
		if err := assign(dest[i], v); err != nil {
			return err
		}
	}
	return nil
}

// fakeRows implements pgx.Rows over a fixed slice of canned rows. It's
// the minimum surface PostgresStorage.List actually touches.
type fakeRows struct {
	rows [][]any
	idx  int
	err  error
}

func (r *fakeRows) Close()                                    {}
func (r *fakeRows) Err() error                                { return r.err }
func (r *fakeRows) CommandTag() pgconn.CommandTag             { return pgconn.NewCommandTag("SELECT 0") }
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *fakeRows) Conn() *pgx.Conn                           { return nil }
func (r *fakeRows) RawValues() [][]byte                       { return nil }
func (r *fakeRows) Values() ([]any, error)                    { return nil, nil }

func (r *fakeRows) Next() bool {
	if r.idx >= len(r.rows) {
		return false
	}
	r.idx++
	return true
}

func (r *fakeRows) Scan(dest ...any) error {
	row := r.rows[r.idx-1]
	if len(dest) != len(row) {
		return errors.New("fakeRows: dest count mismatch")
	}
	for i, v := range row {
		if err := assign(dest[i], v); err != nil {
			return err
		}
	}
	return nil
}

// fakeQuerier captures calls and returns canned responses. Tests
// inject queryRowFn / queryFn / execFn to model what Postgres would
// return for the next call.
type fakeQuerier struct {
	mu sync.Mutex

	lastSQL  string
	lastArgs []any

	queryRowFn func(sql string, args []any) pgx.Row
	queryFn    func(sql string, args []any) (pgx.Rows, error)
	execFn     func(sql string, args []any) (pgconn.CommandTag, error)
}

func (f *fakeQuerier) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	f.mu.Lock()
	f.lastSQL, f.lastArgs = sql, args
	f.mu.Unlock()
	if f.queryRowFn != nil {
		return f.queryRowFn(sql, args)
	}
	return &fakeRow{err: errors.New("queryRowFn not set")}
}

func (f *fakeQuerier) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	f.mu.Lock()
	f.lastSQL, f.lastArgs = sql, args
	f.mu.Unlock()
	if f.queryFn != nil {
		return f.queryFn(sql, args)
	}
	return nil, errors.New("queryFn not set")
}

func (f *fakeQuerier) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	f.mu.Lock()
	f.lastSQL, f.lastArgs = sql, args
	f.mu.Unlock()
	if f.execFn != nil {
		return f.execFn(sql, args)
	}
	return rowsAffectedTag(1), nil
}

// assign is the tiny test-only scanner that copies a canned value into
// a pgx Scan destination pointer. We support only the types our SELECT
// list uses; anything else fails loudly so a new column is caught.
func assign(dst, src any) error {
	switch d := dst.(type) {
	case *string:
		switch s := src.(type) {
		case string:
			*d = s
		default:
			return errors.New("assign: want string")
		}
	case *int:
		switch n := src.(type) {
		case int:
			*d = n
		default:
			return errors.New("assign: want int")
		}
	case *int64:
		switch n := src.(type) {
		case int64:
			*d = n
		default:
			return errors.New("assign: want int64")
		}
	case *time.Time:
		switch t := src.(type) {
		case time.Time:
			*d = t
		default:
			return errors.New("assign: want time.Time")
		}
	case *[]byte:
		switch b := src.(type) {
		case []byte:
			*d = b
		case string:
			*d = []byte(b)
		default:
			return errors.New("assign: want []byte")
		}
	default:
		return errors.New("assign: unsupported dest type")
	}
	return nil
}

// stubPgError implements the sqlStater interface so we can simulate a
// unique-violation without dragging pgconn into test code.
type stubPgError struct{ code string }

func (e *stubPgError) Error() string    { return "stub: " + e.code }
func (e *stubPgError) SQLState() string { return e.code }

// ----- PostgresStorage: Insert -----

func TestPostgresStorage_Insert_HappyPath(t *testing.T) {
	q := &fakeQuerier{}
	st := NewPostgresStorageWithQuerier(q)
	st.NowFunc = func() time.Time { return fixedTime }

	err := st.Insert(context.Background(), Plugin{
		Slug:         "gn-seo",
		Version:      "1.0.0",
		ABIVersion:   1,
		State:        StateInstalled,
		Capabilities: []string{"kv"},
		Manifest:     []byte(`{"slug":"gn-seo"}`),
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if !strings.Contains(q.lastSQL, "INSERT INTO plugins") {
		t.Errorf("bad SQL: %s", q.lastSQL)
	}
}

func TestPostgresStorage_Insert_RejectsEmptySlug(t *testing.T) {
	q := &fakeQuerier{}
	st := NewPostgresStorageWithQuerier(q)
	err := st.Insert(context.Background(), Plugin{Slug: "", State: StateInstalled})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPostgresStorage_Insert_RejectsInvalidState(t *testing.T) {
	q := &fakeQuerier{}
	st := NewPostgresStorageWithQuerier(q)
	err := st.Insert(context.Background(), Plugin{Slug: "x", State: "bogus"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPostgresStorage_Insert_TranslatesUniqueViolation(t *testing.T) {
	q := &fakeQuerier{
		execFn: func(_ string, _ []any) (pgconn.CommandTag, error) {
			return rowsAffectedTag(0), &stubPgError{code: "23505"}
		},
	}
	st := NewPostgresStorageWithQuerier(q)
	err := st.Insert(context.Background(), Plugin{
		Slug: "gn-seo", Version: "1.0.0", ABIVersion: 1, State: StateInstalled,
	})
	if !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("got %v want ErrAlreadyExists", err)
	}
}

func TestPostgresStorage_Insert_PassesThroughOtherErrors(t *testing.T) {
	q := &fakeQuerier{
		execFn: func(_ string, _ []any) (pgconn.CommandTag, error) {
			return rowsAffectedTag(0), errors.New("conn dead")
		},
	}
	st := NewPostgresStorageWithQuerier(q)
	err := st.Insert(context.Background(), Plugin{
		Slug: "gn-seo", Version: "1.0.0", ABIVersion: 1, State: StateInstalled,
	})
	if err == nil || !strings.Contains(err.Error(), "conn dead") {
		t.Fatalf("got %v want conn-dead error", err)
	}
}

func TestPostgresStorage_Insert_RejectsZeroRowsAffected(t *testing.T) {
	q := &fakeQuerier{
		execFn: func(_ string, _ []any) (pgconn.CommandTag, error) {
			return rowsAffectedTag(0), nil
		},
	}
	st := NewPostgresStorageWithQuerier(q)
	err := st.Insert(context.Background(), Plugin{
		Slug: "gn-seo", Version: "1.0.0", ABIVersion: 1, State: StateInstalled,
	})
	if err == nil {
		t.Fatal("expected error for 0 rows affected")
	}
}

// ----- PostgresStorage: Get -----

func TestPostgresStorage_Get_HappyPath(t *testing.T) {
	q := &fakeQuerier{
		queryRowFn: func(_ string, _ []any) pgx.Row {
			return &fakeRow{values: []any{
				"gn-seo", "1.0.0", 1,
				[]byte(`{"slug":"gn-seo"}`),
				"installed",
				[]byte(`["kv"]`),
				"",
				time.Unix(0, 0).UTC(),
				fixedTime, time.Unix(0, 0).UTC(),
				int64(1), fixedTime,
			}}
		},
	}
	st := NewPostgresStorageWithQuerier(q)
	got, err := st.Get(context.Background(), "gn-seo")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Slug != "gn-seo" || got.State != StateInstalled {
		t.Errorf("unexpected: %+v", got)
	}
	if len(got.Capabilities) != 1 || got.Capabilities[0] != "kv" {
		t.Errorf("caps: %v", got.Capabilities)
	}
}

func TestPostgresStorage_Get_NotFound(t *testing.T) {
	q := &fakeQuerier{
		queryRowFn: func(_ string, _ []any) pgx.Row {
			return &fakeRow{err: pgx.ErrNoRows}
		},
	}
	st := NewPostgresStorageWithQuerier(q)
	_, err := st.Get(context.Background(), "ghost")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v want ErrNotFound", err)
	}
}

func TestPostgresStorage_Get_OtherError(t *testing.T) {
	q := &fakeQuerier{
		queryRowFn: func(_ string, _ []any) pgx.Row {
			return &fakeRow{err: errors.New("DB down")}
		},
	}
	st := NewPostgresStorageWithQuerier(q)
	_, err := st.Get(context.Background(), "x")
	if err == nil || strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected wrapped DB-down error, got %v", err)
	}
}

// ----- PostgresStorage: List -----

func TestPostgresStorage_List_HappyPath(t *testing.T) {
	q := &fakeQuerier{
		queryFn: func(_ string, _ []any) (pgx.Rows, error) {
			return &fakeRows{rows: [][]any{
				{"gn-a", "1.0.0", 1,
					[]byte(`{}`), "installed", []byte(`[]`),
					"", time.Unix(0, 0).UTC(),
					fixedTime, time.Unix(0, 0).UTC(),
					int64(1), fixedTime,
				},
				{"gn-b", "1.0.0", 1,
					[]byte(`{}`), "active", []byte(`[]`),
					"", time.Unix(0, 0).UTC(),
					fixedTime, fixedTime,
					int64(2), fixedTime,
				},
			}}, nil
		},
	}
	st := NewPostgresStorageWithQuerier(q)
	rows, err := st.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 2 || rows[0].Slug != "gn-a" || rows[1].Slug != "gn-b" {
		t.Errorf("rows: %+v", rows)
	}
	if rows[1].ActivatedAt.IsZero() {
		t.Error("ActivatedAt should be set for the active row")
	}
}

func TestPostgresStorage_List_QueryError(t *testing.T) {
	q := &fakeQuerier{
		queryFn: func(_ string, _ []any) (pgx.Rows, error) {
			return nil, errors.New("DB down")
		},
	}
	st := NewPostgresStorageWithQuerier(q)
	_, err := st.List(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPostgresStorage_List_ScanError(t *testing.T) {
	q := &fakeQuerier{
		queryFn: func(_ string, _ []any) (pgx.Rows, error) {
			// Row with wrong number of fields → Scan error.
			return &fakeRows{rows: [][]any{{"only-one-field"}}}, nil
		},
	}
	st := NewPostgresStorageWithQuerier(q)
	_, err := st.List(context.Background())
	if err == nil {
		t.Fatal("expected scan error")
	}
}

func TestPostgresStorage_List_RowsErr(t *testing.T) {
	q := &fakeQuerier{
		queryFn: func(_ string, _ []any) (pgx.Rows, error) {
			return &fakeRows{err: errors.New("torn cursor")}, nil
		},
	}
	st := NewPostgresStorageWithQuerier(q)
	_, err := st.List(context.Background())
	if err == nil || !strings.Contains(err.Error(), "torn cursor") {
		t.Fatalf("got %v", err)
	}
}

// ----- PostgresStorage: UpdateState -----

func TestPostgresStorage_UpdateState_HappyPath(t *testing.T) {
	q := &fakeQuerier{
		execFn: func(_ string, _ []any) (pgconn.CommandTag, error) {
			return rowsAffectedTag(1), nil
		},
	}
	st := NewPostgresStorageWithQuerier(q)
	st.NowFunc = func() time.Time { return fixedTime }

	err := st.UpdateState(context.Background(), "gn-seo",
		StateInstalled, StateActive, nil)
	if err != nil {
		t.Fatalf("UpdateState: %v", err)
	}
}

func TestPostgresStorage_UpdateState_LostCAS(t *testing.T) {
	q := &fakeQuerier{
		execFn: func(_ string, _ []any) (pgconn.CommandTag, error) {
			return rowsAffectedTag(0), nil
		},
	}
	st := NewPostgresStorageWithQuerier(q)
	err := st.UpdateState(context.Background(), "gn-seo",
		StateInstalled, StateActive, nil)
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("got %v want ErrInvalidTransition", err)
	}
}

func TestPostgresStorage_UpdateState_AppliesFields(t *testing.T) {
	var captured []any
	q := &fakeQuerier{
		execFn: func(_ string, args []any) (pgconn.CommandTag, error) {
			captured = args
			return rowsAffectedTag(1), nil
		},
	}
	st := NewPostgresStorageWithQuerier(q)

	at := time.Date(2026, 5, 17, 14, 0, 0, 0, time.UTC)
	reason := "boom"
	err := st.UpdateState(context.Background(), "gn-seo",
		StateActive, StateErrored, &StateUpdateFields{
			LastError: &reason,
			ErrorAt:   &at,
		})
	if err != nil {
		t.Fatalf("UpdateState: %v", err)
	}
	// args: state, updatedAt, activatedAt, lastError, errorAt, slug, expectedFrom
	// lastError is index 3.
	if captured[3] != reason {
		t.Errorf("lastError arg: got %v want %v", captured[3], reason)
	}
}

func TestPostgresStorage_UpdateState_RejectsInvalid(t *testing.T) {
	q := &fakeQuerier{}
	st := NewPostgresStorageWithQuerier(q)

	t.Run("bad newState", func(t *testing.T) {
		err := st.UpdateState(context.Background(), "x", StateActive, "weird", nil)
		if err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("bad expectedFrom", func(t *testing.T) {
		err := st.UpdateState(context.Background(), "x", "weird", StateActive, nil)
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestPostgresStorage_UpdateState_ExecError(t *testing.T) {
	q := &fakeQuerier{
		execFn: func(_ string, _ []any) (pgconn.CommandTag, error) {
			return rowsAffectedTag(0), errors.New("conn dead")
		},
	}
	st := NewPostgresStorageWithQuerier(q)
	err := st.UpdateState(context.Background(), "x",
		StateInstalled, StateActive, nil)
	if err == nil || !strings.Contains(err.Error(), "conn dead") {
		t.Fatalf("got %v", err)
	}
}

// ----- PostgresStorage: Delete -----

func TestPostgresStorage_Delete_HappyPath(t *testing.T) {
	q := &fakeQuerier{
		execFn: func(_ string, _ []any) (pgconn.CommandTag, error) {
			return rowsAffectedTag(1), nil
		},
	}
	st := NewPostgresStorageWithQuerier(q)
	if err := st.Delete(context.Background(), "gn-seo"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestPostgresStorage_Delete_NotFound(t *testing.T) {
	q := &fakeQuerier{
		execFn: func(_ string, _ []any) (pgconn.CommandTag, error) {
			return rowsAffectedTag(0), nil
		},
	}
	st := NewPostgresStorageWithQuerier(q)
	err := st.Delete(context.Background(), "gn-seo")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v want ErrNotFound", err)
	}
}

func TestPostgresStorage_Delete_ExecError(t *testing.T) {
	q := &fakeQuerier{
		execFn: func(_ string, _ []any) (pgconn.CommandTag, error) {
			return rowsAffectedTag(0), errors.New("DB down")
		},
	}
	st := NewPostgresStorageWithQuerier(q)
	err := st.Delete(context.Background(), "x")
	if err == nil {
		t.Fatal("expected error")
	}
}

// ----- Constructor / wiring -----

func TestNewPostgresStorage_PanicsOnNilPool(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic")
		}
	}()
	NewPostgresStorage(nil)
}

func TestNewPostgresStorageWithQuerier_PanicsOnNil(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic")
		}
	}()
	NewPostgresStorageWithQuerier(nil)
}

func TestPostgresStorage_DefaultNowFunc(t *testing.T) {
	q := &fakeQuerier{}
	st := NewPostgresStorageWithQuerier(q)
	// Default NowFunc must not panic.
	if st.now().IsZero() {
		t.Error("default now returned zero time")
	}
}

// ----- Helpers -----

func TestIsUniqueViolation(t *testing.T) {
	if !isUniqueViolation(&stubPgError{code: "23505"}) {
		t.Error("23505 should be unique violation")
	}
	if isUniqueViolation(&stubPgError{code: "23503"}) {
		t.Error("23503 (FK) should not be unique violation")
	}
	if isUniqueViolation(errors.New("plain")) {
		t.Error("plain error should not be unique violation")
	}
	// Wrapped pg error.
	wrapped := fmt.Errorf("wrapped: %w", &stubPgError{code: "23505"})
	if !isUniqueViolation(wrapped) {
		t.Error("wrapped 23505 should still be detected")
	}
	if isUniqueViolation(nil) {
		t.Error("nil should not match")
	}
}

func TestIsEpoch(t *testing.T) {
	if !isEpoch(time.Time{}) {
		t.Error("zero time should be epoch")
	}
	if !isEpoch(time.Unix(0, 0).UTC()) {
		t.Error("unix epoch should be epoch")
	}
	if isEpoch(fixedTime) {
		t.Error("non-epoch time wrongly flagged")
	}
}
