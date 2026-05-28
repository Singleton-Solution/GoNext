package pat

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/Singleton-Solution/GoNext/packages/go/audit"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
	"github.com/Singleton-Solution/GoNext/packages/go/testutil/containers"
)

// testPepper is the fixed pepper used across the tests in this file.
// We use a stable value so test failures aren't masked by per-run salt
// drift; the per-call random salt inside argon2 is what actually keeps
// the resulting hashes distinct.
var testPepper = []byte("test-pepper-do-not-use-in-production")

// mustPostgresWithPATSchema spins up a Postgres container, applies
// migrations 000001 through 000026 (the PAT migration), and returns a
// pgxpool plus an auto-cleanup hook. Tests skip cleanly with -short.
//
// We stop at 000026 deliberately: the PAT handlers only need users
// (000002), citext (000001), and the PAT table itself. Bringing in
// later migrations would only slow the container's startup. If a
// future PAT change introduces a join against a newer table, bump the
// upper-bound filter here.
func mustPostgresWithPATSchema(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: skip with -short")
	}
	dsn := containers.Postgres(t)
	if dsn == "" {
		t.Skip("docker not available")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)

	mustApplyPATMigrations(t, dsn)
	return pool
}

// mustApplyPATMigrations applies migrations 000001..000026 against the
// supplied DSN. We open via database/sql + the pgx stdlib so each
// migration runs as a single statement-block — matching the importer
// helper used by every other DB-touching test in apps/api.
func mustApplyPATMigrations(t *testing.T, dsn string) {
	t.Helper()
	root := repoRoot(t)
	dir := filepath.Join(root, "migrations")
	matches, err := filepath.Glob(filepath.Join(dir, "*.up.sql"))
	if err != nil {
		t.Fatalf("glob migrations: %v", err)
	}
	// Filter to 000001..000026. Anything later than that brings in
	// tables we don't touch and only slows the test.
	keep := matches[:0]
	for _, m := range matches {
		base := filepath.Base(m)
		if len(base) < 6 {
			continue
		}
		if base[:6] > "000026" {
			continue
		}
		if base[:6] < "000001" {
			continue
		}
		keep = append(keep, m)
	}
	matches = keep
	if len(matches) == 0 {
		t.Fatalf("no migrations found in %s", dir)
	}
	sort.Strings(matches)
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	for _, m := range matches {
		body, err := os.ReadFile(m)
		if err != nil {
			t.Fatalf("read %s: %v", m, err)
		}
		if _, err := db.ExecContext(ctx, string(body)); err != nil {
			t.Fatalf("apply %s: %v", filepath.Base(m), err)
		}
	}
}

// repoRoot walks up until it finds the directory containing go.work.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(file)
	for i := 0; i < 12; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not locate repo root (go.work)")
	return ""
}

// seedUser inserts a users row and returns its UUID. The PAT user_id
// column has a FK to users.id, so every test that exercises Create
// needs at least one real user.
func seedUser(t *testing.T, pool *pgxpool.Pool, email, handle string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(),
		`INSERT INTO users (email, handle, display_name)
		 VALUES ($1::citext, $2::citext, $3) RETURNING id`,
		email, handle, handle,
	).Scan(&id)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return id
}

// withPrincipal wraps a handler with a middleware that injects a fixed
// Principal. Production wires session/PAT auth here; tests just want
// a Principal on the context so the gate passes.
func withPrincipal(p policy.Principal, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(policy.WithPrincipal(r.Context(), p))
		h.ServeHTTP(w, r)
	})
}

// newServer wires Mount + a principal-injecting middleware. Returns
// the server URL, the audit store (so tests can assert on emitted
// events), and a cleanup hook.
func newServer(t *testing.T, pool *pgxpool.Pool, userID string) (string, *audit.MemoryStore, func()) {
	t.Helper()
	auditStore := audit.NewMemoryStore()
	emitter := audit.NewEmitter(auditStore)
	mux := http.NewServeMux()
	err := Mount(mux, "/api/v1/me/tokens", Deps{
		Pool:         pool,
		Pepper:       testPepper,
		AuditEmitter: emitter,
	})
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}
	pr := policy.Principal{UserID: userID}
	srv := httptest.NewServer(withPrincipal(pr, mux))
	return srv.URL, auditStore, srv.Close
}

func decodeJSON(t *testing.T, r *http.Response, v any) {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if err := json.Unmarshal(body, v); err != nil {
		t.Fatalf("decode: %v\nbody: %s", err, body)
	}
}

// TestList_NoTokens_EmptyData covers the empty-state path the admin
// settings page sees on a fresh account: a 200 with {"data":[]}, never
// a 500 from a nil slice or a 404 from the mount.
func TestList_NoTokens_EmptyData(t *testing.T) {
	pool := mustPostgresWithPATSchema(t)
	user := seedUser(t, pool, "alice@example.com", "alice")
	url, _, cleanup := newServer(t, pool, user.String())
	defer cleanup()

	res, err := http.Get(url + "/api/v1/me/tokens")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(res.Body)
		t.Fatalf("status %d body=%s", res.StatusCode, raw)
	}
	var out struct {
		Data []TokenView `json:"data"`
	}
	decodeJSON(t, res, &out)
	if len(out.Data) != 0 {
		t.Fatalf("expected empty data, got %v", out.Data)
	}
}

// TestCreate_ReturnsRawTokenOnce is the most important test in this
// file: the create response carries the plaintext, but subsequent
// GETs only show the prefix. A regression here means a leaked DB dump
// becomes a leaked credential.
func TestCreate_ReturnsRawTokenOnce(t *testing.T) {
	pool := mustPostgresWithPATSchema(t)
	user := seedUser(t, pool, "bob@example.com", "bob")
	url, _, cleanup := newServer(t, pool, user.String())
	defer cleanup()

	body, _ := json.Marshal(CreateRequest{
		Name:      "ci-token",
		Scopes:    []string{"read"},
		ExpiresIn: "never",
	})
	res, err := http.Post(url+"/api/v1/me/tokens", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(res.Body)
		t.Fatalf("status %d body=%s", res.StatusCode, raw)
	}
	var issued IssuedTokenView
	decodeJSON(t, res, &issued)
	if !strings.HasPrefix(issued.Token, Namespace) {
		t.Fatalf("plaintext missing namespace: %q", issued.Token)
	}
	if len(issued.Token) != MinTokenLen {
		t.Fatalf("plaintext wrong length: %d want %d (%q)", len(issued.Token), MinTokenLen, issued.Token)
	}
	if !issued.SaveNow {
		t.Fatal("save_now must be true on the create response")
	}
	if issued.Prefix == "" || len(issued.Prefix) != PrefixLen {
		t.Fatalf("prefix wrong shape: %q", issued.Prefix)
	}
	// The prefix on the response must match the first PrefixLen chars
	// of the secret tail — that's what the operator will use to
	// recognise the token in the list later.
	wantPrefix := issued.Token[len(Namespace) : len(Namespace)+PrefixLen]
	if issued.Prefix != wantPrefix {
		t.Fatalf("prefix %q does not match token tail %q", issued.Prefix, wantPrefix)
	}

	// A subsequent list MUST NOT include the plaintext. The list
	// response shape is {"data":[TokenView]} and TokenView has no
	// Token field; we double-check by string-searching the raw body.
	listRes, err := http.Get(url + "/api/v1/me/tokens")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer listRes.Body.Close()
	raw, _ := io.ReadAll(listRes.Body)
	if strings.Contains(string(raw), issued.Token) {
		t.Fatalf("list response leaked plaintext: %s", raw)
	}
	low := strings.ToLower(string(raw))
	if strings.Contains(low, "\"token\":") {
		t.Fatalf("list response leaked token field: %s", raw)
	}
	if strings.Contains(low, "\"hash\"") {
		t.Fatalf("list response leaked hash field: %s", raw)
	}
	// The row IS visible — just without the plaintext.
	var listOut struct {
		Data []TokenView `json:"data"`
	}
	if err := json.Unmarshal(raw, &listOut); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listOut.Data) != 1 {
		t.Fatalf("expected 1 token in list, got %d", len(listOut.Data))
	}
	if listOut.Data[0].Prefix != issued.Prefix {
		t.Fatalf("list row prefix mismatch: got %q want %q", listOut.Data[0].Prefix, issued.Prefix)
	}
}

// TestRevoke_RemovesFromList covers the happy revoke path: DELETE
// returns 204, the list call afterwards omits the row. The store
// soft-deletes (sets revoked_at) but the active-only filter on List
// hides the row.
func TestRevoke_RemovesFromList(t *testing.T) {
	pool := mustPostgresWithPATSchema(t)
	user := seedUser(t, pool, "carol@example.com", "carol")
	url, _, cleanup := newServer(t, pool, user.String())
	defer cleanup()

	// Create the token via the HTTP path so we have the canonical id.
	body, _ := json.Marshal(CreateRequest{Name: "to-revoke", Scopes: []string{"read"}})
	res, err := http.Post(url+"/api/v1/me/tokens", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	var issued IssuedTokenView
	decodeJSON(t, res, &issued)
	res.Body.Close()

	req, _ := http.NewRequest("DELETE", url+"/api/v1/me/tokens/"+issued.ID, nil)
	delRes, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer delRes.Body.Close()
	if delRes.StatusCode != http.StatusNoContent {
		raw, _ := io.ReadAll(delRes.Body)
		t.Fatalf("status %d want 204 body=%s", delRes.StatusCode, raw)
	}

	listRes, err := http.Get(url + "/api/v1/me/tokens")
	if err != nil {
		t.Fatalf("GET after revoke: %v", err)
	}
	defer listRes.Body.Close()
	var listOut struct {
		Data []TokenView `json:"data"`
	}
	decodeJSON(t, listRes, &listOut)
	if len(listOut.Data) != 0 {
		t.Fatalf("expected empty list after revoke, got %d rows", len(listOut.Data))
	}
}

// TestRevoke_OtherUsersToken_Returns404 is the existence-oracle guard.
// A caller MUST NOT be able to learn whether an id belongs to another
// user via 403-vs-404; the store collapses both to ErrNotFound.
func TestRevoke_OtherUsersToken_Returns404(t *testing.T) {
	pool := mustPostgresWithPATSchema(t)
	alice := seedUser(t, pool, "alice2@example.com", "alice2")
	bobID := seedUser(t, pool, "bob2@example.com", "bob2")

	// Alice issues a token (we manipulate the store directly to avoid
	// having to spin up a second server for her).
	store := NewStore(pool, testPepper)
	created, err := store.Create(context.Background(), CreateInput{
		UserID: alice.String(),
		Name:   "alices-token",
		Scopes: []string{"read"},
	})
	if err != nil {
		t.Fatalf("seed Alice's token: %v", err)
	}

	// Bob (the principal) tries to revoke Alice's token.
	url, _, cleanup := newServer(t, pool, bobID.String())
	defer cleanup()
	req, _ := http.NewRequest("DELETE", url+"/api/v1/me/tokens/"+created.Token.ID, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		raw, _ := io.ReadAll(res.Body)
		t.Fatalf("status %d want 404 body=%s", res.StatusCode, raw)
	}

	// Alice's token must still be active — Bob's failed attempt should
	// not have flipped a flag.
	rows, err := store.List(context.Background(), alice.String())
	if err != nil {
		t.Fatalf("List Alice: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected Alice's token to survive; got %d rows", len(rows))
	}
}

// TestAuthRequired_AllEndpoints — no Principal on the context means
// 401 for every endpoint. The session middleware would normally make
// this unreachable, but we want a defense-in-depth gate at the
// handler too.
func TestAuthRequired_AllEndpoints(t *testing.T) {
	pool := mustPostgresWithPATSchema(t)

	mux := http.NewServeMux()
	if err := Mount(mux, "/api/v1/me/tokens", Deps{
		Pool:   pool,
		Pepper: testPepper,
	}); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	// No principal-injecting middleware on this server — every call
	// must hit the gate's 401 branch.
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cases := []struct {
		method string
		path   string
	}{
		{"GET", "/api/v1/me/tokens"},
		{"POST", "/api/v1/me/tokens"},
		{"DELETE", "/api/v1/me/tokens/00000000-0000-7000-8000-000000000000"},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req, _ := http.NewRequest(tc.method, srv.URL+tc.path, bytes.NewReader([]byte("{}")))
			req.Header.Set("Content-Type", "application/json")
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("do: %v", err)
			}
			defer res.Body.Close()
			if res.StatusCode != http.StatusUnauthorized {
				raw, _ := io.ReadAll(res.Body)
				t.Fatalf("status %d want 401 body=%s", res.StatusCode, raw)
			}
		})
	}
}

// TestCreate_RejectsEmptyName covers the input-validation path: an
// empty name is a 400 before we burn an argon2 hash.
func TestCreate_RejectsEmptyName(t *testing.T) {
	pool := mustPostgresWithPATSchema(t)
	user := seedUser(t, pool, "dave@example.com", "dave")
	url, _, cleanup := newServer(t, pool, user.String())
	defer cleanup()

	body, _ := json.Marshal(CreateRequest{Name: "   ", Scopes: []string{"read"}})
	res, err := http.Post(url+"/api/v1/me/tokens", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		raw, _ := io.ReadAll(res.Body)
		t.Fatalf("status %d want 400 body=%s", res.StatusCode, raw)
	}
}

// TestCreate_EmitsAuditEvent — the audit emitter receives an
// auth.pat.created event with the issued token id as the target.
func TestCreate_EmitsAuditEvent(t *testing.T) {
	pool := mustPostgresWithPATSchema(t)
	user := seedUser(t, pool, "eve@example.com", "eve")
	url, auditStore, cleanup := newServer(t, pool, user.String())
	defer cleanup()

	body, _ := json.Marshal(CreateRequest{Name: "audit-test", Scopes: []string{"read"}})
	res, err := http.Post(url+"/api/v1/me/tokens", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer res.Body.Close()
	var issued IssuedTokenView
	decodeJSON(t, res, &issued)

	events, err := auditStore.List(context.Background(), audit.Filter{})
	if err != nil {
		t.Fatalf("audit List: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("expected at least one audit event, got none")
	}
	found := false
	for _, e := range events {
		if e.EventType == EventTokenCreated && e.ResourceID == issued.ID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected %s event for token %s; got %d events", EventTokenCreated, issued.ID, len(events))
	}
}
