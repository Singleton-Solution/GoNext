package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// envSource abstracts os.Environ for tests. Production uses osEnv;
// tests pass mapEnv to inject a known fixture without mutating
// the real environment.
type envSource interface {
	get(key string) (value string, ok bool)
}

type osEnv struct{}

func (osEnv) get(key string) (string, bool) { return os.LookupEnv(key) }

type mapEnv map[string]string

func (m mapEnv) get(key string) (string, bool) {
	v, ok := m[key]
	return v, ok
}

// getString returns the env value if set and non-empty, otherwise def.
// Empty-string env vars are treated as unset (common when using docker-compose
// templates with `${FOO:-}` substitution).
func getString(e envSource, key, def string) string {
	if v, ok := e.get(key); ok && v != "" {
		return v
	}
	return def
}

// getStringRequired returns the env value or an error if missing/empty.
// Used for secrets and DATABASE_URL.
func getStringRequired(e envSource, key string) (string, error) {
	v, ok := e.get(key)
	if !ok || v == "" {
		return "", fmt.Errorf("required env var %s is missing or empty", key)
	}
	return v, nil
}

// getInt parses an int env value or returns def. On parse error, returns
// an error rather than silently using def — silent default-on-error is the
// classic config bug. The caller decides whether to log or fail.
func getInt(e envSource, key string, def int) (int, error) {
	v, ok := e.get(key)
	if !ok || v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("env var %s: %q is not an integer", key, v)
	}
	return n, nil
}

// getBool parses a bool env value or returns def. Accepts the standard
// strconv.ParseBool set: 1/0, t/f, true/false, TRUE/FALSE, etc.
func getBool(e envSource, key string, def bool) (bool, error) {
	v, ok := e.get(key)
	if !ok || v == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("env var %s: %q is not a boolean", key, v)
	}
	return b, nil
}

// getFloat parses a float env value or returns def. Used for RUM
// sample rate and similar [0,1] probabilities; the bounds check is
// left to the caller because the right bounds are domain-specific.
func getFloat(e envSource, key string, def float64) (float64, error) {
	v, ok := e.get(key)
	if !ok || v == "" {
		return def, nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, fmt.Errorf("env var %s: %q is not a float", key, v)
	}
	return f, nil
}

// getDuration parses a duration env value or returns def. Accepts
// time.ParseDuration syntax: "30s", "5m", "1h30m".
func getDuration(e envSource, key string, def time.Duration) (time.Duration, error) {
	v, ok := e.get(key)
	if !ok || v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("env var %s: %q is not a duration", key, v)
	}
	return d, nil
}

// getCSV returns a comma-separated env value as a trimmed []string,
// dropping empty segments. Returns def if unset.
func getCSV(e envSource, key string, def []string) []string {
	v, ok := e.get(key)
	if !ok || v == "" {
		return def
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return def
	}
	return out
}
