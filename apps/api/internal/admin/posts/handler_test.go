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

	"github.com/google/uuid"

	"github.com/Singleton-Solution/GoNext/packages/go/policy"
	"github.com/Singleton-Solution/GoNext/packages/go/revisions"
)

// fakePostUpdater captures the last SetContentBlocks call so tests can
// assert the restore writes the materialized JSON to the post layer.
type fakePostUpdater struct {
	mu        sync.Mutex
	lastID    string
	lastRaw   json.RawMessage
	failNext  bool
}

func (f *fakePostUpdater) SetContentBlocks(_ context.Context, postID string, raw json.RawMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failNext {
		f.failNext = false
		return errInjected
	}
	f.lastID = postID
	f.lastRaw = append(f.lastRaw[:0], raw...)
	return nil
}

var errInjected = &injectedErr{}

type injectedErr struct{}

func (*injectedErr) Error() string { return "injected" }

// testHarness wires a memory revisions store + fake post updater into
// a mux, and pre-loads one snapshot revision so the list + restore
// tests have content to point at.
type testHarness struct {
	mux     *http.ServeMux
	revs    *revisions.MemoryStore
	posts   *fakePostUpdater
	postID  uuid.UUID
	revID   uuid.UUID
	author  uuid.UUID
}

func newHarness(t *testing.T) *testHarness {
	t.Helper()

	revs := revisions.NewMemoryStore()
	revs.NowFunc = func() time.Time {
		return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	posts := &fakePostUpdater{}

	mux := http.NewServeMux()
	if err := Mount(mux, "/api/v1/admin/posts", Deps{
		Revisions: revs,
		Posts:     posts,
		Policy: policy.NewBasicPolicy(map[policy.Role]policy.CapabilitySet{
			policy.RoleEditor: policy.NewCapabilitySet(policy.CapEditPosts),
		}),
		Now: func() time.Time {
			return time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
		},
	}); err != nil {
		t.Fatalf("Mount: %v", err)
	}

	postID := uuid.New()
	author := uuid.New()
	revID, err := revs.Save(context.Background(), revisions.Revision{
		PostID:        postID,
		AuthorID:      author,
		Kind:          revisions.Manual,
		Title:         "Hello",
		Excerpt:       "World",
		ContentBlocks: json.RawMessage(`{"version":1,"blocks":[{"type":"core/paragraph","attributes":{"content":"hi"}}]}`),
	}, revisions.WithForceSnapshot())
	if err != nil {
		t.Fatalf("seed revision: %v", err)
	}

	return &testHarness{
		mux:    mux,
		revs:   revs,
		posts:  posts,
		postID: postID,
		revID:  revID,
		author: author,
	}
}

// editorRequest stamps a request with an editor principal so the gate
// admits it. Test helper — production wiring sits in the auth
// middleware.
func editorRequest(method, path string, body []byte) *http.Request {
	var req *http.Request
	if body == nil {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(string(body)))
	}
	pr := policy.Principal{
		UserID: uuid.NewString(),
		Roles:  []policy.Role{policy.RoleEditor},
	}
	return req.WithContext(policy.WithPrincipal(req.Context(), pr))
}

func TestListRevisions_ReturnsRows(t *testing.T) {
	h := newHarness(t)

	req := editorRequest(http.MethodGet,
		"/api/v1/admin/posts/"+h.postID.String()+"/revisions", nil)
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		Data []RevisionView `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if len(got.Data) != 1 {
		t.Fatalf("want 1 revision, got %d", len(got.Data))
	}
	if got.Data[0].ID != h.revID.String() {
		t.Fatalf("revision id mismatch: %s != %s", got.Data[0].ID, h.revID.String())
	}
	if !got.Data[0].IsSnapshot {
		t.Fatal("seed revision must be a snapshot")
	}
}

func TestListRevisions_Unauthenticated(t *testing.T) {
	h := newHarness(t)
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/admin/posts/"+h.postID.String()+"/revisions", nil)
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401", rec.Code)
	}
}

func TestListRevisions_Forbidden(t *testing.T) {
	h := newHarness(t)
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/admin/posts/"+h.postID.String()+"/revisions", nil)
	pr := policy.Principal{
		UserID: uuid.NewString(),
		Roles:  []policy.Role{policy.RoleSubscriber},
	}
	req = req.WithContext(policy.WithPrincipal(req.Context(), pr))
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d want 403; body=%s", rec.Code, rec.Body.String())
	}
}

func TestListRevisions_InvalidPostID(t *testing.T) {
	h := newHarness(t)
	req := editorRequest(http.MethodGet, "/api/v1/admin/posts/not-a-uuid/revisions", nil)
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestRestoreRevision_Success(t *testing.T) {
	h := newHarness(t)

	path := "/api/v1/admin/posts/" + h.postID.String() + "/revisions/" + h.revID.String() + "/restore"
	req := editorRequest(http.MethodPost, path, nil)
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if h.posts.lastID != h.postID.String() {
		t.Fatalf("post updater not called with right id: %q", h.posts.lastID)
	}
	if !strings.Contains(string(h.posts.lastRaw), `"core/paragraph"`) {
		t.Fatalf("post updater raw missing block: %s", string(h.posts.lastRaw))
	}
	// Restore should have appended an audit "manual" revision.
	rows, err := h.revs.List(context.Background(), h.postID, revisions.Filter{Limit: 10})
	if err != nil {
		t.Fatalf("list after restore: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 revisions after restore, got %d", len(rows))
	}
	var auditRow revisions.Revision
	for _, r := range rows {
		if r.ID != h.revID {
			auditRow = r
		}
	}
	if !strings.HasPrefix(auditRow.Comment, "Restored from revision ") {
		t.Fatalf("audit comment not set: %q", auditRow.Comment)
	}
}

func TestRestoreRevision_CrossPostMismatch_404(t *testing.T) {
	h := newHarness(t)

	otherPost := uuid.New()
	path := "/api/v1/admin/posts/" + otherPost.String() + "/revisions/" + h.revID.String() + "/restore"
	req := editorRequest(http.MethodPost, path, nil)
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404; body=%s", rec.Code, rec.Body.String())
	}
	if h.posts.lastID != "" {
		t.Fatal("post updater must not be called on cross-post mismatch")
	}
}

func TestRestoreRevision_PostUpdateFails_500(t *testing.T) {
	h := newHarness(t)
	h.posts.failNext = true

	path := "/api/v1/admin/posts/" + h.postID.String() + "/revisions/" + h.revID.String() + "/restore"
	req := editorRequest(http.MethodPost, path, nil)
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d want 500; body=%s", rec.Code, rec.Body.String())
	}
}

func TestRestoreRevision_UnknownRevision_404(t *testing.T) {
	h := newHarness(t)
	missing := uuid.New()
	path := "/api/v1/admin/posts/" + h.postID.String() + "/revisions/" + missing.String() + "/restore"
	req := editorRequest(http.MethodPost, path, nil)
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404; body=%s", rec.Code, rec.Body.String())
	}
}
