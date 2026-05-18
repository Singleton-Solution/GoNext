package posts

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// autosaveHarness wires both the posts mount and the autosave mount
// against shared stores so a test can seed posts, then exercise the
// autosave routes. The shape mirrors testHarness in handlers_test.go;
// we don't reuse that helper because it doesn't know about autosave.
type autosaveHarness struct {
	mux       *http.ServeMux
	posts     *MemoryStore
	autosaves *MemoryAutosaveStore
	policy    policy.Policy
	base      string
}

func newAutosaveHarness(t *testing.T) *autosaveHarness {
	t.Helper()
	mux := http.NewServeMux()
	postStore := NewMemoryStore()
	autoStore := NewMemoryAutosaveStore()
	pol := policy.NewBasicPolicy(policy.DefaultRoleCapabilities())
	base := "/api/v1/posts"

	if err := Mount(mux, base, Deps{
		Store:    postStore,
		Policy:   pol,
		PostType: PostTypePost,
	}); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	if err := MountAutosave(mux, base, AutosaveDeps{
		PostStore:     postStore,
		AutosaveStore: autoStore,
		Policy:        pol,
		PostType:      PostTypePost,
	}); err != nil {
		t.Fatalf("MountAutosave: %v", err)
	}
	return &autosaveHarness{
		mux:       mux,
		posts:     postStore,
		autosaves: autoStore,
		policy:    pol,
		base:      base,
	}
}

func (h *autosaveHarness) do(req *http.Request, pr *policy.Principal) *httptest.ResponseRecorder {
	if pr != nil {
		req = req.WithContext(policy.WithPrincipal(req.Context(), *pr))
	}
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	return rec
}

// -----------------------------------------------------------------------------
// PUT
// -----------------------------------------------------------------------------

func TestAutosave_Put_HappyPath(t *testing.T) {
	t.Parallel()
	h := newAutosaveHarness(t)
	pr := authorPrincipal("u1")
	created, _ := h.posts.Create(context.Background(), PostTypePost, "u1", CreateInput{})

	body := strings.NewReader(`{"blocks":[{"type":"core/paragraph","attributes":{"text":"hi"}}]}`)
	req := httptest.NewRequest("POST", h.base+"/"+created.ID+"/autosave", body)
	rec := h.do(req, &pr)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got Autosave
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.PostID != created.ID || got.UserID != "u1" {
		t.Errorf("got = %+v", got)
	}
	if len(got.Blocks) == 0 || string(got.Blocks)[0] != '[' {
		t.Errorf("blocks not returned: %s", got.Blocks)
	}
}

func TestAutosave_Put_Unauthenticated(t *testing.T) {
	t.Parallel()
	h := newAutosaveHarness(t)
	created, _ := h.posts.Create(context.Background(), PostTypePost, "u1", CreateInput{})

	body := strings.NewReader(`{"blocks":[]}`)
	req := httptest.NewRequest("POST", h.base+"/"+created.ID+"/autosave", body)
	rec := h.do(req, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestAutosave_Put_Forbidden(t *testing.T) {
	t.Parallel()
	h := newAutosaveHarness(t)
	created, _ := h.posts.Create(context.Background(), PostTypePost, "u1", CreateInput{})

	// Subscriber has no edit cap.
	pr := subscriberPrincipal("u2")
	body := strings.NewReader(`{"blocks":[]}`)
	req := httptest.NewRequest("POST", h.base+"/"+created.ID+"/autosave", body)
	rec := h.do(req, &pr)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestAutosave_Put_LockedByOtherUser_Returns423(t *testing.T) {
	t.Parallel()
	h := newAutosaveHarness(t)
	// Editor seeds the post and holds the lock.
	created, _ := h.posts.Create(context.Background(), PostTypePost, "u-owner", CreateInput{})
	h.autosaves.SetLockHolder(created.ID, "u-owner")

	// A different editor (with edit_others cap) tries to autosave.
	pr := editorPrincipal("u-intruder")
	body := strings.NewReader(`{"blocks":[]}`)
	req := httptest.NewRequest("POST", h.base+"/"+created.ID+"/autosave", body)
	rec := h.do(req, &pr)
	if rec.Code != http.StatusLocked {
		t.Errorf("status = %d, want 423", rec.Code)
	}
}

func TestAutosave_Put_SameUserSucceedsEvenWhenTheyHoldLock(t *testing.T) {
	t.Parallel()
	h := newAutosaveHarness(t)
	pr := authorPrincipal("u1")
	created, _ := h.posts.Create(context.Background(), PostTypePost, "u1", CreateInput{})

	// User holds their own lock — that's the common case mid-edit.
	h.autosaves.SetLockHolder(created.ID, "u1")
	body := strings.NewReader(`{"blocks":[]}`)
	req := httptest.NewRequest("POST", h.base+"/"+created.ID+"/autosave", body)
	rec := h.do(req, &pr)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestAutosave_Put_PostNotFound(t *testing.T) {
	t.Parallel()
	h := newAutosaveHarness(t)
	pr := authorPrincipal("u1")
	body := strings.NewReader(`{"blocks":[]}`)
	req := httptest.NewRequest("POST", h.base+"/does-not-exist/autosave", body)
	rec := h.do(req, &pr)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestAutosave_Put_InvalidBlocks(t *testing.T) {
	t.Parallel()
	h := newAutosaveHarness(t)
	pr := authorPrincipal("u1")
	created, _ := h.posts.Create(context.Background(), PostTypePost, "u1", CreateInput{})

	// blocks must be a JSON array, not an object.
	body := strings.NewReader(`{"blocks":{"oops":true}}`)
	req := httptest.NewRequest("POST", h.base+"/"+created.ID+"/autosave", body)
	rec := h.do(req, &pr)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestAutosave_Put_MissingBlocks(t *testing.T) {
	t.Parallel()
	h := newAutosaveHarness(t)
	pr := authorPrincipal("u1")
	created, _ := h.posts.Create(context.Background(), PostTypePost, "u1", CreateInput{})

	body := strings.NewReader(`{}`)
	req := httptest.NewRequest("POST", h.base+"/"+created.ID+"/autosave", body)
	rec := h.do(req, &pr)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// -----------------------------------------------------------------------------
// GET
// -----------------------------------------------------------------------------

func TestAutosave_Get_RoundTrip(t *testing.T) {
	t.Parallel()
	h := newAutosaveHarness(t)
	pr := authorPrincipal("u1")
	created, _ := h.posts.Create(context.Background(), PostTypePost, "u1", CreateInput{})

	// First write, then read it back.
	wbody := strings.NewReader(`{"blocks":[{"type":"core/heading","attributes":{"text":"hi"}}]}`)
	wreq := httptest.NewRequest("POST", h.base+"/"+created.ID+"/autosave", wbody)
	if rec := h.do(wreq, &pr); rec.Code != http.StatusOK {
		t.Fatalf("put status = %d", rec.Code)
	}

	rreq := httptest.NewRequest("GET", h.base+"/"+created.ID+"/autosave", nil)
	rec := h.do(rreq, &pr)
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got Autosave
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.PostID != created.ID {
		t.Errorf("post_id = %q", got.PostID)
	}
	if !strings.Contains(string(got.Blocks), "core/heading") {
		t.Errorf("blocks not preserved: %s", got.Blocks)
	}
}

func TestAutosave_Get_NoAutosave_Returns204(t *testing.T) {
	t.Parallel()
	h := newAutosaveHarness(t)
	pr := authorPrincipal("u1")
	created, _ := h.posts.Create(context.Background(), PostTypePost, "u1", CreateInput{})

	req := httptest.NewRequest("GET", h.base+"/"+created.ID+"/autosave", nil)
	rec := h.do(req, &pr)
	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("204 has body: %s", rec.Body.String())
	}
}

func TestAutosave_Get_Unauthenticated(t *testing.T) {
	t.Parallel()
	h := newAutosaveHarness(t)
	created, _ := h.posts.Create(context.Background(), PostTypePost, "u1", CreateInput{})
	req := httptest.NewRequest("GET", h.base+"/"+created.ID+"/autosave", nil)
	rec := h.do(req, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestAutosave_Get_OtherUserCannotSeeMyDraft(t *testing.T) {
	t.Parallel()
	h := newAutosaveHarness(t)
	created, _ := h.posts.Create(context.Background(), PostTypePost, "u1", CreateInput{})

	// u1 autosaves.
	pr1 := authorPrincipal("u1")
	wbody := strings.NewReader(`{"blocks":[{"type":"core/paragraph","attributes":{"text":"mine"}}]}`)
	wreq := httptest.NewRequest("POST", h.base+"/"+created.ID+"/autosave", wbody)
	if rec := h.do(wreq, &pr1); rec.Code != http.StatusOK {
		t.Fatalf("u1 put status = %d", rec.Code)
	}

	// Editor (different user) reads — should see 204 (their own draft is empty).
	pr2 := editorPrincipal("u2")
	rreq := httptest.NewRequest("GET", h.base+"/"+created.ID+"/autosave", nil)
	rec := h.do(rreq, &pr2)
	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204 (per-user keying)", rec.Code)
	}
}

// -----------------------------------------------------------------------------
// Race: two browsers attempt to autosave simultaneously
// -----------------------------------------------------------------------------

func TestAutosave_Race_FirstHoldsLock_SecondGets423(t *testing.T) {
	t.Parallel()
	h := newAutosaveHarness(t)
	created, _ := h.posts.Create(context.Background(), PostTypePost, "u-owner", CreateInput{})

	// u-owner takes the lock first.
	h.autosaves.SetLockHolder(created.ID, "u-owner")

	// Now fire both writes concurrently. u-owner's must succeed; the
	// intruder's must 423.
	var wg sync.WaitGroup
	wg.Add(2)
	codes := make([]int, 2)

	prOwner := authorPrincipal("u-owner")
	prIntruder := editorPrincipal("u-intruder")

	go func() {
		defer wg.Done()
		body := strings.NewReader(`{"blocks":[]}`)
		req := httptest.NewRequest("POST", h.base+"/"+created.ID+"/autosave", body)
		rec := h.do(req, &prOwner)
		codes[0] = rec.Code
	}()
	go func() {
		defer wg.Done()
		body := strings.NewReader(`{"blocks":[]}`)
		req := httptest.NewRequest("POST", h.base+"/"+created.ID+"/autosave", body)
		rec := h.do(req, &prIntruder)
		codes[1] = rec.Code
	}()
	wg.Wait()

	if codes[0] != http.StatusOK {
		t.Errorf("owner code = %d, want 200", codes[0])
	}
	if codes[1] != http.StatusLocked {
		t.Errorf("intruder code = %d, want 423", codes[1])
	}
}

// -----------------------------------------------------------------------------
// MountAutosave validation
// -----------------------------------------------------------------------------

func TestMountAutosave_RejectsMissingDeps(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	if err := MountAutosave(mux, "/x", AutosaveDeps{}); err == nil {
		t.Error("MountAutosave with empty Deps should error")
	}
	if err := MountAutosave(mux, "/x", AutosaveDeps{
		PostStore:     NewMemoryStore(),
		AutosaveStore: NewMemoryAutosaveStore(),
		Policy:        policy.NewBasicPolicy(nil),
		PostType:      "unknown",
	}); err == nil {
		t.Error("MountAutosave with unknown PostType should error")
	}
}

// -----------------------------------------------------------------------------
// Store-level: clock pinning
// -----------------------------------------------------------------------------

func TestMemoryAutosaveStore_UpdatedAtUsesClock(t *testing.T) {
	t.Parallel()
	s := NewMemoryAutosaveStore()
	pinned := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	s.SetNow(func() time.Time { return pinned })

	got, err := s.Put(context.Background(), "post-1", "user-1", json.RawMessage(`[]`))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if !got.UpdatedAt.Equal(pinned) {
		t.Errorf("updated_at = %v, want %v", got.UpdatedAt, pinned)
	}
}
