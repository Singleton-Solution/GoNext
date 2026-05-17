// Package config is the single source of truth for runtime configuration.
//
// Twelve-factor config: everything comes from the environment. No config
// files, no flags. Loaded once at process start; passed by pointer to every
// component that needs it. Tests use Load(WithEnv(map[string]string{...}))
// to supply fixtures without mutating os.Environ.
//
// Naming conventions:
//
//   - Industry-standard names where they exist: DATABASE_URL, REDIS_URL,
//     PORT, AWS_REGION. Heroku/Railway/Fly users feel at home.
//
//   - GoNext-specific keys are prefixed GONEXT_, sub-grouped by area:
//     GONEXT_AUTH_PEPPER, GONEXT_AUTH_SESSION_SECRET,
//     GONEXT_LOG_LEVEL, GONEXT_LOG_FORMAT.
//
// Required vs optional:
//
//   - Secrets and the database URL are required. Missing => Load returns
//     an error and the process should exit before serving traffic.
//   - Everything else has a sensible default.
//
// See docs/00-architecture-overview.md §2 and §4, and docs/13-security-baseline.md §5.
// The full env-var surface is also documented in .env.example at the repo root.
package config
