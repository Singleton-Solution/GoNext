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

	// Plugins controls the plugin subsystem. The host-side dev install
	// endpoint (POST /_/plugins/dev/install) is gated on Plugins.DevMode:
	// when false (the production default), the route is not registered
	// at all so prod can never expose it. When true, requests carrying a
	// matching Plugins.DevToken can hot-install plugins for `gonext
	// plugin dev` loops.
	Plugins PluginsConfig

	// Performance groups performance-related toggles. Most fields here
	// are pure optimizations that can be disabled without affecting
	// correctness — useful when a feature interacts badly with an
	// upstream proxy or when an operator is bisecting a regression.
	Performance PerformanceConfig
}

// PerformanceConfig groups opt-out toggles for the performance
// optimizations the server applies. Each field maps to one specific
// optimization; the defaults represent the production-recommended
// shape. Disable a field only after measuring that it's the cause of
// a regression for your deployment.
type PerformanceConfig struct {
	// EarlyHints enables the HTTP 103 Early Hints middleware (see
	// packages/go/middleware/earlyhints). When true (default), the
	// server flushes a 103 interim response carrying Link: rel=preload
	// headers for the active theme's stylesheet (and any registered
	// extras) before the real 200 is rendered. Browsers (Chrome 103+,
	// Firefox 110+, Safari 17+) start fetching those assets while
	// the server is still working, typically winning 50-200ms of
	// Largest Contentful Paint on theme-rendered pages.
	//
	// Disable if:
	//   - A reverse proxy in front of GoNext drops 1xx interim
	//     responses (some legacy load balancers do — modern nginx,
	//     HAProxy, Cloudflare, AWS ALB all forward them correctly).
	//   - You are debugging an issue and want to isolate the
	//     baseline 200-only behavior.
	//
	// Honors GONEXT_PERFORMANCE_EARLY_HINTS. Default true.
	EarlyHints bool
}

// PluginsConfig configures the plugin subsystem.
//
// DevMode and DevToken together gate the apps/api dev-install endpoint.
// DevMode defaults to false: production deployments do not have to do
// anything to keep the endpoint disabled. DevToken is REDACTED in dumps
// — it functions as the shared secret between the `gonext plugin dev`
// CLI and the host, so leaking it lets anyone hot-reload code into the
// process.
type PluginsConfig struct {
	// DevMode toggles registration of /_/plugins/dev/install. Default
	// false. Set true only on developer workstations / staging hosts
	// where you actively run the `gonext plugin dev` watch loop.
	// Honors GONEXT_PLUGINS_DEV_MODE.
	DevMode bool

	// DevToken is the shared secret the dev-install handler compares
	// the request's Dev-Token header against. Empty + DevMode=true is
	// treated as "no token accepted" (the handler rejects every request
	// with 401) so a misconfigured dev box cannot accidentally accept
	// uploads from arbitrary network peers. Honors
	// GONEXT_PLUGINS_DEV_TOKEN.
	DevToken string `redact:"true"`
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
	// Contains the database password embedded in the DSN; redacted in dumps.
	URL string `redact:"true"`

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
// May embed a password (redis://:pw@host:port); redacted in dumps.
type RedisConfig struct {
	URL          string `redact:"true"`
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
	AccessKey string `redact:"true"`
	SecretKey string `redact:"true"`
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
	Pepper string `redact:"true"`

	// SessionSecret signs session cookies. Required. Honors
	// GONEXT_AUTH_SESSION_SECRET.
	SessionSecret string `redact:"true"`

	// CSRFSecret signs anti-CSRF tokens (public-form variant). Required.
	// Honors GONEXT_AUTH_CSRF_SECRET.
	CSRFSecret string `redact:"true"`

	// SessionTTL is the cookie lifetime. Default 30 days.
	SessionTTL time.Duration

	// SessionIdleTTL is the idle expiration. Default 7 days.
	SessionIdleTTL time.Duration
}
