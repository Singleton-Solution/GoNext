package media

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/media/collections"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// newCollectionsMux builds a fresh ServeMux with both the media
// routes and the collections sub-mount wired. Returns the
// collection store, media store, and putter so individual tests can
// assert on their state.
func newCollectionsMux(t *testing.T) (*http.ServeMux, *collections.MemoryStore, *MemoryStore) {
	t.Helper()
	base := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	var clockSeq int
	clock := func() time.Time {
		clockSeq++
		return base.Add(time.Duration(clockSeq) * time.Second)
	}
	var idSeq int
	idGen := func() string {
		idSeq++
		return "col-" + itoa(idSeq)
	}
	colStore := collections.NewMemoryStore(clock, idGen)

	var mediaIDSeq int
	mediaIDGen := func() string {
		mediaIDSeq++
		return "asset-" + itoa(mediaIDSeq)
	}
	mediaStore := NewMemoryStore(clock, mediaIDGen)
	putter := NewMemoryPutter()

	mux := http.NewServeMux()
	pol := policy.NewBasicPolicy(policy.DefaultRoleCapabilities())
	if err := Mount(mux, "/api/v1/admin/media", Deps{
		Store:    mediaStore,
		Putter:   putter,
		Policy:   pol,
		Now:      func() time.Time { return base },
		MaxBytes: 1024 * 1024,
	}); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	if err := MountCollections(mux, "/api/v1/admin/media", CollectionsDeps{
		Store:   colStore,
		MediaSt: mediaStore,
		Policy:  pol,
	}); err != nil {
		t.Fatalf("MountCollections: %v", err)
	}
	return mux, colStore, mediaStore
}

func TestCollectionsCreate(t *testing.T) {
	mux, _, _ := newCollectionsMux(t)
	body := bytes.NewBufferString(`{"slug":"marketing","name":"Marketing"}`)
	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/admin/media/collections", body), authedPrincipal())
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var c collections.Collection
	if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if c.Path != "marketing" {
		t.Errorf("path = %q, want marketing", c.Path)
	}
}

func TestCollectionsCreateRejectsInvalidSlug(t *testing.T) {
	mux, _, _ := newCollectionsMux(t)
	body := bytes.NewBufferString(`{"slug":"Bad-Slug","name":"X"}`)
	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/admin/media/collections", body), authedPrincipal())
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
}

func TestCollectionsCreateRejectsSlugConflict(t *testing.T) {
	mux, _, _ := newCollectionsMux(t)
	bodyA := bytes.NewBufferString(`{"slug":"marketing","name":"A"}`)
	reqA := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/admin/media/collections", bodyA), authedPrincipal())
	reqA.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, reqA)
	if w.Code != http.StatusCreated {
		t.Fatalf("first create status = %d", w.Code)
	}

	bodyB := bytes.NewBufferString(`{"slug":"marketing","name":"B"}`)
	reqB := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/admin/media/collections", bodyB), authedPrincipal())
	reqB.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, reqB)
	if w.Code != http.StatusConflict {
		t.Fatalf("second create status = %d, want 409", w.Code)
	}
}

func TestCollectionsList(t *testing.T) {
	mux, store, _ := newCollectionsMux(t)
	ctx := context.Background()
	_, _ = store.Create(ctx, collections.CreateInput{Slug: "a", Name: "A"})
	_, _ = store.Create(ctx, collections.CreateInput{Slug: "b", Name: "B"})

	req := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/admin/media/collections", nil), authedPrincipal())
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var resp struct {
		Data []collections.Collection `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data) != 2 {
		t.Errorf("len = %d, want 2", len(resp.Data))
	}
}

func TestCollectionsDelete(t *testing.T) {
	mux, store, _ := newCollectionsMux(t)
	ctx := context.Background()
	c, _ := store.Create(ctx, collections.CreateInput{Slug: "a", Name: "A"})

	req := withAuth(httptest.NewRequest(http.MethodDelete, "/api/v1/admin/media/collections/"+c.ID, nil), authedPrincipal())
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
}

func TestCollectionsRename(t *testing.T) {
	mux, store, _ := newCollectionsMux(t)
	ctx := context.Background()
	c, _ := store.Create(ctx, collections.CreateInput{Slug: "old", Name: "Old"})

	body := bytes.NewBufferString(`{"slug":"new","name":"New"}`)
	req := withAuth(httptest.NewRequest(http.MethodPut, "/api/v1/admin/media/collections/"+c.ID, body), authedPrincipal())
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var got collections.Collection
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.Slug != "new" || got.Name != "New" || got.Path != "new" {
		t.Errorf("unexpected: %+v", got)
	}
}

func TestMoveMediaToCollection(t *testing.T) {
	mux, store, mediaStore := newCollectionsMux(t)
	ctx := context.Background()
	c, _ := store.Create(ctx, collections.CreateInput{Slug: "marketing", Name: "Marketing"})

	// Insert an asset directly via the store so we don't need to
	// drive the upload route in this test.
	asset, err := mediaStore.Insert(ctx, AssetCreate{
		Filename:   "logo.png",
		MimeType:   "image/png",
		ByteSize:   42,
		StorageKey: "2026/01/x-logo.png",
		SHA256:     bytes.Repeat([]byte{0x01}, 32),
		UploaderID: "user-1",
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	body := bytes.NewBufferString(`{"ids":["` + asset.ID + `"],"collection_id":"` + c.ID + `"}`)
	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/admin/media/move", body), authedPrincipal())
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	got, _ := mediaStore.GetByID(ctx, asset.ID)
	if got.CollectionID == nil || *got.CollectionID != c.ID {
		t.Errorf("CollectionID = %v, want %q", got.CollectionID, c.ID)
	}
}

func TestMoveMediaToRoot(t *testing.T) {
	mux, store, mediaStore := newCollectionsMux(t)
	ctx := context.Background()
	c, _ := store.Create(ctx, collections.CreateInput{Slug: "marketing", Name: "Marketing"})

	asset, _ := mediaStore.Insert(ctx, AssetCreate{
		Filename:   "logo.png",
		MimeType:   "image/png",
		ByteSize:   42,
		StorageKey: "2026/01/x-logo.png",
		SHA256:     bytes.Repeat([]byte{0x02}, 32),
		UploaderID: "user-1",
	})
	_ = mediaStore.SetCollection(ctx, asset.ID, &c.ID)

	body := bytes.NewBufferString(`{"ids":["` + asset.ID + `"]}`)
	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/admin/media/move", body), authedPrincipal())
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	got, _ := mediaStore.GetByID(ctx, asset.ID)
	if got.CollectionID != nil {
		t.Errorf("CollectionID = %v, want nil", *got.CollectionID)
	}
}

func TestMoveMediaToMissingCollection(t *testing.T) {
	mux, _, mediaStore := newCollectionsMux(t)
	ctx := context.Background()
	asset, _ := mediaStore.Insert(ctx, AssetCreate{
		Filename:   "logo.png",
		MimeType:   "image/png",
		ByteSize:   42,
		StorageKey: "2026/01/x-logo.png",
		SHA256:     bytes.Repeat([]byte{0x03}, 32),
		UploaderID: "user-1",
	})

	body := bytes.NewBufferString(`{"ids":["` + asset.ID + `"],"collection_id":"missing"}`)
	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/admin/media/move", body), authedPrincipal())
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestListFilterByCollection(t *testing.T) {
	mux, store, mediaStore := newCollectionsMux(t)
	ctx := context.Background()
	c, _ := store.Create(ctx, collections.CreateInput{Slug: "marketing", Name: "Marketing"})

	a, _ := mediaStore.Insert(ctx, AssetCreate{
		Filename:   "a.png",
		MimeType:   "image/png",
		ByteSize:   42,
		StorageKey: "2026/01/x-a.png",
		SHA256:     bytes.Repeat([]byte{0x04}, 32),
		UploaderID: "user-1",
	})
	_ = mediaStore.SetCollection(ctx, a.ID, &c.ID)

	_, _ = mediaStore.Insert(ctx, AssetCreate{
		Filename:   "b.png",
		MimeType:   "image/png",
		ByteSize:   42,
		StorageKey: "2026/01/x-b.png",
		SHA256:     bytes.Repeat([]byte{0x05}, 32),
		UploaderID: "user-1",
	})

	req := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/admin/media?collection="+c.ID, nil), authedPrincipal())
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var page Page
	_ = json.Unmarshal(w.Body.Bytes(), &page)
	if len(page.Data) != 1 || page.Data[0].ID != a.ID {
		t.Errorf("expected only %q, got %+v", a.ID, page.Data)
	}

	// "root" filter returns assets without a collection.
	req = withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/admin/media?collection=root", nil), authedPrincipal())
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	_ = json.Unmarshal(w.Body.Bytes(), &page)
	if len(page.Data) != 1 {
		t.Errorf("root filter: expected 1, got %d", len(page.Data))
	}
}
