package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
)

// LoadOption is a functional option to Load.
type LoadOption func(*loadConfig)

type loadConfig struct {
	env envSource
}

// WithEnv overrides the environment source. Tests use this to inject a
// fixture map without touching os.Environ. Production code should not
// call this.
func WithEnv(m map[string]string) LoadOption {
	return func(lc *loadConfig) { lc.env = mapEnv(m) }
}

// Load builds a Config from the process environment.
//
// On error, the returned *Config is partial (best-effort populated) but
// callers should not use it — exit instead. The error message lists every
// missing or invalid key in one batch so operators don't have to fix
// the same misconfiguration five times.
func Load(opts ...LoadOption) (*Config, error) {
	lc := loadConfig{env: osEnv{}}
	for _, o := range opts {
		o(&lc)
	}
	e := lc.env

	cfg := &Config{}
	var errs []error

	// ---- Env ----
	cfg.Env = parseEnv(getString(e, "GONEXT_ENV", "development"))

	// ---- Server ----
	addr := getString(e, "GONEXT_SERVER_ADDR", "")
	if addr == "" {
		// PORT shorthand (Heroku/Railway/Fly convention).
		if port := getString(e, "PORT", ""); port != "" {
			addr = ":" + port
		} else {
			addr = ":8080"
		}
	}
	cfg.Server.Addr = addr

	if d, err := getDuration(e, "GONEXT_SERVER_READ_HEADER_TIMEOUT", 5*time.Second); err != nil {
		errs = append(errs, err)
	} else {
		cfg.Server.ReadHeaderTimeout = d
	}
	if d, err := getDuration(e, "GONEXT_SERVER_READ_TIMEOUT", 15*time.Second); err != nil {
		errs = append(errs, err)
	} else {
		cfg.Server.ReadTimeout = d
	}
	if d, err := getDuration(e, "GONEXT_SERVER_WRITE_TIMEOUT", 30*time.Second); err != nil {
		errs = append(errs, err)
	} else {
		cfg.Server.WriteTimeout = d
	}
	if d, err := getDuration(e, "GONEXT_SERVER_IDLE_TIMEOUT", 60*time.Second); err != nil {
		errs = append(errs, err)
	} else {
		cfg.Server.IdleTimeout = d
	}
	if d, err := getDuration(e, "GONEXT_SERVER_SHUTDOWN_TIMEOUT", 30*time.Second); err != nil {
		errs = append(errs, err)
	} else {
		cfg.Server.ShutdownTimeout = d
	}
	if n, err := getInt(e, "GONEXT_SERVER_MAX_HEADER_BYTES", 1<<20); err != nil {
		errs = append(errs, err)
	} else {
		cfg.Server.MaxHeaderBytes = n
	}
	cfg.Server.TrustedProxies = getCSV(e, "GONEXT_TRUSTED_PROXIES", nil)

	// ---- Log ----
	cfg.Log.Level = getString(e, "GONEXT_LOG_LEVEL", "INFO")
	cfg.Log.Format = getString(e, "GONEXT_LOG_FORMAT", "json")
	if b, err := getBool(e, "GONEXT_LOG_ADDSRC", false); err != nil {
		errs = append(errs, err)
	} else {
		cfg.Log.AddSource = b
	}

	// ---- Database ----
	if url, err := getStringRequired(e, "DATABASE_URL"); err != nil {
		errs = append(errs, err)
	} else {
		cfg.Database.URL = url
	}
	if n, err := getInt(e, "GONEXT_DB_MAX_OPEN_CONNS", 25); err != nil {
		errs = append(errs, err)
	} else {
		cfg.Database.MaxOpenConns = n
	}
	if n, err := getInt(e, "GONEXT_DB_MAX_IDLE_CONNS", 5); err != nil {
		errs = append(errs, err)
	} else {
		cfg.Database.MaxIdleConns = n
	}
	if d, err := getDuration(e, "GONEXT_DB_CONN_MAX_LIFETIME", 30*time.Minute); err != nil {
		errs = append(errs, err)
	} else {
		cfg.Database.ConnMaxLifetime = d
	}
	if d, err := getDuration(e, "GONEXT_DB_CONN_MAX_IDLE_TIME", 5*time.Minute); err != nil {
		errs = append(errs, err)
	} else {
		cfg.Database.ConnMaxIdleTime = d
	}
	if d, err := getDuration(e, "GONEXT_DB_STATEMENT_TIMEOUT", 30*time.Second); err != nil {
		errs = append(errs, err)
	} else {
		cfg.Database.StatementTimeout = d
	}
	cfg.Database.MigrationDir = getString(e, "GONEXT_MIGRATION_DIR", "./migrations")

	// ---- Redis ----
	cfg.Redis.URL = getString(e, "REDIS_URL", "redis://localhost:6379/0")
	if n, err := getInt(e, "GONEXT_REDIS_POOL_SIZE", 20); err != nil {
		errs = append(errs, err)
	} else {
		cfg.Redis.PoolSize = n
	}
	if n, err := getInt(e, "GONEXT_REDIS_MIN_IDLE_CONNS", 2); err != nil {
		errs = append(errs, err)
	} else {
		cfg.Redis.MinIdleConns = n
	}
	if d, err := getDuration(e, "GONEXT_REDIS_DIAL_TIMEOUT", 5*time.Second); err != nil {
		errs = append(errs, err)
	} else {
		cfg.Redis.DialTimeout = d
	}
	if d, err := getDuration(e, "GONEXT_REDIS_READ_TIMEOUT", 3*time.Second); err != nil {
		errs = append(errs, err)
	} else {
		cfg.Redis.ReadTimeout = d
	}
	if d, err := getDuration(e, "GONEXT_REDIS_WRITE_TIMEOUT", 3*time.Second); err != nil {
		errs = append(errs, err)
	} else {
		cfg.Redis.WriteTimeout = d
	}

	// ---- Storage (S3) ----
	cfg.Storage.Endpoint = getString(e, "AWS_ENDPOINT_URL", "")
	cfg.Storage.Region = getString(e, "AWS_REGION", "us-east-1")
	cfg.Storage.Bucket = getString(e, "GONEXT_S3_BUCKET", "gonext-media")
	cfg.Storage.AccessKey = getString(e, "AWS_ACCESS_KEY_ID", "")
	cfg.Storage.SecretKey = getString(e, "AWS_SECRET_ACCESS_KEY", "")
	if b, err := getBool(e, "GONEXT_S3_USE_SSL", true); err != nil {
		errs = append(errs, err)
	} else {
		cfg.Storage.UseSSL = b
	}
	if b, err := getBool(e, "GONEXT_S3_PATH_STYLE", false); err != nil {
		errs = append(errs, err)
	} else {
		cfg.Storage.PathStyle = b
	}
	// Sanity: if a custom endpoint is set, path-style is almost certainly
	// required (MinIO, LocalStack). Warn-by-default in the Env summary, not here.

	// ---- Auth ----
	if v, err := getStringRequired(e, "GONEXT_AUTH_PEPPER"); err != nil {
		errs = append(errs, err)
	} else if err := validateSecretEntropy("GONEXT_AUTH_PEPPER", v); err != nil {
		errs = append(errs, err)
	} else {
		cfg.Auth.Pepper = v
	}
	if v, err := getStringRequired(e, "GONEXT_AUTH_SESSION_SECRET"); err != nil {
		errs = append(errs, err)
	} else if err := validateSecretEntropy("GONEXT_AUTH_SESSION_SECRET", v); err != nil {
		errs = append(errs, err)
	} else {
		cfg.Auth.SessionSecret = v
	}
	if v, err := getStringRequired(e, "GONEXT_AUTH_CSRF_SECRET"); err != nil {
		errs = append(errs, err)
	} else if err := validateSecretEntropy("GONEXT_AUTH_CSRF_SECRET", v); err != nil {
		errs = append(errs, err)
	} else {
		cfg.Auth.CSRFSecret = v
	}
	if d, err := getDuration(e, "GONEXT_AUTH_SESSION_TTL", 30*24*time.Hour); err != nil {
		errs = append(errs, err)
	} else {
		cfg.Auth.SessionTTL = d
	}
	if d, err := getDuration(e, "GONEXT_AUTH_SESSION_IDLE_TTL", 7*24*time.Hour); err != nil {
		errs = append(errs, err)
	} else {
		cfg.Auth.SessionIdleTTL = d
	}

	// ---- Plugins ----
	// DevMode defaults to false: production deployments must NOT have to
	// opt out of the dev-install surface. DevToken has no default — an
	// empty token is meaningful (handler rejects every request).
	if b, err := getBool(e, "GONEXT_PLUGINS_DEV_MODE", false); err != nil {
		errs = append(errs, err)
	} else {
		cfg.Plugins.DevMode = b
	}
	cfg.Plugins.DevToken = getString(e, "GONEXT_PLUGINS_DEV_TOKEN", "")

	// ---- Performance ----
	// EarlyHints defaults to true: the 103 path is a pure win for the
	// vast majority of deployments and operators can opt out with a
	// single env var if an upstream proxy mishandles 1xx.
	if b, err := getBool(e, "GONEXT_PERFORMANCE_EARLY_HINTS", true); err != nil {
		errs = append(errs, err)
	} else {
		cfg.Performance.EarlyHints = b
	}

	if len(errs) > 0 {
		return cfg, joinErrs(errs)
	}
	return cfg, nil
}

// parseEnv maps the string form to Env, defaulting to development for
// unknown values. We deliberately don't error on unknown — operators may
// run custom labels ("staging-eu", "preview"), and our handling shouldn't
// differ from "development" anyway.
func parseEnv(s string) Env {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "production", "prod":
		return EnvProduction
	case "staging", "stage":
		return EnvStaging
	case "test", "testing":
		return EnvTest
	default:
		return EnvDevelopment
	}
}

// validateSecretEntropy returns an error if a secret looks dangerously
// short. We require >= 32 bytes after base64 decode (if the value parses
// as base64) OR >= 32 chars raw. This is conservative; the goal is to
// catch "secret=hunter2" in CI before it reaches production.
func validateSecretEntropy(key, value string) error {
	const minBytes = 32
	if decoded, err := base64.StdEncoding.DecodeString(value); err == nil && len(decoded) >= minBytes {
		return nil
	}
	if decoded, err := base64.RawURLEncoding.DecodeString(value); err == nil && len(decoded) >= minBytes {
		return nil
	}
	if len(value) >= minBytes {
		return nil
	}
	return fmt.Errorf("env var %s: secret must be at least %d bytes (after base64 decode), got %d", key, minBytes, len(value))
}

// joinErrs combines multiple errors into one. Uses errors.Join (Go 1.20+).
func joinErrs(errs []error) error {
	return errors.Join(errs...)
}
