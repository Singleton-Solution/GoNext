package verify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/audit"
	"github.com/Singleton-Solution/GoNext/packages/go/email"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
	"github.com/Singleton-Solution/GoNext/packages/go/ratelimit"
)

// memTokenStore is the test seam for [TokenStore]. It stores
// (hash -> user) pairs with a synthetic expiry derived from a
// caller-supplied clock.
type memTokenStore struct {
	mu     sync.Mutex
	rows   map[string]memTokenRow
	now    func() time.Time
	saveCt int
}

type memTokenRow struct {
	userID string
	expiry time.Time
}

func newMemTokenStore(now func() time.Time) *memTokenStore {
	return &memTokenStore{rows: map[string]memTokenRow{}, now: now}
}

func (s *memTokenStore) Save(_ context.Context, h, u string, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saveCt++
	s.rows[h] = memTokenRow{userID: u, expiry: s.now().Add(ttl)}
	return nil
}

func (s *memTokenStore) Lookup(_ context.Context, h string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[h]
	if !ok {
		return "", ErrTokenNotFound
	}
	if !s.now().Before(row.expiry) {
		delete(s.rows, h)
		return "", ErrTokenNotFound
	}
	return row.userID, nil
}

func (s *memTokenStore) Consume(_ context.Context, h string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.rows, h)
	return nil
}

// memUser is the test seam for [UserVerifier].
type memUser struct {
	mu       sync.Mutex
	emails   map[string]string  // userID -> email
	verified map[string]bool    // userID -> verified
	calls    []string           // userIDs that hit MarkVerified
}

func newMemUser(uid, addr string) *memUser {
	return &memUser{
		emails:   map[string]string{uid: addr},
		verified: map[string]bool{},
	}
}

func (u *memUser) MarkVerified(_ context.Context, userID string) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if _, ok := u.emails[userID]; !ok {
		return ErrUserNotFound
	}
	u.calls = append(u.calls, userID)
	u.verified[userID] = true
	return nil
}

func (u *memUser) LookupEmail(_ context.Context, userID string) (string, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	addr, ok := u.emails[userID]
	if !ok {
		return "", ErrUserNotFound
	}
	return addr, nil
}

func (u *memUser) isVerified(userID string) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.verified[userID]
}

// fakeRequireSession is the test stand-in for the production auth
// middleware. It attaches the given principal to the request
// context so handleSend sees a logged-in user.
func fakeRequireSession(p policy.Principal) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := policy.WithPrincipal(r.Context(), p)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

type harness struct {
	h        *Handler
	tokens   *memTokenStore
	users    *memUser
	sender   *email.NoopSender
	limiter  *ratelimit.MemoryLimiter
	emitter  *audit.Emitter
	store    *audit.MemoryStore
	clock    *fakeClock
	server   *httptest.Server
	verifyTo string
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newClock(t0 time.Time) *fakeClock { return &fakeClock{now: t0} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newHarness(t *testing.T, userID, recipient string) *harness {
	t.Helper()
	clk := newClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	tokens := newMemTokenStore(clk.Now)
	users := newMemUser(userID, recipient)
	sender := email.NewNoopSender()

	limiter, err := ratelimit.NewMemoryLimiter(ratelimit.Policy{
		Capacity:   1,
		RefillRate: 1.0 / 60.0, // 1 per minute
	})
	if err != nil {
		t.Fatalf("limiter: %v", err)
	}

	store := audit.NewMemoryStore()
	emitter := audit.NewEmitter(store)

	h, err := New(Options{
		Tokens:      tokens,
		Users:       users,
		Sender:      sender,
		Limiter:     limiter,
		Audit:       emitter,
		VerifyURL:   "https://chassis.test/verify",
		FromAddress: "noreply@chassis.test",
		Subject:     "Verify your email",
		Now:         clk.Now,
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	mux := http.NewServeMux()
	h.Routes(mux, fakeRequireSession(policy.Principal{UserID: userID}))

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return &harness{
		h:        h,
		tokens:   tokens,
		users:    users,
		sender:   sender,
		limiter:  limiter,
		emitter:  emitter,
		store:    store,
		clock:    clk,
		server:   srv,
		verifyTo: recipient,
	}
}

func TestHandleSend_HappyPath(t *testing.T) {
	hr := newHarness(t, "user-1", "alice@example.com")

	resp, err := http.Post(hr.server.URL+"/api/v1/auth/verify/send", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status: got %d want 202; body=%s", resp.StatusCode, body)
	}
	if hr.sender.Count() != 1 {
		t.Fatalf("expected 1 sent message, got %d", hr.sender.Count())
	}
	msg, _ := hr.sender.Last()
	if msg.To != "alice@example.com" {
		t.Errorf("To: got %q want alice@example.com", msg.To)
	}
	if msg.From != "noreply@chassis.test" {
		t.Errorf("From: got %q", msg.From)
	}
	// Pull the token out of the sent body and assert it round-trips
	// through validToken.
	token := extractToken(t, msg)
	if !validToken(token) {
		t.Errorf("extracted token is malformed: %q", token)
	}
	if hr.tokens.saveCt != 1 {
		t.Errorf("expected 1 save, got %d", hr.tokens.saveCt)
	}
}

func TestHandleSend_URLContainsToken(t *testing.T) {
	hr := newHarness(t, "user-1", "alice@example.com")
	if _, err := http.Post(hr.server.URL+"/api/v1/auth/verify/send", "application/json", nil); err != nil {
		t.Fatalf("POST: %v", err)
	}
	msg, _ := hr.sender.Last()
	if !strings.Contains(msg.TextBody, "token=") {
		t.Errorf("text body missing token= in link:\n%s", msg.TextBody)
	}
	if !strings.Contains(msg.HTMLBody, "token=") {
		t.Errorf("html body missing token= in link:\n%s", msg.HTMLBody)
	}
	if !strings.Contains(msg.TextBody, "https://chassis.test/verify") {
		t.Errorf("text body missing base url:\n%s", msg.TextBody)
	}
}

func TestHandleSend_Unauthenticated(t *testing.T) {
	// Build a handler without the principal-attaching middleware.
	clk := newClock(time.Now())
	h, err := New(Options{
		Tokens:    newMemTokenStore(clk.Now),
		Users:     newMemUser("u", "a@b.test"),
		Sender:    email.NewNoopSender(),
		VerifyURL: "https://x.test/verify",
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mux := http.NewServeMux()
	// Bypass requireSession entirely by passing a no-op.
	h.Routes(mux, func(next http.Handler) http.Handler { return next })

	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/v1/auth/verify/send", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status: got %d want 401", resp.StatusCode)
	}
}

func TestHandleSend_RateLimited(t *testing.T) {
	hr := newHarness(t, "user-1", "alice@example.com")

	// First call should succeed.
	resp, err := http.Post(hr.server.URL+"/api/v1/auth/verify/send", "application/json", nil)
	if err != nil {
		t.Fatalf("POST 1: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("first send: got %d want 202", resp.StatusCode)
	}

	// Second call within the minute is rejected.
	resp, err = http.Post(hr.server.URL+"/api/v1/auth/verify/send", "application/json", nil)
	if err != nil {
		t.Fatalf("POST 2: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("second send: got %d want 429", resp.StatusCode)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Errorf("missing Retry-After header")
	}
	if hr.sender.Count() != 1 {
		t.Errorf("sender count: got %d want 1", hr.sender.Count())
	}
}

func TestHandleSend_MissingEmail(t *testing.T) {
	// user without an email row -> 401 (treated as user-not-found).
	clk := newClock(time.Now())
	tokens := newMemTokenStore(clk.Now)
	users := &memUser{emails: map[string]string{}, verified: map[string]bool{}}
	h, err := New(Options{
		Tokens:    tokens,
		Users:     users,
		Sender:    email.NewNoopSender(),
		VerifyURL: "https://x.test/verify",
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mux := http.NewServeMux()
	h.Routes(mux, fakeRequireSession(policy.Principal{UserID: "ghost"}))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/v1/auth/verify/send", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status: got %d want 401", resp.StatusCode)
	}
}

func TestHandleVerify_HappyPath(t *testing.T) {
	hr := newHarness(t, "user-1", "alice@example.com")
	postSend(t, hr)

	msg, _ := hr.sender.Last()
	token := extractToken(t, msg)

	resp, err := http.Get(hr.server.URL + "/api/v1/auth/verify?token=" + url.QueryEscape(token))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status: got %d want 200; body=%s", resp.StatusCode, body)
	}
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["verified"] != true {
		t.Errorf("body: got %v want {verified:true}", got)
	}
	if !hr.users.isVerified("user-1") {
		t.Errorf("user not marked verified")
	}
}

func TestHandleVerify_ExpiredToken(t *testing.T) {
	hr := newHarness(t, "user-1", "alice@example.com")
	postSend(t, hr)
	msg, _ := hr.sender.Last()
	token := extractToken(t, msg)

	hr.clock.Advance(25 * time.Hour)

	resp, err := http.Get(hr.server.URL + "/api/v1/auth/verify?token=" + url.QueryEscape(token))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusGone {
		t.Errorf("status: got %d want 410", resp.StatusCode)
	}
	if hr.users.isVerified("user-1") {
		t.Errorf("user should not be verified after expired token")
	}
}

func TestHandleVerify_ReusedToken(t *testing.T) {
	hr := newHarness(t, "user-1", "alice@example.com")
	postSend(t, hr)
	msg, _ := hr.sender.Last()
	token := extractToken(t, msg)

	// First call succeeds.
	resp, err := http.Get(hr.server.URL + "/api/v1/auth/verify?token=" + url.QueryEscape(token))
	if err != nil {
		t.Fatalf("GET 1: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first GET: got %d want 200", resp.StatusCode)
	}

	// Second call returns 410.
	resp, err = http.Get(hr.server.URL + "/api/v1/auth/verify?token=" + url.QueryEscape(token))
	if err != nil {
		t.Fatalf("GET 2: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusGone {
		t.Errorf("second GET: got %d want 410", resp.StatusCode)
	}
}

func TestHandleVerify_MalformedToken(t *testing.T) {
	hr := newHarness(t, "user-1", "alice@example.com")
	for _, tok := range []string{"", "x", "deadbeef"} {
		resp, err := http.Get(hr.server.URL + "/api/v1/auth/verify?token=" + url.QueryEscape(tok))
		if err != nil {
			t.Fatalf("GET %q: %v", tok, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusGone {
			t.Errorf("token=%q: got %d want 410", tok, resp.StatusCode)
		}
	}
}

func TestHandleVerify_UnknownToken(t *testing.T) {
	hr := newHarness(t, "user-1", "alice@example.com")
	bogus, _ := generateToken()
	resp, err := http.Get(hr.server.URL + "/api/v1/auth/verify?token=" + url.QueryEscape(bogus))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusGone {
		t.Errorf("status: got %d want 410", resp.StatusCode)
	}
	if hr.users.isVerified("user-1") {
		t.Errorf("user should not be verified")
	}
}

func TestHandleVerify_AuditEmitted(t *testing.T) {
	hr := newHarness(t, "user-1", "alice@example.com")
	postSend(t, hr)
	msg, _ := hr.sender.Last()
	token := extractToken(t, msg)

	resp, err := http.Get(hr.server.URL + "/api/v1/auth/verify?token=" + url.QueryEscape(token))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()

	events, err := hr.store.List(context.Background(), audit.Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var sawSent, sawCompleted bool
	for _, e := range events {
		switch e.EventType {
		case "auth.verify.email.sent":
			sawSent = true
		case "auth.verify.email.completed":
			sawCompleted = true
		}
	}
	if !sawSent {
		t.Errorf("missing auth.verify.email.sent")
	}
	if !sawCompleted {
		t.Errorf("missing auth.verify.email.completed")
	}
}

func TestHandleVerify_InvalidEmitsWarning(t *testing.T) {
	hr := newHarness(t, "user-1", "alice@example.com")
	bogus, _ := generateToken()
	resp, err := http.Get(hr.server.URL + "/api/v1/auth/verify?token=" + url.QueryEscape(bogus))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	events, _ := hr.store.List(context.Background(), audit.Filter{
		EventType: "auth.verify.email.invalid",
	})
	if len(events) == 0 {
		t.Errorf("expected an invalid event")
		return
	}
	if events[0].Severity != audit.SeverityWarning {
		t.Errorf("severity: got %v want warning", events[0].Severity)
	}
}

func TestHandleVerify_AlreadyVerifiedUserStillSucceeds(t *testing.T) {
	hr := newHarness(t, "user-1", "alice@example.com")
	postSend(t, hr)
	msg, _ := hr.sender.Last()
	token := extractToken(t, msg)

	// Pre-flag verified so MarkVerified is effectively idempotent.
	hr.users.mu.Lock()
	hr.users.verified["user-1"] = true
	hr.users.mu.Unlock()

	resp, err := http.Get(hr.server.URL + "/api/v1/auth/verify?token=" + url.QueryEscape(token))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d want 200", resp.StatusCode)
	}
}

func TestHandleSend_UsesRecipientContext(t *testing.T) {
	// When a recipient is attached via WithRecipient upstream, we
	// honor it instead of the DB lookup.
	clk := newClock(time.Now())
	tokens := newMemTokenStore(clk.Now)
	users := newMemUser("user-1", "wrong@example.com")
	sender := email.NewNoopSender()

	h, err := New(Options{
		Tokens:    tokens,
		Users:     users,
		Sender:    sender,
		VerifyURL: "https://x.test/verify",
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mux := http.NewServeMux()
	h.Routes(mux, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := policy.WithPrincipal(r.Context(), policy.Principal{UserID: "user-1"})
			ctx = WithRecipient(ctx, "right@example.com")
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/v1/auth/verify/send", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status: got %d", resp.StatusCode)
	}
	msg, _ := sender.Last()
	if msg.To != "right@example.com" {
		t.Errorf("To: got %q want right@example.com", msg.To)
	}
}

func TestHandleSend_SenderFailure(t *testing.T) {
	hr := newHarness(t, "user-1", "alice@example.com")
	// Swap the sender for one that always errors.
	hr.h.opts.Sender = &erroringSender{err: errors.New("smtp down")}

	resp, err := http.Post(hr.server.URL+"/api/v1/auth/verify/send", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status: got %d want 502", resp.StatusCode)
	}
}

type erroringSender struct{ err error }

func (s *erroringSender) Send(_ context.Context, _ email.Message) error { return s.err }

func TestMaskEmail(t *testing.T) {
	cases := map[string]string{
		"alice@example.com": "a***@example.com",
		"x@example.com":     "*@example.com",
		"first.last@x.test": "f***@x.test",
		"":                  "",
		"no-at-sign":        "no-at-sign",
	}
	for in, want := range cases {
		if got := maskEmail(in); got != want {
			t.Errorf("maskEmail(%q): got %q want %q", in, got, want)
		}
	}
}

func TestNew_RequiresFields(t *testing.T) {
	good := func() Options {
		clk := newClock(time.Now())
		return Options{
			Tokens:    newMemTokenStore(clk.Now),
			Users:     newMemUser("u", "x@y.test"),
			Sender:    email.NewNoopSender(),
			VerifyURL: "https://x.test/v",
		}
	}
	if _, err := New(Options{}); err == nil {
		t.Error("expected error for empty options")
	}
	missing := good()
	missing.Tokens = nil
	if _, err := New(missing); err == nil {
		t.Error("expected error for missing Tokens")
	}
	missing = good()
	missing.Users = nil
	if _, err := New(missing); err == nil {
		t.Error("expected error for missing Users")
	}
	missing = good()
	missing.Sender = nil
	if _, err := New(missing); err == nil {
		t.Error("expected error for missing Sender")
	}
	missing = good()
	missing.VerifyURL = ""
	if _, err := New(missing); err == nil {
		t.Error("expected error for missing VerifyURL")
	}
}

func TestBuildLink_AppendsToken(t *testing.T) {
	clk := newClock(time.Now())
	h, err := New(Options{
		Tokens:    newMemTokenStore(clk.Now),
		Users:     newMemUser("u", "a@b.test"),
		Sender:    email.NewNoopSender(),
		VerifyURL: "https://x.test/verify",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	link := h.buildLink("abc")
	if !strings.Contains(link, "token=abc") {
		t.Errorf("buildLink: %s", link)
	}
}

func TestBuildLink_PreservesQueryString(t *testing.T) {
	clk := newClock(time.Now())
	h, err := New(Options{
		Tokens:    newMemTokenStore(clk.Now),
		Users:     newMemUser("u", "a@b.test"),
		Sender:    email.NewNoopSender(),
		VerifyURL: "https://x.test/verify?lang=en",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	link := h.buildLink("abc")
	if !strings.Contains(link, "lang=en") || !strings.Contains(link, "token=abc") {
		t.Errorf("buildLink: %s", link)
	}
}

// postSend POSTs once to verify/send to seed the token store. Used
// by the verify tests that need a freshly-issued token.
func postSend(t *testing.T, hr *harness) {
	t.Helper()
	resp, err := http.Post(hr.server.URL+"/api/v1/auth/verify/send", "application/json", nil)
	if err != nil {
		t.Fatalf("seed POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("seed POST: got %d want 202; body=%s", resp.StatusCode, body)
	}
}

// extractToken pulls the verification token out of an email body.
// It scans for "token=" and reads until a non-token character.
func extractToken(t *testing.T, msg email.Message) string {
	t.Helper()
	body := msg.TextBody
	idx := strings.Index(body, "token=")
	if idx < 0 {
		t.Fatalf("no token= in body:\n%s", body)
	}
	start := idx + len("token=")
	end := start
	for end < len(body) {
		c := body[end]
		if c >= 'A' && c <= 'Z' {
			end++
			continue
		}
		if c >= 'a' && c <= 'z' {
			end++
			continue
		}
		if c >= '0' && c <= '9' {
			end++
			continue
		}
		if c == '-' || c == '_' {
			end++
			continue
		}
		break
	}
	return body[start:end]
}
