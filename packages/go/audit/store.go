package audit

import (
	"context"
	"errors"
	"time"
)

// ErrInvalidEvent is returned by Store.Emit when the event is structurally
// invalid (empty EventType, unknown Severity). It's intentionally distinct
// from a transport error so callers can decide whether to retry.
var ErrInvalidEvent = errors.New("audit: invalid event")

// Filter narrows a Store.List query. All fields are optional; the zero
// value matches every event in the store (subject to a sensible default
// row cap on the store side, to avoid accidentally streaming the whole
// table back to a curious admin).
//
// Times are inclusive bounds. If both Start and End are zero, the time
// range is unbounded.
type Filter struct {
	Start time.Time
	End   time.Time

	// ActorUserID matches events emitted on behalf of this user. Pair
	// with PluginSlug if you want "the user, but only when a specific
	// plugin was acting" — both must match.
	ActorUserID string

	// PluginSlug matches events emitted by a specific plugin. Empty
	// matches "no plugin involved" — see implementations for exact
	// semantics; the common case (empty filter = match anything) is
	// honored.
	PluginSlug string

	// EventType is an exact match against Event.EventType. Wildcards
	// are not supported in v1 — if you want all auth.* events, filter
	// client-side.
	EventType string

	// Severity, when non-empty, matches the event severity exactly.
	Severity Severity

	// Limit caps the result set. Zero means "store default" (typically
	// 100). Implementations may impose a hard maximum (e.g. 1000) to
	// keep an admin UI honest.
	Limit int
}

// Store persists audit events.
//
// Emit MUST be safe to call from many goroutines. It SHOULD not block on
// network I/O long enough to dominate request latency — implementations
// are expected to either be local (MemoryStore) or backed by a fast
// connection pool (PostgresStore). If durability is paramount, callers
// should wrap their own retry / outbox around Emit; the package does not
// internalize that policy because the right answer is operator-specific.
//
// List returns the most recent events first (occurred_at DESC), capped
// at filter.Limit (or the store's default cap if zero). Implementations
// honor the Filter as best they can; unsupported filter fields are
// ignored rather than failing the request, but the godoc on each store
// notes its specifics.
type Store interface {
	Emit(ctx context.Context, e Event) error
	List(ctx context.Context, f Filter) ([]Event, error)
}

// validateForEmit performs the cheap structural checks every store runs
// before persisting. Returning ErrInvalidEvent here lets callers
// distinguish "you sent me garbage" from "the database is down".
func validateForEmit(e Event) error {
	if e.EventType == "" {
		return errors.Join(ErrInvalidEvent, errors.New("EventType is required"))
	}
	if e.Severity != "" && !e.Severity.Valid() {
		return errors.Join(ErrInvalidEvent, errors.New("unknown Severity: "+string(e.Severity)))
	}
	return nil
}
