package passwordreset

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/email"
)

// memTokenStore is the in-memory [TokenStore] used in handler tests.
// Stored rows expire by comparing now() to expiresAt rather than by a
// background sweep so the test clock is the single source of truth.
type memTokenStore struct {
	mu       sync.Mutex
	rows     map[string]memTokenRow
	saveCt   int
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
	mu        sync.Mutex
	byEmail   map[string]string // lowercase email -> userID
	passwords map[string]string // userID -> hash
}

func newMemUserStore(email, uid string) *memUserStore {
	return &memUserStore{
		byEmail:   map[string]string{strings.ToLower(email): uid},
		passwords: map[string]string{},
	}
}

func (u *memUserStore) LookupIDByEmail(_ context.Context, email string) (string, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	id, ok := u.byEmail[strings.ToLower(strings.TrimSpace(email))]
	if !ok {
		return "", ErrUserNotFound
	}
	return id, nil
}

func (u *memUserStore) UpdatePassword(_ context.Context, userID, h string) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.passwords[userID] = h
	return nil
}

// memSessionRevoker is the in-memory [SessionRevoker].
type memSessionRevoker struct {
	mu       sync.Mutex
	revoked  []string
	failWith error
}

func (s *memSessionRevoker) DeleteAllForUser(_ context.Context, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failWith != nil {
		return s.failWith
	}
	s.revoked = append(s.revoked, userID)
	return nil
}

// memLimiter is the cooperative in-memory rate-limit double. It hands
// out tokens until exhausted, then returns allowed=false with a fixed
// retryAfter. The handler-level test that drives the limiter to its
// floor uses count to verify the limit ratchets per attempt.
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

// noopLimiter trivially allows all requests. Used by the path tests
// that don't care about the rate limit.
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
	if opts.ResetURL == "" {
		opts.ResetURL = "https://example.test/reset-password"
	}
	h, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return h
}

func postJSON(h *Handler, path string, body any) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	h.Routes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	rec := httptest.NewRecorder()
	enc, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(enc)))
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
		Sessions: &memSessionRevoker{},
		Sender:   sender,
	})

	rec := postJSON(h, "/api/v1/auth/password-reset/request", map[string]any{"email": "alice@example.com"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	if tokens.saveCt != 1 {
		t.Errorf("saveCt: got %d, want 1", tokens.saveCt)
	}
	if got := sender.Count(); got != 1 {
		t.Errorf("email send count: got %d, want 1", got)
	}
	msg, _ := sender.Last()
	if !strings.Contains(msg.TextBody, "?token=") {
		t.Errorf("email body missing token link: %q", msg.TextBody)
	}
	if !strings.Contains(msg.HTMLBody, "?token=") {
		t.Errorf("email html missing token link: %q", msg.HTMLBody)
	}
}

func TestRequest_UnknownEmail_Returns200_NoIssue(t *testing.T) {
	users := newMemUserStore("alice@example.com", "user-1")
	tokens := newMemTokenStore()
	sender := email.NewNoopSender()
	h := mustHandler(t, Options{
		Tokens:   tokens,
		Users:    users,
		Sessions: &memSessionRevoker{},
		Sender:   sender,
	})

	rec := postJSON(h, "/api/v1/auth/password-reset/request", map[string]any{"email": "ghost@example.com"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (enumeration-safe)", rec.Code)
	}
	if tokens.saveCt != 0 {
		t.Errorf("saveCt: got %d, want 0 (no user)", tokens.saveCt)
	}
	if sender.Count() != 0 {
		t.Errorf("email send count: got %d, want 0", sender.Count())
	}
}

func TestRequest_EmptyEmail_Returns200(t *testing.T) {
	users := newMemUserStore("alice@example.com", "user-1")
	tokens := newMemTokenStore()
	sender := email.NewNoopSender()
	h := mustHandler(t, Options{
		Tokens:   tokens,
		Users:    users,
		Sessions: &memSessionRevoker{},
		Sender:   sender,
	})

	rec := postJSON(h, "/api/v1/auth/password-reset/request", map[string]any{"email": ""})
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
		Sessions: &memSessionRevoker{},
	})

	mux := http.NewServeMux()
	h.Routes(mux)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password-reset/request", strings.NewReader("{not-json"))
	req.RemoteAddr = "203.0.113.5:1234"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", rec.Code)
	}
}

// =====================================================================
// Confirm endpoint
// =====================================================================

func TestConfirm_HappyPath_UpdatesPassword_RevokesSessions(t *testing.T) {
	users := newMemUserStore("alice@example.com", "user-1")
	tokens := newMemTokenStore()
	revoker := &memSessionRevoker{}
	h := mustHandler(t, Options{
		Tokens:   tokens,
		Users:    users,
		Sessions: revoker,
		Now:      func() time.Time { return time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC) },
	})

	// First, issue a token via the request endpoint.
	rec := postJSON(h, "/api/v1/auth/password-reset/request", map[string]any{"email": "alice@example.com"})
	if rec.Code != http.StatusOK {
		t.Fatalf("request status: got %d, want 200", rec.Code)
	}

	// Pluck the plaintext token out of the tokens fake. We can't read
	// the plaintext from the store (it stores the hash), so the test
	// reconstructs by enumerating issued hashes; we know there's
	// exactly one. To round-trip the plaintext we instead capture it
	// from the noop sender's recorded message.
	sender := h.opts.Sender.(*email.NoopSender)
	msg, _ := sender.Last()
	token := extractToken(t, msg.TextBody)

	confirmRec := postJSON(h, "/api/v1/auth/password-reset/confirm", map[string]any{
		"token":        token,
		"new_password": "correct-horse-battery-staple",
	})
	if confirmRec.Code != http.StatusOK {
		t.Fatalf("confirm status: got %d, want 200; body=%s", confirmRec.Code, confirmRec.Body.String())
	}

	var resp confirmResponse
	if err := json.Unmarshal(confirmRec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.UserID != "user-1" {
		t.Errorf("user_id: got %q, want user-1", resp.UserID)
	}
	if users.passwords["user-1"] == "" {
		t.Errorf("password not written")
	}
	if !strings.HasPrefix(users.passwords["user-1"], "$argon2id$") {
		t.Errorf("password hash shape: got %q, want argon2id PHC", users.passwords["user-1"])
	}
	if len(revoker.revoked) != 1 || revoker.revoked[0] != "user-1" {
		t.Errorf("sessions revoked: got %v, want [user-1]", revoker.revoked)
	}
}

func TestConfirm_ReplayedToken_RejectedWith410(t *testing.T) {
	users := newMemUserStore("alice@example.com", "user-1")
	tokens := newMemTokenStore()
	h := mustHandler(t, Options{
		Tokens:   tokens,
		Users:    users,
		Sessions: &memSessionRevoker{},
		Now:      func() time.Time { return time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC) },
	})

	postJSON(h, "/api/v1/auth/password-reset/request", map[string]any{"email": "alice@example.com"})
	sender := h.opts.Sender.(*email.NoopSender)
	msg, _ := sender.Last()
	token := extractToken(t, msg.TextBody)

	// First confirm: succeeds.
	rec := postJSON(h, "/api/v1/auth/password-reset/confirm", map[string]any{
		"token":        token,
		"new_password": "correct-horse-battery-staple",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("first confirm: %d", rec.Code)
	}

	// Second confirm with the same token: should fail.
	rec2 := postJSON(h, "/api/v1/auth/password-reset/confirm", map[string]any{
		"token":        token,
		"new_password": "correct-horse-battery-staple",
	})
	if rec2.Code != http.StatusGone {
		t.Fatalf("replay: got %d, want 410", rec2.Code)
	}
}

func TestConfirm_ExpiredToken_RejectedWith410(t *testing.T) {
	users := newMemUserStore("alice@example.com", "user-1")
	tokens := newMemTokenStore()
	clock := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	h := mustHandler(t, Options{
		Tokens:   tokens,
		Users:    users,
		Sessions: &memSessionRevoker{},
		Now:      func() time.Time { return clock },
	})

	postJSON(h, "/api/v1/auth/password-reset/request", map[string]any{"email": "alice@example.com"})
	sender := h.opts.Sender.(*email.NoopSender)
	msg, _ := sender.Last()
	token := extractToken(t, msg.TextBody)

	// Advance past the 1h TTL.
	clock = clock.Add(2 * time.Hour)

	rec := postJSON(h, "/api/v1/auth/password-reset/confirm", map[string]any{
		"token":        token,
		"new_password": "correct-horse-battery-staple",
	})
	if rec.Code != http.StatusGone {
		t.Fatalf("expired: got %d, want 410", rec.Code)
	}
}

func TestConfirm_WeakPassword_Returns422(t *testing.T) {
	users := newMemUserStore("alice@example.com", "user-1")
	tokens := newMemTokenStore()
	h := mustHandler(t, Options{
		Tokens:   tokens,
		Users:    users,
		Sessions: &memSessionRevoker{},
		Now:      func() time.Time { return time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC) },
	})

	postJSON(h, "/api/v1/auth/password-reset/request", map[string]any{"email": "alice@example.com"})
	sender := h.opts.Sender.(*email.NoopSender)
	msg, _ := sender.Last()
	token := extractToken(t, msg.TextBody)

	rec := postJSON(h, "/api/v1/auth/password-reset/confirm", map[string]any{
		"token":        token,
		"new_password": "short", // < MinPasswordLength
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status: got %d, want 422", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["error"] != "weak_password" {
		t.Errorf("error code: got %v, want weak_password", body["error"])
	}
	// Weak password must NOT consume the token.
	if tokens.consumeCt != 0 {
		t.Errorf("consumeCt: got %d, want 0 (weak password should short-circuit)", tokens.consumeCt)
	}
}

func TestConfirm_MalformedToken_Returns410(t *testing.T) {
	h := mustHandler(t, Options{
		Tokens:   newMemTokenStore(),
		Users:    newMemUserStore("alice@example.com", "u1"),
		Sessions: &memSessionRevoker{},
	})
	rec := postJSON(h, "/api/v1/auth/password-reset/confirm", map[string]any{
		"token":        "not-a-hex-token",
		"new_password": "correct-horse-battery-staple",
	})
	if rec.Code != http.StatusGone {
		t.Fatalf("status: got %d, want 410", rec.Code)
	}
}

func TestConfirm_UnknownToken_Returns410(t *testing.T) {
	h := mustHandler(t, Options{
		Tokens:   newMemTokenStore(),
		Users:    newMemUserStore("alice@example.com", "u1"),
		Sessions: &memSessionRevoker{},
	})
	// Well-formed (64 hex chars) but never issued.
	rec := postJSON(h, "/api/v1/auth/password-reset/confirm", map[string]any{
		"token":        strings.Repeat("a", 64),
		"new_password": "correct-horse-battery-staple",
	})
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
		Sessions: &memSessionRevoker{},
		Limiter:  limiter,
	})

	for i := 0; i < 2; i++ {
		rec := postJSON(h, "/api/v1/auth/password-reset/request", map[string]any{"email": "alice@example.com"})
		if rec.Code != http.StatusOK {
			t.Fatalf("attempt %d: got %d, want 200", i, rec.Code)
		}
	}
	rec := postJSON(h, "/api/v1/auth/password-reset/request", map[string]any{"email": "alice@example.com"})
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

// extractToken pulls "?token=<token>" out of an email body so tests
// can re-use the same plaintext on the confirm side.
func extractToken(t *testing.T, body string) string {
	t.Helper()
	const marker = "?token="
	idx := strings.Index(body, marker)
	if idx < 0 {
		t.Fatalf("no token marker in body: %q", body)
	}
	rest := body[idx+len(marker):]
	// Stop at the first whitespace, newline, or closing tag.
	end := len(rest)
	for i, r := range rest {
		if r == ' ' || r == '\n' || r == '\r' || r == '<' || r == '"' || r == '>' {
			end = i
			break
		}
	}
	return rest[:end]
}
