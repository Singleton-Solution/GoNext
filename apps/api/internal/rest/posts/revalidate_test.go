package posts

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/Singleton-Solution/GoNext/packages/go/audit"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// recordingRevalidate is a RevalidateNotifier that captures the paths
// passed to Notify / NotifyMany so tests can assert what the handler
// hooked. It's the smallest possible test double — no thread-safety
// concerns because each test owns its own instance.
type recordingRevalidate struct {
	mu    sync.Mutex
	calls [][]string // each entry is the paths argument of one Notify(Many) call
}

func (r *recordingRevalidate) Notify(_ context.Context, path string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, []string{path})
	return nil
}

func (r *recordingRevalidate) NotifyMany(_ context.Context, paths []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, append([]string{}, paths...))
	return nil
}

func (r *recordingRevalidate) Calls() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([][]string, len(r.calls))
	for i, c := range r.calls {
		out[i] = append([]string{}, c...)
	}
	return out
}

func newRevalidateHarness(t *testing.T, postType string) (*testHarness, *recordingRevalidate) {
	t.Helper()
	mux := http.NewServeMux()
	store := NewMemoryStore()
	auditStore := audit.NewMemoryStore()
	em := audit.NewEmitter(auditStore)
	pol := policy.NewBasicPolicy(policy.DefaultRoleCapabilities())
	rec := &recordingRevalidate{}

	base := "/api/v1/posts"
	if postType == PostTypePage {
		base = "/api/v1/pages"
	}
	if err := Mount(mux, base, Deps{
		Store:      store,
		Policy:     pol,
		Audit:      em,
		PostType:   postType,
		Revalidate: rec,
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
	}, rec
}

func TestCreate_PublishedFiresRevalidate(t *testing.T) {
	t.Parallel()
	h, rec := newRevalidateHarness(t, PostTypePost)
	pr := editorPrincipal("u-1") // editor has publish_posts

	title := "Hello"
	status := "published"
	slug := "hello-world"
	req := httptest.NewRequest("POST", h.base, jsonBody(t, CreateInput{
		Title:  &title,
		Status: &status,
		Slug:   &slug,
	}))
	resp := h.do(req, &pr)
	if resp.Code != http.StatusCreated {
		t.Fatalf("status: %d, body=%s", resp.Code, resp.Body.String())
	}

	calls := rec.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	paths := calls[0]
	want := map[string]bool{"/posts/hello-world": false, "/": false}
	for _, p := range paths {
		if _, ok := want[p]; ok {
			want[p] = true
		}
	}
	for k, v := range want {
		if !v {
			t.Errorf("expected revalidate path %q, calls=%v", k, calls)
		}
	}
}

func TestCreate_DraftDoesNotFireRevalidate(t *testing.T) {
	t.Parallel()
	h, rec := newRevalidateHarness(t, PostTypePost)
	pr := authorPrincipal("u-1")

	title := "Draft"
	req := httptest.NewRequest("POST", h.base, jsonBody(t, CreateInput{Title: &title}))
	resp := h.do(req, &pr)
	if resp.Code != http.StatusCreated {
		t.Fatalf("status: %d, body=%s", resp.Code, resp.Body.String())
	}

	if calls := rec.Calls(); len(calls) != 0 {
		t.Fatalf("expected zero revalidate calls for draft, got %v", calls)
	}
}

func TestUpdate_PublishTransitionFiresRevalidate(t *testing.T) {
	t.Parallel()
	h, rec := newRevalidateHarness(t, PostTypePost)
	pr := editorPrincipal("u-1")

	// Create draft.
	title := "T"
	slug := "transition"
	createReq := httptest.NewRequest("POST", h.base, jsonBody(t, CreateInput{Title: &title, Slug: &slug}))
	createResp := h.do(createReq, &pr)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create: %d", createResp.Code)
	}
	var created Post
	decodeJSON(t, createResp, &created)

	if calls := rec.Calls(); len(calls) != 0 {
		t.Fatalf("draft create should not revalidate, got %v", calls)
	}

	// Transition to published.
	status := "published"
	updateReq := httptest.NewRequest("PATCH", h.base+"/"+created.ID, jsonBody(t, UpdateInput{Status: &status}))
	updateReq.Header.Set("If-Match", `"`+strconv.Itoa(created.Version)+`"`)
	updateResp := h.do(updateReq, &pr)
	if updateResp.Code != http.StatusOK {
		t.Fatalf("update: %d body=%s", updateResp.Code, updateResp.Body.String())
	}

	calls := rec.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call after publish, got %d", len(calls))
	}
	found := false
	for _, p := range calls[0] {
		if p == "/posts/transition" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected /posts/transition in calls, got %v", calls)
	}
}

func TestPage_PublishFiresRevalidateWithoutHomepage(t *testing.T) {
	t.Parallel()
	h, rec := newRevalidateHarness(t, PostTypePage)
	pr := editorPrincipal("u-1")

	title := "About"
	status := "published"
	slug := "about"
	req := httptest.NewRequest("POST", h.base, jsonBody(t, CreateInput{
		Title: &title, Status: &status, Slug: &slug,
	}))
	resp := h.do(req, &pr)
	if resp.Code != http.StatusCreated {
		t.Fatalf("status: %d, body=%s", resp.Code, resp.Body.String())
	}

	calls := rec.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	// Pages do NOT push the homepage feed — they're addressed by slug
	// off the root path only.
	for _, p := range calls[0] {
		if p == "/" {
			t.Errorf("page publish should not revalidate /, got %v", calls[0])
		}
	}
	found := false
	for _, p := range calls[0] {
		if p == "/about" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected /about in calls, got %v", calls)
	}
}

func TestRevalidate_NilNotifierIsTolerated(t *testing.T) {
	t.Parallel()
	// No revalidate dep — the handler should noop without crashing.
	mux := http.NewServeMux()
	store := NewMemoryStore()
	auditStore := audit.NewMemoryStore()
	em := audit.NewEmitter(auditStore)
	pol := policy.NewBasicPolicy(policy.DefaultRoleCapabilities())
	if err := Mount(mux, "/api/v1/posts", Deps{
		Store: store, Policy: pol, Audit: em, PostType: PostTypePost,
	}); err != nil {
		t.Fatalf("Mount: %v", err)
	}

	pr := editorPrincipal("u-1")
	title := "X"
	status := "published"
	slug := "x"
	req := httptest.NewRequest("POST", "/api/v1/posts", jsonBody(t, CreateInput{
		Title: &title, Status: &status, Slug: &slug,
	})).WithContext(policy.WithPrincipal(context.Background(), pr))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	// Body should still serialize; just confirm no crash.
	if !strings.Contains(rec.Body.String(), `"slug":"x"`) {
		t.Errorf("body: %s", rec.Body.String())
	}
}
