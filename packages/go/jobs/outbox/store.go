package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ErrInvalidEntry is returned by Write when the caller passes a
// structurally invalid Entry (missing task_name, queue, or payload).
// Wrapped in the returned error; callers use errors.Is.
var ErrInvalidEntry = errors.New("outbox: invalid entry")

// Entry is one logical "enqueue request" that the handler wants to
// forward to Redis. It corresponds 1:1 with a row in the outbox table.
//
// All three string fields are required:
//
//   - TaskName: the handler/worker name the consumer dispatches on.
//   - Queue: destination queue / stream identifier.
//   - Payload: opaque JSON payload forwarded verbatim to Redis.
//
// Payload is *any* — Store.Write marshals it to JSONB. Callers who
// already have a []byte (i.e. they pre-marshalled) can pass it via
// json.RawMessage; Store.Write will respect the raw bytes rather than
// double-encoding.
type Entry struct {
	TaskName string
	Queue    string
	Payload  any
}

// Tx is the minimal surface Store.Write needs from a database
// transaction. *pgx.Tx and pgxpool.Tx satisfy it; so does a fake in
// tests. We deliberately don't take a *pgx.Tx so the package doesn't
// pin the caller's pgx version more than already required by the
// pgx.Row return type below.
//
// The Exec signature mirrors pgx.Tx.Exec but returns a pgxCommandTag —
// see the small adapter below. Callers in production never construct
// this interface directly; they pass their existing pgx.Tx and the
// type system does the rest.
type Tx interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Store writes outbox entries inside an ambient transaction. It holds
// no state of its own and is therefore safe to use as a singleton.
//
// Production wiring:
//
//	var store outbox.Store
//	tx, _ := pool.Begin(ctx)
//	// ... handler's DB writes ...
//	store.Write(ctx, tx, outbox.Entry{
//	    TaskName: "email.send",
//	    Queue:    "default",
//	    Payload:  map[string]any{"to": "user@example.com"},
//	})
//	tx.Commit(ctx)
//
// If the handler returns an error before Commit, the outbox row
// disappears with the rollback — exactly the property we want.
type Store struct{}

// NewStore returns a ready-to-use Store. There is no configuration;
// constructor exists purely to keep the package's API consistent with
// neighbouring stores (audit, settings, sessions).
func NewStore() *Store { return &Store{} }

// Write inserts e into the outbox table using tx. The transaction's
// commit/rollback determines whether the row persists.
//
// Returns ErrInvalidEntry (wrapped) for malformed entries. Returns a
// wrapped pgx error otherwise. On success, the assigned row id is set
// on the returned entry via the id return value — useful for logging,
// not used in the protocol.
func (s *Store) Write(ctx context.Context, tx Tx, e Entry) (id int64, err error) {
	if err := validateEntry(e); err != nil {
		return 0, err
	}
	if tx == nil {
		return 0, fmt.Errorf("outbox.Store.Write: tx is nil")
	}

	payload, err := marshalPayload(e.Payload)
	if err != nil {
		return 0, fmt.Errorf("outbox.Store.Write: marshal payload: %w", err)
	}

	const q = `
		INSERT INTO outbox (task_name, payload, queue)
		VALUES ($1, $2::jsonb, $3)
		RETURNING id
	`
	if err := tx.QueryRow(ctx, q, e.TaskName, payload, e.Queue).Scan(&id); err != nil {
		return 0, fmt.Errorf("outbox.Store.Write: insert: %w", err)
	}
	return id, nil
}

// validateEntry checks the three required fields. We deliberately
// don't validate the payload's *content* — that's the consumer's
// problem. We only insist that something was supplied.
func validateEntry(e Entry) error {
	if e.TaskName == "" {
		return fmt.Errorf("%w: task_name required", ErrInvalidEntry)
	}
	if e.Queue == "" {
		return fmt.Errorf("%w: queue required", ErrInvalidEntry)
	}
	if e.Payload == nil {
		return fmt.Errorf("%w: payload required", ErrInvalidEntry)
	}
	return nil
}

// marshalPayload converts the caller-supplied payload to a []byte the
// Postgres driver can pass to a JSONB column.
//
// Special-case: if the caller already handed us a []byte or
// json.RawMessage, we trust it as valid JSON and pass it through. This
// matters when the handler is forwarding a payload it received from
// somewhere else — re-marshalling would double-encode it.
func marshalPayload(p any) ([]byte, error) {
	switch v := p.(type) {
	case []byte:
		if !json.Valid(v) {
			return nil, fmt.Errorf("payload is []byte but not valid JSON")
		}
		return v, nil
	case json.RawMessage:
		if !json.Valid(v) {
			return nil, fmt.Errorf("payload is json.RawMessage but not valid JSON")
		}
		return v, nil
	default:
		return json.Marshal(p)
	}
}
