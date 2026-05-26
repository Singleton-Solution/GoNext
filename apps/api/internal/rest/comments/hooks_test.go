package comments

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
)

// stubBus is a HookBus stub that lets each test supply a per-call
// handler. The real packages/go/hooks.Bus is exercised in its own
// package tests; here we only need to drive the comments handler's
// behaviour.
type stubBus struct {
	handler func(ctx context.Context, value any) (any, error)
}

func (b *stubBus) ApplyFilters(ctx context.Context, name string, value any, args ...any) (any, error) {
	if b.handler == nil {
		return value, nil
	}
	return b.handler(ctx, value)
}

func newStore(t *testing.T) *MemoryStore {
	t.Helper()
	s := NewMemoryStore()
	s.SeedPost("post-1")
	return s
}

func makeMux(t *testing.T, deps Deps) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	if err := Mount(mux, "/api/v1/posts", deps); err != nil {
		t.Fatalf("mount: %v", err)
	}
	return mux
}

func TestPreSubmit_HookRejects(t *testing.T) {
	t.Parallel()
	store := newStore(t)
	bus := &stubBus{handler: func(ctx context.Context, v any) (any, error) {
		return v, ErrCommentRejected
	}}
	mux := makeMux(t, Deps{Store: store, Hooks: bus})

	body := strings.NewReader(`{"author_name":"alice","content":"hi"}`)
	req := httptest.NewRequest("POST", "/api/v1/posts/post-1/comments", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422; body=%s", rr.Code, rr.Body.String())
	}
}

func TestPreSubmit_HookStampsVerdict(t *testing.T) {
	t.Parallel()
	store := newStore(t)
	bus := &stubBus{handler: func(ctx context.Context, v any) (any, error) {
		p, ok := v.(*PreSubmitPayload)
		if !ok {
			t.Errorf("payload type = %T, want *PreSubmitPayload", v)
			return v, nil
		}
		// Auto-approve everything from this stub plugin.
		p.Verdict = CommentVerdict{Status: StatusApproved, Reason: "trusted source"}
		// Return short-circuit so the chain stops here.
		return p, errors.New("hooks: short-circuit filter chain")
	}}
	mux := makeMux(t, Deps{Store: store, Hooks: bus})

	body := bytes.NewBufferString(`{"author_name":"alice","content":"this is a comment"}`)
	req := httptest.NewRequest("POST", "/api/v1/posts/post-1/comments", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	var out Created
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if out.Pending {
		t.Errorf("Pending = true, want false (verdict should auto-approve)")
	}
}

func TestDuplicateContent(t *testing.T) {
	t.Parallel()
	store := newStore(t)
	mux := makeMux(t, Deps{Store: store, DupChecker: store})

	for i, want := range []int{http.StatusCreated, http.StatusUnprocessableEntity} {
		body := bytes.NewBufferString(`{"author_name":"alice","content":"buy cheap pills now"}`)
		req := httptest.NewRequest("POST", "/api/v1/posts/post-1/comments", body)
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "192.0.2.1:55001"
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != want {
			t.Errorf("attempt %d: status = %d, want %d; body=%s", i, rr.Code, want, rr.Body.String())
		}
	}
}

func TestRedactIP_V4(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"192.0.2.42":    "192.0.2.0",
		"203.0.113.255": "203.0.113.0",
		"127.0.0.1":     "127.0.0.0",
	}
	for in, want := range cases {
		if got := redactIP(in); got != want {
			t.Errorf("redactIP(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRedactIP_V6(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"2001:db8::1":          "2001:db8::",
		"fe80::1234:5678:9abc": "fe80::",
	}
	for in, want := range cases {
		if got := redactIP(in); got != want {
			t.Errorf("redactIP(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRunRedactionCron(t *testing.T) {
	t.Parallel()
	clock := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStoreWithClock(func() time.Time { return clock })
	store.SeedPost("post-1")
	// Recent row — should NOT be redacted.
	store.Seed(Comment{ID: "c1", PostID: "post-1", Content: "fresh", CreatedAt: clock.Add(-24 * time.Hour)}, StatusApproved)
	// Old row — should be redacted.
	store.Seed(Comment{ID: "c2", PostID: "post-1", Content: "stale", CreatedAt: clock.Add(-40 * 24 * time.Hour)}, StatusApproved)
	// Attach IPs by walking the rows directly.
	row := store.rows["c1"]
	row.AuthorIP = "192.0.2.10"
	store.rows["c1"] = row
	row = store.rows["c2"]
	row.AuthorIP = "192.0.2.20"
	store.rows["c2"] = row

	n, err := RunRedactionCron(context.Background(), store, DefaultRedactionAge, func() time.Time { return clock })
	if err != nil {
		t.Fatalf("cron: %v", err)
	}
	if n != 1 {
		t.Errorf("redacted = %d, want 1", n)
	}
	if got := store.rows["c1"].AuthorIP; got != "192.0.2.10" {
		t.Errorf("c1.ip = %q, want untouched", got)
	}
	if got := store.rows["c2"].AuthorIP; got != "192.0.2.0" {
		t.Errorf("c2.ip = %q, want 192.0.2.0", got)
	}
}

func TestContentFingerprint_Normalised(t *testing.T) {
	t.Parallel()
	a := contentFingerprint("Hello World!")
	b := contentFingerprint("  hello   world!  ")
	if a != b {
		t.Errorf("expected match after whitespace + case normalisation")
	}
}
