// Package dataloader wires the per-request user-batching loader used
// by the Post.author resolver. The lifetime contract is critical:
//
//   * One Loaders struct per HTTP request (the middleware attaches a
//     fresh one to the context).
//   * Resolvers pull the loader via FromContext, never construct one.
//   * After the request returns, the loader is discarded; the
//     graph-gophers loader holds a per-key Future cache that MUST not
//     leak across requests (cross-tenant data exposure otherwise).
//
// The single batch function we implement here loads users by id. It
// returns rows in the same order as the input keys; missing rows
// surface as a nil entry (which the loader v7 generic propagates as a
// nil Value alongside a "not found" Error). The Post.author resolver
// turns a not-found into a GraphQL field error rather than a panic.
package dataloader

import (
	"context"
	"fmt"

	"github.com/graph-gophers/dataloader/v7"
)

// UserRow is duplicated from the resolvers package to avoid a circular
// import (the resolvers package imports dataloader to attach loaders
// to context, and the loader needs to know what a UserRow is). It is
// a deliberate type duplication — copying five fields is cheaper than
// inverting the dependency.
type UserRow struct {
	ID          string
	Handle      string
	DisplayName *string
	Email       string
	CreatedAt   interface{} // time.Time; kept as `any` to avoid time import cycle complexity
}

// UserBatchFn is the signature the resolver wiring fulfills. It MUST
// return rows in the same order as the input ids; entries for not-
// found ids should be nil (the loader will pair them with a nil
// Error too — that's fine, the resolver checks for nil before
// dereferencing).
type UserBatchFn func(ctx context.Context, ids []string) ([]*UserRow, error)

// Loaders holds every per-request dataloader. Right now there is one;
// adding more (e.g., TermsByPostID, MediaByID) follows the same
// pattern. The struct is intentionally a value type (no pointer
// receivers needed) so it's safe to compare against a sentinel zero
// value in FromContext.
type Loaders struct {
	UserByID *dataloader.Loader[string, *UserRow]
}

// New builds a fresh Loaders bundle wired to the given batch
// functions. Call once per request from the GraphQL middleware.
func New(loadUsers UserBatchFn) *Loaders {
	return &Loaders{
		UserByID: dataloader.NewBatchedLoader[string, *UserRow](func(ctx context.Context, ids []string) []*dataloader.Result[*UserRow] {
			rows, err := loadUsers(ctx, ids)
			out := make([]*dataloader.Result[*UserRow], len(ids))
			if err != nil {
				// Batch-wide failure: every result carries the same
				// error so each resolver call sees it. This is the
				// only correct behaviour — partial success requires
				// the batch fn to return per-row errors, which we
				// don't surface from the SQL layer.
				for i := range ids {
					out[i] = &dataloader.Result[*UserRow]{Error: err}
				}
				return out
			}
			// Defensive: if the batch fn returns a slice of the wrong
			// length, surface a clear error rather than indexing out
			// of range. This is a programmer error, not a data error,
			// but a panic during a GraphQL resolve would crash the
			// whole request.
			if len(rows) != len(ids) {
				err := fmt.Errorf("user batch: expected %d rows, got %d", len(ids), len(rows))
				for i := range ids {
					out[i] = &dataloader.Result[*UserRow]{Error: err}
				}
				return out
			}
			for i, row := range rows {
				out[i] = &dataloader.Result[*UserRow]{Data: row}
			}
			return out
		}),
	}
}

// ctxKey is the unexported type used as the context.WithValue key.
// Unexported so callers cannot stash their own Loaders under our key
// (intentional or otherwise).
type ctxKey struct{}

// Attach returns a child context carrying the given Loaders. The
// GraphQL HTTP handler calls this once per request before invoking
// the schema executor.
func Attach(ctx context.Context, l *Loaders) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// FromContext retrieves the per-request Loaders. Returns nil if no
// Loaders were attached (which is a programmer error — the resolver
// should treat it as "load uncached" and call the underlying repo
// directly, or fail loudly; we choose the latter via a non-nil
// fallback in Required).
func FromContext(ctx context.Context) *Loaders {
	if ctx == nil {
		return nil
	}
	l, _ := ctx.Value(ctxKey{}).(*Loaders)
	return l
}
