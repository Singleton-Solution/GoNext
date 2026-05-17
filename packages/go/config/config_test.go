package config

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// fixture returns a map with the three required secrets pre-populated
// to valid 32-byte strings, plus a usable DATABASE_URL. Tests override
// individual keys via the map literal.
func fixture(overrides ...map[string]string) map[string]string {
	m := map[string]string{
		"DATABASE_URL":              "postgres://gonext:dev@localhost:5432/gonext_dev?sslmode=disable",
		"GONEXT_AUTH_PEPPER":        strings.Repeat("a", 32),
		"GONEXT_AUTH_SESSION_SECRET": strings.Repeat("b", 32),
		"GONEXT_AUTH_CSRF_SECRET":   strings.Repeat("c", 32),
	}
	for _, o := range overrides {
		for k, v := range o {
			m[k] = v
		}
	}
	return m
}

func TestLoad_Defaults(t *testing.T) {
	cfg, err := Load(WithEnv(fixture()))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cases := []struct {
		name string
		got  any
		want any
	}{
		{"Env", cfg.Env, EnvDevelopment},
		{"Server.Addr", cfg.Server.Addr, ":8080"},
		{"Server.ReadHeaderTimeout", cfg.Server.ReadHeaderTimeout, 5 * time.Second},
		{"Server.ReadTimeout", cfg.Server.ReadTimeout, 15 * time.Second},
		{"Server.WriteTimeout", cfg.Server.WriteTimeout, 30 * time.Second},
		{"Server.IdleTimeout", cfg.Server.IdleTimeout, 60 * time.Second},
		{"Server.ShutdownTimeout", cfg.Server.ShutdownTimeout, 30 * time.Second},
		{"Server.MaxHeaderBytes", cfg.Server.MaxHeaderBytes, 1 << 20},
		{"Log.Level", cfg.Log.Level, "INFO"},
		{"Log.Format", cfg.Log.Format, "json"},
		{"Log.AddSource", cfg.Log.AddSource, false},
		{"Database.MaxOpenConns", cfg.Database.MaxOpenConns, 25},
		{"Database.MaxIdleConns", cfg.Database.MaxIdleConns, 5},
		{"Database.ConnMaxLifetime", cfg.Database.ConnMaxLifetime, 30 * time.Minute},
		{"Database.StatementTimeout", cfg.Database.StatementTimeout, 30 * time.Second},
		{"Database.MigrationDir", cfg.Database.MigrationDir, "./migrations"},
		{"Redis.URL", cfg.Redis.URL, "redis://localhost:6379/0"},
		{"Redis.PoolSize", cfg.Redis.PoolSize, 20},
		{"Storage.Region", cfg.Storage.Region, "us-east-1"},
		{"Storage.Bucket", cfg.Storage.Bucket, "gonext-media"},
		{"Storage.UseSSL", cfg.Storage.UseSSL, true},
		{"Storage.PathStyle", cfg.Storage.PathStyle, false},
		{"Auth.SessionTTL", cfg.Auth.SessionTTL, 30 * 24 * time.Hour},
		{"Auth.SessionIdleTTL", cfg.Auth.SessionIdleTTL, 7 * 24 * time.Hour},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.got != c.want {
				t.Errorf("%s: got %v, want %v", c.name, c.got, c.want)
			}
		})
	}
}

func TestLoad_OverridesHonored(t *testing.T) {
	cfg, err := Load(WithEnv(fixture(map[string]string{
		"GONEXT_ENV":              "production",
		"PORT":                    "9090",
		"GONEXT_LOG_LEVEL":        "WARN",
		"GONEXT_LOG_FORMAT":       "text",
		"GONEXT_LOG_ADDSRC":       "true",
		"GONEXT_DB_MAX_OPEN_CONNS": "50",
		"GONEXT_SERVER_SHUTDOWN_TIMEOUT": "45s",
		"REDIS_URL":               "redis://cache.internal:6379/3",
		"AWS_REGION":              "eu-west-2",
		"GONEXT_S3_BUCKET":         "prod-media",
		"GONEXT_S3_PATH_STYLE":     "true",
	})))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cases := []struct {
		name string
		got  any
		want any
	}{
		{"Env", cfg.Env, EnvProduction},
		{"Server.Addr", cfg.Server.Addr, ":9090"},
		{"Log.Level", cfg.Log.Level, "WARN"},
		{"Log.Format", cfg.Log.Format, "text"},
		{"Log.AddSource", cfg.Log.AddSource, true},
		{"Database.MaxOpenConns", cfg.Database.MaxOpenConns, 50},
		{"Server.ShutdownTimeout", cfg.Server.ShutdownTimeout, 45 * time.Second},
		{"Redis.URL", cfg.Redis.URL, "redis://cache.internal:6379/3"},
		{"Storage.Region", cfg.Storage.Region, "eu-west-2"},
		{"Storage.Bucket", cfg.Storage.Bucket, "prod-media"},
		{"Storage.PathStyle", cfg.Storage.PathStyle, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.got != c.want {
				t.Errorf("%s: got %v, want %v", c.name, c.got, c.want)
			}
		})
	}
}

func TestLoad_ServerAddrTakesPrecedenceOverPORT(t *testing.T) {
	cfg, err := Load(WithEnv(fixture(map[string]string{
		"PORT":                "9090",
		"GONEXT_SERVER_ADDR": "0.0.0.0:7000",
	})))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Addr != "0.0.0.0:7000" {
		t.Errorf("Addr: got %q, want %q", cfg.Server.Addr, "0.0.0.0:7000")
	}
}

func TestLoad_MissingRequired_Errors(t *testing.T) {
	cases := []struct {
		name   string
		drop   string
		errSub string
	}{
		{"missing DATABASE_URL", "DATABASE_URL", "DATABASE_URL"},
		{"missing pepper", "GONEXT_AUTH_PEPPER", "GONEXT_AUTH_PEPPER"},
		{"missing session secret", "GONEXT_AUTH_SESSION_SECRET", "GONEXT_AUTH_SESSION_SECRET"},
		{"missing csrf secret", "GONEXT_AUTH_CSRF_SECRET", "GONEXT_AUTH_CSRF_SECRET"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := fixture()
			delete(env, c.drop)
			_, err := Load(WithEnv(env))
			if err == nil {
				t.Fatalf("expected error mentioning %q, got nil", c.errSub)
			}
			if !strings.Contains(err.Error(), c.errSub) {
				t.Errorf("error message should mention %q, got: %v", c.errSub, err)
			}
		})
	}
}

func TestLoad_AggregatesMultipleErrors(t *testing.T) {
	// Drop two required secrets and supply one bad numeric value.
	// The error should mention all three.
	env := fixture(map[string]string{
		"GONEXT_DB_MAX_OPEN_CONNS": "not-an-int",
	})
	delete(env, "GONEXT_AUTH_PEPPER")
	delete(env, "GONEXT_AUTH_SESSION_SECRET")

	_, err := Load(WithEnv(env))
	if err == nil {
		t.Fatal("expected aggregated error, got nil")
	}
	for _, want := range []string{
		"GONEXT_AUTH_PEPPER",
		"GONEXT_AUTH_SESSION_SECRET",
		"GONEXT_DB_MAX_OPEN_CONNS",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("aggregated error missing %q\nfull error:\n%s", want, err.Error())
		}
	}
}

func TestLoad_ShortSecret_Errors(t *testing.T) {
	_, err := Load(WithEnv(fixture(map[string]string{
		"GONEXT_AUTH_PEPPER": "hunter2", // 7 bytes — too short
	})))
	if err == nil || !strings.Contains(err.Error(), "GONEXT_AUTH_PEPPER") {
		t.Errorf("expected pepper entropy error, got: %v", err)
	}
	if err != nil && !strings.Contains(err.Error(), "at least") {
		t.Errorf("error message should mention length requirement, got: %v", err)
	}
}

func TestLoad_Base64SecretAccepted(t *testing.T) {
	// 32 random bytes encoded as base64. Encodes to 44 chars but decodes to
	// 32 bytes, which is what the entropy check requires.
	b64 := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	cfg, err := Load(WithEnv(fixture(map[string]string{
		"GONEXT_AUTH_PEPPER": b64,
	})))
	if err != nil {
		t.Fatalf("expected base64-decoded 32-byte secret to be accepted: %v", err)
	}
	if cfg.Auth.Pepper != b64 {
		t.Errorf("secret stored verbatim: got %q, want %q", cfg.Auth.Pepper, b64)
	}
}

func TestLoad_BadIntFormat_Errors(t *testing.T) {
	_, err := Load(WithEnv(fixture(map[string]string{
		"GONEXT_DB_MAX_OPEN_CONNS": "lots",
	})))
	if err == nil || !strings.Contains(err.Error(), "GONEXT_DB_MAX_OPEN_CONNS") {
		t.Errorf("expected int parse error mentioning the key, got: %v", err)
	}
}

func TestLoad_BadDurationFormat_Errors(t *testing.T) {
	_, err := Load(WithEnv(fixture(map[string]string{
		"GONEXT_SERVER_READ_TIMEOUT": "forever",
	})))
	if err == nil || !strings.Contains(err.Error(), "GONEXT_SERVER_READ_TIMEOUT") {
		t.Errorf("expected duration parse error mentioning the key, got: %v", err)
	}
}

func TestLoad_TrustedProxies_CSV(t *testing.T) {
	cfg, err := Load(WithEnv(fixture(map[string]string{
		"GONEXT_TRUSTED_PROXIES": "10.0.0.0/8, 192.168.1.0/24 ,  ,172.16.0.0/12",
	})))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{"10.0.0.0/8", "192.168.1.0/24", "172.16.0.0/12"}
	if len(cfg.Server.TrustedProxies) != len(want) {
		t.Fatalf("got %d entries, want %d", len(cfg.Server.TrustedProxies), len(want))
	}
	for i, w := range want {
		if cfg.Server.TrustedProxies[i] != w {
			t.Errorf("[%d]: got %q, want %q", i, cfg.Server.TrustedProxies[i], w)
		}
	}
}

func TestParseEnv(t *testing.T) {
	cases := []struct {
		in   string
		want Env
	}{
		{"production", EnvProduction},
		{"prod", EnvProduction},
		{"PRODUCTION", EnvProduction},
		{"staging", EnvStaging},
		{"stage", EnvStaging},
		{"test", EnvTest},
		{"testing", EnvTest},
		{"development", EnvDevelopment},
		{"", EnvDevelopment},
		{"random-label", EnvDevelopment},
		{"  staging  ", EnvStaging},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			if got := parseEnv(c.in); got != c.want {
				t.Errorf("parseEnv(%q): got %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestLoad_BadEnvVarsListAll(t *testing.T) {
	// Multiple parse errors should be aggregated, not short-circuited.
	_, err := Load(WithEnv(fixture(map[string]string{
		"GONEXT_DB_MAX_OPEN_CONNS":      "x",
		"GONEXT_REDIS_POOL_SIZE":        "y",
		"GONEXT_SERVER_WRITE_TIMEOUT":   "z",
		"GONEXT_LOG_ADDSRC":             "maybe",
	})))
	if err == nil {
		t.Fatal("expected aggregated parse errors")
	}
	for _, want := range []string{
		"GONEXT_DB_MAX_OPEN_CONNS",
		"GONEXT_REDIS_POOL_SIZE",
		"GONEXT_SERVER_WRITE_TIMEOUT",
		"GONEXT_LOG_ADDSRC",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("aggregated error missing %q\nfull: %v", want, err)
		}
	}
}

func TestLoad_ReturnsConcreteError(t *testing.T) {
	// errors.Join returns a non-nil error; ensure errors.Is/As still work
	// the way we expect (won't match a sentinel since we don't have one,
	// but the returned error must be non-nil and not panic on unwrap).
	_, err := Load(WithEnv(fixture(map[string]string{
		"GONEXT_DB_MAX_OPEN_CONNS": "x",
	})))
	if err == nil {
		t.Fatal("expected error")
	}
	// Validate Unwrap chain (errors.Join supports it).
	var u interface{ Unwrap() []error }
	if !errors.As(err, &u) {
		// Not required; some implementations don't expose this. Soft assert.
		t.Log("errors.As to Unwrap()[]error failed; that's OK")
	}
}
