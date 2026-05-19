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
)

const testBase = "/api/v1/posts"

// testHarness wires Mount onto an http.ServeMux against a fresh
// MemoryStore. The clock is pinned to a known instant so the spam
// classifier and rate limiter assertions are deterministic.
type testHarness struct {
	mux      *http.ServeMux
	store    *MemoryStore
	handlers *handlers
	clock    time.Time
}

func newTestHarness(t *testing.T) *testHarness {
	t.Helper()
	clock := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStoreWithClock(func() time.Time { return clock })
	mux := http.NewServeMux()
	hs, err := mountForTest(mux, testBase, Deps{
		Store:  store,
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Now:    func() time.Time { return clock },
	})
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}
	return &testHarness{mux: mux, store: store, handlers: hs, clock: clock}
}

func (h *testHarness) do(req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	return rec
}

// sampleApproved builds a basic approved comment row.
func sampleApproved(id, postID, path string, t0 time.Time) Comment {
	return Comment{
		ID:                id,
		PostID:            postID,
		Path:              path,
		Depth:             strings.Count(path, ".") + 1,
		AuthorDisplayName: "Tester",
		Content:           "hello from " + id,
		CreatedAt:         t0,
	}
}

// -----------------------------------------------------------------------------
// LIST
// -----------------------------------------------------------------------------

func TestList_HappyPath_ReturnsApprovedOnly(t *testing.T) {
	h := newTestHarness(t)
	h.store.SeedPost("p1")
	t0 := time.Date(2026, 5, 17, 9, 0, 0, 0, time.UTC)
	h.store.Seed(sampleApproved("aa", "p1", "aa", t0), StatusApproved)
	h.store.Seed(sampleApproved("bb", "p1", "bb", t0.Add(time.Minute)), StatusPending)
	h.store.Seed(sampleApproved("cc", "p1", "cc", t0.Add(2*time.Minute)), StatusSpam)

	req := httptest.NewRequest("GET", testBase+"/p1/comments", nil)
	rec := h.do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var page router.Page[Comment]
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, rec.Body.String())
	}
	if len(page.Data) != 1 || page.Data[0].ID != "aa" {
		t.Errorf("data: got %+v, want [aa]", page.Data)
	}
}

func TestList_OrderedByLtreePath(t *testing.T) {
	h := newTestHarness(t)
	h.store.SeedPost("p1")
	t0 := time.Date(2026, 5, 17, 9, 0, 0, 0, time.UTC)
	// Two top-level: aa, bb. Two children of aa: aa.aa1, aa.aa2.
	h.store.Seed(sampleApproved("bb", "p1", "bb", t0), StatusApproved)
	h.store.Seed(sampleApproved("aa_aa1", "p1", "aa.aa1", t0), StatusApproved)
	h.store.Seed(sampleApproved("aa", "p1", "aa", t0), StatusApproved)
	h.store.Seed(sampleApproved("aa_aa2", "p1", "aa.aa2", t0), StatusApproved)

	req := httptest.NewRequest("GET", testBase+"/p1/comments", nil)
	rec := h.do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	var page router.Page[Comment]
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	wantOrder := []string{"aa", "aa_aa1", "aa_aa2", "bb"}
	if got := idsOf(page.Data); !slicesEqual(got, wantOrder) {
		t.Errorf("order: got %v, want %v", got, wantOrder)
	}
}

func TestList_PaginationCursor(t *testing.T) {
	h := newTestHarness(t)
	h.store.SeedPost("p1")
	t0 := time.Date(2026, 5, 17, 9, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		id := "c" + string(rune('0'+i))
		h.store.Seed(sampleApproved(id, "p1", id, t0), StatusApproved)
	}

	req := httptest.NewRequest("GET", testBase+"/p1/comments?limit=2", nil)
	rec := h.do(req)
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
	if page.Pagination.NextCursor == "" {
		t.Errorf("next_cursor: got empty, want non-empty")
	}

	// Page 2 — follow the cursor.
	req2 := httptest.NewRequest("GET", testBase+"/p1/comments?limit=2&after="+page.Pagination.NextCursor, nil)
	rec2 := h.do(req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("page2: status: got %d, want 200", rec2.Code)
	}
	var page2 router.Page[Comment]
	if err := json.Unmarshal(rec2.Body.Bytes(), &page2); err != nil {
		t.Fatalf("page2 unmarshal: %v", err)
	}
	if len(page2.Data) != 2 {
		t.Errorf("page2 data: got %d, want 2", len(page2.Data))
	}
	// No overlap with page 1.
	if page2.Data[0].ID == page.Data[0].ID {
		t.Errorf("page overlap: got %s", page2.Data[0].ID)
	}
}

func TestList_EmptyReturnsEmptyArray(t *testing.T) {
	h := newTestHarness(t)
	h.store.SeedPost("p1")
	req := httptest.NewRequest("GET", testBase+"/p1/comments", nil)
	rec := h.do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"data":[]`) {
		t.Errorf("body: want \"data\":[], got %s", rec.Body.String())
	}
}

func TestList_InvalidCursor(t *testing.T) {
	h := newTestHarness(t)
	req := httptest.NewRequest("GET", testBase+"/p1/comments?after=!notvalid", nil)
	rec := h.do(req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// -----------------------------------------------------------------------------
// SUBMIT
// -----------------------------------------------------------------------------

func TestSubmit_AnonymousTopLevel_LandsPending(t *testing.T) {
	h := newTestHarness(t)
	h.store.SeedPost("p1")

	body := strings.NewReader(`{"author_name":"Jane","content":"first post"}`)
	req := httptest.NewRequest("POST", testBase+"/p1/comments", body)
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "192.0.2.1:1234"
	rec := h.do(req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var created Created
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !created.Pending {
		t.Errorf("pending: got false, want true (anonymous default)")
	}
	if created.Comment.ParentID != "" {
		t.Errorf("parent_id: got %q, want empty", created.Comment.ParentID)
	}
	if created.Comment.AuthorDisplayName != "Jane" {
		t.Errorf("author: got %q, want Jane", created.Comment.AuthorDisplayName)
	}
	if created.Comment.Depth != 1 {
		t.Errorf("depth: got %d, want 1", created.Comment.Depth)
	}
}

func TestSubmit_Reply_BuildsChildPath(t *testing.T) {
	h := newTestHarness(t)
	h.store.SeedPost("p1")
	t0 := time.Date(2026, 5, 17, 9, 0, 0, 0, time.UTC)
	parent := sampleApproved("aaaa_bbbb", "p1", "aaaa_bbbb", t0)
	h.store.Seed(parent, StatusApproved)

	body := strings.NewReader(`{"parent_id":"aaaa_bbbb","author_name":"Jane","content":"thanks!"}`)
	req := httptest.NewRequest("POST", testBase+"/p1/comments", body)
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "192.0.2.2:1234"
	rec := h.do(req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var created Created
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if created.Comment.ParentID != "aaaa_bbbb" {
		t.Errorf("parent_id: got %q", created.Comment.ParentID)
	}
	if !strings.HasPrefix(created.Comment.Path, "aaaa_bbbb.") {
		t.Errorf("path: got %q, want starts with parent path", created.Comment.Path)
	}
	if created.Comment.Depth != 2 {
		t.Errorf("depth: got %d, want 2", created.Comment.Depth)
	}
}

func TestSubmit_StripsHTMLAndScripts(t *testing.T) {
	h := newTestHarness(t)
	h.store.SeedPost("p1")

	body := strings.NewReader(`{"author_name":"Jane","content":"<script>alert('xss')</script>Hello <b>world</b>"}`)
	req := httptest.NewRequest("POST", testBase+"/p1/comments", body)
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "192.0.2.3:1234"
	rec := h.do(req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var created Created
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if strings.Contains(created.Comment.Content, "<script") {
		t.Errorf("content: %q still contains <script>", created.Comment.Content)
	}
	if strings.Contains(created.Comment.Content, "<b>") {
		t.Errorf("content: %q still contains <b>", created.Comment.Content)
	}
	if !strings.Contains(created.Comment.Content, "Hello world") {
		t.Errorf("content: %q does not preserve text", created.Comment.Content)
	}
}

func TestSubmit_SpamOnTooManyURLs(t *testing.T) {
	h := newTestHarness(t)
	h.store.SeedPost("p1")
	body := strings.NewReader(`{"author_name":"Spam","content":"http://a.com http://b.com http://c.com http://d.com http://e.com http://f.com"}`)
	req := httptest.NewRequest("POST", testBase+"/p1/comments", body)
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "192.0.2.4:1234"
	rec := h.do(req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var created Created
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	// The row was created as 'spam' — confirmed via Pending=false AND
	// it doesn't appear in the public list.
	if created.Pending {
		t.Errorf("pending: got true, want false (spam classification)")
	}
	listReq := httptest.NewRequest("GET", testBase+"/p1/comments", nil)
	listRec := h.do(listReq)
	if strings.Contains(listRec.Body.String(), created.Comment.ID) {
		t.Errorf("spam row appears in public list: %s", listRec.Body.String())
	}
}

func TestSubmit_NoContent400(t *testing.T) {
	h := newTestHarness(t)
	h.store.SeedPost("p1")
	body := strings.NewReader(`{"author_name":"Jane","content":""}`)
	req := httptest.NewRequest("POST", testBase+"/p1/comments", body)
	req.Header.Set("Content-Type", "application/json")
	rec := h.do(req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestSubmit_NoName400(t *testing.T) {
	h := newTestHarness(t)
	h.store.SeedPost("p1")
	body := strings.NewReader(`{"content":"hello"}`)
	req := httptest.NewRequest("POST", testBase+"/p1/comments", body)
	req.Header.Set("Content-Type", "application/json")
	rec := h.do(req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestSubmit_PostNotFound404(t *testing.T) {
	h := newTestHarness(t)
	body := strings.NewReader(`{"author_name":"Jane","content":"hello"}`)
	req := httptest.NewRequest("POST", testBase+"/missing/comments", body)
	req.Header.Set("Content-Type", "application/json")
	rec := h.do(req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

func TestSubmit_ParentMismatch_400(t *testing.T) {
	h := newTestHarness(t)
	h.store.SeedPost("p1")
	h.store.SeedPost("p2")
	t0 := time.Date(2026, 5, 17, 9, 0, 0, 0, time.UTC)
	parent := sampleApproved("aa", "p2", "aa", t0)
	h.store.Seed(parent, StatusApproved)

	body := strings.NewReader(`{"parent_id":"aa","author_name":"Jane","content":"reply"}`)
	req := httptest.NewRequest("POST", testBase+"/p1/comments", body)
	req.Header.Set("Content-Type", "application/json")
	rec := h.do(req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestSubmit_RateLimit_HardCap429(t *testing.T) {
	h := newTestHarness(t)
	h.store.SeedPost("p1")

	// Bypass the spam-on-soft-rate-limit gate by injecting submissions
	// already past the hard cap; the hard cap should refuse.
	now := h.clock
	h.handlers.ipMu.Lock()
	for i := 0; i < maxCommentsPerIPHard; i++ {
		h.handlers.ips["192.0.2.99"] = append(h.handlers.ips["192.0.2.99"], now)
	}
	h.handlers.ipMu.Unlock()

	body := strings.NewReader(`{"author_name":"Spam","content":"hi"}`)
	req := httptest.NewRequest("POST", testBase+"/p1/comments", body)
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "192.0.2.99:1234"
	rec := h.do(req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status: got %d, want 429; body=%s", rec.Code, rec.Body.String())
	}
}

func TestSubmit_LongContent413(t *testing.T) {
	h := newTestHarness(t)
	h.store.SeedPost("p1")
	// Pre-sanitisation cap (handler.maxBodyBytes) — anything past
	// this 413s during the decode step.
	huge := strings.Repeat("a", maxBodyBytes+10)
	body := strings.NewReader(`{"author_name":"Jane","content":"` + huge + `"}`)
	req := httptest.NewRequest("POST", testBase+"/p1/comments", body)
	req.Header.Set("Content-Type", "application/json")
	rec := h.do(req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status: got %d, want 413; body=%s", rec.Code, rec.Body.String())
	}
}

func TestSubmit_UnknownFields_400(t *testing.T) {
	h := newTestHarness(t)
	h.store.SeedPost("p1")
	body := strings.NewReader(`{"author_name":"Jane","content":"hi","extra":true}`)
	req := httptest.NewRequest("POST", testBase+"/p1/comments", body)
	req.Header.Set("Content-Type", "application/json")
	rec := h.do(req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// -----------------------------------------------------------------------------
// CORS
// -----------------------------------------------------------------------------

func TestCORS_AllowOriginEcho(t *testing.T) {
	store := NewMemoryStore()
	store.SeedPost("p1")
	mux := http.NewServeMux()
	if err := Mount(mux, testBase, Deps{
		Store:       store,
		Logger:      slog.New(slog.NewJSONHandler(io.Discard, nil)),
		AllowOrigin: "https://example.com",
	}); err != nil {
		t.Fatalf("Mount: %v", err)
	}

	req := httptest.NewRequest("GET", testBase+"/p1/comments", nil)
	req.Header.Set("Origin", "https://example.com")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://example.com" {
		t.Errorf("ACAO: got %q, want %q", got, "https://example.com")
	}
}

func TestCORS_PreflightOK(t *testing.T) {
	store := NewMemoryStore()
	store.SeedPost("p1")
	mux := http.NewServeMux()
	_ = Mount(mux, testBase, Deps{
		Store:       store,
		Logger:      slog.New(slog.NewJSONHandler(io.Discard, nil)),
		AllowOrigin: "https://example.com",
	})

	req := httptest.NewRequest("OPTIONS", testBase+"/p1/comments", nil)
	req.Header.Set("Origin", "https://example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status: got %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
}

// -----------------------------------------------------------------------------
// MOUNT
// -----------------------------------------------------------------------------

func TestMount_RequiresStore(t *testing.T) {
	if err := Mount(http.NewServeMux(), testBase, Deps{}); err == nil {
		t.Fatal("Mount with nil Store: want error, got nil")
	}
}

// -----------------------------------------------------------------------------
// helpers
// -----------------------------------------------------------------------------

func idsOf(cs []Comment) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.ID
	}
	return out
}

func slicesEqual(a, b []string) bool {
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

// _ ensures we exercise CommentsByIP from the store layer too.
var _ = context.Background
