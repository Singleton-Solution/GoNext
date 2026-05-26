package hooks

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestApplyBatch_NoHandlers returns the input slice untouched and no
// error — symmetric with ApplyFilters on a name nothing subscribes to.
func TestApplyBatch_NoHandlers(t *testing.T) {
	bus, _ := newTestBus(t)
	in := []any{"a", "b", "c"}
	out, err := bus.ApplyBatch(context.Background(), "noone.subscribes", in)
	if err != nil {
		t.Fatalf("ApplyBatch: %v", err)
	}
	if len(out) != 3 || out[0] != "a" || out[1] != "b" || out[2] != "c" {
		t.Errorf("out: got %v want [a b c]", out)
	}
}

// TestApplyBatch_BatchAwareHandler routes the whole slice through a
// BatchFilterHandler in one call.
func TestApplyBatch_BatchAwareHandler(t *testing.T) {
	bus, _ := newTestBus(t)
	var callCount int
	bus.RegisterBatchFilter("titles", 10, func(ctx context.Context, items []any, args ...any) ([]any, error) {
		callCount++
		out := make([]any, len(items))
		for i, v := range items {
			out[i] = strings.ToUpper(v.(string))
		}
		return out, nil
	})

	out, err := bus.ApplyBatch(context.Background(), "titles", []any{"hello", "world", "foo"})
	if err != nil {
		t.Fatalf("ApplyBatch: %v", err)
	}
	if callCount != 1 {
		t.Errorf("batch handler invocation count: got %d want 1", callCount)
	}
	if out[0] != "HELLO" || out[1] != "WORLD" || out[2] != "FOO" {
		t.Errorf("out: got %v", out)
	}
}

// TestApplyBatch_LegacyFilterHandler loops a plain FilterHandler over
// each item so existing subscribers keep working inside a batched chain.
func TestApplyBatch_LegacyFilterHandler(t *testing.T) {
	bus, _ := newTestBus(t)
	var perItem int
	bus.RegisterFilter("titles", 10, func(ctx context.Context, value any, args ...any) (any, error) {
		perItem++
		return value.(string) + "!", nil
	})

	out, err := bus.ApplyBatch(context.Background(), "titles", []any{"a", "b", "c"})
	if err != nil {
		t.Fatalf("ApplyBatch: %v", err)
	}
	if perItem != 3 {
		t.Errorf("legacy filter calls: got %d want 3", perItem)
	}
	if out[0] != "a!" || out[1] != "b!" || out[2] != "c!" {
		t.Errorf("out: got %v", out)
	}
}

// TestApplyBatch_MixedChain interleaves a batch-aware handler with a
// legacy per-item one. Priorities are respected.
func TestApplyBatch_MixedChain(t *testing.T) {
	bus, _ := newTestBus(t)
	var batchCalls, legacyCalls int

	bus.RegisterFilter("mix", 20, func(ctx context.Context, value any, args ...any) (any, error) {
		legacyCalls++
		return value.(string) + ".legacy", nil
	})
	bus.RegisterBatchFilter("mix", 10, func(ctx context.Context, items []any, args ...any) ([]any, error) {
		batchCalls++
		out := make([]any, len(items))
		for i, v := range items {
			out[i] = v.(string) + ".batch"
		}
		return out, nil
	})

	out, err := bus.ApplyBatch(context.Background(), "mix", []any{"x", "y"})
	if err != nil {
		t.Fatalf("ApplyBatch: %v", err)
	}
	if batchCalls != 1 {
		t.Errorf("batch calls: got %d want 1", batchCalls)
	}
	if legacyCalls != 2 {
		t.Errorf("legacy calls: got %d want 2", legacyCalls)
	}
	if out[0] != "x.batch.legacy" || out[1] != "y.batch.legacy" {
		t.Errorf("out: got %v", out)
	}
}

// TestApplyBatch_LengthMismatchIgnored rejects a slice that changed
// length: the previous accepted value carries forward.
func TestApplyBatch_LengthMismatchIgnored(t *testing.T) {
	bus, _ := newTestBus(t)
	bus.RegisterBatchFilter("buggy", 10, func(ctx context.Context, items []any, args ...any) ([]any, error) {
		// Drop the last item — a contract violation.
		return items[:len(items)-1], nil
	})

	in := []any{"a", "b", "c"}
	out, err := bus.ApplyBatch(context.Background(), "buggy", in)
	if err != nil {
		t.Fatalf("ApplyBatch: %v", err)
	}
	if len(out) != 3 {
		t.Errorf("output length: got %d want 3 (mismatch should be ignored)", len(out))
	}
}

// TestApplyBatch_ShortCircuit stops the chain with the value-so-far.
func TestApplyBatch_ShortCircuit(t *testing.T) {
	bus, _ := newTestBus(t)
	bus.RegisterBatchFilter("sc", 10, func(ctx context.Context, items []any, args ...any) ([]any, error) {
		out := make([]any, len(items))
		for i, v := range items {
			out[i] = "stopped-" + v.(string)
		}
		return out, ErrShortCircuit
	})
	bus.RegisterBatchFilter("sc", 20, func(ctx context.Context, items []any, args ...any) ([]any, error) {
		t.Errorf("downstream handler ran after short-circuit")
		return items, nil
	})

	out, err := bus.ApplyBatch(context.Background(), "sc", []any{"a", "b"})
	if err != nil {
		t.Fatalf("ApplyBatch: %v", err)
	}
	if out[0] != "stopped-a" || out[1] != "stopped-b" {
		t.Errorf("out: got %v", out)
	}
}

// TestApplyBatch_HandlerError stops the chain with the last accepted
// slice and surfaces the error.
func TestApplyBatch_HandlerError(t *testing.T) {
	bus, _ := newTestBus(t)
	bus.RegisterBatchFilter("err", 10, func(ctx context.Context, items []any, args ...any) ([]any, error) {
		out := make([]any, len(items))
		for i, v := range items {
			out[i] = "first-" + v.(string)
		}
		return out, nil
	})
	want := errors.New("blew up")
	bus.RegisterBatchFilter("err", 20, func(ctx context.Context, items []any, args ...any) ([]any, error) {
		return items, want
	})

	out, err := bus.ApplyBatch(context.Background(), "err", []any{"a"})
	if !errors.Is(err, want) {
		t.Errorf("err: got %v want %v", err, want)
	}
	// last-accepted value is the first handler's output.
	if out[0] != "first-a" {
		t.Errorf("out: got %v want [first-a]", out)
	}
}

// BenchmarkApplyBatch_BatchAware vs BenchmarkApplyFilters_PerItem
// quantify the hot-path improvement issue #263 targets. The batch case
// dispatches the chain once with the whole slice; the per-item case
// calls ApplyFilters N times.

func BenchmarkApplyFilters_PerItem(b *testing.B) {
	bus := NewBus()
	bus.RegisterFilter("bench", 10, func(ctx context.Context, value any, args ...any) (any, error) {
		return value.(int) + 1, nil
	})
	const N = 100
	items := make([]int, N)
	for i := range items {
		items[i] = i
	}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, x := range items {
			_, _ = bus.ApplyFilters(ctx, "bench", x)
		}
	}
}

func BenchmarkApplyBatch_BatchAware(b *testing.B) {
	bus := NewBus()
	bus.RegisterBatchFilter("bench", 10, func(ctx context.Context, in []any, args ...any) ([]any, error) {
		out := make([]any, len(in))
		for i, v := range in {
			out[i] = v.(int) + 1
		}
		return out, nil
	})
	const N = 100
	items := make([]any, N)
	for i := range items {
		items[i] = i
	}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = bus.ApplyBatch(ctx, "bench", items)
	}
}

func BenchmarkApplyBatch_LegacyHandler(b *testing.B) {
	bus := NewBus()
	bus.RegisterFilter("bench", 10, func(ctx context.Context, value any, args ...any) (any, error) {
		return value.(int) + 1, nil
	})
	const N = 100
	items := make([]any, N)
	for i := range items {
		items[i] = i
	}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = bus.ApplyBatch(ctx, "bench", items)
	}
}

// Self-test: errors.New + fmt.Errorf identity preserved.
var _ = fmt.Errorf
