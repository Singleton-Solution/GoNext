package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// recordingEnqueuer captures every Enqueue call so the upload tests
// can assert the handler fired the pipeline with the right (id, key,
// mime) triple. Optionally returns err to exercise the warn-and-
// continue path on enqueue failure.
type recordingEnqueuer struct {
	mu    sync.Mutex
	calls []enqueueCall
	err   error
}

type enqueueCall struct {
	assetID    string
	storageKey string
	mimeType   string
}

func (r *recordingEnqueuer) Enqueue(_ context.Context, assetID, storageKey, mimeType string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, enqueueCall{assetID, storageKey, mimeType})
	return r.err
}

func (r *recordingEnqueuer) Calls() []enqueueCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]enqueueCall(nil), r.calls...)
}

// newMuxWithProcessor mirrors newMux but installs a processor. We
// don't add the processor to the shared fixture to avoid touching
// every existing test's wiring.
func newMuxWithProcessor(t *testing.T, proc ProcessEnqueuer) (*http.ServeMux, *MemoryStore, *MemoryPutter) {
	t.Helper()
	var idSeq int
	idGen := func() string {
		idSeq++
		return "asset-" + strings.Repeat("0", 4-len(itoa(idSeq))) + itoa(idSeq)
	}
	base := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	var clockSeq int
	clock := func() time.Time {
		clockSeq++
		return base.Add(time.Duration(clockSeq) * time.Second)
	}
	store := NewMemoryStore(clock, idGen)
	putter := NewMemoryPutter()
	mux := http.NewServeMux()
	if err := Mount(mux, "/api/v1/admin/media", Deps{
		Store:     store,
		Putter:    putter,
		Policy:    policy.NewBasicPolicy(policy.DefaultRoleCapabilities()),
		Processor: proc,
		Now:       func() time.Time { return base },
		MaxBytes:  1024 * 1024,
	}); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	return mux, store, putter
}

// TestUpload_EnqueuesProcessing verifies the upload handler fires
// the processor after a successful insert and that the call carries
// the (id, key, mime) triple the worker needs.
func TestUpload_EnqueuesProcessing(t *testing.T) {
	proc := &recordingEnqueuer{}
	mux, _, _ := newMuxWithProcessor(t, proc)

	body, ct := buildMultipart(t, "logo.png", pngBytes())
	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/admin/media", body), authedPrincipal())
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, body = %s", w.Code, w.Body.String())
	}
	var asset Asset
	_ = json.Unmarshal(w.Body.Bytes(), &asset)

	calls := proc.Calls()
	if len(calls) != 1 {
		t.Fatalf("Enqueue called %d times, want 1", len(calls))
	}
	if calls[0].assetID != asset.ID || calls[0].storageKey != asset.StorageKey {
		t.Errorf("Enqueue payload mismatch: got %+v, want (%s,%s)", calls[0], asset.ID, asset.StorageKey)
	}
	if calls[0].mimeType != "image/png" {
		t.Errorf("Enqueue mime = %q, want image/png", calls[0].mimeType)
	}
}

// TestUpload_EnqueueErrorDoesNotFailUpload pins the documented
// contract: a worker outage at enqueue time must not lose the
// upload. The row is committed; only the variant generation is
// delayed.
func TestUpload_EnqueueErrorDoesNotFailUpload(t *testing.T) {
	proc := &recordingEnqueuer{err: errors.New("worker queue down")}
	mux, store, _ := newMuxWithProcessor(t, proc)

	body, ct := buildMultipart(t, "logo.png", pngBytes())
	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/admin/media", body), authedPrincipal())
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("upload should succeed even with enqueue failure: status = %d", w.Code)
	}
	page, _ := store.List(context.Background(), ListFilter{})
	if len(page.Data) != 1 {
		t.Errorf("row not committed after enqueue failure: rows = %d", len(page.Data))
	}
}

// TestList_IncludesVariantURLs proves the list response renders the
// per-variant public URL. The variant rows are written via
// SetVariants; the handler must populate PublicURL on each one with
// the same convention as the asset-level URL.
func TestList_IncludesVariantURLs(t *testing.T) {
	mux, store, putter := newMuxWithProcessor(t, nil)

	asset, err := store.Insert(context.Background(), AssetCreate{
		Filename:   "photo.jpg",
		MimeType:   "image/jpeg",
		ByteSize:   1024,
		StorageKey: "2026/01/photo.jpg",
		SHA256:     bytes.Repeat([]byte{0x21}, 32),
		UploaderID: "user-1",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	variants := []Variant{
		{
			Name: "thumb", Format: "webp", Width: 256, Height: 192,
			StorageKey: "2026/01/photo.jpg.thumb.webp", MimeType: "image/webp",
		},
		{
			Name: "medium", Format: "webp", Width: 768, Height: 576,
			StorageKey: "2026/01/photo.jpg.medium.webp", MimeType: "image/webp",
		},
	}
	if err := store.SetVariants(context.Background(), asset.ID, variants); err != nil {
		t.Fatalf("SetVariants: %v", err)
	}

	req := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/admin/media", nil), authedPrincipal())
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d", w.Code)
	}
	var page Page
	_ = json.Unmarshal(w.Body.Bytes(), &page)
	if len(page.Data) != 1 {
		t.Fatalf("expected 1 row, got %d", len(page.Data))
	}
	row := page.Data[0]
	if len(row.Variants) != 2 {
		t.Fatalf("expected 2 variants on row, got %d", len(row.Variants))
	}
	for _, v := range row.Variants {
		want := putter.PublicURL(v.StorageKey)
		if v.PublicURL != want {
			t.Errorf("variant %q public_url = %q, want %q", v.Name, v.PublicURL, want)
		}
	}
}

// TestGet_IncludesVariantURLs is the single-row equivalent of the
// list test: the detail endpoint must also render the per-variant
// PublicURL field.
func TestGet_IncludesVariantURLs(t *testing.T) {
	mux, store, putter := newMuxWithProcessor(t, nil)
	asset, _ := store.Insert(context.Background(), AssetCreate{
		Filename: "photo.jpg", MimeType: "image/jpeg", ByteSize: 1,
		StorageKey: "k", SHA256: bytes.Repeat([]byte{0x22}, 32), UploaderID: "u",
	})
	if err := store.SetVariants(context.Background(), asset.ID, []Variant{
		{Name: "thumb", Format: "webp", StorageKey: "k.thumb.webp", MimeType: "image/webp", Width: 256, Height: 256},
	}); err != nil {
		t.Fatalf("SetVariants: %v", err)
	}

	req := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/admin/media/"+asset.ID, nil), authedPrincipal())
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get status = %d", w.Code)
	}
	var out Asset
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if len(out.Variants) != 1 {
		t.Fatalf("expected 1 variant on detail response, got %d", len(out.Variants))
	}
	if want := putter.PublicURL("k.thumb.webp"); out.Variants[0].PublicURL != want {
		t.Errorf("variant public_url = %q, want %q", out.Variants[0].PublicURL, want)
	}
}

// TestStore_SetVariantsIdempotent pins that a re-run of SetVariants
// (e.g. an admin reprocess) replaces the previous variant list
// rather than appending.
func TestStore_SetVariantsIdempotent(t *testing.T) {
	store := NewMemoryStore(nil, nil)
	asset, _ := store.Insert(context.Background(), AssetCreate{
		Filename: "x", MimeType: "image/png", ByteSize: 1, StorageKey: "k",
		SHA256: bytes.Repeat([]byte{0x33}, 32), UploaderID: "u",
	})
	first := []Variant{{Name: "thumb", Format: "webp", StorageKey: "a"}}
	if err := store.SetVariants(context.Background(), asset.ID, first); err != nil {
		t.Fatalf("first SetVariants: %v", err)
	}
	second := []Variant{
		{Name: "thumb", Format: "webp", StorageKey: "b"},
		{Name: "medium", Format: "webp", StorageKey: "c"},
	}
	if err := store.SetVariants(context.Background(), asset.ID, second); err != nil {
		t.Fatalf("second SetVariants: %v", err)
	}
	got, _ := store.GetByID(context.Background(), asset.ID)
	if len(got.Variants) != 2 {
		t.Errorf("rerun did not replace: variants = %d, want 2", len(got.Variants))
	}
	if got.Variants[0].StorageKey != "b" {
		t.Errorf("rerun did not replace first entry: %v", got.Variants)
	}
}

// TestStore_SetVariantsOnDeletedRow pins the contract that a soft-
// deleted row rejects variant writes: a task that races a delete
// must surface ErrNotFound rather than silently writing variants to
// a tombstoned asset.
func TestStore_SetVariantsOnDeletedRow(t *testing.T) {
	store := NewMemoryStore(nil, nil)
	asset, _ := store.Insert(context.Background(), AssetCreate{
		Filename: "x", MimeType: "image/png", ByteSize: 1, StorageKey: "k",
		SHA256: bytes.Repeat([]byte{0x44}, 32), UploaderID: "u",
	})
	_ = store.SoftDelete(context.Background(), asset.ID)
	if err := store.SetVariants(context.Background(), asset.ID, nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("SetVariants on deleted row: err = %v, want ErrNotFound", err)
	}
}
