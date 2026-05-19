package verify

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/audit"
	"github.com/Singleton-Solution/GoNext/packages/go/email"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
	"github.com/Singleton-Solution/GoNext/packages/go/ratelimit"
)

// TestHandleSend_UsesTemplatesWhenWired confirms a Handler constructed
// with Options.Templates renders the verification email from the
// shared template pair (not the fallback) and stamps the
// "template=verify-email" tag.
func TestHandleSend_UsesTemplatesWhenWired(t *testing.T) {
	clk := newClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	tokens := newMemTokenStore(clk.Now)
	users := newMemUser("user-1", "alice@example.com")
	sender := email.NewNoopSender()

	limiter, err := ratelimit.NewMemoryLimiter(ratelimit.Policy{
		Capacity: 1, RefillRate: 1.0 / 60.0,
	})
	if err != nil {
		t.Fatalf("limiter: %v", err)
	}

	tpl, err := email.DefaultTemplates()
	if err != nil {
		t.Fatalf("DefaultTemplates: %v", err)
	}
	emitter := audit.NewEmitter(audit.NewMemoryStore())

	h, err := New(Options{
		Tokens:      tokens,
		Users:       users,
		Sender:      sender,
		Limiter:     limiter,
		Audit:       emitter,
		Templates:   tpl,
		Brand:       email.BrandContext{SiteName: "ChassisTest", SiteURL: "https://chassis.test/", SiteBrandColor: "#aa00ff"},
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
	h.Routes(mux, fakeRequireSession(policy.Principal{UserID: "user-1"}))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/v1/auth/verify/send", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status: got %d want 202", resp.StatusCode)
	}

	msg, ok := sender.Last()
	if !ok {
		t.Fatal("sender saw no message")
	}
	// Brand color stamped into HTML body via the template.
	if !strings.Contains(msg.HTMLBody, "#aa00ff") {
		t.Errorf("html missing brand color:\n%s", msg.HTMLBody)
	}
	// Site name from the template footer.
	if !strings.Contains(msg.HTMLBody, "ChassisTest") {
		t.Errorf("html missing site name:\n%s", msg.HTMLBody)
	}
	if !strings.Contains(msg.TextBody, "ChassisTest") {
		t.Errorf("text missing site name:\n%s", msg.TextBody)
	}
	// Tags carry the template + flow labels for audit downstream.
	if msg.Tags["template"] != "verify-email" {
		t.Errorf("template tag: got %q want verify-email", msg.Tags["template"])
	}
	if msg.Tags["flow"] != "auth.verify.email" {
		t.Errorf("flow tag: got %q want auth.verify.email", msg.Tags["flow"])
	}
	// And the actual token-bearing URL still survives the substitution.
	if !strings.Contains(msg.HTMLBody, "https://chassis.test/verify?token=") {
		t.Errorf("html missing verify URL:\n%s", msg.HTMLBody)
	}
	if !strings.Contains(msg.TextBody, "https://chassis.test/verify?token=") {
		t.Errorf("text missing verify URL:\n%s", msg.TextBody)
	}
}

func TestFormatTTL(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{0, "24 hours"},
		{time.Hour, "1 hour"},
		{24 * time.Hour, "24 hours"},
		{30 * time.Minute, "30m0s"},
	}
	for _, c := range cases {
		if got := formatTTL(c.in); got != c.want {
			t.Errorf("formatTTL(%v) = %q; want %q", c.in, got, c.want)
		}
	}
}
