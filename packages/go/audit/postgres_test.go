package audit

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// fakeRow implements pgx.Row over a fixed scan result.
type fakeRow struct {
	values []any
	err    error
}

func (r *fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		return errors.New("fakeRow: wrong dest count")
	}
	for i, v := range r.values {
		switch d := dest[i].(type) {
		case *string:
			*d = v.(string)
		default:
			return errors.New("fakeRow: unsupported dest type for test")
		}
	}
	return nil
}

// fakeQuerier captures the last Emit call and returns canned responses.
type fakeQuerier struct {
	lastSQL  string
	lastArgs []any

	insertReturnID string
	insertErr      error

	queryRows pgx.Rows
	queryErr  error
}

func (q *fakeQuerier) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	q.lastSQL = sql
	q.lastArgs = args
	if q.insertErr != nil {
		return &fakeRow{err: q.insertErr}
	}
	return &fakeRow{values: []any{q.insertReturnID}}
}

func (q *fakeQuerier) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	q.lastSQL = sql
	q.lastArgs = args
	return q.queryRows, q.queryErr
}

func TestPostgresStore_Emit_BuildsCorrectArgs(t *testing.T) {
	q := &fakeQuerier{insertReturnID: "1"}
	s := NewPostgresStoreWithQuerier(q)
	fixed := time.Date(2026, 5, 17, 10, 0, 0, 0, time.UTC)
	s.NowFunc = func() time.Time { return fixed }

	err := s.Emit(context.Background(), Event{
		EventType:       "auth.login.success",
		ActorUserID:     "42",
		ActorPluginSlug: "",
		IP:              "192.0.2.5",
		UserAgent:       "ua",
		ResourceType:    "user",
		ResourceID:      "42",
		Metadata:        map[string]any{"method": "password"},
		Severity:        SeverityInfo,
	})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if q.lastSQL == "" || !contains(q.lastSQL, "INSERT INTO audit_log") {
		t.Errorf("SQL missing INSERT: %s", q.lastSQL)
	}
	if len(q.lastArgs) != 12 {
		t.Fatalf("args count: got %d want 12", len(q.lastArgs))
	}
	if q.lastArgs[0].(time.Time) != fixed {
		t.Errorf("time arg: got %v want %v", q.lastArgs[0], fixed)
	}
	if q.lastArgs[1] != "42" {
		t.Errorf("actor_user_id: got %v", q.lastArgs[1])
	}
	if q.lastArgs[2] != "user" {
		t.Errorf("actor_kind: got %v want user", q.lastArgs[2])
	}
	if q.lastArgs[4] != "auth.login.success" {
		t.Errorf("event: got %v", q.lastArgs[4])
	}
	if q.lastArgs[10] != string(SeverityInfo) {
		t.Errorf("severity: got %v", q.lastArgs[10])
	}
}

func TestPostgresStore_Emit_ActorKindPlugin(t *testing.T) {
	q := &fakeQuerier{insertReturnID: "1"}
	s := NewPostgresStoreWithQuerier(q)

	_ = s.Emit(context.Background(), Event{
		EventType:       "gn-forms.submission.exported",
		ActorPluginSlug: "gn-forms",
	})
	if q.lastArgs[2] != "plugin" {
		t.Errorf("actor_kind: got %v want plugin", q.lastArgs[2])
	}
	if q.lastArgs[3] != "gn-forms" {
		t.Errorf("actor_label: got %v want gn-forms", q.lastArgs[3])
	}
}

func TestPostgresStore_Emit_ActorKindSystem(t *testing.T) {
	q := &fakeQuerier{insertReturnID: "1"}
	s := NewPostgresStoreWithQuerier(q)

	_ = s.Emit(context.Background(), Event{
		EventType: "auth.login.failed",
	})
	if q.lastArgs[2] != "system" {
		t.Errorf("actor_kind: got %v want system", q.lastArgs[2])
	}
}

func TestPostgresStore_Emit_HonorsValidActorKindOverride(t *testing.T) {
	// Caller can promote a no-actor event to "system" via metadata.
	// This is the documented override path; it must keep working.
	q := &fakeQuerier{insertReturnID: "1"}
	s := NewPostgresStoreWithQuerier(q)
	err := s.Emit(context.Background(), Event{
		EventType: "internal.cron.tick",
		Metadata:  map[string]any{"actor_kind": "system"},
	})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if q.lastArgs[2] != "system" {
		t.Errorf("actor_kind: got %v want system", q.lastArgs[2])
	}
}

func TestPostgresStore_Emit_RejectsUnknownActorKindOverride(t *testing.T) {
	// A plugin author who passes an arbitrary string into the
	// Metadata["actor_kind"] slot must NOT be able to write it into
	// the column. Reject with ErrInvalidEvent.
	q := &fakeQuerier{insertReturnID: "1"}
	s := NewPostgresStoreWithQuerier(q)
	err := s.Emit(context.Background(), Event{
		EventType: "plugin.x.y",
		Metadata:  map[string]any{"actor_kind": "root"},
	})
	if !errors.Is(err, ErrInvalidEvent) {
		t.Errorf("expected ErrInvalidEvent, got %v", err)
	}
	if q.lastSQL != "" {
		t.Errorf("INSERT should not have been issued for invalid actor_kind, got SQL: %s", q.lastSQL)
	}
}

func TestPostgresStore_Emit_RejectsEmptyOverrideAsFallbackToSystem(t *testing.T) {
	// An empty string in the override slot is not "user-supplied
	// garbage" — it's effectively no override. The store falls back
	// to "system" rather than erroring.
	q := &fakeQuerier{insertReturnID: "1"}
	s := NewPostgresStoreWithQuerier(q)
	err := s.Emit(context.Background(), Event{
		EventType: "internal.x",
		Metadata:  map[string]any{"actor_kind": ""},
	})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if q.lastArgs[2] != "system" {
		t.Errorf("actor_kind: got %v want system", q.lastArgs[2])
	}
}

func TestIsValidActorKind(t *testing.T) {
	for _, ok := range []string{"user", "plugin", "system"} {
		if !isValidActorKind(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"", "root", "USER", "User", "robot", "anonymous"} {
		if isValidActorKind(bad) {
			t.Errorf("%q should be invalid", bad)
		}
	}
}

func TestPostgresStore_Emit_MetadataDefaultsToObject(t *testing.T) {
	q := &fakeQuerier{insertReturnID: "1"}
	s := NewPostgresStoreWithQuerier(q)

	_ = s.Emit(context.Background(), Event{EventType: "x.y"})
	got, ok := q.lastArgs[9].([]byte)
	if !ok {
		t.Fatalf("metadata arg type: %T", q.lastArgs[9])
	}
	if string(got) != "{}" {
		t.Errorf("metadata default: got %s want {}", got)
	}
}

func TestPostgresStore_Emit_PropagatesError(t *testing.T) {
	want := errors.New("connection refused")
	q := &fakeQuerier{insertErr: want}
	s := NewPostgresStoreWithQuerier(q)
	err := s.Emit(context.Background(), Event{EventType: "x.y"})
	if !errors.Is(err, want) {
		t.Errorf("expected wrapped %v, got %v", want, err)
	}
}

func TestPostgresStore_Emit_RejectsInvalid(t *testing.T) {
	s := NewPostgresStoreWithQuerier(&fakeQuerier{})
	err := s.Emit(context.Background(), Event{})
	if !errors.Is(err, ErrInvalidEvent) {
		t.Errorf("expected ErrInvalidEvent, got %v", err)
	}
}

func TestPostgresStore_List_ClampsLimit(t *testing.T) {
	q := &fakeQuerier{queryRows: emptyRows{}}
	s := NewPostgresStoreWithQuerier(q)

	_, err := s.List(context.Background(), Filter{Limit: postgresMaxLimit + 5000})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got := q.lastArgs[len(q.lastArgs)-1].(int); got != postgresMaxLimit {
		t.Errorf("limit not clamped: got %d want %d", got, postgresMaxLimit)
	}
}

func TestPostgresStore_List_ZeroLimitUsesDefault(t *testing.T) {
	q := &fakeQuerier{queryRows: emptyRows{}}
	s := NewPostgresStoreWithQuerier(q)

	_, err := s.List(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got := q.lastArgs[len(q.lastArgs)-1].(int); got != postgresDefaultLimit {
		t.Errorf("default limit: got %d want %d", got, postgresDefaultLimit)
	}
}

// emptyRows is a pgx.Rows that yields nothing.
type emptyRows struct{}

func (emptyRows) Close()                                       {}
func (emptyRows) Err() error                                   { return nil }
func (emptyRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (emptyRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (emptyRows) Next() bool                                   { return false }
func (emptyRows) Scan(...any) error                            { return errors.New("no rows") }
func (emptyRows) Values() ([]any, error)                       { return nil, errors.New("no rows") }
func (emptyRows) RawValues() [][]byte                          { return nil }
func (emptyRows) Conn() *pgx.Conn                              { return nil }

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
