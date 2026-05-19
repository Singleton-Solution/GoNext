package comments

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/router"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// testHarness wires Mount onto an http.ServeMux against a fresh
// MemoryStore + BasicPolicy. Each test constructs its own — no
// shared state between tests. The clock is pinned to a known
// instant so the UpdatedAt assertions are deterministic.
type testHarness struct {
	mux    *http.ServeMux
	store  *MemoryStore
	policy policy.Policy
	clock  time.Time
}

const testBase = "/api/v1/admin/comments"

func newTestHarness(t *testing.T) *testHarness {
	t.Helper()
	clock := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStoreWithClock(func() time.Time { return clock })
	pol := policy.NewBasicPolicy(policy.DefaultRoleCapabilities())
	mux := http.NewServeMux()
	if err := Mount(mux, testBase, Deps{
		Store:  store,
		Policy: pol,
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
		CurrentDisplayName: func(*http.Request) string {
			return "Mod Operator"
		},
	}); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	return &testHarness{mux: mux, store: store, policy: pol, clock: clock}
}

func editorPrincipal() policy.Principal {
	return policy.Principal{UserID: "user:9", Roles: []policy.Role{policy.RoleEditor}}
}

func authorPrincipal() policy.Principal {
	return policy.Principal{UserID: "user:2", Roles: []policy.Role{policy.RoleAuthor}}
}

func (h *testHarness) do(req *http.Request, pr *policy.Principal) *httptest.ResponseRecorder {
	if pr != nil {
		req = req.WithContext(policy.WithPrincipal(req.Context(), *pr))
	}
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	return rec
}

// sampleComment returns a seedable comment. Callers customise the
// status / post and any field that matters to the test.
func sampleComment(id, postID, postTitle string, status Status, createdAt time.Time) Comment {
	return Comment{
		ID:                id,
		PostID:            postID,
		PostTitle:         postTitle,
		Path:              labelFromID(id),
		AuthorUserID:      "user:42",
		AuthorDisplayName: "Jane",
		Content:           "a comment by " + id,
		ContentFormat:     "html",
		Status:            status,
		CreatedAt:         createdAt,
		UpdatedAt:         createdAt,
	}
}

// -----------------------------------------------------------------------------
// LIST
// -----------------------------------------------------------------------------

func TestList_HappyPath(t *testing.T) {
	h := newTestHarness(t)
	// Three rows in different states; the list should return all.
	t0 := time.Date(2026, 5, 17, 9, 0, 0, 0, time.UTC)
	h.store.Seed(sampleComment("c1", "p1", "Hello", StatusPending, t0))
	h.store.Seed(sampleComment("c2", "p1", "Hello", StatusApproved, t0.Add(time.Minute)))
	h.store.Seed(sampleComment("c3", "p2", "World", StatusSpam, t0.Add(2*time.Minute)))

	req := httptest.NewRequest("GET", testBase, nil)
	rec := h.do(req, ptr(editorPrincipal()))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var page router.Page[Comment]
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, rec.Body.String())
	}
	if got := len(page.Data); got != 3 {
		t.Fatalf("data: got %d, want 3", got)
	}
	// Newest first.
	if page.Data[0].ID != "c3" || page.Data[2].ID != "c1" {
		t.Errorf("order: got %v, want [c3 c2 c1]",
			[]string{page.Data[0].ID, page.Data[1].ID, page.Data[2].ID})
	}
}

func TestList_FilterByStatus(t *testing.T) {
	h := newTestHarness(t)
	t0 := time.Date(2026, 5, 17, 9, 0, 0, 0, time.UTC)
	h.store.Seed(sampleComment("c1", "p1", "Hello", StatusPending, t0))
	h.store.Seed(sampleComment("c2", "p1", "Hello", StatusApproved, t0))
	h.store.Seed(sampleComment("c3", "p1", "Hello", StatusSpam, t0))
	h.store.Seed(sampleComment("c4", "p1", "Hello", StatusTrash, t0))

	for _, st := range []Status{StatusPending, StatusApproved, StatusSpam, StatusTrash} {
		t.Run(string(st), func(t *testing.T) {
			req := httptest.NewRequest("GET", testBase+"?status="+string(st), nil)
			rec := h.do(req, ptr(editorPrincipal()))
			if rec.Code != http.StatusOK {
				t.Fatalf("status: got %d, want 200; body=%s", rec.Code, rec.Body.String())
			}
			var page router.Page[Comment]
			if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if len(page.Data) != 1 {
				t.Fatalf("data: got %d, want 1", len(page.Data))
			}
			if page.Data[0].Status != st {
				t.Errorf("status: got %q, want %q", page.Data[0].Status, st)
			}
		})
	}
}

func TestList_FilterByPostID(t *testing.T) {
	h := newTestHarness(t)
	t0 := time.Date(2026, 5, 17, 9, 0, 0, 0, time.UTC)
	h.store.Seed(sampleComment("c1", "p1", "Hello", StatusPending, t0))
	h.store.Seed(sampleComment("c2", "p2", "World", StatusPending, t0))
	h.store.Seed(sampleComment("c3", "p1", "Hello", StatusApproved, t0))

	req := httptest.NewRequest("GET", testBase+"?post_id=p1", nil)
	rec := h.do(req, ptr(editorPrincipal()))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var page router.Page[Comment]
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(page.Data) != 2 {
		t.Fatalf("data: got %d, want 2", len(page.Data))
	}
	for _, c := range page.Data {
		if c.PostID != "p1" {
			t.Errorf("post_id: got %q, want p1", c.PostID)
		}
	}
}

func TestList_FilterByUserID(t *testing.T) {
	h := newTestHarness(t)
	t0 := time.Date(2026, 5, 17, 9, 0, 0, 0, time.UTC)
	c1 := sampleComment("c1", "p1", "Hello", StatusPending, t0)
	c1.AuthorUserID = "user:alice"
	c2 := sampleComment("c2", "p1", "Hello", StatusPending, t0)
	c2.AuthorUserID = "user:bob"
	c3 := sampleComment("c3", "p1", "Hello", StatusPending, t0)
	c3.AuthorUserID = "" // anonymous
	h.store.Seed(c1)
	h.store.Seed(c2)
	h.store.Seed(c3)

	req := httptest.NewRequest("GET", testBase+"?user_id=user:alice", nil)
	rec := h.do(req, ptr(editorPrincipal()))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var page router.Page[Comment]
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(page.Data) != 1 || page.Data[0].ID != "c1" {
		t.Errorf("want only c1; got %+v", page.Data)
	}
}

func TestList_PaginationCursor(t *testing.T) {
	h := newTestHarness(t)
	t0 := time.Date(2026, 5, 17, 9, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		h.store.Seed(sampleComment(
			"c"+itoa(i), "p1", "Hello", StatusPending, t0.Add(time.Duration(i)*time.Minute),
		))
	}

	req := httptest.NewRequest("GET", testBase+"?limit=2", nil)
	rec := h.do(req, ptr(editorPrincipal()))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	var page router.Page[Comment]
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(page.Data) != 2 {
		t.Fatalf("data: got %d, want 2", len(page.Data))
	}
	if page.Pagination.NextCursor == "" {
		t.Errorf("next_cursor: got empty, want non-empty (more pages)")
	}
}

func TestList_InvalidStatus(t *testing.T) {
	h := newTestHarness(t)
	req := httptest.NewRequest("GET", testBase+"?status=approve", nil) // verb, not state
	rec := h.do(req, ptr(editorPrincipal()))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestList_EmptyReturnsEmptyArray(t *testing.T) {
	h := newTestHarness(t)
	req := httptest.NewRequest("GET", testBase, nil)
	rec := h.do(req, ptr(editorPrincipal()))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	// Spot-check that the body has "data":[], not "data":null. The
	// admin UI treats null as a surprise error.
	if !strings.Contains(rec.Body.String(), `"data":[]`) {
		t.Errorf("body: want \"data\":[], got %s", rec.Body.String())
	}
}

// -----------------------------------------------------------------------------
// AUTH
// -----------------------------------------------------------------------------

func TestAuth_AnonymousIsUnauthorized(t *testing.T) {
	h := newTestHarness(t)
	req := httptest.NewRequest("GET", testBase, nil)
	rec := h.do(req, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
}

func TestAuth_NonModeratorIsForbidden(t *testing.T) {
	h := newTestHarness(t)
	req := httptest.NewRequest("GET", testBase, nil)
	rec := h.do(req, ptr(authorPrincipal()))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status: got %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
}

func TestAuth_EditorCanList(t *testing.T) {
	h := newTestHarness(t)
	req := httptest.NewRequest("GET", testBase, nil)
	rec := h.do(req, ptr(editorPrincipal()))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

// -----------------------------------------------------------------------------
// UPDATE
// -----------------------------------------------------------------------------

func TestUpdate_EachStatus(t *testing.T) {
	t0 := time.Date(2026, 5, 17, 9, 0, 0, 0, time.UTC)
	for _, st := range []Status{StatusPending, StatusApproved, StatusSpam, StatusTrash} {
		t.Run(string(st), func(t *testing.T) {
			h := newTestHarness(t)
			h.store.Seed(sampleComment("c1", "p1", "Hello", StatusPending, t0))

			body := strings.NewReader(`{"status":"` + string(st) + `"}`)
			req := httptest.NewRequest("PATCH", testBase+"/c1", body)
			req.Header.Set("Content-Type", "application/json")
			rec := h.do(req, ptr(editorPrincipal()))
			if rec.Code != http.StatusOK {
				t.Fatalf("status: got %d, want 200; body=%s", rec.Code, rec.Body.String())
			}
			var out Comment
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if out.Status != st {
				t.Errorf("status: got %q, want %q", out.Status, st)
			}
			if !out.UpdatedAt.Equal(h.clock) {
				t.Errorf("updated_at: got %v, want %v", out.UpdatedAt, h.clock)
			}
		})
	}
}

func TestUpdate_NotFound(t *testing.T) {
	h := newTestHarness(t)
	body := strings.NewReader(`{"status":"approved"}`)
	req := httptest.NewRequest("PATCH", testBase+"/missing", body)
	rec := h.do(req, ptr(editorPrincipal()))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

func TestUpdate_InvalidStatus(t *testing.T) {
	h := newTestHarness(t)
	t0 := time.Date(2026, 5, 17, 9, 0, 0, 0, time.UTC)
	h.store.Seed(sampleComment("c1", "p1", "Hello", StatusPending, t0))
	body := strings.NewReader(`{"status":"flagged"}`)
	req := httptest.NewRequest("PATCH", testBase+"/c1", body)
	rec := h.do(req, ptr(editorPrincipal()))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestUpdate_MissingStatus(t *testing.T) {
	h := newTestHarness(t)
	t0 := time.Date(2026, 5, 17, 9, 0, 0, 0, time.UTC)
	h.store.Seed(sampleComment("c1", "p1", "Hello", StatusPending, t0))
	body := strings.NewReader(`{}`)
	req := httptest.NewRequest("PATCH", testBase+"/c1", body)
	rec := h.do(req, ptr(editorPrincipal()))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestUpdate_RejectsUnknownFields(t *testing.T) {
	h := newTestHarness(t)
	t0 := time.Date(2026, 5, 17, 9, 0, 0, 0, time.UTC)
	h.store.Seed(sampleComment("c1", "p1", "Hello", StatusPending, t0))
	body := strings.NewReader(`{"status":"approved","extra":true}`)
	req := httptest.NewRequest("PATCH", testBase+"/c1", body)
	rec := h.do(req, ptr(editorPrincipal()))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// -----------------------------------------------------------------------------
// BULK
// -----------------------------------------------------------------------------

func TestBulk_HappyPath(t *testing.T) {
	h := newTestHarness(t)
	t0 := time.Date(2026, 5, 17, 9, 0, 0, 0, time.UTC)
	h.store.Seed(sampleComment("c1", "p1", "Hello", StatusPending, t0))
	h.store.Seed(sampleComment("c2", "p1", "Hello", StatusPending, t0))
	h.store.Seed(sampleComment("c3", "p1", "Hello", StatusPending, t0))

	body := strings.NewReader(`{"ids":["c1","c2","c3"],"action":"approve"}`)
	req := httptest.NewRequest("POST", testBase+"/bulk", body)
	rec := h.do(req, ptr(editorPrincipal()))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp bulkResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Count != 3 {
		t.Errorf("count: got %d, want 3", resp.Count)
	}
	// Verify each row landed in 'approved'.
	for _, c := range resp.Updated {
		if c.Status != StatusApproved {
			t.Errorf("status for %s: got %q, want approved", c.ID, c.Status)
		}
	}
	// And the store reflects the change.
	for _, id := range []string{"c1", "c2", "c3"} {
		got, _ := h.store.Get(context.Background(), id)
		if got.Status != StatusApproved {
			t.Errorf("store status for %s: got %q, want approved", id, got.Status)
		}
	}
}

func TestBulk_Atomicity_OneBadIDRejectsWhole(t *testing.T) {
	h := newTestHarness(t)
	t0 := time.Date(2026, 5, 17, 9, 0, 0, 0, time.UTC)
	h.store.Seed(sampleComment("c1", "p1", "Hello", StatusPending, t0))
	h.store.Seed(sampleComment("c2", "p1", "Hello", StatusPending, t0))

	body := strings.NewReader(`{"ids":["c1","c2","missing"],"action":"spam"}`)
	req := httptest.NewRequest("POST", testBase+"/bulk", body)
	rec := h.do(req, ptr(editorPrincipal()))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status: got %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	// Neither c1 nor c2 should have changed status — atomicity.
	for _, id := range []string{"c1", "c2"} {
		got, _ := h.store.Get(context.Background(), id)
		if got.Status != StatusPending {
			t.Errorf("atomicity: %s status got %q, want pending", id, got.Status)
		}
	}
}

func TestBulk_InvalidAction(t *testing.T) {
	h := newTestHarness(t)
	body := strings.NewReader(`{"ids":["c1"],"action":"pending"}`) // status, not verb
	req := httptest.NewRequest("POST", testBase+"/bulk", body)
	rec := h.do(req, ptr(editorPrincipal()))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestBulk_EmptyIDs(t *testing.T) {
	h := newTestHarness(t)
	body := strings.NewReader(`{"ids":[],"action":"approve"}`)
	req := httptest.NewRequest("POST", testBase+"/bulk", body)
	rec := h.do(req, ptr(editorPrincipal()))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestBulk_NoAuth(t *testing.T) {
	h := newTestHarness(t)
	body := strings.NewReader(`{"ids":["c1"],"action":"approve"}`)
	req := httptest.NewRequest("POST", testBase+"/bulk", body)
	rec := h.do(req, ptr(authorPrincipal()))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status: got %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
}

// -----------------------------------------------------------------------------
// REPLY
// -----------------------------------------------------------------------------

func TestReply_CreatesLtreeChild(t *testing.T) {
	h := newTestHarness(t)
	t0 := time.Date(2026, 5, 17, 9, 0, 0, 0, time.UTC)
	parent := sampleComment("c1", "p1", "Hello", StatusApproved, t0)
	// Use a realistic-ish UUID for the parent so the label
	// computation is exercised properly.
	parent.ID = "00000000-0000-7000-8000-000000000001"
	parent.Path = labelFromID(parent.ID)
	h.store.Seed(parent)

	body := strings.NewReader(`{"content":"thanks for the feedback"}`)
	req := httptest.NewRequest("POST", testBase+"/"+parent.ID+"/reply", body)
	rec := h.do(req, ptr(editorPrincipal()))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var child Comment
	if err := json.Unmarshal(rec.Body.Bytes(), &child); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if child.ParentID != parent.ID {
		t.Errorf("parent_id: got %q, want %q", child.ParentID, parent.ID)
	}
	if child.PostID != parent.PostID {
		t.Errorf("post_id: got %q, want %q", child.PostID, parent.PostID)
	}
	// path = parent.path || self.label.
	want := parent.Path + "." + labelFromID(child.ID)
	if child.Path != want {
		t.Errorf("path: got %q, want %q", child.Path, want)
	}
	// Author wiring.
	if child.AuthorUserID != "user:9" {
		t.Errorf("author_user_id: got %q, want user:9", child.AuthorUserID)
	}
	if child.AuthorDisplayName != "Mod Operator" {
		t.Errorf("author_display_name: got %q, want Mod Operator", child.AuthorDisplayName)
	}
	// Moderator replies land approved.
	if child.Status != StatusApproved {
		t.Errorf("status: got %q, want approved", child.Status)
	}
}

func TestReply_NotFound(t *testing.T) {
	h := newTestHarness(t)
	body := strings.NewReader(`{"content":"hi"}`)
	req := httptest.NewRequest("POST", testBase+"/missing/reply", body)
	rec := h.do(req, ptr(editorPrincipal()))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

func TestReply_EmptyContent(t *testing.T) {
	h := newTestHarness(t)
	t0 := time.Date(2026, 5, 17, 9, 0, 0, 0, time.UTC)
	h.store.Seed(sampleComment("c1", "p1", "Hello", StatusApproved, t0))
	body := strings.NewReader(`{"content":"   "}`)
	req := httptest.NewRequest("POST", testBase+"/c1/reply", body)
	rec := h.do(req, ptr(editorPrincipal()))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestReply_NoAuth(t *testing.T) {
	h := newTestHarness(t)
	t0 := time.Date(2026, 5, 17, 9, 0, 0, 0, time.UTC)
	h.store.Seed(sampleComment("c1", "p1", "Hello", StatusApproved, t0))
	body := strings.NewReader(`{"content":"hello"}`)
	req := httptest.NewRequest("POST", testBase+"/c1/reply", body)
	rec := h.do(req, ptr(authorPrincipal()))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status: got %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
}

// -----------------------------------------------------------------------------
// MOUNT
// -----------------------------------------------------------------------------

func TestMount_RequiresStore(t *testing.T) {
	if err := Mount(http.NewServeMux(), "/x", Deps{
		Policy: policy.NewBasicPolicy(policy.DefaultRoleCapabilities()),
	}); err == nil {
		t.Fatal("Mount with nil Store: want error, got nil")
	}
}

func TestMount_RequiresPolicy(t *testing.T) {
	if err := Mount(http.NewServeMux(), "/x", Deps{
		Store: NewMemoryStore(),
	}); err == nil {
		t.Fatal("Mount with nil Policy: want error, got nil")
	}
}

// -----------------------------------------------------------------------------
// helpers
// -----------------------------------------------------------------------------

func ptr[T any](v T) *T { return &v }
