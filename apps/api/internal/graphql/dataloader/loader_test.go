package dataloader_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/graphql/dataloader"
)

// TestFromContextNilContext: passing a nil context returns nil.
// The resolver-side code falls back to the direct repo path in this
// case rather than panicking.
func TestFromContextNilContext(t *testing.T) {
	t.Parallel()
	//nolint:staticcheck // SA1012 — explicitly testing the nil-context contract.
	if got := dataloader.FromContext(nil); got != nil {
		t.Fatalf("nil ctx: got %+v, want nil", got)
	}
}

// TestFromContextMissing: a ctx without an attached Loaders returns
// nil. Same fallback contract as nil-ctx.
func TestFromContextMissing(t *testing.T) {
	t.Parallel()
	if got := dataloader.FromContext(context.Background()); got != nil {
		t.Fatalf("missing: got %+v, want nil", got)
	}
}

// TestAttachAndLoad: round-trip — attach a loader, retrieve it, load
// a single key, verify the batch fn was called.
func TestAttachAndLoad(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	loaders := dataloader.New(func(_ context.Context, ids []string) ([]*dataloader.UserRow, error) {
		calls.Add(1)
		out := make([]*dataloader.UserRow, len(ids))
		for i, id := range ids {
			out[i] = &dataloader.UserRow{ID: id, Handle: "h_" + id, Email: "e_" + id}
		}
		return out, nil
	})
	ctx := dataloader.Attach(context.Background(), loaders)
	got := dataloader.FromContext(ctx)
	if got != loaders {
		t.Fatalf("FromContext returned different loader: %p vs %p", got, loaders)
	}
	row, err := got.UserByID.Load(ctx, "u1")()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if row == nil || row.ID != "u1" {
		t.Fatalf("load result wrong: %+v", row)
	}
}

// TestBatchCoalesces: concurrent Loads for distinct keys coalesce
// into a single batch call. This is the property the N+1 test in
// the resolvers package also exercises, but at the loader level so
// failures here are easier to localise.
func TestBatchCoalesces(t *testing.T) {
	t.Parallel()
	var batchCalls atomic.Int32
	var lastBatch struct {
		mu sync.Mutex
		v  []string
	}
	loaders := dataloader.New(func(_ context.Context, ids []string) ([]*dataloader.UserRow, error) {
		batchCalls.Add(1)
		lastBatch.mu.Lock()
		lastBatch.v = append([]string{}, ids...)
		lastBatch.mu.Unlock()
		out := make([]*dataloader.UserRow, len(ids))
		for i, id := range ids {
			out[i] = &dataloader.UserRow{ID: id}
		}
		return out, nil
	})
	ctx := dataloader.Attach(context.Background(), loaders)

	// Fan out N loads concurrently, then wait.
	const N = 8
	thunks := make([]func() (*dataloader.UserRow, error), N)
	for i := 0; i < N; i++ {
		id := "u" + itoa(i)
		thunks[i] = loaders.UserByID.Load(ctx, id)
	}
	for i, th := range thunks {
		row, err := th()
		if err != nil {
			t.Fatalf("load %d: %v", i, err)
		}
		if row == nil || row.ID != "u"+itoa(i) {
			t.Fatalf("load %d: wrong row %+v", i, row)
		}
	}
	if got := batchCalls.Load(); got != 1 {
		t.Errorf("batch call count: got %d, want 1 (loads did not coalesce)", got)
	}
}

// itoa is the local copy to keep the test package import-free.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
