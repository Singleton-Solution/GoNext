package posts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/router"
	"github.com/Singleton-Solution/GoNext/packages/go/audit"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// testHarness wires a mounted posts mount over an in-memory store
// against an in-memory audit emitter and a basic policy. The shape
// here is the substrate every handler test uses; each subtest builds
// its own harness so the tests don't share state.
type testHarness struct {
	mux        *http.ServeMux
	store      *MemoryStore
	audit      *audit.Emitter
	auditStore *audit.MemoryStore
	policy     policy.Policy
	postType   string
	base       string
}

// newHarness boots a posts mount. role chooses which of the seeded
// roles the request principal will carry by default; tests that want
// a different role override per-request via mustRequest's
// withPrincipal options.
func newHarness(t *testing.T, postType string) *testHarness {
	t.Helper()
	mux := http.NewServeMux()
	store := NewMemoryStore()
	auditStore := audit.NewMemoryStore()
	em := audit.NewEmitter(auditStore)
	pol := policy.NewBasicPolicy(policy.DefaultRoleCapabilities())

	base := "/api/v1/posts"
	if postType == PostTypePage {
		base = "/api/v1/pages"
	}
	if err := Mount(mux, base, Deps{
		Store:    store,
		Policy:   pol,
		Audit:    em,
		PostType: postType,
	}); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	return &testHarness{
		mux:        mux,
		store:      store,
		audit:      em,
		auditStore: auditStore,
		policy:     pol,
		postType:   postType,
		base:       base,
	}
}

// do issues req against the harness with the given principal. A nil
// principal means anonymous (no policy.Principal on context).
func (h *testHarness) do(req *http.Request, pr *policy.Principal) *httptest.ResponseRecorder {
	if pr != nil {
		req = req.WithContext(policy.WithPrincipal(req.Context(), *pr))
	}
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	return rec
}

func jsonBody(t *testing.T, v any) *bytes.Buffer {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return bytes.NewBuffer(b)
}

func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder, out any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
}

func authorPrincipal(userID string) policy.Principal {
	return policy.Principal{UserID: userID, Roles: []policy.Role{policy.RoleAuthor}}
}

func editorPrincipal(userID string) policy.Principal {
	return policy.Principal{UserID: userID, Roles: []policy.Role{policy.RoleEditor}}
}

func subscriberPrincipal(userID string) policy.Principal {
	return policy.Principal{UserID: userID, Roles: []policy.Role{policy.RoleSubscriber}}
}

// -----------------------------------------------------------------------------
// CREATE
// -----------------------------------------------------------------------------

func TestCreate_AuthorHappyPath(t *testing.T) {
	t.Parallel()
	h := newHarness(t, PostTypePost)
	pr := authorPrincipal("user-1")

	title := "Hello"
	req := httptest.NewRequest("POST", h.base, jsonBody(t, CreateInput{Title: &title}))
	rec := h.do(req, &pr)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var got Post
	decodeJSON(t, rec, &got)
	if got.Title != "Hello" || got.AuthorID != "user-1" {
		t.Errorf("got = %+v", got)
	}
	if rec.Header().Get(HeaderVersion) != "1" {
		t.Errorf("X-Version = %q", rec.Header().Get(HeaderVersion))
	}
	if rec.Header().Get("ETag") == "" {
		t.Errorf("ETag missing")
	}

	// Audit emission.
	events, err := h.auditStore.List(context.Background(), audit.Filter{})
	if err != nil {
		t.Fatalf("audit List: %v", err)
	}
	if len(events) != 1 || events[0].EventType != "post.created" {
		t.Errorf("audit events = %+v", events)
	}
}

func TestCreate_Unauthenticated(t *testing.T) {
	t.Parallel()
	h := newHarness(t, PostTypePost)

	req := httptest.NewRequest("POST", h.base, jsonBody(t, CreateInput{}))
	rec := h.do(req, nil) // no principal

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestCreate_ForbiddenForSubscriber(t *testing.T) {
	t.Parallel()
	h := newHarness(t, PostTypePost)
	pr := subscriberPrincipal("user-2")

	req := httptest.NewRequest("POST", h.base, jsonBody(t, CreateInput{}))
	rec := h.do(req, &pr)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestCreate_InvalidContentBlocks(t *testing.T) {
	t.Parallel()
	h := newHarness(t, PostTypePost)
	pr := authorPrincipal("user-1")

	body := CreateInput{ContentBlocks: json.RawMessage(`["not an object"]`)}
	req := httptest.NewRequest("POST", h.base, jsonBody(t, body))
	rec := h.do(req, &pr)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestCreate_PublishRequiresPublishCap(t *testing.T) {
	t.Parallel()
	h := newHarness(t, PostTypePost)
	// Contributor has edit_posts but NOT publish_posts.
	pr := policy.Principal{UserID: "u1", Roles: []policy.Role{policy.RoleContributor}}

	status := "published"
	req := httptest.NewRequest("POST", h.base, jsonBody(t, CreateInput{Status: &status}))
	rec := h.do(req, &pr)
	if rec.Code != http.StatusForbidden {
		t.Errorf("contributor publishing should be 403, got %d", rec.Code)
	}
}

// -----------------------------------------------------------------------------
// GET ONE
// -----------------------------------------------------------------------------

func TestGet_Happy(t *testing.T) {
	t.Parallel()
	h := newHarness(t, PostTypePost)
	pr := authorPrincipal("u1")

	title := "Hello"
	created, _ := h.store.Create(context.Background(), PostTypePost, "u1", CreateInput{Title: &title})

	req := httptest.NewRequest("GET", h.base+"/"+created.ID, nil)
	rec := h.do(req, &pr)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("ETag") == "" {
		t.Errorf("ETag missing")
	}
}

func TestGet_NotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t, PostTypePost)
	pr := authorPrincipal("u1")
	req := httptest.NewRequest("GET", h.base+"/does-not-exist", nil)
	rec := h.do(req, &pr)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestGet_Unauthenticated(t *testing.T) {
	t.Parallel()
	h := newHarness(t, PostTypePost)
	created, _ := h.store.Create(context.Background(), PostTypePost, "u1", CreateInput{})
	req := httptest.NewRequest("GET", h.base+"/"+created.ID, nil)
	rec := h.do(req, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestGet_PrivateRequiresCap(t *testing.T) {
	t.Parallel()
	h := newHarness(t, PostTypePost)
	priv := "private"
	created, _ := h.store.Create(context.Background(), PostTypePost, "u1", CreateInput{Status: &priv})

	// Author does NOT hold read_private_posts.
	authorPr := authorPrincipal("u2")
	req := httptest.NewRequest("GET", h.base+"/"+created.ID, nil)
	rec := h.do(req, &authorPr)
	if rec.Code != http.StatusForbidden {
		t.Errorf("author reading private = %d, want 403", rec.Code)
	}

	// Editor holds read_private_posts.
	editorPr := editorPrincipal("u3")
	req = httptest.NewRequest("GET", h.base+"/"+created.ID, nil)
	rec = h.do(req, &editorPr)
	if rec.Code != http.StatusOK {
		t.Errorf("editor reading private = %d", rec.Code)
	}
}

func TestGet_ETagRoundTrip(t *testing.T) {
	t.Parallel()
	h := newHarness(t, PostTypePost)
	pr := authorPrincipal("u1")

	created, _ := h.store.Create(context.Background(), PostTypePost, "u1", CreateInput{})
	req := httptest.NewRequest("GET", h.base+"/"+created.ID, nil)
	rec := h.do(req, &pr)
	if rec.Code != http.StatusOK {
		t.Fatalf("first GET status = %d", rec.Code)
	}
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("ETag missing")
	}

	// Send If-None-Match → 304.
	req = httptest.NewRequest("GET", h.base+"/"+created.ID, nil)
	req.Header.Set("If-None-Match", etag)
	rec = h.do(req, &pr)
	if rec.Code != http.StatusNotModified {
		t.Errorf("conditional GET status = %d, want 304", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("304 has body: %s", rec.Body.String())
	}
}

func TestGet_PasswordProtected(t *testing.T) {
	t.Parallel()
	h := newHarness(t, PostTypePost)
	pr := authorPrincipal("u1")

	password := "secret-pw"
	contentBlocks := json.RawMessage(`[{"type":"core/paragraph","content":"hi"}]`)
	created, _ := h.store.Create(context.Background(), PostTypePost, "u1", CreateInput{
		Password:      &password,
		ContentBlocks: contentBlocks,
	})

	// Without password header: content stripped.
	req := httptest.NewRequest("GET", h.base+"/"+created.ID, nil)
	rec := h.do(req, &pr)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got Post
	decodeJSON(t, rec, &got)
	if !got.Protected {
		t.Errorf("protected flag missing")
	}
	if string(got.ContentBlocks) != "[]" {
		t.Errorf("content not stripped: %s", got.ContentBlocks)
	}

	// With correct password header: content returned.
	req = httptest.NewRequest("GET", h.base+"/"+created.ID, nil)
	req.Header.Set(HeaderPostPassword, password)
	rec = h.do(req, &pr)
	decodeJSON(t, rec, &got)
	if string(got.ContentBlocks) == "[]" {
		t.Errorf("content stripped despite password: %s", got.ContentBlocks)
	}
}

// -----------------------------------------------------------------------------
// LIST
// -----------------------------------------------------------------------------

func TestList_Paginates50Posts(t *testing.T) {
	t.Parallel()
	h := newHarness(t, PostTypePost)
	pr := authorPrincipal("u1")

	// Insert 50 posts with deterministic ids.
	var n int
	h.store.SetIDFunc(func() string {
		n++
		return formatTestID(n)
	})
	for i := 0; i < 50; i++ {
		if _, err := h.store.Create(context.Background(), PostTypePost, "u1", CreateInput{}); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	seen := map[string]bool{}
	cursor := ""
	pages := 0
	for {
		pages++
		if pages > 10 {
			t.Fatal("pagination did not terminate")
		}
		url := h.base + "?limit=20"
		if cursor != "" {
			url += "&after=" + cursor
		}
		req := httptest.NewRequest("GET", url, nil)
		rec := h.do(req, &pr)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
		var page router.Page[Post]
		decodeJSON(t, rec, &page)
		for _, p := range page.Data {
			if seen[p.ID] {
				t.Errorf("duplicate id %s across pages", p.ID)
			}
			seen[p.ID] = true
		}
		if page.Pagination.NextCursor == "" {
			break
		}
		cursor = page.Pagination.NextCursor
	}
	if len(seen) != 50 {
		t.Errorf("paginated count = %d, want 50", len(seen))
	}
}

func TestList_FilterByStatus(t *testing.T) {
	t.Parallel()
	h := newHarness(t, PostTypePost)
	pr := authorPrincipal("u1")

	draft := "draft"
	pub := "published"
	h.store.Create(context.Background(), PostTypePost, "u1", CreateInput{Status: &draft})
	h.store.Create(context.Background(), PostTypePost, "u1", CreateInput{Status: &pub})

	req := httptest.NewRequest("GET", h.base+"?status=published", nil)
	rec := h.do(req, &pr)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var page router.Page[Post]
	decodeJSON(t, rec, &page)
	if len(page.Data) != 1 || page.Data[0].Status != "published" {
		t.Errorf("data = %+v", page.Data)
	}
}

func TestList_InvalidStatus(t *testing.T) {
	t.Parallel()
	h := newHarness(t, PostTypePost)
	pr := authorPrincipal("u1")
	req := httptest.NewRequest("GET", h.base+"?status=bogus", nil)
	rec := h.do(req, &pr)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestList_PrivateRequiresCap(t *testing.T) {
	t.Parallel()
	h := newHarness(t, PostTypePost)
	authorPr := authorPrincipal("u1")
	req := httptest.NewRequest("GET", h.base+"?status=private", nil)
	rec := h.do(req, &authorPr)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestList_SearchByTitle(t *testing.T) {
	t.Parallel()
	h := newHarness(t, PostTypePost)
	pr := authorPrincipal("u1")

	t1 := "Hello World"
	t2 := "Other Post"
	h.store.Create(context.Background(), PostTypePost, "u1", CreateInput{Title: &t1})
	h.store.Create(context.Background(), PostTypePost, "u1", CreateInput{Title: &t2})

	req := httptest.NewRequest("GET", h.base+"?search=hello", nil)
	rec := h.do(req, &pr)
	var page router.Page[Post]
	decodeJSON(t, rec, &page)
	if len(page.Data) != 1 || page.Data[0].Title != "Hello World" {
		t.Errorf("data = %+v", page.Data)
	}
}

// TestList_FilterByPostType is the regression test for the admin Pages
// list (issue #506). The /api/v1/posts mount is hard-coded to
// PostTypePost, but admin/pages queries /api/v1/posts?post_type=page —
// the handler must honor that override and return only page rows.
func TestList_FilterByPostType(t *testing.T) {
	t.Parallel()
	h := newHarness(t, PostTypePost)
	pr := authorPrincipal("u1")

	postTitle := "A Post"
	pageTitle := "About Us"
	h.store.Create(context.Background(), PostTypePost, "u1", CreateInput{Title: &postTitle})
	h.store.Create(context.Background(), PostTypePage, "u1", CreateInput{Title: &pageTitle})

	req := httptest.NewRequest("GET", h.base+"?post_type=page", nil)
	rec := h.do(req, &pr)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var page router.Page[Post]
	decodeJSON(t, rec, &page)
	if len(page.Data) != 1 || page.Data[0].Title != "About Us" || page.Data[0].PostType != PostTypePage {
		t.Errorf("data = %+v; want a single page row", page.Data)
	}
}

// TestList_FilterByPostType_InvalidRejected guards the closed-set
// validation: an unknown post_type is a 400, not a silent fall-through
// to the mount default.
func TestList_FilterByPostType_InvalidRejected(t *testing.T) {
	t.Parallel()
	h := newHarness(t, PostTypePost)
	pr := authorPrincipal("u1")

	req := httptest.NewRequest("GET", h.base+"?post_type=attachment", nil)
	rec := h.do(req, &pr)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
}

// -----------------------------------------------------------------------------
// UPDATE
// -----------------------------------------------------------------------------

func TestUpdate_AuthorCanEditOwn(t *testing.T) {
	t.Parallel()
	h := newHarness(t, PostTypePost)
	pr := authorPrincipal("u1")

	created, _ := h.store.Create(context.Background(), PostTypePost, "u1", CreateInput{})

	newTitle := "Updated"
	body := jsonBody(t, UpdateInput{Title: &newTitle})
	req := httptest.NewRequest("PATCH", h.base+"/"+created.ID, body)
	req.Header.Set("If-Match", `"`+strconv.Itoa(created.Version)+`"`)
	rec := h.do(req, &pr)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got Post
	decodeJSON(t, rec, &got)
	if got.Title != "Updated" {
		t.Errorf("title = %q", got.Title)
	}
	if got.Version != created.Version+1 {
		t.Errorf("version not bumped: %d", got.Version)
	}
}

func TestUpdate_NonAuthorNeedsEditOthers(t *testing.T) {
	t.Parallel()
	h := newHarness(t, PostTypePost)

	created, _ := h.store.Create(context.Background(), PostTypePost, "u1", CreateInput{})

	// Different user, only Author role — Author does NOT have edit_others_posts.
	pr := authorPrincipal("u2")
	body := jsonBody(t, UpdateInput{})
	req := httptest.NewRequest("PATCH", h.base+"/"+created.ID, body)
	req.Header.Set("If-Match", `"`+strconv.Itoa(created.Version)+`"`)
	rec := h.do(req, &pr)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}

	// Editor has edit_others_posts.
	editorPr := editorPrincipal("u3")
	body = jsonBody(t, UpdateInput{})
	req = httptest.NewRequest("PATCH", h.base+"/"+created.ID, body)
	req.Header.Set("If-Match", `"`+strconv.Itoa(created.Version)+`"`)
	rec = h.do(req, &editorPr)
	if rec.Code != http.StatusOK {
		t.Errorf("editor patching others = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
}

func TestUpdate_VersionMismatch412(t *testing.T) {
	t.Parallel()
	h := newHarness(t, PostTypePost)
	pr := authorPrincipal("u1")

	created, _ := h.store.Create(context.Background(), PostTypePost, "u1", CreateInput{})
	body := jsonBody(t, UpdateInput{})
	req := httptest.NewRequest("PATCH", h.base+"/"+created.ID, body)
	req.Header.Set("If-Match", `"99"`)
	rec := h.do(req, &pr)
	if rec.Code != http.StatusPreconditionFailed {
		t.Errorf("status = %d, want 412", rec.Code)
	}
	var pd router.ProblemDetails
	decodeJSON(t, rec, &pd)
	if pd.Code != "version_mismatch" {
		t.Errorf("code = %q", pd.Code)
	}
}

func TestUpdate_NotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t, PostTypePost)
	pr := authorPrincipal("u1")
	req := httptest.NewRequest("PATCH", h.base+"/none", jsonBody(t, UpdateInput{}))
	req.Header.Set("If-Match", `"1"`)
	rec := h.do(req, &pr)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestUpdate_MissingIfMatch(t *testing.T) {
	t.Parallel()
	h := newHarness(t, PostTypePost)
	pr := authorPrincipal("u1")
	created, _ := h.store.Create(context.Background(), PostTypePost, "u1", CreateInput{})
	req := httptest.NewRequest("PATCH", h.base+"/"+created.ID, jsonBody(t, UpdateInput{}))
	rec := h.do(req, &pr)
	// Must require If-Match for PATCH.
	if rec.Code != http.StatusPreconditionRequired && rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 428 or 400", rec.Code)
	}
}

func TestUpdate_MalformedBody(t *testing.T) {
	t.Parallel()
	h := newHarness(t, PostTypePost)
	pr := authorPrincipal("u1")
	created, _ := h.store.Create(context.Background(), PostTypePost, "u1", CreateInput{})
	req := httptest.NewRequest("PATCH", h.base+"/"+created.ID, strings.NewReader("not json"))
	req.Header.Set("If-Match", `"`+strconv.Itoa(created.Version)+`"`)
	rec := h.do(req, &pr)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

// -----------------------------------------------------------------------------
// DELETE (trash)
// -----------------------------------------------------------------------------

func TestTrash_AuthorOwn(t *testing.T) {
	t.Parallel()
	h := newHarness(t, PostTypePost)
	pr := authorPrincipal("u1")
	created, _ := h.store.Create(context.Background(), PostTypePost, "u1", CreateInput{})

	req := httptest.NewRequest("DELETE", h.base+"/"+created.ID, nil)
	req.Header.Set("If-Match", `"`+strconv.Itoa(created.Version)+`"`)
	rec := h.do(req, &pr)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got Post
	decodeJSON(t, rec, &got)
	if got.Status != "trash" {
		t.Errorf("status = %q, want trash", got.Status)
	}
}

func TestTrash_NonAuthor403(t *testing.T) {
	t.Parallel()
	h := newHarness(t, PostTypePost)
	created, _ := h.store.Create(context.Background(), PostTypePost, "u1", CreateInput{})

	pr := authorPrincipal("u2") // Author (not editor) cannot delete others' posts
	req := httptest.NewRequest("DELETE", h.base+"/"+created.ID, nil)
	req.Header.Set("If-Match", `"`+strconv.Itoa(created.Version)+`"`)
	rec := h.do(req, &pr)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestTrash_VersionMismatch(t *testing.T) {
	t.Parallel()
	h := newHarness(t, PostTypePost)
	pr := authorPrincipal("u1")
	created, _ := h.store.Create(context.Background(), PostTypePost, "u1", CreateInput{})
	req := httptest.NewRequest("DELETE", h.base+"/"+created.ID, nil)
	req.Header.Set("If-Match", `"99"`)
	rec := h.do(req, &pr)
	if rec.Code != http.StatusPreconditionFailed {
		t.Errorf("status = %d, want 412", rec.Code)
	}
}

func TestTrash_NotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t, PostTypePost)
	pr := authorPrincipal("u1")
	req := httptest.NewRequest("DELETE", h.base+"/none", nil)
	req.Header.Set("If-Match", `"1"`)
	rec := h.do(req, &pr)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d", rec.Code)
	}
}

// -----------------------------------------------------------------------------
// PAGES
// -----------------------------------------------------------------------------

func TestPagesMount_UsesPageCaps(t *testing.T) {
	t.Parallel()
	// Author has edit_posts but NOT edit_pages.
	h := newHarness(t, PostTypePage)
	pr := authorPrincipal("u1")

	req := httptest.NewRequest("POST", h.base, jsonBody(t, CreateInput{}))
	rec := h.do(req, &pr)
	if rec.Code != http.StatusForbidden {
		t.Errorf("author POST /pages = %d, want 403", rec.Code)
	}

	// Editor has edit_pages.
	editorPr := editorPrincipal("u2")
	req = httptest.NewRequest("POST", h.base, jsonBody(t, CreateInput{}))
	rec = h.do(req, &editorPr)
	if rec.Code != http.StatusCreated {
		t.Errorf("editor POST /pages = %d, want 201, body = %s", rec.Code, rec.Body.String())
	}
}

// -----------------------------------------------------------------------------
// Mount validation
// -----------------------------------------------------------------------------

func TestMount_RejectsMissingDeps(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	if err := Mount(mux, "/x", Deps{}); err == nil {
		t.Error("Mount with empty Deps should error")
	}
	if err := Mount(mux, "/x", Deps{
		Store:    NewMemoryStore(),
		Policy:   policy.NewBasicPolicy(nil),
		PostType: "unknown",
	}); err == nil {
		t.Error("Mount with unknown PostType should error")
	}
}

// -----------------------------------------------------------------------------
// ProblemDetails Content-Type
// -----------------------------------------------------------------------------

func TestErrorResponse_UsesProblemContentType(t *testing.T) {
	t.Parallel()
	h := newHarness(t, PostTypePost)
	pr := authorPrincipal("u1")
	req := httptest.NewRequest("GET", h.base+"/does-not-exist", nil)
	rec := h.do(req, &pr)
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/problem+json") {
		t.Errorf("Content-Type = %q, want application/problem+json", ct)
	}
}

func TestList_PaginationShape(t *testing.T) {
	t.Parallel()
	h := newHarness(t, PostTypePost)
	pr := authorPrincipal("u1")
	for i := 0; i < 3; i++ {
		h.store.Create(context.Background(), PostTypePost, "u1", CreateInput{})
	}
	req := httptest.NewRequest("GET", h.base, nil)
	rec := h.do(req, &pr)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	// Verify the JSON has both top-level keys.
	var envelope map[string]json.RawMessage
	decodeJSON(t, rec, &envelope)
	if _, ok := envelope["data"]; !ok {
		t.Error("response missing data key")
	}
	if _, ok := envelope["pagination"]; !ok {
		t.Error("response missing pagination key")
	}
}

// Defense in depth: confirm that a deeply nested malformed content_blocks
// is caught by validation before reaching the store.
func TestCreate_RejectsNonArrayContentBlocks(t *testing.T) {
	t.Parallel()
	h := newHarness(t, PostTypePost)
	pr := authorPrincipal("u1")
	body := bytes.NewBufferString(fmt.Sprintf(`{"content_blocks": %q}`, "not a json array"))
	req := httptest.NewRequest("POST", h.base, body)
	rec := h.do(req, &pr)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d", rec.Code)
	}
}
