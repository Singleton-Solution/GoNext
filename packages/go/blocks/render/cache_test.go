package render

import (
	"context"
	"html/template"
	"sync"
	"testing"
	"time"
)

// inMemoryBackend is the test stand-in for the production fragment
// cache. It is intentionally NOT version-aware (no tag invalidation):
// the CachedWalker is the unit under test here, and we want every
// missed expectation to be the walker's fault, not a tag glitch.
type inMemoryBackend struct {
	mu      sync.Mutex
	items   map[string][]byte
	getHits int
	getMiss int
	sets    int
}

func newInMemoryBackend() *inMemoryBackend {
	return &inMemoryBackend{items: make(map[string][]byte)}
}

func (b *inMemoryBackend) Get(_ context.Context, key string, _ []string) ([]byte, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	v, ok := b.items[key]
	if !ok {
		b.getMiss++
		return nil, false, nil
	}
	b.getHits++
	return v, true, nil
}

func (b *inMemoryBackend) Set(_ context.Context, key string, value []byte, _ []string, _ time.Duration) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	cp := make([]byte, len(value))
	copy(cp, value)
	b.items[key] = cp
	b.sets++
	return nil
}

// newCachedTestWalker is a small helper that mounts the core block
// renderers and wraps them with the cache backend.
func newCachedTestWalker(t *testing.T, backend CacheBackend) *CachedWalker {
	t.Helper()
	reg := NewRegistry()
	if err := RegisterCoreBlocks(reg); err != nil {
		t.Fatalf("RegisterCoreBlocks: %v", err)
	}
	cw, err := NewCached(reg, CachedWalkerOptions{
		Cache:   backend,
		Version: "test-v1",
	})
	if err != nil {
		t.Fatalf("NewCached: %v", err)
	}
	return cw
}

// TestCachedWalker_RepeatedRender_HitsCache exercises the headline
// acceptance criterion of issue #108: the second render of an
// identical block subtree hits the cache.
func TestCachedWalker_RepeatedRender_HitsCache(t *testing.T) {
	t.Parallel()
	backend := newInMemoryBackend()
	cw := newCachedTestWalker(t, backend)
	ctx := context.Background()

	tree := []Block{
		{
			Type: "core/paragraph",
			Attributes: map[string]any{
				"content": "hello world",
				"align":   "center",
			},
		},
	}

	first := cw.Walk(ctx, tree, nil)
	second := cw.Walk(ctx, tree, nil)

	if first.HTML != second.HTML {
		t.Errorf("renders disagree:\n  first: %q\n  second: %q", first.HTML, second.HTML)
	}
	m := cw.Metrics()
	if m.Hits != 1 {
		t.Errorf("Hits: got %d, want 1", m.Hits)
	}
	if m.Misses != 1 {
		t.Errorf("Misses: got %d, want 1", m.Misses)
	}
	if m.Stores != 1 {
		t.Errorf("Stores: got %d, want 1", m.Stores)
	}
}

// TestCachedWalker_HitRateTarget renders the same tree of 10 blocks
// several times and asserts a hit rate ≥ 80% after warmup — the
// issue's acceptance criterion.
func TestCachedWalker_HitRateTarget(t *testing.T) {
	t.Parallel()
	backend := newInMemoryBackend()
	cw := newCachedTestWalker(t, backend)
	ctx := context.Background()

	tree := make([]Block, 10)
	for i := range tree {
		tree[i] = Block{
			Type: "core/paragraph",
			Attributes: map[string]any{
				"content": "para",
				"i":       float64(i),
			},
		}
	}

	// Six passes: 10 misses + 50 hits = 83% hit rate.
	for i := 0; i < 6; i++ {
		cw.Walk(ctx, tree, nil)
	}

	m := cw.Metrics()
	if rate := m.HitRate(); rate < 0.8 {
		t.Errorf("HitRate after warmup: got %.4f (hits=%d misses=%d), want >= 0.80",
			rate, m.Hits, m.Misses)
	}
}

// TestCachedWalker_BypassesContextBlocks confirms that blocks
// declaring UsesContext or ProvidesContext skip the cache entirely.
// Caching a context-coupled block would require including inherited
// state in the key; the current design takes the safer fallback.
func TestCachedWalker_BypassesContextBlocks(t *testing.T) {
	t.Parallel()
	backend := newInMemoryBackend()

	reg := NewRegistry()
	if err := reg.Register("test/uses-ctx", BlockSpec{
		Render: func(_ Block, _ template.HTML, _ Context) (template.HTML, error) {
			return template.HTML("<x>used</x>"), nil
		},
		UsesContext: []string{"postId"},
	}); err != nil {
		t.Fatalf("Register test/uses-ctx: %v", err)
	}
	cw, err := NewCached(reg, CachedWalkerOptions{
		Cache:   backend,
		Version: "test-v1",
	})
	if err != nil {
		t.Fatalf("NewCached: %v", err)
	}

	tree := []Block{{Type: "test/uses-ctx", Attributes: map[string]any{}}}
	for i := 0; i < 3; i++ {
		cw.Walk(context.Background(), tree, nil)
	}
	m := cw.Metrics()
	if m.Bypasses != 3 {
		t.Errorf("Bypasses: got %d, want 3", m.Bypasses)
	}
	if m.Hits != 0 || m.Misses != 0 {
		t.Errorf("cache should not have been touched: hits=%d misses=%d", m.Hits, m.Misses)
	}
}

// TestCanonicalEncode_KeyStabilityAcrossMapOrder pins the key-
// stability property: two blocks built from the same attributes but
// in different map-insertion order MUST produce the same cache key.
// This is the bug Go's randomized map iteration would silently
// reintroduce if canonicalEncode regressed.
func TestCanonicalEncode_KeyStabilityAcrossMapOrder(t *testing.T) {
	t.Parallel()
	a := Block{
		Type: "core/paragraph",
		Attributes: map[string]any{
			"content": "x",
			"align":   "left",
			"dropCap": true,
		},
	}
	b := Block{
		Type: "core/paragraph",
		Attributes: map[string]any{
			"dropCap": true,
			"content": "x",
			"align":   "left",
		},
	}
	if string(canonicalEncode(a)) != string(canonicalEncode(b)) {
		t.Errorf("canonicalEncode disagrees on equal attribute sets:\n  a: %s\n  b: %s",
			canonicalEncode(a), canonicalEncode(b))
	}
}

// TestCachedWalker_DifferentAttrs_DifferentKey covers the inverse of
// the key-stability test: a one-attribute change MUST produce a
// different cache key (otherwise the cache would serve stale HTML).
func TestCachedWalker_DifferentAttrs_DifferentKey(t *testing.T) {
	t.Parallel()
	backend := newInMemoryBackend()
	cw := newCachedTestWalker(t, backend)
	ctx := context.Background()

	a := []Block{{Type: "core/paragraph", Attributes: map[string]any{"content": "a"}}}
	b := []Block{{Type: "core/paragraph", Attributes: map[string]any{"content": "b"}}}

	cw.Walk(ctx, a, nil)
	cw.Walk(ctx, b, nil)

	m := cw.Metrics()
	if m.Hits != 0 {
		t.Errorf("Hits: got %d, want 0 (different content must miss)", m.Hits)
	}
	if m.Misses != 2 {
		t.Errorf("Misses: got %d, want 2", m.Misses)
	}
}

// TestCachedWalker_NilCacheRejected pins the constructor's contract:
// a CachedWalker without a backend is a programming error.
func TestCachedWalker_NilCacheRejected(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	if _, err := NewCached(reg, CachedWalkerOptions{}); err == nil {
		t.Fatal("expected error for nil Cache")
	}
}

// TestCachedWalker_VersionBumpInvalidates verifies that two walkers
// constructed with different Version strings see independent caches
// — the operator-controlled cache-buster.
func TestCachedWalker_VersionBumpInvalidates(t *testing.T) {
	t.Parallel()
	backend := newInMemoryBackend()

	reg := NewRegistry()
	if err := RegisterCoreBlocks(reg); err != nil {
		t.Fatalf("RegisterCoreBlocks: %v", err)
	}
	cwV1, _ := NewCached(reg, CachedWalkerOptions{Cache: backend, Version: "v1"})
	cwV2, _ := NewCached(reg, CachedWalkerOptions{Cache: backend, Version: "v2"})

	tree := []Block{{Type: "core/paragraph", Attributes: map[string]any{"content": "x"}}}
	cwV1.Walk(context.Background(), tree, nil)
	cwV2.Walk(context.Background(), tree, nil)

	// Each walker should have populated its own key — two stores,
	// zero hits.
	if backend.sets != 2 {
		t.Errorf("sets across versions: got %d, want 2", backend.sets)
	}
}
