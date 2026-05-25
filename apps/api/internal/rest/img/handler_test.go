package img

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Singleton-Solution/GoNext/packages/go/media"
	"github.com/Singleton-Solution/GoNext/packages/go/media/imgproxy"
)

// fakeLookup is a map-backed AssetLookup. Tests register rows by ID;
// LookupByID returns ErrAssetNotFound for any unregistered ID.
type fakeLookup struct {
	rows map[string]AssetRef
}

func (f *fakeLookup) LookupByID(_ context.Context, id string) (AssetRef, error) {
	ref, ok := f.rows[id]
	if !ok {
		return AssetRef{}, ErrAssetNotFound
	}
	return ref, nil
}

// fakeSource is a map-backed Source. Tests prime it with bytes at
// the storage keys their assets point at.
type fakeSource struct {
	mu     sync.Mutex
	bodies map[string][]byte
	calls  int32
	fail   error
}

func (f *fakeSource) GetObject(_ context.Context, key string) ([]byte, error) {
	atomic.AddInt32(&f.calls, 1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return nil, f.fail
	}
	b, ok := f.bodies[key]
	if !ok {
		return nil, ErrSourceNotFound
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out, nil
}

// loadSamplePNG returns the bytes of the checked-in 200x200 sample
// image. The fixture lives in testdata/sample-200x200.png so tests
// in this package — and a future fuzz test — can share one
// deterministic source.
func loadSamplePNG(t *testing.T) []byte {
	t.Helper()
	path := filepath.Join("testdata", "sample-200x200.png")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read sample image %s: %v", path, err)
	}
	if len(b) == 0 {
		t.Fatal("sample image is empty")
	}
	// Sanity-check the bounds so a corrupted fixture surfaces here
	// rather than as a confusing transformer error downstream.
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("decode sample image: %v", err)
	}
	if img.Bounds().Dx() != 200 || img.Bounds().Dy() != 200 {
		t.Fatalf("sample image is %dx%d, want 200x200", img.Bounds().Dx(), img.Bounds().Dy())
	}
	return b
}

// newTestHandler builds the handler against in-memory fakes and
// returns the mux + the underlying coalescer so tests can assert on
// counters.
func newTestHandler(t *testing.T, source *fakeSource, lookup *fakeLookup) (*http.ServeMux, *imgproxy.Cache, *media.Coalescer) {
	t.Helper()
	cache := imgproxy.NewCache(t.TempDir())
	coal := media.NewCoalescer(media.CoalescerOptions{})
	mux := http.NewServeMux()
	err := Mount(mux, "/img", Deps{
		Lookup:    lookup,
		Source:    source,
		Cache:     cache,
		Coalescer: coal,
	})
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}
	return mux, cache, coal
}

func TestServe_HappyPath(t *testing.T) {
	sample := loadSamplePNG(t)
	source := &fakeSource{bodies: map[string][]byte{"k/sample.png": sample}}
	lookup := &fakeLookup{rows: map[string]AssetRef{
		"asset-1": {ID: "asset-1", StorageKey: "k/sample.png", MIMEType: "image/png"},
	}}
	mux, cache, _ := newTestHandler(t, source, lookup)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/img/asset-1/w-100.h-100.fit-cover.jpeg")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status: want 200, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "image/jpeg" {
		t.Fatalf("content-type: want image/jpeg, got %s", got)
	}
	if got := resp.Header.Get("Cache-Control"); !strings.Contains(got, "immutable") {
		t.Fatalf("cache-control missing 'immutable': %s", got)
	}
	if got := resp.Header.Get("Vary"); !strings.Contains(got, "Accept") {
		t.Fatalf("vary missing Accept: %s", got)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if len(body) == 0 {
		t.Fatal("empty body")
	}
	// Decode the body as JPEG and check the dimensions match the spec.
	out, err := jpeg.Decode(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("decode response JPEG: %v", err)
	}
	if out.Bounds().Dx() != 100 || out.Bounds().Dy() != 100 {
		t.Fatalf("response bounds: want 100x100, got %v", out.Bounds())
	}

	// Verify the cache entry exists on disk.
	spec, _ := imgproxy.Parse("w-100.h-100.fit-cover.jpeg")
	if entry, err := cache.Get("asset-1", spec); err != nil {
		t.Fatalf("expected cache entry, got %v", err)
	} else {
		_ = entry.Reader.Close()
	}
}

func TestServe_BadSpec(t *testing.T) {
	source := &fakeSource{bodies: map[string][]byte{}}
	lookup := &fakeLookup{rows: map[string]AssetRef{
		"asset-1": {ID: "asset-1", StorageKey: "k", MIMEType: "image/png"},
	}}
	mux, _, _ := newTestHandler(t, source, lookup)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/img/asset-1/w-99999.webp")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("status: want 400, got %d", resp.StatusCode)
	}
}

func TestServe_NotFound(t *testing.T) {
	source := &fakeSource{bodies: map[string][]byte{}}
	lookup := &fakeLookup{rows: map[string]AssetRef{}}
	mux, _, _ := newTestHandler(t, source, lookup)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/img/does-not-exist/w-100.webp")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("status: want 404, got %d", resp.StatusCode)
	}
}

func TestServe_UnsupportedMIME(t *testing.T) {
	source := &fakeSource{bodies: map[string][]byte{"k/doc.pdf": []byte("PDF")}}
	lookup := &fakeLookup{rows: map[string]AssetRef{
		"asset-1": {ID: "asset-1", StorageKey: "k/doc.pdf", MIMEType: "application/pdf"},
	}}
	mux, _, _ := newTestHandler(t, source, lookup)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/img/asset-1/w-100.webp")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 415 {
		t.Fatalf("status: want 415, got %d", resp.StatusCode)
	}
}

func TestServe_CacheHitSkipsSource(t *testing.T) {
	sample := loadSamplePNG(t)
	source := &fakeSource{bodies: map[string][]byte{"k/sample.png": sample}}
	lookup := &fakeLookup{rows: map[string]AssetRef{
		"asset-1": {ID: "asset-1", StorageKey: "k/sample.png", MIMEType: "image/png"},
	}}
	mux, _, _ := newTestHandler(t, source, lookup)

	srv := httptest.NewServer(mux)
	defer srv.Close()
	url := srv.URL + "/img/asset-1/w-50.webp"

	// First request — miss path, fetches source.
	r1, err := http.Get(url)
	if err != nil {
		t.Fatalf("first GET: %v", err)
	}
	io.Copy(io.Discard, r1.Body)
	r1.Body.Close()
	if r1.StatusCode != 200 {
		t.Fatalf("first status: want 200, got %d", r1.StatusCode)
	}

	callsAfterFirst := atomic.LoadInt32(&source.calls)
	if callsAfterFirst != 1 {
		t.Fatalf("expected 1 source call after first request, got %d", callsAfterFirst)
	}

	// Second request — should hit cache and NOT call source.
	r2, err := http.Get(url)
	if err != nil {
		t.Fatalf("second GET: %v", err)
	}
	io.Copy(io.Discard, r2.Body)
	r2.Body.Close()
	if r2.StatusCode != 200 {
		t.Fatalf("second status: want 200, got %d", r2.StatusCode)
	}

	callsAfterSecond := atomic.LoadInt32(&source.calls)
	if callsAfterSecond != 1 {
		t.Fatalf("expected source calls to stay at 1, got %d", callsAfterSecond)
	}
}

func TestServe_Coalesces(t *testing.T) {
	// 20 concurrent requests for the same (id, spec) should produce
	// exactly one source fetch (the leader's) — the followers reuse
	// the leader's rendered bytes.
	sample := loadSamplePNG(t)
	source := &fakeSource{bodies: map[string][]byte{"k/sample.png": sample}}
	lookup := &fakeLookup{rows: map[string]AssetRef{
		"asset-1": {ID: "asset-1", StorageKey: "k/sample.png", MIMEType: "image/png"},
	}}
	mux, _, coal := newTestHandler(t, source, lookup)

	srv := httptest.NewServer(mux)
	defer srv.Close()
	url := srv.URL + "/img/asset-1/w-77.h-77.q-70.fit-cover.webp"

	const N = 20
	var wg sync.WaitGroup
	wg.Add(N)
	start := make(chan struct{})
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			<-start
			resp, err := http.Get(url)
			if err != nil {
				t.Errorf("GET: %v", err)
				return
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode != 200 {
				t.Errorf("status: want 200, got %d", resp.StatusCode)
			}
		}()
	}
	close(start)
	wg.Wait()

	calls := atomic.LoadInt32(&source.calls)
	// We expect exactly 1 (the leader). The coalescer's stats give
	// the leader/follower count.
	stats := coal.Stats()
	if calls != 1 {
		t.Errorf("expected 1 source call, got %d (coalesced=%d, generated=%d)",
			calls, stats.TotalCoalesced, stats.TotalGenerated)
	}
	if stats.TotalGenerated != 1 {
		t.Errorf("expected 1 generation, got %d", stats.TotalGenerated)
	}
}

func TestServe_HeadDoesNotWriteBody(t *testing.T) {
	sample := loadSamplePNG(t)
	source := &fakeSource{bodies: map[string][]byte{"k/sample.png": sample}}
	lookup := &fakeLookup{rows: map[string]AssetRef{
		"asset-1": {ID: "asset-1", StorageKey: "k/sample.png", MIMEType: "image/png"},
	}}
	mux, _, _ := newTestHandler(t, source, lookup)

	srv := httptest.NewServer(mux)
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodHead, srv.URL+"/img/asset-1/w-50.webp", nil)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("HEAD: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status: want 200, got %d", resp.StatusCode)
	}
	if resp.Header.Get("Content-Type") == "" {
		t.Fatal("missing content-type on HEAD")
	}
	body, _ := io.ReadAll(resp.Body)
	if len(body) != 0 {
		t.Fatalf("HEAD response should have empty body, got %d bytes", len(body))
	}
}

func TestServe_SourceMissing(t *testing.T) {
	// Row exists, but storage key resolves to nothing.
	source := &fakeSource{bodies: map[string][]byte{}}
	lookup := &fakeLookup{rows: map[string]AssetRef{
		"asset-1": {ID: "asset-1", StorageKey: "k/missing", MIMEType: "image/png"},
	}}
	mux, _, _ := newTestHandler(t, source, lookup)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/img/asset-1/w-100.webp")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("status: want 404, got %d", resp.StatusCode)
	}
}

func TestServe_TransformError(t *testing.T) {
	// Source returns bytes the transformer can't decode → 415.
	source := &fakeSource{bodies: map[string][]byte{"k/bad": []byte("not an image")}}
	lookup := &fakeLookup{rows: map[string]AssetRef{
		"asset-1": {ID: "asset-1", StorageKey: "k/bad", MIMEType: "image/png"},
	}}
	mux, _, _ := newTestHandler(t, source, lookup)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/img/asset-1/w-100.webp")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 415 {
		t.Fatalf("status: want 415, got %d", resp.StatusCode)
	}
}

func TestMount_ValidateMissingDeps(t *testing.T) {
	mux := http.NewServeMux()
	err := Mount(mux, "/img", Deps{})
	if err == nil {
		t.Fatal("expected error for missing deps")
	}
	if !strings.Contains(err.Error(), "Lookup is required") {
		t.Fatalf("expected Lookup error, got %v", err)
	}
}

func TestServe_SourceErrorBubblesAs500(t *testing.T) {
	source := &fakeSource{
		bodies: map[string][]byte{"k": []byte{}},
		fail:   errors.New("storage timeout"),
	}
	lookup := &fakeLookup{rows: map[string]AssetRef{
		"asset-1": {ID: "asset-1", StorageKey: "k", MIMEType: "image/png"},
	}}
	mux, _, _ := newTestHandler(t, source, lookup)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/img/asset-1/w-100.webp")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 500 {
		t.Fatalf("status: want 500, got %d", resp.StatusCode)
	}
}

// Sanity check our fixture decodes deterministically as 200x200.
func TestSampleFixtureBounds(t *testing.T) {
	b := loadSamplePNG(t)
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := image.Rect(0, 0, 200, 200)
	if img.Bounds() != want {
		t.Fatalf("bounds: want %v, got %v", want, img.Bounds())
	}
	// A sanity-checked pixel so corrupted fixture surfaces here.
	_ = img.At(100, 100).(color.Color)
}
