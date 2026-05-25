package imgproxy

import (
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
)

func TestCache_HitAndMiss(t *testing.T) {
	dir := t.TempDir()
	c := NewCache(dir)

	spec, err := Parse("w-100.webp")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	assetID := "asset-1"

	if _, err := c.Get(assetID, spec); !errors.Is(err, ErrCacheMiss) {
		t.Fatalf("expected miss, got %v", err)
	}

	body := []byte("test bytes")
	if err := c.Put(assetID, spec, body); err != nil {
		t.Fatalf("Put: %v", err)
	}

	entry, err := c.Get(assetID, spec)
	if err != nil {
		t.Fatalf("Get after Put: %v", err)
	}
	defer entry.Reader.Close()

	if entry.Size != int64(len(body)) {
		t.Fatalf("size: want %d, got %d", len(body), entry.Size)
	}
	if entry.MIMEType != "image/webp" {
		t.Fatalf("mime: want image/webp, got %s", entry.MIMEType)
	}
	got, err := io.ReadAll(entry.Reader)
	if err != nil {
		t.Fatalf("read entry: %v", err)
	}
	if !strings.HasPrefix(string(got), "test bytes") {
		t.Fatalf("body mismatch: %q", got)
	}
}

func TestCache_CanonicalKeyCollapse(t *testing.T) {
	dir := t.TempDir()
	c := NewCache(dir)

	a, _ := Parse("w-100.h-100.webp")
	b, _ := Parse("h-100.w-100.webp")

	if c.Key("id", a) != c.Key("id", b) {
		t.Fatalf("equivalent specs should produce the same cache key")
	}
}

func TestCache_PutAtomicRename(t *testing.T) {
	dir := t.TempDir()
	c := NewCache(dir)
	spec, _ := Parse("w-100.webp")
	if err := c.Put("a", spec, []byte("aaa")); err != nil {
		t.Fatalf("first put: %v", err)
	}
	if err := c.Put("a", spec, []byte("bbb")); err != nil {
		t.Fatalf("second put: %v", err)
	}
	entry, err := c.Get("a", spec)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer entry.Reader.Close()
	got, _ := io.ReadAll(entry.Reader)
	if string(got) != "bbb" {
		t.Fatalf("second put should win, got %q", got)
	}
}

func TestCache_EmptyBodyRejected(t *testing.T) {
	dir := t.TempDir()
	c := NewCache(dir)
	spec, _ := Parse("w-100.webp")
	if err := c.Put("a", spec, nil); err == nil {
		t.Fatal("expected error for empty body")
	}
}

func TestCache_ConcurrentPuts(t *testing.T) {
	// Concurrent puts at the same key must not corrupt the file —
	// each put writes to its own temp path and rename is atomic.
	dir := t.TempDir()
	c := NewCache(dir)
	spec, _ := Parse("w-100.webp")
	const N = 20
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			body := []byte(strings.Repeat("x", 100+i))
			if err := c.Put("a", spec, body); err != nil {
				t.Errorf("Put %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	entry, err := c.Get("a", spec)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer entry.Reader.Close()
	body, _ := io.ReadAll(entry.Reader)
	// Every byte should be 'x' (one of the writers won) — never a
	// half-written file with truncated tail.
	for _, b := range body {
		if b != 'x' {
			t.Fatalf("corrupted body, found %q", b)
		}
	}
}
