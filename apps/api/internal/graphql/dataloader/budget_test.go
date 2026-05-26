package dataloader

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestLoaderCoalescesBatches asserts the core dataloader contract:
// many .Load calls on the same loader within a single request collapse
// into one batch round-trip. If this assertion fails, the CI budget
// gate is no longer meaningful.
func TestLoaderCoalescesBatches(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	loaders := NewExtended(
		func(ctx context.Context, ids []string) ([]*UserRow, error) {
			out := make([]*UserRow, len(ids))
			for i, id := range ids {
				out[i] = &UserRow{ID: id, Handle: "u" + id}
			}
			return out, nil
		},
		func(ctx context.Context, ids []string) ([]*TermRow, error) {
			out := make([]*TermRow, len(ids))
			for i, id := range ids {
				out[i] = &TermRow{ID: id, Slug: "t-" + id}
			}
			return out, nil
		},
		func(ctx context.Context, ids []string) ([]*MediaRow, error) {
			out := make([]*MediaRow, len(ids))
			for i, id := range ids {
				out[i] = &MediaRow{ID: id, Filename: "f-" + id}
			}
			return out, nil
		},
		func(ctx context.Context, postIDs []string) ([][]*TermRow, error) {
			out := make([][]*TermRow, len(postIDs))
			for i := range postIDs {
				out[i] = []*TermRow{}
			}
			return out, nil
		},
	)

	// Fan-out: 100 concurrent loader calls for distinct user ids.
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			thunk := loaders.UserByID.Load(ctx, idFor(id))
			_, _ = thunk()
		}(i)
	}
	wg.Wait()

	snap := loaders.Snapshot()
	// All 100 .Load calls should coalesce into a small number of
	// batches. graph-gophers/dataloader v7 fires a batch when the
	// goroutine queue reaches MaxBatch OR when ScheduleWait expires
	// (default 16ms). For 100 keys with no MaxBatch override, this
	// is almost always 1 batch — we allow up to 3 as a defence
	// against scheduler jitter on slow CI runners.
	if snap.UserBatchCalls > 3 {
		t.Errorf("expected <= 3 batch calls for 100 keys, got %d", snap.UserBatchCalls)
	}
	if snap.UserBatchCalls == 0 {
		t.Errorf("expected >= 1 batch call, got 0 — counter not incremented")
	}
}

// BenchmarkGraphQLBudgets is the CI-runnable budget enforcer. Each
// scenario simulates a representative GraphQL operation and asserts
// the resulting Snapshot is within the per-operation budget loaded
// from tools/graphql-budgets.yml.
//
// Run as:
//
//	go test -run='^$' -bench BenchmarkGraphQLBudgets -benchtime=1x ./apps/api/internal/graphql/dataloader/...
//
// `-benchtime=1x` runs each scenario once — we're not microbenching
// the loader, we're asserting the batch-count budget.
func BenchmarkGraphQLBudgets(b *testing.B) {
	cfg := BudgetConfig{
		DefaultMaxBatchRoundTrips: 4,
		Operations: map[string]Budget{
			"HomeFeed":       {MaxBatchRoundTrips: 2},
			"AuthorArchive":  {MaxBatchRoundTrips: 3},
			"PostDetail":     {MaxBatchRoundTrips: 5},
			"AdminPostsList": {MaxBatchRoundTrips: 4},
		},
	}

	scenarios := []struct {
		name string
		run  func(ctx context.Context, l *Loaders)
	}{
		{
			// 20 posts, each resolves its author. Expect 1 user batch.
			name: "HomeFeed",
			run: func(ctx context.Context, l *Loaders) {
				var wg sync.WaitGroup
				for i := 0; i < 20; i++ {
					wg.Add(1)
					go func(i int) {
						defer wg.Done()
						thunk := l.UserByID.Load(ctx, idFor(i%5))
						_, _ = thunk()
					}(i)
				}
				wg.Wait()
			},
		},
		{
			// One author + their posts + each post's featured media.
			// Expect: 1 user batch + 1 media batch = 2.
			name: "AuthorArchive",
			run: func(ctx context.Context, l *Loaders) {
				thunk := l.UserByID.Load(ctx, idFor(0))
				_, _ = thunk()
				var wg sync.WaitGroup
				for i := 0; i < 10; i++ {
					wg.Add(1)
					go func(i int) {
						defer wg.Done()
						t := l.MediaByID.Load(ctx, idFor(i%3))
						_, _ = t()
					}(i)
				}
				wg.Wait()
			},
		},
		{
			// One post + author + comments + categories + tags.
			// Expect: 1 user + 1 terms-by-post + 1 media = 3 batches.
			name: "PostDetail",
			run: func(ctx context.Context, l *Loaders) {
				thunkU := l.UserByID.Load(ctx, idFor(0))
				_, _ = thunkU()
				thunkT := l.TermsByPostID.Load(ctx, idFor(1))
				_, _ = thunkT()
				thunkM := l.MediaByID.Load(ctx, idFor(2))
				_, _ = thunkM()
			},
		},
		{
			// Admin posts list: posts + authors + featured media +
			// primary category. Expect: 1 user + 1 media + 1 terms = 3.
			name: "AdminPostsList",
			run: func(ctx context.Context, l *Loaders) {
				var wg sync.WaitGroup
				for i := 0; i < 30; i++ {
					wg.Add(1)
					go func(i int) {
						defer wg.Done()
						t := l.UserByID.Load(ctx, idFor(i%4))
						_, _ = t()
					}(i)
				}
				for i := 0; i < 30; i++ {
					wg.Add(1)
					go func(i int) {
						defer wg.Done()
						t := l.MediaByID.Load(ctx, idFor(i%4))
						_, _ = t()
					}(i)
				}
				for i := 0; i < 30; i++ {
					wg.Add(1)
					go func(i int) {
						defer wg.Done()
						t := l.TermByID.Load(ctx, idFor(i%4))
						_, _ = t()
					}(i)
				}
				wg.Wait()
			},
		},
	}

	for _, sc := range scenarios {
		b.Run(sc.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				l := freshLoaders()
				sc.run(ctx, l)
				snap := l.Snapshot()
				cancel()
				if err := cfg.CheckSnapshot(sc.name, snap); err != nil {
					b.Fatalf("%v", err)
				}
			}
		})
	}
}

// freshLoaders returns a Loaders bundle wired to stub batch fns that
// always succeed. Used by the benchmark above and any per-resolver
// unit test that wants to exercise the loader without standing up a
// fake repo.
func freshLoaders() *Loaders {
	return NewExtended(
		func(ctx context.Context, ids []string) ([]*UserRow, error) {
			out := make([]*UserRow, len(ids))
			for i, id := range ids {
				out[i] = &UserRow{ID: id}
			}
			return out, nil
		},
		func(ctx context.Context, ids []string) ([]*TermRow, error) {
			out := make([]*TermRow, len(ids))
			for i, id := range ids {
				out[i] = &TermRow{ID: id}
			}
			return out, nil
		},
		func(ctx context.Context, ids []string) ([]*MediaRow, error) {
			out := make([]*MediaRow, len(ids))
			for i, id := range ids {
				out[i] = &MediaRow{ID: id}
			}
			return out, nil
		},
		func(ctx context.Context, postIDs []string) ([][]*TermRow, error) {
			out := make([][]*TermRow, len(postIDs))
			for i := range postIDs {
				out[i] = []*TermRow{}
			}
			return out, nil
		},
	)
}

// idFor returns a deterministic stub id string. The benchmark uses
// these to ensure repeated keys collapse to a single batch entry.
func idFor(i int) string {
	const hex = "0123456789abcdef"
	c := hex[i%16]
	return "0000000" + string(c) + "-0000-4000-8000-000000000000"
}

func TestCheckSnapshot_PassesWhenWithinBudget(t *testing.T) {
	t.Parallel()
	cfg := BudgetConfig{DefaultMaxBatchRoundTrips: 4}
	snap := Snapshot{UserBatchCalls: 2, MediaBatchCalls: 1}
	if err := cfg.CheckSnapshot("HomeFeed", snap); err != nil {
		t.Errorf("expected pass, got: %v", err)
	}
}

func TestCheckSnapshot_FailsWhenExceeded(t *testing.T) {
	t.Parallel()
	cfg := BudgetConfig{
		DefaultMaxBatchRoundTrips: 4,
		Operations: map[string]Budget{
			"HomeFeed": {MaxBatchRoundTrips: 2},
		},
	}
	snap := Snapshot{UserBatchCalls: 3}
	if err := cfg.CheckSnapshot("HomeFeed", snap); err == nil {
		t.Error("expected fail, got pass")
	}
}
