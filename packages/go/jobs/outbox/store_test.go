package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

// fakeRow is a tiny pgx.Row stub.
type fakeRow struct {
	scan func(dest ...any) error
}

func (r *fakeRow) Scan(dest ...any) error { return r.scan(dest...) }

// fakeTx records the SQL and args of QueryRow calls and returns a
// canned row. Lets us assert the INSERT exactly without spinning up a
// container for unit-level coverage.
type fakeTx struct {
	lastSQL  string
	lastArgs []any
	returnID int64
	scanErr  error
}

func (f *fakeTx) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	f.lastSQL = sql
	f.lastArgs = args
	return &fakeRow{scan: func(dest ...any) error {
		if f.scanErr != nil {
			return f.scanErr
		}
		if len(dest) != 1 {
			return errors.New("fakeTx: unexpected dest count")
		}
		out, ok := dest[0].(*int64)
		if !ok {
			return errors.New("fakeTx: dest not *int64")
		}
		*out = f.returnID
		return nil
	}}
}

func TestStore_Write_HappyPath(t *testing.T) {
	tx := &fakeTx{returnID: 42}
	s := NewStore()

	id, err := s.Write(context.Background(), tx, Entry{
		TaskName: "email.send",
		Queue:    "default",
		Payload:  map[string]any{"to": "alice@example.com"},
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if id != 42 {
		t.Errorf("id: got %d want 42", id)
	}
	if !contains(tx.lastSQL, "INSERT INTO outbox") {
		t.Errorf("SQL missing INSERT: %s", tx.lastSQL)
	}
	if len(tx.lastArgs) != 3 {
		t.Fatalf("args: got %d want 3", len(tx.lastArgs))
	}
	if tx.lastArgs[0] != "email.send" {
		t.Errorf("task_name arg: got %v", tx.lastArgs[0])
	}
	if tx.lastArgs[2] != "default" {
		t.Errorf("queue arg: got %v", tx.lastArgs[2])
	}
	// payload is the second arg and must be valid JSON.
	pb, ok := tx.lastArgs[1].([]byte)
	if !ok {
		t.Fatalf("payload arg type: %T", tx.lastArgs[1])
	}
	if !json.Valid(pb) {
		t.Errorf("payload arg is not valid JSON: %s", pb)
	}
}

func TestStore_Write_RejectsInvalidEntry(t *testing.T) {
	s := NewStore()
	tx := &fakeTx{}

	cases := []struct {
		name string
		e    Entry
	}{
		{"empty task name", Entry{TaskName: "", Queue: "default", Payload: 1}},
		{"empty queue", Entry{TaskName: "x", Queue: "", Payload: 1}},
		{"nil payload", Entry{TaskName: "x", Queue: "default", Payload: nil}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.Write(context.Background(), tx, tc.e)
			if !errors.Is(err, ErrInvalidEntry) {
				t.Errorf("expected ErrInvalidEntry, got %v", err)
			}
			if tx.lastSQL != "" {
				t.Errorf("INSERT should not have been issued for invalid entry, got: %s", tx.lastSQL)
			}
		})
	}
}

func TestStore_Write_NilTx(t *testing.T) {
	s := NewStore()
	_, err := s.Write(context.Background(), nil, Entry{
		TaskName: "x", Queue: "y", Payload: 1,
	})
	if err == nil || !contains(err.Error(), "tx is nil") {
		t.Errorf("expected nil-tx error, got %v", err)
	}
}

func TestStore_Write_RespectsRawMessage(t *testing.T) {
	// A caller forwarding a payload it received elsewhere shouldn't
	// have it double-encoded. json.RawMessage passes through verbatim.
	tx := &fakeTx{returnID: 1}
	s := NewStore()
	raw := json.RawMessage(`{"already":"encoded"}`)
	_, err := s.Write(context.Background(), tx, Entry{
		TaskName: "x", Queue: "y", Payload: raw,
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	pb, ok := tx.lastArgs[1].([]byte)
	if !ok {
		t.Fatalf("payload arg type: %T", tx.lastArgs[1])
	}
	if string(pb) != string(raw) {
		t.Errorf("raw message altered: got %s want %s", pb, raw)
	}
}

func TestStore_Write_RejectsInvalidJSONBytes(t *testing.T) {
	tx := &fakeTx{}
	s := NewStore()
	_, err := s.Write(context.Background(), tx, Entry{
		TaskName: "x", Queue: "y", Payload: []byte("not json"),
	})
	if err == nil || !contains(err.Error(), "valid JSON") {
		t.Errorf("expected invalid-JSON error, got %v", err)
	}
}

func TestStore_Write_PropagatesScanError(t *testing.T) {
	tx := &fakeTx{scanErr: errors.New("boom")}
	s := NewStore()
	_, err := s.Write(context.Background(), tx, Entry{
		TaskName: "x", Queue: "y", Payload: 1,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !contains(err.Error(), "boom") {
		t.Errorf("error should wrap underlying: got %v", err)
	}
}

func TestValidateEntry(t *testing.T) {
	ok := Entry{TaskName: "x", Queue: "y", Payload: 1}
	if err := validateEntry(ok); err != nil {
		t.Errorf("valid entry rejected: %v", err)
	}
	for _, bad := range []Entry{
		{Queue: "y", Payload: 1},
		{TaskName: "x", Payload: 1},
		{TaskName: "x", Queue: "y"},
	} {
		if err := validateEntry(bad); !errors.Is(err, ErrInvalidEntry) {
			t.Errorf("expected ErrInvalidEntry for %+v, got %v", bad, err)
		}
	}
}

// contains is a tiny strings.Contains stand-in so the test file
// doesn't grow a strings import beside json/errors.
func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
