package wprest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/audit"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// -----------------------------------------------------------------------------
// Write-path fakes
// -----------------------------------------------------------------------------

// fakePostSink records what the handler asked it to do and returns
// pre-seeded rows. Failures can be injected by setting a *Err field.
type fakePostSink struct {
	mu        sync.Mutex
	nextID    int
	rows      map[int]PostRow
	createIn  []PostWriteInput
	updateIn  []PostWriteInput
	deleteIDs []int
	createErr error
	updateErr error
	deleteErr error
	actor     []string
}

func newFakePostSink(initial ...PostRow) *fakePostSink {
	s := &fakePostSink{rows: map[int]PostRow{}, nextID: 1000}
	for _, r := range initial {
		s.rows[r.LegacyID] = r
		if r.LegacyID >= s.nextID {
			s.nextID = r.LegacyID + 1
		}
	}
	return s
}

func (s *fakePostSink) Create(_ context.Context, actor string, in PostWriteInput) (PostRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.createErr != nil {
		return PostRow{}, s.createErr
	}
	s.actor = append(s.actor, actor)
	s.createIn = append(s.createIn, in)
	id := s.nextID
	s.nextID++
	row := PostRow{
		LegacyID: id,
		Type:     in.Type,
		Status:   "publish",
		Date:     time.Now().UTC(),
		DateGMT:  time.Now().UTC(),
	}
	if in.Title != nil {
		row.Title = *in.Title
	}
	if in.ContentHTML != nil {
		row.ContentHTML = *in.ContentHTML
	}
	if in.Slug != nil {
		row.Slug = *in.Slug
	}
	if in.Status != nil {
		row.Status = *in.Status
	}
	if in.AuthorID != nil {
		row.AuthorID = *in.AuthorID
	}
	if in.Categories != nil {
		row.Categories = append([]int(nil), (*in.Categories)...)
	}
	if in.Tags != nil {
		row.Tags = append([]int(nil), (*in.Tags)...)
	}
	s.rows[id] = row
	return row, nil
}

func (s *fakePostSink) Update(_ context.Context, actor string, id int, in PostWriteInput) (PostRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.updateErr != nil {
		return PostRow{}, s.updateErr
	}
	s.actor = append(s.actor, actor)
	row, ok := s.rows[id]
	if !ok {
		return PostRow{}, ErrNotFound
	}
	if in.Title != nil {
		row.Title = *in.Title
	}
	if in.ContentHTML != nil {
		row.ContentHTML = *in.ContentHTML
	}
	if in.Slug != nil {
		row.Slug = *in.Slug
	}
	if in.Status != nil {
		row.Status = *in.Status
	}
	if in.Categories != nil {
		row.Categories = append([]int(nil), (*in.Categories)...)
	}
	row.Modified = time.Now().UTC()
	s.rows[id] = row
	s.updateIn = append(s.updateIn, in)
	return row, nil
}

func (s *fakePostSink) Delete(_ context.Context, actor string, id int) (PostRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deleteErr != nil {
		return PostRow{}, s.deleteErr
	}
	row, ok := s.rows[id]
	if !ok {
		return PostRow{}, ErrNotFound
	}
	s.actor = append(s.actor, actor)
	s.deleteIDs = append(s.deleteIDs, id)
	delete(s.rows, id)
	return row, nil
}

// fakeUserSink mirrors fakePostSink for users.
type fakeUserSink struct {
	mu        sync.Mutex
	rows      map[int]UserRow
	nextID    int
	createErr error
}

func newFakeUserSink(initial ...UserRow) *fakeUserSink {
	s := &fakeUserSink{rows: map[int]UserRow{}, nextID: 500}
	for _, u := range initial {
		s.rows[u.LegacyID] = u
		if u.LegacyID >= s.nextID {
			s.nextID = u.LegacyID + 1
		}
	}
	return s
}

func (s *fakeUserSink) Create(_ context.Context, _ string, in UserWriteInput) (UserRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.createErr != nil {
		return UserRow{}, s.createErr
	}
	id := s.nextID
	s.nextID++
	row := UserRow{LegacyID: id}
	if in.Username != nil {
		row.Slug = *in.Username
	}
	if in.Name != nil {
		row.Name = *in.Name
	}
	s.rows[id] = row
	return row, nil
}

func (s *fakeUserSink) Update(_ context.Context, _ string, id int, in UserWriteInput) (UserRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[id]
	if !ok {
		return UserRow{}, ErrNotFound
	}
	if in.Name != nil {
		row.Name = *in.Name
	}
	s.rows[id] = row
	return row, nil
}

func (s *fakeUserSink) Delete(_ context.Context, _ string, id int, _ *int) (UserRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[id]
	if !ok {
		return UserRow{}, ErrNotFound
	}
	delete(s.rows, id)
	return row, nil
}

// fakeTermSink mirrors the others for terms.
type fakeTermSink struct {
	mu     sync.Mutex
	rows   map[int]TermRow
	nextID int
}

func newFakeTermSink(initial ...TermRow) *fakeTermSink {
	s := &fakeTermSink{rows: map[int]TermRow{}, nextID: 100}
	for _, t := range initial {
		s.rows[t.LegacyID] = t
		if t.LegacyID >= s.nextID {
			s.nextID = t.LegacyID + 1
		}
	}
	return s
}

func (s *fakeTermSink) Create(_ context.Context, _, taxonomy string, in TermWriteInput) (TermRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextID
	s.nextID++
	row := TermRow{LegacyID: id, Taxonomy: taxonomy}
	if in.Name != nil {
		row.Name = *in.Name
	}
	if in.Slug != nil {
		row.Slug = *in.Slug
	}
	s.rows[id] = row
	return row, nil
}

func (s *fakeTermSink) Update(_ context.Context, _, taxonomy string, id int, in TermWriteInput) (TermRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[id]
	if !ok {
		return TermRow{}, ErrNotFound
	}
	if in.Name != nil {
		row.Name = *in.Name
	}
	if in.Slug != nil {
		row.Slug = *in.Slug
	}
	row.Taxonomy = taxonomy
	s.rows[id] = row
	return row, nil
}

func (s *fakeTermSink) Delete(_ context.Context, _, _ string, id int) (TermRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[id]
	if !ok {
		return TermRow{}, ErrNotFound
	}
	delete(s.rows, id)
	return row, nil
}

// nonceVerifier fakes always-pass / always-fail.
type fakeNonceVerifier struct {
	allow bool
	err   error
}

func (f *fakeNonceVerifier) Verify(_ *http.Request) error {
	if f.allow {
		return nil
	}
	if f.err != nil {
		return f.err
	}
	return errNonceMissing
}

// -----------------------------------------------------------------------------
// Write harness
// -----------------------------------------------------------------------------

// writeHarness is a richer test substrate than `harness`: it wires the
// write surface with policy, nonce, and audit. Each subtest builds its
// own (no shared state). The defaults are permissive (admin principal,
// allow-all nonce) so most tests only override what they need.
type writeHarness struct {
	mux           *http.ServeMux
	postsSink     *fakePostSink
	pagesSink     *fakePostSink
	usersSink     *fakeUserSink
	categoriesS   *fakeTermSink
	tagsSink      *fakeTermSink
	auditStore    *audit.MemoryStore
	nonce         *fakeNonceVerifier
	principal     policy.Principal
	usePrincipal  bool
}

func newWriteHarness(t *testing.T) *writeHarness {
	t.Helper()
	posts := &fakePostSource{rows: seedPosts()}
	pages := &fakePostSource{rows: seedPages()}
	categories := &fakeTermSource{rows: seedCategories()}
	tags := &fakeTermSource{rows: seedTags()}
	users := &fakeUserSource{rows: seedUsers()}

	wh := &writeHarness{
		postsSink:   newFakePostSink(),
		pagesSink:   newFakePostSink(),
		usersSink:   newFakeUserSink(),
		categoriesS: newFakeTermSink(seedCategories()...),
		tagsSink:    newFakeTermSink(seedTags()...),
		auditStore:  audit.NewMemoryStore(),
		nonce:       &fakeNonceVerifier{allow: true},
		principal: policy.Principal{
			UserID: "user-admin",
			Roles:  []policy.Role{policy.RoleAdmin},
		},
		usePrincipal: true,
	}

	pol := policy.NewBasicPolicy(policy.DefaultRoleCapabilities())
	mux := http.NewServeMux()
	deps := Deps{
		Posts:          posts,
		PostsSink:      wh.postsSink,
		Pages:          pages,
		PagesSink:      wh.pagesSink,
		Users:          users,
		UsersSink:      wh.usersSink,
		Categories:     categories,
		CategoriesSink: wh.categoriesS,
		Tags:           tags,
		TagsSink:       wh.tagsSink,
		Policy:         pol,
		PrincipalFromContext: func(ctx context.Context) (policy.Principal, bool) {
			if !wh.usePrincipal {
				return policy.Principal{}, false
			}
			return wh.principal, true
		},
		NonceVerifier: wh.nonce,
		Audit:         audit.NewEmitter(wh.auditStore),
		SiteURL:       "https://example.test",
	}
	if err := Mount(mux, deps); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	wh.mux = mux
	return wh
}

func (wh *writeHarness) do(method, path, body string) *httptest.ResponseRecorder {
	var reader *strings.Reader
	if body != "" {
		reader = strings.NewReader(body)
	} else {
		reader = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	wh.mux.ServeHTTP(rec, req)
	return rec
}

// -----------------------------------------------------------------------------
// Posts: CREATE
// -----------------------------------------------------------------------------

func TestCreatePost_Happy(t *testing.T) {
	t.Parallel()
	wh := newWriteHarness(t)
	body := `{
		"title": {"raw": "New Post"},
		"content": {"raw": "<p>Hello</p>"},
		"slug": "new-post",
		"status": "publish",
		"categories": [10]
	}`
	rec := wh.do("POST", "/wp-json/wp/v2/posts", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["slug"] != "new-post" {
		t.Errorf("slug = %v", got["slug"])
	}
	title, _ := got["title"].(map[string]any)
	if title == nil || title["rendered"] != "New Post" {
		t.Errorf("title.rendered = %v", got["title"])
	}
	if got["status"] != "publish" {
		t.Errorf("status = %v", got["status"])
	}

	// Sink got the input we sent.
	if len(wh.postsSink.createIn) != 1 {
		t.Fatalf("sink calls = %d, want 1", len(wh.postsSink.createIn))
	}
	in := wh.postsSink.createIn[0]
	if in.Type != "post" {
		t.Errorf("Type = %s", in.Type)
	}
	if in.Title == nil || *in.Title != "New Post" {
		t.Errorf("Title = %v", in.Title)
	}
	if in.ContentHTML == nil || *in.ContentHTML != "<p>Hello</p>" {
		t.Errorf("ContentHTML = %v", in.ContentHTML)
	}

	// Audit fired exactly once.
	assertAuditCount(t, wh.auditStore, EventPostCreated, 1)
}

func TestCreatePost_AcceptsPlainStringTitle(t *testing.T) {
	t.Parallel()
	wh := newWriteHarness(t)
	rec := wh.do("POST", "/wp-json/wp/v2/posts", `{"title": "Plain Title"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	in := wh.postsSink.createIn[0]
	if in.Title == nil || *in.Title != "Plain Title" {
		t.Errorf("Title = %v", in.Title)
	}
}

func TestCreatePost_NoNonce(t *testing.T) {
	t.Parallel()
	wh := newWriteHarness(t)
	wh.nonce.allow = false
	rec := wh.do("POST", "/wp-json/wp/v2/posts", `{"title": "x"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertWPError(t, rec, errCodeInvalidNonce, http.StatusForbidden)
	// Sink must NOT have been called.
	if len(wh.postsSink.createIn) != 0 {
		t.Errorf("sink called despite nonce failure")
	}
	assertAuditCount(t, wh.auditStore, EventPostCreated, 0)
}

func TestCreatePost_NoCapability(t *testing.T) {
	t.Parallel()
	wh := newWriteHarness(t)
	// Subscriber has no edit_posts.
	wh.principal = policy.Principal{
		UserID: "user-subscriber",
		Roles:  []policy.Role{policy.RoleSubscriber},
	}
	rec := wh.do("POST", "/wp-json/wp/v2/posts", `{"title":"x"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertWPError(t, rec, errCodeForbidden, http.StatusForbidden)
	if len(wh.postsSink.createIn) != 0 {
		t.Errorf("sink called despite capability denial")
	}
	assertAuditCount(t, wh.auditStore, EventPostCreated, 0)
}

func TestCreatePost_NoAuth(t *testing.T) {
	t.Parallel()
	wh := newWriteHarness(t)
	wh.usePrincipal = false
	rec := wh.do("POST", "/wp-json/wp/v2/posts", `{"title":"x"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertWPError(t, rec, errCodeUnauthenticated, http.StatusUnauthorized)
}

func TestCreatePost_InvalidCategory(t *testing.T) {
	t.Parallel()
	wh := newWriteHarness(t)
	rec := wh.do("POST", "/wp-json/wp/v2/posts", `{
		"title": "x",
		"categories": [9999]
	}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertWPError(t, rec, errCodeInvalidTermID, http.StatusBadRequest)
	if len(wh.postsSink.createIn) != 0 {
		t.Errorf("sink called despite invalid term")
	}
}

func TestCreatePost_InvalidJSON(t *testing.T) {
	t.Parallel()
	wh := newWriteHarness(t)
	rec := wh.do("POST", "/wp-json/wp/v2/posts", `{not-json`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertWPError(t, rec, errCodeInvalidJSON, http.StatusBadRequest)
}

// -----------------------------------------------------------------------------
// Posts: UPDATE
// -----------------------------------------------------------------------------

func TestUpdatePost_Happy(t *testing.T) {
	t.Parallel()
	wh := newWriteHarness(t)
	// Seed a row via the sink.
	wh.postsSink.rows[42] = PostRow{LegacyID: 42, Type: "post", Status: "publish", Title: "Old"}

	rec := wh.do("PUT", "/wp-json/wp/v2/posts/42", `{"title": "Updated"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	title, _ := got["title"].(map[string]any)
	if title == nil || title["rendered"] != "Updated" {
		t.Errorf("title = %v", got["title"])
	}
	if got["id"] != float64(42) {
		t.Errorf("id = %v", got["id"])
	}
	assertAuditCount(t, wh.auditStore, EventPostUpdated, 1)
}

func TestUpdatePost_PatchSparse(t *testing.T) {
	t.Parallel()
	wh := newWriteHarness(t)
	wh.postsSink.rows[42] = PostRow{LegacyID: 42, Type: "post", Status: "publish", Title: "Old", Slug: "old"}

	rec := wh.do("PATCH", "/wp-json/wp/v2/posts/42", `{"slug": "new-slug"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	// Title should be untouched.
	if got := wh.postsSink.rows[42].Title; got != "Old" {
		t.Errorf("title clobbered: %q", got)
	}
	if got := wh.postsSink.rows[42].Slug; got != "new-slug" {
		t.Errorf("slug = %q", got)
	}
}

func TestUpdatePost_NotFound(t *testing.T) {
	t.Parallel()
	wh := newWriteHarness(t)
	rec := wh.do("PUT", "/wp-json/wp/v2/posts/9999", `{"title":"x"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertWPError(t, rec, errCodeInvalidPostID, http.StatusNotFound)
}

// -----------------------------------------------------------------------------
// Posts: DELETE
// -----------------------------------------------------------------------------

func TestDeletePost_Happy(t *testing.T) {
	t.Parallel()
	wh := newWriteHarness(t)
	wh.postsSink.rows[42] = PostRow{LegacyID: 42, Type: "post", Status: "publish", Title: "Bye", Slug: "bye"}

	rec := wh.do("DELETE", "/wp-json/wp/v2/posts/42", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["deleted"] != true {
		t.Errorf("deleted = %v", got["deleted"])
	}
	prev, ok := got["previous"].(map[string]any)
	if !ok {
		t.Fatalf("previous missing or wrong shape")
	}
	if prev["id"] != float64(42) {
		t.Errorf("previous.id = %v", prev["id"])
	}
	if prev["slug"] != "bye" {
		t.Errorf("previous.slug = %v", prev["slug"])
	}
	// Row was removed.
	if _, exists := wh.postsSink.rows[42]; exists {
		t.Errorf("row not removed")
	}
	assertAuditCount(t, wh.auditStore, EventPostDeleted, 1)
}

func TestDeletePost_NoNonce(t *testing.T) {
	t.Parallel()
	wh := newWriteHarness(t)
	wh.nonce.allow = false
	wh.postsSink.rows[42] = PostRow{LegacyID: 42}
	rec := wh.do("DELETE", "/wp-json/wp/v2/posts/42", "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d", rec.Code)
	}
	assertWPError(t, rec, errCodeInvalidNonce, http.StatusForbidden)
	if _, exists := wh.postsSink.rows[42]; !exists {
		t.Errorf("row removed despite nonce failure")
	}
}

// -----------------------------------------------------------------------------
// Pages: CREATE
// -----------------------------------------------------------------------------

func TestCreatePage_Happy(t *testing.T) {
	t.Parallel()
	wh := newWriteHarness(t)
	rec := wh.do("POST", "/wp-json/wp/v2/pages", `{"title": "About Us", "status": "publish"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got["type"] != "page" {
		t.Errorf("type = %v", got["type"])
	}
	assertAuditCount(t, wh.auditStore, EventPageCreated, 1)
}

// -----------------------------------------------------------------------------
// Users: CREATE / UPDATE / DELETE
// -----------------------------------------------------------------------------

func TestCreateUser_Happy(t *testing.T) {
	t.Parallel()
	wh := newWriteHarness(t)
	rec := wh.do("POST", "/wp-json/wp/v2/users",
		`{"username": "newbie", "email": "new@example.test", "name": "Newbie"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got["slug"] != "newbie" {
		t.Errorf("slug = %v", got["slug"])
	}
	assertAuditCount(t, wh.auditStore, EventUserCreated, 1)
}

func TestCreateUser_RequiresCapability(t *testing.T) {
	t.Parallel()
	wh := newWriteHarness(t)
	wh.principal = policy.Principal{
		UserID: "user-editor",
		Roles:  []policy.Role{policy.RoleEditor},
	}
	// Editor lacks CapCreateUsers.
	rec := wh.do("POST", "/wp-json/wp/v2/users", `{"username":"x"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertWPError(t, rec, errCodeForbidden, http.StatusForbidden)
}

func TestDeleteUser_Soft(t *testing.T) {
	t.Parallel()
	wh := newWriteHarness(t)
	wh.usersSink.rows[7] = UserRow{LegacyID: 7, Name: "Alice", Slug: "alice"}

	rec := wh.do("DELETE", "/wp-json/wp/v2/users/7", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got["deleted"] != true {
		t.Errorf("deleted = %v", got["deleted"])
	}
	prev, _ := got["previous"].(map[string]any)
	if prev == nil || prev["slug"] != "alice" {
		t.Errorf("previous shape wrong: %v", got["previous"])
	}
	assertAuditCount(t, wh.auditStore, EventUserDeleted, 1)
}

// -----------------------------------------------------------------------------
// Categories: CREATE / UPDATE / DELETE
// -----------------------------------------------------------------------------

func TestCreateCategory_Happy(t *testing.T) {
	t.Parallel()
	wh := newWriteHarness(t)
	rec := wh.do("POST", "/wp-json/wp/v2/categories", `{"name": "Sports", "slug": "sports"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got["name"] != "Sports" {
		t.Errorf("name = %v", got["name"])
	}
	if got["taxonomy"] != "category" {
		t.Errorf("taxonomy = %v", got["taxonomy"])
	}
	assertAuditCount(t, wh.auditStore, EventTermCreated, 1)
}

func TestUpdateTag_Happy(t *testing.T) {
	t.Parallel()
	wh := newWriteHarness(t)
	// Seed via sink.
	wh.tagsSink.rows[200] = TermRow{LegacyID: 200, Name: "Old", Slug: "old", Taxonomy: "post_tag"}

	rec := wh.do("PUT", "/wp-json/wp/v2/tags/200", `{"name": "New"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got["name"] != "New" {
		t.Errorf("name = %v", got["name"])
	}
	if got["taxonomy"] != "post_tag" {
		t.Errorf("taxonomy = %v", got["taxonomy"])
	}
	assertAuditCount(t, wh.auditStore, EventTermUpdated, 1)
}

func TestDeleteCategory_NotFound(t *testing.T) {
	t.Parallel()
	wh := newWriteHarness(t)
	rec := wh.do("DELETE", "/wp-json/wp/v2/categories/9999", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertWPError(t, rec, errCodeInvalidTermID, http.StatusNotFound)
}

// -----------------------------------------------------------------------------
// Nil-sink fallback (no write surface)
// -----------------------------------------------------------------------------

func TestWriteRefused_WhenSinkNil(t *testing.T) {
	t.Parallel()
	// Mount without any sink — every write must still 405.
	mux := http.NewServeMux()
	if err := Mount(mux, Deps{
		Posts: &fakePostSource{}, Pages: &fakePostSource{},
		SiteURL: "https://example.test",
	}); err != nil {
		t.Fatalf("Mount: %v", err)
	}

	cases := []struct{ method, path string }{
		{"POST", "/wp-json/wp/v2/posts"},
		{"PUT", "/wp-json/wp/v2/posts/1"},
		{"DELETE", "/wp-json/wp/v2/categories/10"},
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, c.path, strings.NewReader(`{}`))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s: status = %d body=%s", c.method, c.path, rec.Code, rec.Body.String())
		}
	}
}

// -----------------------------------------------------------------------------
// Nonce header alternates
// -----------------------------------------------------------------------------

func TestBridgedNonceVerifier_AcceptsHeaderAndQuery(t *testing.T) {
	t.Parallel()
	v := NewBridgedNonceVerifier("csrf")

	// No header, no cookie → missing.
	r := httptest.NewRequest("POST", "/wp-json/wp/v2/posts", nil)
	if err := v.Verify(r); !errors.Is(err, errNonceMissing) {
		t.Errorf("missing case: err = %v", err)
	}

	// Header set but no cookie → missing (the cookie carrier is also required).
	r = httptest.NewRequest("POST", "/wp-json/wp/v2/posts", nil)
	r.Header.Set(HeaderWPNonce, "tok123")
	if err := v.Verify(r); !errors.Is(err, errNonceMissing) {
		t.Errorf("header-only: err = %v", err)
	}

	// Header matches cookie → success.
	r = httptest.NewRequest("POST", "/wp-json/wp/v2/posts", nil)
	r.Header.Set(HeaderWPNonce, "tok123")
	r.AddCookie(&http.Cookie{Name: "csrf", Value: "tok123"})
	if err := v.Verify(r); err != nil {
		t.Errorf("header-match: err = %v", err)
	}

	// Header mismatches cookie → mismatch.
	r = httptest.NewRequest("POST", "/wp-json/wp/v2/posts", nil)
	r.Header.Set(HeaderWPNonce, "tokA")
	r.AddCookie(&http.Cookie{Name: "csrf", Value: "tokB"})
	if err := v.Verify(r); !errors.Is(err, errNonceMismatch) {
		t.Errorf("mismatch case: err = %v", err)
	}

	// Query-param fallback also works.
	r = httptest.NewRequest("POST", "/wp-json/wp/v2/posts?_wpnonce=tok123", nil)
	r.AddCookie(&http.Cookie{Name: "csrf", Value: "tok123"})
	if err := v.Verify(r); err != nil {
		t.Errorf("query fallback: err = %v", err)
	}
}

// -----------------------------------------------------------------------------
// Audit single-emission invariant
// -----------------------------------------------------------------------------

func TestAuditFiresExactlyOncePerWrite(t *testing.T) {
	t.Parallel()
	wh := newWriteHarness(t)

	// Create
	rec := wh.do("POST", "/wp-json/wp/v2/posts", `{"title":"A"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d", rec.Code)
	}
	createdID := func() int {
		var b map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &b)
		return int(b["id"].(float64))
	}()
	assertAuditCount(t, wh.auditStore, EventPostCreated, 1)

	// Update same row
	rec = wh.do("PUT", "/wp-json/wp/v2/posts/"+strconv.Itoa(createdID), `{"title":"B"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertAuditCount(t, wh.auditStore, EventPostUpdated, 1)

	// Delete same row
	rec = wh.do("DELETE", "/wp-json/wp/v2/posts/"+strconv.Itoa(createdID), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d", rec.Code)
	}
	assertAuditCount(t, wh.auditStore, EventPostDeleted, 1)

	// Failed writes (no auth) must NOT emit audit.
	wh.usePrincipal = false
	rec = wh.do("POST", "/wp-json/wp/v2/posts", `{"title":"C"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no-auth status = %d", rec.Code)
	}
	// Created count unchanged.
	assertAuditCount(t, wh.auditStore, EventPostCreated, 1)
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

func assertAuditCount(t *testing.T, store *audit.MemoryStore, eventType string, want int) {
	t.Helper()
	events, err := store.List(context.Background(), audit.Filter{EventType: eventType})
	if err != nil {
		t.Fatalf("audit list: %v", err)
	}
	if len(events) != want {
		t.Errorf("audit events for %s = %d, want %d", eventType, len(events), want)
	}
}
