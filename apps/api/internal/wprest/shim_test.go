package wprest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// -----------------------------------------------------------------------------
// In-memory fake sources
// -----------------------------------------------------------------------------

// fakePostSource is the test substrate for PostSource. It is filter-aware
// (so the listing tests can verify the shim plumbs WP query params through
// without the production store) but stores rows in a flat slice.
type fakePostSource struct {
	rows []PostRow
	err  error // when non-nil, every method returns this error
}

func (f *fakePostSource) List(_ context.Context, filter PostFilter) ([]PostRow, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([]PostRow, 0, len(f.rows))
	for _, r := range f.rows {
		if filter.Search != "" && !strings.Contains(strings.ToLower(r.Title), strings.ToLower(filter.Search)) {
			continue
		}
		if filter.Slug != "" && r.Slug != filter.Slug {
			continue
		}
		if len(filter.Statuses) > 0 && !contains(filter.Statuses, r.Status) {
			continue
		} else if len(filter.Statuses) == 0 && r.Status != "publish" {
			// Default to publish-only when caller didn't override —
			// matches WP's default policy on unauthenticated reads.
			continue
		}
		if len(filter.Categories) > 0 && !intersects(r.Categories, filter.Categories) {
			continue
		}
		if len(filter.Tags) > 0 && !intersects(r.Tags, filter.Tags) {
			continue
		}
		out = append(out, r)
	}

	switch filter.OrderBy {
	case "title":
		sort.SliceStable(out, func(i, j int) bool { return out[i].Title < out[j].Title })
	case "id":
		sort.SliceStable(out, func(i, j int) bool { return out[i].LegacyID < out[j].LegacyID })
	case "slug":
		sort.SliceStable(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	default: // date
		sort.SliceStable(out, func(i, j int) bool { return out[i].Date.Before(out[j].Date) })
	}
	if filter.Order == "desc" {
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	return out, nil
}

func (f *fakePostSource) GetByLegacyID(_ context.Context, id int) (PostRow, error) {
	if f.err != nil {
		return PostRow{}, f.err
	}
	for _, r := range f.rows {
		if r.LegacyID == id {
			return r, nil
		}
	}
	return PostRow{}, ErrNotFound
}

type fakeUserSource struct {
	rows []UserRow
}

func (f *fakeUserSource) List(_ context.Context) ([]UserRow, error) { return f.rows, nil }
func (f *fakeUserSource) GetByLegacyID(_ context.Context, id int) (UserRow, error) {
	for _, u := range f.rows {
		if u.LegacyID == id {
			return u, nil
		}
	}
	return UserRow{}, ErrNotFound
}

type fakeTermSource struct {
	rows []TermRow
}

func (f *fakeTermSource) List(_ context.Context) ([]TermRow, error) { return f.rows, nil }
func (f *fakeTermSource) GetByLegacyID(_ context.Context, id int) (TermRow, error) {
	for _, t := range f.rows {
		if t.LegacyID == id {
			return t, nil
		}
	}
	return TermRow{}, ErrNotFound
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func intersects(a, b []int) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}

// -----------------------------------------------------------------------------
// Harness
// -----------------------------------------------------------------------------

// harness boots one Mount with seeded sources and exposes a helper to
// run requests against it.
type harness struct {
	mux        *http.ServeMux
	posts      *fakePostSource
	pages      *fakePostSource
	users      *fakeUserSource
	categories *fakeTermSource
	tags       *fakeTermSource
}

// newHarness builds a fully wired test mount. Each subtest gets its own
// harness — no shared state across t.Parallel tests.
func newHarness(t *testing.T) *harness {
	t.Helper()
	posts := &fakePostSource{rows: seedPosts()}
	pages := &fakePostSource{rows: seedPages()}
	users := &fakeUserSource{rows: seedUsers()}
	categories := &fakeTermSource{rows: seedCategories()}
	tags := &fakeTermSource{rows: seedTags()}

	mux := http.NewServeMux()
	if err := Mount(mux, Deps{
		Posts:      posts,
		Pages:      pages,
		Users:      users,
		Categories: categories,
		Tags:       tags,
		SiteURL:    "https://example.test",
	}); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	return &harness{
		mux:        mux,
		posts:      posts,
		pages:      pages,
		users:      users,
		categories: categories,
		tags:       tags,
	}
}

func (h *harness) do(method, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	return rec
}

// -----------------------------------------------------------------------------
// Seed data
// -----------------------------------------------------------------------------

func seedPosts() []PostRow {
	t1, _ := time.Parse(time.RFC3339, "2024-01-01T10:00:00Z")
	t2, _ := time.Parse(time.RFC3339, "2024-02-01T10:00:00Z")
	t3, _ := time.Parse(time.RFC3339, "2024-03-01T10:00:00Z")
	return []PostRow{
		{
			LegacyID: 1, Slug: "hello-world", Status: "publish", Type: "post",
			Title: "Hello World", ContentHTML: "<p>Hi there.</p>", ExcerptHTML: "Hi.",
			AuthorID: 7, FeaturedMedia: 0, Format: "standard",
			Categories: []int{10}, Tags: []int{20},
			Date: t1, DateGMT: t1, Modified: t1, ModifiedGMT: t1,
		},
		{
			LegacyID: 2, Slug: "second", Status: "publish", Type: "post",
			Title: "Second Post", ContentHTML: "<p>Second.</p>", ExcerptHTML: "Second.",
			AuthorID: 7, Format: "standard",
			Categories: []int{10, 11}, Tags: []int{},
			Date: t2, DateGMT: t2, Modified: t2, ModifiedGMT: t2,
		},
		{
			LegacyID: 3, Slug: "third", Status: "publish", Type: "post",
			Title: "Third Sticky", ContentHTML: "<p>Third.</p>", ExcerptHTML: "Third.",
			AuthorID: 8, Format: "standard", Sticky: true,
			Categories: []int{11}, Tags: []int{21},
			Date: t3, DateGMT: t3, Modified: t3, ModifiedGMT: t3,
		},
		{
			LegacyID: 4, Slug: "draft-post", Status: "draft", Type: "post",
			Title: "Draft", ContentHTML: "<p>Draft.</p>",
			AuthorID: 7, Format: "standard",
			Date: t3, DateGMT: t3, Modified: t3, ModifiedGMT: t3,
		},
	}
}

func seedPages() []PostRow {
	t1, _ := time.Parse(time.RFC3339, "2024-01-15T10:00:00Z")
	return []PostRow{
		{
			LegacyID: 100, Slug: "about", Status: "publish", Type: "page",
			Title: "About", ContentHTML: "<p>About us.</p>",
			AuthorID: 7,
			Date:     t1, DateGMT: t1, Modified: t1, ModifiedGMT: t1,
		},
	}
}

func seedUsers() []UserRow {
	return []UserRow{
		{LegacyID: 7, Slug: "alice", Name: "Alice", Description: "Author.", URL: "https://alice.example", AvatarURL: "https://gravatar/alice"},
		{LegacyID: 8, Slug: "bob", Name: "Bob", Description: "Editor.", AvatarURL: "https://gravatar/bob"},
	}
}

func seedCategories() []TermRow {
	return []TermRow{
		{LegacyID: 10, Slug: "news", Name: "News", Description: "News stuff", Count: 2, Taxonomy: "category"},
		{LegacyID: 11, Slug: "tech", Name: "Tech", Description: "Tech stuff", Count: 2, Taxonomy: "category"},
	}
}

func seedTags() []TermRow {
	return []TermRow{
		{LegacyID: 20, Slug: "intro", Name: "Intro", Count: 1, Taxonomy: "post_tag"},
		{LegacyID: 21, Slug: "deep", Name: "Deep", Count: 1, Taxonomy: "post_tag"},
	}
}

// -----------------------------------------------------------------------------
// Mount validation
// -----------------------------------------------------------------------------

func TestMount_RequiresPostsSource(t *testing.T) {
	t.Parallel()
	err := Mount(http.NewServeMux(), Deps{Pages: &fakePostSource{}, SiteURL: "x"})
	if err == nil {
		t.Fatal("expected error when Posts is nil")
	}
}

func TestMount_RequiresSiteURL(t *testing.T) {
	t.Parallel()
	err := Mount(http.NewServeMux(), Deps{Posts: &fakePostSource{}, Pages: &fakePostSource{}})
	if err == nil {
		t.Fatal("expected error when SiteURL is empty")
	}
}

// -----------------------------------------------------------------------------
// Posts: listing
// -----------------------------------------------------------------------------

func TestListPosts_DefaultPublishedOnly(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	rec := h.do("GET", "/wp-json/wp/v2/posts")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var got []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	// Only publish status, draft is filtered.
	if len(got) != 3 {
		t.Fatalf("got %d, want 3 published posts; body=%s", len(got), rec.Body.String())
	}
	if rec.Header().Get("X-WP-Total") != "3" {
		t.Errorf("X-WP-Total = %q, want 3", rec.Header().Get("X-WP-Total"))
	}
	if rec.Header().Get("X-WP-TotalPages") != "1" {
		t.Errorf("X-WP-TotalPages = %q, want 1", rec.Header().Get("X-WP-TotalPages"))
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestListPosts_PostShape(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	rec := h.do("GET", "/wp-json/wp/v2/posts?per_page=1&page=1&orderby=id&order=asc")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 row, got %d", len(got))
	}
	post := got[0]

	// WP-shape assertions: every field must be present and correctly typed.
	mustEqual(t, post["id"], float64(1))
	mustEqual(t, post["slug"], "hello-world")
	mustEqual(t, post["status"], "publish")
	mustEqual(t, post["type"], "post")
	mustEqual(t, post["link"], "https://example.test/hello-world/")
	mustEqual(t, post["author"], float64(7))

	title, ok := post["title"].(map[string]any)
	if !ok {
		t.Fatalf("title is not an object: %T", post["title"])
	}
	mustEqual(t, title["rendered"], "Hello World")

	content, ok := post["content"].(map[string]any)
	if !ok {
		t.Fatalf("content is not an object")
	}
	mustEqual(t, content["rendered"], "<p>Hi there.</p>")
	mustEqual(t, content["protected"], false)

	excerpt, ok := post["excerpt"].(map[string]any)
	if !ok {
		t.Fatalf("excerpt is not an object")
	}
	mustEqual(t, excerpt["rendered"], "Hi.")

	// _links is always populated; _embedded only with ?_embed.
	if _, ok := post["_links"]; !ok {
		t.Fatalf("_links missing: body=%s", rec.Body.String())
	}
	if _, ok := post["_embedded"]; ok {
		t.Errorf("_embedded should not be set without ?_embed")
	}
}

func TestListPosts_Pagination(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// per_page=2 → 2 pages for 3 publish posts.
	rec := h.do("GET", "/wp-json/wp/v2/posts?per_page=2&page=1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Header().Get("X-WP-Total") != "3" {
		t.Errorf("X-WP-Total = %q", rec.Header().Get("X-WP-Total"))
	}
	if rec.Header().Get("X-WP-TotalPages") != "2" {
		t.Errorf("X-WP-TotalPages = %q", rec.Header().Get("X-WP-TotalPages"))
	}
	var page1 []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &page1); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(page1) != 2 {
		t.Errorf("page1 length = %d, want 2", len(page1))
	}

	rec2 := h.do("GET", "/wp-json/wp/v2/posts?per_page=2&page=2")
	if rec2.Code != http.StatusOK {
		t.Fatalf("page2 status = %d", rec2.Code)
	}
	var page2 []map[string]any
	if err := json.Unmarshal(rec2.Body.Bytes(), &page2); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(page2) != 1 {
		t.Errorf("page2 length = %d, want 1", len(page2))
	}
}

func TestListPosts_StickyFloatsToTop(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	rec := h.do("GET", "/wp-json/wp/v2/posts") // default date desc
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) < 1 {
		t.Fatalf("no rows")
	}
	if id := got[0]["id"]; id != float64(3) {
		t.Errorf("first id = %v, want 3 (the sticky)", id)
	}
}

func TestListPosts_FilterByCategory(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	rec := h.do("GET", "/wp-json/wp/v2/posts?categories=11")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Posts 2 and 3 are in category 11.
	if len(got) != 2 {
		t.Errorf("got %d, want 2", len(got))
	}
}

func TestListPosts_FilterByCategoryArrayForm(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	// PHP-array form ?categories[]=10
	rec := h.do("GET", "/wp-json/wp/v2/posts?categories%5B%5D=10")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %d, want 2 (posts 1+2 in cat 10)", len(got))
	}
}

func TestListPosts_FilterByTag(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	rec := h.do("GET", "/wp-json/wp/v2/posts?tags=21")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0]["id"] != float64(3) {
		t.Errorf("got %v", got)
	}
}

func TestListPosts_Search(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	rec := h.do("GET", "/wp-json/wp/v2/posts?search=second")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got []map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got) != 1 || got[0]["id"] != float64(2) {
		t.Errorf("got %+v", got)
	}
}

func TestListPosts_InvalidPage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	rec := h.do("GET", "/wp-json/wp/v2/posts?page=abc")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	assertWPError(t, rec, errCodeInvalidParam, http.StatusBadRequest)
}

func TestListPosts_OutOfRangePage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	rec := h.do("GET", "/wp-json/wp/v2/posts?per_page=10&page=99")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if strings.TrimSpace(rec.Body.String()) != "null" && rec.Body.String() != "[]\n" {
		var got []map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v body=%q", err, rec.Body.String())
		}
		if len(got) != 0 {
			t.Errorf("want empty, got %d rows", len(got))
		}
	}
}

func TestListPosts_PerPageClamped(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	rec := h.do("GET", "/wp-json/wp/v2/posts?per_page=999")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	// Just ensure the header pagination math used the clamp.
	if rec.Header().Get("X-WP-TotalPages") != "1" {
		t.Errorf("X-WP-TotalPages = %q", rec.Header().Get("X-WP-TotalPages"))
	}
}

// -----------------------------------------------------------------------------
// Posts: single resource
// -----------------------------------------------------------------------------

func TestGetPost_Happy(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	rec := h.do("GET", "/wp-json/wp/v2/posts/1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	mustEqual(t, got["id"], float64(1))
	mustEqual(t, got["slug"], "hello-world")
}

func TestGetPost_404(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	rec := h.do("GET", "/wp-json/wp/v2/posts/9999")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
	assertWPError(t, rec, errCodeInvalidPostID, http.StatusNotFound)
}

func TestGetPost_NonNumericID(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	rec := h.do("GET", "/wp-json/wp/v2/posts/abc")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
	assertWPError(t, rec, errCodeInvalidPostID, http.StatusNotFound)
}

func TestGetPost_RejectsPageThroughPostsRoute(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	// Inject a page-typed row into the posts source via the test
	// substrate to verify the defensive type check fires.
	h.posts.rows = append(h.posts.rows, PostRow{LegacyID: 555, Type: "page", Status: "publish"})
	rec := h.do("GET", "/wp-json/wp/v2/posts/555")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestGetPost_StoreError(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.posts.err = errors.New("boom")
	rec := h.do("GET", "/wp-json/wp/v2/posts/1")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
}

// -----------------------------------------------------------------------------
// Pages
// -----------------------------------------------------------------------------

func TestListPages_Shape(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	rec := h.do("GET", "/wp-json/wp/v2/pages")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 page, got %d", len(got))
	}
	mustEqual(t, got[0]["type"], "page")
}

func TestGetPage_404(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	rec := h.do("GET", "/wp-json/wp/v2/pages/9999")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
	assertWPError(t, rec, errCodeInvalidPageID, http.StatusNotFound)
}

// -----------------------------------------------------------------------------
// Embed
// -----------------------------------------------------------------------------

func TestListPosts_EmbedPopulatesAuthorAndTerms(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	rec := h.do("GET", "/wp-json/wp/v2/posts?_embed&orderby=id&order=asc&per_page=1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 row")
	}
	embedded, ok := got[0]["_embedded"].(map[string]any)
	if !ok {
		t.Fatalf("_embedded missing or wrong type: %v", got[0]["_embedded"])
	}
	authors, ok := embedded["author"].([]any)
	if !ok || len(authors) != 1 {
		t.Fatalf("embedded author wrong shape: %v", embedded["author"])
	}
	a := authors[0].(map[string]any)
	mustEqual(t, a["id"], float64(7))
	mustEqual(t, a["slug"], "alice")

	terms, ok := embedded["wp:term"].([]any)
	if !ok || len(terms) != 2 {
		t.Fatalf("wp:term wrong shape: %v", embedded["wp:term"])
	}
}

func TestGetPost_LinksHaveSelfAndCollection(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	rec := h.do("GET", "/wp-json/wp/v2/posts/1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	links, _ := got["_links"].(map[string]any)
	if links == nil {
		t.Fatalf("_links missing")
	}
	self, _ := links["self"].([]any)
	if len(self) != 1 {
		t.Fatalf("self link wrong shape")
	}
	mustEqual(t, self[0].(map[string]any)["href"], "https://example.test/wp-json/wp/v2/posts/1")
	coll, _ := links["collection"].([]any)
	if len(coll) != 1 {
		t.Fatalf("collection link wrong shape")
	}
}

// -----------------------------------------------------------------------------
// Users
// -----------------------------------------------------------------------------

func TestListUsers_Shape(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	rec := h.do("GET", "/wp-json/wp/v2/users")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got []map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got) != 2 {
		t.Fatalf("want 2 users, got %d", len(got))
	}
	mustEqual(t, got[0]["slug"], "alice")
	if _, ok := got[0]["avatar_urls"].(map[string]any); !ok {
		t.Errorf("avatar_urls missing or wrong type")
	}
	// Sensitive fields must be absent.
	if _, ok := got[0]["email"]; ok {
		t.Errorf("email leaked into unauthenticated response")
	}
	if _, ok := got[0]["roles"]; ok {
		t.Errorf("roles leaked into unauthenticated response")
	}
}

func TestGetUser_404(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	rec := h.do("GET", "/wp-json/wp/v2/users/9999")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
	assertWPError(t, rec, errCodeInvalidUserID, http.StatusNotFound)
}

func TestUsersWhenNilSource_EmptyArray(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	if err := Mount(mux, Deps{
		Posts: &fakePostSource{}, Pages: &fakePostSource{},
		SiteURL: "https://example.test",
	}); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/wp-json/wp/v2/users", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Header().Get("X-WP-Total") != "0" {
		t.Errorf("X-WP-Total = %q", rec.Header().Get("X-WP-Total"))
	}
}

// -----------------------------------------------------------------------------
// Terms (categories + tags)
// -----------------------------------------------------------------------------

func TestListCategories_Shape(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	rec := h.do("GET", "/wp-json/wp/v2/categories")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got []map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got) != 2 {
		t.Fatalf("want 2 categories, got %d", len(got))
	}
	mustEqual(t, got[0]["taxonomy"], "category")
}

func TestListTags_Shape(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	rec := h.do("GET", "/wp-json/wp/v2/tags")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got []map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got) != 2 {
		t.Fatalf("want 2 tags, got %d", len(got))
	}
	mustEqual(t, got[0]["taxonomy"], "post_tag")
}

func TestGetCategory_404(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	rec := h.do("GET", "/wp-json/wp/v2/categories/9999")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
	assertWPError(t, rec, errCodeInvalidTermID, http.StatusNotFound)
}

func TestGetTag_Happy(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	rec := h.do("GET", "/wp-json/wp/v2/tags/20")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	mustEqual(t, got["taxonomy"], "post_tag")
	mustEqual(t, got["slug"], "intro")
}

// -----------------------------------------------------------------------------
// Write methods refused
// -----------------------------------------------------------------------------

func TestWriteMethodsRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	cases := []struct {
		method string
		path   string
	}{
		{"POST", "/wp-json/wp/v2/posts"},
		{"PUT", "/wp-json/wp/v2/posts/1"},
		{"PATCH", "/wp-json/wp/v2/posts/1"},
		{"DELETE", "/wp-json/wp/v2/posts/1"},
		{"POST", "/wp-json/wp/v2/pages"},
		{"PUT", "/wp-json/wp/v2/pages/1"},
		{"DELETE", "/wp-json/wp/v2/categories/10"},
		{"PATCH", "/wp-json/wp/v2/tags/20"},
		{"POST", "/wp-json/wp/v2/users"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.method+"_"+c.path, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(c.method, c.path, strings.NewReader(`{}`))
			rec := httptest.NewRecorder()
			h.mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("%s %s: status = %d body=%s", c.method, c.path, rec.Code, rec.Body.String())
			}
			if got := rec.Header().Get("Allow"); got != "GET" {
				t.Errorf("Allow header = %q, want GET", got)
			}
			assertWPError(t, rec, errCodeMethodNotAllowed, http.StatusMethodNotAllowed)
		})
	}
}

// -----------------------------------------------------------------------------
// Concurrency safety
// -----------------------------------------------------------------------------

func TestListPosts_ConcurrentReads(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	done := make(chan struct{}, 32)
	for i := 0; i < 32; i++ {
		go func(i int) {
			rec := h.do("GET", "/wp-json/wp/v2/posts?per_page=2&page="+strconv.Itoa((i%2)+1))
			if rec.Code != http.StatusOK {
				t.Errorf("iter %d status = %d", i, rec.Code)
			}
			done <- struct{}{}
		}(i)
	}
	for i := 0; i < 32; i++ {
		<-done
	}
}

// -----------------------------------------------------------------------------
// Pagination helper unit tests
// -----------------------------------------------------------------------------

func TestApplyPagination(t *testing.T) {
	t.Parallel()
	items := []int{1, 2, 3, 4, 5}
	cases := []struct {
		page, per int
		want      []int
	}{
		{1, 2, []int{1, 2}},
		{2, 2, []int{3, 4}},
		{3, 2, []int{5}},
		{4, 2, nil},
		{0, 2, []int{1, 2}}, // page=0 clamps to 1
		{1, 0, []int{1, 2, 3, 4, 5, /*defaults*/ }[:5]},
	}
	for i, c := range cases {
		got := applyPagination(items, c.page, c.per)
		if fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Errorf("case %d: got %v want %v", i, got, c.want)
		}
	}
}

func TestParseIntList_CSV(t *testing.T) {
	t.Parallel()
	got := parseIntList(map[string][]string{"categories": {"1,2, 3"}}, "categories")
	want := []int{1, 2, 3}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestParseIntList_ArrayForm(t *testing.T) {
	t.Parallel()
	got := parseIntList(map[string][]string{"categories[]": {"4", "5"}}, "categories")
	want := []int{4, 5}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestParseIntList_DropsNonNumeric(t *testing.T) {
	t.Parallel()
	got := parseIntList(map[string][]string{"categories": {"1,foo,3"}}, "categories")
	want := []int{1, 3}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v want %v", got, want)
	}
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

func mustEqual(t *testing.T, got, want any) {
	t.Helper()
	if got != want {
		t.Errorf("got %v (%T), want %v (%T)", got, got, want, want)
	}
}

func assertWPError(t *testing.T, rec *httptest.ResponseRecorder, wantCode string, wantStatus int) {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v body=%s", err, rec.Body.String())
	}
	if body["code"] != wantCode {
		t.Errorf("code = %v, want %s", body["code"], wantCode)
	}
	if _, ok := body["message"].(string); !ok {
		t.Errorf("message missing or not string: %v", body["message"])
	}
	data, ok := body["data"].(map[string]any)
	if !ok {
		t.Fatalf("data field missing or wrong shape: %v", body["data"])
	}
	if data["status"] != float64(wantStatus) {
		t.Errorf("data.status = %v, want %d", data["status"], wantStatus)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}
}
