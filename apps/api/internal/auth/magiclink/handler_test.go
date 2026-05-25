package magiclink

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"encoding/json"

	"github.com/Singleton-Solution/GoNext/packages/go/email"
)

// memTokenStore is the in-memory [TokenStore] used in handler tests.
type memTokenStore struct {
	mu        sync.Mutex
	rows      map[string]memTokenRow
	saveCt    int
	consumeCt int
}

type memTokenRow struct {
	userID    string
	expiresAt time.Time
	usedAt    *time.Time
}

func newMemTokenStore() *memTokenStore {
	return &memTokenStore{rows: map[string]memTokenRow{}}
}

func (s *memTokenStore) Save(_ context.Context, h, u string, exp time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saveCt++
	s.rows[h] = memTokenRow{userID: u, expiresAt: exp}
	return nil
}

func (s *memTokenStore) Consume(_ context.Context, h string, now time.Time) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.consumeCt++
	row, ok := s.rows[h]
	if !ok {
		return "", ErrTokenNotFound
	}
	if row.usedAt != nil {
		return "", ErrTokenNotFound
	}
	if !now.Before(row.expiresAt) {
		return "", ErrTokenNotFound
	}
	now2 := now
	row.usedAt = &now2
	s.rows[h] = row
	return row.userID, nil
}

// memUserStore is the in-memory [UserStore]. Emails are lowercased on
// insert+lookup to mirror the production citext behaviour.
type memUserStore struct {
	mu      sync.Mutex
	byEmail map[string]string // lowercase email -> userID
}

func newMemUserStore(email, uid string) *memUserStore {
	return &memUserStore{byEmail: map[string]string{strings.ToLower(email): uid}}
}

func (u *memUserStore) LookupIDByEmail(_ context.Context, e string) (string, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	id, ok := u.byEmail[strings.ToLower(strings.TrimSpace(e))]
	if !ok {
		return "", ErrUserNotFound
	}
	return id, nil
}

// memSessionCreator is the in-memory [SessionCreator]. Each Create call
// returns a deterministic stub token so tests can assert on the cookie
// value.
type memSessionCreator struct {
	mu       sync.Mutex
	creates  []memCreate
	failWith error
}

type memCreate struct {
	userID  string
	ttl     time.Duration
	idleTTL time.Duration
	data    map[string]any
}

func (s *memSessionCreator) Create(_ context.Context, userID string, data map[string]any, ttl, idleTTL time.Duration) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failWith != nil {
		return "", s.failWith
	}
	s.creates = append(s.creates, memCreate{
		userID:  userID,
		ttl:     ttl,
		idleTTL: idleTTL,
		data:    data,
	})
	return "session-" + userID, nil
}

// memLimiter / noopLimiter mirror the passwordreset test doubles.
type memLimiter struct {
	mu        sync.Mutex
	allowance int
	count     int
}

func newMemLimiter(allow int) *memLimiter { return &memLimiter{allowance: allow} }

func (l *memLimiter) Allow(_ context.Context, _ string) (bool, time.Duration, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.count++
	if l.count > l.allowance {
		return false, 30 * time.Second, nil
	}
	return true, 0, nil
}

type noopLimiter struct{}

func (noopLimiter) Allow(_ context.Context, _ string) (bool, time.Duration, error) {
	return true, 0, nil
}

func mustHandler(t *testing.T, opts Options) *Handler {
	t.Helper()
	if opts.Limiter == nil {
		opts.Limiter = noopLimiter{}
	}
	if opts.Sender == nil {
		opts.Sender = email.NewNoopSender()
	}
	if opts.LinkURL == "" {
		opts.LinkURL = "https://example.test/api/v1/auth/magic-link"
	}
	if opts.SessionAbsoluteTTL == 0 {
		opts.SessionAbsoluteTTL = 24 * time.Hour
	}
	if opts.SessionIdleTTL == 0 {
		opts.SessionIdleTTL = 1 * time.Hour
	}
	h, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return h
}

func postJSON(h *Handler, body any) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	h.Routes(mux)
	rec := httptest.NewRecorder()
	enc, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/magic-link/request", strings.NewReader(string(enc)))
	req.RemoteAddr = "203.0.113.5:51234"
	mux.ServeHTTP(rec, req)
	return rec
}

func getRedeem(h *Handler, token string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	h.Routes(mux)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/magic-link?token="+token, nil)
	req.RemoteAddr = "203.0.113.5:51234"
	mux.ServeHTTP(rec, req)
	return rec
}

// =====================================================================
// Request endpoint
// =====================================================================

func TestRequest_KnownEmail_IssuesAndEmails(t *testing.T) {
	users := newMemUserStore("alice@example.com", "user-1")
	tokens := newMemTokenStore()
	sender := email.NewNoopSender()
	h := mustHandler(t, Options{
		Tokens:   tokens,
		Users:    users,
		Sessions: &memSessionCreator{},
		Sender:   sender,
	})

	rec := postJSON(h, map[string]any{"email": "alice@example.com"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	if tokens.saveCt != 1 {
		t.Errorf("saveCt: got %d, want 1", tokens.saveCt)
	}
	if sender.Count() != 1 {
		t.Errorf("email send count: got %d, want 1", sender.Count())
	}
	msg, _ := sender.Last()
	if !strings.Contains(msg.TextBody, "?token=") {
		t.Errorf("email body missing token link: %q", msg.TextBody)
	}
}

func TestRequest_UnknownEmail_Returns200_NoIssue(t *testing.T) {
	users := newMemUserStore("alice@example.com", "user-1")
	tokens := newMemTokenStore()
	sender := email.NewNoopSender()
	h := mustHandler(t, Options{
		Tokens:   tokens,
		Users:    users,
		Sessions: &memSessionCreator{},
		Sender:   sender,
	})

	rec := postJSON(h, map[string]any{"email": "ghost@example.com"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (enumeration-safe)", rec.Code)
	}
	if tokens.saveCt != 0 {
		t.Errorf("saveCt: got %d, want 0", tokens.saveCt)
	}
	if sender.Count() != 0 {
		t.Errorf("email send count: got %d, want 0", sender.Count())
	}
}

func TestRequest_EmptyEmail_Returns200(t *testing.T) {
	users := newMemUserStore("alice@example.com", "user-1")
	tokens := newMemTokenStore()
	h := mustHandler(t, Options{
		Tokens:   tokens,
		Users:    users,
		Sessions: &memSessionCreator{},
	})

	rec := postJSON(h, map[string]any{"email": ""})
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	if tokens.saveCt != 0 {
		t.Errorf("saveCt: got %d, want 0", tokens.saveCt)
	}
}

func TestRequest_MalformedJSON_Returns400(t *testing.T) {
	h := mustHandler(t, Options{
		Tokens:   newMemTokenStore(),
		Users:    newMemUserStore("alice@example.com", "u1"),
		Sessions: &memSessionCreator{},
	})

	mux := http.NewServeMux()
	h.Routes(mux)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/magic-link/request", strings.NewReader("{not-json"))
	req.RemoteAddr = "203.0.113.5:1234"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", rec.Code)
	}
}

// =====================================================================
// Verify endpoint
// =====================================================================

func TestVerify_HappyPath_MintsSessionRedirects(t *testing.T) {
	users := newMemUserStore("alice@example.com", "user-1")
	tokens := newMemTokenStore()
	creator := &memSessionCreator{}
	h := mustHandler(t, Options{
		Tokens:          tokens,
		Users:           users,
		Sessions:        creator,
		SuccessRedirect: "/admin",
		Now:             func() time.Time { return time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC) },
	})

	// Issue a token via the request endpoint.
	postJSON(h, map[string]any{"email": "alice@example.com"})

	sender := h.opts.Sender.(*email.NoopSender)
	msg, _ := sender.Last()
	token := extractToken(t, msg.TextBody)

	rec := getRedeem(h, token)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status: got %d, want 303 See Other; body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/admin" {
		t.Errorf("Location: got %q, want /admin", got)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatalf("no Set-Cookie on redirect")
	}
	if !strings.HasPrefix(cookies[0].Value, "session-") {
		t.Errorf("cookie value: got %q, want stub session token", cookies[0].Value)
	}
	if len(creator.creates) != 1 {
		t.Fatalf("session creates: got %d, want 1", len(creator.creates))
	}
	if creator.creates[0].userID != "user-1" {
		t.Errorf("session user_id: got %q, want user-1", creator.creates[0].userID)
	}
}

func TestVerify_ReplayedToken_Returns410(t *testing.T) {
	users := newMemUserStore("alice@example.com", "user-1")
	tokens := newMemTokenStore()
	creator := &memSessionCreator{}
	h := mustHandler(t, Options{
		Tokens:   tokens,
		Users:    users,
		Sessions: creator,
		Now:      func() time.Time { return time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC) },
	})

	postJSON(h, map[string]any{"email": "alice@example.com"})
	sender := h.opts.Sender.(*email.NoopSender)
	msg, _ := sender.Last()
	token := extractToken(t, msg.TextBody)

	// First redeem succeeds.
	rec := getRedeem(h, token)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("first redeem: %d", rec.Code)
	}
	// Replay fails.
	rec2 := getRedeem(h, token)
	if rec2.Code != http.StatusGone {
		t.Fatalf("replay: got %d, want 410", rec2.Code)
	}
}

func TestVerify_ExpiredToken_Returns410(t *testing.T) {
	users := newMemUserStore("alice@example.com", "user-1")
	tokens := newMemTokenStore()
	clock := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	h := mustHandler(t, Options{
		Tokens:   tokens,
		Users:    users,
		Sessions: &memSessionCreator{},
		Now:      func() time.Time { return clock },
	})

	postJSON(h, map[string]any{"email": "alice@example.com"})
	sender := h.opts.Sender.(*email.NoopSender)
	msg, _ := sender.Last()
	token := extractToken(t, msg.TextBody)

	// Advance past the 15-minute TTL.
	clock = clock.Add(30 * time.Minute)

	rec := getRedeem(h, token)
	if rec.Code != http.StatusGone {
		t.Fatalf("expired: got %d, want 410", rec.Code)
	}
}

func TestVerify_MalformedToken_Returns410(t *testing.T) {
	h := mustHandler(t, Options{
		Tokens:   newMemTokenStore(),
		Users:    newMemUserStore("alice@example.com", "u1"),
		Sessions: &memSessionCreator{},
	})
	rec := getRedeem(h, "not-a-hex-token")
	if rec.Code != http.StatusGone {
		t.Fatalf("status: got %d, want 410", rec.Code)
	}
}

func TestVerify_UnknownToken_Returns410(t *testing.T) {
	h := mustHandler(t, Options{
		Tokens:   newMemTokenStore(),
		Users:    newMemUserStore("alice@example.com", "u1"),
		Sessions: &memSessionCreator{},
	})
	rec := getRedeem(h, strings.Repeat("a", 64))
	if rec.Code != http.StatusGone {
		t.Fatalf("status: got %d, want 410", rec.Code)
	}
}

// =====================================================================
// Rate limit
// =====================================================================

func TestRequest_RateLimit_Returns429(t *testing.T) {
	users := newMemUserStore("alice@example.com", "user-1")
	tokens := newMemTokenStore()
	limiter := newMemLimiter(2)
	h := mustHandler(t, Options{
		Tokens:   tokens,
		Users:    users,
		Sessions: &memSessionCreator{},
		Limiter:  limiter,
	})

	for i := 0; i < 2; i++ {
		rec := postJSON(h, map[string]any{"email": "alice@example.com"})
		if rec.Code != http.StatusOK {
			t.Fatalf("attempt %d: got %d, want 200", i, rec.Code)
		}
	}
	rec := postJSON(h, map[string]any{"email": "alice@example.com"})
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("third attempt: got %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Errorf("Retry-After header missing")
	}
}

// =====================================================================
// Helpers
// =====================================================================

func extractToken(t *testing.T, body string) string {
	t.Helper()
	const marker = "?token="
	idx := strings.Index(body, marker)
	if idx < 0 {
		t.Fatalf("no token marker in body: %q", body)
	}
	rest := body[idx+len(marker):]
	end := len(rest)
	for i, r := range rest {
		if r == ' ' || r == '\n' || r == '\r' || r == '<' || r == '"' || r == '>' {
			end = i
			break
		}
	}
	return rest[:end]
}
