package setup

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

	"github.com/Singleton-Solution/GoNext/packages/go/ratelimit"
	"github.com/Singleton-Solution/GoNext/packages/go/session"
)

// fixedNow returns a deterministic clock for assertions over the
// install timestamp. We use a fixed UTC instant inside 2026 so the
// test never drifts when run under -count=N.
func fixedNow() time.Time {
	return time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
}

// stubHash is a PasswordHasher that returns a deterministic string
// without paying the argon2 cost. The handler doesn't inspect the
// returned hash beyond passing it through to UserCreator; the unit
// tests therefore don't need real PHC strings.
func stubHash(plaintext string, _ []byte) (string, error) {
	return "stub$" + plaintext, nil
}

// brokenHash returns an error so we can exercise the 500 path.
func brokenHash(_ string, _ []byte) (string, error) {
	return "", errors.New("hash exploded")
}

// fixture bundles every dependency a handler test needs. Each top-level
// test calls newFixture() to get a fresh slate; sub-tests under it
// share state on purpose so the rate-limit assertion can observe
// previous attempts.
type fixture struct {
	users    *MemoryUserCreator
	options  *MemoryOptionStore
	sessions *MemorySession
	limiter  Limiter
	handler  *Handler
	d        Deps
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	users := NewMemoryUserCreator()
	options := NewMemoryOptionStore()
	sessions := NewMemorySession()
	limiter, err := NewMemoryLimiter(DefaultRateLimit)
	if err != nil {
		t.Fatalf("NewMemoryLimiter: %v", err)
	}
	d := Deps{
		Users:              users,
		Options:            options,
		Sessions:           sessions,
		Hash:               stubHash,
		Pepper:             []byte("test-pepper"),
		Limiter:            limiter,
		SessionAbsoluteTTL: 24 * time.Hour,
		SessionIdleTTL:     1 * time.Hour,
		Insecure:           true, // so the cookie's Secure attribute is dropped under httptest
		Now:                fixedNow,
	}
	h, err := NewHandler(d)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	return &fixture{
		users:    users,
		options:  options,
		sessions: sessions,
		limiter:  limiter,
		handler:  h,
		d:        d,
	}
}

// doPost issues an install POST with the given body. The remote addr
// is parameterized so rate-limit tests can simulate distinct IPs.
func doPost(t *testing.T, h http.HandlerFunc, body any, remoteAddr string) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	switch x := body.(type) {
	case string:
		raw = []byte(x)
	default:
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
	}
	r := httptest.NewRequest(http.MethodPost, "/api/v1/setup/install", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = remoteAddr
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

func doGet(t *testing.T, h http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/setup/status", nil)
	r.RemoteAddr = "203.0.113.1:5000"
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

// validBody returns a payload that satisfies every validation rule.
func validBody() installRequest {
	return installRequest{
		AdminEmail:    "admin@example.com",
		AdminPassword: "correct-horse-battery-staple", // 28 chars
		SiteName:      "Acme CMS",
		SiteURL:       "https://acme.example.com",
	}
}

// ---- Status -----------------------------------------------------------------

func TestStatus_BeforeInstall(t *testing.T) {
	f := newFixture(t)

	w := doGet(t, f.handler.Status)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", w.Code, w.Body)
	}
	var out statusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.InstallationCompleted {
		t.Error("installation_completed: want false")
	}
	if out.UserCount != 0 {
		t.Errorf("user_count: got %d, want 0", out.UserCount)
	}
}

func TestStatus_AfterInstall(t *testing.T) {
	f := newFixture(t)
	// Seed the marker directly so the test doesn't depend on the install
	// path's correctness.
	_ = f.options.Write(context.Background(), InstallationOptionKey, "2026-05-22T12:00:00Z")

	w := doGet(t, f.handler.Status)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", w.Code)
	}
	var out statusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.InstallationCompleted {
		t.Error("installation_completed: want true")
	}
	if out.UserCount != 1 {
		t.Errorf("user_count: got %d, want ≥1", out.UserCount)
	}
}

func TestStatus_MethodNotAllowed(t *testing.T) {
	f := newFixture(t)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/setup/status", nil)
	w := httptest.NewRecorder()
	f.handler.Status(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status: got %d, want 405", w.Code)
	}
	if w.Header().Get("Allow") != http.MethodGet {
		t.Errorf("Allow: got %q, want GET", w.Header().Get("Allow"))
	}
}

// ---- Install: happy path ----------------------------------------------------

func TestInstall_HappyPath(t *testing.T) {
	f := newFixture(t)

	w := doPost(t, f.handler.Install, validBody(), "203.0.113.1:5000")
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", w.Code, w.Body)
	}
	var out installResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.UserID == "" {
		t.Error("user_id: empty")
	}
	if out.ExpiresAt.IsZero() {
		t.Error("expires_at: zero")
	}

	// User row exists.
	if f.users.Len() != 1 {
		t.Errorf("users: got %d, want 1", f.users.Len())
	}

	// Install marker is set with the fixed clock's timestamp.
	got, ok := f.options.Get(InstallationOptionKey)
	if !ok {
		t.Fatalf("install marker not written")
	}
	if s, ok := got.(string); !ok || !strings.HasPrefix(s, "2026-05-22T12:00:00") {
		t.Errorf("install marker: got %v, want 2026-05-22T12:00:00...", got)
	}

	// Site name + URL written.
	if v, _ := f.options.Get(SiteNameOptionKey); v != "Acme CMS" {
		t.Errorf("site name: got %v, want Acme CMS", v)
	}
	if v, _ := f.options.Get(SiteURLOptionKey); v != "https://acme.example.com" {
		t.Errorf("site URL: got %v", v)
	}

	// Cookie set.
	cookies := w.Result().Cookies()
	var found bool
	for _, c := range cookies {
		if c.Name == session.CookieName && c.Value != "" {
			found = true
			if !c.HttpOnly {
				t.Error("cookie not HttpOnly")
			}
		}
	}
	if !found {
		t.Error("session cookie not set")
	}
}

// ---- Install: already-installed lock ---------------------------------------

func TestInstall_RejectsSecondAttempt(t *testing.T) {
	f := newFixture(t)

	// First install.
	w1 := doPost(t, f.handler.Install, validBody(), "203.0.113.1:5000")
	if w1.Code != http.StatusOK {
		t.Fatalf("first install: %d", w1.Code)
	}

	// Second install from a DIFFERENT IP — the lock is global, not
	// per-IP.
	body2 := validBody()
	body2.AdminEmail = "evil@example.com"
	w2 := doPost(t, f.handler.Install, body2, "198.51.100.1:5000")
	if w2.Code != http.StatusLocked {
		t.Fatalf("second install: got %d, want 423; body=%s", w2.Code, w2.Body)
	}
	var out errorResponse
	if err := json.Unmarshal(w2.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Code != "already_installed" {
		t.Errorf("code: got %q, want already_installed", out.Code)
	}

	// No second user row.
	if f.users.Len() != 1 {
		t.Errorf("users: got %d, want 1 (second install must not create)", f.users.Len())
	}
}

func TestStatus_StaysCorrectAfterInstall(t *testing.T) {
	f := newFixture(t)
	w := doPost(t, f.handler.Install, validBody(), "203.0.113.1:5000")
	if w.Code != http.StatusOK {
		t.Fatalf("install: %d", w.Code)
	}
	s := doGet(t, f.handler.Status)
	var out statusResponse
	_ = json.Unmarshal(s.Body.Bytes(), &out)
	if !out.InstallationCompleted {
		t.Errorf("installation_completed: want true after install")
	}
}

// ---- Install: validation -----------------------------------------------------

func TestInstall_RejectsWeakPassword(t *testing.T) {
	f := newFixture(t)
	body := validBody()
	body.AdminPassword = "short" // 5 chars, well below the 12-char floor
	w := doPost(t, f.handler.Install, body, "203.0.113.1:5000")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", w.Code)
	}
	var out errorResponse
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out.Code != "weak_password" {
		t.Errorf("code: got %q, want weak_password", out.Code)
	}
	if f.users.Len() != 0 {
		t.Errorf("users: weak password must not create a user")
	}
}

func TestInstall_RejectsInvalidEmail(t *testing.T) {
	f := newFixture(t)
	cases := []string{"", "no-at-sign", "no@dot", "bad email@example.com"}
	for _, e := range cases {
		body := validBody()
		body.AdminEmail = e
		w := doPost(t, f.handler.Install, body, "203.0.113.1:5000")
		if w.Code != http.StatusBadRequest {
			t.Errorf("%q: got %d, want 400", e, w.Code)
		}
		var out errorResponse
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		if out.Code != "invalid_email" {
			t.Errorf("%q: code %q, want invalid_email", e, out.Code)
		}
	}
}

func TestInstall_RejectsInvalidURL(t *testing.T) {
	f := newFixture(t)
	cases := []string{"", "not a url", "ftp://example.com", "//host"}
	for _, u := range cases {
		body := validBody()
		body.SiteURL = u
		w := doPost(t, f.handler.Install, body, "203.0.113.1:5000")
		if w.Code != http.StatusBadRequest {
			t.Errorf("%q: got %d, want 400", u, w.Code)
		}
		var out errorResponse
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		if out.Code != "invalid_site_url" {
			t.Errorf("%q: code %q, want invalid_site_url", u, out.Code)
		}
	}
}

func TestInstall_RejectsEmptySiteName(t *testing.T) {
	f := newFixture(t)
	body := validBody()
	body.SiteName = "   "
	w := doPost(t, f.handler.Install, body, "203.0.113.1:5000")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", w.Code)
	}
	var out errorResponse
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out.Code != "invalid_site_name" {
		t.Errorf("code: got %q, want invalid_site_name", out.Code)
	}
}

func TestInstall_RejectsMalformedJSON(t *testing.T) {
	f := newFixture(t)
	w := doPost(t, f.handler.Install, "this is not json", "203.0.113.1:5000")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", w.Code)
	}
}

func TestInstall_RejectsUnknownFields(t *testing.T) {
	f := newFixture(t)
	body := `{
		"admin_email": "admin@example.com",
		"admin_password": "correct-horse-battery-staple",
		"site_name": "Acme",
		"site_url": "https://acme.example.com",
		"backdoor": "yes please"
	}`
	w := doPost(t, f.handler.Install, body, "203.0.113.1:5000")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", w.Code)
	}
}

func TestInstall_MethodNotAllowed(t *testing.T) {
	f := newFixture(t)
	r := httptest.NewRequest(http.MethodGet, "/api/v1/setup/install", nil)
	w := httptest.NewRecorder()
	f.handler.Install(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status: got %d, want 405", w.Code)
	}
}

// ---- Install: rate limiting --------------------------------------------------

func TestInstall_RateLimitTriggersAfterBudget(t *testing.T) {
	// Fresh fixture, no install yet, so every attempt hits the limiter.
	// We use validBody but invert the password so each call fails
	// validation rather than completing — that way the lock never
	// trips and the rate-limit token counter is the only ceiling.
	users := NewMemoryUserCreator()
	options := NewMemoryOptionStore()
	sessions := NewMemorySession()
	// Custom limiter with capacity 3 and a refill rate that's
	// effectively zero over the test window, so the budget never
	// regenerates mid-test.
	rl, err := ratelimit.NewMemoryLimiter(ratelimit.Policy{
		Capacity:   3,
		RefillRate: 0.0001, // ~1 token per 10000s — way out of test scope
		Prefix:     "test",
	})
	if err != nil {
		t.Fatalf("ratelimit: %v", err)
	}
	d := Deps{
		Users:              users,
		Options:            options,
		Sessions:           sessions,
		Hash:               stubHash,
		Limiter:            adapter{rl: rl},
		SessionAbsoluteTTL: time.Hour,
		SessionIdleTTL:     time.Minute,
		Insecure:           true,
		Now:                fixedNow,
	}
	h, err := NewHandler(d)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	// Three attempts from the same IP — last one passes (install OK).
	// Then a fourth attempt from the SAME IP is rate-limited.
	addr := "203.0.113.50:1000"

	// First, exhaust the bucket with two failed-validation attempts
	// (weak password) so the install never completes (which would set
	// the lock and short-circuit the next check).
	body := validBody()
	body.AdminPassword = "short" // 5 chars, fails validation
	for i := 0; i < 3; i++ {
		w := doPost(t, h.Install, body, addr)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("attempt %d: got %d, want 400", i+1, w.Code)
		}
	}

	// Fourth attempt: budget exhausted.
	w := doPost(t, h.Install, body, addr)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("attempt 4: got %d, want 429; body=%s", w.Code, w.Body)
	}
	var out errorResponse
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out.Code != "rate_limited" {
		t.Errorf("code: got %q, want rate_limited", out.Code)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Error("Retry-After: missing")
	}

	// A DIFFERENT IP gets its own bucket.
	w2 := doPost(t, h.Install, body, "198.51.100.99:2000")
	if w2.Code == http.StatusTooManyRequests {
		t.Errorf("different IP: got 429, want a non-rate-limited response")
	}
}

// ---- Install: failure modes --------------------------------------------------

type erroringOptions struct {
	*MemoryOptionStore
	failOnWrite bool
	failOnHas   bool
}

func (e *erroringOptions) Has(ctx context.Context, key string) (bool, error) {
	if e.failOnHas {
		return false, errors.New("synthetic options.Has failure")
	}
	return e.MemoryOptionStore.Has(ctx, key)
}

func (e *erroringOptions) Write(ctx context.Context, key string, value any) error {
	if e.failOnWrite {
		return errors.New("synthetic options.Write failure")
	}
	return e.MemoryOptionStore.Write(ctx, key, value)
}

func TestInstall_HashErrorReturns500(t *testing.T) {
	f := newFixture(t)
	f.d.Hash = brokenHash
	h, err := NewHandler(f.d)
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	w := doPost(t, h.Install, validBody(), "203.0.113.1:5000")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d, want 500", w.Code)
	}
}

func TestInstall_LockReadErrorReturns500(t *testing.T) {
	f := newFixture(t)
	wrapped := &erroringOptions{
		MemoryOptionStore: f.options,
		failOnHas:         true,
	}
	f.d.Options = wrapped
	h, err := NewHandler(f.d)
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	w := doPost(t, h.Install, validBody(), "203.0.113.1:5000")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d, want 500", w.Code)
	}
}

// ---- NewHandler validation --------------------------------------------------

func TestNewHandler_RejectsIncompleteDeps(t *testing.T) {
	base := func() Deps {
		l, _ := NewMemoryLimiter(DefaultRateLimit)
		return Deps{
			Users:              NewMemoryUserCreator(),
			Options:            NewMemoryOptionStore(),
			Sessions:           NewMemorySession(),
			Hash:               stubHash,
			Limiter:            l,
			SessionAbsoluteTTL: time.Hour,
			SessionIdleTTL:     time.Minute,
		}
	}

	cases := map[string]func(d *Deps){
		"missing Users":    func(d *Deps) { d.Users = nil },
		"missing Options":  func(d *Deps) { d.Options = nil },
		"missing Sessions": func(d *Deps) { d.Sessions = nil },
		"missing Hash":     func(d *Deps) { d.Hash = nil },
		"missing Limiter":  func(d *Deps) { d.Limiter = nil },
		"zero absolute":    func(d *Deps) { d.SessionAbsoluteTTL = 0 },
		"zero idle":        func(d *Deps) { d.SessionIdleTTL = 0 },
		"idle > absolute":  func(d *Deps) { d.SessionIdleTTL = 2 * d.SessionAbsoluteTTL },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			d := base()
			mutate(&d)
			if _, err := NewHandler(d); err == nil {
				t.Errorf("%s: want error, got nil", name)
			}
		})
	}
}

// ---- Mount ------------------------------------------------------------------

func TestMount_RegistersRoutes(t *testing.T) {
	mux := http.NewServeMux()
	f := newFixture(t)
	if err := Mount(mux, f.d); err != nil {
		t.Fatalf("Mount: %v", err)
	}

	// Hit /status through the mux.
	r := httptest.NewRequest(http.MethodGet, "/api/v1/setup/status", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("Mount didn't register status: %d", w.Code)
	}

	// Hit /install through the mux.
	r = httptest.NewRequest(http.MethodPost, "/api/v1/setup/install",
		bytes.NewReader(jsonMust(t, validBody())))
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = "203.0.113.1:5000"
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("Mount didn't register install: %d, body=%s", w.Code, w.Body)
	}
}

func jsonMust(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// ---- Derived defaults -------------------------------------------------------

func TestDeriveHandle(t *testing.T) {
	cases := map[string]string{
		"admin@example.com":          "admin",
		"Alice.Smith@example.com":    "alice.smith",
		"someone+tag@example.com":    "someone",
		"no-at-sign":                 "no-at-sign", // pathological fallback
	}
	for in, want := range cases {
		got := deriveHandle(in)
		if got != want {
			t.Errorf("deriveHandle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDeriveDisplayName(t *testing.T) {
	if got := deriveDisplayName("admin@example.com"); got != "Admin" {
		t.Errorf("deriveDisplayName: got %q, want Admin", got)
	}
}

func TestLooksLikeEmail(t *testing.T) {
	ok := []string{"a@b.co", "user.name+tag@sub.example.com"}
	bad := []string{"", "@", "a@", "@b.c", "no-at", "a@b", "a b@c.d"}
	for _, s := range ok {
		if !looksLikeEmail(s) {
			t.Errorf("looksLikeEmail(%q): want true", s)
		}
	}
	for _, s := range bad {
		if looksLikeEmail(s) {
			t.Errorf("looksLikeEmail(%q): want false", s)
		}
	}
}
