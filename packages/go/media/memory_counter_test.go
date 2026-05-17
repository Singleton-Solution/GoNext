package media

import (
	"sync"
	"testing"
)

func TestMemoryCounter_ZeroForUnknown(t *testing.T) {
	c := NewMemoryCounter()
	if got := c.Get("never-incremented"); got != 0 {
		t.Errorf("Get(unknown) = %d, want 0", got)
	}
}

func TestMemoryCounter_IncrementsAndReads(t *testing.T) {
	c := NewMemoryCounter()
	c.Inc("a")
	c.Inc("a")
	c.Inc("b")
	if got := c.Get("a"); got != 2 {
		t.Errorf("Get(a) = %d, want 2", got)
	}
	if got := c.Get("b"); got != 1 {
		t.Errorf("Get(b) = %d, want 1", got)
	}
}

func TestMemoryCounter_Snapshot(t *testing.T) {
	c := NewMemoryCounter()
	c.Inc("x")
	c.Inc("y")
	c.Inc("y")
	c.Inc("y")

	snap := c.Snapshot()
	if snap["x"] != 1 {
		t.Errorf("snap[x] = %d, want 1", snap["x"])
	}
	if snap["y"] != 3 {
		t.Errorf("snap[y] = %d, want 3", snap["y"])
	}

	// Mutating the snapshot should not affect the counter — Snapshot
	// returns a copy.
	snap["y"] = 999
	if c.Get("y") != 3 {
		t.Errorf("mutating snapshot affected counter: Get(y) = %d", c.Get("y"))
	}
}

// TestMemoryCounter_ConcurrentInc fires N goroutines each incrementing
// the same set of keys. The final counts must equal N for each key.
// The -race detector will catch any unsynchronized access.
func TestMemoryCounter_ConcurrentInc(t *testing.T) {
	c := NewMemoryCounter()
	const N = 200
	const K = 5
	keys := []string{"a", "b", "c", "d", "e"}
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			for _, k := range keys {
				c.Inc(k)
			}
		}()
	}
	wg.Wait()
	for _, k := range keys {
		if got := c.Get(k); got != int64(N) {
			t.Errorf("Get(%q) = %d, want %d", k, got, N)
		}
	}
	if len(c.Snapshot()) != K {
		t.Errorf("snapshot has %d keys, want %d", len(c.Snapshot()), K)
	}
}

func TestNopCounter_DoesNotPanic(t *testing.T) {
	// nopCounter is unexported but reachable through a Coalescer
	// constructed with Counter=nil. Inc just must not panic.
	var c Counter = nopCounter{}
	c.Inc("any-name")
}
