package secrets

import (
	"fmt"
	"os"
)

// EnvStore reads secrets from the process environment. It's the default
// for local development and CI where the orchestrator already injects
// values via env vars (docker-compose, GitHub Actions, direnv).
//
// EnvStore deliberately does *not* cache: os.Getenv is already a cheap
// in-process lookup, and not caching means tests that mutate the env
// (via t.Setenv) see fresh values without an explicit reload step.
//
// EnvStore is safe for concurrent use.
type EnvStore struct {
	// lookup is the function used to read values. nil means use os.LookupEnv.
	// Exposed for tests via NewEnvStoreWithLookup; production code uses the
	// zero value.
	lookup func(key string) (string, bool)
}

// NewEnvStore returns an EnvStore backed by os.LookupEnv.
func NewEnvStore() *EnvStore { return &EnvStore{} }

// newEnvStoreWithLookup is an internal helper for tests that want to
// stub the environment without mutating os.Environ. It is package-private
// because production callers should use NewEnvStore.
func newEnvStoreWithLookup(fn func(key string) (string, bool)) *EnvStore {
	return &EnvStore{lookup: fn}
}

// Get returns the value of the named environment variable. If the variable
// is unset or set to the empty string, Get returns ErrNotFound. The empty
// string is treated as absent because docker-compose's `${FOO:-}` syntax
// produces empty strings for unset variables; silently treating those as
// real values is the classic "production booted with no pepper" bug.
func (s *EnvStore) Get(key string) (string, error) {
	lookup := s.lookup
	if lookup == nil {
		lookup = os.LookupEnv
	}
	v, ok := lookup(key)
	if !ok || v == "" {
		// Wrap with the key — never the value — so callers can identify
		// which variable was missing without searching upstream logs.
		return "", fmt.Errorf("env %q: %w", key, ErrNotFound)
	}
	return v, nil
}

// MustGet returns the value or panics with a redacted message.
func (s *EnvStore) MustGet(key string) string { return mustGet(s, key) }
