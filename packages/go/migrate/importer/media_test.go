package importer

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakePutter is the test-only MediaPutter. Captures every PUT so a
// test can assert "the migrator wrote these bytes to this key".
type fakePutter struct {
	mu     sync.Mutex
	stored map[string]storedObject
	err    error
}

type storedObject struct {
	body     []byte
	mimeType string
}

func newFakePutter() *fakePutter {
	return &fakePutter{stored: map[string]storedObject{}}
}

func (f *fakePutter) PutObject(ctx context.Context, key string, body []byte, mime string) error {
	if f.err != nil {
		return f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]byte, len(body))
	copy(cp, body)
	f.stored[key] = storedObject{body: cp, mimeType: mime}
	return nil
}

// fakeInserter is the test-only MediaInserter. Records every insert
// so tests can assert the (mode, key, source_url) outcome.
type fakeInserter struct {
	mu          sync.Mutex
	bySourceURL map[string]string // sourceURL → id (proxy only)
	byKey       map[string]string // storage_key → id (copy and proxy)
	copied      []MediaRow
	proxied     []proxyEntry
	idSeq       int
	findErr     error
	insertErr   error
}

type proxyEntry struct {
	sourceURL string
	row       MediaRow
}

func newFakeInserter() *fakeInserter {
	return &fakeInserter{
		bySourceURL: map[string]string{},
		byKey:       map[string]string{},
	}
}

func (f *fakeInserter) nextID() string {
	f.idSeq++
	return fmt.Sprintf("media-%04d", f.idSeq)
}

func (f *fakeInserter) FindBySourceURL(ctx context.Context, sourceURL string) (string, string, bool, error) {
	if f.findErr != nil {
		return "", "", false, f.findErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if id, ok := f.bySourceURL[sourceURL]; ok {
		// Find the storage key for the id by reverse lookup.
		for k, v := range f.byKey {
			if v == id {
				return id, k, true, nil
			}
		}
		return id, "", true, nil
	}
	return "", "", false, nil
}

func (f *fakeInserter) InsertCopied(ctx context.Context, row MediaRow) (string, error) {
	if f.insertErr != nil {
		return "", f.insertErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.nextID()
	f.copied = append(f.copied, row)
	f.byKey[row.StorageKey] = id
	return id, nil
}

func (f *fakeInserter) InsertProxied(ctx context.Context, sourceURL string, row MediaRow) (string, error) {
	if f.insertErr != nil {
		return "", f.insertErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.nextID()
	f.proxied = append(f.proxied, proxyEntry{sourceURL: sourceURL, row: row})
	f.bySourceURL[sourceURL] = id
	f.byKey[row.StorageKey] = id
	return id, nil
}

// pinClock returns a clock function that always returns t. Used to
// make storage-key minting deterministic in tests.
func pinClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

// staticKeyGen returns a key generator that ignores its inputs and
// always returns key. Lets a test assert "the migrator wrote at
// EXACTLY this key".
func staticKeyGen(key string) func(time.Time, string) string {
	return func(time.Time, string) string { return key }
}

// TestMediaMigrator_CopyMode_DownloadsAndStores spins up a fake
// source server, points the migrator at it in copy mode, and
// asserts:
//   - the source server received exactly one request
//   - the putter saw the same bytes
//   - the inserter recorded the row as copied (not proxied)
//   - the resulting row carries no SourceURL (copy mode owns the
//     bytes outright)
func TestMediaMigrator_CopyMode_DownloadsAndStores(t *testing.T) {
	var reqCount int
	body := []byte("FAKE_JPEG_BYTES_FROM_SOURCE")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqCount++
		w.Header().Set("Content-Type", "image/jpeg")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	putter := newFakePutter()
	inserter := newFakeInserter()
	m := NewMediaMigrator(MediaConfig{Mode: MediaModeCopy}, putter, inserter)
	m.SetNow(pinClock(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)))
	m.SetKeyGen(staticKeyGen("2026/05/test-photo.jpg"))

	res, err := m.IngestURL(context.Background(), srv.URL+"/uploads/2024/03/photo.jpg", "user-001")
	if err != nil {
		t.Fatalf("IngestURL: %v", err)
	}
	if reqCount != 1 {
		t.Errorf("source server hit %d times, want 1", reqCount)
	}
	if res.Mode != MediaModeCopy {
		t.Errorf("result.Mode = %v, want copy", res.Mode)
	}
	if res.StorageKey != "2026/05/test-photo.jpg" {
		t.Errorf("result.StorageKey = %q, want 2026/05/test-photo.jpg", res.StorageKey)
	}
	if res.BytesFetched != int64(len(body)) {
		t.Errorf("result.BytesFetched = %d, want %d", res.BytesFetched, len(body))
	}
	if res.Reused {
		t.Error("result.Reused should be false on first call")
	}
	// Putter saw the bytes
	stored, ok := putter.stored["2026/05/test-photo.jpg"]
	if !ok {
		t.Fatalf("putter did not receive the expected key; got keys: %v", keysOf(putter.stored))
	}
	if string(stored.body) != string(body) {
		t.Errorf("putter body mismatch: %q vs %q", stored.body, body)
	}
	// Inserter recorded the row as copied
	if len(inserter.copied) != 1 {
		t.Fatalf("inserter.copied len = %d, want 1", len(inserter.copied))
	}
	if inserter.copied[0].SourceURL != "" {
		t.Errorf("copied row should have empty SourceURL, got %q", inserter.copied[0].SourceURL)
	}
	if len(inserter.proxied) != 0 {
		t.Errorf("inserter should not have proxied rows in copy mode")
	}
}

// TestMediaMigrator_ProxyMode_NoFetchNoUpload confirms proxy mode
// never hits the source server and never writes to the putter.
func TestMediaMigrator_ProxyMode_NoFetchNoUpload(t *testing.T) {
	var reqCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqCount++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	putter := newFakePutter()
	inserter := newFakeInserter()
	m := NewMediaMigrator(MediaConfig{Mode: MediaModeProxy}, putter, inserter)

	res, err := m.IngestURL(context.Background(), srv.URL+"/uploads/2024/03/photo.jpg", "user-001")
	if err != nil {
		t.Fatalf("IngestURL: %v", err)
	}
	if reqCount != 0 {
		t.Errorf("proxy mode should NOT fetch source; got %d requests", reqCount)
	}
	if len(putter.stored) != 0 {
		t.Errorf("proxy mode should NOT write to putter; got %d uploads", len(putter.stored))
	}
	if res.Mode != MediaModeProxy {
		t.Errorf("result.Mode = %v, want proxy", res.Mode)
	}
	if res.BytesFetched != 0 {
		t.Errorf("result.BytesFetched = %d, want 0 for proxy", res.BytesFetched)
	}
	if !strings.HasPrefix(res.StorageKey, "proxy/") {
		t.Errorf("proxy storage key should have proxy/ prefix; got %q", res.StorageKey)
	}
	// Inserter recorded the row as proxied
	if len(inserter.proxied) != 1 {
		t.Fatalf("inserter.proxied len = %d, want 1", len(inserter.proxied))
	}
	if inserter.proxied[0].sourceURL != srv.URL+"/uploads/2024/03/photo.jpg" {
		t.Errorf("proxied sourceURL = %q", inserter.proxied[0].sourceURL)
	}
	if inserter.proxied[0].row.SourceURL == "" {
		t.Error("proxied row.SourceURL should be set")
	}
	if len(inserter.copied) != 0 {
		t.Errorf("inserter should not have copied rows in proxy mode")
	}
}

// TestMediaMigrator_Idempotency confirms a second IngestURL with the
// same URL returns the existing row without re-fetching or
// re-inserting.
func TestMediaMigrator_Idempotency(t *testing.T) {
	var reqCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqCount++
		w.Write([]byte("BYTES"))
	}))
	defer srv.Close()

	putter := newFakePutter()
	inserter := newFakeInserter()
	// Pre-seed the inserter with a row for this URL so the
	// idempotency probe hits on first call too.
	inserter.bySourceURL[srv.URL+"/x.jpg"] = "preexisting-id"
	inserter.byKey["preexisting-key"] = "preexisting-id"

	m := NewMediaMigrator(MediaConfig{Mode: MediaModeProxy}, putter, inserter)
	res, err := m.IngestURL(context.Background(), srv.URL+"/x.jpg", "u")
	if err != nil {
		t.Fatalf("IngestURL: %v", err)
	}
	if !res.Reused {
		t.Error("result.Reused should be true when FindBySourceURL hits")
	}
	if res.MediaID != "preexisting-id" {
		t.Errorf("MediaID = %q, want preexisting-id", res.MediaID)
	}
	if reqCount != 0 {
		t.Errorf("idempotent call should not fetch; got %d requests", reqCount)
	}
}

// TestMediaMigrator_CopyMode_Non200 confirms a 404 from the source
// surfaces as ErrSourceNot200.
func TestMediaMigrator_CopyMode_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	m := NewMediaMigrator(MediaConfig{Mode: MediaModeCopy}, newFakePutter(), newFakeInserter())
	_, err := m.IngestURL(context.Background(), srv.URL+"/missing.jpg", "u")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrSourceNot200) {
		t.Errorf("err = %v, want ErrSourceNot200", err)
	}
}

// TestMediaMigrator_CopyMode_TooLarge confirms the size cap is
// enforced and ErrTooLarge surfaces.
func TestMediaMigrator_CopyMode_TooLarge(t *testing.T) {
	big := make([]byte, 200) // larger than the cap below
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(big)
	}))
	defer srv.Close()

	m := NewMediaMigrator(MediaConfig{Mode: MediaModeCopy, MaxBytes: 50}, newFakePutter(), newFakeInserter())
	_, err := m.IngestURL(context.Background(), srv.URL+"/big.jpg", "u")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrTooLarge) {
		t.Errorf("err = %v, want ErrTooLarge", err)
	}
}

// TestMediaMigrator_RejectsBadInput pins the input-validation
// contract: empty URL, invalid URL, missing uploader.
func TestMediaMigrator_RejectsBadInput(t *testing.T) {
	m := NewMediaMigrator(MediaConfig{}, newFakePutter(), newFakeInserter())
	if _, err := m.IngestURL(context.Background(), "", "u"); err == nil {
		t.Error("empty URL: expected error")
	}
	if _, err := m.IngestURL(context.Background(), "not a url", "u"); err == nil {
		t.Error("invalid URL: expected error")
	}
	if _, err := m.IngestURL(context.Background(), "http://x/y.jpg", ""); err == nil {
		t.Error("empty uploaderID: expected error")
	}
}

// TestParseMediaMode pins the canonical CLI form so flag parsing
// stays stable across releases.
func TestParseMediaMode(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want MediaMode
		ok   bool
	}{
		{"", MediaModeCopy, true},
		{"copy", MediaModeCopy, true},
		{"COPY", MediaModeCopy, true},
		{"proxy", MediaModeProxy, true},
		{"PROXY", MediaModeProxy, true},
		{"junk", MediaModeCopy, false},
	} {
		got, err := ParseMediaMode(tt.in)
		if (err == nil) != tt.ok {
			t.Errorf("ParseMediaMode(%q) ok = %v, want %v", tt.in, err == nil, tt.ok)
		}
		if tt.ok && got != tt.want {
			t.Errorf("ParseMediaMode(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

// TestRewriteContent confirms the per-asset URL substitution does
// what it says.
func TestRewriteContent(t *testing.T) {
	in := `<p>See <img src="https://old.example/a.jpg" /> and ` +
		`<a href="https://old.example/b.pdf">file</a>.</p>`
	out := RewriteContent(in, map[string]string{
		"https://old.example/a.jpg": "https://cdn.gonext.example/2026/05/a.jpg",
		"https://old.example/b.pdf": "https://proxy.gonext.example/m/abc",
	})
	if !strings.Contains(out, "https://cdn.gonext.example/2026/05/a.jpg") {
		t.Errorf("missing copy-mode replacement: %s", out)
	}
	if !strings.Contains(out, "https://proxy.gonext.example/m/abc") {
		t.Errorf("missing proxy-mode replacement: %s", out)
	}
	if strings.Contains(out, "old.example") {
		t.Errorf("source URL still present after rewrite: %s", out)
	}
}

// TestRewriteContent_EmptyInputs handles the no-op paths.
func TestRewriteContent_EmptyInputs(t *testing.T) {
	if got := RewriteContent("", nil); got != "" {
		t.Errorf("empty content: got %q", got)
	}
	if got := RewriteContent("hello", nil); got != "hello" {
		t.Errorf("no replacements: got %q", got)
	}
	if got := RewriteContent("hello", map[string]string{"": ""}); got != "hello" {
		t.Errorf("empty pairs: got %q", got)
	}
}

// keysOf returns sorted keys of a map for error messages.
func keysOf(m map[string]storedObject) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
