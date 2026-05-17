package ratelimit

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"
)

// TestMemoryFailureStore_Lifecycle exercises the full FailureStore
// contract on the in-memory implementation: increment, set lock, read,
// clear.
func TestMemoryFailureStore_Lifecycle(t *testing.T) {
	s := NewMemoryFailureStore()
	ctx := context.Background()
	const u = "user-1"

	// Fresh state is zero.
	c, lu, err := s.GetFailures(ctx, u)
	if err != nil {
		t.Fatal(err)
	}
	if c != 0 || !lu.IsZero() {
		t.Errorf("fresh state: count=%d lockedUntil=%v", c, lu)
	}

	// Two increments → count 2, no lock.
	for i := 0; i < 2; i++ {
		c, _, err = s.IncrementFailure(ctx, u)
		if err != nil {
			t.Fatal(err)
		}
	}
	if c != 2 {
		t.Errorf("after 2 increments, count=%d want 2", c)
	}

	// Set lock; GetFailures sees it.
	when := time.Now().Add(time.Hour)
	if err := s.SetLockedUntil(ctx, u, when); err != nil {
		t.Fatal(err)
	}
	_, lu, _ = s.GetFailures(ctx, u)
	if !lu.Equal(when) {
		t.Errorf("lockedUntil: got %v want %v", lu, when)
	}

	// Clear wipes both.
	if err := s.ClearFailures(ctx, u); err != nil {
		t.Fatal(err)
	}
	c, lu, _ = s.GetFailures(ctx, u)
	if c != 0 || !lu.IsZero() {
		t.Errorf("after Clear: count=%d lockedUntil=%v", c, lu)
	}
}

// TestMemoryFailureStore_Concurrent asserts that concurrent
// IncrementFailure calls on the same userID produce exactly N
// increments (no lost updates).
func TestMemoryFailureStore_Concurrent(t *testing.T) {
	s := NewMemoryFailureStore()
	ctx := context.Background()
	const N = 200
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _ = s.IncrementFailure(ctx, "race")
		}()
	}
	wg.Wait()
	c, _, _ := s.GetFailures(ctx, "race")
	if c != N {
		t.Errorf("expected %d increments, got %d", N, c)
	}
}

// TestPostgresFailureStore_NilDB confirms the constructor rejects nil.
func TestPostgresFailureStore_NilDB(t *testing.T) {
	_, err := NewPostgresFailureStore(nil)
	if !errors.Is(err, ErrFailureStorePostgresNilDB) {
		t.Errorf("expected ErrFailureStorePostgresNilDB; got %v", err)
	}
}

// TestPostgresFailureStore_QueryShape verifies the SQL statements the
// store issues against a fake executor. The shape is what a follow-up
// migration PR needs to keep stable.
func TestPostgresFailureStore_QueryShape(t *testing.T) {
	now := time.Unix(1_700_800_000, 0).UTC()
	fake := &fakePGExecutor{
		rowResult: rowResult{
			scan: func(dest ...any) error {
				return scanInto(dest, 3, sql.NullTime{Time: now, Valid: true})
			},
		},
	}
	store := newPostgresFailureStoreWithExecutor(fake)

	// IncrementFailure: UPDATE ... RETURNING.
	count, lu, err := store.IncrementFailure(context.Background(), "u-42")
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Errorf("count: got %d want 3", count)
	}
	if !lu.Equal(now) {
		t.Errorf("lockedUntil: got %v", lu)
	}
	if got := fake.lastQuery(); !contains(got, "UPDATE users SET failed_login_count = failed_login_count + 1") {
		t.Errorf("IncrementFailure SQL shape unexpected: %s", got)
	}
	if !contains(fake.lastQuery(), "RETURNING failed_login_count, locked_until") {
		t.Errorf("IncrementFailure missing RETURNING: %s", fake.lastQuery())
	}

	// SetLockedUntil.
	when := time.Unix(1_700_900_000, 0).UTC()
	if err := store.SetLockedUntil(context.Background(), "u-42", when); err != nil {
		t.Fatal(err)
	}
	if !contains(fake.lastQuery(), "SET locked_until = $1") {
		t.Errorf("SetLockedUntil SQL unexpected: %s", fake.lastQuery())
	}

	// ClearFailures.
	if err := store.ClearFailures(context.Background(), "u-42"); err != nil {
		t.Fatal(err)
	}
	if !contains(fake.lastQuery(), "SET failed_login_count = 0, locked_until = NULL") {
		t.Errorf("ClearFailures SQL unexpected: %s", fake.lastQuery())
	}

	// GetFailures (SELECT) — set fake to return count 7.
	fake.setRow(rowResult{scan: func(dest ...any) error {
		return scanInto(dest, 7, sql.NullTime{})
	}})
	c2, _, err := store.GetFailures(context.Background(), "u-42")
	if err != nil {
		t.Fatal(err)
	}
	if c2 != 7 {
		t.Errorf("Get count: got %d want 7", c2)
	}
	if !contains(fake.lastQuery(), "SELECT failed_login_count, locked_until FROM users") {
		t.Errorf("GetFailures SQL unexpected: %s", fake.lastQuery())
	}
}

// TestPostgresFailureStore_GetUnknown returns zero-value when the
// underlying row isn't present (sql.ErrNoRows path).
func TestPostgresFailureStore_GetUnknown(t *testing.T) {
	fake := &fakePGExecutor{rowResult: rowResult{scan: func(dest ...any) error { return sql.ErrNoRows }}}
	store := newPostgresFailureStoreWithExecutor(fake)

	count, lu, err := store.GetFailures(context.Background(), "ghost")
	if err != nil {
		t.Fatalf("ErrNoRows should be swallowed; got %v", err)
	}
	if count != 0 || !lu.IsZero() {
		t.Errorf("zero-value expected; got count=%d lockedUntil=%v", count, lu)
	}
}

// TestPostgresFailureStore_NullLockedUntil verifies the
// sql.NullTime path returns time.Time{} rather than a populated
// timestamp when the column is NULL.
func TestPostgresFailureStore_NullLockedUntil(t *testing.T) {
	fake := &fakePGExecutor{rowResult: rowResult{scan: func(dest ...any) error {
		return scanInto(dest, 1, sql.NullTime{Valid: false})
	}}}
	store := newPostgresFailureStoreWithExecutor(fake)

	_, lu, err := store.IncrementFailure(context.Background(), "u")
	if err != nil {
		t.Fatal(err)
	}
	if !lu.IsZero() {
		t.Errorf("expected zero time for NULL locked_until; got %v", lu)
	}
}

// TestPostgresFailureStore_ScanError surfaces a non-ErrNoRows scan
// error from GetFailures as a wrapped store error.
func TestPostgresFailureStore_ScanError(t *testing.T) {
	want := errors.New("driver boom")
	fake := &fakePGExecutor{rowResult: rowResult{scan: func(dest ...any) error { return want }}}
	store := newPostgresFailureStoreWithExecutor(fake)

	if _, _, err := store.GetFailures(context.Background(), "u"); err == nil {
		t.Fatal("expected scan error")
	}
	if _, _, err := store.IncrementFailure(context.Background(), "u"); err == nil {
		t.Fatal("expected scan error")
	}
}

// TestPostgresFailureStore_ExecError surfaces Exec errors from
// SetLockedUntil and ClearFailures.
func TestPostgresFailureStore_ExecError(t *testing.T) {
	want := errors.New("exec boom")
	fake := &fakePGExecutor{execErr: want}
	store := newPostgresFailureStoreWithExecutor(fake)
	if err := store.SetLockedUntil(context.Background(), "u", time.Now()); err == nil {
		t.Error("expected SetLockedUntil error")
	}
	if err := store.ClearFailures(context.Background(), "u"); err == nil {
		t.Error("expected ClearFailures error")
	}
}

// fakePGExecutor implements pgExecutor with controllable return
// values. Records queries for assertion and lets each test plug in
// a Scan closure that fills the destination pointers however the
// test needs.
type fakePGExecutor struct {
	mu        sync.Mutex
	queries   []string
	rowResult rowResult
	execErr   error
}

type rowResult struct {
	scan func(dest ...any) error
}

type fakeSQLRow struct {
	scan func(dest ...any) error
}

func (r fakeSQLRow) Scan(dest ...any) error {
	if r.scan == nil {
		return errors.New("fake row: no scan func registered")
	}
	return r.scan(dest...)
}

func (f *fakePGExecutor) ExecContext(_ context.Context, query string, _ ...any) (sql.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queries = append(f.queries, query)
	if f.execErr != nil {
		return nil, f.execErr
	}
	return execResultStub{}, nil
}

func (f *fakePGExecutor) QueryRowContext(_ context.Context, query string, _ ...any) rowScanner {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queries = append(f.queries, query)
	return fakeSQLRow{scan: f.rowResult.scan}
}

func (f *fakePGExecutor) setRow(r rowResult) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rowResult = r
}

func (f *fakePGExecutor) lastQuery() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.queries) == 0 {
		return ""
	}
	return f.queries[len(f.queries)-1]
}

type execResultStub struct{}

func (execResultStub) LastInsertId() (int64, error) { return 0, nil }
func (execResultStub) RowsAffected() (int64, error) { return 0, nil }

// scanInto is a helper for fakeSQLRow.scan closures: writes count
// and lockedUntil into the dest slice in the order
// PostgresFailureStore expects.
func scanInto(dest []any, count int, lockedUntil sql.NullTime) error {
	if len(dest) != 2 {
		return errors.New("scanInto: expected 2 dest pointers")
	}
	if p, ok := dest[0].(*int); ok {
		*p = count
	} else {
		return errors.New("scanInto: dest[0] is not *int")
	}
	if p, ok := dest[1].(*sql.NullTime); ok {
		*p = lockedUntil
	} else {
		return errors.New("scanInto: dest[1] is not *sql.NullTime")
	}
	return nil
}

// contains is a tiny substring helper used by the SQL-shape
// assertions.
func contains(haystack, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	if len(needle) > len(haystack) {
		return false
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
