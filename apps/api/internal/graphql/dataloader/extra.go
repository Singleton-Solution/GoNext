// extra.go extends the per-request Loaders bundle with additional
// resolver-specific batchers. The existing UserByID covers Post.author;
// the entries below are the rest of the resolvers that fan out to the
// persistence layer — each one a candidate for an N+1 explosion if a
// query selects many parents.
//
// The pattern across every loader is identical:
//
//   1. The persistence interface exposes a ByIDs([]string) batch fn.
//   2. The loader wraps that fn in a graph-gophers/dataloader v7
//      BatchedLoader keyed by string id.
//   3. Resolvers pull the loader via FromContext and call .Load()
//      (which returns a thunk; the thunk dispatches the batch once
//      the gqlgen tick completes).
//
// What's NOT in this file: cross-tenant or cross-request caching.
// Every loader is built fresh per request by New(); reuse across
// requests would break tenant isolation.
package dataloader

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/graph-gophers/dataloader/v7"
)

// TermRow is the dataloader's local term shape. Mirrors the resolver
// package's term type but duplicated to avoid a dependency cycle —
// see UserRow for the same rationale.
type TermRow struct {
	ID       string
	Slug     string
	Name     string
	Taxonomy string
}

// MediaRow is the dataloader's local media shape. Same duplication
// rationale as TermRow.
type MediaRow struct {
	ID       string
	Filename string
	MimeType string
	URL      string
}

// TermBatchFn returns rows in input order with nil entries for
// missing ids — same contract as UserBatchFn.
type TermBatchFn func(ctx context.Context, ids []string) ([]*TermRow, error)

// MediaBatchFn returns rows in input order with nil entries for
// missing ids.
type MediaBatchFn func(ctx context.Context, ids []string) ([]*MediaRow, error)

// TermsByPostFn returns the terms attached to a slice of post ids.
// The slice-of-slices return shape lets a single SQL JOIN service
// every parent's request in one round trip.
type TermsByPostFn func(ctx context.Context, postIDs []string) ([][]*TermRow, error)

// counters is the per-request loader-call observability shim. Each
// .Load call increments the matching counter so a benchmark or a
// CI budget check can assert that the loader was actually used
// (i.e. that the resolver fan-out coalesced).
//
// All counters are atomic so the benchmark can read them from a
// different goroutine than the one driving the request.
type counters struct {
	UserCalls         atomic.Int64
	UserBatchCalls    atomic.Int64
	TermCalls         atomic.Int64
	TermBatchCalls    atomic.Int64
	MediaCalls        atomic.Int64
	MediaBatchCalls   atomic.Int64
	TermsByPostCalls  atomic.Int64
	TermsByPostBatch  atomic.Int64
}

// Snapshot returns a read-only copy of the current counter values.
// Useful for benchmark assertions and budget enforcement.
type Snapshot struct {
	UserCalls         int64
	UserBatchCalls    int64
	TermCalls         int64
	TermBatchCalls    int64
	MediaCalls        int64
	MediaBatchCalls   int64
	TermsByPostCalls  int64
	TermsByPostBatch  int64
}

// Snapshot returns the current counter values without resetting them.
func (l *Loaders) Snapshot() Snapshot {
	if l == nil || l.counters == nil {
		return Snapshot{}
	}
	return Snapshot{
		UserCalls:         l.counters.UserCalls.Load(),
		UserBatchCalls:    l.counters.UserBatchCalls.Load(),
		TermCalls:         l.counters.TermCalls.Load(),
		TermBatchCalls:    l.counters.TermBatchCalls.Load(),
		MediaCalls:        l.counters.MediaCalls.Load(),
		MediaBatchCalls:   l.counters.MediaBatchCalls.Load(),
		TermsByPostCalls:  l.counters.TermsByPostCalls.Load(),
		TermsByPostBatch:  l.counters.TermsByPostBatch.Load(),
	}
}

// NewExtended builds a Loaders bundle with every batcher wired. The
// existing New() builds only UserByID for backward compat; callers
// that want the full set call NewExtended.
func NewExtended(loadUsers UserBatchFn, loadTerms TermBatchFn, loadMedia MediaBatchFn, loadTermsByPost TermsByPostFn) *Loaders {
	c := &counters{}

	userLoader := dataloader.NewBatchedLoader[string, *UserRow](func(ctx context.Context, ids []string) []*dataloader.Result[*UserRow] {
		c.UserBatchCalls.Add(1)
		return userBatcherResults(ctx, ids, loadUsers)
	})
	termLoader := dataloader.NewBatchedLoader[string, *TermRow](func(ctx context.Context, ids []string) []*dataloader.Result[*TermRow] {
		c.TermBatchCalls.Add(1)
		return termBatcherResults(ctx, ids, loadTerms)
	})
	mediaLoader := dataloader.NewBatchedLoader[string, *MediaRow](func(ctx context.Context, ids []string) []*dataloader.Result[*MediaRow] {
		c.MediaBatchCalls.Add(1)
		return mediaBatcherResults(ctx, ids, loadMedia)
	})
	termsByPostLoader := dataloader.NewBatchedLoader[string, []*TermRow](func(ctx context.Context, ids []string) []*dataloader.Result[[]*TermRow] {
		c.TermsByPostBatch.Add(1)
		return termsByPostBatcherResults(ctx, ids, loadTermsByPost)
	})

	return &Loaders{
		UserByID:        userLoader,
		TermByID:        termLoader,
		MediaByID:       mediaLoader,
		TermsByPostID:   termsByPostLoader,
		counters:        c,
	}
}

// --- batch result builders. Each one validates the upstream contract
// (input/output slice length match) so a programmer error fails
// loudly inside one request rather than scrambling across all of
// them.

func userBatcherResults(ctx context.Context, ids []string, fn UserBatchFn) []*dataloader.Result[*UserRow] {
	rows, err := fn(ctx, ids)
	out := make([]*dataloader.Result[*UserRow], len(ids))
	if err != nil {
		for i := range ids {
			out[i] = &dataloader.Result[*UserRow]{Error: err}
		}
		return out
	}
	if len(rows) != len(ids) {
		err = fmt.Errorf("user batch: expected %d rows, got %d", len(ids), len(rows))
		for i := range ids {
			out[i] = &dataloader.Result[*UserRow]{Error: err}
		}
		return out
	}
	for i, row := range rows {
		out[i] = &dataloader.Result[*UserRow]{Data: row}
	}
	return out
}

func termBatcherResults(ctx context.Context, ids []string, fn TermBatchFn) []*dataloader.Result[*TermRow] {
	rows, err := fn(ctx, ids)
	out := make([]*dataloader.Result[*TermRow], len(ids))
	if err != nil {
		for i := range ids {
			out[i] = &dataloader.Result[*TermRow]{Error: err}
		}
		return out
	}
	if len(rows) != len(ids) {
		err = fmt.Errorf("term batch: expected %d rows, got %d", len(ids), len(rows))
		for i := range ids {
			out[i] = &dataloader.Result[*TermRow]{Error: err}
		}
		return out
	}
	for i, row := range rows {
		out[i] = &dataloader.Result[*TermRow]{Data: row}
	}
	return out
}

func mediaBatcherResults(ctx context.Context, ids []string, fn MediaBatchFn) []*dataloader.Result[*MediaRow] {
	rows, err := fn(ctx, ids)
	out := make([]*dataloader.Result[*MediaRow], len(ids))
	if err != nil {
		for i := range ids {
			out[i] = &dataloader.Result[*MediaRow]{Error: err}
		}
		return out
	}
	if len(rows) != len(ids) {
		err = fmt.Errorf("media batch: expected %d rows, got %d", len(ids), len(rows))
		for i := range ids {
			out[i] = &dataloader.Result[*MediaRow]{Error: err}
		}
		return out
	}
	for i, row := range rows {
		out[i] = &dataloader.Result[*MediaRow]{Data: row}
	}
	return out
}

func termsByPostBatcherResults(ctx context.Context, postIDs []string, fn TermsByPostFn) []*dataloader.Result[[]*TermRow] {
	rows, err := fn(ctx, postIDs)
	out := make([]*dataloader.Result[[]*TermRow], len(postIDs))
	if err != nil {
		for i := range postIDs {
			out[i] = &dataloader.Result[[]*TermRow]{Error: err}
		}
		return out
	}
	if len(rows) != len(postIDs) {
		err = fmt.Errorf("terms-by-post batch: expected %d rows, got %d", len(postIDs), len(rows))
		for i := range postIDs {
			out[i] = &dataloader.Result[[]*TermRow]{Error: err}
		}
		return out
	}
	for i, row := range rows {
		out[i] = &dataloader.Result[[]*TermRow]{Data: row}
	}
	return out
}
