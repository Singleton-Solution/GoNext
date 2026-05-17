package resolvers_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/99designs/gqlgen/client"
	gqlhandler "github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/transport"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/graphql/dataloader"
	"github.com/Singleton-Solution/GoNext/apps/api/internal/graphql/generated"
	"github.com/Singleton-Solution/GoNext/apps/api/internal/graphql/resolvers"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// buildServer constructs an httptest server with the GraphQL handler
// wired to the supplied repos. The caller optionally hooks in a
// per-request principal-injecting middleware so tests can simulate
// authenticated requests.
func buildServer(t *testing.T, posts *memPostRepo, users *memUserRepo, pol policy.Policy, principal *policy.Principal) *httptest.Server {
	t.Helper()
	r := &resolvers.Resolver{
		PostRepo: posts,
		UserRepo: users,
		Policy:   pol,
	}
	es := generated.NewExecutableSchema(generated.Config{Resolvers: r})
	srv := gqlhandler.New(es)
	srv.AddTransport(transport.POST{})
	srv.AddTransport(transport.GET{})
	srv.Use(extension.Introspection{})

	h := http.Handler(srv)
	// Attach per-request dataloader so the N+1 test sees the batched call.
	h = func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			loaders := dataloader.New(func(ctx context.Context, ids []string) ([]*dataloader.UserRow, error) {
				rows, err := users.ByIDs(ctx, ids)
				if err != nil {
					return nil, err
				}
				out := make([]*dataloader.UserRow, len(rows))
				for i, row := range rows {
					if row == nil {
						continue
					}
					out[i] = &dataloader.UserRow{
						ID:          row.ID,
						Handle:      row.Handle,
						DisplayName: row.DisplayName,
						Email:       row.Email,
						CreatedAt:   row.CreatedAt,
					}
				}
				return out, nil
			})
			ctx := dataloader.Attach(req.Context(), loaders)
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	}(h)
	if principal != nil {
		p := *principal
		h = func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				next.ServeHTTP(w, req.WithContext(policy.WithPrincipal(req.Context(), p)))
			})
		}(h)
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return ts
}

// newPolicy returns a BasicPolicy with the standard role caps.
// Tests that need a principal with specific caps build it inline.
func newPolicy() policy.Policy {
	return policy.NewBasicPolicy(map[policy.Role]policy.CapabilitySet{
		"author": policy.NewCapabilitySet(policy.CapEditPosts, policy.CapPublishPosts),
		"editor": policy.NewCapabilitySet(
			policy.CapEditPosts, policy.CapPublishPosts,
			policy.CapReadPrivatePosts, policy.CapEditOthersPosts,
			policy.CapListUsers,
		),
	})
}

// TestViewerAnonymous: an unauthenticated request to `viewer` returns
// null. No GraphQL errors are emitted — this matches the Relay
// convention.
func TestViewerAnonymous(t *testing.T) {
	t.Parallel()
	users := newMemUserRepo()
	posts := newMemPostRepo()
	ts := buildServer(t, posts, users, newPolicy(), nil)

	resp := postGQL(t, ts.URL, `query { viewer { id } }`, nil)
	if len(resp.Errors) != 0 {
		t.Fatalf("unexpected errors: %+v", resp.Errors)
	}
	if got, ok := resp.Data["viewer"]; !ok || got != nil {
		t.Fatalf("expected viewer=null, got %+v (ok=%v)", got, ok)
	}
}

// TestViewerAuthenticated: a request with a principal returns the
// user's record, including the email field (self always sees email).
func TestViewerAuthenticated(t *testing.T) {
	t.Parallel()
	users := newMemUserRepo(resolvers.UserRow{
		ID: "u1", Handle: "alice", Email: "alice@example.com", CreatedAt: fixedTime(),
	})
	posts := newMemPostRepo()
	p := &policy.Principal{UserID: "u1", Roles: []policy.Role{"author"}}
	ts := buildServer(t, posts, users, newPolicy(), p)

	resp := postGQL(t, ts.URL, `query { viewer { id handle email } }`, nil)
	if len(resp.Errors) != 0 {
		t.Fatalf("unexpected errors: %+v", resp.Errors)
	}
	viewer, ok := resp.Data["viewer"].(map[string]any)
	if !ok {
		t.Fatalf("viewer missing or wrong type: %+v", resp.Data)
	}
	if viewer["id"] != "u1" {
		t.Errorf("id: got %v, want u1", viewer["id"])
	}
	if viewer["handle"] != "alice" {
		t.Errorf("handle: got %v, want alice", viewer["handle"])
	}
	if viewer["email"] != "alice@example.com" {
		t.Errorf("email: got %v, want alice@example.com", viewer["email"])
	}
}

// TestPostQueryFields: post(id:) returns the expected fields for a
// published post.
func TestPostQueryFields(t *testing.T) {
	t.Parallel()
	users := newMemUserRepo(resolvers.UserRow{
		ID: "u1", Handle: "alice", Email: "alice@example.com", CreatedAt: fixedTime(),
	})
	excerpt := "hello"
	posts := newMemPostRepo(resolvers.PostRow{
		ID:        "p1",
		Title:     "Hello",
		Slug:      "hello",
		Status:    "PUBLISHED",
		Excerpt:   &excerpt,
		AuthorID:  "u1",
		CreatedAt: fixedTime(),
		UpdatedAt: fixedTime(),
	})
	ts := buildServer(t, posts, users, newPolicy(), nil)

	resp := postGQL(t, ts.URL, `query { post(id: "p1") { id title slug status excerpt authorID } }`, nil)
	if len(resp.Errors) != 0 {
		t.Fatalf("unexpected errors: %+v", resp.Errors)
	}
	post, ok := resp.Data["post"].(map[string]any)
	if !ok {
		t.Fatalf("post missing or wrong type: %+v", resp.Data)
	}
	if post["id"] != "p1" || post["title"] != "Hello" || post["slug"] != "hello" {
		t.Errorf("post fields wrong: %+v", post)
	}
	if post["status"] != "PUBLISHED" {
		t.Errorf("status: got %v, want PUBLISHED", post["status"])
	}
	if post["excerpt"] != "hello" {
		t.Errorf("excerpt: got %v, want hello", post["excerpt"])
	}
	if post["authorID"] != "u1" {
		t.Errorf("authorID: got %v, want u1", post["authorID"])
	}
}

// TestPostQueryNotFound: post(id:) on an unknown id returns null.
func TestPostQueryNotFound(t *testing.T) {
	t.Parallel()
	ts := buildServer(t, newMemPostRepo(), newMemUserRepo(), newPolicy(), nil)
	resp := postGQL(t, ts.URL, `query { post(id: "nope") { id } }`, nil)
	if len(resp.Errors) != 0 {
		t.Fatalf("unexpected errors: %+v", resp.Errors)
	}
	if resp.Data["post"] != nil {
		t.Fatalf("expected null, got %+v", resp.Data["post"])
	}
}

// TestPostsDraftHidden: anonymous request to `posts` filters out
// non-published rows.
func TestPostsDraftHidden(t *testing.T) {
	t.Parallel()
	users := newMemUserRepo(resolvers.UserRow{
		ID: "u1", Handle: "alice", Email: "a@x", CreatedAt: fixedTime(),
	})
	posts := newMemPostRepo(
		resolvers.PostRow{ID: "p1", Title: "Published", Slug: "p1", Status: "PUBLISHED", AuthorID: "u1", CreatedAt: fixedTime(), UpdatedAt: fixedTime()},
		resolvers.PostRow{ID: "p2", Title: "Draft", Slug: "p2", Status: "DRAFT", AuthorID: "u1", CreatedAt: fixedTime(), UpdatedAt: fixedTime()},
	)
	ts := buildServer(t, posts, users, newPolicy(), nil)
	resp := postGQL(t, ts.URL, `query { posts { edges { node { id status } } totalCount } }`, nil)
	if len(resp.Errors) != 0 {
		t.Fatalf("unexpected errors: %+v", resp.Errors)
	}
	conn, ok := resp.Data["posts"].(map[string]any)
	if !ok {
		t.Fatalf("posts missing: %+v", resp.Data)
	}
	edges := conn["edges"].([]any)
	if len(edges) != 1 {
		t.Fatalf("want 1 visible edge (published only), got %d: %+v", len(edges), edges)
	}
	if node := edges[0].(map[string]any)["node"].(map[string]any); node["id"] != "p1" {
		t.Errorf("expected p1, got %v", node["id"])
	}
}

// TestCreatePostUnauthenticated: createPost from an anonymous request
// returns an UNAUTHORIZED error.
func TestCreatePostUnauthenticated(t *testing.T) {
	t.Parallel()
	ts := buildServer(t, newMemPostRepo(), newMemUserRepo(), newPolicy(), nil)
	q := `mutation { createPost(input: { title: "t", slug: "s", status: DRAFT }) { id } }`
	resp := postGQL(t, ts.URL, q, nil)
	if len(resp.Errors) == 0 {
		t.Fatalf("expected an error, got none")
	}
	if !strings.Contains(strings.ToLower(resp.Errors[0].Message), "unauthor") {
		t.Errorf("expected unauthorized message, got %q", resp.Errors[0].Message)
	}
}

// TestCreatePostForbidden: createPost from a principal without
// edit_posts returns FORBIDDEN.
func TestCreatePostForbidden(t *testing.T) {
	t.Parallel()
	// Subscriber-style principal: has a role, but no caps.
	pol := policy.NewBasicPolicy(map[policy.Role]policy.CapabilitySet{
		"subscriber": policy.NewCapabilitySet(policy.CapRead),
	})
	p := &policy.Principal{UserID: "u1", Roles: []policy.Role{"subscriber"}}
	ts := buildServer(t, newMemPostRepo(), newMemUserRepo(), pol, p)
	q := `mutation { createPost(input: { title: "t", slug: "s", status: DRAFT }) { id } }`
	resp := postGQL(t, ts.URL, q, nil)
	if len(resp.Errors) == 0 {
		t.Fatalf("expected forbidden error")
	}
	if !strings.Contains(strings.ToLower(resp.Errors[0].Message), "forbidden") {
		t.Errorf("expected forbidden message, got %q", resp.Errors[0].Message)
	}
}

// TestCreatePostHappyPath: an author principal can create a draft;
// author is set from the principal regardless of input.
func TestCreatePostHappyPath(t *testing.T) {
	t.Parallel()
	pol := newPolicy()
	users := newMemUserRepo(resolvers.UserRow{ID: "u1", Handle: "alice", Email: "a@x", CreatedAt: fixedTime()})
	posts := newMemPostRepo()
	p := &policy.Principal{UserID: "u1", Roles: []policy.Role{"author"}}
	ts := buildServer(t, posts, users, pol, p)
	q := `mutation { createPost(input: { title: "Hi", slug: "hi", status: DRAFT }) { id title slug status authorID } }`
	resp := postGQL(t, ts.URL, q, nil)
	if len(resp.Errors) != 0 {
		t.Fatalf("unexpected errors: %+v", resp.Errors)
	}
	got := resp.Data["createPost"].(map[string]any)
	if got["title"] != "Hi" || got["slug"] != "hi" || got["status"] != "DRAFT" {
		t.Errorf("create returned wrong fields: %+v", got)
	}
	if got["authorID"] != "u1" {
		t.Errorf("authorID must come from principal, got %v", got["authorID"])
	}
}

// TestDataLoaderBatchesAuthor: when N posts share an author, the
// dataloader collapses N Post.author lookups into a single
// UserRepo.ByIDs call. This is the canonical N+1 test.
func TestDataLoaderBatchesAuthor(t *testing.T) {
	t.Parallel()
	users := newMemUserRepo(
		resolvers.UserRow{ID: "u1", Handle: "alice", Email: "a@x", CreatedAt: fixedTime()},
		resolvers.UserRow{ID: "u2", Handle: "bob", Email: "b@x", CreatedAt: fixedTime()},
	)
	rows := []resolvers.PostRow{}
	for i := 0; i < 10; i++ {
		row := resolvers.PostRow{
			ID:        "p" + itoa(i),
			Title:     "T" + itoa(i),
			Slug:      "t" + itoa(i),
			Status:    "PUBLISHED",
			CreatedAt: fixedTime(),
			UpdatedAt: fixedTime(),
		}
		if i%2 == 0 {
			row.AuthorID = "u1"
		} else {
			row.AuthorID = "u2"
		}
		rows = append(rows, row)
	}
	posts := newMemPostRepo(rows...)
	ts := buildServer(t, posts, users, newPolicy(), nil)

	resp := postGQL(t, ts.URL, `query { posts(first: 20) { edges { node { id author { id handle } } } } }`, nil)
	if len(resp.Errors) != 0 {
		t.Fatalf("unexpected errors: %+v", resp.Errors)
	}
	conn := resp.Data["posts"].(map[string]any)
	edges := conn["edges"].([]any)
	if len(edges) != 10 {
		t.Fatalf("want 10 edges, got %d", len(edges))
	}
	// Author must be populated on every edge.
	for i, e := range edges {
		node := e.(map[string]any)["node"].(map[string]any)
		author, ok := node["author"].(map[string]any)
		if !ok {
			t.Fatalf("edge %d: author missing: %+v", i, node)
		}
		if author["id"] != "u1" && author["id"] != "u2" {
			t.Errorf("edge %d: unexpected author id %v", i, author["id"])
		}
	}
	// Critical assertion: a single batched call to ByIDs, no
	// per-author ByID calls.
	if got := users.byIDsCalls.Load(); got != 1 {
		t.Errorf("ByIDs call count: got %d, want 1 (N+1 not avoided)", got)
	}
	if got := users.byIDCalls.Load(); got != 0 {
		t.Errorf("ByID call count: got %d, want 0 (dataloader bypassed)", got)
	}
}

// TestRepoErrorPropagates: when the repo errors mid-resolve, the
// resolver surfaces a GraphQL error rather than panicking.
func TestRepoErrorPropagates(t *testing.T) {
	t.Parallel()
	users := erroringUserRepo{err: errors.New("boom")}
	posts := newMemPostRepo(resolvers.PostRow{
		ID: "p1", Title: "x", Slug: "x", Status: "PUBLISHED", AuthorID: "u1",
		CreatedAt: fixedTime(), UpdatedAt: fixedTime(),
	})
	r := &resolvers.Resolver{PostRepo: posts, UserRepo: users, Policy: newPolicy()}
	es := generated.NewExecutableSchema(generated.Config{Resolvers: r})
	srv := gqlhandler.New(es)
	srv.AddTransport(transport.POST{})
	c := client.New(srv)
	var out struct {
		Post *struct {
			ID     string
			Author *struct{ ID string }
		}
	}
	err := c.Post(`query { post(id: "p1") { id author { id } } }`, &out)
	if err == nil {
		t.Fatal("expected error, got none")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("expected error to contain repo error, got %q", err.Error())
	}
	_ = time.Now() // keep the time import in use; some tests may not reference it
}

// graphQLResponse mirrors a GraphQL HTTP response envelope.
type graphQLResponse struct {
	Data   map[string]any `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// postGQL POSTs a GraphQL request and unmarshals the response.
func postGQL(t *testing.T, url, query string, variables map[string]any) graphQLResponse {
	t.Helper()
	body := map[string]any{"query": query}
	if variables != nil {
		body["variables"] = variables
	}
	b, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, url, strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	var out graphQLResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}
