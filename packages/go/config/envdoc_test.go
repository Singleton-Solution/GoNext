package config

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// envExampleAllKeys is the authoritative list of every key the loader
// reads. When you add a new env var to load.go you MUST also add it
// here AND to the root .env.example. The two failing-test arms
// (orphan-in-loader and orphan-in-example) keep the three sources in
// lock-step so an operator standing up a new deployment never hits
// "wait, what env var sets X?".
//
// Naming rule: include GONEXT_ keys plus the small set of industry-
// standard names the loader honors (DATABASE_URL, REDIS_URL, PORT,
// AWS_*). The .env.example test below accepts any key here as
// "documented", regardless of prefix.
var envExampleAllKeys = []string{
	// Environment
	"GONEXT_ENV",
	// HTTP server
	"GONEXT_SERVER_ADDR",
	"PORT",
	"GONEXT_SERVER_READ_HEADER_TIMEOUT",
	"GONEXT_SERVER_READ_TIMEOUT",
	"GONEXT_SERVER_WRITE_TIMEOUT",
	"GONEXT_SERVER_IDLE_TIMEOUT",
	"GONEXT_SERVER_SHUTDOWN_TIMEOUT",
	"GONEXT_SERVER_MAX_HEADER_BYTES",
	"GONEXT_TRUSTED_PROXIES",
	// Logging
	"GONEXT_LOG_LEVEL",
	"GONEXT_LOG_FORMAT",
	"GONEXT_LOG_ADDSRC",
	// Database
	"DATABASE_URL",
	"GONEXT_DB_MAX_OPEN_CONNS",
	"GONEXT_DB_MAX_IDLE_CONNS",
	"GONEXT_DB_CONN_MAX_LIFETIME",
	"GONEXT_DB_CONN_MAX_IDLE_TIME",
	"GONEXT_DB_STATEMENT_TIMEOUT",
	"GONEXT_MIGRATION_DIR",
	// Redis
	"REDIS_URL",
	"GONEXT_REDIS_POOL_SIZE",
	"GONEXT_REDIS_MIN_IDLE_CONNS",
	"GONEXT_REDIS_DIAL_TIMEOUT",
	"GONEXT_REDIS_READ_TIMEOUT",
	"GONEXT_REDIS_WRITE_TIMEOUT",
	// Storage
	"AWS_ENDPOINT_URL",
	"AWS_REGION",
	"GONEXT_S3_BUCKET",
	"AWS_ACCESS_KEY_ID",
	"AWS_SECRET_ACCESS_KEY",
	"GONEXT_S3_USE_SSL",
	"GONEXT_S3_PATH_STYLE",
	// Auth
	"GONEXT_AUTH_PEPPER",
	"GONEXT_AUTH_SESSION_SECRET",
	"GONEXT_AUTH_CSRF_SECRET",
	"GONEXT_AUTH_SESSION_TTL",
	"GONEXT_AUTH_SESSION_IDLE_TTL",
	// Plugins
	"GONEXT_PLUGINS_DEV_MODE",
	"GONEXT_PLUGINS_DEV_TOKEN",
	// Performance
	"GONEXT_PERFORMANCE_EARLY_HINTS",
	// RUM
	"GONEXT_RUM_ENABLED",
	"GONEXT_RUM_SAMPLE_RATE",
	// Email
	"GONEXT_EMAIL_PROVIDER",
	"GONEXT_EMAIL_HOST",
	"GONEXT_SMTP_HOST",
	"GONEXT_EMAIL_PORT",
	"GONEXT_SMTP_PORT",
	"GONEXT_EMAIL_USERNAME",
	"GONEXT_SMTP_USER",
	"GONEXT_EMAIL_PASSWORD",
	"GONEXT_SMTP_PASSWORD",
	"GONEXT_EMAIL_FROM",
	"GONEXT_SMTP_FROM",
	"GONEXT_EMAIL_TLS",
	"GONEXT_EMAIL_AUTH_MECH",
	"GONEXT_EMAIL_INSECURE_SKIP_VERIFY",
	"GONEXT_EMAIL_DIAL_TIMEOUT",
	"GONEXT_EMAIL_BRAND_NAME",
	"GONEXT_EMAIL_BRAND_COLOR",
	"GONEXT_EMAIL_SITE_URL",
	"GONEXT_EMAIL_SUPPORT",
	// PublicSite
	"GONEXT_PUBLIC_SITE_BASE_URL",
	"GONEXT_PUBLIC_SITE_ALLOW_INDEX",
}

// findRepoRoot walks upward from this test file's directory until it
// finds .env.example. Tests run from the package dir, but the doc
// lives at the repo root.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed; cannot locate test file")
	}
	dir := filepath.Dir(thisFile)
	for i := 0; i < 16; i++ {
		if _, err := os.Stat(filepath.Join(dir, ".env.example")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("could not locate .env.example walking up from %s", filepath.Dir(thisFile))
	return ""
}

// parseEnvExample reads the .env.example file and extracts every key.
// Lines starting with '#' are comments; otherwise the first '=' splits
// KEY from value. A leading "# KEY=val" is treated as a documented
// (commented-out) example: the operator opts in by uncommenting it, and
// the key is therefore "documented" from the test's perspective.
func parseEnvExample(t *testing.T, path string) map[string]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open .env.example: %v", err)
	}
	defer f.Close()

	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		raw := strings.TrimSpace(sc.Text())
		if raw == "" {
			continue
		}
		// A "# KEY=val" line is a documented optional. Strip the
		// leading '#' (and any whitespace after it) and try to parse
		// the remainder as KEY=val. A bare prose comment like
		// "# Connection pool ..." will not contain '=' before any
		// reasonable comment terminator and we ignore those.
		line := raw
		commented := false
		if strings.HasPrefix(line, "#") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "#"))
			commented = true
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		// A real env var key is uppercase letters/digits/underscores,
		// nothing else. This filters out things like
		// "# Format: postgres://user:password@host:port" which would
		// otherwise look key-shaped.
		if !looksLikeEnvKey(key) {
			continue
		}
		val := strings.TrimSpace(line[eq+1:])
		// If a commented-out line redeclares an already-uncommented
		// one (e.g. PORT shown both ways), the uncommented value wins.
		if _, exists := out[key]; exists && commented {
			continue
		}
		out[key] = val
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan .env.example: %v", err)
	}
	return out
}

// looksLikeEnvKey filters scanner hits to actual env-var keys. Real
// env keys are uppercase letters, digits, and underscores, with at
// least one letter. Prose like "Format: postgres" is keyed on the
// colon-prefixed "Format" — which is not all-caps — so this filter
// excludes it.
func looksLikeEnvKey(s string) bool {
	if s == "" {
		return false
	}
	letterSeen := false
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z':
			letterSeen = true
		case r >= '0' && r <= '9':
		case r == '_':
		default:
			return false
		}
	}
	return letterSeen
}

func TestEnvExample_NoOrphansInLoader(t *testing.T) {
	// Every key in .env.example must correspond to a real env var the
	// loader reads. Stops a contributor from leaving a stale entry
	// after deleting a feature.
	root := findRepoRoot(t)
	got := parseEnvExample(t, filepath.Join(root, ".env.example"))

	known := map[string]struct{}{}
	for _, k := range envExampleAllKeys {
		known[k] = struct{}{}
	}

	var orphans []string
	for k := range got {
		if _, ok := known[k]; !ok {
			orphans = append(orphans, k)
		}
	}
	if len(orphans) > 0 {
		sort.Strings(orphans)
		t.Errorf(".env.example contains %d key(s) the loader does not read: %v\n"+
			"Either remove them from .env.example or add them to envExampleAllKeys (and the loader).",
			len(orphans), orphans)
	}
}

func TestEnvExample_DocumentsEveryLoaderKey(t *testing.T) {
	// Every env var the loader reads must be documented somewhere in
	// .env.example. Stops a contributor from adding a new knob without
	// telling operators it exists.
	root := findRepoRoot(t)
	got := parseEnvExample(t, filepath.Join(root, ".env.example"))

	var missing []string
	for _, k := range envExampleAllKeys {
		if _, ok := got[k]; !ok {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf(".env.example is missing documentation for %d env var(s) the loader reads: %v\n"+
			"Add a comment + KEY=value (or commented-out # KEY=value) entry for each.",
			len(missing), missing)
	}
}

func TestEnvExample_LoadsSuccessfullyAsFixture(t *testing.T) {
	// Confidence check: a fixture built from the uncommented entries
	// in .env.example should load without error, modulo the obviously
	//-dev-only auth secrets which the file marks as "change-me".
	// This protects against typos in keys that ARE in
	// envExampleAllKeys (so the lists agree) but are spelled wrong in
	// the file. If a key is wrong, Load() will not see the value and
	// either falls back silently (most cases) or — for secrets — fails
	// the entropy check, which is what we leverage here.
	root := findRepoRoot(t)
	got := parseEnvExample(t, filepath.Join(root, ".env.example"))

	// Override the three placeholder secrets with valid 32-byte values
	// so the entropy check passes; everything else uses the
	// .env.example value verbatim.
	got["GONEXT_AUTH_PEPPER"] = strings.Repeat("a", 32)
	got["GONEXT_AUTH_SESSION_SECRET"] = strings.Repeat("b", 32)
	got["GONEXT_AUTH_CSRF_SECRET"] = strings.Repeat("c", 32)

	cfg, err := Load(WithEnv(got))
	if err != nil {
		t.Fatalf("Load(.env.example fixture): %v", err)
	}
	if cfg.Env == "" {
		t.Error("Load returned cfg with empty Env")
	}
}
