package config

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestLoad_EmailDefaults(t *testing.T) {
	cfg, err := Load(WithEnv(fixture()))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Email.Provider != "noop" {
		t.Errorf("Provider default: got %q want noop", cfg.Email.Provider)
	}
	if cfg.Email.Port != 587 {
		t.Errorf("Port default: got %d want 587", cfg.Email.Port)
	}
	if cfg.Email.AuthMech != "plain" {
		t.Errorf("AuthMech default: got %q want plain", cfg.Email.AuthMech)
	}
	if cfg.Email.DialTimeout != 10*time.Second {
		t.Errorf("DialTimeout default: got %v want 10s", cfg.Email.DialTimeout)
	}
	if cfg.Email.BrandName == "" || cfg.Email.BrandColor == "" {
		t.Errorf("expected brand defaults, got %q / %q", cfg.Email.BrandName, cfg.Email.BrandColor)
	}
}

func TestLoad_EmailSMTPFull(t *testing.T) {
	cfg, err := Load(WithEnv(fixture(map[string]string{
		"GONEXT_EMAIL_PROVIDER":   "smtp",
		"GONEXT_EMAIL_HOST":       "smtp.example.com",
		"GONEXT_EMAIL_PORT":       "465",
		"GONEXT_EMAIL_USERNAME":   "u",
		"GONEXT_EMAIL_PASSWORD":   "p",
		"GONEXT_EMAIL_FROM":       "noreply@example.com",
		"GONEXT_EMAIL_TLS":        "true",
		"GONEXT_EMAIL_AUTH_MECH":  "login",
		"GONEXT_EMAIL_BRAND_NAME": "ExampleCo",
		"GONEXT_EMAIL_SITE_URL":   "https://example.com/",
		"GONEXT_EMAIL_SUPPORT":    "help@example.com",
	})))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := cfg.Email
	wantEqual := []struct {
		name string
		got  any
		want any
	}{
		{"Provider", got.Provider, "smtp"},
		{"Host", got.Host, "smtp.example.com"},
		{"Port", got.Port, 465},
		{"Username", got.Username, "u"},
		{"Password", got.Password, "p"},
		{"From", got.From, "noreply@example.com"},
		{"TLS", got.TLS, true},
		{"AuthMech", got.AuthMech, "login"},
		{"BrandName", got.BrandName, "ExampleCo"},
		{"SiteURL", got.SiteURL, "https://example.com/"},
		{"SupportEmail", got.SupportEmail, "help@example.com"},
	}
	for _, c := range wantEqual {
		if c.got != c.want {
			t.Errorf("%s: got %v want %v", c.name, c.got, c.want)
		}
	}
}

func TestLoad_EmailRejectsBadProvider(t *testing.T) {
	_, err := Load(WithEnv(fixture(map[string]string{
		"GONEXT_EMAIL_PROVIDER": "carrier-pigeon",
	})))
	if err == nil {
		t.Fatal("expected error for bogus provider")
	}
	if !strings.Contains(err.Error(), "GONEXT_EMAIL_PROVIDER") {
		t.Errorf("error did not mention the offending key: %v", err)
	}
}

func TestLoad_EmailRejectsBadMech(t *testing.T) {
	_, err := Load(WithEnv(fixture(map[string]string{
		"GONEXT_EMAIL_AUTH_MECH": "fancy-sasl",
	})))
	if err == nil {
		t.Fatal("expected error for bogus mech")
	}
	if !strings.Contains(err.Error(), "GONEXT_EMAIL_AUTH_MECH") {
		t.Errorf("error did not mention the offending key: %v", err)
	}
}

func TestLoad_SMTPProvider_RequiresHostAndFrom(t *testing.T) {
	_, err := Load(WithEnv(fixture(map[string]string{
		"GONEXT_EMAIL_PROVIDER": "smtp",
	})))
	if err == nil {
		t.Fatal("expected error: smtp provider without host/from")
	}
	if !strings.Contains(err.Error(), "GONEXT_EMAIL_HOST") {
		t.Errorf("expected host requirement in error: %v", err)
	}
	if !strings.Contains(err.Error(), "GONEXT_EMAIL_FROM") {
		t.Errorf("expected from requirement in error: %v", err)
	}
}

func TestLoad_SMTPProvider_UserWithoutPassword(t *testing.T) {
	_, err := Load(WithEnv(fixture(map[string]string{
		"GONEXT_EMAIL_PROVIDER": "smtp",
		"GONEXT_EMAIL_HOST":     "smtp.example.com",
		"GONEXT_EMAIL_FROM":     "x@y.test",
		"GONEXT_EMAIL_USERNAME": "u",
	})))
	if err == nil {
		t.Fatal("expected error: USERNAME without PASSWORD")
	}
	var _ = errors.New // keep errors import for future expansion
}

func TestLoad_EmailLegacyEnvFallback(t *testing.T) {
	// GONEXT_EMAIL_* takes priority but the legacy GONEXT_SMTP_* names
	// continue to work for deployments still on the early bootstrap
	// shape (issue #74 wired those before the wider Email config
	// existed).
	cfg, err := Load(WithEnv(fixture(map[string]string{
		"GONEXT_EMAIL_PROVIDER": "smtp",
		"GONEXT_SMTP_HOST":      "legacy.smtp.test",
		"GONEXT_SMTP_PORT":      "2525",
		"GONEXT_SMTP_USER":      "u",
		"GONEXT_SMTP_PASSWORD":  "p",
		"GONEXT_SMTP_FROM":      "legacy@x.test",
	})))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Email.Host != "legacy.smtp.test" {
		t.Errorf("Host fallback: got %q want legacy.smtp.test", cfg.Email.Host)
	}
	if cfg.Email.Port != 2525 {
		t.Errorf("Port fallback: got %d want 2525", cfg.Email.Port)
	}
	if cfg.Email.From != "legacy@x.test" {
		t.Errorf("From fallback: got %q want legacy@x.test", cfg.Email.From)
	}
}

// TestDump_RedactsEmailPassword confirms the SMTP password is masked
// in dumps via the redact:"true" tag — operators reviewing a config
// dump must not see the real secret.
func TestDump_RedactsEmailPassword(t *testing.T) {
	cfg, err := Load(WithEnv(fixture(map[string]string{
		"GONEXT_EMAIL_PROVIDER": "smtp",
		"GONEXT_EMAIL_HOST":     "smtp.example.com",
		"GONEXT_EMAIL_USERNAME": "u",
		"GONEXT_EMAIL_PASSWORD": "s3cretValueRedactMe",
		"GONEXT_EMAIL_FROM":     "x@y.test",
	})))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var buf bytes.Buffer
	if err := Dump(*cfg, &buf); err != nil {
		t.Fatalf("Dump: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "s3cretValueRedactMe") {
		t.Errorf("dump leaked SMTP password:\n%s", out)
	}
	if !strings.Contains(out, "Email.Password=***REDACTED***") {
		t.Errorf("expected Email.Password redaction line:\n%s", out)
	}
	// Operators DO need to see the From and Host in the dump.
	if !strings.Contains(out, "Email.From=x@y.test") {
		t.Errorf("expected Email.From in dump:\n%s", out)
	}
	if !strings.Contains(out, "Email.Host=smtp.example.com") {
		t.Errorf("expected Email.Host in dump:\n%s", out)
	}
}
