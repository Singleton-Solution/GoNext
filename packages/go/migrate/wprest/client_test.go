package wprest

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Singleton-Solution/GoNext/packages/go/migrate/wxr"
)

// ---------------------------------------------------------------------
// Unit tests for the standalone helpers + Config validation.
// ---------------------------------------------------------------------

func TestNewClient_Validation(t *testing.T) {
	if _, err := NewClient(Config{}); err == nil {
		t.Error("empty BaseURL should error")
	}
	if _, err := NewClient(Config{BaseURL: "not a url"}); err == nil {
		t.Error("invalid BaseURL should error")
	}
	if _, err := NewClient(Config{BaseURL: "/just-a-path"}); err == nil {
		t.Error("BaseURL without scheme should error")
	}
}

func TestNewClient_DefaultsAndAuth(t *testing.T) {
	c, err := NewClient(Config{
		BaseURL:     "https://wp.example.com/",
		User:        "alice",
		AppPassword: "abc def ghi",
		PerPage:     250, // clamps to 100
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if c.perPage != 100 {
		t.Errorf("perPage clamp: got %d want 100", c.perPage)
	}
	if !strings.HasSuffix(c.baseURL, "/wp-json/wp/v2/") {
		t.Errorf("baseURL: %q", c.baseURL)
	}
	// Auth header reflects base64("alice:abcdefghi") (spaces stripped).
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("alice:abcdefghi"))
	if c.authHeader != want {
		t.Errorf("auth header:\n got: %q\nwant: %q", c.authHeader, want)
	}
}

func TestNewClient_PerPageDefault(t *testing.T) {
	c, _ := NewClient(Config{BaseURL: "https://x.example/"})
	if c.perPage != 100 {
		t.Errorf("perPage default: got %d want 100", c.perPage)
	}
}

// ---------------------------------------------------------------------
// Integration test: spin up an httptest.Server that replays the
// fixtures from testdata/. The server validates the auth header and
// reports X-WP-TotalPages so pagination is exercised.
// ---------------------------------------------------------------------

// testServer wires the fixture replay logic. Returns the server URL,
// the recorded request log, and a cleanup func.
func testServer(t *testing.T) (string, *[]string, func()) {
	t.Helper()
	var log []string
	mux := http.NewServeMux()
	dir := "testdata"
	// Helper that serves a fixture file or 404.
	serve := func(filename string, totalPages int) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			log = append(log, r.URL.String())
			// Validate auth on every request.
			if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "Basic ") {
				t.Errorf("missing/bad auth header on %s: %q", r.URL.String(), got)
			}
			data, err := os.ReadFile(filepath.Join(dir, filename))
			if err != nil {
				http.Error(w, "no fixture: "+err.Error(), http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-WP-TotalPages", strconv.Itoa(totalPages))
			_, _ = w.Write(data)
		}
	}

	// Page-aware handler that dispatches to *_pageN.json fixtures.
	paged := func(prefix string, totalPages int) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			page := 1
			if p := r.URL.Query().Get("page"); p != "" {
				if n, err := strconv.Atoi(p); err == nil && n > 0 {
					page = n
				}
			}
			fname := prefix + "_page" + strconv.Itoa(page) + ".json"
			serve(fname, totalPages)(w, r)
		}
	}

	mux.HandleFunc("/wp-json/wp/v2/users", paged("users", 1))
	mux.HandleFunc("/wp-json/wp/v2/categories", paged("categories", 1))
	mux.HandleFunc("/wp-json/wp/v2/tags", paged("tags", 1))
	mux.HandleFunc("/wp-json/wp/v2/posts", paged("posts", 2))
	mux.HandleFunc("/wp-json/wp/v2/pages", paged("pages", 1))
	mux.HandleFunc("/wp-json/wp/v2/media", paged("media", 1))

	srv := httptest.NewServer(mux)
	return srv.URL, &log, srv.Close
}

func TestClient_FetchAuthors(t *testing.T) {
	base, _, cleanup := testServer(t)
	defer cleanup()

	c, err := NewClient(Config{
		BaseURL:     base,
		User:        "u",
		AppPassword: "p",
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	var authors []*wxr.Author
	if err := c.FetchAuthors(context.Background(), func(a *wxr.Author) error {
		authors = append(authors, a)
		return nil
	}); err != nil {
		t.Fatalf("FetchAuthors: %v", err)
	}
	if len(authors) != 2 {
		t.Fatalf("authors: got %d want 2", len(authors))
	}
	if authors[0].Login != "admin" || authors[0].Email != "admin@example.com" {
		t.Errorf("authors[0]: %+v", authors[0])
	}
	if authors[1].DisplayName != "Edna Editor" {
		t.Errorf("authors[1] display: %q", authors[1].DisplayName)
	}
}

func TestClient_FetchCategoriesAndTags(t *testing.T) {
	base, _, cleanup := testServer(t)
	defer cleanup()
	c, _ := NewClient(Config{BaseURL: base, User: "u", AppPassword: "p"})

	var cats []*wxr.Category
	if err := c.FetchCategories(context.Background(), func(t *wxr.Category) error {
		cats = append(cats, t)
		return nil
	}); err != nil {
		t.Fatalf("FetchCategories: %v", err)
	}
	if len(cats) != 3 {
		t.Fatalf("cats: got %d want 3", len(cats))
	}
	// Hierarchical: golang's parent should be "2" (id of Tech).
	if cats[2].Nicename != "golang" || cats[2].Parent != "2" {
		t.Errorf("golang cat: %+v", cats[2])
	}

	var tags []*wxr.Tag
	if err := c.FetchTags(context.Background(), func(t *wxr.Tag) error {
		tags = append(tags, t)
		return nil
	}); err != nil {
		t.Fatalf("FetchTags: %v", err)
	}
	if len(tags) != 1 || tags[0].Slug != "news" {
		t.Errorf("tags: %+v", tags)
	}
}

func TestClient_FetchPosts_Paginated(t *testing.T) {
	base, log, cleanup := testServer(t)
	defer cleanup()
	c, _ := NewClient(Config{BaseURL: base, User: "u", AppPassword: "p"})

	var posts []*wxr.Post
	if err := c.FetchPosts(context.Background(), func(p *wxr.Post) error {
		posts = append(posts, p)
		return nil
	}); err != nil {
		t.Fatalf("FetchPosts: %v", err)
	}
	if len(posts) != 3 {
		t.Fatalf("posts: got %d want 3 (2 page1 + 1 page2)", len(posts))
	}
	if posts[0].Title != "Hello World" || posts[0].Name != "hello-world" {
		t.Errorf("posts[0]: %+v", posts[0])
	}
	if posts[0].IsSticky != "1" {
		t.Errorf("sticky should be '1': %q", posts[0].IsSticky)
	}
	if posts[1].Status != "draft" {
		t.Errorf("draft status: %q", posts[1].Status)
	}
	// posts[0] is in categories [1,2] + tag [10] → 3 term refs.
	if len(posts[0].Terms) != 3 {
		t.Errorf("posts[0].Terms: got %d want 3", len(posts[0].Terms))
	}
	// Make sure both pages were fetched.
	pages := 0
	for _, e := range *log {
		if strings.HasPrefix(e, "/wp-json/wp/v2/posts") {
			pages++
		}
	}
	if pages != 2 {
		t.Errorf("expected 2 page GETs, got %d (log: %v)", pages, *log)
	}
}

func TestClient_FetchMedia(t *testing.T) {
	base, _, cleanup := testServer(t)
	defer cleanup()
	c, _ := NewClient(Config{BaseURL: base, User: "u", AppPassword: "p"})

	var media []*wxr.Post
	if err := c.FetchMedia(context.Background(), func(p *wxr.Post) error {
		media = append(media, p)
		return nil
	}); err != nil {
		t.Fatalf("FetchMedia: %v", err)
	}
	if len(media) != 1 {
		t.Fatalf("media: got %d want 1", len(media))
	}
	m := media[0]
	if m.PostType != "attachment" {
		t.Errorf("PostType: %q want attachment", m.PostType)
	}
	if m.AttachmentURL != "https://example.com/wp-content/uploads/2024/03/photo.jpg" {
		t.Errorf("AttachmentURL: %q", m.AttachmentURL)
	}
	if m.Meta["wp_mime_type"] != "image/jpeg" {
		t.Errorf("wp_mime_type: %q", m.Meta["wp_mime_type"])
	}
	if m.Meta["wp_alt_text"] != "A photo" {
		t.Errorf("wp_alt_text: %q", m.Meta["wp_alt_text"])
	}
}

func TestClient_FetchAll(t *testing.T) {
	base, _, cleanup := testServer(t)
	defer cleanup()
	c, _ := NewClient(Config{BaseURL: base, User: "u", AppPassword: "p"})

	counts := map[string]int{}
	if err := c.FetchAll(context.Background(), func(rec wxr.Record) error {
		counts[rec.Kind()]++
		return nil
	}); err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	want := map[string]int{
		"author":   2,
		"category": 3,
		"tag":      1,
		"post":     5, // 3 posts + 1 page + 1 media (all use kind "post" via *wxr.Post)
	}
	for k, v := range want {
		if counts[k] != v {
			t.Errorf("kind %q: got %d want %d", k, counts[k], v)
		}
	}
}

func TestClient_FetchAuthors_CallbackError(t *testing.T) {
	base, _, cleanup := testServer(t)
	defer cleanup()
	c, _ := NewClient(Config{BaseURL: base, User: "u", AppPassword: "p"})

	stop := errors.New("stop")
	err := c.FetchAuthors(context.Background(), func(a *wxr.Author) error {
		return stop
	})
	if !errors.Is(err, stop) {
		t.Errorf("expected sentinel propagation, got %v", err)
	}
}

func TestClient_FetchAuthors_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code":"rest_forbidden","message":"Sorry."}`))
	}))
	defer srv.Close()

	c, _ := NewClient(Config{BaseURL: srv.URL, User: "u", AppPassword: "p"})
	err := c.FetchAuthors(context.Background(), func(*wxr.Author) error { return nil })
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "Sorry") {
		t.Errorf("error should mention status and WP message: %v", err)
	}
}

func TestClient_ContextCancel(t *testing.T) {
	// Hang the server until the context is cancelled.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	c, _ := NewClient(Config{BaseURL: srv.URL, User: "u", AppPassword: "p"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := c.FetchAuthors(ctx, func(*wxr.Author) error { return nil })
	if err == nil {
		t.Fatal("expected error after cancel")
	}
}

func TestClient_PerPageQuery(t *testing.T) {
	// Verify the client sends per_page and page query params and
	// status=any on posts.
	var captured *url.URL
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.URL
		w.Header().Set("X-WP-TotalPages", "1")
		_, _ = w.Write([]byte("[]"))
	}))
	defer srv.Close()
	c, _ := NewClient(Config{BaseURL: srv.URL, User: "u", AppPassword: "p"})
	if err := c.FetchPosts(context.Background(), func(*wxr.Post) error { return nil }); err != nil {
		t.Fatalf("FetchPosts: %v", err)
	}
	if captured == nil {
		t.Fatal("no request captured")
	}
	q := captured.Query()
	if q.Get("per_page") != "100" {
		t.Errorf("per_page: %q want 100", q.Get("per_page"))
	}
	if q.Get("page") != "1" {
		t.Errorf("page: %q want 1", q.Get("page"))
	}
	if q.Get("status") != "any" {
		t.Errorf("status: %q want any", q.Get("status"))
	}
	if q.Get("context") != "edit" {
		t.Errorf("context: %q want edit", q.Get("context"))
	}
}
