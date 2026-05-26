package media

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// newBulkMux builds a mux with media + bulk wired and an AltGenerator
// supplied. Returns the store + the captured enqueued ids for the
// AI-alt test.
func newBulkMux(t *testing.T) (*http.ServeMux, *MemoryStore, *[]string) {
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
		return "asset-" + itoa(idSeq)
	}
	store := NewMemoryStore(clock, idGen)
	putter := NewMemoryPutter()

	mux := http.NewServeMux()
	pol := policy.NewBasicPolicy(policy.DefaultRoleCapabilities())
	if err := Mount(mux, "/api/v1/admin/media", Deps{
		Store: store, Putter: putter, Policy: pol, Now: func() time.Time { return base },
	}); err != nil {
		t.Fatalf("Mount: %v", err)
	}

	captured := &[]string{}
	gen := AltGeneratorFunc(func(_ context.Context, id string) error {
		*captured = append(*captured, id)
		return nil
	})
	if err := MountBulk(mux, "/api/v1/admin/media", BulkDeps{
		Store: store, Policy: pol, AltGenerator: gen,
	}); err != nil {
		t.Fatalf("MountBulk: %v", err)
	}
	return mux, store, captured
}

func seedAssets(t *testing.T, store *MemoryStore, n int) []string {
	t.Helper()
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		a, err := store.Insert(context.Background(), AssetCreate{
			Filename:   "f" + itoa(i) + ".png",
			MimeType:   "image/png",
			ByteSize:   100,
			StorageKey: "2026/01/x-f" + itoa(i) + ".png",
			SHA256:     bytes.Repeat([]byte{byte(0x10 + i)}, 32),
			UploaderID: "user-1",
		})
		if err != nil {
			t.Fatalf("Insert: %v", err)
		}
		ids = append(ids, a.ID)
	}
	return ids
}

func doBulk(t *testing.T, mux *http.ServeMux, body string) (*httptest.ResponseRecorder, BulkResult) {
	t.Helper()
	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/admin/media/bulk", bytes.NewBufferString(body)), authedPrincipal())
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	var result BulkResult
	if w.Code == http.StatusOK {
		_ = json.Unmarshal(w.Body.Bytes(), &result)
	}
	return w, result
}

func TestBulkDelete(t *testing.T) {
	mux, store, _ := newBulkMux(t)
	ids := seedAssets(t, store, 3)

	body, _ := json.Marshal(BulkRequest{Op: BulkDelete, IDs: ids})
	w, result := doBulk(t, mux, string(body))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if result.Succeeded != 3 {
		t.Errorf("succeeded = %d, want 3", result.Succeeded)
	}
	// All assets should be soft-deleted.
	for _, id := range ids {
		if _, err := store.GetByID(context.Background(), id); err == nil {
			t.Errorf("asset %q still alive", id)
		}
	}
}

func TestBulkDeleteRecordsFailures(t *testing.T) {
	mux, store, _ := newBulkMux(t)
	ids := seedAssets(t, store, 2)
	// Pre-delete the second so the bulk op reports it as failed.
	_ = store.SoftDelete(context.Background(), ids[1])

	body, _ := json.Marshal(BulkRequest{Op: BulkDelete, IDs: ids})
	_, result := doBulk(t, mux, string(body))
	if result.Succeeded != 1 {
		t.Errorf("succeeded = %d, want 1", result.Succeeded)
	}
	if result.Failed[ids[1]] != "not_found" {
		t.Errorf("failed[%s] = %q, want not_found", ids[1], result.Failed[ids[1]])
	}
}

func TestBulkMove(t *testing.T) {
	mux, store, _ := newBulkMux(t)
	ids := seedAssets(t, store, 2)
	target := "folder-1"
	params, _ := json.Marshal(moveParams{CollectionID: &target})
	body, _ := json.Marshal(BulkRequest{Op: BulkMove, IDs: ids, Params: params})
	_, result := doBulk(t, mux, string(body))
	if result.Succeeded != 2 {
		t.Errorf("succeeded = %d, want 2", result.Succeeded)
	}
	for _, id := range ids {
		got, _ := store.GetByID(context.Background(), id)
		if got.CollectionID == nil || *got.CollectionID != target {
			t.Errorf("asset %q collection = %v", id, got.CollectionID)
		}
	}
}

func TestBulkTagAddMergesDedupedLowercase(t *testing.T) {
	mux, store, _ := newBulkMux(t)
	ids := seedAssets(t, store, 1)
	// Pre-tag with one tag so we can prove the merge.
	_ = store.SetTags(context.Background(), ids[0], []string{"hero"})

	params, _ := json.Marshal(tagParams{Add: []string{"Hero", "  Banner  ", "marketing"}})
	body, _ := json.Marshal(BulkRequest{Op: BulkTag, IDs: ids, Params: params})
	_, result := doBulk(t, mux, string(body))
	if result.Succeeded != 1 {
		t.Fatalf("succeeded = %d, want 1", result.Succeeded)
	}
	got, _ := store.GetByID(context.Background(), ids[0])
	want := []string{"banner", "hero", "marketing"}
	sort.Strings(got.Tags)
	if !equalSlices(got.Tags, want) {
		t.Errorf("tags = %v, want %v", got.Tags, want)
	}
}

func TestBulkTagRemove(t *testing.T) {
	mux, store, _ := newBulkMux(t)
	ids := seedAssets(t, store, 1)
	_ = store.SetTags(context.Background(), ids[0], []string{"a", "b", "c"})
	params, _ := json.Marshal(tagParams{Remove: []string{"b"}})
	body, _ := json.Marshal(BulkRequest{Op: BulkTag, IDs: ids, Params: params})
	_, _ = doBulk(t, mux, string(body))
	got, _ := store.GetByID(context.Background(), ids[0])
	if !equalSlices(got.Tags, []string{"a", "c"}) {
		t.Errorf("tags = %v, want [a c]", got.Tags)
	}
}

func TestBulkTagSetReplaces(t *testing.T) {
	mux, store, _ := newBulkMux(t)
	ids := seedAssets(t, store, 1)
	_ = store.SetTags(context.Background(), ids[0], []string{"old"})
	set := []string{"NewTag", "Another"}
	params, _ := json.Marshal(tagParams{Set: &set})
	body, _ := json.Marshal(BulkRequest{Op: BulkTag, IDs: ids, Params: params})
	_, _ = doBulk(t, mux, string(body))
	got, _ := store.GetByID(context.Background(), ids[0])
	if !equalSlices(got.Tags, []string{"another", "newtag"}) {
		t.Errorf("tags = %v", got.Tags)
	}
}

func TestBulkAIAltEnqueuesPerID(t *testing.T) {
	mux, store, captured := newBulkMux(t)
	ids := seedAssets(t, store, 3)
	body, _ := json.Marshal(BulkRequest{Op: BulkAIAlt, IDs: ids})
	_, result := doBulk(t, mux, string(body))
	if result.Succeeded != 3 {
		t.Errorf("succeeded = %d, want 3", result.Succeeded)
	}
	if len(*captured) != 3 {
		t.Errorf("captured = %d, want 3", len(*captured))
	}
}

func TestBulkAIAltStubProducesDeterministicAlt(t *testing.T) {
	store := NewMemoryStore(nil, nil)
	asset, _ := store.Insert(context.Background(), AssetCreate{
		Filename:   "x.png",
		MimeType:   "image/png",
		ByteSize:   42,
		StorageKey: "2026/01/x.png",
		SHA256:     bytes.Repeat([]byte{0x99}, 32),
		UploaderID: "user-1",
	})
	stub := &StubAltGenerator{Store: store}
	if err := stub.Enqueue(context.Background(), asset.ID); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	got, _ := store.GetByID(context.Background(), asset.ID)
	want := "auto-generated alt for image " + asset.ID
	if got.AltText != want {
		t.Errorf("AltText = %q, want %q", got.AltText, want)
	}
}

func TestBulkRejectsEmptyIDs(t *testing.T) {
	mux, _, _ := newBulkMux(t)
	body, _ := json.Marshal(BulkRequest{Op: BulkDelete, IDs: nil})
	w, _ := doBulk(t, mux, string(body))
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestBulkRejectsUnknownOp(t *testing.T) {
	mux, store, _ := newBulkMux(t)
	ids := seedAssets(t, store, 1)
	body := `{"op":"nuke","ids":["` + ids[0] + `"]}`
	w, _ := doBulk(t, mux, body)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
