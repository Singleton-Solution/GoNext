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

	// RUM controls the in-house Real User Monitoring subsystem (issue
	// #132). When Enabled is false (the default), the public theme
	// will not emit beacon scripts and no rum_events rows are written
	// — the table sits empty until an operator opts in.
	RUM RUMConfig

	// Email selects and configures the outbound mail transport for
	// transactional flows (password reset, email verification,
	// welcome, comment notifications). See [EmailConfig] for the
	// provider/SMTP/credential matrix.
	Email EmailConfig

	// PublicSite groups settings for the public-facing Next.js
	// renderer (apps/web): the canonical base URL stamped into
	// sitemap entries / Atom feed self-links, and whether search
	// engines are allowed to index the deployment. The renderer
	// fetches these values from the API at boot so a single source
	// of truth lives here on the Go side.
	PublicSite PublicSiteConfig
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

// RUMConfig configures the in-house Real User Monitoring subsystem.
//
// The beacon endpoint (POST /_/rum/beacon) is always mounted; the
// per-visitor beacon library only emits payloads when Enabled is true.
// This means an operator who wants to A/B "is RUM costing us
// anything" can flip the flag at runtime without re-deploying — the
// public theme respects the flag on every render.
//
// SampleRate, when < 1.0, lets the public theme drop a fraction of
// visitors at the browser level so a busy site doesn't write every
// pageview's Core Web Vitals to Postgres. The default is 1.0 (every
// visitor).
type RUMConfig struct {
	// Enabled toggles whether the public theme emits beacon scripts.
	// Default false. Honors GONEXT_RUM_ENABLED.
	Enabled bool

	// SampleRate is the per-visitor sample probability in [0, 1].
	// Default 1.0. Honors GONEXT_RUM_SAMPLE_RATE.
	SampleRate float64
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

// EmailConfig configures the outbound mailer.
//
// Provider selects which packages/go/email.Sender the chassis wires
// in:
//
//   - "smtp" — the production [email.SMTPSender] backed by Host/Port
//     and the AuthMech-selected SASL variant. Required for any
//     real-world deployment.
//   - "noop" — [email.NoopSender]; sends are recorded in memory but
//     never go on the wire. Default in tests and the recommended
//     starting state in local development.
//   - "log"  — [email.LogSender]; renders messages to slog. Useful for
//     "what would have been sent" debugging without standing up
//     MailHog.
//
// All credential fields are tagged `redact:"true"` so config dumps
// reveal length+hash only — see packages/go/config/dump.go for the
// masking format. From is intentionally NOT redacted because the
// envelope sender address is operational data an operator needs to
// see in a debug dump.
type EmailConfig struct {
	// Provider is the transport selector. Defaults to "noop" so a
	// freshly-bootstrapped deployment doesn't blast messages at users
	// before SMTP is set up. Honors GONEXT_EMAIL_PROVIDER.
	Provider string

	// Host is the SMTP server hostname. Required when Provider="smtp".
	// Honors GONEXT_EMAIL_HOST (falls back to GONEXT_SMTP_HOST for
	// backwards compatibility with the early-bootstrap env names).
	Host string

	// Port is the SMTP port. Defaults to 587 (submission with
	// STARTTLS). Honors GONEXT_EMAIL_PORT / GONEXT_SMTP_PORT.
	Port int

	// Username is the SASL identity. Empty disables auth entirely.
	// Honors GONEXT_EMAIL_USERNAME / GONEXT_SMTP_USER.
	Username string

	// Password is the SASL secret. Redacted in dumps. Honors
	// GONEXT_EMAIL_PASSWORD / GONEXT_SMTP_PASSWORD.
	Password string `redact:"true"`

	// From is the default envelope-sender address. Required when
	// Provider="smtp". Honors GONEXT_EMAIL_FROM / GONEXT_SMTP_FROM.
	From string

	// TLS selects implicit-TLS (port 465) when true. The default
	// false means STARTTLS on the configured Port (587), which is the
	// modern submission convention. Honors GONEXT_EMAIL_TLS.
	TLS bool

	// AuthMech is the SMTP AUTH variant: "plain" (default), "login",
	// or "crammd5". See packages/go/email.AuthMechanism for the trade
	// offs. Honors GONEXT_EMAIL_AUTH_MECH.
	AuthMech string

	// InsecureSkipVerify disables TLS certificate verification. DEV
	// ONLY — production deployments MUST leave this false. Honors
	// GONEXT_EMAIL_INSECURE_SKIP_VERIFY.
	InsecureSkipVerify bool

	// DialTimeout bounds the TCP+TLS handshake. Default 10 seconds.
	// Honors GONEXT_EMAIL_DIAL_TIMEOUT.
	DialTimeout time.Duration

	// BrandName is the "Site Name" stamped into every templated
	// message ("Welcome to <BrandName>"). Falls back to "GoNext" at
	// render time. Honors GONEXT_EMAIL_BRAND_NAME.
	BrandName string

	// BrandColor is the CSS color literal used in HTML template
	// headers and CTAs. Falls back to "#2563eb" at render time.
	// Honors GONEXT_EMAIL_BRAND_COLOR.
	BrandColor string

	// SiteURL is the canonical home URL used in template footers and
	// the welcome "sign in" button. Honors GONEXT_EMAIL_SITE_URL.
	SiteURL string

	// SupportEmail is the user-facing escalation address printed in
	// password-reset bodies. Empty hides the "contact support" line.
	// Honors GONEXT_EMAIL_SUPPORT.
	SupportEmail string
}

// PublicSiteConfig configures the discoverability surfaces served by
// the public renderer (apps/web): sitemap.xml, Atom feeds, robots.txt.
//
// BaseURL is the canonical origin the renderer stamps into XML
// `<loc>` elements, Atom `<id>` / `<link rel="self">` URIs, and the
// `Sitemap:` line in robots.txt. It must be an absolute URL with no
// trailing slash — the renderer's builders concatenate paths directly.
//
// AllowIndex is the kill-switch for non-production hosts. When false,
// robots.txt emits `User-agent: *` + `Disallow: /` so staging and
// preview deployments don't accidentally rank for their public
// content. The default depends on Env:
//
//   - EnvProduction => AllowIndex defaults to true
//   - everything else (development, staging, test) => false
//
// Operators can override either direction via
// GONEXT_PUBLIC_SITE_ALLOW_INDEX.
type PublicSiteConfig struct {
	// BaseURL is the canonical origin (e.g. "https://example.com").
	// No trailing slash; the renderer joins paths verbatim. Empty
	// disables every absolute-URL surface (sitemap entries skip
	// `<loc>`, feed self-links degrade to a placeholder). Honors
	// GONEXT_PUBLIC_SITE_BASE_URL.
	BaseURL string

	// AllowIndex toggles whether robots.txt allows search engine
	// crawling. Defaults to (Env == EnvProduction). Honors
	// GONEXT_PUBLIC_SITE_ALLOW_INDEX.
	AllowIndex bool

	// NextRevalidateURL is the apps/web origin used for outbound ISR
	// cache-invalidation hooks. When a post or page is published, the
	// REST handler POSTs to
	// {NextRevalidateURL}/api/revalidate?path=...&secret=...
	// so the Next.js side can clear its incremental-static-regeneration
	// cache without waiting for the next revalidate interval.
	//
	// Empty disables the hook entirely — useful when the API is
	// deployed without apps/web (a JSON-only deployment) or when the
	// renderer is served from a static host that doesn't speak ISR.
	// Honors GONEXT_NEXT_REVALIDATE_URL.
	NextRevalidateURL string

	// NextRevalidateSecret is the shared token the Next.js
	// /api/revalidate route handler validates before clearing its
	// cache. The chassis sends this as the `secret` query parameter
	// on the outbound POST.
	//
	// Empty disables the hook (same shape as an empty
	// NextRevalidateURL). Honors GONEXT_NEXT_REVALIDATE_SECRET.
	NextRevalidateSecret string
}
