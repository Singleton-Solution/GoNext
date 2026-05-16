package config

import "time"

// Env names the deployment environment. Used for runtime guards
// ("are we in production? then refuse Redact=false") and for downstream
// services that want to be told.
type Env string

const (
	EnvDevelopment Env = "development"
	EnvStaging     Env = "staging"
	EnvProduction  Env = "production"
	EnvTest        Env = "test"
)

// Config is the root configuration object. One instance per process,
// loaded at boot.
type Config struct {
	// Env is the deployment environment. Defaults to "development".
	// Honors GONEXT_ENV.
	Env Env

	// Server is the HTTP listener config for apps/api.
	Server ServerConfig

	// Log controls the structured logger (packages/go/log).
	Log LogConfig

	// Database is the primary Postgres connection.
	Database DatabaseConfig

	// Redis is the cache + session + queue store.
	Redis RedisConfig

	// Storage is the S3-compatible media backend.
	Storage StorageConfig

	// Auth holds secrets and timings for sessions, CSRF, and password hashing.
	Auth AuthConfig
}

// ServerConfig configures the HTTP server in apps/api.
//
// Addr accepts "host:port" or ":port" (binds all interfaces). PORT (no
// host) is honored as a shorthand for ":<PORT>" so Heroku/Railway/Fly
// deploys work without re-config.
type ServerConfig struct {
	// Addr is the listen address. Default ":8080".
	Addr string

	// Timeouts. Defaults: read 15s, write 30s, idle 60s, shutdown 30s.
	// Read covers request headers + body; write covers response generation.
	// Shutdown is the drain window when SIGTERM arrives.
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration

	// MaxHeaderBytes guards against header smuggling and slowloris-shaped
	// memory abuse. Default 1 MiB.
	MaxHeaderBytes int

	// TrustedProxies is the comma-separated list of CIDRs allowed to set
	// X-Forwarded-For, X-Forwarded-Proto, etc. Empty = trust no upstream.
	// Honors GONEXT_TRUSTED_PROXIES; populated by the X-Forwarded-* middleware.
	TrustedProxies []string
}

// LogConfig mirrors the logger options. Read in packages/go/log via
// OptionsFromEnv() — this struct exists so that config-flow review can
// see logging as a first-class concern and tests can override it.
type LogConfig struct {
	Level     string // DEBUG | INFO | WARN | ERROR
	Format    string // json | text
	AddSource bool
}

// DatabaseConfig is the Postgres connection.
//
// URL is the libpq-style DSN: postgres://user:pass@host:port/db?sslmode=...
// This is the only field that is REQUIRED (and the only one without a default).
type DatabaseConfig struct {
	// URL is required. Format: postgres://user:pass@host:port/db
	URL string

	// Connection pool. Defaults: Max=25, MaxIdle=5, lifetimes per pgx best-practice.
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration

	// StatementTimeout sets the statement_timeout for every connection.
	// Default 30s. Per-query overrides via context still work.
	StatementTimeout time.Duration

	// MigrationDir is where golang-migrate looks for .sql files.
	// Default "./migrations" (repo-relative). In Docker images it should
	// point to the embedded path. Honors GONEXT_MIGRATION_DIR.
	MigrationDir string
}

// RedisConfig is the Redis connection.
//
// URL is required for production. In dev, defaults to redis://localhost:6379/0.
type RedisConfig struct {
	URL          string
	PoolSize     int
	MinIdleConns int
	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

// StorageConfig is the S3-compatible object store for media.
//
// Endpoint is optional — empty means AWS S3 default endpoint resolution.
// For MinIO/R2/Backblaze/etc., set the endpoint URL.
type StorageConfig struct {
	Endpoint  string // empty = AWS default; set for MinIO/R2/etc.
	Region    string
	Bucket    string
	AccessKey string
	SecretKey string
	UseSSL    bool
	PathStyle bool // required for MinIO; off for AWS
}

// AuthConfig holds the secrets and timings for authentication.
//
// All three secrets (Pepper, SessionSecret, CSRFSecret) are required and
// have no defaults. Boot fails fast if any is missing. They must be ≥32
// bytes after base64-decoding (entropy check), per docs/13-security-baseline.md §5.
type AuthConfig struct {
	// Pepper is the secret HMAC'd into the password-hash input. Rotation
	// is supported in packages/go/auth/password — see that package.
	// Required. Honors GONEXT_AUTH_PEPPER.
	Pepper string

	// SessionSecret signs session cookies. Required. Honors
	// GONEXT_AUTH_SESSION_SECRET.
	SessionSecret string

	// CSRFSecret signs anti-CSRF tokens (public-form variant). Required.
	// Honors GONEXT_AUTH_CSRF_SECRET.
	CSRFSecret string

	// SessionTTL is the cookie lifetime. Default 30 days.
	SessionTTL time.Duration

	// SessionIdleTTL is the idle expiration. Default 7 days.
	SessionIdleTTL time.Duration
}
