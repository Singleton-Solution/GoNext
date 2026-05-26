package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

// expectedHashPrefix mirrors what redactedMask produces, so the test
// never asserts on a literal hex string (which would be load-bearing
// magic in the test) — it computes the expected mask from the input.
func expectedHashPrefix(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:8]
}

func expectedMask(s string) string {
	return fmt.Sprintf("***REDACTED*** (len=%d, sha256[:8]=%s)", len(s), expectedHashPrefix(s))
}

// dumpLines runs Dump and returns the map of KEY -> value, parsing each
// "KEY=value" line. Saves test cases from repeating the parse loop.
func dumpLines(t *testing.T, cfg Config) map[string]string {
	t.Helper()
	var buf bytes.Buffer
	if err := Dump(cfg, &buf); err != nil {
		t.Fatalf("Dump: %v", err)
	}
	out := map[string]string{}
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			t.Fatalf("malformed line: %q", line)
		}
		out[line[:eq]] = line[eq+1:]
	}
	return out
}

func TestDump_NonSecretPrintedVerbatim(t *testing.T) {
	cfg := Config{
		Env: EnvProduction,
		Server: ServerConfig{
			Addr:           ":9090",
			MaxHeaderBytes: 1024,
		},
		Log: LogConfig{Level: "INFO", Format: "json"},
	}
	lines := dumpLines(t, cfg)

	if got := lines["Env"]; got != "production" {
		t.Errorf("Env: got %q, want %q", got, "production")
	}
	if got := lines["Server.Addr"]; got != ":9090" {
		t.Errorf("Server.Addr: got %q, want %q", got, ":9090")
	}
	if got := lines["Server.MaxHeaderBytes"]; got != "1024" {
		t.Errorf("Server.MaxHeaderBytes: got %q, want %q", got, "1024")
	}
	if got := lines["Log.Level"]; got != "INFO" {
		t.Errorf("Log.Level: got %q, want %q", got, "INFO")
	}
	if got := lines["Log.Format"]; got != "json" {
		t.Errorf("Log.Format: got %q, want %q", got, "json")
	}
}

func TestDump_RedactTagMaskedRegardlessOfName(t *testing.T) {
	// Auth.Pepper has redact:"true" — value must be masked.
	pepper := strings.Repeat("a", 32)
	cfg := Config{Auth: AuthConfig{Pepper: pepper}}
	lines := dumpLines(t, cfg)

	want := expectedMask(pepper)
	if got := lines["Auth.Pepper"]; got != want {
		t.Errorf("Auth.Pepper: got %q, want %q", got, want)
	}
}

func TestDump_NameContainingPasswordIsRedacted(t *testing.T) {
	// Synthesize a struct field name that hits the fallback name regex
	// (no redact tag) by exercising AccessKey — its name contains "Key"
	// which is a fallback keyword. The tag also covers it, so to actually
	// test the fallback we test via a local synthetic. We do this by
	// renaming through reflection-friendly: just confirm the fallback
	// regex works for "password" via the SecretKey path, which also has
	// redact:"true" — but the rule is OR so name match alone is enough.
	//
	// Cleanest test: directly assert nameRedactPattern hits these.
	for _, name := range []string{"Password", "password", "PASSWORD", "MyPasswordField", "secret", "Token", "APIKey", "Pepper", "DSN", "Dsn"} {
		t.Run(name, func(t *testing.T) {
			if !matchesNameRule(name) {
				t.Errorf("expected name %q to match redact rule", name)
			}
		})
	}
}

func TestDump_NameRuleFallback_NoTagButNameMatches(t *testing.T) {
	// Use a temporary struct purely in-test to prove that the name rule
	// is enough even when no `redact:"true"` tag is present. We dump it
	// through reflection by constructing a Config with one of the secret
	// fields cleared of the tag... but the public Config has all secrets
	// tagged. So we exercise the walk function directly here.
	type fakeCfg struct {
		// No redact tag, but the name should trigger fallback.
		ApiToken string
		// Should pass through.
		Hostname string
	}
	// We can't call Dump(fakeCfg{}, ...) directly because Dump's signature
	// is Config-typed. But the test would be brittle if we changed the
	// signature later. Instead, exercise the walk helper directly.
	val := fakeCfg{ApiToken: "abc123", Hostname: "example.com"}
	var entries []dumpEntry
	walk(reflect.ValueOf(val), reflect.TypeOf(val), "", &entries)

	got := map[string]string{}
	for _, e := range entries {
		got[e.key] = e.value
	}
	want := expectedMask("abc123")
	if got["ApiToken"] != want {
		t.Errorf("ApiToken: got %q, want %q", got["ApiToken"], want)
	}
	if got["Hostname"] != "example.com" {
		t.Errorf("Hostname: got %q, want %q", got["Hostname"], "example.com")
	}
}

func TestDump_DSNFieldRedacted(t *testing.T) {
	cfg := Config{Database: DatabaseConfig{
		URL: "postgres://user:hunter2@db.internal:5432/app?sslmode=require",
	}}
	lines := dumpLines(t, cfg)

	want := expectedMask("postgres://user:hunter2@db.internal:5432/app?sslmode=require")
	if got := lines["Database.URL"]; got != want {
		t.Errorf("Database.URL: got %q, want %q", got, want)
	}
	// Make sure the plaintext password didn't leak elsewhere in the dump.
	for k, v := range lines {
		if strings.Contains(v, "hunter2") {
			t.Errorf("plaintext password leaked into %s=%s", k, v)
		}
	}
}

func TestDump_EmptySecretStillRedactedExplicitly(t *testing.T) {
	// An unset secret should produce a deterministic "(len=0, sha256[:8]=...)"
	// line — never a blank value. The whole point is that an operator
	// reading the dump can distinguish "this was set to something hashing
	// to deadbeef" from "this is unset" without ambiguity.
	cfg := Config{Auth: AuthConfig{Pepper: ""}}
	lines := dumpLines(t, cfg)

	want := expectedMask("")
	if got := lines["Auth.Pepper"]; got != want {
		t.Errorf("empty Auth.Pepper: got %q, want %q", got, want)
	}
	if !strings.Contains(lines["Auth.Pepper"], "len=0") {
		t.Errorf("empty secret must mention len=0 explicitly: got %q", lines["Auth.Pepper"])
	}
}

func TestDump_OutputDeterministic(t *testing.T) {
	cfg := Config{
		Env:    EnvStaging,
		Server: ServerConfig{Addr: ":7070", ReadTimeout: 5 * time.Second},
		Log:    LogConfig{Level: "DEBUG", Format: "text", AddSource: true},
		Auth:   AuthConfig{Pepper: "p", SessionSecret: "s", CSRFSecret: "c"},
	}
	var a, b bytes.Buffer
	if err := Dump(cfg, &a); err != nil {
		t.Fatalf("Dump #1: %v", err)
	}
	if err := Dump(cfg, &b); err != nil {
		t.Fatalf("Dump #2: %v", err)
	}
	if a.String() != b.String() {
		t.Errorf("dump output is non-deterministic between runs:\n#1:\n%s\n#2:\n%s", a.String(), b.String())
	}

	// Also assert the lines are actually sorted (not just stable).
	lines := strings.Split(strings.TrimRight(a.String(), "\n"), "\n")
	keys := make([]string, len(lines))
	for i, l := range lines {
		keys[i] = l[:strings.IndexByte(l, '=')]
	}
	for i := 1; i < len(keys); i++ {
		if keys[i-1] >= keys[i] {
			t.Errorf("keys not sorted ascending: %q then %q (full order: %v)", keys[i-1], keys[i], keys)
			break
		}
	}
}

func TestDump_Golden(t *testing.T) {
	// Representative config with one of every shape: scalar string, int,
	// bool, duration, slice, sub-struct, and every flavor of secret.
	pepper := strings.Repeat("p", 32)
	session := strings.Repeat("s", 32)
	csrf := strings.Repeat("c", 32)
	dbURL := "postgres://u:pw@h:5432/d"
	redisURL := "redis://:pw@h:6379/0"
	accessKey := "AKIAIOSFODNN7EXAMPLE"
	secretKey := "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"

	cfg := Config{
		Env: EnvProduction,
		Server: ServerConfig{
			Addr:              ":8080",
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       15 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       60 * time.Second,
			ShutdownTimeout:   30 * time.Second,
			MaxHeaderBytes:    1 << 20,
			TrustedProxies:    []string{"10.0.0.0/8", "192.168.0.0/16"},
		},
		Log:         LogConfig{Level: "INFO", Format: "json", AddSource: false},
		Database:    DatabaseConfig{URL: dbURL, MaxOpenConns: 25, MaxIdleConns: 5, ConnMaxLifetime: 30 * time.Minute, ConnMaxIdleTime: 5 * time.Minute, StatementTimeout: 30 * time.Second, MigrationDir: "./migrations"},
		Redis:       RedisConfig{URL: redisURL, PoolSize: 20, MinIdleConns: 2, DialTimeout: 5 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second},
		Storage:     StorageConfig{Endpoint: "", Region: "us-east-1", Bucket: "media", AccessKey: accessKey, SecretKey: secretKey, UseSSL: true, PathStyle: false},
		Auth:        AuthConfig{Pepper: pepper, SessionSecret: session, CSRFSecret: csrf, SessionTTL: 30 * 24 * time.Hour, SessionIdleTTL: 7 * 24 * time.Hour},
		Plugins:     PluginsConfig{DevMode: false, DevToken: ""},
		Performance: PerformanceConfig{EarlyHints: true},
		RUM:         RUMConfig{Enabled: false, SampleRate: 1.0},
		Email: EmailConfig{
			Provider: "smtp", Host: "smtp.example.com", Port: 587,
			Username: "u", Password: "smtp-secret",
			From: "noreply@example.com", TLS: false, AuthMech: "plain",
			InsecureSkipVerify: false, DialTimeout: 10 * time.Second,
			BrandName: "GoNext", BrandColor: "#2563eb",
			SiteURL: "https://example.com/", SupportEmail: "help@example.com",
		},
		PublicSite: PublicSiteConfig{
			BaseURL:    "https://example.com",
			AllowIndex: true,
		},
	}

	want := strings.Join([]string{
		"Auth.CSRFSecret=" + expectedMask(csrf),
		"Auth.Pepper=" + expectedMask(pepper),
		"Auth.SessionIdleTTL=168h0m0s",
		"Auth.SessionSecret=" + expectedMask(session),
		"Auth.SessionTTL=720h0m0s",
		"Database.ConnMaxIdleTime=5m0s",
		"Database.ConnMaxLifetime=30m0s",
		"Database.MaxIdleConns=5",
		"Database.MaxOpenConns=25",
		"Database.MigrationDir=./migrations",
		"Database.StatementTimeout=30s",
		"Database.URL=" + expectedMask(dbURL),
		"Email.AuthMech=plain",
		"Email.BrandColor=#2563eb",
		"Email.BrandName=GoNext",
		"Email.DialTimeout=10s",
		"Email.From=noreply@example.com",
		"Email.Host=smtp.example.com",
		"Email.InsecureSkipVerify=false",
		"Email.Password=" + expectedMask("smtp-secret"),
		"Email.Port=587",
		"Email.Provider=smtp",
		"Email.SiteURL=https://example.com/",
		"Email.SupportEmail=help@example.com",
		"Email.TLS=false",
		"Email.Username=u",
		"Env=production",
		"Log.AddSource=false",
		"Log.Format=json",
		"Log.Level=INFO",
		"Performance.EarlyHints=true",
		"Plugins.DevMode=false",
		"Plugins.DevToken=" + expectedMask(""),
		"PublicSite.AllowIndex=true",
		"PublicSite.BaseURL=https://example.com",
		"PublicSite.NextRevalidateSecret=" + expectedMask(""),
		"PublicSite.NextRevalidateURL=",
		"RUM.Enabled=false",
		"RUM.SampleRate=1",
		"Redis.DialTimeout=5s",
		"Redis.MinIdleConns=2",
		"Redis.PoolSize=20",
		"Redis.ReadTimeout=3s",
		"Redis.URL=" + expectedMask(redisURL),
		"Redis.WriteTimeout=3s",
		"Server.Addr=:8080",
		"Server.IdleTimeout=1m0s",
		"Server.MaxHeaderBytes=1048576",
		"Server.ReadHeaderTimeout=5s",
		"Server.ReadTimeout=15s",
		"Server.ShutdownTimeout=30s",
		"Server.TrustedProxies=[10.0.0.0/8,192.168.0.0/16]",
		"Server.WriteTimeout=30s",
		"Storage.AccessKey=" + expectedMask(accessKey),
		"Storage.Bucket=media",
		"Storage.Endpoint=",
		"Storage.PathStyle=false",
		"Storage.Region=us-east-1",
		"Storage.SecretKey=" + expectedMask(secretKey),
		"Storage.UseSSL=true",
	}, "\n") + "\n"

	var buf bytes.Buffer
	if err := Dump(cfg, &buf); err != nil {
		t.Fatalf("Dump: %v", err)
	}
	if buf.String() != want {
		// Show a unified-style diff to make failures debuggable.
		gotLines := strings.Split(buf.String(), "\n")
		wantLines := strings.Split(want, "\n")
		t.Errorf("golden mismatch.\n--- got (%d lines)\n+++ want (%d lines)\n", len(gotLines), len(wantLines))
		max := len(gotLines)
		if len(wantLines) > max {
			max = len(wantLines)
		}
		for i := 0; i < max; i++ {
			var g, w string
			if i < len(gotLines) {
				g = gotLines[i]
			}
			if i < len(wantLines) {
				w = wantLines[i]
			}
			if g != w {
				t.Errorf("  line %d:\n    got:  %q\n    want: %q", i+1, g, w)
			}
		}
	}
}

func TestDump_NoPlaintextSecretsInOutput(t *testing.T) {
	// Negative assertion: after redaction, the dump must not contain any
	// of the raw secrets anywhere — not in the redacted line, not in any
	// other line. This guards against future fields that accidentally
	// embed a secret without their own redact tag.
	cfg := Config{
		Database: DatabaseConfig{URL: "postgres://u:supersecret@h/d"},
		Redis:    RedisConfig{URL: "redis://:redispw@h/0"},
		Storage:  StorageConfig{AccessKey: "AKIASECRETACCESS", SecretKey: "s3cretBytesHere"},
		Auth:     AuthConfig{Pepper: "pepper-bytes", SessionSecret: "session-bytes", CSRFSecret: "csrf-bytes"},
	}
	var buf bytes.Buffer
	if err := Dump(cfg, &buf); err != nil {
		t.Fatalf("Dump: %v", err)
	}
	for _, plaintext := range []string{"supersecret", "redispw", "AKIASECRETACCESS", "s3cretBytesHere", "pepper-bytes", "session-bytes", "csrf-bytes"} {
		if strings.Contains(buf.String(), plaintext) {
			t.Errorf("plaintext %q leaked into dump output:\n%s", plaintext, buf.String())
		}
	}
}
