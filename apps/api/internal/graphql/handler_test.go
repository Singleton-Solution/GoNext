package graphql_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gql "github.com/Singleton-Solution/GoNext/apps/api/internal/graphql"
	"github.com/Singleton-Solution/GoNext/apps/api/internal/graphql/cost"
	"github.com/Singleton-Solution/GoNext/apps/api/internal/graphql/resolvers"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// fakePostRepo is a minimal PostRepo for handler-level tests.
type fakePostRepo struct{}

func (fakePostRepo) ByID(context.Context, string) (*resolvers.PostRow, error) {
	return nil, nil
}
func (fakePostRepo) List(context.Context, resolvers.PostFilter, int, string) (*resolvers.PostPage, error) {
	return &resolvers.PostPage{}, nil
}
func (fakePostRepo) Create(context.Context, resolvers.PostRow) (*resolvers.PostRow, error) {
	return nil, errors.New("unused")
}

// fakeUserRepo is a minimal UserRepo for handler-level tests.
type fakeUserRepo struct{}

func (fakeUserRepo) ByID(_ context.Context, id string) (*resolvers.UserRow, error) {
	if id == "u1" {
		return &resolvers.UserRow{ID: "u1", Handle: "alice", Email: "a@x", CreatedAt: time.Now()}, nil
	}
	return nil, nil
}
func (fakeUserRepo) ByIDs(_ context.Context, ids []string) ([]*resolvers.UserRow, error) {
	out := make([]*resolvers.UserRow, len(ids))
	for i, id := range ids {
		if id == "u1" {
			out[i] = &resolvers.UserRow{ID: "u1", Handle: "alice", Email: "a@x", CreatedAt: time.Now()}
		}
	}
	return out, nil
}

func newHandlerServer(t *testing.T, principal *policy.Principal) *httptest.Server {
	t.Helper()
	deps := gql.Deps{
		PostRepo:            fakePostRepo{},
		UserRepo:            fakeUserRepo{},
		Policy:              policy.NewBasicPolicy(map[policy.Role]policy.CapabilitySet{}),
		Cost:                cost.Config{},
		EnableIntrospection: true,
	}
	h := gql.Handler(deps)
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

// TestHandlerIntrospection: introspection queries work when
// EnableIntrospection=true. This is the smoke test that gqlgen
// parsed our schema correctly.
func TestHandlerIntrospection(t *testing.T) {
	t.Parallel()
	ts := newHandlerServer(t, nil)
	resp := doPost(t, ts.URL, `{"query": "{ __schema { types { name } } }"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var out struct {
		Data struct {
			Schema struct {
				Types []struct{ Name string } `json:"types"`
			} `json:"__schema"`
		}
		Errors []any
	}
	mustDecode(t, resp.Body, &out)
	if len(out.Errors) != 0 {
		t.Fatalf("errors: %+v", out.Errors)
	}
	// Spot-check a couple of our types.
	names := make(map[string]bool, len(out.Data.Schema.Types))
	for _, ty := range out.Data.Schema.Types {
		names[ty.Name] = true
	}
	for _, want := range []string{"Post", "User", "PostConnection", "PostEdge", "PageInfo"} {
		if !names[want] {
			t.Errorf("missing type %q in schema", want)
		}
	}
}

// TestHandlerRejectsExpensiveQuery: a pathological query exceeds the
// anonymous budget and is rejected at the operation-middleware layer
// (before any resolver runs). We use aliases to repeat the
// posts(first: N) connection many times — each alias scores
// independently in the cost analyzer, so even with a flat schema
// the budget is easy to blow with enough aliases.
func TestHandlerRejectsExpensiveQuery(t *testing.T) {
	t.Parallel()
	ts := newHandlerServer(t, nil)
	// 20 aliased posts(first: 50) queries, each requesting author.id.
	// Per posts(first:50): 1 (composite) + 5 (depth) + 50 * (edges
	// child cost). Easy >1000 with 20 of them stacked.
	var sb strings.Builder
	sb.WriteString("query {")
	for i := 0; i < 20; i++ {
		// Alias names must start with a letter; "p0" ... "p19".
		sb.WriteString(" p")
		sb.WriteString(itoa(i))
		sb.WriteString(`: posts(first: 50) { edges { node { id author { id handle } } } }`)
	}
	sb.WriteString(" }")
	body := map[string]any{"query": sb.String()}
	b, _ := json.Marshal(body)
	resp := doPost(t, ts.URL, string(b))
	var out struct {
		Data   any
		Errors []struct {
			Message string `json:"message"`
		}
	}
	mustDecode(t, resp.Body, &out)
	if len(out.Errors) == 0 {
		t.Fatalf("expected cost error, got none. data=%v", out.Data)
	}
	if !strings.Contains(out.Errors[0].Message, "cost") {
		t.Errorf("expected cost error message, got %q (full=%+v)", out.Errors[0].Message, out.Errors)
	}
}

// itoa is the local copy used by the alias-builder test.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// TestHandlerCheaperBudgetForAuthenticated: a query that is rejected
// for an anonymous principal succeeds for an authenticated one with
// the higher budget. We use the same alias trick as the rejection
// test but with fewer aliases so the cost lands between the two
// budgets (1000 < cost < 10000).
func TestHandlerCheaperBudgetForAuthenticated(t *testing.T) {
	t.Parallel()
	// Build a query with enough aliases to exceed 1000 but stay
	// well under 10000. Each aliased posts(first:50){...} costs
	// roughly 50 * (4 or 5) ~ 250 — five aliases ~ 1250, which
	// exceeds the anon budget but easily fits in 10000.
	var sb strings.Builder
	sb.WriteString("query {")
	for i := 0; i < 5; i++ {
		sb.WriteString(" p")
		sb.WriteString(itoa(i))
		sb.WriteString(`: posts(first: 50) { edges { node { id author { id } } } }`)
	}
	sb.WriteString(" }")
	q := sb.String()

	// Anon: should be rejected.
	tsAnon := newHandlerServer(t, nil)
	respA := doPost(t, tsAnon.URL, jsonQuery(q))
	var outA struct {
		Errors []struct{ Message string }
	}
	mustDecode(t, respA.Body, &outA)
	rejected := false
	for _, e := range outA.Errors {
		if strings.Contains(e.Message, "cost") {
			rejected = true
			break
		}
	}
	if !rejected {
		t.Fatalf("anon: expected cost rejection, got none; errors=%+v", outA.Errors)
	}

	// Auth: should pass the cost gate (resolver may still error,
	// but NOT a cost error).
	p := &policy.Principal{UserID: "u1", Roles: []policy.Role{"author"}}
	tsAuth := newHandlerServer(t, p)
	respB := doPost(t, tsAuth.URL, jsonQuery(q))
	var outB struct {
		Errors []struct{ Message string }
	}
	mustDecode(t, respB.Body, &outB)
	for _, e := range outB.Errors {
		if strings.Contains(e.Message, "cost") {
			t.Errorf("auth: cost rejected what should fit: %q", e.Message)
		}
	}
}

func jsonQuery(q string) string {
	b, _ := json.Marshal(map[string]any{"query": q})
	return string(b)
}

func doPost(t *testing.T, url, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func mustDecode(t *testing.T, body interface{ Read(p []byte) (int, error) }, out any) {
	t.Helper()
	if err := json.NewDecoder(body).Decode(out); err != nil {
		t.Fatalf("decode: %v", err)
	}
}
