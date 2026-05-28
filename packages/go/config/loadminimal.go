package config

import "time"

// LoadMinimal builds a Config carrying only the fields a CLI subcommand
// typically needs: Database, Redis, and Env. The auth secrets
// (GONEXT_AUTH_PEPPER / GONEXT_AUTH_SESSION_SECRET / GONEXT_AUTH_CSRF_SECRET)
// are intentionally NOT required — short-lived CLI invocations
// (`gonext audit verify`, `gonext jobs drain`, etc.) don't construct
// auth subsystems and should not refuse to boot when an operator runs
// them from a shell that lacks the production secrets.
//
// Production binaries (apps/api, apps/worker) must continue to call
// Load(), which enforces the full env surface.
//
// On error the returned *Config is partial; callers should treat it
// the same way as Load()'s partial return — useful for diagnostics, not
// for running.
func LoadMinimal(opts ...LoadOption) (*Config, error) {
	lc := loadConfig{env: osEnv{}}
	for _, o := range opts {
		o(&lc)
	}
	e := lc.env

	cfg := &Config{}
	var errs []error

	cfg.Env = parseEnv(getString(e, "GONEXT_ENV", "development"))

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

	if len(errs) > 0 {
		return cfg, joinErrs(errs)
	}
	return cfg, nil
}
